package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/store"
)

// ---- boot ----

func TestHomeBootShape(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "personal", "git", "main", false, true, "")
	e.project("gamma", "work", "git,fossil", "trunk", false, true, "")
	e.project("beta", "tools", "", "", true, false, "")
	if err := e.db.ReplaceDirty(context.Background(), "alpha", []probe.DirtyFile{{Path: "a.go", State: "M"}, {Path: "b", State: "?"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.AddSweepLog(context.Background(), store.SweepLog{TS: ahClock.Add(-90 * time.Second), Scope: "all", OK: true}); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Now        string   `json:"now"`
		Statuses   []string `json:"statuses"`
		Profiles   []string `json:"profiles"`
		Author     string   `json:"author"`
		LastScanAt *string  `json:"lastScanAt"`
		Counts     struct {
			LiveServers, OpenTasks, AwaitingReview, OpenNotes int
		} `json:"counts"`
		Projects []struct {
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			Group        string   `json:"group"`
			Path         string   `json:"path"`
			VCS          []string `json:"vcs"`
			Branch       *string  `json:"branch"`
			LastChangeAt *string  `json:"lastChangeAt"`
			Hidden       bool     `json:"hidden"`
			Surveyed     bool     `json:"surveyed"`
			DirtyCount   int      `json:"dirtyCount"`
		} `json:"projects"`
	}
	ahDecode(t, e.get("/api/boot"), 200, &got)

	if got.Now != "2026-09-30T14:03:22+05:30" {
		t.Errorf("now = %q", got.Now)
	}
	if !reflect.DeepEqual(got.Statuses, []string{"backlog", "todo", "in_progress", "review", "done"}) {
		t.Errorf("statuses = %v", got.Statuses)
	}
	if !reflect.DeepEqual(got.Profiles, []string{".claude", ".claude-work"}) {
		t.Errorf("profiles = %v", got.Profiles)
	}
	if got.Author != "gitname" {
		t.Errorf("author = %q", got.Author)
	}
	if got.LastScanAt == nil || *got.LastScanAt != "2026-09-30T14:01:52+05:30" {
		t.Errorf("lastScanAt = %v", got.LastScanAt)
	}
	if len(got.Projects) != 3 {
		t.Fatalf("projects = %+v", got.Projects)
	}
	// ordered by group, then name
	byID := map[string]int{}
	for i, p := range got.Projects {
		byID[p.ID] = i
	}
	a, g, b := got.Projects[byID["alpha"]], got.Projects[byID["gamma"]], got.Projects[byID["beta"]]
	if a.Name != "alpha" || a.Group != "personal" || a.Path != "/p/alpha" || !reflect.DeepEqual(a.VCS, []string{"git"}) ||
		a.Branch == nil || *a.Branch != "main" || a.DirtyCount != 2 || !a.Surveyed || a.Hidden {
		t.Errorf("alpha = %+v", a)
	}
	if a.LastChangeAt == nil || *a.LastChangeAt != "2026-09-30T13:03:22+05:30" {
		t.Errorf("alpha lastChangeAt = %v", a.LastChangeAt)
	}
	if !reflect.DeepEqual(g.VCS, []string{"git", "fossil"}) {
		t.Errorf("gamma vcs = %v", g.VCS)
	}
	if b.Surveyed || !b.Hidden || b.Branch != nil || b.VCS == nil || len(b.VCS) != 0 {
		t.Errorf("beta = %+v", b)
	}
}

func TestHomeBootFileCount(t *testing.T) {
	e := ahNew(t, nil)
	ctx := context.Background()
	e.project("full", "g", "git", "main", false, true, "")
	e.project("empty", "g", "", "", false, true, "")
	e.project("fresh", "g", "", "", false, false, "")
	e.project("beta", "g", "", "", true, true, "") // hidden, never walked
	if err := e.db.ReplaceFiles(ctx, "full", []store.File{{Path: "a.go", Kind: "code"}, {Path: "b.go", Kind: "code"}, {Path: "c.md", Kind: "doc"}}); err != nil {
		t.Fatal(err)
	}
	m := ahRaw(t, e.get("/api/boot"), 200)
	got := map[string]any{}
	for _, p := range m["projects"].([]any) {
		pm := p.(map[string]any)
		if v, ok := pm["fileCount"]; ok {
			got[pm["id"].(string)] = v
		} else {
			got[pm["id"].(string)] = "absent"
		}
	}
	want := map[string]any{"full": 3.0, "empty": 0.0, "fresh": "absent", "beta": "absent"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fileCount by project = %v, want %v", got, want)
	}
}

func TestHomeBootEmptyStoreAndNulls(t *testing.T) {
	e := ahNew(t, func(d *HomeDeps) { d.DefaultAuthor = nil; d.Profiles = nil })
	m := ahRaw(t, e.get("/api/boot"), 200)
	if p, ok := m["projects"].([]any); !ok || len(p) != 0 {
		t.Errorf("projects = %#v (want an empty list, not null)", m["projects"])
	}
	if v, ok := m["lastScanAt"]; !ok || v != nil {
		t.Errorf("lastScanAt = %#v (want null before the first scan)", v)
	}
	if p, ok := m["profiles"].([]any); !ok || len(p) != 0 {
		t.Errorf("profiles = %#v", m["profiles"])
	}
	if m["author"] != "" {
		t.Errorf("author = %#v", m["author"])
	}
	c := m["counts"].(map[string]any)
	for _, k := range []string{"liveServers", "openTasks", "awaitingReview", "openNotes"} {
		if c[k] != float64(0) {
			t.Errorf("counts.%s = %v", k, c[k])
		}
	}
}

func TestHomeBootCountsAndAuthorSetting(t *testing.T) {
	e := ahNew(t, nil)
	ctx := context.Background()
	e.project("alpha", "g", "git", "main", false, true, "")
	if err := e.db.UpsertAuthoredTask(ctx, store.Task{ProjectID: "alpha", ID: "T-1", Title: "t", Status: "review"}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertAuthoredSnapshot(ctx, store.Snapshot{ProjectID: "alpha", TaskID: "T-1", Version: 1, Verdict: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertAuthoredNote(ctx, store.Note{ProjectID: "alpha", ID: "N-1", Ref: "task:T-1", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.ReplaceServersNow(ctx, []store.ServerNow{{Key: "k", Port: 5173, ProjectID: "alpha", Kind: "dev"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.SetSetting(ctx, "author", "alice"); err != nil {
		t.Fatal(err)
	}
	m := ahRaw(t, e.get("/api/boot"), 200)
	c := m["counts"].(map[string]any)
	want := map[string]any{"liveServers": 1.0, "openTasks": 1.0, "awaitingReview": 1.0, "openNotes": 1.0}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("counts = %v", c)
	}
	if m["author"] != "alice" {
		t.Errorf("author = %v (a saved author beats the default)", m["author"])
	}
}

func TestHomeBootUsesInjectedCounts(t *testing.T) {
	e := ahNew(t, func(d *HomeDeps) {
		d.Counts = func(context.Context) (live.Counts, error) {
			return live.Counts{LiveServers: 7, OpenTasks: 8, AwaitingReview: 9, OpenNotes: 10}, nil
		}
	})
	m := ahRaw(t, e.get("/api/boot"), 200)
	want := map[string]any{"liveServers": 7.0, "openTasks": 8.0, "awaitingReview": 9.0, "openNotes": 10.0}
	if !reflect.DeepEqual(m["counts"], want) {
		t.Errorf("counts = %v", m["counts"])
	}
}

func TestHomeBootCountsError(t *testing.T) {
	e := ahNew(t, func(d *HomeDeps) {
		d.Counts = func(context.Context) (live.Counts, error) { return live.Counts{}, fmt.Errorf("boom") }
	})
	ahErrCode(t, e.get("/api/boot"), 500, "internal")
}

func TestHomeReadRoutesAreGETOnly(t *testing.T) {
	e := ahNew(t, nil)
	for _, p := range []string{"/api/boot", "/api/home", "/api/day", "/api/inbox", "/api/search"} {
		rec := e.do("POST", p, "{}", true)
		if rec.Code != 405 {
			t.Errorf("POST %s = %d, want 405", p, rec.Code)
		}
	}
	// the guard still applies: a foreign Host never reaches the handler
	req := httptest.NewRequest("GET", "/api/boot", nil)
	req.Host = "evil.example:4455"
	rec := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("foreign host gave %d", rec.Code)
	}
}

// ---- home ----

// ahToday is today's key in the test zone, read from the real clock (store.Home uses the real clock).
func ahToday() time.Time {
	n := ahClock.In(ahIST)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, ahIST)
}

func (e *ahEnv) activity(project, day string, files, commits, plus, minus int) {
	e.t.Helper()
	err := e.db.Write(context.Background(), func(tx *sqlTx) error {
		_, err := tx.Exec(`INSERT OR REPLACE INTO daily_activity(project_id, day, files, commits, lines_plus, lines_minus, sessions, snapshots, notes, server_minutes)
VALUES(?, ?, ?, ?, ?, ?, 1, 2, 3, 4)`, project, day, files, commits, plus, minus)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func TestHomeShapeAndRange(t *testing.T) {
	e := ahNew(t, nil)
	ctx := context.Background()
	e.project("alpha", "personal", "git", "main", false, true, "")
	e.project("beta", "tools", "", "", true, false, "")
	if err := e.db.UpsertProject(ctx, store.Project{ID: "quiet", Name: "quiet", Group: "tools", Path: "/p/quiet"}); err != nil {
		t.Fatal(err)
	}
	today := ahToday()
	todayKey := today.Format(store.DayLayout)
	e.activity("alpha", todayKey, 12, 2, 340, 88)
	e.activity("alpha", today.AddDate(0, 0, -3).Format(store.DayLayout), 5, 0, 0, 0)
	e.activity("alpha", today.AddDate(0, 0, -20).Format(store.DayLayout), 9, 1, 10, 1) // outside 7d
	if err := e.db.ReplaceServersNow(ctx, []store.ServerNow{
		{Key: "k1", Port: 5173, ProjectID: "alpha", Kind: "dev"},
		{Key: "k2", Port: 9000, ProjectID: "alpha", Kind: "helper"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.ReplaceDirty(ctx, "alpha", []probe.DirtyFile{{Path: "x", State: "M"}}); err != nil {
		t.Fatal(err)
	}

	type cell struct {
		Files, Commits, LinesPlus, LinesMinus, Sessions, Snapshots, Notes, ServerMinutes int
	}
	type resp struct {
		Range int      `json:"range"`
		Days  []string `json:"days"`
		Lanes []struct {
			ProjectID string `json:"projectId"`
			Hidden    bool   `json:"hidden"`
			Dirty     int    `json:"dirty"`
			Days      []cell `json:"days"`
			Live      struct {
				Servers  []int    `json:"servers"`
				Sessions []string `json:"sessions"`
			} `json:"live"`
			LastChangeAt *string `json:"lastChangeAt"`
		} `json:"lanes"`
	}

	var r30 resp
	ahDecode(t, e.get("/api/home"), 200, &r30) // default range is 30
	if r30.Range != 30 || len(r30.Days) != 30 || r30.Days[29] != todayKey {
		t.Fatalf("range/days = %d %d %q", r30.Range, len(r30.Days), r30.Days[len(r30.Days)-1])
	}
	if len(r30.Lanes) != 3 {
		t.Fatalf("lanes = %d (hidden and quiet lanes must be there too)", len(r30.Lanes))
	}
	var alpha, beta, quiet int
	for i, l := range r30.Lanes {
		switch l.ProjectID {
		case "alpha":
			alpha = i
		case "beta":
			beta = i
		case "quiet":
			quiet = i
		}
		if len(l.Days) != 30 {
			t.Errorf("%s has %d day cells", l.ProjectID, len(l.Days))
		}
	}
	la := r30.Lanes[alpha]
	if la.Days[29].Files != 12 || la.Days[29].Commits != 2 || la.Days[29].LinesPlus != 340 || la.Days[29].LinesMinus != 88 ||
		la.Days[29].Sessions != 1 || la.Days[29].Snapshots != 2 || la.Days[29].Notes != 3 || la.Days[29].ServerMinutes != 4 {
		t.Errorf("today's cell = %+v", la.Days[29])
	}
	if la.Days[26].Files != 5 || la.Days[9].Files != 9 {
		t.Errorf("older cells = %+v / %+v", la.Days[26], la.Days[9])
	}
	if !reflect.DeepEqual(la.Live.Servers, []int{5173}) || la.Live.Sessions == nil || la.Dirty != 1 || la.Hidden {
		t.Errorf("alpha lane = %+v", la)
	}
	if la.LastChangeAt == nil || *la.LastChangeAt != "2026-09-30T13:03:22+05:30" {
		t.Errorf("alpha lastChangeAt = %v", la.LastChangeAt)
	}
	if !r30.Lanes[beta].Hidden {
		t.Error("hidden lane lost its flag")
	}
	if r30.Lanes[quiet].LastChangeAt != nil {
		t.Errorf("a lane that never changed must have lastChangeAt null, got %v", *r30.Lanes[quiet].LastChangeAt)
	}
	if r30.Lanes[quiet].Live.Servers == nil || r30.Lanes[quiet].Live.Sessions == nil {
		t.Error("live lists must be [] and not null")
	}

	var r7 resp
	ahDecode(t, e.get("/api/home?range=7"), 200, &r7)
	if r7.Range != 7 || len(r7.Days) != 7 || r7.Days[6] != todayKey {
		t.Fatalf("7d: %d %d", r7.Range, len(r7.Days))
	}
	for _, l := range r7.Lanes {
		if len(l.Days) != 7 {
			t.Errorf("7d lane %s has %d cells", l.ProjectID, len(l.Days))
		}
		if l.ProjectID == "alpha" && (l.Days[6].Files != 12 || l.Days[3].Files != 5) {
			t.Errorf("7d cells = %+v", l.Days)
		}
	}
}

func TestHomeRangeValidation(t *testing.T) {
	e := ahNew(t, nil)
	for _, v := range []string{"", "0", "1", "14", "31", "365", "-7", "abc", "7d", "07", "30.0", "7 ", "%00", "7,30"} {
		rec := e.get("/api/home?range=" + url.QueryEscape(v))
		if rec.Code != 400 {
			t.Errorf("range=%q gave %d, want 400", v, rec.Code)
			continue
		}
		ahErrCode(t, rec, 400, "bad_request")
	}
	for _, v := range []string{"7", "30"} {
		if rec := e.get("/api/home?range=" + v); rec.Code != 200 {
			t.Errorf("range=%s gave %d", v, rec.Code)
		}
	}
}

func TestHomeEmptyStoreHasEmptyLanes(t *testing.T) {
	e := ahNew(t, nil)
	m := ahRaw(t, e.get("/api/home"), 200)
	if l, ok := m["lanes"].([]any); !ok || len(l) != 0 {
		t.Errorf("lanes = %#v", m["lanes"])
	}
}

// TestHomePerformance: 30 projects x 30 days must answer in under 50 ms (03 section 10).
// The median of several runs is used so one slow scheduler tick does not fail the test.
func TestHomePerformance(t *testing.T) {
	e := ahNew(t, nil)
	ctx := context.Background()
	today := ahToday()
	err := e.db.Write(ctx, func(tx *sqlTx) error {
		for p := 0; p < 30; p++ {
			id := fmt.Sprintf("proj%02d", p)
			if _, err := tx.Exec(`INSERT INTO projects(id, name, grp, path, hidden, last_change_at, indexed_at) VALUES(?, ?, 'g', ?, ?, ?, 1)`,
				id, id, "/p/"+id, p%10 == 0, today.Unix()-int64(p)*60); err != nil {
				return err
			}
			for d := 0; d < 30; d++ {
				day := today.AddDate(0, 0, -d).Format(store.DayLayout)
				if _, err := tx.Exec(`INSERT INTO daily_activity(project_id, day, files, commits, lines_plus, lines_minus, sessions, snapshots, notes, server_minutes)
VALUES(?, ?, ?, ?, ?, ?, 1, 0, 0, 5)`, id, day, d+p, d%3, d*10, d); err != nil {
					return err
				}
			}
			for k := 0; k < 12; k++ {
				if _, err := tx.Exec(`INSERT INTO dirty(project_id, path, state) VALUES(?, ?, 'M')`, id, fmt.Sprintf("f%d", k)); err != nil {
					return err
				}
			}
			if p%3 == 0 {
				if _, err := tx.Exec(`INSERT INTO servers_now(key, port, project_id, kind) VALUES(?, ?, ?, 'dev')`, "k"+id, 3000+p, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	limit := 50 * time.Millisecond
	if ahRaceEnabled {
		limit = 500 * time.Millisecond // the race detector is several times slower
	}
	const runs = 25
	took := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		rec := e.get("/api/home?range=30")
		took = append(took, time.Since(start))
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		if i == 0 {
			var r struct {
				Lanes []struct {
					Days []struct{ Files int } `json:"days"`
				} `json:"lanes"`
			}
			ahDecode(t, rec, 200, &r)
			if len(r.Lanes) != 30 || len(r.Lanes[0].Days) != 30 {
				t.Fatalf("lanes = %d", len(r.Lanes))
			}
		}
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	median := took[runs/2]
	t.Logf("GET /api/home 30x30: median %v, best %v, worst %v (limit %v)", median, took[0], took[runs-1], limit)
	if median > limit {
		t.Fatalf("median %v is over the %v budget", median, limit)
	}
}

// ---- day ----

func TestHomeDayEventsAndTotals(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	e.project("gamma", "g", "git", "main", false, true, "")
	day := time.Date(2026, 9, 30, 0, 0, 0, 0, ahIST)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	e.event("alpha", "commit", "commit:a1b2c3d", "fix the dome", at(9, 5), `{"plus":40,"minus":3}`)
	e.event("alpha", "session_start", "session:11111111-1111-1111-1111-111111111111", "dome work", at(10, 0), `{"profile":".claude-work"}`)
	e.event("alpha", "session_end", "session:11111111-1111-1111-1111-111111111111", "dome work", at(10, 40), "")
	e.event("alpha", "session_end", "session:22222222-2222-2222-2222-222222222222", "started yesterday", at(11, 0), `{"context":"ctx text"}`)
	e.event("alpha", "task_created", "task:T-4", "write tests", at(12, 0), "")
	e.event("alpha", "task_status", "task:T-4", "T-4 -> review", at(12, 30), "")
	e.event("alpha", "snapshot", "snapshot:T-4/v1", "Dome v1", at(13, 0), "")
	e.event("alpha", "verdict", "snapshot:T-4/v1", "approved", at(13, 30), "")
	e.event("alpha", "note", "note:N-1", "too bright", at(14, 0), "")
	e.event("alpha", "server_up", "server:5173", "vite :5173", at(8, 0), "")
	e.event("alpha", "file_burst", "", "12 files", at(8, 30), "")
	e.event("gamma", "commit", "commit:ffffffff", "other project", at(9, 6), "")
	e.event("alpha", "commit", "commit:old", "yesterday", at(9, 0).AddDate(0, 0, -1), "")
	ctx := context.Background()
	if err := e.db.AddFileChanges(ctx, "alpha", "2026-09-30", []string{"a.go", "a.go", "b.go", "c.go"}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.AddFileChanges(ctx, "gamma", "2026-09-30", []string{"g.go"}); err != nil {
		t.Fatal(err)
	}
	e.activity("alpha", "2026-09-30", 3, 1, 40, 3)
	e.activity("gamma", "2026-09-30", 1, 1, 0, 0)

	type resp struct {
		Date   string `json:"date"`
		Events []struct {
			TS        string `json:"ts"`
			Kind      string `json:"kind"`
			Event     string `json:"event"`
			Ref       string `json:"ref"`
			Label     string `json:"label"`
			ProjectID string `json:"projectId"`
			Context   string `json:"context"`
		} `json:"events"`
		Totals struct {
			Files, Commits, LinesPlus, LinesMinus int
		} `json:"totals"`
		Files      []string `json:"files"`
		MoreFiles  int      `json:"moreFiles"`
		FilesTotal int      `json:"filesTotal"`
	}

	var all resp
	ahDecode(t, e.get("/api/day?date=2026-09-30"), 200, &all)
	if all.Date != "2026-09-30" {
		t.Errorf("date = %q", all.Date)
	}
	var lines []string
	for _, x := range all.Events {
		lines = append(lines, fmt.Sprintf("%s %s/%s %s %s|%s", x.TS[11:16], x.Kind, x.Event, x.ProjectID, x.Ref, x.Context))
	}
	want := []string{
		"08:00 server/server_up alpha alpha:server:5173|",
		"08:30 file/file_burst alpha |",
		"09:05 commit/commit alpha alpha:commit:a1b2c3d|+40 −3",
		"09:06 commit/commit gamma gamma:commit:ffffffff|",
		"10:00 session/session_start alpha alpha:session:11111111-1111-1111-1111-111111111111|.claude-work",
		// the session_end of the first session is folded into its start; the second only has an end
		"11:00 session/session_end alpha alpha:session:22222222-2222-2222-2222-222222222222|ctx text",
		"12:00 task/task_created alpha alpha:task:T-4|",
		"12:30 task/task_status alpha alpha:task:T-4|",
		"13:00 snapshot/snapshot alpha alpha:snapshot:T-4/v1|pending",
		"13:30 snapshot/verdict alpha alpha:snapshot:T-4/v1|approved",
		"14:00 note/note alpha alpha:note:N-1|",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("events:\n got  %q\n want %q", lines, want)
	}
	if !strings.HasSuffix(all.Events[0].TS, "+05:30") {
		t.Errorf("ts must carry the local offset: %q", all.Events[0].TS)
	}
	if all.Totals.Files != 4 || all.Totals.Commits != 2 || all.Totals.LinesPlus != 40 {
		t.Errorf("totals = %+v", all.Totals)
	}
	if len(all.Files) != 0 || all.MoreFiles != 0 {
		t.Errorf("a day for all projects lists no files: %v %d", all.Files, all.MoreFiles)
	}

	var one resp
	ahDecode(t, e.get("/api/day?date=2026-09-30&project=alpha"), 200, &one)
	for _, x := range one.Events {
		if x.ProjectID != "alpha" {
			t.Errorf("project filter leaked %+v", x)
		}
	}
	if one.Totals.Files != 3 || one.Totals.Commits != 1 {
		t.Errorf("project totals = %+v", one.Totals)
	}
	if !reflect.DeepEqual(one.Files, []string{"a.go", "b.go", "c.go"}) || one.MoreFiles != 0 || one.FilesTotal != 3 {
		t.Errorf("files = %v more=%d total=%d", one.Files, one.MoreFiles, one.FilesTotal)
	}

	// more than 20 files: top 20 and a count of the rest
	many := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, fmt.Sprintf("m%02d.go", i))
	}
	if err := e.db.AddFileChanges(ctx, "gamma", "2026-09-29", many); err != nil {
		t.Fatal(err)
	}
	var big resp
	ahDecode(t, e.get("/api/day?date=2026-09-29&project=gamma"), 200, &big)
	if len(big.Files) != 20 || big.MoreFiles != 10 || big.FilesTotal != 30 {
		t.Errorf("many files: %d more=%d total=%d", len(big.Files), big.MoreFiles, big.FilesTotal)
	}
}

func TestHomeDayQuietAndUnknown(t *testing.T) {
	e := ahNew(t, nil)
	m := ahRaw(t, e.get("/api/day?date=2026-01-02&project=nobody"), 200)
	if ev, ok := m["events"].([]any); !ok || len(ev) != 0 {
		t.Errorf("events = %#v (want [])", m["events"])
	}
	if f, ok := m["files"].([]any); !ok || len(f) != 0 {
		t.Errorf("files = %#v (want [])", m["files"])
	}
}

func TestHomeDayValidation(t *testing.T) {
	e := ahNew(t, nil)
	bad := []string{"", "yesterday", "2026-9-3", "2026-09-3", "26-09-30", "2026-13-01", "2026-00-10", "2026-02-30",
		"2026-09-31", "2026-09-30T00:00:00Z", "2026/09/30", "2026-09-30 ", "２０２６-09-30", "2026-09-30%00", "20260930", "-2026-09-3"}
	for _, d := range bad {
		rec := e.get("/api/day?date=" + url.QueryEscape(d))
		if rec.Code != 400 {
			t.Errorf("date=%q gave %d, want 400", d, rec.Code)
			continue
		}
		ahErrCode(t, rec, 400, "bad_request")
	}
	if rec := e.get("/api/day"); rec.Code != 400 {
		t.Errorf("missing date gave %d", rec.Code)
	}
	if rec := e.get("/api/day?date=2026-09-30&project=" + url.QueryEscape("a\nb")); rec.Code != 400 {
		t.Errorf("control character in project gave %d", rec.Code)
	}
	if rec := e.get("/api/day?date=2026-09-30&project=" + strings.Repeat("x", 300)); rec.Code != 400 {
		t.Errorf("long project gave %d", rec.Code)
	}
	if rec := e.get("/api/day?date=2024-02-29"); rec.Code != 200 {
		t.Errorf("leap day gave %d", rec.Code)
	}
}
