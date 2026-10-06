package store

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// All data here is invented. The tests work in t.TempDir() through openTest.

func TestAPIProjGetAndCounts(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	if _, ok, err := db.ProjGet(ctx, "nope"); err != nil || ok {
		t.Fatalf("unknown project: ok=%v err=%v", ok, err)
	}
	err := db.UpsertProject(ctx, Project{ID: "alpha", Name: "alpha", Group: "g", Path: "/p/alpha", VCS: "git,fossil",
		Branch: "main", Stack: []string{"go"}, ReadmePath: "README.md", LastChangeAt: time.Unix(500, 0), IndexedAt: time.Unix(600, 0)})
	if err != nil {
		t.Fatal(err)
	}
	p, ok, err := db.ProjGet(ctx, "alpha")
	if err != nil || !ok || p.VCS != "git,fossil" || p.Branch != "main" || !reflect.DeepEqual(p.Stack, []string{"go"}) ||
		!p.LastChangeAt.Equal(time.Unix(500, 0)) || p.ReadmePath != "README.md" {
		t.Fatalf("ProjGet = %+v ok=%v err=%v", p, ok, err)
	}

	_ = db.ReplaceFiles(ctx, "alpha", []File{{Path: "a.go", Kind: "code"}, {Path: "README.md", Kind: "doc", Title: "Alpha", Lines: 3}})
	_ = db.ReplaceCommits(ctx, "alpha", []probe.Commit{{ID: "c1", Time: time.Unix(10, 0), Msg: "one"}, {ID: "c2", Time: time.Unix(20, 0), Msg: "two"}})
	_ = db.ReplaceDirty(ctx, "alpha", []probe.DirtyFile{{Path: "b.go", State: "M"}, {Path: "a.go", State: "?"}})
	_ = db.UpsertSessions(ctx, []Session{{ID: "s1", ProjectID: "alpha"}, {ID: "s2", ProjectID: "beta"}, {ID: "s3", ProjectID: "beta"}})
	_ = db.SetSessionProjects(ctx, "s2", []SessionProject{{ProjectID: "alpha", Reason: "mention"}})
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-1", Status: "todo"})
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-2", Status: "done"})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: "alpha", ID: "N-1", Ref: "file:a.go", Body: "open"})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: "alpha", ID: "N-2", Ref: "file:a.go", Body: "closed", Resolved: true})

	c, err := db.ProjCountsFor(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	want := ProjCounts{Files: 2, Commits: 2, Sessions: 2, OpenTasks: 1, OpenNotes: 1, Dirty: 2}
	if c != want {
		t.Fatalf("counts = %+v, want %+v", c, want)
	}

	dirty, _ := db.ProjDirtyFiles(ctx, "alpha")
	if len(dirty) != 2 || dirty[0].Path != "a.go" || dirty[1].State != "M" {
		t.Errorf("dirty = %+v", dirty)
	}
	if m, _ := db.ProjDirtyStates(ctx, "alpha"); m["a.go"] != "?" || m["b.go"] != "M" {
		t.Errorf("dirty states = %+v", m)
	}
	docs, _ := db.ProjDocs(ctx, "alpha")
	if len(docs) != 1 || docs[0].Path != "README.md" || docs[0].Title != "Alpha" || docs[0].Lines != 3 {
		t.Errorf("docs = %+v", docs)
	}
}

func TestAPIProjMilestones(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")
	_ = db.UpsertAuthoredMilestone(ctx, Milestone{ProjectID: "alpha", ID: "M-10", Title: "later", State: "open"})
	_ = db.UpsertAuthoredMilestone(ctx, Milestone{ProjectID: "alpha", ID: "M-2", Title: "second", State: "open"})
	_ = db.UpsertAuthoredMilestone(ctx, Milestone{ProjectID: "alpha", ID: "M-1", Title: "dated", Due: "2026-10-05", State: "open"})
	_ = db.UpsertAuthoredFeature(ctx, Feature{ProjectID: "alpha", ID: "F-2", Title: "b", MilestoneID: "M-1"})
	_ = db.UpsertAuthoredFeature(ctx, Feature{ProjectID: "alpha", ID: "F-10", Title: "c", MilestoneID: "M-1"})
	_ = db.UpsertAuthoredFeature(ctx, Feature{ProjectID: "alpha", ID: "F-3", Title: "none"})
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-1", FeatureID: "F-2", Status: "done"})
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-2", FeatureID: "F-10", Status: "review"})
	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-3", FeatureID: "F-3", Status: "todo"})

	ms, err := db.ProjMilestones(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	if !reflect.DeepEqual(ids, []string{"M-1", "M-2", "M-10"}) {
		t.Fatalf("order = %v", ids)
	}
	m := ms[0]
	if m.Due == nil || *m.Due != "2026-10-05" || !reflect.DeepEqual(m.FeatureIDs, []string{"F-2", "F-10"}) || m.Tasks != 2 || m.Done != 1 {
		t.Errorf("M-1 = %+v", m)
	}
	if ms[1].Due != nil || ms[1].FeatureIDs == nil || len(ms[1].FeatureIDs) != 0 {
		t.Errorf("M-2 = %+v", ms[1])
	}
	if none, err := db.ProjMilestones(ctx, "ghost"); err != nil || none == nil || len(none) != 0 {
		t.Errorf("no milestones should be an empty list, got %v %v", none, err)
	}
}

func TestAPIProjActivityZeroFilled(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")
	useClock(t, at("2026-09-30", 12, 0))
	mustWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO daily_activity(project_id, day, files, commits, lines_plus) VALUES('alpha','2026-09-29',4,2,9)`)
		return err
	})
	days, err := db.ProjActivity(ctx, "alpha", 7, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 7 || days[0].Day != "2026-09-24" || days[6].Day != "2026-09-30" {
		t.Fatalf("days = %+v", days)
	}
	if got := days[5]; got.Day != "2026-09-29" || got.Files != 4 || got.Commits != 2 || got.LinesPlus != 9 {
		t.Errorf("2026-09-29 = %+v", got)
	}
	if days[6].Files != 0 {
		t.Errorf("today should be a zero cell: %+v", days[6])
	}
	if d, _ := db.ProjActivity(ctx, "alpha", 0, testLoc); len(d) != 30 {
		t.Errorf("default range = %d, want 30", len(d))
	}
}

func TestAPIProjCommitsKeyset(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")
	var commits []probe.Commit
	for i := 1; i <= 5; i++ {
		commits = append(commits, probe.Commit{ID: "aa" + string(rune('0'+i)) + "0000000", VCS: "git",
			Time: time.Unix(int64(100+i), 0), Msg: "m", Plus: i})
	}
	commits = append(commits, probe.Commit{ID: "ff00000001", VCS: "fossil", Time: time.Unix(103, 0), Msg: "fossil"})
	if err := db.ReplaceCommits(ctx, "alpha", commits); err != nil {
		t.Fatal(err)
	}
	page1, err := db.ProjCommitsBefore(ctx, "alpha", ProjCursor{}, 3)
	if err != nil || len(page1) != 3 || page1[0].ID != "aa50000000" || page1[1].ID != "aa40000000" {
		t.Fatalf("page1 = %+v %v", page1, err)
	}
	last := page1[2] // ts 103, the id decides between aa3 and ff0
	cur := ProjCursor{Set: true, Unix: last.Time.Unix(), ID: last.ID}
	page2, err := db.ProjCommitsBefore(ctx, "alpha", cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range append(page1, page2...) {
		if seen[c.ID] {
			t.Fatalf("duplicate %s across pages", c.ID)
		}
		seen[c.ID] = true
	}
	if len(seen) != 6 {
		t.Errorf("pages together hold %d commits, want 6", len(seen))
	}
	counts, _ := db.ProjCommitCounts(ctx, "alpha")
	if counts["git"] != 5 || counts["fossil"] != 1 {
		t.Errorf("counts = %v", counts)
	}
	c, ok, err := db.ProjCommitByPrefix(ctx, "alpha", "ff0000")
	if err != nil || !ok || c.VCS != "fossil" {
		t.Errorf("prefix = %+v ok=%v err=%v", c, ok, err)
	}
	if _, ok, _ := db.ProjCommitByPrefix(ctx, "alpha", "dead"); ok {
		t.Error("unknown prefix should not match")
	}
}

func TestAPIProjSessionsAndTaskRefs(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")
	seedProject(t, db, "beta")
	err := db.UpsertSessions(ctx, []Session{
		{ID: "s-old", ProjectID: "alpha", Start: time.Unix(100, 0), End: time.Unix(200, 0), Title: "old", Turns: 4,
			Tools: map[string]int{"Edit": 2}, Edited: []string{"a.go"}, JSONLPath: "/x/s-old.jsonl", Profile: "p1"},
		{ID: "s-new", ProjectID: "alpha", Start: time.Unix(300, 0), End: time.Unix(400, 0), Title: "new"},
		{ID: "s-men", ProjectID: "beta", Start: time.Unix(250, 0), End: time.Unix(260, 0), Title: "mention"},
		{ID: "s-other", ProjectID: "beta", Start: time.Unix(1, 0), End: time.Unix(2, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.SetSessionProjects(ctx, "s-men", []SessionProject{{ProjectID: "beta", Reason: "cwd"}, {ProjectID: "alpha", Reason: "mention"}})

	all, err := db.ProjSessionsPage(ctx, "alpha", ProjCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids, reasons []string
	for _, s := range all {
		ids = append(ids, s.ID)
		reasons = append(reasons, s.Reason)
	}
	if !reflect.DeepEqual(ids, []string{"s-new", "s-men", "s-old"}) || !reflect.DeepEqual(reasons, []string{"cwd", "mention", "cwd"}) {
		t.Fatalf("sessions = %v reasons = %v", ids, reasons)
	}
	if all[2].Tools["Edit"] != 2 || !reflect.DeepEqual(all[2].Edited, []string{"a.go"}) {
		t.Errorf("tools/edited not decoded: %+v", all[2])
	}
	page, _ := db.ProjSessionsPage(ctx, "alpha", ProjCursor{Set: true, Unix: all[0].SortKey(), ID: all[0].ID}, 1)
	if len(page) != 1 || page[0].ID != "s-men" {
		t.Errorf("keyset page = %+v", page)
	}

	one, ok, err := db.ProjSessionByID(ctx, "s-old")
	if err != nil || !ok || one.JSONLPath != "/x/s-old.jsonl" || one.Reason != "cwd" {
		t.Errorf("ProjSessionByID = %+v ok=%v err=%v", one, ok, err)
	}
	if _, ok, _ := db.ProjSessionByID(ctx, "ghost"); ok {
		t.Error("unknown session should not be found")
	}

	_ = db.UpsertAuthoredTask(ctx, Task{ProjectID: "alpha", ID: "T-1"})
	_ = db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "auto", []TaskLink{
		{Ref: "commit:abc1234", Reason: "commit message"}, {Ref: "session:s-old"}, {Ref: "alpha:commit:def5678"}, {Ref: "file:a.go"},
	})
	refs, err := db.ProjTaskRefs(ctx, "alpha", "commit")
	if err != nil || len(refs) != 2 || refs[0].TaskID != "T-1" {
		t.Fatalf("commit refs = %+v %v", refs, err)
	}
	if refs, _ := db.ProjTaskRefs(ctx, "alpha", "session"); len(refs) != 1 || refs[0].Ref != "session:s-old" {
		t.Errorf("session refs = %+v", refs)
	}
}

func TestAPIProjFileNotesAndServerTitles(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	seedProject(t, db, "alpha")
	line := 12
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: "alpha", ID: "N-1", Ref: "file:src/x.go#L12", Body: "pinned", Author: "alice", Created: "2026-09-30T10:00:00+05:30", Line: &line})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: "alpha", ID: "N-2", Ref: "alpha:file:src/x.go", Body: "whole file", Created: "2026-09-30T11:00:00+05:30"})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: "alpha", ID: "N-3", Ref: "file:src/x.go.bak", Body: "other file"})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: "alpha", ID: "N-4", Ref: "file:src/x_go", Body: "like wildcard must not match"})
	_ = db.UpsertAuthoredNote(ctx, Note{ProjectID: "alpha", ID: "N-5", Ref: "file:src/y.go#L7", Body: "line from the ref only"})

	notes, err := db.ProjFileNotes(ctx, "alpha", "src/x.go")
	if err != nil || len(notes) != 2 || notes[0].ID != "N-1" || notes[1].ID != "N-2" {
		t.Fatalf("notes = %+v %v", notes, err)
	}
	if notes[0].Line == nil || *notes[0].Line != 12 || notes[0].Text != "pinned" || notes[0].ProjectID != "alpha" {
		t.Errorf("note 1 = %+v", notes[0])
	}
	if y, _ := db.ProjFileNotes(ctx, "alpha", "src/y.go"); len(y) != 1 || y[0].Line == nil || *y[0].Line != 7 {
		t.Errorf("line from ref fragment = %+v", y)
	}

	_ = db.ReplaceServersNow(ctx, []ServerNow{
		{Key: "/a|node x", Port: 3000, Title: "My app", ProjectID: "alpha"},
		{Key: "/b|node y", Port: 3001, ProjectID: "alpha"},
	})
	titles, err := db.ProjServerTitles(ctx)
	if err != nil || len(titles) != 1 || titles[0].Port != 3000 || titles[0].Title != "My app" {
		t.Errorf("titles = %+v %v", titles, err)
	}
}
