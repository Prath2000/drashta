package authored

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectFixture(t *testing.T) {
	root := fixtureCopy(t)
	tr, errs := LoadProject(root)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tr.Milestones) != 2 || len(tr.Features) != 3 || len(tr.Tasks) != 5 || len(tr.Snapshots) != 3 || len(tr.Notes) != 3 {
		t.Fatalf("counts: m=%d f=%d t=%d s=%d n=%d", len(tr.Milestones), len(tr.Features), len(tr.Tasks), len(tr.Snapshots), len(tr.Notes))
	}

	m := tr.Milestones[0]
	if m.ID != "M-1" || m.Title != "Lantern 1.0" || len(m.Features) != 2 || m.State != StateOpen {
		t.Errorf("milestone: %+v", m)
	}
	if m.Due != "2026-11-15" || m.Body != "Ship the first stable build with offline sync and dark mode." {
		t.Errorf("milestone due/body: %q %q", m.Due, m.Body)
	}

	task := tr.Tasks[1]
	if task.ID != "T-2" {
		t.Fatalf("tasks are not sorted: %s", task.ID)
	}
	if task.Title != "Sync status badge: show when notes are waiting to upload" {
		t.Errorf("title: %q", task.Title)
	}
	if task.Status != StatusReview || task.Feature != "F-1" || task.Assignee != "alice + Claude" || task.CreatedBy != "alice" {
		t.Errorf("task fields: %+v", task)
	}
	if task.Created != "2026-09-27T10:00:00+05:30" || task.Updated != "2026-09-30T12:02:00+05:30" {
		t.Errorf("task times: %q %q", task.Created, task.Updated)
	}
	if len(task.Links) != 2 || task.Links[0] != "file:src/ui/Badge.tsx#L12-L40" || len(task.Unlinks) != 1 {
		t.Errorf("links: %v unlinks: %v", task.Links, task.Unlinks)
	}
	if want := "The badge sits in the top bar. It shows a count while notes wait.\n\nIt must also work in dark mode."; task.Body != want {
		t.Errorf("body: %q", task.Body)
	}
	if len(task.Log) != 2 || task.Log[1].Time != "2026-09-30T12:02+05:30" || task.Log[1].By != "Claude" || task.Log[1].Text != "posted snapshot v2" {
		t.Errorf("log: %+v", task.Log)
	}

	// Rev is the sha256 of the file bytes.
	raw, _ := os.ReadFile(dotPath(root, "tasks", "T-2.md"))
	sum := sha256.Sum256(raw)
	if task.Rev != hex.EncodeToString(sum[:]) {
		t.Errorf("rev %s is not the sha256 of the file", task.Rev)
	}

	// Empty lists are [] and not nil, so JSON shows [].
	if tr.Tasks[4].Links == nil || tr.Tasks[4].Log == nil {
		t.Errorf("nil slices in %+v", tr.Tasks[4])
	}
	if tr.Tasks[3].Body != "" {
		t.Errorf("T-4 body should be empty, got %q", tr.Tasks[3].Body)
	}

	// Snapshots are sorted by task then version, and know their image.
	var ids []string
	for _, s := range tr.Snapshots {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "T-1/v1,T-2/v1,T-2/v2" {
		t.Errorf("snapshot ids: %v", ids)
	}
	s := tr.Snapshots[1]
	if s.Verdict != VerdictChanges || s.ReviewNote != "The count is too small to read." || s.Image != "v1.png" || s.ImageRel() != ".drashta/snapshots/T-2/v1.png" {
		t.Errorf("snapshot: %+v", s)
	}
	if s2 := tr.Snapshots[2]; s2.ReviewedBy != "" || s2.Verdict != VerdictPending || s2.Body != "Bigger count and a pulse while uploading." {
		t.Errorf("snapshot v2: %+v", s2)
	}

	// Notes.
	n1 := tr.Notes[0]
	if n1.Ref != "snapshot:T-2/v1" || n1.X == nil || *n1.X != 0.46 || n1.Y == nil || *n1.Y != 0.2 || !n1.Resolved || n1.ResolvedBy != "Claude" {
		t.Errorf("note 1: %+v", n1)
	}
	n2 := tr.Notes[1]
	if n2.Line == nil || *n2.Line != 20 || n2.Anchor != "const size = 8;" || n2.Resolved || n2.X != nil {
		t.Errorf("note 2: %+v", n2)
	}
	if n3 := tr.Notes[2]; n3.ReplyTo != "N-2" || n3.Body != "Checked: the spacing token is 12. I will switch to it." {
		t.Errorf("note 3: %+v", n3)
	}
}

func TestLoadProjectNoDrashtaFolder(t *testing.T) {
	tr, errs := LoadProject(t.TempDir())
	if len(errs) != 0 || len(tr.Tasks) != 0 || tr.Tasks == nil {
		t.Fatalf("want an empty tracker, got %+v %v", tr, errs)
	}
}

func TestLoadProjectBadFileIsolation(t *testing.T) {
	root := fixtureCopy(t)
	// 1. broken YAML
	writeFile(t, dotPath(root, "tasks", "T-7.md"), "---\nid: T-7\ntitle: [unclosed\n---\nbody\n")
	// 2. no front matter at all
	writeFile(t, dotPath(root, "tasks", "T-8.md"), "just text\n")
	// 3. front matter never closed
	writeFile(t, dotPath(root, "notes", "N-9.md"), "---\nid: N-9\nref: task:T-1\n")
	// 4. wrong value type
	writeFile(t, dotPath(root, "snapshots", "T-3", "v1.md"), "---\ntask: T-3\nversion: banana\n---\n")
	// 5. a file name that is not an id
	writeFile(t, dotPath(root, "tasks", "notes.md"), "---\nid: x\n---\n")
	// 6. id differs from the file name: loaded, with a warning
	writeFile(t, dotPath(root, "tasks", "T-6.md"), "---\nid: T-60\ntitle: Renamed by hand\nstatus: todo\n---\n")
	// 7. empty file = reserved id, silently skipped
	writeFile(t, dotPath(root, "tasks", "T-10.md"), "")
	// 8. a bad status loads with a warning
	writeFile(t, dotPath(root, "tasks", "T-11.md"), "---\nid: T-11\ntitle: Odd status\nstatus: parked\n---\n")
	// 9. the drop folder and other folders are ignored
	writeFile(t, dotPath(root, "snapshots", "inbox", "T-2--shot.png"), "x")

	tr, errs := LoadProject(root)

	// All the good items are still there.
	if len(tr.Tasks) != 5+2 { // 5 fixture tasks + T-6 + T-11
		var ids []string
		for _, x := range tr.Tasks {
			ids = append(ids, x.ID)
		}
		t.Fatalf("tasks: %v", ids)
	}
	if len(tr.Notes) != 3 || len(tr.Snapshots) != 3 {
		t.Fatalf("notes=%d snapshots=%d", len(tr.Notes), len(tr.Snapshots))
	}
	var t6 *Task
	for i := range tr.Tasks {
		if tr.Tasks[i].ID == "T-6" {
			t6 = &tr.Tasks[i]
		}
	}
	if t6 == nil || t6.Title != "Renamed by hand" {
		t.Fatalf("T-6 not loaded by file name: %+v", t6)
	}

	var hard, warn int
	paths := map[string]bool{}
	for _, err := range errs {
		var fe *FileError
		if !errors.As(err, &fe) {
			t.Fatalf("error is not a *FileError: %v", err)
		}
		paths[fe.Path] = true
		if fe.Warn {
			warn++
		} else {
			hard++
		}
	}
	for _, want := range []string{
		".drashta/tasks/T-7.md", ".drashta/tasks/T-8.md", ".drashta/notes/N-9.md",
		".drashta/snapshots/T-3/v1.md", ".drashta/tasks/notes.md", ".drashta/tasks/T-6.md", ".drashta/tasks/T-11.md",
	} {
		if !paths[want] {
			t.Errorf("no error reported for %s (got %v)", want, errs)
		}
	}
	if paths[".drashta/tasks/T-10.md"] {
		t.Errorf("an empty reserved file should not be reported")
	}
	if hard != 4 || warn != 3 {
		t.Errorf("hard=%d warn=%d, errors: %v", hard, warn, errs)
	}
}

func TestLoadSingleItems(t *testing.T) {
	root := fixtureCopy(t)
	tk, err := LoadTask(root, "T-2")
	if err != nil || tk.ID != "T-2" || tk.Rev == "" {
		t.Fatalf("%+v %v", tk, err)
	}
	sn, err := LoadSnapshot(root, "T-2/v2")
	if err != nil || sn.Version != 2 || sn.ID != "T-2/v2" {
		t.Fatalf("%+v %v", sn, err)
	}
	if _, err := LoadTask(root, "T-99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing task: %v", err)
	}
	if _, err := LoadTask(root, "../x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id: %v", err)
	}
	if v, err := LoadItem(root, "notes", "N-1"); err != nil {
		t.Errorf("LoadItem: %v", err)
	} else if _, ok := v.(*Note); !ok {
		t.Errorf("LoadItem returned %T", v)
	}
}

func TestLoadRefusesDrashtaSymlinkedOutside(t *testing.T) {
	outside := t.TempDir()
	copyTree(t, filepath.Join(fixtureDir, ".drashta"), outside)
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".drashta")); err != nil {
		t.Fatal(err)
	}
	tr, errs := LoadProject(root)
	if len(tr.Tasks) != 0 || len(errs) == 0 {
		t.Fatalf("a .drashta that points outside the project must not be read: %d tasks, %v", len(tr.Tasks), errs)
	}
	if _, err := Save(root, &Task{ID: "T-1", Title: "x"}, ""); err == nil {
		t.Errorf("Save must refuse a .drashta that points outside the project")
	}
}

func TestLoadSkipsFileSymlinkedOutside(t *testing.T) {
	root := fixtureCopy(t)
	secret := filepath.Join(t.TempDir(), "T-9.md")
	writeFile(t, secret, "---\nid: T-9\ntitle: Outside\n---\n")
	if err := os.Symlink(secret, dotPath(root, "tasks", "T-9.md")); err != nil {
		t.Fatal(err)
	}
	tr, errs := LoadProject(root)
	for _, x := range tr.Tasks {
		if x.ID == "T-9" {
			t.Fatalf("loaded a file outside the project")
		}
	}
	if len(errs) != 1 {
		t.Errorf("want one error, got %v", errs)
	}
}

func TestClassify(t *testing.T) {
	root := "/proj"
	cases := []struct {
		path, kind, id string
		ok             bool
	}{
		{"/proj/.drashta/tasks/T-12.md", KindTask, "T-12", true},
		{".drashta/tasks/T-12.md", KindTask, "T-12", true},
		{"/proj/.drashta/features/F-3.md", KindFeature, "F-3", true},
		{"/proj/.drashta/milestones/M-1.md", KindMilestone, "M-1", true},
		{"/proj/.drashta/notes/N-40.md", KindNote, "N-40", true},
		{"/proj/.drashta/snapshots/T-12/v3.md", KindSnapshot, "T-12/v3", true},
		{"/proj/.drashta/snapshots/T-12/v3.png", KindSnapshot, "T-12/v3", true},
		{"/proj/.drashta/snapshots/T-12/v3.exe", "", "", false},
		{"/proj/.drashta/snapshots/inbox/T-2--a.png", "inbox", "T-2--a.png", true},
		{"/proj/.drashta/tasks/readme.md", "", "", false},
		{"/proj/.drashta/tasks/.tmp-123", "", "", false},
		{"/proj/.drashta/.lock", "", "", false},
		{"/proj/src/main.go", "", "", false},
		{"/other/.drashta/tasks/T-1.md", "", "", false},
	}
	for _, c := range cases {
		k, id, ok := Classify(root, c.path)
		if k != c.kind || id != c.id || ok != c.ok {
			t.Errorf("Classify(%q) = %q %q %v, want %q %q %v", c.path, k, id, ok, c.kind, c.id, c.ok)
		}
	}
}
