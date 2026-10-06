package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	outside := filepath.Join(base, "secret")
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(t, os.MkdirAll(outside, 0o755))
	must(t, os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(outside, "pw.txt"), []byte("x"), 0o644))
	must(t, os.Symlink(outside, filepath.Join(root, "escape")))
	must(t, os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "ok")))
	// sibling whose name starts with the root name
	must(t, os.MkdirAll(root+"-evil", 0o755))
	must(t, os.WriteFile(filepath.Join(root+"-evil", "f"), []byte("x"), 0o644))

	cases := []struct {
		name, rel string
		wantErr   error
		anyErr    bool
	}{
		{"file inside", "sub/a.txt", nil, false},
		{"root itself", "", nil, false},
		{"dot-dot escape", "../secret/pw.txt", nil, false}, // cleaned to root-relative, so it does not exist
		{"symlink out", "escape/pw.txt", ErrOutside, false},
		{"symlink in", "ok/a.txt", nil, false},
		{"absolute rel", "/etc/passwd", ErrOutside, false},
	}
	for _, c := range cases {
		got, err := Resolve(root, c.rel)
		switch c.name {
		case "dot-dot escape":
			if err == nil {
				t.Errorf("%s: want error (path must not climb out), got %s", c.name, got)
			}
			continue
		}
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Errorf("%s: want %v, got %v (%s)", c.name, c.wantErr, err, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
	}
}

func TestInsidePrefixTrap(t *testing.T) {
	if Inside("/a/b", "/a/bc") {
		t.Fatal("/a/bc must not be inside /a/b")
	}
	if !Inside("/a/b", "/a/b/c") || !Inside("/a/b", "/a/b") {
		t.Fatal("child and self must be inside")
	}
}

func TestResolveAny(t *testing.T) {
	base := t.TempDir()
	r1 := filepath.Join(base, "one")
	must(t, os.MkdirAll(r1, 0o755))
	f := filepath.Join(r1, "f.txt")
	must(t, os.WriteFile(f, []byte("x"), 0o644))
	if _, err := ResolveAny([]string{r1}, f); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveAny([]string{r1}, "/etc/hosts"); !errors.Is(err, ErrOutside) {
		t.Fatalf("want ErrOutside, got %v", err)
	}
	if _, err := ResolveAny([]string{r1}, "relative/path"); !errors.Is(err, ErrOutside) {
		t.Fatalf("want ErrOutside for relative, got %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
