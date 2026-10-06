package authored

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNextIDSequence(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ kind, want string }{
		{"task", "T-1"}, {"task", "T-2"}, {"feature", "F-1"}, {"milestone", "M-1"}, {"note", "N-1"}, {"tasks", "T-3"},
	} {
		got, err := NextID(root, tc.kind)
		if err != nil || got != tc.want {
			t.Errorf("NextID(%q) = %q, %v; want %q", tc.kind, got, err, tc.want)
		}
	}
	if _, err := NextID(root, "snapshot"); err == nil {
		t.Errorf("snapshot ids are not made by NextID")
	}
	if _, err := NextID(root, "banana"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestNextIDContinuesAfterExistingFiles(t *testing.T) {
	root := fixtureCopy(t)
	id, err := NextID(root, "task")
	if err != nil || id != "T-6" {
		t.Fatalf("%q %v", id, err)
	}
	id, _ = NextID(root, "note")
	if id != "N-4" {
		t.Errorf("note id %q", id)
	}
}

// Many goroutines allocate ids at the same time. All ids must be different and every id must
// be a real file. A second group of writers does what a shell without the CLI does (see
// 04-data-model.md section 2.2): max+1, create with O_EXCL, try +1 on failure.
func TestNextIDRace(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	const goroutines, each = 16, 15
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	record := func(id string) {
		mu.Lock()
		seen[id]++
		mu.Unlock()
	}
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				id, err := NextID(root, "task")
				if err != nil {
					t.Error(err)
					return
				}
				record(id)
			}
		}()
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dir := dotPath(root, "tasks")
			for i := 0; i < each; i++ {
				for n := maxIn(dir, "T") + 1; ; n++ {
					id := fmt.Sprintf("T-%d", n)
					f, err := os.OpenFile(filepath.Join(dir, id+".md"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
					if err == nil {
						f.Close()
						record(id)
						break
					}
				}
			}
		}()
	}
	wg.Wait()

	want := goroutines*each + 4*each
	if len(seen) != want {
		t.Fatalf("got %d different ids, want %d", len(seen), want)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("id %s was given %d times", id, n)
		}
	}
	entries, _ := os.ReadDir(dotPath(root, "tasks"))
	if len(entries) != want {
		t.Errorf("%d files for %d ids", len(entries), want)
	}
}

func TestNextIDNeverReusedAfterDelete(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		id, _ := NextID(root, "task")
		if _, err := Save(root, &Task{ID: id, Title: "t"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := Delete(root, "task", "T-3"); err != nil {
		t.Fatal(err)
	}
	id, err := NextID(root, "task")
	if err != nil || id != "T-4" {
		t.Fatalf("after deleting the highest id, got %q %v (T-3 must not come back)", id, err)
	}
}

func TestDeleteMovesToTrash(t *testing.T) {
	root := fixtureCopy(t)
	orig := readFile(t, dotPath(root, "tasks", "T-2.md"))
	if err := Delete(root, "task", "T-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dotPath(root, "tasks", "T-2.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the task file is still there: %v", err)
	}
	if got := readFile(t, dotPath(root, ".trash", "tasks", "T-2.md")); got != orig {
		t.Errorf("trash copy differs")
	}
	if tr, errs := LoadProject(root); len(errs) != 0 || len(tr.Tasks) != 4 {
		t.Errorf("after delete: %d tasks, %v", len(tr.Tasks), errs)
	}
	// Deleting twice: not found. A bad id: invalid.
	if err := Delete(root, "task", "T-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if err := Delete(root, "task", "../../x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id: %v", err)
	}
	// If the same id is trashed twice, the first copy is kept.
	writeFile(t, dotPath(root, "tasks", "T-2.md"), "---\nid: T-2\ntitle: Second life\n---\n")
	if err := Delete(root, "tasks", "T-2"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dotPath(root, ".trash", "tasks"))
	if len(entries) != 2 {
		t.Errorf("want 2 trash files, got %d", len(entries))
	}
	if got := readFile(t, dotPath(root, ".trash", "tasks", "T-2.md")); got != orig {
		t.Errorf("the first trash copy was overwritten")
	}
}

func TestDeleteSnapshotMovesImageToo(t *testing.T) {
	root := fixtureCopy(t)
	if err := Delete(root, "snapshot", "T-2/v1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"v1.md", "v1.png"} {
		if _, err := os.Stat(dotPath(root, ".trash", "snapshots", "T-2", name)); err != nil {
			t.Errorf("%s not in trash: %v", name, err)
		}
		if _, err := os.Stat(dotPath(root, "snapshots", "T-2", name)); err == nil {
			t.Errorf("%s still in place", name)
		}
	}
	if _, err := os.Stat(dotPath(root, "snapshots", "T-2", "v2.md")); err != nil {
		t.Errorf("v2 must stay: %v", err)
	}
	// The version number is not reused either.
	next, _, err := AddSnapshotImage(root, "T-2", pngReader(), "png")
	if err != nil || next != 3 {
		t.Errorf("next version = %d, %v", next, err)
	}
	if err := Delete(root, "snapshot", "T-2/v1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting again: %v", err)
	}
}
