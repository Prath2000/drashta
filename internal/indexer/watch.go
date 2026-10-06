package indexer

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Prath2000/drashta/internal/probe/tree"
)

// File watching: the contract the rest of the indexer codes against.
//
// Exported API of watch.go, watch_fsevents.go and activity.go (the indexer package also has
// the scheduler files, which have their own notes):
//
//	type WatchKind int                 Create | Modify | Remove | Rename | MustScan
//	                                   (also named WatchCreate, WatchModify, WatchRemove, WatchRename, WatchMustScan)
//	func (WatchKind) String() string
//	type WatchEvent struct{ Path string; Kind WatchKind; Time time.Time }
//	type Watcher interface{ Start(ctx, roots []string) (<-chan WatchEvent, error); Close() error }
//	func NewFSEventsWatcher(latencies map[string]time.Duration) Watcher   real watcher (macOS, cgo)
//	func NewFakeWatcher() *FakeWatcher                                    for tests
//	    (*FakeWatcher).Send(WatchEvent)  Roots() []string  Close()  Start(...)
//	func IsSkippedPath(rel string, extra []string) bool                   activity rule: any skip dir counts
//	func IsIgnoredWatchPath(rel string, extra []string) bool              watcher rule: .git allow-list applies
//	type Activity struct{...}
//	func NewActivity(db *store.DB, loc *time.Location, now func() time.Time) *Activity
//	    Record(projectID, relPath string, t time.Time)
//	    Flush(ctx) error
//	    Backfill(ctx, projectID string, files []tree.Entry, since time.Time) error
//	    Run(ctx)
//	    Count(projectID, day string) int
//	    SetOnFlush(fn func(projectID, day string))
//	    SetExtraSkip(names []string)
//
// Rules for a Watcher:
//   - Start may be called once. The channel is closed when ctx ends or Close is called.
//   - Every Path starts with one of the roots exactly as it was passed to Start (cleaned).
//     The macOS alias /var -> /private/var is hidden. Events from the real stream are mapped back.
//   - Events inside skip dirs never leave the watcher (node_modules, dist, build, target, venv, .venv,
//     __pycache__, .next, .cache, .DS_Store, config.skip). Inside .git only HEAD, index, packed-refs,
//     FETCH_HEAD and refs/** pass. The file .fslckout also passes, because the fossil probe needs it.
//   - Create and Modify both mean "the path exists and changed". A Create can be a late flag of an
//     older event, so treat them the same. Remove means the path is gone. Rename is sent for the old
//     and the new name; stat the path to see which one it is.
//   - MustScan means events were dropped or merged. Path is the folder (or watch root) to rescan fully.
//   - Directory events are sent as Create, Remove or Rename only. There is no IsDir field, so a caller
//     that does not want folders in the activity count must stat the path first.

// WatchKind is the kind of a WatchEvent.
type WatchKind int

// The kinds of file event.
const (
	Create WatchKind = iota
	Modify
	Remove
	Rename
	MustScan
)

// Longer names for the same values.
const (
	WatchCreate   = Create
	WatchModify   = Modify
	WatchRemove   = Remove
	WatchRename   = Rename
	WatchMustScan = MustScan
)

func (k WatchKind) String() string {
	switch k {
	case Create:
		return "create"
	case Modify:
		return "modify"
	case Remove:
		return "remove"
	case Rename:
		return "rename"
	case MustScan:
		return "mustscan"
	}
	return "unknown"
}

// WatchEvent is one file system change.
type WatchEvent struct {
	Path string // absolute, starts with one of the roots given to Start
	Kind WatchKind
	Time time.Time
}

// Watcher watches folder trees. The real one uses FSEvents. Tests use the FakeWatcher.
type Watcher interface {
	// Start begins watching the roots and returns the event channel.
	Start(ctx context.Context, roots []string) (<-chan WatchEvent, error)
	// Close stops the watcher and closes the event channel. It is safe to call twice.
	Close() error
}

// SkipConfigurer is implemented by watchers that take the config.skip names.
// The watcher from NewFSEventsWatcher has it. Call SetExtraSkip before Start:
//
//	if sc, ok := w.(indexer.SkipConfigurer); ok { sc.SetExtraSkip(cfg.Skip) }
type SkipConfigurer interface {
	SetExtraSkip(names []string)
}

// ---------------------------------------------------------------------------
// Skip rules

// pathFilter decides which relative paths are skipped. It uses the tree probe's skip list, so the
// tree walk, the watcher and the activity count agree.
type pathFilter struct {
	names map[string]bool
	seqs  [][]string // extra entries with a "/" (for example "docs/generated"), as folder sequences
}

func newPathFilter(extra []string) *pathFilter {
	f := &pathFilter{names: map[string]bool{}}
	for _, n := range tree.DefaultSkipNames() {
		f.names[n] = true
	}
	for _, e := range extra {
		e = strings.Trim(strings.TrimSpace(filepath.ToSlash(e)), "/")
		if e == "" {
			continue
		}
		if strings.Contains(e, "/") {
			f.seqs = append(f.seqs, splitRel(e))
		} else {
			f.names[e] = true
		}
	}
	return f
}

// splitRel splits a "/" path into its non-empty parts.
func splitRel(rel string) []string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}

// hasSeqAt reports whether comps[i:] starts with seq.
func hasSeqAt(comps []string, i int, seq []string) bool {
	if i+len(seq) > len(comps) {
		return false
	}
	for j, s := range seq {
		if comps[i+j] != s {
			return false
		}
	}
	return true
}

// skippedForActivity: rel is relative to the project. Any skip name (also .git, .fslckout) skips it.
// Entries of the form "a/b" match from the project root.
func (f *pathFilter) skippedForActivity(rel string) bool {
	comps := splitRel(rel)
	for _, c := range comps {
		if f.names[c] {
			return true
		}
	}
	for _, seq := range f.seqs {
		if hasSeqAt(comps, 0, seq) {
			return true
		}
	}
	return false
}

// ignoredForWatch: rel is relative to the watch root. It is true when the event must not leave the
// watcher. Inside .git only the routing-table files pass. The file .fslckout passes.
// Entries of the form "a/b" match at any depth, because the watcher does not know where projects start.
func (f *pathFilter) ignoredForWatch(rel string) bool {
	comps := splitRel(rel)
	for i, c := range comps {
		if c == ".git" {
			return !gitPathAllowed(comps[i+1:])
		}
		if c == ".fslckout" && i == len(comps)-1 {
			continue
		}
		if f.names[c] {
			return true
		}
		for _, seq := range f.seqs {
			if hasSeqAt(comps, i, seq) {
				return true
			}
		}
	}
	return false
}

// gitPathAllowed: rest is the path below a ".git" folder. The git probe needs HEAD, index,
// packed-refs, FETCH_HEAD and refs/** (03-architecture.md section 4, routing table), plus
// worktrees/<n>/HEAD and worktrees/<n>/index for linked worktree projects.
// The .git folder or file itself also passes (a repository appears or goes away).
func gitPathAllowed(rest []string) bool {
	if len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case "HEAD", "index", "packed-refs", "FETCH_HEAD":
		return len(rest) == 1
	case "refs":
		return !strings.HasSuffix(rest[len(rest)-1], ".lock")
	case "worktrees":
		// a linked worktree's HEAD and index (I6)
		return len(rest) == 3 && (rest[2] == "HEAD" || rest[2] == "index")
	}
	return false
}

// IsSkippedPath reports whether a project-relative path is inside a skip dir (or is a skip file).
// Activity counting uses this rule: .git and .fslckout are skipped too.
// extra holds the config.skip names.
func IsSkippedPath(rel string, extra []string) bool {
	return newPathFilter(extra).skippedForActivity(rel)
}

// IsIgnoredWatchPath reports whether a path relative to a watch root is dropped by the watcher.
// It is IsSkippedPath plus the .git allow-list and the .fslckout exception.
func IsIgnoredWatchPath(rel string, extra []string) bool {
	return newPathFilter(extra).ignoredForWatch(rel)
}

// ---------------------------------------------------------------------------
// Root mapping (macOS path aliases)

// watchRoot is one folder the caller asked to watch.
type watchRoot struct {
	orig     string   // the root as the caller gave it (cleaned, absolute)
	real     string   // orig after EvalSymlinks: what FSEvents reports
	prefixes []string // every spelling of the root that an event path may start with
}

// newWatchRoot resolves root. It fails if the folder does not exist.
func newWatchRoot(root string) (watchRoot, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return watchRoot{}, err
	}
	abs = filepath.Clean(abs)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return watchRoot{}, err
	}
	r := watchRoot{orig: abs, real: filepath.Clean(real)}
	seen := map[string]bool{}
	for _, p := range []string{r.real, r.orig, privateAlias(r.real), privateAlias(r.orig)} {
		if p != "" && !seen[p] {
			seen[p] = true
			r.prefixes = append(r.prefixes, p)
		}
	}
	return r, nil
}

// privateAlias flips the macOS alias for /var, /tmp and /etc: /var/x <-> /private/var/x.
// It returns "" for any other path.
func privateAlias(p string) string {
	for _, d := range []string{"/var", "/tmp", "/etc"} {
		if p == d || strings.HasPrefix(p, d+"/") {
			return "/private" + p
		}
		if p == "/private"+d || strings.HasPrefix(p, "/private"+d+"/") {
			return strings.TrimPrefix(p, "/private")
		}
	}
	return ""
}

// inside reports whether p is dir itself or below it.
func inside(dir, p string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// dataVolumePrefix is how the macOS data volume may appear in a raw event path.
const dataVolumePrefix = "/System/Volumes/Data"

// translate maps an event path to the root's own spelling.
// orig is the path in the caller's form. rel is relative to the root ("" for the root itself).
func (r watchRoot) translate(p string) (orig, rel string, ok bool) {
	p = filepath.Clean(p)
	cands := []string{p}
	if inside(dataVolumePrefix, p) {
		cands = append(cands, strings.TrimPrefix(p, dataVolumePrefix))
	}
	for _, c := range cands {
		for _, pre := range r.prefixes {
			if !inside(pre, c) {
				continue
			}
			rel = strings.TrimPrefix(strings.TrimPrefix(c, pre), "/")
			if rel == "" {
				return r.orig, "", true
			}
			return r.orig + "/" + rel, rel, true
		}
	}
	return "", "", false
}

// mapEvent finds the root that holds p. With nested roots the longest match wins.
func mapEvent(roots []watchRoot, p string) (orig, rel string, ok bool) {
	best := -1
	for _, r := range roots {
		o, rl, hit := r.translate(p)
		if !hit {
			continue
		}
		if len(r.orig) > best {
			best = len(r.orig)
			orig, rel, ok = o, rl, true
		}
	}
	return orig, rel, ok
}

// ---------------------------------------------------------------------------
// Fake watcher (tests)

// FakeWatcher is a Watcher for tests. Tests call Send to push events. Nothing touches the disk.
// Events sent before Start wait in a buffer.
type FakeWatcher struct {
	mu      sync.RWMutex
	ch      chan WatchEvent
	done    chan struct{}
	once    sync.Once
	closed  bool
	started bool
	roots   []string
}

// NewFakeWatcher makes a fake watcher.
func NewFakeWatcher() *FakeWatcher {
	return &FakeWatcher{ch: make(chan WatchEvent, 4096), done: make(chan struct{})}
}

// Start returns the event channel. It records the roots (see Roots). It can be called once.
func (f *FakeWatcher) Start(ctx context.Context, roots []string) (<-chan WatchEvent, error) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, errors.New("indexer: watcher is closed")
	}
	if f.started {
		f.mu.Unlock()
		return nil, errors.New("indexer: watcher already started")
	}
	f.started = true
	f.roots = append([]string(nil), roots...)
	f.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			_ = f.Close()
		case <-f.done:
		}
	}()
	return f.ch, nil
}

// Send pushes one event to the channel. If the buffer is full it waits for the reader.
// After Close it does nothing. A zero Time is replaced by the current time.
func (f *FakeWatcher) Send(ev WatchEvent) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return
	}
	select {
	case f.ch <- ev:
	case <-f.done:
	}
}

// Roots returns the roots that were given to Start.
func (f *FakeWatcher) Roots() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]string(nil), f.roots...)
}

// Close closes the channel. Safe to call more than once.
func (f *FakeWatcher) Close() error {
	f.once.Do(func() {
		close(f.done) // wakes a Send that waits on a full buffer
		f.mu.Lock()
		f.closed = true
		close(f.ch)
		f.mu.Unlock()
	})
	return nil
}
