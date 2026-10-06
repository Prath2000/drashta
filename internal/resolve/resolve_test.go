package resolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/store"
)

func TestParseRefDocAlias(t *testing.T) {
	for in, want := range map[string]string{
		"doc:README.md":         "file:README.md",
		"alpha:doc:docs/a.md":   "alpha:file:docs/a.md",
		"alpha:doc:x.md#L3":     "alpha:file:x.md#L3",
		"alpha:commit:abcdef12": "alpha:commit:abcdef12",
		"image:a.png":           "image:a.png",
	} {
		r, err := ParseRef(in)
		if err != nil || r.String() != want {
			t.Errorf("%q: got %q, %v; want %q", in, r.String(), err, want)
		}
	}
	for _, bad := range []string{"", "doc", "x:y:z", "file:../a", "commit:zz"} {
		if _, err := ParseRef(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: want ErrInvalid, got %v", bad, err)
		}
	}
}

func TestSameItem(t *testing.T) {
	mk := func(s string) authored.Ref {
		r, err := ParseRef(s)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cases := []struct {
		a, b string
		want bool
	}{
		{"p:commit:abcdef1", "p:commit:abcdef1234567", true},
		{"p:commit:ABCDEF1", "p:commit:abcdef1", true},
		{"p:commit:abcdef1", "q:commit:abcdef1", false},
		{"p:commit:abcdef1", "p:commit:abcdef2", false},
		{"p:file:a.go#L3", "p:file:a.go", true},
		{"p:image:a.png", "p:file:a.png", true},
		{"p:file:a.go", "p:file:b.go", false},
		{"p:task:T-1", "p:task:T-1", true},
		{"p:task:T-1", "p:task:T-10", false},
		{"session:121c3b19", "session:121c3b19-ada3-495e-a518-7fe5acf0a71e", true},
		{"p:task:T-1", "p:note:N-1", false},
	}
	for _, c := range cases {
		if got := SameItem(mk(c.a), mk(c.b)); got != c.want {
			t.Errorf("%s vs %s: got %v", c.a, c.b, got)
		}
	}
}

func TestDrops(t *testing.T) {
	root := t.TempDir()
	if UnfiledSnapshots(root) == nil || len(UnfiledSnapshots(root)) != 0 {
		t.Fatal("no inbox must give an empty list")
	}
	inbox := filepath.Join(root, ".drashta", "snapshots", "inbox")
	if err := os.MkdirAll(filepath.Join(inbox, "sub.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, name := range []string{"old.png", "new.JPG", ".hidden.png", "note.txt"} {
		p := filepath.Join(inbox, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, now.Add(time.Duration(i)*time.Minute), now.Add(time.Duration(i)*time.Minute))
	}
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "s.png"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(out, "s.png"), filepath.Join(inbox, "link.png")); err != nil {
		t.Fatal(err)
	}
	got := UnfiledSnapshots(root)
	if len(got) != 2 || got[0].Name != "new.JPG" || got[1].Name != "old.png" || got[1].Path != ".drashta/snapshots/inbox/old.png" {
		t.Fatalf("drops: %+v", got)
	}

	for name, want := range map[string]error{
		"a.png": nil, "a b.webp": nil, "": ErrBadDropName, "..": ErrBadDropName, ".x.png": ErrBadDropName,
		"a/b.png": ErrBadDropName, `a\b.png`: ErrBadDropName, "a\x00.png": ErrBadDropName, " a.png": ErrBadDropName,
		"a.txt": authored.ErrBadExt, "a": authored.ErrBadExt, "a\n.png": ErrBadDropName,
	} {
		if err := CheckDropName(name); !errors.Is(err, want) && !(want == nil && err == nil) {
			t.Errorf("%q: got %v, want %v", name, err, want)
		}
	}
	if _, err := DropPath(root, "old.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := DropPath(root, "link.png"); err == nil {
		t.Fatal("a link that leaves the project must be refused")
	}
	if _, err := DropPath(root, "missing.png"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := DropPath(root, "sub.png"); err == nil {
		t.Fatal("a folder is not a drop")
	}
}

func newTestResolver(t *testing.T) (*Resolver, string, string) {
	t.Helper()
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	projects := filepath.Join(base, "projects")
	root := filepath.Join(projects, "g", "alpha")
	for _, f := range []string{"src/a.go", ".drashta/tasks/T-1.md"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "src/a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := "---\nid: T-1\ntitle: One\nstatus: todo\nlinks:\n  - commit:abcdef1\nunlinks: []\n---\n"
	if err := os.WriteFile(filepath.Join(root, ".drashta/tasks/T-1.md"), []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(base, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := db.UpsertProject(ctx, store.Project{ID: "alpha", Name: "alpha", Group: "g", Path: root, Stack: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCommits(ctx, "alpha", []probe.Commit{{ID: "abcdef1234567890abcdef1234567890abcdef12", VCS: "git",
		Msg: "first", Time: time.Unix(1700000000, 0)}}); err != nil {
		t.Fatal(err)
	}
	r := New(Options{DB: db, Actions: actions.Config{Roots: []string{projects}}})
	return r, root, projects
}

func TestResolverQualifyTitleTarget(t *testing.T) {
	r, root, _ := newTestResolver(t)
	ctx := context.Background()

	q, err := r.Qualify(ctx, "file:src/a.go", "alpha")
	if err != nil || q.String() != "alpha:file:src/a.go" {
		t.Fatalf("qualify with default: %v %v", q, err)
	}
	if _, err := r.Qualify(ctx, "file:src/a.go", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("local ref without project: %v", err)
	}
	if _, err := r.Qualify(ctx, "nope:file:a", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}

	if title, sub := r.Title(ctx, "alpha:commit:abcdef1"); title != "first" || sub == "" {
		t.Fatalf("title: %q %q", title, sub)
	}
	if title, _ := r.Title(ctx, "alpha:commit:fffffff"); title != "alpha:commit:fffffff" {
		t.Fatalf("title fallback: %q", title)
	}

	tg, err := r.Target(ctx, "alpha:file:src/a.go#L1")
	if err != nil || tg.Path != "src/a.go" || tg.ProjectRoot != root || tg.Line != 1 || tg.Type != actions.TypeFile {
		t.Fatalf("target: %+v %v", tg, err)
	}
	tg, err = r.Target(ctx, "alpha:commit:abcdef1")
	if err != nil || tg.SHA != "abcdef1234567890abcdef1234567890abcdef12" || tg.VCS != "git" {
		t.Fatalf("commit target: %+v %v", tg, err)
	}
	if _, err := r.Target(ctx, "alpha:task:T-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task: %v", err)
	}

	// Manual link from the file (no index row yet) is found.
	ref, _ := r.Qualify(ctx, "alpha:commit:abcdef1234", "")
	tasks, err := r.TasksFor(ctx, ref)
	if err != nil || len(tasks) != 1 || tasks[0].ID != "T-1" {
		t.Fatalf("tasks for commit: %v %v", tasks, err)
	}
}
