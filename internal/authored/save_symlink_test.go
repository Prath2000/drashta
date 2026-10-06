package authored

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// TestMkdirBelowDotRefusesSymlinkOutside (review M-4): a .drashta/snapshots
// symlink that points outside the project must not get folders made in it.
func TestMkdirBelowDotRefusesSymlinkOutside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "evil")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	snaps := filepath.Join(root, dotName, "snapshots")
	os.RemoveAll(snaps)
	if err := os.Symlink(outside, snaps); err != nil {
		t.Fatal(err)
	}
	if _, err := mkdirBelowDot(root, "snapshots/T-1"); !errors.Is(err, pathguard.ErrOutside) {
		t.Fatalf("err = %v, want ErrOutside", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "T-1")); !os.IsNotExist(err) {
		t.Fatalf("a folder was made outside the project: %v", err)
	}
	// A symlink that stays inside the project is fine.
	inside := filepath.Join(root, "real-snaps")
	os.Mkdir(inside, 0o755)
	os.Remove(snaps)
	os.Symlink(inside, snaps)
	got, err := mkdirBelowDot(root, "snapshots/T-2")
	if err != nil {
		t.Fatalf("inside symlink: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(inside, "T-2")); err != nil || !fi.IsDir() || filepath.Base(got) != "T-2" {
		t.Fatalf("inside symlink: got %q, stat err %v", got, err)
	}
}

// TestEnsureDotRefusesOutsideOrBrokenLink: a .drashta that is a symlink
// outside the project, or a broken one, creates nothing.
func TestEnsureDotRefusesOutsideOrBrokenLink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "evil")
	outside := filepath.Join(base, "outside")
	os.MkdirAll(root, 0o755)
	os.MkdirAll(outside, 0o755)
	dot := filepath.Join(root, dotName)
	os.Symlink(outside, dot)
	if _, err := mkdirBelowDot(root, "tasks"); !errors.Is(err, pathguard.ErrOutside) {
		t.Fatalf("outside .drashta: err = %v", err)
	}
	os.Remove(dot)
	gone := filepath.Join(outside, "gone")
	os.Symlink(gone, dot)
	if _, err := mkdirBelowDot(root, "tasks"); !errors.Is(err, pathguard.ErrOutside) {
		t.Fatalf("broken .drashta: err = %v", err)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("made %d entries outside the project", len(ents))
	}
}
