package store

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/Prath2000/drashta/internal/probe"
)

type dsDayRow struct{ commits, plus, minus int }

func dsRow(t *testing.T, db *DB, project, day string) dsDayRow {
	t.Helper()
	var r dsDayRow
	err := db.Read().QueryRow(`SELECT commits, lines_plus, lines_minus FROM daily_activity
		WHERE project_id = ? AND day = ?`, project, day).Scan(&r.commits, &r.plus, &r.minus)
	if err == sql.ErrNoRows {
		return dsDayRow{}
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSetDailyCommitStatsWinsOverCommitRows(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")
	// Only 2 commit rows on day 3 (the capped list) ...
	if err := db.ReplaceCommits(ctx, "alpha", []probe.Commit{
		{ID: "c1", Time: at(fmtDay(3), 10, 0), Plus: 1, Minus: 1},
		{ID: "c2", Time: at(fmtDay(3), 11, 0), Plus: 2},
		{ID: "c3", Time: at(fmtDay(9), 11, 0), Plus: 5},
	}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{fmtDay(3), fmtDay(9)} {
		if err := db.RecomputeDaily(ctx, "alpha", d, testLoc); err != nil {
			t.Fatal(err)
		}
	}
	if got := dsRow(t, db, "alpha", fmtDay(3)); got != (dsDayRow{2, 3, 1}) {
		t.Fatalf("before: %+v", got)
	}

	// ... but git says day 3 had 300 commits, day 4 had 1, and day 9 is outside the window.
	stats := map[string]probe.DayCount{
		fmtDay(3): {Commits: 300, Plus: 1000, Minus: 40},
		fmtDay(4): {Commits: 1, Plus: 7},
	}
	changed, err := db.SetDailyCommitStats(ctx, "alpha", fmtDay(1), fmtDay(5), stats, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fmtDay(1), fmtDay(2), fmtDay(3), fmtDay(4), fmtDay(5)}
	if !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v", changed)
	}
	if got := dsRow(t, db, "alpha", fmtDay(3)); got != (dsDayRow{300, 1000, 40}) {
		t.Fatalf("day 3 = %+v", got)
	}
	if got := dsRow(t, db, "alpha", fmtDay(4)); got != (dsDayRow{1, 7, 0}) {
		t.Fatalf("day 4 = %+v", got)
	}
	// Outside the window the commit rows still count.
	if got := dsRow(t, db, "alpha", fmtDay(9)); got != (dsDayRow{1, 5, 0}) {
		t.Fatalf("day 9 = %+v", got)
	}
	// Zero days are stored (counted), but daily_activity has no row for them.
	if n := count(t, db, `SELECT COUNT(*) FROM daily_commits WHERE project_id = 'alpha'`); n != 5 {
		t.Fatalf("daily_commits rows = %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM daily_activity WHERE project_id = 'alpha' AND day = ?`, fmtDay(1)); n != 0 {
		t.Fatalf("zero day has a daily_activity row")
	}

	// A later RecomputeDaily (another job, for example sessions) keeps the exact numbers.
	if err := db.RecomputeDaily(ctx, "alpha", fmtDay(3), testLoc); err != nil {
		t.Fatal(err)
	}
	if got := dsRow(t, db, "alpha", fmtDay(3)); got != (dsDayRow{300, 1000, 40}) {
		t.Fatalf("after RecomputeDaily: %+v", got)
	}

	// Same numbers again: nothing changes.
	changed, err = db.SetDailyCommitStats(ctx, "alpha", fmtDay(1), fmtDay(5), stats, testLoc)
	if err != nil || len(changed) != 0 {
		t.Fatalf("second set changed %v, %v", changed, err)
	}

	got, err := db.DailyCommitStats(ctx, "alpha", fmtDay(3), fmtDay(4))
	if err != nil || len(got) != 2 || got[fmtDay(3)].Commits != 300 {
		t.Fatalf("DailyCommitStats = %v, %v", got, err)
	}

	// Clear: the commit rows (2 commits) cannot lower the stored history (C1).
	var cleared []string
	mustWrite(t, db, func(tx *sql.Tx) error {
		var err error
		cleared, err = ClearDailyCommitStatsTx(ctx, tx, "alpha", testLoc)
		return err
	})
	if len(cleared) != 5 {
		t.Fatalf("cleared = %v", cleared)
	}
	if got := dsRow(t, db, "alpha", fmtDay(3)); got != (dsDayRow{300, 1000, 40}) {
		t.Fatalf("after clear: %+v", got)
	}

	// DeleteProject removes the rows too.
	if _, err := db.SetDailyCommitStats(ctx, "alpha", fmtDay(1), fmtDay(2), nil, testLoc); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProject(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM daily_commits`); n != 0 {
		t.Fatalf("rows after DeleteProject = %d", n)
	}
}

func TestSetDailyCommitStatsRejectsBadDays(t *testing.T) {
	db := openTest(t)
	if _, err := db.SetDailyCommitStats(bg(), "alpha", "x", fmtDay(2), nil, testLoc); err == nil {
		t.Fatal("want an error for a bad day")
	}
}
