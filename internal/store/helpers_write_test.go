package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

func TestUpsertProjectAndList(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	p := Project{
		ID: "ledger", Name: "ledger", Group: "personal", Path: "/home/x/projects/ledger",
		VCS: "git,fossil", Branch: "main", Remote: "git@example.com:x/ledger.git", Description: "servers",
		Stack: []string{"go"}, ReadmePath: "README.md", LastChangeAt: time.Unix(5000, 0), IndexedAt: time.Unix(6000, 0),
	}
	if err := db.UpsertProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	p2 := Project{ID: "gizmo", Name: "gizmo", Group: "tools", Path: "/home/x/projects/gizmo", Hidden: true}
	if err := db.UpsertProject(ctx, p2); err != nil {
		t.Fatal(err)
	}
	// Update.
	p.Branch = "dev"
	if err := db.UpsertProject(ctx, p); err != nil {
		t.Fatal(err)
	}

	list, err := db.ProjectList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("projects = %d", len(list))
	}
	// Ordered by group then name: personal before tools.
	got := list[0]
	if got.ID != p.ID || got.Branch != "dev" || got.VCS != "git,fossil" || !reflect.DeepEqual(got.Stack, []string{"go"}) ||
		!got.LastChangeAt.Equal(p.LastChangeAt) || !got.IndexedAt.Equal(p.IndexedAt) || got.Hidden {
		t.Errorf("project = %+v", got)
	}
	if !list[1].Hidden || list[1].Stack == nil || !list[1].LastChangeAt.IsZero() {
		t.Errorf("hidden project = %+v", list[1])
	}
}

func TestDeleteProjectClearsCacheButNotHistory(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")
	seedProject(t, db, "beta")
	for _, id := range []string{"alpha", "beta"} {
		_ = db.ReplaceFiles(ctx, id, []File{{Path: "x.go", Kind: "code", MTime: time.Unix(1, 0)}})
		_ = db.ReplaceCommits(ctx, id, []probe.Commit{{ID: "c1", Time: time.Unix(2, 0), Msg: "message"}})
		_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: id, ID: "T-1", Title: "task"})
		_ = db.AddFileChanges(ctx, id, "2026-09-30", []string{"x.go"})
	}
	_ = db.UpsertSessions(ctx, []Session{{ID: "s1", ProjectID: "alpha", Title: "session title"}})

	if err := db.DeleteProject(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM projects WHERE id = 'alpha'`,
		`SELECT COUNT(*) FROM files WHERE project_id = 'alpha'`,
		`SELECT COUNT(*) FROM commits WHERE project_id = 'alpha'`,
		`SELECT COUNT(*) FROM tasks WHERE project_id = 'alpha'`,
		`SELECT COUNT(*) FROM search WHERE project_id = 'alpha'`,
		`SELECT COUNT(*) FROM search_ref WHERE project_id = 'alpha'`,
	} {
		if n := count(t, db, q); n != 0 {
			t.Errorf("%s = %d, want 0", q, n)
		}
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search WHERE project_id = 'beta'`); n != 3 {
		t.Errorf("project b search rows = %d, want 3 (file, commit, task)", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM file_changes WHERE project_id = 'alpha'`); n != 1 {
		t.Errorf("history must be kept, file_changes = %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sessions WHERE id = 's1' AND project_id = ''`); n != 1 {
		t.Errorf("the session should become unassigned")
	}
}

func TestReplaceFilesKeepsSearchInStep(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")

	files := []File{
		{Path: "src/discover.go", Size: 10, MTime: time.Unix(100, 0), Kind: "code", Lines: 3},
		{Path: "docs/plan.md", Size: 20, MTime: time.Unix(100, 0), Kind: "doc", Title: "The Plan", Body: "We build the store first."},
		{Path: "README.md", Size: 5, MTime: time.Unix(100, 0), Kind: "doc", Title: "Readme"}, // no body: a plain file row
	}
	if err := db.ReplaceFiles(ctx, "alpha", files); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM files WHERE project_id = 'alpha'`); n != 3 {
		t.Fatalf("files = %d", n)
	}
	kinds := func() map[string]string {
		rows, err := db.Read().Query(`SELECT ref, kind FROM search_ref ORDER BY ref`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var r, k string
			_ = rows.Scan(&r, &k)
			out[r] = k
		}
		return out
	}
	want := map[string]string{"file:src/discover.go": "file", "file:docs/plan.md": "doc", "file:README.md": "file"}
	if got := kinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("search kinds = %v, want %v", got, want)
	}
	if hits, _ := db.Search(ctx, "build the store", "", "doc", 5); len(hits) != 1 || hits[0].Ref != "file:docs/plan.md" {
		t.Fatalf("doc text search = %+v", hits)
	}
	if hits, _ := db.Search(ctx, "scover", "", "file", 5); len(hits) != 1 {
		t.Fatalf("path search = %+v", hits)
	}

	// Second pass: one file removed, one changed, one new.
	files2 := []File{
		{Path: "src/discover.go", Size: 11, MTime: time.Unix(100, 0), Kind: "code"},                                      // same mtime: no search work
		{Path: "docs/plan.md", Size: 22, MTime: time.Unix(200, 0), Kind: "doc", Title: "The Plan", Body: "Now we ship."}, // changed
		{Path: "src/newfile.go", Size: 1, MTime: time.Unix(300, 0), Kind: "code"},
	}
	if err := db.ReplaceFiles(ctx, "alpha", files2); err != nil {
		t.Fatal(err)
	}
	want = map[string]string{"file:src/discover.go": "file", "file:docs/plan.md": "doc", "file:src/newfile.go": "file"}
	if got := kinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("search kinds = %v, want %v", got, want)
	}
	if hits, _ := db.Search(ctx, "build the store", "", "", 5); len(hits) != 0 {
		t.Fatalf("old doc text should be gone: %+v", hits)
	}
	if hits, _ := db.Search(ctx, "now we ship", "", "", 5); len(hits) != 1 {
		t.Fatalf("new doc text should be found")
	}
	if hits, _ := db.Search(ctx, "README", "", "", 5); len(hits) != 0 {
		t.Fatalf("removed file should leave search: %+v", hits)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search`); n != 3 {
		t.Fatalf("search rows = %d, want 3", n)
	}
	var size int
	_ = db.Read().QueryRow(`SELECT size FROM files WHERE path = 'src/discover.go'`).Scan(&size)
	if size != 11 {
		t.Fatalf("files row must be updated even when search is skipped, size = %d", size)
	}

	// A doc that gets its body later is switched from "file" to "doc".
	if err := db.ReplaceFiles(ctx, "alpha", []File{{Path: "NOTES.md", MTime: time.Unix(1, 0), Kind: "doc", Title: "Notes"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceFiles(ctx, "alpha", []File{{Path: "NOTES.md", MTime: time.Unix(1, 0), Kind: "doc", Title: "Notes", Body: "now with text"}}); err != nil {
		t.Fatal(err)
	}
	if got := kinds(); !reflect.DeepEqual(got, map[string]string{"file:NOTES.md": "doc"}) {
		t.Fatalf("kind switch: %v", got)
	}

	// Empty list clears everything.
	if err := db.ReplaceFiles(ctx, "alpha", nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM files`) + count(t, db, `SELECT COUNT(*) FROM search`); n != 0 {
		t.Fatalf("rows left: %d", n)
	}
}

func TestReplaceFilesIsPerProject(t *testing.T) {
	db := openTest(t)
	_ = db.ReplaceFiles(bg(), "alpha", []File{{Path: "x.go", MTime: time.Unix(1, 0)}})
	_ = db.ReplaceFiles(bg(), "beta", []File{{Path: "x.go", MTime: time.Unix(1, 0)}})
	_ = db.ReplaceFiles(bg(), "alpha", nil)
	if n := count(t, db, `SELECT COUNT(*) FROM files WHERE project_id = 'beta'`); n != 1 {
		t.Fatal("project b lost its files")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search WHERE project_id = 'beta'`); n != 1 {
		t.Fatal("project b lost its search row")
	}
}

func TestReplaceCommitsKeepsOlderAndDropsRewritten(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	c := func(id string, day string, h int, files ...probe.CommitFile) probe.Commit {
		return probe.Commit{ID: id, VCS: "git", Author: "A", Time: at(day, h, 0), Msg: "msg " + id, Plus: 2, Minus: 1, Files: files}
	}
	first := []probe.Commit{
		c("c5", "2026-09-30", 12, probe.CommitFile{Path: "a.go", Plus: 2, Minus: 1}),
		c("c4", "2026-09-29", 12),
		c("c3", "2026-09-28", 12),
		c("c2", "2026-09-20", 12), // older than the next window
	}
	if err := db.ReplaceCommits(ctx, "alpha", first); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM commit_files WHERE commit_id = 'c5'`); n != 1 {
		t.Fatalf("commit_files = %d", n)
	}
	var nfiles int
	_ = db.Read().QueryRow(`SELECT nfiles FROM commits WHERE id = 'c5'`).Scan(&nfiles)
	if nfiles != 1 {
		t.Errorf("nfiles should default to the number of files, got %d", nfiles)
	}

	// A new log: c4 was amended into c4x. The window starts at c3 (28th). c2 is older, so it stays.
	second := []probe.Commit{
		c("c6", "2026-10-01", 9),
		c("c5", "2026-09-30", 12), // now without files
		c("c4x", "2026-09-29", 13),
		c("c3", "2026-09-28", 12),
	}
	if err := db.ReplaceCommits(ctx, "alpha", second); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Read().Query(`SELECT id FROM commits WHERE project_id = 'alpha' ORDER BY ts DESC`)
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if want := []string{"c6", "c5", "c4x", "c3", "c2"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("commits = %v, want %v", ids, want)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM commit_files WHERE commit_id = 'c5'`); n != 0 {
		t.Errorf("files of a re-sent commit must be replaced, got %d", n)
	}
	if hits, _ := db.Search(ctx, "msg c4x", "", "commit", 5); len(hits) != 1 {
		t.Errorf("new commit must be searchable")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search WHERE ref = 'commit:c4'`); n != 0 {
		t.Errorf("the dropped commit must leave search")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search WHERE kind = 'commit'`); n != 5 {
		t.Errorf("commit search rows = %d, want 5", n)
	}

	// Empty list deletes all.
	if err := db.ReplaceCommits(ctx, "alpha", nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM commits`) + count(t, db, `SELECT COUNT(*) FROM search`); n != 0 {
		t.Fatalf("rows left: %d", n)
	}
}

func TestReplaceDirty(t *testing.T) {
	db := openTest(t)
	_ = db.ReplaceDirty(bg(), "alpha", []probe.DirtyFile{{Path: "a.go", State: "M"}, {Path: "b.go", State: "?"}})
	_ = db.ReplaceDirty(bg(), "beta", []probe.DirtyFile{{Path: "z.go", State: "A"}})
	_ = db.ReplaceDirty(bg(), "alpha", []probe.DirtyFile{{Path: "c.go", State: "D"}})
	if n := count(t, db, `SELECT COUNT(*) FROM dirty WHERE project_id = 'alpha'`); n != 1 {
		t.Fatalf("dirty a = %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM dirty WHERE project_id = 'beta'`); n != 1 {
		t.Fatalf("dirty b = %d", n)
	}
}

func TestSessionsAndProjectsAndSearch(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	s := Session{
		ID: "121c3b19-ada3", Profile: "~/.claude-work", JSONLPath: "/x/121c3b19.jsonl", CWD: "/p/alpha", ProjectID: "alpha",
		Start: at("2026-09-30", 9, 0), End: at("2026-09-30", 10, 30), Title: "Build the dome", Turns: 12, KB: 40,
		Tools: map[string]int{"Edit": 3}, Edited: []string{"dome.go"}, JSONLSize: 4096, JSONLMTime: time.Unix(1000, 0),
		Text: "please make the constellation lines follow the curvature",
	}
	if err := db.UpsertSessions(ctx, []Session{s}); err != nil {
		t.Fatal(err)
	}
	var title, tools, edited string
	var turns int
	if err := db.Read().QueryRow(`SELECT title, turns, tools, edited FROM sessions WHERE id = ?`, s.ID).
		Scan(&title, &turns, &tools, &edited); err != nil {
		t.Fatal(err)
	}
	if title != "Build the dome" || turns != 12 || tools != `{"Edit":3}` || edited != `["dome.go"]` {
		t.Fatalf("row = %q %d %s %s", title, turns, tools, edited)
	}
	if hits, _ := db.Search(ctx, "constellation", "alpha", "session", 5); len(hits) != 1 || hits[0].Ref != "session:"+s.ID {
		t.Fatalf("session text search = %+v", hits)
	}

	// The session moves to another project: the search row moves with it, no duplicate.
	s.ProjectID = "beta"
	s.Title = "Build the dome v2"
	if err := db.UpsertSessions(ctx, []Session{s}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search WHERE kind = 'session'`); n != 1 {
		t.Fatalf("session search rows = %d", n)
	}
	if hits, _ := db.Search(ctx, "dome v2", "beta", "session", 5); len(hits) != 1 {
		t.Fatal("session should be searchable under the new project")
	}

	links := []SessionProject{{ProjectID: "beta", Reason: "cwd"}, {ProjectID: "gamma", Reason: "mention"}, {ProjectID: "delta"}}
	if err := db.SetSessionProjects(ctx, s.ID, links); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSessionProjects(ctx, s.ID, links[:2]); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM session_projects WHERE session_id = ?`, s.ID); n != 2 {
		t.Fatalf("session_projects = %d", n)
	}

	if err := db.DeleteSession(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSession(ctx, s.ID); err != nil {
		t.Fatalf("deleting twice must be fine: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sessions`) + count(t, db, `SELECT COUNT(*) FROM session_projects`) + count(t, db, `SELECT COUNT(*) FROM search`); n != 0 {
		t.Fatalf("rows left: %d", n)
	}
}

func TestServersNowAndRuns(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	if err := db.ReplaceServersNow(ctx, []ServerNow{
		{Key: "k1", Port: 5173, PID: 11, Name: "vite", Cmd: "node vite", CWD: "/p", ProjectID: "alpha", Kind: "dev", FirstSeen: time.Unix(10, 0), LastSeen: time.Unix(20, 0)},
		{Key: "k2", Port: 9000, ProjectID: ""},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceServersNow(ctx, []ServerNow{{Key: "k3", Port: 8080, ProjectID: "alpha"}}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM servers_now`); n != 1 {
		t.Fatalf("servers_now = %d, want 1 (replaced)", n)
	}
	if k := count(t, db, `SELECT COUNT(*) FROM servers_now WHERE kind = 'dev'`); k != 1 {
		t.Fatal("kind should default to dev")
	}

	// runs
	id1, err := db.AppendServerRun(ctx, ServerRun{Key: "k1", ProjectID: "alpha", Port: 5173, PID: 11, Name: "vite", Cmd: "node vite",
		CWD: "/p", Kind: "dev", Started: time.Unix(1000, 0), LastSeen: time.Unix(1060, 0)})
	if err != nil || id1 == 0 {
		t.Fatalf("append: %d %v", id1, err)
	}
	id2, _ := db.AppendServerRun(ctx, ServerRun{Key: "k2", Port: 9000, Kind: "helper", Started: time.Unix(500, 0)})
	if err := db.EndServerRun(ctx, id1, time.Unix(2000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := db.EndServerRun(ctx, id1, time.Unix(3000, 0)); err != nil { // already ended: ignored
		t.Fatal(err)
	}
	runs, err := db.ListServerRuns(ctx)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	// oldest first
	if runs[0].ID != id2 || runs[1].ID != id1 {
		t.Fatalf("order = %d, %d", runs[0].ID, runs[1].ID)
	}
	r1 := runs[1]
	if r1.Kind != "dev" || !r1.Started.Equal(time.Unix(1000, 0)) || !r1.Ended.Equal(time.Unix(2000, 0)) ||
		!r1.LastSeen.Equal(time.Unix(1060, 0)) || r1.Name != "vite" || r1.CWD != "/p" || r1.PID != 11 {
		t.Fatalf("run = %+v", r1)
	}
	if runs[0].Kind != "helper" || !runs[0].Ended.IsZero() || !runs[0].LastSeen.IsZero() {
		t.Fatalf("open run = %+v", runs[0])
	}

	// ReplaceServerRuns round trip keeps ids and every field.
	if err := db.ReplaceServerRuns(ctx, runs); err != nil {
		t.Fatal(err)
	}
	again, _ := db.ListServerRuns(ctx)
	if !reflect.DeepEqual(again, runs) {
		t.Fatalf("round trip changed rows:\n got %+v\nwant %+v", again, runs)
	}
	// New ids when the caller gives none.
	if err := db.ReplaceServerRuns(ctx, []ServerRun{{Key: "n1", Started: time.Unix(1, 0)}, {Key: "n2", Started: time.Unix(2, 0)}}); err != nil {
		t.Fatal(err)
	}
	again, _ = db.ListServerRuns(ctx)
	if len(again) != 2 || again[0].ID == 0 || again[0].ID == again[1].ID {
		t.Fatalf("runs = %+v", again)
	}
}

func TestClaudeFile(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	if _, ok, err := db.GetClaudeFile(ctx, "/nope"); ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}
	in := ClaudeFile{Path: "/a/b.jsonl", Size: 1234, MTime: time.Unix(1700000000, 123456789), Offset: 1200, Inode: 1<<40 + 5}
	if err := db.PutClaudeFile(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, ok, err := db.GetClaudeFile(ctx, in.Path)
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	if out.Size != in.Size || !out.MTime.Equal(in.MTime) || out.Offset != in.Offset || out.Inode != in.Inode || out.Path != in.Path {
		t.Fatalf("got %+v want %+v", out, in)
	}
	in.Size, in.Offset = 2000, 2000
	_ = db.PutClaudeFile(ctx, in)
	out, _, _ = db.GetClaudeFile(ctx, in.Path)
	if out.Size != 2000 || out.Offset != 2000 {
		t.Fatalf("update: %+v", out)
	}
	if err := db.DeleteClaudeFile(ctx, in.Path); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.GetClaudeFile(ctx, in.Path); ok {
		t.Fatal("should be gone")
	}
}

func TestAuthoredRoundTripAndSearch(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	x, y, line := 0.46, 0.2, 30
	steps := []error{
		db.UpsertAuthoredMilestone(ctx, Milestone{ProjectID: "alpha", ID: "M-1", Title: "Chambers v3", Due: "2026-10-05", Body: "All three relic chambers reviewed."}),
		db.UpsertAuthoredFeature(ctx, Feature{ProjectID: "alpha", ID: "F-1", Title: "Relic chambers", MilestoneID: "M-1", Body: "Philosophy, diary, voyage."}),
		db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-1", Title: "Philosophy chamber", Status: "review", FeatureID: "F-1",
			Created: "2026-09-29T13:10:00+05:30", Updated: "2026-09-30T12:02:00+05:30", Body: "The dome should feel like the night sky."}),
		db.UpsertAuthoredSnapshot(ctx, Snapshot{ProjectID: "alpha", TaskID: "T-1", Version: 3, Title: "Dome", Captured: "2026-09-30T12:02:00+05:30"}),
		db.UpsertAuthoredNote(ctx, Note{ProjectID: "alpha", ID: "N-40", Ref: "snapshot:T-1/v3", X: &x, Y: &y, Line: &line,
			Author: "alice", Created: "2026-09-30T13:00:00+05:30", Body: "Constellation lines slightly too bright.\nSecond line."}),
	}
	for i, err := range steps {
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	var status, verdict string
	_ = db.Read().QueryRow(`SELECT status FROM tasks WHERE id = 'T-1'`).Scan(&status)
	_ = db.Read().QueryRow(`SELECT verdict FROM snapshots WHERE task_id = 'T-1' AND version = 3`).Scan(&verdict)
	if status != "review" || verdict != "pending" {
		t.Fatalf("status %q verdict %q", status, verdict)
	}
	var gx, gy sql.NullFloat64
	var gl sql.NullInt64
	_ = db.Read().QueryRow(`SELECT x, y, line FROM notes WHERE id = 'N-40'`).Scan(&gx, &gy, &gl)
	if gx.Float64 != 0.46 || gy.Float64 != 0.2 || gl.Int64 != 30 {
		t.Fatalf("note pin = %v %v %v", gx, gy, gl)
	}

	cases := map[string]string{"Chambers v3": "milestone:M-1", "Relic chambers": "feature:F-1", "night sky": "task:T-1", "too bright": "note:N-40"}
	for q, want := range cases {
		hits, _ := db.Search(ctx, q, "alpha", "", 3)
		found := false
		for _, h := range hits {
			if h.Ref == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Search(%q) did not find %s: %+v", q, want, hits)
		}
	}
	// Task search time comes from "updated".
	hits, _ := db.Search(ctx, "night sky", "", "task", 3)
	if len(hits) != 1 || !hits[0].TS.Equal(time.Date(2026, 9, 30, 12, 2, 0, 0, time.FixedZone("", 5*3600+1800))) {
		t.Errorf("task hit time = %+v", hits)
	}

	// Update: text changes, FTS follows, no duplicates.
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-1", Title: "Philosophy chamber", Status: "done", Body: "Shipped."})
	if hits, _ := db.Search(ctx, "night sky", "", "", 3); len(hits) != 0 {
		t.Error("old task text must leave search")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search WHERE kind = 'task'`); n != 1 {
		t.Errorf("task search rows = %d", n)
	}

	// Delete.
	for _, d := range [][2]string{{"milestone", "M-1"}, {"feature", "F-1"}, {"task", "T-1"}, {"snapshot", "T-1/v3"}, {"note", "N-40"}} {
		if err := db.DeleteAuthored(ctx, "alpha", d[0], d[1]); err != nil {
			t.Fatalf("delete %v: %v", d, err)
		}
	}
	for _, tbl := range []string{"milestones", "features", "tasks", "snapshots", "notes", "search", "search_ref"} {
		if n := count(t, db, `SELECT COUNT(*) FROM `+tbl); n != 0 {
			t.Errorf("%s has %d rows after delete", tbl, n)
		}
	}
	if err := db.DeleteAuthored(ctx, "alpha", "snapshot", "bad"); err == nil {
		t.Error("a bad snapshot id must be an error")
	}
	if err := db.DeleteAuthored(ctx, "alpha", "weird", "x"); err == nil {
		t.Error("an unknown kind must be an error")
	}
}

func TestTaskLinksManualAndAuto(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-1", Title: "x"})
	if err := db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "manual", []TaskLink{{Ref: "commit:abc1234"}, {Ref: "file:a.go#L3"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "auto", []TaskLink{
		{Ref: "commit:abc1234", Reason: "commit message"}, // same ref as a manual one: manual wins
		{Ref: "session:s1", Reason: "mentioned"},
	}); err != nil {
		t.Fatal(err)
	}
	type row struct{ ref, source, reason string }
	read := func() []row {
		rows, _ := db.Read().Query(`SELECT ref, source, reason FROM task_links WHERE task_id = 'T-1' ORDER BY ref`)
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			_ = rows.Scan(&r.ref, &r.source, &r.reason)
			out = append(out, r)
		}
		return out
	}
	want := []row{{"commit:abc1234", "manual", ""}, {"file:a.go#L3", "manual", ""}, {"session:s1", "auto", "mentioned"}}
	if got := read(); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %v, want %v", got, want)
	}
	// Replacing the auto set leaves manual links alone.
	_ = db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "auto", nil)
	if got := read(); len(got) != 2 {
		t.Fatalf("after clearing auto: %v", got)
	}
	// Replacing manual with a set that includes an auto ref turns it manual.
	_ = db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "auto", []TaskLink{{Ref: "server:8090", Reason: "r"}})
	_ = db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "manual", []TaskLink{{Ref: "server:8090"}})
	got := read()
	if len(got) != 1 || got[0].ref != "server:8090" || got[0].source != "manual" {
		t.Fatalf("links = %v", got)
	}
	if err := db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "bogus", nil); err == nil {
		t.Fatal("bad source must be an error")
	}
	// Deleting a task removes its links.
	_ = db.DeleteAuthored(ctx, "alpha", "task", "T-1")
	if n := count(t, db, `SELECT COUNT(*) FROM task_links`); n != 0 {
		t.Fatalf("links left: %d", n)
	}
}

func TestEventsAreIdempotentAndDayIsDerived(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	e := Event{ProjectID: "alpha", TS: at("2026-09-30", 10, 0), Day: "2026-09-30", Kind: "commit", Ref: "commit:abc", Label: "msg", Meta: json.RawMessage(`{"plus":3}`)}
	added, err := db.AddEvent(ctx, e)
	if err != nil || !added {
		t.Fatalf("first add: %v %v", added, err)
	}
	added, err = db.AddEvent(ctx, e)
	if err != nil || added {
		t.Fatalf("second add must be ignored: %v %v", added, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM events`); n != 1 {
		t.Fatalf("events = %d", n)
	}
	// Same ref and kind at another time is a new event.
	e2 := e
	e2.TS = at("2026-09-30", 11, 0)
	if added, _ := db.AddEvent(ctx, e2); !added {
		t.Fatal("different ts is a new event")
	}
	// No day: derived from TS (in time.Local).
	e3 := Event{ProjectID: "alpha", TS: time.Date(2026, 9, 15, 12, 0, 0, 0, time.Local), Kind: "note", Ref: "note:N-1"}
	_, _ = db.AddEvent(ctx, e3)
	var day, meta string
	_ = db.Read().QueryRow(`SELECT day, meta FROM events WHERE ref = 'note:N-1'`).Scan(&day, &meta)
	if day != "2026-09-15" || meta != "{}" {
		t.Fatalf("day %q meta %q", day, meta)
	}
	if _, err := db.AddEvent(ctx, Event{ProjectID: "alpha", TS: time.Unix(1, 0), Kind: "x", Meta: json.RawMessage(`{bad`)}); err == nil {
		t.Fatal("bad meta JSON must be an error")
	}
}

func TestAddFileChangesCountsAndValidatesDay(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	if err := db.AddFileChanges(ctx, "alpha", "2026-09-30", []string{"a.go", "b.go", "a.go", ""}); err != nil {
		t.Fatal(err)
	}
	_ = db.AddFileChanges(ctx, "alpha", "2026-09-30", []string{"a.go"})
	var n int
	_ = db.Read().QueryRow(`SELECT n FROM file_changes WHERE path = 'a.go'`).Scan(&n)
	if n != 3 {
		t.Fatalf("a.go n = %d, want 3", n)
	}
	if c := count(t, db, `SELECT COUNT(*) FROM file_changes`); c != 2 {
		t.Fatalf("rows = %d, want 2 distinct paths", c)
	}
	if err := db.AddFileChanges(ctx, "alpha", "30-09-2026", []string{"a.go"}); err == nil {
		t.Fatal("bad day must be an error")
	}
}

func TestRecomputeDaily(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	useClock(t, at("2026-10-02", 12, 0))
	day := "2026-09-30"
	proj := "alpha"

	_ = db.AddFileChanges(ctx, proj, day, []string{"a.go", "b.go", "c.go"})
	_ = db.ReplaceCommits(ctx, proj, []probe.Commit{
		{ID: "c1", Time: at(day, 0, 5), Msg: "just after midnight", Plus: 10, Minus: 2},
		{ID: "c2", Time: at(day, 23, 59), Msg: "just before midnight", Plus: 5, Minus: 1},
		{ID: "c3", Time: at("2026-10-01", 0, 0), Msg: "next day", Plus: 100, Minus: 100},
		{ID: "c0", Time: at("2026-09-29", 23, 59), Msg: "day before", Plus: 100, Minus: 100},
	})
	// Session A is inside the day (90 min). Session B starts the day before and ends at 01:00 (60 min on the day).
	// Session C is another project. Session D has no end (counts, 0 minutes).
	_ = db.UpsertSessions(ctx, []Session{
		{ID: "A", ProjectID: proj, Start: at(day, 9, 0), End: at(day, 10, 30)},
		{ID: "B", ProjectID: proj, Start: at("2026-09-29", 22, 0), End: at(day, 1, 0)},
		{ID: "C", ProjectID: "other", Start: at(day, 9, 0), End: at(day, 10, 0)},
		{ID: "D", ProjectID: proj, Start: at(day, 15, 0)},
		{ID: "E", ProjectID: proj, Start: at("2026-10-01", 0, 0), End: at("2026-10-01", 3, 0)}, // next day
	})
	_ = db.UpsertAuthoredSnapshot(ctx, Snapshot{ProjectID: proj, TaskID: "T-1", Version: 1, Captured: "2026-09-30T12:00:00+05:30"})
	_ = db.UpsertAuthoredSnapshot(ctx, Snapshot{ProjectID: proj, TaskID: "T-1", Version: 2, Captured: "2026-10-01T12:00:00+05:30"})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: proj, ID: "N-1", Created: "2026-09-30T08:00:00+05:30", Body: "a"})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: proj, ID: "N-2", Created: "2026-09-30T23:00:00+05:30", Body: "b"})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: proj, ID: "N-3", Created: "not a time", Body: "c"})
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: proj, ID: "T-1", Updated: "2026-09-30T14:00:00+05:30"})
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: proj, ID: "T-2", Updated: "2026-09-29T14:00:00+05:30"})
	// Servers: two overlapping runs 10:00-11:00 and 10:30-12:00 (union 120 min), plus one still running that
	// started yesterday (to "now" = 2026-10-02 12:00, so the whole day: but it covers everything then).
	_, _ = db.AppendServerRun(ctx, ServerRun{Key: "s1", ProjectID: proj, Started: at(day, 10, 0), Ended: at(day, 11, 0)})
	_, _ = db.AppendServerRun(ctx, ServerRun{Key: "s2", ProjectID: proj, Started: at(day, 10, 30), Ended: at(day, 12, 0)})
	_, _ = db.AppendServerRun(ctx, ServerRun{Key: "s3", ProjectID: "other", Started: at(day, 1, 0), Ended: at(day, 23, 0)})

	if err := db.RecomputeDaily(ctx, proj, day, testLoc); err != nil {
		t.Fatal(err)
	}
	got := dailyRow(t, db, proj, day)
	want := DayCell{Files: 3, Commits: 2, LinesPlus: 15, LinesMinus: 3, Sessions: 3, SessionMinutes: 90 + 60,
		Snapshots: 1, Notes: 2, ServerMinutes: 120, TasksMoved: 1}
	if got != want {
		t.Fatalf("daily row\n got %+v\nwant %+v", got, want)
	}

	// A server that never ended counts until now.
	id, _ := db.AppendServerRun(ctx, ServerRun{Key: "s4", ProjectID: proj, Started: at("2026-09-29", 20, 0)})
	_ = db.RecomputeDaily(ctx, proj, day, testLoc)
	if got := dailyRow(t, db, proj, day); got.ServerMinutes != 24*60 {
		t.Fatalf("open run should cover the whole day, got %d min", got.ServerMinutes)
	}
	_ = db.EndServerRun(ctx, id, at(day, 0, 30))
	_ = db.RecomputeDaily(ctx, proj, day, testLoc)
	if got := dailyRow(t, db, proj, day); got.ServerMinutes != 30+120 {
		t.Fatalf("server minutes = %d, want 150", got.ServerMinutes)
	}

	// Recomputing is stable.
	again := dailyRow(t, db, proj, day)
	_ = db.RecomputeDaily(ctx, proj, day, testLoc)
	if dailyRow(t, db, proj, day) != again {
		t.Fatal("RecomputeDaily is not stable")
	}

	// An empty day has no row, and an old row is removed.
	_ = db.RecomputeDaily(ctx, proj, "2026-09-10", testLoc)
	if n := count(t, db, `SELECT COUNT(*) FROM daily_activity WHERE day = '2026-09-10'`); n != 0 {
		t.Fatal("empty day should have no row")
	}
	_, _ = db.Read(), 0
	mustWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO daily_activity(project_id, day, files) VALUES(?, '2026-09-10', 9)`, proj)
		return err
	})
	_ = db.RecomputeDaily(ctx, proj, "2026-09-10", testLoc)
	if n := count(t, db, `SELECT COUNT(*) FROM daily_activity WHERE day = '2026-09-10'`); n != 0 {
		t.Fatal("a stale row must be removed")
	}

	if err := db.RecomputeDaily(ctx, proj, "yesterday", testLoc); err == nil {
		t.Fatal("bad day must be an error")
	}
}

func dailyRow(t *testing.T, db *DB, project, day string) DayCell {
	t.Helper()
	var c DayCell
	err := db.Read().QueryRow(`
SELECT files, commits, lines_plus, lines_minus, sessions, session_minutes, snapshots, notes, server_minutes, tasks_moved
FROM daily_activity WHERE project_id = ? AND day = ?`, project, day).
		Scan(&c.Files, &c.Commits, &c.LinesPlus, &c.LinesMinus, &c.Sessions, &c.SessionMinutes, &c.Snapshots, &c.Notes, &c.ServerMinutes, &c.TasksMoved)
	if err != nil {
		t.Fatalf("daily row %s %s: %v", project, day, err)
	}
	return c
}

func TestSettingsAreMirroredAndRestored(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := bg()
	if _, ok, _ := db.GetSetting(ctx, "author"); ok {
		t.Fatal("no setting yet")
	}
	if err := db.SetSetting(ctx, "author", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, "hidden", `["venv","go"]`); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, "author", "Alex"); err != nil {
		t.Fatal(err)
	}
	all, _ := db.Settings(ctx)
	if !reflect.DeepEqual(all, map[string]string{"author": "Alex", "hidden": `["venv","go"]`}) {
		t.Fatalf("settings = %v", all)
	}

	// settings.json mirrors the table.
	raw, err := os.ReadFile(db.SettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]string
	if err := json.Unmarshal(raw, &file); err != nil || !reflect.DeepEqual(file, all) {
		t.Fatalf("settings.json = %s (%v)", raw, err)
	}
	if st, _ := os.Stat(db.SettingsPath()); st.Mode().Perm() != 0o600 {
		t.Errorf("settings.json mode = %v", st.Mode().Perm())
	}
	// No temp files are left behind.
	if m, _ := filepathGlob(dir, ".settings-*.json"); len(m) != 0 {
		t.Errorf("temp files left: %v", m)
	}

	if err := db.DeleteSetting(ctx, "hidden"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(db.SettingsPath())
	file = nil
	_ = json.Unmarshal(raw, &file)
	if !reflect.DeepEqual(file, map[string]string{"author": "Alex"}) {
		t.Fatalf("settings.json after delete = %s", raw)
	}
	db.Close()

	// The database is deleted. Settings come back from settings.json.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dbPathIn(dir) + suffix)
	}
	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if v, ok, _ := db2.GetSetting(ctx, "author"); !ok || v != "Alex" {
		t.Fatalf("restored author = %q, %v", v, ok)
	}

	// If the table already has rows, the file does not override it.
	_ = db2.SetSetting(ctx, "author", "changed")
	db2.Close()
	_ = os.WriteFile(db2.SettingsPath(), []byte(`{"author":"from file"}`), 0o600)
	db3, _ := Open(dir)
	defer db3.Close()
	if v, _, _ := db3.GetSetting(ctx, "author"); v != "changed" {
		t.Fatalf("database must win, got %q", v)
	}
}

func TestSettingsSurviveReindex(t *testing.T) {
	db := openTest(t)
	_ = db.SetSetting(bg(), "k", "v")
	if err := db.Reindex(bg()); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := db.GetSetting(bg(), "k"); !ok || v != "v" {
		t.Fatal("settings must survive Reindex")
	}
}

func TestBadSettingsFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(dir+"/"+SettingsFileName, []byte("{not json"), 0o600)
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("a bad settings.json must not stop Open: %v", err)
	}
	defer db.Close()
}

func TestMetaAndSweepLog(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	_ = db.SetMeta(ctx, "last_sweep_at", "1")
	_ = db.SetMeta(ctx, "last_sweep_at", "2")
	if v, ok, _ := db.GetMeta(ctx, "last_sweep_at"); !ok || v != "2" {
		t.Fatalf("meta = %q %v", v, ok)
	}
	if _, ok, _ := db.GetMeta(ctx, "nope"); ok {
		t.Fatal("missing meta")
	}

	useClock(t, time.Unix(5000, 0))
	for i := 0; i < 3; i++ {
		if err := db.AddSweepLog(ctx, SweepLog{Scope: "all", MS: int64(10 * (i + 1)), OK: i != 1}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.RecentSweeps(ctx, 2)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	if rows[0].MS != 30 || !rows[0].OK || rows[1].MS != 20 || rows[1].OK || !rows[0].TS.Equal(time.Unix(5000, 0)) {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestSweepLogIsCapped(t *testing.T) {
	db := openTest(t)
	mustWrite(t, db, func(tx *sql.Tx) error {
		st, _ := tx.Prepare(`INSERT INTO sweep_log(ts, scope, ms, ok) VALUES(1, 'x', 1, 1)`)
		defer st.Close()
		for i := 0; i < maxSweepLog+50; i++ {
			if _, err := st.Exec(); err != nil {
				return err
			}
		}
		return nil
	})
	_ = db.AddSweepLog(bg(), SweepLog{Scope: "all", OK: true})
	if n := count(t, db, `SELECT COUNT(*) FROM sweep_log`); n != maxSweepLog {
		t.Fatalf("sweep_log rows = %d, want %d", n, maxSweepLog)
	}
}

func TestTxVariantsShareOneTransaction(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	// A failure after several helpers rolls ALL of them back, including the search rows.
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := UpsertProjectTx(ctx, tx, Project{ID: "alpha", Path: "/p/a"}); err != nil {
			return err
		}
		if err := ReplaceCommitsTx(ctx, tx, "alpha", []probe.Commit{{ID: "c1", Msg: "hello world", Time: time.Unix(5, 0)}}); err != nil {
			return err
		}
		if err := ReplaceFilesTx(ctx, tx, "alpha", []File{{Path: "a.go", MTime: time.Unix(1, 0)}}); err != nil {
			return err
		}
		return sql.ErrConnDone
	})
	if err == nil {
		t.Fatal("expected the error")
	}
	for _, tbl := range []string{"projects", "commits", "files", "search", "search_ref"} {
		if n := count(t, db, `SELECT COUNT(*) FROM `+tbl); n != 0 {
			t.Errorf("%s has %d rows after a rolled back transaction", tbl, n)
		}
	}
}

func filepathGlob(dir, pattern string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if ok, _ := filepath.Match(pattern, e.Name()); ok {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
