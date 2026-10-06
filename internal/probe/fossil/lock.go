package fossil

import (
	"context"
	"sync"
)

// Fossil takes a read lock on the repository database. Two fossil processes in
// one project at the same time can fail with "database is locked", so drashta
// runs at most one fossil call per project at a time (03-architecture.md
// section 3). Different projects run in parallel.

var (
	locksMu sync.Mutex
	locks   = map[string]chan struct{}{}
)

// lockFor returns the one-slot semaphore of a project key.
func lockFor(key string) chan struct{} {
	locksMu.Lock()
	defer locksMu.Unlock()
	sem, ok := locks[key]
	if !ok {
		sem = make(chan struct{}, 1)
		locks[key] = sem
	}
	return sem
}

// withLock runs fn while holding the project's slot. It gives up with the
// context error when ctx ends before the slot is free.
func withLock(ctx context.Context, key string, fn func() error) error {
	sem := lockFor(key)
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-sem }()
	return fn()
}
