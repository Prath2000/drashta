package indexer

import (
	"context"
	"database/sql"
	"log/slog"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

// Per-day file activity (03-architecture.md section 4, "Per-day activity").
//
// files_changed = the number of distinct paths changed in a project on a local day, skip dirs excluded.
//   - Live: Record adds the path to an in-memory set for (project, day). Flush writes the new paths to
//     file_changes (source "events") and refreshes daily_activity.files. Run does this every 60 s and
//     at shutdown. The set is kept for today only: at midnight it is flushed and cleared.
//   - Backfill: files whose mtime is newer than the last sweep add one path to the day of their mtime
//     (source "mtime"). It never overwrites a live row.
//   - Count is max(stored, len(set)). The first Record for a (project, day) also loads the paths that
//     are already stored into the set. So a restart does not count a path twice.
//
// The source of a row is kept in file_changes.n (see internal/store/q_activity.go): n >= 1 is "events",
// n = 0 is "mtime". The table has no source column.

const (
	activityFlushEvery    = 60 * time.Second
	activityShutdownLimit = 5 * time.Second
	activityLoadLimit     = 5 * time.Second
	// A first-run backfill looks back at most this many days. The home screen shows 30 days at most.
	backfillMaxDays = 90
	// midnightSlack is added to the midnight timer so the clock is surely in the new day when it fires.
	midnightSlack = time.Second
)

type actKey struct{ project, day string }

// actBucket is the live state of one (project, day).
type actBucket struct {
	seen    map[string]struct{} // every distinct path known for the day (stored + live)
	pending map[string]int      // events not written yet, by path
	loaded  bool                // the stored paths were merged into seen
	loading bool                // a load is running
}

func newActBucket() *actBucket {
	return &actBucket{seen: map[string]struct{}{}, pending: map[string]int{}}
}

// Activity counts changed files per project and local day.
type Activity struct {
	db  *store.DB
	loc *time.Location
	now func() time.Time

	mu      sync.Mutex
	buckets map[actKey]*actBucket
	filter  *pathFilter
	onFlush func(project, day string)

	flushMu sync.Mutex // one Flush at a time

	// timer is a test hook. It returns a channel that fires after d, and a stop function.
	timer func(d time.Duration) (<-chan time.Time, func() bool)
}

// NewActivity makes an activity tracker. loc is the zone that cuts days (nil means time.Local).
// now is the clock (nil means time.Now). db may be nil: then nothing is stored and Count uses memory only.
func NewActivity(db *store.DB, loc *time.Location, now func() time.Time) *Activity {
	if loc == nil {
		loc = time.Local
	}
	if now == nil {
		now = time.Now
	}
	return &Activity{
		db:      db,
		loc:     loc,
		now:     now,
		buckets: map[actKey]*actBucket{},
		filter:  newPathFilter(nil),
		timer: func(d time.Duration) (<-chan time.Time, func() bool) {
			t := time.NewTimer(d)
			return t.C, t.Stop
		},
	}
}

// SetExtraSkip adds the config.skip names to the built-in skip list.
func (a *Activity) SetExtraSkip(names []string) {
	f := newPathFilter(names)
	a.mu.Lock()
	a.filter = f
	a.mu.Unlock()
}

// SetOnFlush sets a function that is called after rows were written for a (project, day).
// The indexer uses it to publish the "activity" event. It runs outside the internal lock.
func (a *Activity) SetOnFlush(fn func(projectID, day string)) {
	a.mu.Lock()
	a.onFlush = fn
	a.mu.Unlock()
}

// cleanRel makes a project-relative slash path. It returns "" for an empty path or one that leaves the project.
func cleanRel(p string) string {
	// A leading "/" is dropped, so a path that was given as absolute is read as relative.
	p = path.Clean(strings.TrimLeft(filepath.ToSlash(p), "/"))
	if p == "" || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// Record notes that relPath (relative to the project folder) changed at time t.
// Paths inside skip dirs are ignored. The call is cheap. The first call for a (project, day)
// also reads the already stored paths of that day, so counts stay exact after a restart.
func (a *Activity) Record(projectID, relPath string, t time.Time) {
	rel := cleanRel(relPath)
	if projectID == "" || rel == "" {
		return
	}
	if t.IsZero() {
		t = a.now()
	}
	key := actKey{projectID, store.DayOf(t, a.loc)}

	a.mu.Lock()
	if a.filter.skippedForActivity(rel) {
		a.mu.Unlock()
		return
	}
	b := a.buckets[key]
	if b == nil {
		b = newActBucket()
		a.buckets[key] = b
	}
	b.seen[rel] = struct{}{}
	b.pending[rel]++
	load := a.db != nil && !b.loaded && !b.loading
	if load {
		b.loading = true
	}
	a.mu.Unlock()

	if load {
		a.loadStored(key)
	}
}

// loadStored merges the stored paths of a (project, day) into the live set.
func (a *Activity) loadStored(key actKey) {
	ctx, cancel := context.WithTimeout(context.Background(), activityLoadLimit)
	defer cancel()
	paths, err := a.db.FileChangePaths(ctx, key.project, key.day)

	a.mu.Lock()
	defer a.mu.Unlock()
	b := a.buckets[key]
	if b == nil {
		return // the bucket was flushed and dropped meanwhile
	}
	if err == nil {
		for _, p := range paths {
			b.seen[p] = struct{}{}
		}
	}
	// On an error we still mark it loaded: Count uses max(stored, set), so it stays a safe lower bound.
	b.loaded = true
	b.loading = false
}

// Count returns the number of distinct changed files of a project on a local day (YYYY-MM-DD).
// It is max(stored, len(set)).
func (a *Activity) Count(projectID, day string) int {
	a.mu.Lock()
	n := 0
	if b := a.buckets[actKey{projectID, day}]; b != nil {
		n = len(b.seen)
	}
	a.mu.Unlock()
	if a.db == nil {
		return n
	}
	ctx, cancel := context.WithTimeout(context.Background(), activityLoadLimit)
	defer cancel()
	if t, err := a.db.FileChangeCount(ctx, projectID, day); err == nil && t.Total() > n {
		n = t.Total()
	}
	return n
}

type actBatch struct {
	key    actKey
	counts map[string]int
}

// Flush writes the new paths to file_changes and refreshes daily_activity for the touched days.
// It is safe to call at any time and more than once. A second call with nothing new writes nothing.
// If the write fails, the paths stay pending and the next Flush tries again.
func (a *Activity) Flush(ctx context.Context) error {
	a.flushMu.Lock()
	defer a.flushMu.Unlock()

	a.mu.Lock()
	var work []actBatch
	for k, b := range a.buckets {
		if len(b.pending) > 0 {
			work = append(work, actBatch{k, b.pending})
			b.pending = map[string]int{}
		}
	}
	a.mu.Unlock()
	sort.Slice(work, func(i, j int) bool {
		if work[i].key.project != work[j].key.project {
			return work[i].key.project < work[j].key.project
		}
		return work[i].key.day < work[j].key.day
	})

	if len(work) > 0 && a.db != nil {
		err := a.db.Write(ctx, func(tx *sql.Tx) error {
			for _, w := range work {
				if err := store.ApplyFileEventsTx(ctx, tx, w.key.project, w.key.day, w.counts); err != nil {
					return err
				}
				if err := store.RecomputeDailyTx(ctx, tx, w.key.project, w.key.day, a.loc); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			a.restore(work)
			return err
		}
	}

	a.dropOldDays()

	a.mu.Lock()
	cb := a.onFlush
	a.mu.Unlock()
	if cb != nil && a.db != nil {
		for _, w := range work {
			cb(w.key.project, w.key.day)
		}
	}
	return nil
}

// restore puts the paths of a failed write back into the pending sets.
func (a *Activity) restore(work []actBatch) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, w := range work {
		b := a.buckets[w.key]
		if b == nil {
			b = newActBucket()
			a.buckets[w.key] = b
		}
		for p, n := range w.counts {
			b.pending[p] += n
			b.seen[p] = struct{}{}
		}
	}
}

// dropOldDays forgets buckets of days before today that have nothing left to write.
func (a *Activity) dropOldDays() {
	today := store.DayOf(a.now(), a.loc)
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, b := range a.buckets {
		if k.day != today && len(b.pending) == 0 {
			delete(a.buckets, k)
		}
	}
}

// Backfill adds one path per file whose mtime is newer than since, on the local day of that mtime.
// files is the tree probe's listing of the project. Rows are marked source "mtime" and never replace
// a live ("events") row. Folders, skip dirs, files with an mtime in the future, and files older than
// 90 days are ignored. A zero since means "no lower bound" (within those 90 days).
func (a *Activity) Backfill(ctx context.Context, projectID string, files []tree.Entry, since time.Time) error {
	if a.db == nil || projectID == "" {
		return nil
	}
	now := a.now()
	if floor := now.AddDate(0, 0, -backfillMaxDays); since.Before(floor) {
		since = floor
	}
	future := now.Add(24 * time.Hour)

	a.mu.Lock()
	f := a.filter
	a.mu.Unlock()

	perDay := map[string][]string{}
	for _, e := range files {
		if e.Dir || e.MTime.IsZero() || !e.MTime.After(since) || e.MTime.After(future) {
			continue
		}
		rel := cleanRel(e.Path)
		if rel == "" || f.skippedForActivity(rel) {
			continue
		}
		day := store.DayOf(e.MTime, a.loc)
		perDay[day] = append(perDay[day], rel)
	}
	if len(perDay) == 0 {
		return nil
	}
	days := make([]string, 0, len(perDay))
	for d := range perDay {
		days = append(days, d)
	}
	sort.Strings(days)

	var touched []string
	err := a.db.Write(ctx, func(tx *sql.Tx) error {
		touched = touched[:0]
		for _, day := range days {
			added, err := store.ApplyFileMtimesTx(ctx, tx, projectID, day, perDay[day])
			if err != nil {
				return err
			}
			if added == 0 {
				continue
			}
			if err := store.RecomputeDailyTx(ctx, tx, projectID, day, a.loc); err != nil {
				return err
			}
			touched = append(touched, day)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Keep today's live set exact: the paths are stored now, so add them to the set.
	a.mu.Lock()
	for _, day := range days {
		if b := a.buckets[actKey{projectID, day}]; b != nil {
			for _, p := range perDay[day] {
				b.seen[p] = struct{}{}
			}
		}
	}
	cb := a.onFlush
	a.mu.Unlock()
	if cb != nil {
		for _, day := range touched {
			cb(projectID, day)
		}
	}
	return nil
}

// untilMidnight is the time to the next local midnight, plus a small slack.
func (a *Activity) untilMidnight() time.Duration {
	n := a.now().In(a.loc)
	// The first instant of tomorrow. Where midnight is skipped (DST at 00:00) that is
	// 01:00; time.Date(.., 0, 0, 0, 0) would give 23:00 of today and fire too early (I1).
	tomorrow, _ := store.AddDays(store.DayOf(n, a.loc), 1)
	next, err := store.DayStart(tomorrow, a.loc)
	if err != nil {
		next = n.Add(24 * time.Hour)
	}
	d := next.Sub(n) + midnightSlack
	if d < time.Second {
		d = time.Second
	}
	return d
}

// rollover writes everything and forgets the days that are over.
func (a *Activity) rollover(ctx context.Context) error {
	return a.Flush(ctx)
}

// Run flushes every 60 s, flushes and clears the old day at local midnight, and flushes once more
// when ctx ends. It blocks until ctx is done.
func (a *Activity) Run(ctx context.Context) {
	flushC, flushStop := a.timer(activityFlushEvery)
	midC, midStop := a.timer(a.untilMidnight())
	defer func() {
		flushStop()
		midStop()
	}()
	for {
		select {
		case <-ctx.Done():
			// ctx is over, so use a fresh one for the last write.
			sctx, cancel := context.WithTimeout(context.Background(), activityShutdownLimit)
			if err := a.Flush(sctx); err != nil {
				slog.Warn("activity: final flush failed", "err", err)
			}
			cancel()
			return
		case <-flushC:
			if err := a.Flush(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("activity: flush failed", "err", err)
			}
			flushC, flushStop = a.timer(activityFlushEvery)
		case <-midC:
			if err := a.rollover(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("activity: midnight flush failed", "err", err)
			}
			midC, midStop = a.timer(a.untilMidnight())
		}
	}
}
