package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Prath2000/drashta/internal/indexer"
	"github.com/Prath2000/drashta/internal/store"
)

// ---- gh.Store over the gh_cache table ----

// ghStore backs gh.Store with store.WiringGHGet/Put. gh.Store has no ctx and no error,
// so each call gets a short timeout and errors are logged.
type ghStore struct {
	db  *store.DB
	log *slog.Logger
}

func (s ghStore) Get(project, kind string) ([]byte, time.Time, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, at, ok, err := s.db.WiringGHGet(ctx, project, kind)
	if err != nil {
		s.log.Warn("gh cache read", "project", project, "err", err)
		return nil, time.Time{}, false
	}
	return data, at, ok
}

func (s ghStore) Put(project, kind string, data []byte, at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.WiringGHPut(ctx, project, kind, data, at); err != nil {
		s.log.Warn("gh cache write", "project", project, "err", err)
	}
}

// ---- log once per hour ----

// hourlyLogger logs one error per key at most once per hour (03 section 10).
type hourlyLogger struct {
	mu   sync.Mutex
	seen map[string]time.Time
	log  *slog.Logger
	now  func() time.Time
}

func newHourlyLogger(log *slog.Logger) *hourlyLogger {
	return &hourlyLogger{seen: map[string]time.Time{}, log: log, now: time.Now}
}

// Log writes msg for key unless the same key was logged less than an hour ago.
func (h *hourlyLogger) Log(key, msg string, err error) bool {
	h.mu.Lock()
	now := h.now()
	if last, ok := h.seen[key]; ok && now.Sub(last) < time.Hour {
		h.mu.Unlock()
		return false
	}
	h.seen[key] = now
	h.mu.Unlock()
	h.log.Warn(msg, "key", key, "err", err)
	return true
}

// ---- indexer adapters ----

// homeIndexer adapts *indexer.Indexer (methods return an error) to httpapi.HomeIndexer.
type homeIndexer struct {
	ix  *indexer.Indexer
	log *slog.Logger
}

func (h homeIndexer) Refresh(ctx context.Context, projectID string, withGH bool) {
	if err := h.ix.Refresh(ctx, projectID, withGH); err != nil {
		h.log.Warn("refresh", "project", projectID, "err", err)
	}
}

func (h homeIndexer) RefreshAll(ctx context.Context) {
	if err := h.ix.RefreshAll(ctx); err != nil {
		h.log.Warn("refresh all", "err", err)
	}
}

// serverActions wraps the actions.Manager for the servers API: after a kill or a
// restart it asks the indexer for one lsof pass at once, so the page updates
// without waiting for the next 5 s / 60 s tick.
type serverActions struct {
	m interface {
		Kill(pid int, key string, confirm bool) error
		Restart(key string, confirm bool) (string, error)
	}
	rescan func()
}

func (a serverActions) Kill(pid int, key string, confirm bool) error {
	err := a.m.Kill(pid, key, confirm)
	if err == nil && a.rescan != nil {
		a.rescan()
	}
	return err
}

func (a serverActions) Restart(key string, confirm bool) (string, error) {
	log, err := a.m.Restart(key, confirm)
	if err == nil && a.rescan != nil {
		a.rescan()
	}
	return log, err
}
