package authored

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// A hand-written file with things drashta does not know about.
const handFile = `---
# a comment at the top
id: T-3
title: Hand written
status: todo
priority: high              # unknown key with a comment
labels: [ui, "needs design"]
meta:
  owner: asha
  points: 3
feature: F-1
links:
  - commit:3f9a1c2d4e
created: 2026-09-28T09:30:00+05:30
---
First paragraph.

` + "```" + `
## Log
not a log, this is inside a code fence
` + "```" + `

Last paragraph.

## Log
- 2026-09-29T10:00+05:30 asha made it
- a free text line
`

func TestRoundTripKeepsUnknownKeysAndBody(t *testing.T) {
	root := t.TempDir()
	path := dotPath(root, "tasks", "T-3.md")
	writeFile(t, path, handFile)

	task, err := LoadTask(root, "T-3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(task.Body, "First paragraph.") || !strings.HasSuffix(task.Body, "Last paragraph.") || !strings.Contains(task.Body, "inside a code fence") {
		t.Fatalf("body was cut in the wrong place: %q", task.Body)
	}
	if len(task.Log) != 2 || task.Log[1].Raw != "- a free text line" || task.Log[1].Text != "a free text line" {
		t.Fatalf("log: %+v", task.Log)
	}

	task.Status = StatusInProgress
	task.Links = append(task.Links, "server:5173")
	task.Updated = "2026-10-01T10:00:00+05:30"
	task.AppendLog("2026-10-01T10:00+05:30", "Claude", "status todo -> in_progress")
	oldRev := task.Rev
	rev, err := Save(root, task, task.Rev)
	if err != nil {
		t.Fatal(err)
	}
	if rev == "" || rev == oldRev || task.Rev != rev {
		t.Fatalf("rev did not change: %q", rev)
	}
	got := readFile(t, path)

	// Unknown keys, their values, their comments and their order are kept.
	for _, want := range []string{
		"# a comment at the top\n",
		"priority: high              # unknown key with a comment\n",
		"labels: [ui, \"needs design\"]\n",
		"meta:\n  owner: asha\n  points: 3\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q in:\n%s", want, got)
		}
	}
	iPriority, iLabels, iMeta, iFeature := strings.Index(got, "priority:"), strings.Index(got, "labels:"), strings.Index(got, "meta:"), strings.Index(got, "feature:")
	if !(iPriority < iLabels && iLabels < iMeta && iMeta < iFeature) {
		t.Errorf("key order changed:\n%s", got)
	}
	// Changed keys changed.
	for _, want := range []string{"status: in_progress\n", "  - commit:3f9a1c2d4e\n  - server:5173\n", "updated: 2026-10-01T10:00:00+05:30\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The body and the old log lines are byte for byte the same; the new line is appended.
	bodyAndLog := handFile[strings.Index(handFile, "---\nFirst")+4:]
	if !strings.HasSuffix(got, bodyAndLog+"- 2026-10-01T10:00+05:30 Claude status todo -> in_progress\n") {
		t.Errorf("body or old log lines changed:\n%s", got)
	}
	// It is still valid YAML front matter.
	fm, _, _, err := splitFrontMatter([]byte(got))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(fm), &m); err != nil {
		t.Fatalf("front matter is not valid YAML: %v\n%s", err, fm)
	}

	// Load again: same values.
	again, err := LoadTask(root, "T-3")
	if err != nil {
		t.Fatal(err)
	}
	if again.Rev != rev || again.Status != StatusInProgress || len(again.Links) != 2 || len(again.Log) != 3 {
		t.Errorf("reloaded: %+v", again)
	}
}

func TestSaveWithoutChangesWritesNothing(t *testing.T) {
	root := fixtureCopy(t)
	path := dotPath(root, "tasks", "T-2.md")
	before := readFile(t, path)
	st1, _ := os.Stat(path)

	task, _ := LoadTask(root, "T-2")
	time.Sleep(10 * time.Millisecond)
	rev, err := Save(root, task, task.Rev)
	if err != nil {
		t.Fatal(err)
	}
	if rev != task.Rev {
		t.Errorf("rev changed without a change")
	}
	if after := readFile(t, path); after != before {
		t.Errorf("file changed:\n%s", after)
	}
	st2, _ := os.Stat(path)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("file was rewritten")
	}
}

func TestOnlyOneKeyChangesInTheFile(t *testing.T) {
	root := fixtureCopy(t)
	path := dotPath(root, "tasks", "T-2.md")
	before := strings.Split(readFile(t, path), "\n")
	if _, err := UpdateTask(root, "T-2", func(tk *Task) error { tk.Status = StatusDone; return nil }); err != nil {
		t.Fatal(err)
	}
	after := strings.Split(readFile(t, path), "\n")
	diff := 0
	for i := range before {
		if i < len(after) && before[i] != after[i] {
			diff++
			if !strings.HasPrefix(before[i], "status:") {
				t.Errorf("line %d changed: %q -> %q", i, before[i], after[i])
			}
		}
	}
	if len(before) != len(after) || diff != 1 {
		t.Errorf("want exactly one changed line, got %d (len %d -> %d)", diff, len(before), len(after))
	}
}

func TestConflictOnConcurrentEdit(t *testing.T) {
	root := fixtureCopy(t)
	a, _ := LoadTask(root, "T-3")
	b, _ := LoadTask(root, "T-3")

	a.Status = StatusReview
	if _, err := Save(root, a, a.Rev); err != nil {
		t.Fatal(err)
	}
	b.Title = "Edited by the other writer"
	_, err := Save(root, b, b.Rev)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	cur, _ := LoadTask(root, "T-3")
	if cur.Status != StatusReview || cur.Title != "Dark palette tokens" {
		t.Errorf("the losing write changed the file: %+v", cur)
	}

	// An edit by hand on disk (for example a Claude session) is a conflict too.
	path := dotPath(root, "tasks", "T-4.md")
	c, _ := LoadTask(root, "T-4")
	writeFile(t, path, strings.Replace(readFile(t, path), "status: todo", "status: in_progress", 1))
	c.Title = "Mine"
	if _, err := Save(root, c, c.Rev); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict after a hand edit, got %v", err)
	}
	if !strings.Contains(readFile(t, path), "status: in_progress") {
		t.Errorf("the hand edit was lost")
	}
}

func TestSaveConflictAfterRecreate(t *testing.T) {
	root := fixtureCopy(t)
	task, _ := LoadTask(root, "T-5")
	if err := Delete(root, "task", "T-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(root, task, task.Rev); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for a deleted item, got %v", err)
	}
}

func TestCreateNewItems(t *testing.T) {
	root := t.TempDir() // no .drashta yet: Save builds the layout
	id, err := NextID(root, "task")
	if err != nil || id != "T-1" {
		t.Fatalf("NextID: %q %v", id, err)
	}
	// The reserved id is an empty file; it is not loaded as an item.
	if tr, errs := LoadProject(root); len(tr.Tasks) != 0 || len(errs) != 0 {
		t.Fatalf("a reserved id must be invisible: %+v %v", tr.Tasks, errs)
	}
	task := &Task{ID: id, Title: "Philosophy chamber: star dome", Created: "2026-10-01T10:00:00+05:30", CreatedBy: "alice", Body: "The dome."}
	rev, err := Save(root, task, "")
	if err != nil {
		t.Fatal(err)
	}
	if task.Rev != rev || task.Status != StatusTodo {
		t.Errorf("rev/default status: %+v", task)
	}
	got := readFile(t, dotPath(root, "tasks", "T-1.md"))
	want := `---
id: T-1
title: 'Philosophy chamber: star dome'
status: todo
created: 2026-10-01T10:00:00+05:30
created_by: alice
links: []
unlinks: []
---
The dome.
`
	if got != want {
		t.Errorf("new task file:\n%s\nwant:\n%s", got, want)
	}

	// Creating the same id again fails.
	if _, err := Save(root, &Task{ID: "T-1", Title: "Again"}, ""); !errors.Is(err, ErrExists) {
		t.Errorf("want ErrExists, got %v", err)
	}
	// An id that was never created cannot be updated.
	if _, err := Save(root, &Task{ID: "T-5", Title: "Ghost"}, "abc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	// The layout exists.
	for _, f := range []string{"README.md", ".gitignore", ".lock"} {
		if _, err := os.Stat(dotPath(root, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}

	// Snapshot, note, feature and milestone files in the shape of 04 section 2.3.
	if _, err := Save(root, &Snapshot{Task: "T-1", Version: 1, Title: "Dome", Image: "v1.png", Source: "upload",
		Captured: "2026-10-01T10:05:00+05:30", Author: "Claude", Body: "First pass."}, ""); err != nil {
		t.Fatal(err)
	}
	wantSnap := `---
task: T-1
version: 1
title: Dome
image: v1.png
source: upload
captured: 2026-10-01T10:05:00+05:30
author: Claude
verdict: pending
reviewed_by: ""
reviewed_at: ""
review_note: ""
---
First pass.
`
	if got := readFile(t, dotPath(root, "snapshots", "T-1", "v1.md")); got != wantSnap {
		t.Errorf("snapshot file:\n%s", got)
	}
	nid, _ := NextID(root, "note")
	x, y := 0.46, 0.2
	if _, err := Save(root, &Note{ID: nid, Ref: "snapshot:T-1/v1", X: &x, Y: &y, Author: "alice",
		Created: "2026-10-01T11:00:00+05:30", Body: "Too bright."}, ""); err != nil {
		t.Fatal(err)
	}
	wantNote := `---
id: N-1
ref: snapshot:T-1/v1
x: 0.46
y: 0.2
author: alice
created: 2026-10-01T11:00:00+05:30
resolved: false
---
Too bright.
`
	if got := readFile(t, dotPath(root, "notes", "N-1.md")); got != wantNote {
		t.Errorf("note file:\n%s", got)
	}
	mid, _ := NextID(root, "milestone")
	if _, err := Save(root, &Milestone{ID: mid, Title: "Chambers v3", Due: "2026-10-05", Features: []string{"F-1", "F-2"}, Created: "2026-09-29T13:00:00+05:30", Body: "All reviewed."}, ""); err != nil {
		t.Fatal(err)
	}
	wantM := `---
id: M-1
title: Chambers v3
due: 2026-10-05
features: [F-1, F-2]
state: open
created: 2026-09-29T13:00:00+05:30
---
All reviewed.
`
	if got := readFile(t, dotPath(root, "milestones", "M-1.md")); got != wantM {
		t.Errorf("milestone file:\n%s", got)
	}
	fid, _ := NextID(root, "feature")
	if _, err := Save(root, Feature{ID: fid, Title: "Relic chambers", Milestone: "M-1"}, ""); err != nil {
		t.Fatal(err)
	}
	wantF := "---\nid: F-1\ntitle: Relic chambers\nmilestone: M-1\n---\n"
	if got := readFile(t, dotPath(root, "features", "F-1.md")); got != wantF {
		t.Errorf("feature file:\n%s", got)
	}

	// All of it loads back without a problem.
	tr, errs := LoadProject(root)
	if len(errs) != 0 || len(tr.Tasks) != 1 || len(tr.Snapshots) != 1 || len(tr.Notes) != 1 || len(tr.Milestones) != 1 || len(tr.Features) != 1 {
		t.Fatalf("reload: %+v %v", tr, errs)
	}
	if tr.Notes[0].X == nil || *tr.Notes[0].X != 0.46 {
		t.Errorf("pin lost: %+v", tr.Notes[0])
	}
}

func TestTitlesSurviveYAMLEdgeCases(t *testing.T) {
	titles := []string{
		"Plain title",
		"Colon: inside",
		"Hash # inside",
		"- starts with a dash",
		"'quoted' and \"double\"",
		"123",
		"true",
		"null",
		"2026-01-01",
		"@at and *star and &amp and !bang",
		"  padded  ",
		"Unicode: café ✓ — done",
		strings.Repeat("long ", 60) + "end",
		"tab\tinside",
		"{braces} [brackets] | pipe > fold",
		"percent % and backtick `",
		"yes",
		"~",
		"",
	}
	for i, title := range titles {
		root := t.TempDir()
		id, _ := NextID(root, "task")
		if title == "" {
			if _, err := Save(root, &Task{ID: id, Title: title}, ""); !errors.Is(err, ErrInvalid) {
				t.Errorf("empty title: want ErrInvalid, got %v", err)
			}
			continue
		}
		task := &Task{ID: id, Title: title, Assignee: title, Body: "body " + fmt.Sprint(i)}
		if _, err := Save(root, task, ""); err != nil {
			t.Errorf("%q: %v", title, err)
			continue
		}
		got, err := LoadTask(root, id)
		if err != nil {
			t.Errorf("%q: %v", title, err)
			continue
		}
		if got.Title != title || got.Assignee != title {
			t.Errorf("title %q came back as %q / %q", title, got.Title, got.Assignee)
		}
		// Changing it again and loading must also work.
		got.Title = title + "!"
		if _, err := Save(root, got, got.Rev); err != nil {
			t.Errorf("%q: second save: %v", title, err)
		}
	}
	if _, err := Save(t.TempDir(), &Task{ID: "T-1", Title: "two\nlines"}, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("multi-line title: %v", err)
	}
}

func TestMultilineValuesKeepTheirText(t *testing.T) {
	root := t.TempDir()
	if _, err := Save(root, &Snapshot{Task: "T-1", Version: 1, Title: "x", ReviewNote: "Line one.\nLine two: has a colon.\n  indented"}, ""); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSnapshot(root, "T-1/v1")
	if err != nil {
		t.Fatal(err)
	}
	if s.ReviewNote != "Line one.\nLine two: has a colon.\n  indented" {
		t.Errorf("review note: %q", s.ReviewNote)
	}
}

func TestLogIsAppendOnly(t *testing.T) {
	root := fixtureCopy(t)
	path := dotPath(root, "tasks", "T-2.md")
	before := readFile(t, path)
	_, err := UpdateTask(root, "T-2", func(tk *Task) error {
		tk.AppendLog("2026-10-01T09:00+05:30", "alice", "status review -> in_progress")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)
	logStart := strings.Index(before, "## Log\n")
	if !strings.HasPrefix(after[strings.Index(after, "## Log\n"):], before[logStart:]) {
		t.Errorf("old log lines changed:\n%s", after)
	}
	if !strings.HasSuffix(after, "- 2026-10-01T09:00+05:30 alice status review -> in_progress\n") {
		t.Errorf("new line missing:\n%s", after)
	}
	// A task without a log gets a "## Log" section after a blank line.
	if _, err := UpdateTask(root, "T-3", func(tk *Task) error {
		tk.AppendLog("2026-10-01T09:00+05:30", "Claude", "hello")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dotPath(root, "tasks", "T-3.md"))
	if !strings.HasSuffix(got, "Components come after.\n\n## Log\n- 2026-10-01T09:00+05:30 Claude hello\n") {
		t.Errorf("new log section:\n%s", got)
	}
	// And a task with an empty body.
	if _, err := UpdateTask(root, "T-4", func(tk *Task) error {
		tk.AppendLog("2026-10-01T09:00+05:30", "Claude", "hello")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dotPath(root, "tasks", "T-4.md")); !strings.HasSuffix(got, "---\n## Log\n- 2026-10-01T09:00+05:30 Claude hello\n") {
		t.Errorf("log on empty body:\n%s", got)
	}
}

func TestBodyEditKeepsLog(t *testing.T) {
	root := fixtureCopy(t)
	_, err := UpdateTask(root, "T-2", func(tk *Task) error { tk.Body = "New body."; return nil })
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dotPath(root, "tasks", "T-2.md"))
	if !strings.Contains(got, "---\nNew body.\n\n## Log\n- 2026-09-28T14:10+05:30 Claude status todo -> in_progress\n- 2026-09-30T12:02+05:30 Claude posted snapshot v2\n") {
		t.Errorf("body edit:\n%s", got)
	}
}

func TestUpdateSerializesConcurrentWriters(t *testing.T) {
	root := fixtureCopy(t)
	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := UpdateTask(root, "T-3", func(tk *Task) error {
				tk.AppendLog("2026-10-01T09:00+05:30", "w", fmt.Sprintf("line %d", i))
				return nil
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	tk, _ := LoadTask(root, "T-3")
	if len(tk.Log) != n {
		t.Fatalf("lost updates: %d of %d log lines", len(tk.Log), n)
	}
}

func TestSnapshotAndNotePatches(t *testing.T) {
	root := fixtureCopy(t)
	// verdict on a snapshot
	_, err := UpdateSnapshot(root, "T-2/v2", func(s *Snapshot) error {
		s.Verdict = VerdictApproved
		s.ReviewedBy = "alice"
		s.ReviewedAt = "2026-10-01T12:00:00+05:30"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dotPath(root, "snapshots", "T-2", "v2.md"))
	for _, want := range []string{"verdict: approved\n", "reviewed_by: alice\n", "reviewed_at: 2026-10-01T12:00:00+05:30\n", "review_note: \"\"\n", "Bigger count and a pulse while uploading.\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// resolve a note, and remove its pin
	_, err = UpdateNote(root, "N-1", func(n *Note) error {
		n.Resolved = false
		n.ResolvedBy, n.ResolvedAt = "", ""
		n.X, n.Y = nil, nil
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	n1, _ := LoadNote(root, "N-1")
	if n1.Resolved || n1.X != nil || n1.Y != nil || n1.ResolvedBy != "" {
		t.Errorf("note 1: %+v", n1)
	}
	got = readFile(t, dotPath(root, "notes", "N-1.md"))
	if !strings.Contains(got, "x: null\n") || !strings.Contains(got, "resolved: false\n") {
		t.Errorf("note file:\n%s", got)
	}
	// update must not change the id
	_, err = UpdateTask(root, "T-3", func(tk *Task) error { tk.ID = "T-9"; return nil })
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("changing the id: %v", err)
	}
}

func TestSaveValidation(t *testing.T) {
	root := t.TempDir()
	bad := []any{
		&Task{ID: "T-1", Title: "x", Status: "parked"},
		&Task{ID: "t-1", Title: "x"},
		&Task{ID: "../T-1", Title: "x"},
		&Task{ID: "T-1", Title: "x", Feature: "T-3"},
		&Snapshot{Task: "../x", Version: 1, Title: "x"},
		&Snapshot{Task: "T-1", Version: 0, Title: "x"},
		&Snapshot{Task: "T-1", Version: 1, Title: "x", Verdict: "maybe"},
		&Snapshot{Task: "T-1", Version: 1, Title: "x", Image: "../v1.png"},
		&Note{ID: "N-1", Ref: "bogus"},
		&Note{ID: "N-1", Ref: "task:T-1", X: ptrF(1.5), Y: ptrF(0.5)},
		&Note{ID: "N-1", Ref: "task:T-1", X: ptrF(0.5)},
		&Milestone{ID: "M-1", Title: "x", State: "paused"},
		"a string",
	}
	for i, item := range bad {
		if _, err := Save(root, item, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d (%T): want ErrInvalid, got %v", i, item, err)
		}
	}
	if _, err := os.Stat(dotPath(root, "tasks", "T-1.md")); err == nil {
		t.Errorf("an invalid item was written")
	}
}

func ptrF(f float64) *float64 { return &f }

func TestHostileValuesCannotBreakTheFile(t *testing.T) {
	root := t.TempDir()
	// A review note that looks like the end of the front matter and a fake key.
	note := "first\n---\nid: T-99\nstatus: done\n..."
	snap := &Snapshot{Task: "T-1", Version: 1, Title: "x", ReviewNote: note, Source: "a: b\n- c", Body: "---\nbody that starts with dashes\n## Log\nnot a task"}
	if _, err := Save(root, snap, ""); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSnapshot(root, "T-1/v1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewNote != note || got.Source != "a: b\n- c" || got.Body != snap.Body || got.Task != "T-1" || got.Verdict != VerdictPending {
		t.Errorf("snapshot changed on the way: %+v", got)
	}
	if tr, errs := LoadProject(root); len(errs) != 0 || len(tr.Snapshots) != 1 || len(tr.Tasks) != 0 {
		t.Errorf("%+v %v", tr, errs)
	}

	// A task body with a "## Log" line would be cut when the file is read back, so it is refused.
	bad := &Task{ID: "T-1", Title: "x", Body: "text\n## Log\nmore"}
	if _, err := Save(root, bad, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("body with ## Log: %v", err)
	}
	// ... unless it is inside a code fence.
	ok := &Task{ID: "T-1", Title: "x", Body: "text\n```\n## Log\n```\nmore"}
	if _, err := Save(root, ok, ""); err != nil {
		t.Errorf("fenced ## Log: %v", err)
	}
	// Log lines must be one line.
	for _, e := range []LogEntry{{Time: "2026-10-01T10:00+05:30", By: "a", Text: "x\n- evil"}, {Time: "t", By: "a b", Text: "x"}, {Time: "t\nx", By: "a", Text: "x"}} {
		tk := &Task{ID: "T-2", Title: "x", Log: []LogEntry{e}}
		if _, err := Save(root, tk, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("log entry %+v: %v", e, err)
		}
	}
	// White space around a body is cleaned, as it is when reading.
	tk := &Task{ID: "T-3", Title: "x", Body: "\n\n  text  \n\n"}
	if _, err := Save(root, tk, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadTask(root, "T-3"); got.Body != "  text" || tk.Body != "  text" {
		t.Errorf("body %q / %q", got.Body, tk.Body)
	}
}
