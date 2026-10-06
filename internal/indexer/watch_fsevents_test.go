//go:build darwin

package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsevents"
)

// waTranslator builds a watcher with a fake disk (the set of paths that "exist") and one stream
// that watches root. The mapping tests need no real FSEvents stream.
func waTranslator(t *testing.T, root string, exists map[string]bool) (*fseWatcher, *fseStream) {
	t.Helper()
	r, err := newWatchRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	w := newFSEventsWatcher(nil)
	w.filter = newPathFilter(nil)
	w.lstat = func(p string) (os.FileInfo, error) {
		if exists[p] {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	return w, &fseStream{roots: []watchRoot{r}}
}

func TestFSEvents_FlagMapping(t *testing.T) {
	root := t.TempDir()
	at := func(rel string) string { return filepath.Join(root, rel) }
	exists := map[string]bool{
		at("g/p/back.go"): true,
	}
	w, st := waTranslator(t, root, exists)
	now := time.Unix(1000, 0)

	const (
		file = fsevents.ItemIsFile
		dir  = fsevents.ItemIsDir
	)
	cases := []struct {
		name  string
		path  string
		flags fsevents.EventFlags
		want  []WatchKind // nil means no event
	}{
		{"created file", at("g/p/new.go"), fsevents.ItemCreated | file, []WatchKind{Create}},
		{"created and modified", at("g/p/new.go"), fsevents.ItemCreated | fsevents.ItemModified | file, []WatchKind{Create}},
		{"modified", at("g/p/a.go"), fsevents.ItemModified | file, []WatchKind{Modify}},
		{"inode meta change counts as modify", at("g/p/a.go"), fsevents.ItemInodeMetaMod | file, []WatchKind{Modify}},
		{"removed", at("g/p/gone.go"), fsevents.ItemRemoved | file, []WatchKind{Remove}},
		{"renamed", at("g/p/old.go"), fsevents.ItemRenamed | file, []WatchKind{Rename}},
		{"symlink is a file", at("g/p/link"), fsevents.ItemCreated | fsevents.ItemIsSymlink, []WatchKind{Create}},

		// Merged steps: the disk decides.
		{"created then removed (gone)", at("g/p/tmp.go"), fsevents.ItemCreated | fsevents.ItemRemoved | file, []WatchKind{Remove}},
		{"modified then removed (gone)", at("g/p/tmp.go"), fsevents.ItemModified | fsevents.ItemRemoved | file, []WatchKind{Remove}},
		{"removed then created again (there)", at("g/p/back.go"), fsevents.ItemCreated | fsevents.ItemRemoved | file, []WatchKind{Create}},
		{"removed then modified (there)", at("g/p/back.go"), fsevents.ItemModified | fsevents.ItemRemoved | file, []WatchKind{Modify}},
		{"removed then renamed in (there)", at("g/p/back.go"), fsevents.ItemRenamed | fsevents.ItemRemoved | file, []WatchKind{Rename}},

		// Not content changes.
		{"xattr only", at("g/p/a.go"), fsevents.ItemXattrMod | file, nil},
		{"finder info only", at("g/p/a.go"), fsevents.ItemFinderInfoMod | file, nil},
		{"owner only", at("g/p/a.go"), fsevents.ItemChangeOwner | file, nil},
		{"no item flag (sentinel)", at("g/p/a.go"), fsevents.HistoryDone, nil},
		{"ids wrapped", at("g/p/a.go"), fsevents.EventIDsWrapped, nil},

		// Folders: only create, remove and rename. Their own "modified" is noise.
		{"folder created", at("g/p/newdir"), fsevents.ItemCreated | dir, []WatchKind{Create}},
		{"folder removed", at("g/p/olddir"), fsevents.ItemRemoved | dir, []WatchKind{Remove}},
		{"folder renamed", at("g/p/mvdir"), fsevents.ItemRenamed | dir, []WatchKind{Rename}},
		{"folder modified", at("g/p/somedir"), fsevents.ItemModified | dir, nil},
		{"folder inode meta", at("g/p/somedir"), fsevents.ItemInodeMetaMod | dir, nil},

		// Dropped or merged events become MustScan.
		{"must scan sub dirs", at("g/p"), fsevents.MustScanSubDirs, []WatchKind{MustScan}},
		{"kernel dropped", at("g/p"), fsevents.MustScanSubDirs | fsevents.KernelDropped, []WatchKind{MustScan}},
		{"user dropped", at("g"), fsevents.MustScanSubDirs | fsevents.UserDropped, []WatchKind{MustScan}},
		{"mount", at("g/vol"), fsevents.Mount, []WatchKind{MustScan}},
		{"unmount", at("g/vol"), fsevents.Unmount, []WatchKind{MustScan}},
		{"must scan inside a skip dir", at("g/p/node_modules/x"), fsevents.MustScanSubDirs, nil},
		{"must scan inside a skip dir 2", at("g/p/dist"), fsevents.MustScanSubDirs | fsevents.UserDropped, nil},

		// Skip dirs and the .git allow-list.
		{"node_modules file", at("g/p/node_modules/x/i.js"), fsevents.ItemModified | file, nil},
		{"dist file", at("g/p/dist/a.js"), fsevents.ItemCreated | file, nil},
		{"ds store", at("g/p/.DS_Store"), fsevents.ItemModified | file, nil},
		{"git HEAD", at("g/p/.git/HEAD"), fsevents.ItemModified | file, []WatchKind{Modify}},
		{"git index", at("g/p/.git/index"), fsevents.ItemModified | file, []WatchKind{Modify}},
		{"git ref", at("g/p/.git/refs/heads/main"), fsevents.ItemModified | file, []WatchKind{Modify}},
		{"git packed refs", at("g/p/.git/packed-refs"), fsevents.ItemModified | file, []WatchKind{Modify}},
		{"git fetch head", at("g/p/.git/FETCH_HEAD"), fsevents.ItemCreated | file, []WatchKind{Create}},
		{"git object", at("g/p/.git/objects/ab/cd"), fsevents.ItemCreated | file, nil},
		{"git index lock", at("g/p/.git/index.lock"), fsevents.ItemCreated | file, nil},
		{"fossil checkout", at("g/p/.fslckout"), fsevents.ItemModified | file, []WatchKind{Modify}},

		// Outside the roots.
		{"outside", "/somewhere/else/a.go", fsevents.ItemModified | file, nil},
	}
	for _, c := range cases {
		got := w.translate(st, fsevents.Event{Path: c.path, Flags: c.flags}, now, nil)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %d events %+v, want kinds %v", c.name, len(got), got, c.want)
			continue
		}
		for i, ev := range got {
			if ev.Kind != c.want[i] {
				t.Errorf("%s: kind = %v, want %v", c.name, ev.Kind, c.want[i])
			}
			if ev.Path != c.path {
				t.Errorf("%s: path = %q, want %q", c.name, ev.Path, c.path)
			}
			if !ev.Time.Equal(now) {
				t.Errorf("%s: time = %v, want %v", c.name, ev.Time, now)
			}
		}
	}
}

func TestFSEvents_DroppedWithoutPathScansEveryRoot(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	ra, err := newWatchRoot(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := newWatchRoot(b)
	if err != nil {
		t.Fatal(err)
	}
	w := newFSEventsWatcher(nil)
	w.filter = newPathFilter(nil)
	st := &fseStream{roots: []watchRoot{ra, rb}}

	got := w.translate(st, fsevents.Event{Path: "", Flags: fsevents.MustScanSubDirs | fsevents.KernelDropped}, time.Unix(1, 0), nil)
	if len(got) != 2 {
		t.Fatalf("got %d events, want one per root: %+v", len(got), got)
	}
	if got[0].Kind != MustScan || got[1].Kind != MustScan || got[0].Path != ra.orig || got[1].Path != rb.orig {
		t.Fatalf("events = %+v", got)
	}
}

func TestFSEvents_PathsUseTheCallersRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "g", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	w, st := waTranslator(t, link, nil)
	resolved, _ := filepath.EvalSymlinks(real)

	got := w.translate(st, fsevents.Event{Path: filepath.Join(resolved, "g", "p", "a.go"), Flags: fsevents.ItemModified}, time.Now(), nil)
	if len(got) != 1 || got[0].Path != filepath.Join(link, "g", "p", "a.go") {
		t.Fatalf("got %+v, want a path under the link root", got)
	}
}

func TestFSEvents_StartErrors(t *testing.T) {
	w := newFSEventsWatcher(nil)
	if _, err := w.Start(context.Background(), []string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("Start with no usable root must fail")
	}
	// Close before use is fine, and Start after Close fails.
	w2 := newFSEventsWatcher(nil)
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Start(context.Background(), []string{t.TempDir()}); err == nil {
		t.Fatal("Start after Close must fail")
	}
}

func TestFSEvents_LatencyLookup(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	w := newFSEventsWatcher(map[string]time.Duration{a: 2 * time.Second, b: -1})
	for _, tc := range []struct {
		dir  string
		want time.Duration
	}{{a, 2 * time.Second}, {b, DefaultWatchLatency}, {c, DefaultWatchLatency}} {
		r, err := newWatchRoot(tc.dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.latencyFor(r, tc.dir); got != tc.want {
			t.Errorf("latency for %s = %v, want %v", tc.dir, got, tc.want)
		}
	}
}

// waNextEvent waits for an event for which match returns true. Other events are collected.
func waNextEvent(t *testing.T, ch <-chan WatchEvent, within time.Duration, match func(WatchEvent) bool, seen *[]WatchEvent) (WatchEvent, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return WatchEvent{}, false
			}
			*seen = append(*seen, ev)
			if match(ev) {
				return ev, true
			}
		case <-deadline:
			return WatchEvent{}, false
		}
	}
}

// A small test with the real FSEvents stream. It skips if the stream cannot start.
func TestFSEvents_Smoke(t *testing.T) {
	root := t.TempDir() // /var/folders/... on macOS: also tests the /private alias
	w := NewFSEventsWatcher(map[string]time.Duration{root: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := w.Start(ctx, []string{root})
	if err != nil {
		t.Skipf("cannot start an FSEvents stream here: %v", err)
	}
	defer w.Close()
	time.Sleep(300 * time.Millisecond)

	proj := filepath.Join(root, "grp", "proj")
	if err := os.MkdirAll(filepath.Join(proj, "node_modules", "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A skip dir change first: it must never come out.
	if err := os.WriteFile(filepath.Join(proj, "node_modules", "dep", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(proj, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var seen []WatchEvent
	isFile := func(ev WatchEvent) bool { return ev.Path == file }
	ev, ok := waNextEvent(t, ch, 8*time.Second, isFile, &seen)
	if !ok {
		t.Skipf("no FSEvents event arrived within 8 s (sandbox or slow disk?). Seen: %+v", seen)
	}
	if ev.Kind != Create && ev.Kind != Modify {
		t.Errorf("new file: kind = %v, want create or modify", ev.Kind)
	}
	if ev.Time.IsZero() {
		t.Error("event has no time")
	}

	// Modify.
	time.Sleep(400 * time.Millisecond)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("// more\n")
	_ = f.Close()
	ev, ok = waNextEvent(t, ch, 8*time.Second, isFile, &seen)
	if !ok {
		t.Fatalf("no event after modifying the file. Seen: %+v", seen)
	}
	if ev.Kind != Create && ev.Kind != Modify {
		t.Errorf("modified file: kind = %v, want create or modify", ev.Kind)
	}

	// Remove.
	time.Sleep(400 * time.Millisecond)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	ev, ok = waNextEvent(t, ch, 8*time.Second, func(e WatchEvent) bool { return e.Path == file && e.Kind == Remove }, &seen)
	if !ok {
		t.Fatalf("no remove event. Seen: %+v", seen)
	}

	for _, e := range seen {
		if filepath.Base(filepath.Dir(e.Path)) == "dep" || filepath.Base(e.Path) == "node_modules" {
			t.Errorf("an event from inside node_modules left the watcher: %+v", e)
		}
		if !inside(root, e.Path) {
			t.Errorf("event path %q is not under the root %q", e.Path, root)
		}
	}

	t.Logf("events seen: %+v", seen)

	// Close ends the channel.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the channel was not closed after Close")
		}
	}
}
