package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestHomeShapeAndLive(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	useClock(t, at("2026-09-30", 15, 0))

	for _, p := range []Project{
		{ID: "old", Name: "old", Group: "tools", Path: "/p/old", LastChangeAt: at("2026-09-01", 9, 0)},
		{ID: "busy", Name: "busy", Group: "personal", Path: "/p/busy", LastChangeAt: at("2026-09-30", 14, 0)},
		{ID: "venv", Name: "venv", Group: "tools", Path: "/p/venv", Hidden: true},
	} {
		if err := db.UpsertProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.ReplaceDirty(ctx, "busy", nil)
	mustWrite(t, db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO dirty(project_id, path, state) VALUES('busy','a.go','M'),('busy','b.go','?')`); err != nil {
			return err
		}
		// today, 3 days ago, 8 days ago (outside a 7 day range), and one in the future (outside)
		for _, r := range []struct {
			day            string
			files, commits int
		}{{"2026-09-30", 7, 2}, {"2026-09-27", 1, 0}, {"2026-09-22", 9, 9}, {"2026-10-05", 5, 5}} {
			if _, err := tx.Exec(`INSERT INTO daily_activity(project_id, day, files, commits, lines_plus, sessions) VALUES('busy', ?, ?, ?, 3, 1)`,
				r.day, r.files, r.commits); err != nil {
				return err
			}
		}
		// A row for a project that is not in the projects table is ignored.
		_, err := tx.Exec(`INSERT INTO daily_activity(project_id, day, files) VALUES('ghost', '2026-09-30', 1)`)
		return err
	})
	_ = db.ReplaceServersNow(ctx, []ServerNow{
		{Key: "a", Port: 5173, ProjectID: "busy", Kind: "dev"},
		{Key: "b", Port: 3000, ProjectID: "busy", Kind: "dev"},
		{Key: "c", Port: 9999, ProjectID: "busy", Kind: "helper"},
		{Key: "d", Port: 8000, ProjectID: ""},
		{Key: "e", Port: 8001, ProjectID: "nope"},
	})
	_ = db.UpsertSessions(ctx, []Session{
		{ID: "fresh", ProjectID: "busy", JSONLMTime: at("2026-09-30", 14, 58)},
		{ID: "stale", ProjectID: "busy", JSONLMTime: at("2026-09-30", 13, 0)},
		{ID: "unassigned", ProjectID: "", JSONLMTime: at("2026-09-30", 14, 59)},
	})

	h, err := db.Home(ctx, 7, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if h.Range != 7 || len(h.Days) != 7 || h.Days[0] != "2026-09-24" || h.Days[6] != "2026-09-30" {
		t.Fatalf("days = %v range %d", h.Days, h.Range)
	}
	if len(h.Lanes) != 3 {
		t.Fatalf("lanes = %d (hidden projects are included)", len(h.Lanes))
	}
	// Newest change first; projects without a change time last.
	if h.Lanes[0].ProjectID != "busy" || h.Lanes[1].ProjectID != "old" || h.Lanes[2].ProjectID != "venv" {
		t.Fatalf("lane order = %s %s %s", h.Lanes[0].ProjectID, h.Lanes[1].ProjectID, h.Lanes[2].ProjectID)
	}
	busy := h.Lanes[0]
	if busy.Name != "busy" || busy.Group != "personal" || busy.Hidden || busy.Dirty != 2 || !busy.LastChangeAt.Equal(at("2026-09-30", 14, 0)) {
		t.Fatalf("busy lane = %+v", busy)
	}
	if !h.Lanes[2].Hidden {
		t.Error("venv lane must be flagged hidden")
	}
	if len(busy.Days) != 7 {
		t.Fatalf("cells = %d", len(busy.Days))
	}
	if busy.Days[6].Files != 7 || busy.Days[6].Commits != 2 || busy.Days[6].LinesPlus != 3 || busy.Days[6].Sessions != 1 {
		t.Errorf("today cell = %+v", busy.Days[6])
	}
	if busy.Days[3].Files != 1 { // 27th
		t.Errorf("27th cell = %+v", busy.Days[3])
	}
	total := 0
	for _, c := range busy.Days {
		total += c.Files
	}
	if total != 8 {
		t.Errorf("sum of files in range = %d, want 8 (22nd and 5 Oct are outside)", total)
	}
	if !reflect.DeepEqual(busy.Live.Servers, []int{3000, 5173}) {
		t.Errorf("live servers = %v (dev only, by port)", busy.Live.Servers)
	}
	if !reflect.DeepEqual(busy.Live.Sessions, []string{"fresh"}) {
		t.Errorf("live sessions = %v", busy.Live.Sessions)
	}
	// Empty lanes still have the slices, so JSON gives [] and not null.
	raw, _ := json.Marshal(h.Lanes[1])
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	live := back["live"].(map[string]any)
	if live["servers"] == nil || live["sessions"] == nil || back["days"] == nil {
		t.Errorf("lane JSON has null lists: %s", raw)
	}

	// Range 30 sees the 22nd.
	h30, _ := db.Home(ctx, 30, testLoc)
	if h30.Lanes[0].Days[29-8].Files != 9 {
		t.Errorf("22nd in a 30 day range = %+v", h30.Lanes[0].Days[29-8])
	}
	// Defaults.
	hd, _ := db.Home(ctx, 0, nil)
	if hd.Range != 30 || len(hd.Days) != 30 {
		t.Errorf("default range = %d", hd.Range)
	}
}

func TestHomeOnEmptyDatabase(t *testing.T) {
	db := openTest(t)
	h, err := db.Home(bg(), 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Lanes) != 0 || h.Lanes == nil || len(h.Days) != 30 {
		t.Fatalf("home = %+v", h)
	}
}

func TestDay(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	day := "2026-09-30"
	_, _ = db.AddEvent(ctx, Event{ProjectID: "alpha", TS: at(day, 12, 0), Day: day, Kind: "commit", Ref: "commit:b", Label: "second", Meta: json.RawMessage(`{"plus":3}`)})
	_, _ = db.AddEvent(ctx, Event{ProjectID: "alpha", TS: at(day, 9, 0), Day: day, Kind: "commit", Ref: "commit:a", Label: "first"})
	_, _ = db.AddEvent(ctx, Event{ProjectID: "beta", TS: at(day, 10, 0), Day: day, Kind: "session_start", Ref: "session:s", Label: "other project"})
	_, _ = db.AddEvent(ctx, Event{ProjectID: "alpha", TS: at("2026-10-01", 1, 0), Day: "2026-10-01", Kind: "note", Ref: "note:N-1"})
	_ = db.AddFileChanges(ctx, "alpha", day, []string{"a.go", "b.go", "b.go", "b.go"})
	_ = db.AddFileChanges(ctx, "beta", day, []string{"z.go"})
	mustWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO daily_activity(project_id, day, files, commits) VALUES('alpha', ?, 2, 2), ('beta', ?, 1, 0)`, day, day)
		return err
	})

	d, err := db.Day(ctx, day, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if d.Date != day || len(d.Events) != 2 || d.Events[0].Label != "first" || d.Events[1].Label != "second" {
		t.Fatalf("events = %+v", d.Events)
	}
	if string(d.Events[1].Meta) != `{"plus":3}` || d.Events[0].Meta != nil {
		t.Errorf("meta = %q / %q", d.Events[1].Meta, d.Events[0].Meta)
	}
	if !d.Events[0].TS.Equal(at(day, 9, 0)) || d.Events[0].ProjectID != "alpha" || d.Events[0].Kind != "commit" {
		t.Errorf("event 0 = %+v", d.Events[0])
	}
	if d.Totals.Files != 2 || d.Totals.Commits != 2 {
		t.Errorf("totals = %+v", d.Totals)
	}
	if d.FilesTotal != 2 || len(d.Files) != 2 || d.Files[0].Path != "b.go" || d.Files[0].N != 3 {
		t.Errorf("files = %+v total %d", d.Files, d.FilesTotal)
	}

	all, _ := db.Day(ctx, day, "")
	if len(all.Events) != 3 || all.Totals.Files != 3 || all.FilesTotal != 3 || len(all.Files) != 0 {
		t.Errorf("all projects = %+v", all)
	}

	quiet, err := db.Day(ctx, "2026-09-01", "alpha")
	if err != nil || quiet.Events == nil || len(quiet.Events) != 0 || quiet.Files == nil || quiet.Totals != (DayCell{}) {
		t.Errorf("quiet day = %+v, %v", quiet, err)
	}

	if _, err := db.Day(ctx, "30-09-2026", ""); err == nil {
		t.Error("a bad date must be an error")
	}
}

func TestProjectListEmpty(t *testing.T) {
	db := openTest(t)
	l, err := db.ProjectList(bg())
	if err != nil || l == nil || len(l) != 0 {
		t.Fatalf("list = %v, %v", l, err)
	}
}

// TestHomePerformance: 30 projects x 30 days with realistic row counts must answer in under 50 ms
// (03 section 10). The median of several runs is used, so one slow scheduler tick does not fail it.
func TestHomePerformance(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	useClock(t, at("2026-09-30", 15, 0))

	const projects, days = 30, 30
	mustWrite(t, db, func(tx *sql.Tx) error {
		ins := func(q string) *sql.Stmt {
			st, err := tx.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			return st
		}
		pStmt := ins(`INSERT INTO projects(id, name, grp, path, last_change_at) VALUES(?, ?, 'personal', ?, ?)`)
		dStmt := ins(`INSERT INTO daily_activity(project_id, day, files, commits, lines_plus, lines_minus, sessions, session_minutes, snapshots, notes, server_minutes, tasks_moved)
		              VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		fStmt := ins(`INSERT INTO files(project_id, path, size, mtime, kind) VALUES(?, ?, 10, ?, 'code')`)
		cStmt := ins(`INSERT INTO commits(project_id, id, ts, msg) VALUES(?, ?, ?, 'a commit message')`)
		eStmt := ins(`INSERT INTO events(project_id, ts, day, kind, ref, label) VALUES(?, ?, ?, 'commit', ?, 'label')`)
		sStmt := ins(`INSERT INTO sessions(id, project_id, jsonl_mtime, title) VALUES(?, ?, ?, 'a session')`)
		dirtyStmt := ins(`INSERT INTO dirty(project_id, path, state) VALUES(?, ?, 'M')`)
		srvStmt := ins(`INSERT INTO servers_now(key, port, project_id, kind) VALUES(?, ?, ?, 'dev')`)
		base := at("2026-09-30", 0, 0)
		for p := 0; p < projects; p++ {
			id := fmt.Sprintf("project%02d", p)
			if _, err := pStmt.Exec(id, id, "/p/"+id, base.Unix()-int64(p*3600)); err != nil {
				return err
			}
			for d := 0; d < days; d++ {
				day := base.AddDate(0, 0, -d).Format(DayLayout)
				if _, err := dStmt.Exec(id, day, d*3+1, d%4, d*10, d*2, d%2, d*5, d%3, d%2, d*7, d%3); err != nil {
					return err
				}
			}
			for i := 0; i < 800; i++ {
				if _, err := fStmt.Exec(id, fmt.Sprintf("src/dir%d/file%d.go", i%20, i), base.Unix()-int64(i)); err != nil {
					return err
				}
			}
			for i := 0; i < 200; i++ {
				ts := base.Unix() - int64(i*20000)
				if _, err := cStmt.Exec(id, fmt.Sprintf("%07x", p*1000+i), ts); err != nil {
					return err
				}
				if _, err := eStmt.Exec(id, ts, time.Unix(ts, 0).In(testLoc).Format(DayLayout), fmt.Sprintf("commit:%d", i)); err != nil {
					return err
				}
			}
			for i := 0; i < 10; i++ {
				if _, err := sStmt.Exec(fmt.Sprintf("s-%d-%d", p, i), id, base.Unix()-int64(i*86400)); err != nil {
					return err
				}
			}
			for i := 0; i < 3; i++ {
				if _, err := dirtyStmt.Exec(id, fmt.Sprintf("dirty%d.go", i)); err != nil {
					return err
				}
			}
			if p%3 == 0 {
				if _, err := srvStmt.Exec(fmt.Sprintf("k%d", p), 4000+p, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if n := count(t, db, `SELECT COUNT(*) FROM daily_activity`); n != projects*days {
		t.Fatalf("seeded %d daily rows", n)
	}

	limit := 50 * time.Millisecond
	if raceEnabled {
		limit = 500 * time.Millisecond // the race detector is several times slower
	}
	if _, err := db.Home(ctx, 30, testLoc); err != nil { // warm up the pool
		t.Fatal(err)
	}
	const runs = 15
	took := make([]time.Duration, runs)
	var data HomeData
	for i := range took {
		start := time.Now()
		var err error
		data, err = db.Home(ctx, 30, testLoc)
		took[i] = time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	median := took[runs/2]
	t.Logf("Home 30x30: median %v, best %v, worst %v (limit %v)", median, took[0], took[runs-1], limit)
	if median > limit {
		t.Fatalf("Home median %v is over the %v budget", median, limit)
	}
	if len(data.Lanes) != projects || len(data.Lanes[0].Days) != days {
		t.Fatalf("lanes %d, cells %d", len(data.Lanes), len(data.Lanes[0].Days))
	}
	if data.Lanes[0].Days[days-1].Files == 0 || data.Lanes[0].Live.Servers == nil {
		t.Fatalf("lane 0 = %+v", data.Lanes[0])
	}
}

// HomeAt uses the clock it is given, not the package clock.
func TestHomeAtUsesGivenNow(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	useClock(t, at("2020-01-01", 12, 0)) // far from the data: Home would show other days
	if err := db.UpsertProject(ctx, Project{ID: "busy", Name: "busy", Group: "personal", Path: "/p/busy"}); err != nil {
		t.Fatal(err)
	}
	_ = db.UpsertSessions(ctx, []Session{{ID: "fresh", ProjectID: "busy", JSONLMTime: at("2026-09-30", 14, 58)}})

	h, err := db.HomeAt(ctx, 7, testLoc, at("2026-09-30", 15, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Days[len(h.Days)-1]; got != "2026-09-30" {
		t.Errorf("last day = %s, want 2026-09-30", got)
	}
	if len(h.Lanes) != 1 || len(h.Lanes[0].Live.Sessions) != 1 {
		t.Errorf("live sessions = %+v, want the fresh one", h.Lanes)
	}
	old, _ := db.Home(ctx, 7, testLoc)
	if old.Days[len(old.Days)-1] != "2020-01-01" {
		t.Errorf("Home should follow the package clock, got %s", old.Days[len(old.Days)-1])
	}
}
