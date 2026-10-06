package httpapi

// Regression tests for the Wave 3 fix wave (fix-core): M14, I11 and I10.

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/store"
)

func fcFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// fcSnapshot stores a pending snapshot of a task through the authored API.
func fcSnapshot(t *testing.T, root, task string) string {
	t.Helper()
	v, rel, err := authored.AddSnapshotImage(root, task, strings.NewReader(trkPNG), ".png")
	if err != nil {
		t.Fatal(err)
	}
	s := &authored.Snapshot{Task: task, Version: v, Title: "shot", Image: path.Base(rel),
		Captured: "2026-09-30T10:00:00+05:30", Verdict: authored.VerdictPending}
	if _, err := authored.Save(root, s, ""); err != nil {
		t.Fatal(err)
	}
	return authored.SnapshotID(task, v)
}

// M14: a create that fails validation leaves no reserved (empty) id file behind.
func TestFixM14InvalidCreateReservesNoID(t *testing.T) {
	f := newTrkFix(t)
	d := filepath.Join(f.root, ".drashta")
	before := map[string][]string{}
	for _, k := range []string{"tasks", "features", "milestones", "notes"} {
		before[k] = fcFiles(t, filepath.Join(d, k))
	}
	trkCode(t, f.do("POST", "/api/projects/alpha/tasks", map[string]any{"title": "two\nlines"}), 422, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/tasks", map[string]any{"title": "x", "body": "a\n## Log\nforged"}), 422, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/features", map[string]any{"title": "two\nlines"}), 422, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/milestones", map[string]any{"title": "m", "state": "weird"}), 422, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/milestones", map[string]any{"title": "two\rlines"}), 422, "")
	for _, k := range []string{"tasks", "features", "milestones", "notes"} {
		if got := fcFiles(t, filepath.Join(d, k)); strings.Join(got, ",") != strings.Join(before[k], ",") {
			t.Errorf("%s: files %v after refused creates, want %v", k, got, before[k])
		}
	}
	// A good create still works and gets the next number.
	w := f.do("POST", "/api/projects/alpha/tasks", map[string]any{"title": "fine"})
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"T-3"`) {
		t.Fatalf("good create: %d %s", w.Code, w.Body.String())
	}
}

// M14: Release removes only an empty reserved file.
func TestFixM14ReleaseOnlyEmpty(t *testing.T) {
	f := newTrkFix(t)
	id, err := authored.NextID(f.root, authored.KindTask)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.root, ".drashta", "tasks", id+".md")
	if err := authored.Release(f.root, authored.KindTask, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("empty reserved file kept")
	}
	if err := authored.Release(f.root, authored.KindTask, "T-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.taskFile("T-1")); err != nil {
		t.Fatalf("Release removed a real task: %v", err)
	}
}

// I11: deleting a task moves its snapshots and its notes to the trash, mirrors each moved
// file, and the task no longer shows as awaiting review.
func TestFixI11DeleteTaskTakesSnapshotsAndNotes(t *testing.T) {
	f := newTrkFix(t)
	snap := fcSnapshot(t, f.root, "T-2")
	trkWrite(t, filepath.Join(f.root, ".drashta", "notes", "N-2.md"),
		"---\nid: N-2\nref: snapshot:"+snap+"\nauthor: alice\ncreated: 2026-09-30T11:00:00+05:30\nresolved: false\n---\nLooks off.\n")
	trkWrite(t, filepath.Join(f.root, ".drashta", "notes", "N-3.md"),
		"---\nid: N-3\nref: task:T-2\nauthor: alice\ncreated: 2026-09-30T11:05:00+05:30\nresolved: false\n---\nOn the task.\n")
	trkWrite(t, filepath.Join(f.root, ".drashta", "notes", "N-4.md"),
		"---\nid: N-4\nref: task:T-1\nauthor: alice\ncreated: 2026-09-30T11:06:00+05:30\nreply_to: N-3\nresolved: false\n---\nA reply.\n")

	var review trkTrackerReply
	ahDecode(t, f.do("GET", "/api/tracker?filter=review", nil), 200, &review)
	if len(review.Tasks) != 1 || review.Tasks[0].ID != "T-2" {
		t.Fatalf("before delete: review = %+v", review.Tasks)
	}

	if w := f.do("DELETE", "/api/projects/alpha/tasks/T-2", nil); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	d := filepath.Join(f.root, ".drashta")
	if _, err := os.Stat(filepath.Join(d, "snapshots", "T-2")); !os.IsNotExist(err) {
		t.Errorf("snapshots/T-2 still there: %v", fcFiles(t, filepath.Join(d, "snapshots", "T-2")))
	}
	if got := fcFiles(t, filepath.Join(d, ".trash", "snapshots", "T-2")); strings.Join(got, ",") != "v1.md,v1.png" {
		t.Errorf("trash snapshots = %v", got)
	}
	if got := fcFiles(t, filepath.Join(d, ".trash", "notes")); strings.Join(got, ",") != "N-2.md,N-3.md,N-4.md" {
		t.Errorf("trash notes = %v (N-1 is on T-1 and must stay)", got)
	}
	if _, err := os.Stat(filepath.Join(d, "notes", "N-1.md")); err != nil {
		t.Errorf("N-1 moved: %v", err)
	}
	for _, rel := range []string{".drashta/tasks/T-2.md", ".drashta/snapshots/T-2/v1.md", ".drashta/notes/N-2.md", ".drashta/notes/N-3.md"} {
		if !f.reindex.has("alpha:" + rel) {
			t.Errorf("not reindexed: %s (got %v)", rel, f.reindex.paths)
		}
	}
	ahDecode(t, f.do("GET", "/api/tracker?filter=review", nil), 200, &review)
	if len(review.Tasks) != 0 {
		t.Fatalf("after delete: review = %+v", review.Tasks)
	}
}

// I10: the counts (HomeBootCounts, used by /api/boot and the SSE hello/counts events) equal
// what GET /api/tracker returns: filter=review, and the tasks that are not done.
func TestFixI10CountsEqualTracker(t *testing.T) {
	f := newTrkFix(t)
	ctx := context.Background()
	d := filepath.Join(f.root, ".drashta", "tasks")
	trkWrite(t, filepath.Join(d, "T-3.md"), "---\nid: T-3\ntitle: In review, no snapshot\nstatus: review\nlinks: []\nunlinks: []\n---\n")
	trkWrite(t, filepath.Join(d, "T-4.md"), "---\nid: T-4\ntitle: Done with a pending snapshot\nstatus: done\nlinks: []\nunlinks: []\n---\n")
	fcSnapshot(t, f.root, "T-4")
	fcSnapshot(t, f.root, "T-1") // in_progress with a pending snapshot: awaiting
	// beta is hidden and has a review task.
	trkWrite(t, filepath.Join(f.beta, ".drashta", "tasks", "T-1.md"), "---\nid: T-1\ntitle: Hidden review\nstatus: review\nlinks: []\nunlinks: []\n---\n")
	if err := f.db.UpsertProject(ctx, store.Project{ID: "beta", Name: "beta", Group: "g", Path: f.beta, Hidden: true, Stack: []string{}}); err != nil {
		t.Fatal(err)
	}
	// Mirror the files into the index the way the indexer does.
	for pid, root := range map[string]string{"alpha": f.root, "beta": f.beta} {
		tr, _ := authored.LoadProject(root)
		for _, tk := range tr.Tasks {
			if err := f.db.UpsertAuthoredTask(ctx, store.Task{ProjectID: pid, ID: tk.ID, Title: tk.Title, Status: tk.Status}); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range tr.Snapshots {
			if err := f.db.UpsertAuthoredSnapshot(ctx, store.Snapshot{ProjectID: pid, TaskID: s.Task, Version: s.Version, Verdict: s.Verdict}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var review, all trkTrackerReply
	ahDecode(t, f.do("GET", "/api/tracker?filter=review", nil), 200, &review)
	ahDecode(t, f.do("GET", "/api/tracker", nil), 200, &all)
	open := 0
	for _, tk := range all.Tasks {
		if tk.Status != authored.StatusDone {
			open++
		}
	}
	c, err := f.db.HomeBootCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.AwaitingReview != len(review.Tasks) || c.OpenTasks != open {
		t.Fatalf("counts %+v, tracker: review %d (%+v), open %d", c, len(review.Tasks), review.Tasks, open)
	}
	if c.AwaitingReview != 2 {
		t.Fatalf("awaitingReview = %d, want 2 (T-1, T-3)", c.AwaitingReview)
	}
}
