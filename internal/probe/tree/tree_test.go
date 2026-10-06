package tree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// mk writes a file (making folders as needed) under root.
func mk(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}

func find(es []Entry, p string) (Entry, bool) {
	for _, e := range es {
		if e.Path == p {
			return e, true
		}
	}
	return Entry{}, false
}

// withCaps sets small caps for one test and restores them afterwards.
func withCaps(t *testing.T, entries, depth int) {
	t.Helper()
	oe, od := maxEntries, maxDepth
	maxEntries, maxDepth = entries, depth
	t.Cleanup(func() { maxEntries, maxDepth = oe, od })
}

func TestWalkSkipListAndCollapsed(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "main.go", "package main\n")
	mk(t, root, "README.md", "# Hi\n")
	mk(t, root, "img/logo.png", "\x89PNG")
	mk(t, root, "src/app.ts", "x")
	mk(t, root, "src/.DS_Store", "junk")
	mk(t, root, ".DS_Store", "junk")
	mk(t, root, ".fslckout", "db")
	mk(t, root, "node_modules/pkg/index.js", "x")
	mk(t, root, ".git/HEAD", "ref")
	mk(t, root, "dist/a.js", "x")
	mk(t, root, "src/build/out.js", "x") // "build" is skipped at any depth
	mk(t, root, "custom_out/f.txt", "x")
	mk(t, root, "docs/generated/x.md", "x")
	mk(t, root, "docs/keep.md", "x")

	res, err := Walk(context.Background(), root, []string{"custom_out", "docs/generated"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated || len(res.Denied) != 0 {
		t.Fatalf("unexpected truncated=%v denied=%v", res.Truncated, res.Denied)
	}

	for _, c := range []string{"node_modules", ".git", "dist", "src/build", "custom_out", "docs/generated"} {
		e, ok := find(res.Entries, c)
		if !ok || !e.Dir || !e.Collapsed {
			t.Errorf("%s should be a collapsed dir, got %+v (found=%v)", c, e, ok)
		}
	}
	for _, e := range res.Entries {
		for _, bad := range []string{"node_modules/", ".git/", "dist/", "src/build/", "custom_out/", "docs/generated/"} {
			if strings.HasPrefix(e.Path, bad) {
				t.Errorf("walked into a skipped folder: %s", e.Path)
			}
		}
		if strings.HasSuffix(e.Path, ".DS_Store") || e.Path == ".fslckout" {
			t.Errorf("skipped file listed: %s", e.Path)
		}
		if strings.Contains(e.Path, `\`) || strings.HasPrefix(e.Path, "/") {
			t.Errorf("path must be relative with slashes: %q", e.Path)
		}
	}

	want := map[string]struct {
		dir  bool
		kind string
	}{
		"main.go":        {false, "code"},
		"README.md":      {false, "doc"},
		"img":            {true, ""},
		"img/logo.png":   {false, "image"},
		"src/app.ts":     {false, "code"},
		"docs/keep.md":   {false, "doc"},
		"docs":           {true, ""},
		"src":            {true, ""},
		"docs/generated": {true, ""},
	}
	for p, w := range want {
		e, ok := find(res.Entries, p)
		if !ok {
			t.Errorf("missing entry %s", p)
			continue
		}
		if e.Dir != w.dir || e.Kind != w.kind {
			t.Errorf("%s: dir=%v kind=%q, want dir=%v kind=%q", p, e.Dir, e.Kind, w.dir, w.kind)
		}
	}
	if e, _ := find(res.Entries, "main.go"); e.Size != int64(len("package main\n")) || e.MTime.IsZero() {
		t.Errorf("main.go size/mtime wrong: %+v", e)
	}
}

func TestWalkSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mk(t, outside, "secret.txt", "hush")
	mk(t, root, "real/a.txt", "x")
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "linkfile.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nope"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}

	res, err := Walk(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := find(res.Entries, "linkdir"); !ok || !e.Dir || !e.Collapsed {
		t.Errorf("link to a folder should be a collapsed folder: %+v", e)
	}
	for _, e := range res.Entries {
		if strings.HasPrefix(e.Path, "linkdir/") {
			t.Errorf("followed a symlink: %s", e.Path)
		}
	}
	if e, ok := find(res.Entries, "linkfile.txt"); !ok || e.Dir {
		t.Errorf("link to a file should be a file entry: %+v", e)
	}
	if e, ok := find(res.Entries, "broken"); !ok || e.Dir {
		t.Errorf("broken link should be a file entry: %+v", e)
	}
}

func TestWalkEntryCap(t *testing.T) {
	withCaps(t, 5, 12)
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		mk(t, root, "f"+string(rune('a'+i))+".txt", "x")
	}
	res, err := Walk(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 5 || !res.Truncated {
		t.Errorf("entries=%d truncated=%v, want 5 and true", len(res.Entries), res.Truncated)
	}

	// Exactly at the cap is not truncated.
	withCaps(t, 10, 12)
	res, _ = Walk(context.Background(), root, nil)
	if len(res.Entries) != 10 || res.Truncated {
		t.Errorf("entries=%d truncated=%v, want 10 and false", len(res.Entries), res.Truncated)
	}
}

func TestWalkDepthCap(t *testing.T) {
	withCaps(t, 1000, 2)
	root := t.TempDir()
	mk(t, root, "a/top.txt", "x")
	mk(t, root, "a/b/deep.txt", "x")
	mk(t, root, "a/b/c/deeper.txt", "x")
	if err := os.MkdirAll(filepath.Join(root, "empty/e2"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Walk(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := paths(res.Entries)
	want := []string{"a", "a/b", "a/top.txt", "empty", "empty/e2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if !res.Truncated {
		t.Error("a/b has children below the depth cap, Truncated should be true")
	}

	// Only an empty folder sits at the cap: not truncated.
	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root2, "a/b"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ = Walk(context.Background(), root2, nil)
	if res.Truncated {
		t.Error("empty folder at the cap must not set Truncated")
	}
}

func TestWalkDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read everything")
	}
	root := t.TempDir()
	mk(t, root, "ok/a.txt", "x")
	mk(t, root, "locked/inner/secret.txt", "x")
	mk(t, root, "z.txt", "x")
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	res, err := Walk(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Denied, []string{"locked"}) {
		t.Errorf("Denied = %v, want [locked]", res.Denied)
	}
	if _, ok := find(res.Entries, "locked"); !ok {
		t.Error("the locked folder itself should still be listed")
	}
	if _, ok := find(res.Entries, "locked/inner"); ok {
		t.Error("must not list inside a denied folder")
	}
	// The walk goes on after the denied folder.
	if _, ok := find(res.Entries, "z.txt"); !ok {
		t.Error("walk stopped at the denied folder")
	}
	if _, ok := find(res.Entries, "ok/a.txt"); !ok {
		t.Error("walk missed ok/a.txt")
	}
}

func TestWalkContextAndBadRoot(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "a.txt", "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Walk(ctx, root, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
	if _, err := Walk(context.Background(), filepath.Join(root, "missing"), nil); err == nil {
		t.Error("want an error for a missing root")
	}
	if _, err := Walk(context.Background(), filepath.Join(root, "a.txt"), nil); err == nil {
		t.Error("want an error when root is a file")
	}
}

func TestWalkOrderIsStable(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"b.txt", "a/z.txt", "a/y.txt", "c.txt"} {
		mk(t, root, f, "x")
	}
	r1, _ := Walk(context.Background(), root, nil)
	r2, _ := Walk(context.Background(), root, nil)
	if !reflect.DeepEqual(paths(r1.Entries), paths(r2.Entries)) {
		t.Error("two walks gave different order")
	}
	want := []string{"a", "a/y.txt", "a/z.txt", "b.txt", "c.txt"}
	if !reflect.DeepEqual(paths(r1.Entries), want) {
		t.Errorf("order = %v, want %v", paths(r1.Entries), want)
	}
}

func TestListDir(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "Zeta.go", "x")
	mk(t, root, "alpha.go", "x")
	mk(t, root, "src/main.go", "x")
	mk(t, root, "src/pkg/util.go", "x")
	mk(t, root, "src/pkg/deep/more.go", "x")
	mk(t, root, "node_modules/x/y.js", "x")
	mk(t, root, "mine/custom/z.txt", "x")
	if err := os.MkdirAll(filepath.Join(root, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}

	es, err := ListDir(root, "", 1, "mine")
	if err != nil {
		t.Fatal(err)
	}
	// Folders first, then files, each A-Z without regard to case.
	want := []string{"emptydir", "mine", "node_modules", "src", "alpha.go", "Zeta.go"}
	if !reflect.DeepEqual(paths(es), want) {
		t.Errorf("depth 1 = %v, want %v", paths(es), want)
	}
	byPath := func(p string) Entry { e, _ := find(es, p); return e }
	if !byPath("node_modules").Collapsed || byPath("node_modules").HasChildren {
		t.Errorf("node_modules should be collapsed: %+v", byPath("node_modules"))
	}
	if !byPath("mine").Collapsed {
		t.Errorf("extraSkip should collapse mine: %+v", byPath("mine"))
	}
	if !byPath("src").HasChildren || byPath("emptydir").HasChildren {
		t.Errorf("HasChildren wrong: src=%v emptydir=%v", byPath("src").HasChildren, byPath("emptydir").HasChildren)
	}

	// Depth 2 and a sub folder. Paths stay relative to root.
	es, err = ListDir(root, "src", 2)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"src/pkg", "src/pkg/deep", "src/pkg/util.go", "src/main.go"}
	if !reflect.DeepEqual(paths(es), want) {
		t.Errorf("src depth 2 = %v, want %v", paths(es), want)
	}
	if e, _ := find(es, "src/pkg"); !e.HasChildren {
		t.Error("src/pkg is opened and has children")
	}
	if e, _ := find(es, "src/pkg/deep"); !e.HasChildren {
		t.Error("src/pkg/deep is on the last level, HasChildren should be probed")
	}

	// Depth below 1 acts like 1.
	es, _ = ListDir(root, "src", 0)
	if !reflect.DeepEqual(paths(es), []string{"src/pkg", "src/main.go"}) {
		t.Errorf("depth 0 = %v", paths(es))
	}

	// A trailing slash is fine.
	es, err = ListDir(root, "src/", 1)
	if err != nil || len(es) != 2 {
		t.Errorf("clean path: %v %v", paths(es), err)
	}
}

func TestListDirGuarded(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mk(t, outside, "secret.txt", "hush")
	mk(t, root, "a.txt", "x")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := ListDir(root, "escape", 1); !errors.Is(err, pathguard.ErrOutside) {
		t.Errorf("symlink out of root: want ErrOutside, got %v", err)
	}
	if _, err := ListDir(root, "a.txt", 1); !errors.Is(err, ErrNotDir) {
		t.Errorf("file: want ErrNotDir, got %v", err)
	}
	if _, err := ListDir(root, "missing", 1); err == nil {
		t.Error("missing folder should be an error")
	}
	// ".." cannot climb out. It is clamped to root, as pathguard does.
	es, err := ListDir(root, "../..", 1)
	if err != nil {
		t.Fatal(err)
	}
	got := paths(es)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a.txt", "escape"}) {
		t.Errorf("'../..' should list root only, got %v", got)
	}
}

func TestListDirEntryCap(t *testing.T) {
	withCaps(t, 3, 12)
	root := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		mk(t, root, n+".txt", "x")
	}
	es, err := ListDir(root, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 {
		t.Errorf("want 3 entries at the cap, got %d", len(es))
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"main.go": "code", "app.TSX": "code", "style.css": "code", "Makefile": "code", "data.json": "code",
		"README": "doc", "readme.rst": "doc", "notes.md": "doc", "a.txt": "doc", "CLAUDE.md": "doc",
		"logo.png": "image", "photo.JPG": "image", "icon.svg": "image",
		"archive.zip": "other", "binary": "other", "font.woff2": "other",
	}
	for name, want := range cases {
		if got := Classify(name); got != want {
			t.Errorf("Classify(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestIsSecret(t *testing.T) {
	secret := []string{".env", ".env.local", ".env.production", ".envrc", "key.pem", "server.KEY", "id_rsa", "id_ed25519", "id_rsa.pub", "cert.p12", "a/b/.env", "ID_RSA"}
	for _, n := range secret {
		if !IsSecret(n) {
			t.Errorf("IsSecret(%q) = false, want true", n)
		}
	}
	plain := []string{"main.go", "environment.md", "env.go", "keyboard.txt", "hide_rsa", "pem.txt", "idea.md", "monkey.go"}
	for _, n := range plain {
		if IsSecret(n) {
			t.Errorf("IsSecret(%q) = true, want false", n)
		}
	}
}

func TestDefaultSkipNamesIsACopy(t *testing.T) {
	a := DefaultSkipNames()
	a[0] = "changed"
	if DefaultSkipNames()[0] == "changed" {
		t.Error("DefaultSkipNames must return a copy")
	}
	for _, want := range []string{"node_modules", ".git", "dist", "build", "target", "venv", ".venv", "__pycache__", ".next", ".cache", ".DS_Store", ".fslckout"} {
		found := false
		for _, n := range DefaultSkipNames() {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("default skip list lacks %s", want)
		}
	}
}
