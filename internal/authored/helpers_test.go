package authored

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// fixtureDir is the checked-in sample project (invented data).
const fixtureDir = "../../testdata/authored/project"

// copyTree copies a folder into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fixtureCopy returns a temp project folder holding a copy of the sample .drashta data.
func fixtureCopy(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, fixtureDir, root)
	return root
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dotPath(root string, parts ...string) string {
	return filepath.Join(append([]string{root, ".drashta"}, parts...)...)
}
