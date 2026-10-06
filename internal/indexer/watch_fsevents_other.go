//go:build !darwin

package indexer

import (
	"context"
	"errors"
	"time"
)

// DefaultWatchLatency is used for a root that has no entry in the latencies map.
const DefaultWatchLatency = time.Second

// NewFSEventsWatcher is a stub on systems without FSEvents. Start always fails.
// (The real one is in watch_fsevents.go, for macOS.)
func NewFSEventsWatcher(latencies map[string]time.Duration) Watcher {
	return unsupportedWatcher{}
}

type unsupportedWatcher struct{}

func (unsupportedWatcher) Start(context.Context, []string) (<-chan WatchEvent, error) {
	return nil, errors.New("indexer: the FSEvents watcher only works on macOS")
}

func (unsupportedWatcher) Close() error { return nil }
