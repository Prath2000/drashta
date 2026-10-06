//go:build darwin

package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsevents"
)

// DefaultWatchLatency is used for a root that has no entry in the latencies map.
const DefaultWatchLatency = time.Second

// fsEventBuffer is the size of the channel between the FSEvents callback and our reader.
const fsEventBuffer = 64

// fsOutBuffer is the size of the public event channel.
const fsOutBuffer = 4096

// closeStopTimeout is how long Close waits for the FSEvents streams to stop.
const closeStopTimeout = 3 * time.Second

// fseWatcher is the real watcher. Streams are grouped by latency. With the default setup
// that is two streams: ~/projects (1 s) and the Claude profile dirs (2 s). A stream is recursive
// and costs no open file per watched file, so there is no file-count limit.
type fseWatcher struct {
	latencies map[string]time.Duration

	// Test hooks.
	lstat func(string) (os.FileInfo, error)
	now   func() time.Time

	mu       sync.Mutex
	state    int // 0 new, 1 started, 2 closed
	extra    []string
	filter   *pathFilter
	streams  []*fseStream
	out      chan WatchEvent
	stopping chan struct{} // closed first: readers stop forwarding but keep draining
	done     chan struct{} // closed after the streams are stopped: readers exit
	wg       sync.WaitGroup
}

type fseStream struct {
	es    *fsevents.EventStream
	roots []watchRoot
}

// NewFSEventsWatcher makes the real watcher. latencies maps a root path (as you will pass it to
// Start) to the FSEvents latency. A root without an entry gets DefaultWatchLatency (1 s).
func NewFSEventsWatcher(latencies map[string]time.Duration) Watcher {
	return newFSEventsWatcher(latencies)
}

func newFSEventsWatcher(latencies map[string]time.Duration) *fseWatcher {
	cp := make(map[string]time.Duration, len(latencies))
	for k, v := range latencies {
		cp[k] = v
	}
	return &fseWatcher{
		latencies: cp,
		lstat:     os.Lstat,
		now:       time.Now,
		out:       make(chan WatchEvent, fsOutBuffer),
		stopping:  make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// SetExtraSkip adds the config.skip names. Call it before Start.
func (w *fseWatcher) SetExtraSkip(names []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.extra = append([]string(nil), names...)
}

func (w *fseWatcher) latencyFor(root watchRoot, given string) time.Duration {
	for _, k := range []string{given, root.orig, root.real} {
		if d, ok := w.latencies[k]; ok && d > 0 {
			return d
		}
	}
	return DefaultWatchLatency
}

// Start opens the streams. A root that does not exist is skipped with a warning.
// It fails when no root can be watched or a stream does not start.
func (w *fseWatcher) Start(ctx context.Context, roots []string) (<-chan WatchEvent, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.state {
	case 1:
		return nil, errors.New("indexer: watcher already started")
	case 2:
		return nil, errors.New("indexer: watcher is closed")
	}
	w.filter = newPathFilter(w.extra)

	// Group the roots by latency: one stream per latency.
	groups := map[time.Duration][]watchRoot{}
	for _, given := range roots {
		r, err := newWatchRoot(given)
		if err != nil {
			slog.Warn("watch: root skipped", "root", given, "err", err)
			continue
		}
		lat := w.latencyFor(r, given)
		groups[lat] = append(groups[lat], r)
	}
	if len(groups) == 0 {
		return nil, errors.New("indexer: none of the watch roots exist")
	}
	lats := make([]time.Duration, 0, len(groups))
	for l := range groups {
		lats = append(lats, l)
	}
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })

	var started []*fseStream
	for _, lat := range lats {
		rs := groups[lat]
		paths := make([]string, len(rs))
		for i, r := range rs {
			paths[i] = r.real
		}
		es := &fsevents.EventStream{
			Paths:   paths,
			Latency: lat,
			Flags:   fsevents.FileEvents,
			Events:  make(chan []fsevents.Event, fsEventBuffer),
		}
		if err := es.Start(); err != nil {
			for _, s := range started {
				s.es.Stop()
			}
			return nil, fmt.Errorf("indexer: start FSEvents stream for %v: %w", paths, err)
		}
		started = append(started, &fseStream{es: es, roots: rs})
	}

	w.streams = started
	w.state = 1
	for _, st := range started {
		w.wg.Add(1)
		go w.read(st)
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = w.Close()
		case <-w.done:
		}
	}()
	return w.out, nil
}

// read turns the batches from one stream into WatchEvents.
func (w *fseWatcher) read(st *fseStream) {
	defer w.wg.Done()
	var buf []WatchEvent
	for {
		select {
		case batch := <-st.es.Events:
			select {
			case <-w.stopping:
				continue // closing: keep draining so the FSEvents callback never blocks
			default:
			}
			now := w.now()
			buf = buf[:0]
			for _, e := range batch {
				buf = w.translate(st, e, now, buf)
			}
			for _, ev := range buf {
				select {
				case w.out <- ev:
				case <-w.stopping:
				}
			}
		case <-w.done:
			return
		}
	}
}

// translate maps one FSEvents event to zero or more WatchEvents (appended to buf).
func (w *fseWatcher) translate(st *fseStream, e fsevents.Event, now time.Time, buf []WatchEvent) []WatchEvent {
	f := e.Flags
	orig, rel, ok := mapEvent(st.roots, e.Path)

	// Dropped or merged events: the caller must rescan.
	const scanFlags = fsevents.MustScanSubDirs | fsevents.UserDropped | fsevents.KernelDropped
	if f&scanFlags != 0 {
		if !ok {
			// No usable path: rescan every root of this stream.
			for _, r := range st.roots {
				buf = append(buf, WatchEvent{Path: r.orig, Kind: MustScan, Time: now})
			}
			return buf
		}
		if rel != "" && w.filter.ignoredForWatch(rel) {
			return buf
		}
		return append(buf, WatchEvent{Path: orig, Kind: MustScan, Time: now})
	}
	if !ok {
		return buf
	}
	if f&(fsevents.Mount|fsevents.Unmount) != 0 {
		return append(buf, WatchEvent{Path: orig, Kind: MustScan, Time: now})
	}
	if rel != "" && w.filter.ignoredForWatch(rel) {
		return buf
	}

	const itemFlags = fsevents.ItemCreated | fsevents.ItemRemoved | fsevents.ItemRenamed |
		fsevents.ItemModified | fsevents.ItemInodeMetaMod
	if f&itemFlags == 0 {
		// Only xattr, Finder info or owner changes (or a sentinel). Not a content change.
		return buf
	}
	kind := w.kindOf(f, orig)
	if f&fsevents.ItemIsDir != 0 && kind == Modify {
		return buf // a folder's own mtime changes whenever a child changes: noise
	}
	return append(buf, WatchEvent{Path: orig, Kind: kind, Time: now})
}

// kindOf picks the kind from the item flags. FSEvents can merge several steps for one path
// (created, then modified, then removed). Then it looks at the disk to see how the path ended.
func (w *fseWatcher) kindOf(f fsevents.EventFlags, path string) WatchKind {
	created := f&fsevents.ItemCreated != 0
	removed := f&fsevents.ItemRemoved != 0
	renamed := f&fsevents.ItemRenamed != 0
	switch {
	case removed && (created || renamed || f&(fsevents.ItemModified|fsevents.ItemInodeMetaMod) != 0):
		if _, err := w.lstat(path); err != nil {
			return Remove
		}
		switch {
		case created:
			return Create
		case renamed:
			return Rename
		}
		return Modify
	case removed:
		return Remove
	case created:
		return Create
	case renamed:
		return Rename
	}
	return Modify
}

// Close stops the streams and closes the event channel. Safe to call more than once,
// and before Start.
func (w *fseWatcher) Close() error {
	w.mu.Lock()
	switch w.state {
	case 2:
		w.mu.Unlock()
		return nil
	case 0:
		w.state = 2
		close(w.stopping)
		close(w.done)
		close(w.out)
		w.mu.Unlock()
		return nil
	}
	w.state = 2
	streams := w.streams
	w.mu.Unlock()

	// Readers keep draining while we stop the streams, so a callback that is blocked on a full
	// channel can finish and Stop does not wait for it forever.
	close(w.stopping)
	stopped := make(chan struct{})
	go func() {
		for _, st := range streams {
			st.es.Stop()
		}
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(closeStopTimeout):
		slog.Warn("watch: FSEvents streams did not stop in time")
	}
	close(w.done)
	w.wg.Wait()
	close(w.out)
	return nil
}
