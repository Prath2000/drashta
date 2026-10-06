package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A request that fails validation must not leave an empty reserved id file behind
// (review M14, CLI path). The title below holds a newline, which Validate refuses.
func TestBadTaskLeavesNoPlaceholderFile(t *testing.T) {
	h := newHarness(t)
	before := listIDFiles(t, filepath.Join(h.root, ".drashta", "tasks"))

	r := h.run("task", "new", "bad\ntitle")
	if r.code == 0 {
		t.Fatalf("a title with a newline must be refused, out=%q", r.out)
	}
	after := listIDFiles(t, filepath.Join(h.root, ".drashta", "tasks"))
	if len(after) != len(before) {
		t.Fatalf("task files before=%v after=%v: a refused task left a placeholder", before, after)
	}
}

func listIDFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}
