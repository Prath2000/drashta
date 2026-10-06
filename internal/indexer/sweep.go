package indexer

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/store"
)

// sweepReq asks for a full sweep.
type sweepReq struct {
	reason string
	withGH bool // RefreshAll: also gh for projects that changed in the last 7 days
}

// requestSweep asks for a sweep without blocking. Requests that arrive while one is
// waiting are merged.
func (i *Indexer) requestSweep(r sweepReq) {
	select {
	case i.sweepCh <- r:
	default:
	}
}

// sweeper runs the startup sweep, then one every SweepEvery (hourly), plus requested ones.
func (i *Indexer) sweeper(ctx context.Context) {
	i.safeSweep(ctx, sweepReq{reason: "startup"})
	i.firstOnce.Do(func() { close(i.firstDone) })
	for {
		tick := i.clock.After(i.c.SweepEvery)
		select {
		case <-ctx.Done():
			return
		case r := <-i.sweepCh:
			i.safeSweep(ctx, r)
		case <-tick:
			i.safeSweep(ctx, sweepReq{reason: "hourly"})
		}
	}
}

// safeSweep runs one sweep and turns a panic into a log line (once per hour), so one
// bad piece of data can never kill the process or put launchd into a crash loop (I5).
func (i *Indexer) safeSweep(ctx context.Context, r sweepReq) {
	defer func() {
		if p := recover(); p != nil {
			i.logOnce("", "sweep", fmt.Errorf("panic (recovered): %v", p))
			i.log.Debug("panic stack", "stack", string(debug.Stack()))
		}
	}()
	i.runSweep(ctx, r)
}

// guard runs fn and restarts it after a panic (logged once per hour), until ctx ends.
// It is for the long-running loops (activity, lsof) (I5).
func (i *Indexer) guard(ctx context.Context, name string, fn func(context.Context)) {
	for {
		panicked := func() (p bool) {
			defer func() {
				if r := recover(); r != nil {
					i.logOnce("", name, fmt.Errorf("panic (recovered, restarting): %v", r))
					i.log.Debug("panic stack", "stack", string(debug.Stack()))
					p = true
				}
			}()
			fn(ctx)
			return false
		}()
		if !panicked || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-i.clock.After(time.Second):
		}
	}
}

// runSweep is a full sweep: all probes except gh, for all projects, then all Claude
// transcripts. The jobs go through the normal queue (one job per project at a time),
// and this function waits until they are all done.
func (i *Indexer) runSweep(ctx context.Context, r sweepReq) {
	if !i.sweeping.CompareAndSwap(false, true) {
		return
	}
	defer i.sweeping.Store(false)
	start := i.clock.Now()
	i.publish(live.ScanEvent("", "running", start))
	i.log.Info("sweep started", "reason", r.reason)

	wait := func(chs []chan struct{}) bool {
		for _, ch := range chs {
			select {
			case <-ch:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}

	// 1. the project list
	done := make(chan struct{})
	i.enqueue(request{key: jobKey{bucket: bucketProjects, kind: kindProjects}, immediate: true, done: done})
	if !wait([]chan struct{}{done}) {
		return
	}

	// 2. events that live in history tables (lost by a Reindex): server_up/down, file_burst
	if err := i.c.DB.RebuildHistoryEvents(ctx, i.loc); err != nil && ctx.Err() == nil {
		i.logOnce("", "sweep", err)
	}

	// 3. every project, and the Claude transcripts
	i.mu.Lock()
	since := i.lastSwp
	i.mu.Unlock()
	if since.IsZero() {
		since = start.AddDate(0, 0, -31) // first run: back-fill the seismograph range only
	}
	var waits []chan struct{}
	weekAgo := start.AddDate(0, 0, -7)
	for _, p := range i.Projects() {
		ch := make(chan struct{})
		gh := false
		if r.withGH {
			if ps := i.state(p.ID); ps != nil && ps.lastChange().After(weekAgo) {
				gh = true
			}
		}
		i.enqueue(request{key: jobKey{bucket: p.ID, kind: kindFull}, immediate: true, backfill: since,
			withGH: gh, done: ch})
		waits = append(waits, ch)
	}
	ch := make(chan struct{})
	i.enqueue(request{key: jobKey{bucket: bucketClaude, kind: kindClaudeAll}, immediate: true, done: ch})
	waits = append(waits, ch)
	if !wait(waits) {
		return
	}

	// Auto links once more for every project: a project's cross-project links (ledger:T-12)
	// can only be found after the other project's tasks are in the index.
	for _, p := range i.Projects() {
		if ctx.Err() != nil {
			return
		}
		i.autolink(ctx, p.ID)
	}

	end := i.clock.Now()
	if err := i.c.DB.AddSweepLog(ctx, store.SweepLog{TS: start, Scope: "all", MS: end.Sub(start).Milliseconds(), OK: true}); err != nil {
		i.logOnce("", "sweep", err)
	}
	if err := i.c.DB.SetMeta(ctx, "last_sweep_at", start.Format(time.RFC3339)); err != nil {
		i.logOnce("", "sweep", err)
	}
	i.mu.Lock()
	i.lastSwp = start
	i.mu.Unlock()
	i.publishCounts(ctx)
	i.publish(live.ScanEvent("", "done", end))
	i.log.Info("sweep done", "reason", r.reason, "ms", end.Sub(start).Milliseconds())
}
