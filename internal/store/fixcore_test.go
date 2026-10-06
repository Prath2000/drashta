package store

// Regression tests for the Wave 3 fix wave (fix-core). Each test names the finding it covers.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

type fcDay struct{ commits, plus, minus, sessions, sessMin int }

func fcRow(t *testing.T, db *DB, project, day string) (fcDay, bool) {
	t.Helper()
	var r fcDay
	err := db.Read().QueryRow(`SELECT commits, lines_plus, lines_minus, sessions, session_minutes
		FROM daily_activity WHERE project_id = ? AND day = ?`, project, day).
		Scan(&r.commits, &r.plus, &r.minus, &r.sessions, &r.sessMin)
	if err == sql.ErrNoRows {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

// C1: daily_activity is history. A Reindex (commits capped, daily_commits dropped) and
// deleted .jsonl files (sessions rows gone) must not change an old day's row.
func TestFixC1ReindexAndPrunedSessionsKeepOldDay(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")
	old := fmtDay(2)

	// Old day: 3 commits (exact numbers from daily_commits) and 2 sessions.
	if err := db.ReplaceCommits(ctx, "alpha", []probe.Commit{
		{ID: "o1", Time: at(old, 9, 0), Plus: 1},
		{ID: "o2", Time: at(old, 10, 0), Plus: 1},
		{ID: "n1", Time: at(fmtDay(20), 10, 0), Plus: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetDailyCommitStats(ctx, "alpha", old, old,
		map[string]probe.DayCount{old: {Commits: 3, Plus: 30, Minus: 3}}, testLoc); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSessions(ctx, []Session{
		{ID: "s1", ProjectID: "alpha", Start: at(old, 9, 0), End: at(old, 10, 0)},
		{ID: "s2", ProjectID: "alpha", Start: at(old, 11, 0), End: at(old, 11, 30)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecomputeDaily(ctx, "alpha", old, testLoc); err != nil {
		t.Fatal(err)
	}
	before, ok := fcRow(t, db, "alpha", old)
	if !ok || before != (fcDay{3, 30, 3, 2, 90}) {
		t.Fatalf("before = %+v (%v)", before, ok)
	}

	// Reindex: commits, sessions and daily_commits are dropped (R tables).
	if err := db.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	seedProject(t, db, "alpha")
	// The sweep refills only the newest commits (the old day fell off the cap).
	if err := db.ReplaceCommits(ctx, "alpha", []probe.Commit{{ID: "n1", Time: at(fmtDay(20), 10, 0), Plus: 1}}); err != nil {
		t.Fatal(err)
	}
	// Some job recomputes the old day (for example a task created that day).
	if err := db.RecomputeDaily(ctx, "alpha", old, testLoc); err != nil {
		t.Fatal(err)
	}
	if got, ok := fcRow(t, db, "alpha", old); !ok || got != before {
		t.Fatalf("after reindex: %+v (row %v), want %+v", got, ok, before)
	}

	// One transcript is parsed again, the other one is gone from disk for good.
	if err := db.UpsertSessions(ctx, []Session{{ID: "s1", ProjectID: "alpha", Start: at(old, 9, 0), End: at(old, 10, 0)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecomputeDaily(ctx, "alpha", old, testLoc); err != nil {
		t.Fatal(err)
	}
	if got, _ := fcRow(t, db, "alpha", old); got != before {
		t.Fatalf("after pruned jsonl: %+v, want %+v", got, before)
	}

	// Columns from complete sources still move: a commit day inside the exact window.
	if _, err := db.SetDailyCommitStats(ctx, "alpha", old, old,
		map[string]probe.DayCount{old: {Commits: 1, Plus: 2, Minus: 0}}, testLoc); err != nil {
		t.Fatal(err)
	}
	if got, _ := fcRow(t, db, "alpha", old); got.commits != 1 || got.plus != 2 || got.sessions != 2 {
		t.Fatalf("exact numbers must win: %+v", got)
	}
}

// I1: zones whose clocks change at midnight. No day loop may hang, no day may repeat or vanish.
func TestFixI1MidnightDSTZones(t *testing.T) {
	for _, tc := range []struct {
		zone     string
		from, to string
	}{
		{"America/Santiago", "2026-09-04", "2026-09-08"}, // 00:00 on Sep 6 does not exist
		{"America/Santiago", "2026-04-03", "2026-04-07"}, // clocks go back
		{"Asia/Kolkata", "2026-09-04", "2026-09-08"},
		{"Pacific/Auckland", "2026-09-25", "2026-09-29"}, // DST starts Sep 27 at 02:00
		{"America/Havana", "2026-03-06", "2026-03-10"},
	} {
		t.Run(tc.zone+"_"+tc.from, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Skip("no tz data:", err)
			}
			db := openTest(t)
			seedProject(t, db, "p")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			changed, err := db.SetDailyCommitStats(ctx, "p", tc.from, tc.to, nil, loc)
			if err != nil {
				t.Fatalf("SetDailyCommitStats: %v (a loop that never ends times out here)", err)
			}
			if len(changed) != 5 {
				t.Fatalf("changed = %v, want 5 days", changed)
			}
			for n := 0; n < 5; n++ {
				day := mustAddDays(tc.from, n)
				s, e, err := dayBounds(day, loc)
				if err != nil {
					t.Fatal(err)
				}
				if got := time.Unix(s, 0).In(loc).Format(DayLayout); got != day {
					t.Errorf("start of %s is on %s", day, got)
				}
				if got := time.Unix(s-1, 0).In(loc).Format(DayLayout); got == day {
					t.Errorf("start of %s is not the first instant", day)
				}
				if got := time.Unix(e-1, 0).In(loc).Format(DayLayout); got != day {
					t.Errorf("end of %s: last second is on %s", day, got)
				}
			}

			// Home over the change: unique, consecutive days.
			useClock(t, time.Date(2026, 10, 1, 12, 0, 0, 0, loc))
			if tc.from < "2026-07-01" {
				d, _ := time.ParseInLocation(DayLayout, tc.to, loc)
				useClock(t, d.Add(12*time.Hour))
			}
			h, err := db.Home(ctx, 60, loc)
			if err != nil {
				t.Fatal(err)
			}
			for n := 1; n < len(h.Days); n++ {
				if want := mustAddDays(h.Days[n-1], 1); h.Days[n] != want {
					t.Fatalf("Home days: %s then %s (want %s)", h.Days[n-1], h.Days[n], want)
				}
			}
		})
	}
}

// I2: a git+fossil project where fossil's oldest commit is older than git's. A git commit
// that falls off git's cap must survive, and fossil's must too.
func TestFixI2CutOffPerVCS(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "p")
	g := func(id string, day int) probe.Commit {
		return probe.Commit{ID: id, VCS: "git", Time: at(fmtDay(day), 10, 0)}
	}
	f := func(id string, day int) probe.Commit {
		return probe.Commit{ID: id, VCS: "fossil", Time: at(fmtDay(day), 10, 0)}
	}
	if err := db.ReplaceCommits(ctx, "p", []probe.Commit{g("g3", 13), g("g2", 12), g("g1", 11), f("f1", 1)}); err != nil {
		t.Fatal(err)
	}
	// g1 falls off git's cap; fossil is unchanged.
	if err := db.ReplaceCommits(ctx, "p", []probe.Commit{g("g4", 14), g("g3", 13), g("g2", 12), f("f1", 1)}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM commits WHERE project_id = 'p' AND id = 'g1'`); n != 1 {
		t.Fatalf("g1 rows after the second sweep = %d, want 1", n)
	}
	// A real rewrite inside git's window is still dropped.
	if err := db.ReplaceCommits(ctx, "p", []probe.Commit{g("g4b", 14), g("g3", 13), g("g2", 12), f("f1", 1)}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM commits WHERE project_id = 'p' AND id = 'g4'`); n != 0 {
		t.Fatalf("rewritten g4 still stored")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM commits WHERE project_id = 'p'`); n != 5 {
		t.Fatalf("rows = %d, want 5 (g4b g3 g2 g1 f1)", n)
	}
}

// M1: commits with the same time as the oldest given commit survive when the cap falls between them.
func TestFixM1EqualTimestampsSurvive(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "p")
	same := at(fmtDay(5), 10, 0)
	c := func(id string, tm time.Time) probe.Commit { return probe.Commit{ID: id, Time: tm} }
	if err := db.ReplaceCommits(ctx, "p", []probe.Commit{c("b", same.Add(time.Hour)), c("a1", same), c("a2", same)}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCommits(ctx, "p", []probe.Commit{c("c", same.Add(2*time.Hour)), c("b", same.Add(time.Hour)), c("a1", same)}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM commits WHERE id = 'a2'`); n != 1 {
		t.Fatalf("a2 was deleted")
	}
}

// I10: one definition of the counts, the Tracker's: a done task with a pending snapshot is
// not awaiting review, a review task without a snapshot is, and hidden projects are left out.
func TestFixI10CountsMatchTracker(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	ahProj(t, db, "alpha", "g", false, true)
	ahProj(t, db, "hid", "g", true, true)
	for _, task := range []Task{
		{ProjectID: "alpha", ID: "T-1", Title: "done, pending snap", Status: "done"},
		{ProjectID: "alpha", ID: "T-2", Title: "review, no snap", Status: "review"},
		{ProjectID: "alpha", ID: "T-3", Title: "todo, pending snap", Status: "todo"},
		{ProjectID: "alpha", ID: "T-4", Title: "todo", Status: "todo"},
		{ProjectID: "hid", ID: "T-1", Title: "hidden review", Status: "review"},
		{ProjectID: "hid", ID: "T-2", Title: "hidden todo", Status: "todo"},
	} {
		if err := db.UpsertAuthoredTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []Snapshot{
		{ProjectID: "alpha", TaskID: "T-1", Version: 1, Verdict: "pending"},
		{ProjectID: "alpha", TaskID: "T-3", Version: 1, Verdict: "pending"},
		{ProjectID: "alpha", TaskID: "T-9", Version: 1, Verdict: "pending"}, // task deleted
	} {
		if err := db.UpsertAuthoredSnapshot(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	c, err := db.HomeBootCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.AwaitingReview != 2 || c.OpenTasks != 3 {
		t.Fatalf("counts = %+v, want awaitingReview 2 (T-2, T-3), openTasks 3", c)
	}
	ic, err := db.IdxCounts(ctx)
	if err != nil || ic.AwaitingReview != c.AwaitingReview || ic.OpenTasks != c.OpenTasks {
		t.Fatalf("IdxCounts %+v differs from HomeBootCounts %+v (%v)", ic, c, err)
	}
}

// M-1: the data folder is 0700 (also an existing 0755 one) and the database files are 0600.
func TestFixM1PrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(bg(), "author", "x"); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode %v", fi.Mode().Perm())
	}
	for _, name := range []string{DBFileName, DBFileName + "-wal", DBFileName + "-shm", "settings.json"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) && name != DBFileName {
				continue
			}
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", name, fi.Mode().Perm())
		}
	}
}
