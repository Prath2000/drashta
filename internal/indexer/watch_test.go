package indexer

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWatcher_SkipDirsAreIgnored(t *testing.T) {
	cases := []struct {
		rel     string
		ignored bool
	}{
		{"personal/app/main.go", false},
		{"personal/app/src/deep/file.ts", false},
		{"personal/app/node_modules/left-pad/index.js", true},
		{"personal/app/web/node_modules", true},
		{"personal/app/dist/bundle.js", true},
		{"personal/app/build/out.o", true},
		{"personal/app/target/debug/app", true},
		{"personal/app/venv/bin/python", true},
		{"personal/app/.venv/lib/x.py", true},
		{"personal/app/pkg/__pycache__/x.pyc", true},
		{"personal/app/.next/cache/x", true},
		{"personal/app/.cache/x", true},
		{"personal/app/.DS_Store", true},
		{"personal/app/sub/.DS_Store", true},
		// A name that only looks like a skip dir is not one.
		{"personal/app/distro/x.go", false},
		{"personal/app/builder.go", false},
		{"personal/app/my_build/x.go", false},
		// The root itself.
		{"", false},
	}
	for _, c := range cases {
		if got := IsIgnoredWatchPath(c.rel, nil); got != c.ignored {
			t.Errorf("IsIgnoredWatchPath(%q) = %v, want %v", c.rel, got, c.ignored)
		}
	}
}

func TestWatcher_GitAllowList(t *testing.T) {
	cases := []struct {
		rel     string
		ignored bool
	}{
		{"g/p/.git/HEAD", false},
		{"g/p/.git/index", false},
		{"g/p/.git/packed-refs", false},
		{"g/p/.git/FETCH_HEAD", false},
		{"g/p/.git/refs", false},
		{"g/p/.git/refs/heads/main", false},
		{"g/p/.git/refs/remotes/origin/main", false},
		{"g/p/.git/refs/tags/v1", false},
		{"g/p/.git", false},                     // the repository appears or goes away
		{"g/p/.git/index.lock", true},           // lock files are noise
		{"g/p/.git/refs/heads/main.lock", true}, // also under refs
		{"g/p/.git/objects/ab/cdef", true},
		{"g/p/.git/logs/HEAD", true},
		{"g/p/.git/config", true},
		{"g/p/.git/COMMIT_EDITMSG", true},
		{"g/p/.git/HEAD/extra", true},
		{"g/p/.git/index/extra", true},
		// A nested repository follows the same rule.
		{"g/p/vendor/lib/.git/HEAD", false},
		{"g/p/vendor/lib/.git/objects/x", true},
		// A skip dir above the .git wins.
		{"g/p/node_modules/x/.git/HEAD", true},
	}
	for _, c := range cases {
		if got := IsIgnoredWatchPath(c.rel, nil); got != c.ignored {
			t.Errorf("IsIgnoredWatchPath(%q) = %v, want %v", c.rel, got, c.ignored)
		}
	}
}

func TestWatcher_FossilCheckoutFilePasses(t *testing.T) {
	// The fossil probe needs events for .fslckout, but the activity count must not use it.
	if IsIgnoredWatchPath("g/p/.fslckout", nil) {
		t.Error("the watcher must pass .fslckout")
	}
	if !IsSkippedPath("g/p/.fslckout", nil) || !IsSkippedPath(".fslckout", nil) {
		t.Error("activity must skip .fslckout")
	}
	// A folder with that name is still a skip dir.
	if !IsIgnoredWatchPath("g/p/.fslckout/x", nil) {
		t.Error("a .fslckout folder must be ignored")
	}
	// Other fossil files are not special.
	if IsIgnoredWatchPath("g/p/project.fossil", nil) || IsIgnoredWatchPath("g/p/_FOSSIL_", nil) {
		t.Error("project.fossil and _FOSSIL_ must pass")
	}
}

func TestWatcher_ActivitySkipRule(t *testing.T) {
	cases := map[string]bool{
		"main.go":                 false,
		"src/a/b.go":              false,
		".drashta/tasks/T-1.md":   false, // the router decides about .drashta, not the skip rule
		"node_modules/x.js":       true,
		"src/node_modules/x/y.js": true,
		".git/HEAD":               true,
		".git":                    true,
		"sub/.git/index":          true,
		".DS_Store":               true,
		"docs/.DS_Store":          true,
		".fslckout":               true,
		"dist/a.js":               true,
		"web/.next/server/app.js": true,
		"a/__pycache__/m.pyc":     true,
		"a/b/target/release/x":    true,
		"distro/x":                false,
		"src/venv_notes.md":       false,
		"coverage/lcov.info":      false,
	}
	for rel, want := range cases {
		if got := IsSkippedPath(rel, nil); got != want {
			t.Errorf("IsSkippedPath(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestWatcher_ExtraSkipNames(t *testing.T) {
	extra := []string{"generated", "docs/out", "  /trimmed/  ", ""}
	// Activity: a plain name matches at any depth. A "a/b" entry matches from the project root.
	for rel, want := range map[string]bool{
		"generated/x.go":      true,
		"src/generated/x.go":  true,
		"docs/out/a.html":     true,
		"docs/src/a.md":       false,
		"other/docs/out/a.md": false,
		"trimmed/x":           true,
		"src/main.go":         false,
	} {
		if got := IsSkippedPath(rel, extra); got != want {
			t.Errorf("IsSkippedPath(%q, extra) = %v, want %v", rel, got, want)
		}
	}
	// Watcher: it does not know where projects start, so "a/b" matches at any depth.
	for rel, want := range map[string]bool{
		"g/p/generated/x.go":  true,
		"g/p/docs/out/a.html": true,
		"g/p/docs/src/a.md":   false,
		"g/p/src/main.go":     false,
	} {
		if got := IsIgnoredWatchPath(rel, extra); got != want {
			t.Errorf("IsIgnoredWatchPath(%q, extra) = %v, want %v", rel, got, want)
		}
	}
}

func TestWatcher_PrivateAlias(t *testing.T) {
	cases := map[string]string{
		"/var/folders/ab/T/x":    "/private/var/folders/ab/T/x",
		"/private/var/folders/x": "/var/folders/x",
		"/tmp/a":                 "/private/tmp/a",
		"/private/tmp":           "/tmp",
		"/etc/hosts":             "/private/etc/hosts",
		"/Users/fx/projects":     "",
		"/variable/x":            "", // not the /var folder
		"/private/variable/x":    "",
		"/private/Users/fx/foo":  "",
	}
	for in, want := range cases {
		if got := privateAlias(in); got != want {
			t.Errorf("privateAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWatcher_RootMappingThroughSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "g", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	r, err := newWatchRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	realResolved, _ := filepath.EvalSymlinks(real)
	if r.real != realResolved {
		t.Fatalf("real = %q, want %q", r.real, realResolved)
	}
	// FSEvents reports the real path. We give back the path the caller used.
	orig, rel, ok := r.translate(filepath.Join(realResolved, "g", "p", "a.go"))
	if !ok || rel != "g/p/a.go" || orig != filepath.Join(link, "g", "p", "a.go") {
		t.Fatalf("translate real path: orig=%q rel=%q ok=%v", orig, rel, ok)
	}
	// The caller's own spelling works as well.
	orig, rel, ok = r.translate(filepath.Join(link, "g", "p", "a.go"))
	if !ok || rel != "g/p/a.go" || orig != filepath.Join(link, "g", "p", "a.go") {
		t.Fatalf("translate link path: orig=%q rel=%q ok=%v", orig, rel, ok)
	}
	// The root itself.
	orig, rel, ok = r.translate(realResolved)
	if !ok || rel != "" || orig != link {
		t.Fatalf("translate root: orig=%q rel=%q ok=%v", orig, rel, ok)
	}
	// A sibling with the same prefix text is not inside.
	if _, _, ok := r.translate(realResolved + "-other/x.go"); ok {
		t.Fatal("real-other must not match root real")
	}
	if _, _, ok := r.translate("/somewhere/else.go"); ok {
		t.Fatal("outside path must not match")
	}
}

func TestWatcher_RootMappingPrivateAndDataVolume(t *testing.T) {
	// Pure string cases: the roots do not need to exist, so build the struct by hand.
	r := watchRoot{orig: "/var/folders/ab/T/proj", real: "/private/var/folders/ab/T/proj"}
	r.prefixes = []string{r.real, r.orig}
	if a := privateAlias(r.real); a != "" {
		r.prefixes = append(r.prefixes, a)
	}
	for _, p := range []string{
		"/private/var/folders/ab/T/proj/g/a.go",
		"/var/folders/ab/T/proj/g/a.go",
		"/System/Volumes/Data/private/var/folders/ab/T/proj/g/a.go",
	} {
		orig, rel, ok := r.translate(p)
		if !ok || rel != "g/a.go" || orig != "/var/folders/ab/T/proj/g/a.go" {
			t.Errorf("translate(%q): orig=%q rel=%q ok=%v", p, orig, rel, ok)
		}
	}

	home := watchRoot{orig: "/Users/me/projects", real: "/Users/me/projects"}
	home.prefixes = []string{home.real}
	orig, rel, ok := home.translate("/System/Volumes/Data/Users/me/projects/g/p/x.go")
	if !ok || rel != "g/p/x.go" || orig != "/Users/me/projects/g/p/x.go" {
		t.Errorf("data volume path: orig=%q rel=%q ok=%v", orig, rel, ok)
	}
}

func TestWatcher_MapEventPicksLongestRoot(t *testing.T) {
	outer := watchRoot{orig: "/a", real: "/a", prefixes: []string{"/a"}}
	inner := watchRoot{orig: "/a/b", real: "/a/b", prefixes: []string{"/a/b"}}
	orig, rel, ok := mapEvent([]watchRoot{outer, inner}, "/a/b/c.go")
	if !ok || orig != "/a/b/c.go" || rel != "c.go" {
		t.Fatalf("got orig=%q rel=%q ok=%v", orig, rel, ok)
	}
	if _, _, ok := mapEvent([]watchRoot{outer, inner}, "/z/c.go"); ok {
		t.Fatal("outside path must not map")
	}
}

func TestWatchKind_String(t *testing.T) {
	for k, want := range map[WatchKind]string{
		Create: "create", Modify: "modify", Remove: "remove", Rename: "rename", MustScan: "mustscan", WatchKind(99): "unknown",
	} {
		if k.String() != want {
			t.Errorf("%d.String() = %q, want %q", int(k), k.String(), want)
		}
	}
	if WatchCreate != Create || WatchModify != Modify || WatchRemove != Remove || WatchRename != Rename || WatchMustScan != MustScan {
		t.Error("the Watch* names must equal the short names")
	}
}

// ---------------------------------------------------------------------------
// FakeWatcher

func TestFakeWatcher_SendStartClose(t *testing.T) {
	f := NewFakeWatcher()
	// Events sent before Start wait in the buffer.
	f.Send(WatchEvent{Path: "/r/a", Kind: Create})
	ch, err := f.Start(context.Background(), []string{"/r", "/s"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Roots(); len(got) != 2 || got[0] != "/r" || got[1] != "/s" {
		t.Fatalf("Roots = %v", got)
	}
	f.Send(WatchEvent{Path: "/r/b", Kind: Modify, Time: time.Unix(10, 0)})
	f.Send(WatchEvent{Path: "/r", Kind: MustScan})

	ev := <-ch
	if ev.Path != "/r/a" || ev.Kind != Create || ev.Time.IsZero() {
		t.Fatalf("first event = %+v (a zero Time must be filled)", ev)
	}
	ev = <-ch
	if ev.Path != "/r/b" || ev.Kind != Modify || !ev.Time.Equal(time.Unix(10, 0)) {
		t.Fatalf("second event = %+v", ev)
	}
	ev = <-ch
	if ev.Kind != MustScan {
		t.Fatalf("third event = %+v", ev)
	}

	if _, err := f.Start(context.Background(), nil); err == nil {
		t.Fatal("a second Start must fail")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal("Close twice must be fine")
	}
	f.Send(WatchEvent{Path: "/r/late"}) // after Close: no panic, no event
	if _, ok := <-ch; ok {
		t.Fatal("the channel must be closed")
	}
	if _, err := f.Start(context.Background(), nil); err == nil {
		t.Fatal("Start after Close must fail")
	}
}

func TestFakeWatcher_CtxCancelClosesChannel(t *testing.T) {
	f := NewFakeWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := f.Start(ctx, []string{"/r"})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the channel was not closed after ctx ended")
	}
}

func TestFakeWatcher_CloseReleasesBlockedSender(t *testing.T) {
	f := NewFakeWatcher()
	if _, err := f.Start(context.Background(), []string{"/r"}); err != nil {
		t.Fatal(err)
	}
	// Nobody reads, so the buffer fills and Send blocks. Close must free it.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			f.Send(WatchEvent{Path: "/r/x", Kind: Modify})
		}
	}()
	time.Sleep(50 * time.Millisecond)
	_ = f.Close()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Send stayed blocked after Close")
	}
}

func TestFakeWatcher_SatisfiesWatcher(t *testing.T) {
	var w Watcher = NewFakeWatcher()
	_ = w
	// The real constructor returns the same interface on every platform.
	var r Watcher = NewFSEventsWatcher(map[string]time.Duration{"/x": time.Second})
	_ = r.Close()
}
