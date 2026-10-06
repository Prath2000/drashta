package indexer

import (
	"context"
	"sort"
	"time"

	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/store"
)

// publish sends an event to the hub (no-op without a hub). The hub coalesces project events.
func (i *Indexer) publish(e live.Event) {
	if i.c.Hub != nil {
		i.c.Hub.Publish(e)
	}
}

// publishCounts sends a "counts" event when a number changed since the last one.
func (i *Indexer) publishCounts(ctx context.Context) {
	c, err := i.Counts(ctx)
	if err != nil {
		i.logOnce("", "counts", err)
		return
	}
	i.mu.Lock()
	same := i.counts != nil && *i.counts == c
	if !same {
		cc := c
		i.counts = &cc
	}
	i.mu.Unlock()
	if !same {
		i.publish(live.CountsEvent(c))
	}
}

// publishDays sends one "activity" event per touched day (only days in the last 31 days,
// the longest seismograph range).
func (i *Indexer) publishDays(project string, days map[string]bool) {
	if project == "" {
		return
	}
	oldest, _ := store.AddDays(i.dayOf(i.clock.Now()), -31)
	list := make([]string, 0, len(days))
	for d := range days {
		if d >= oldest {
			list = append(list, d)
		}
	}
	sort.Strings(list)
	for _, d := range list {
		i.publish(live.ActivityEvent(project, d))
	}
}

// logOnce logs an error at most once per hour for the same project and part (03 section 10).
func (i *Indexer) logOnce(project, part string, err error) {
	key := project + "\x00" + part + "\x00" + err.Error()
	now := i.clock.Now()
	i.mu.Lock()
	last, seen := i.errSeen[key]
	if !seen || now.Sub(last) >= errLogEvery {
		i.errSeen[key] = now
		i.mu.Unlock()
		i.log.Warn("probe failed; keeping the cached data", "project", project, "part", part, "err", err)
		return
	}
	i.mu.Unlock()
}

// setStatus records the result of one probe run. A nil error clears the stale mark.
// A failing run keeps the first failure time (stale_since) until a run succeeds.
func (i *Indexer) setStatus(project, part string, err error) {
	i.mu.Lock()
	ps := i.projs[project]
	if ps == nil {
		i.mu.Unlock()
		if err != nil {
			i.logOnce(project, part, err)
		}
		return
	}
	if ps.status == nil {
		ps.status = map[string]PartStatus{}
	}
	st := ps.status[part]
	if err == nil {
		st.StaleSince, st.Error = time.Time{}, ""
	} else {
		if st.StaleSince.IsZero() {
			st.StaleSince = i.clock.Now()
		}
		st.Error = err.Error()
	}
	if st == (PartStatus{}) {
		delete(ps.status, part)
	} else {
		ps.status[part] = st
	}
	i.mu.Unlock()
	if err != nil {
		i.logOnce(project, part, err)
	}
}

// setUnavailable marks a part as "tool missing" (reason "" clears it).
func (i *Indexer) setUnavailable(project, part, reason string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	ps := i.projs[project]
	if ps == nil {
		return
	}
	if ps.status == nil {
		ps.status = map[string]PartStatus{}
	}
	st := ps.status[part]
	st.Unavailable = reason
	if st == (PartStatus{}) {
		delete(ps.status, part)
	} else {
		ps.status[part] = st
	}
}

// dayOf is the local day key of t.
func (i *Indexer) dayOf(t time.Time) string { return store.DayOf(t, i.loc) }

// daysBetween lists the local days from a to b (inclusive), at most max days ending at b.
func (i *Indexer) daysBetween(a, b time.Time, max int) []string {
	if a.IsZero() {
		a = b
	}
	if b.IsZero() {
		b = a
	}
	if b.Before(a) {
		a, b = b, a
	}
	if a.IsZero() {
		return nil
	}
	// Step day keys with calendar math, never local midnight instants: some zones
	// skip midnight on the DST day, and stepping such an instant repeats a day (I1).
	first, last := i.dayOf(a), i.dayOf(b)
	if f, err := store.AddDays(last, -(max - 1)); err == nil && f > first {
		first = f
	}
	var out []string
	for d := first; d <= last; {
		out = append(out, d)
		next, err := store.AddDays(d, 1)
		if err != nil {
			break
		}
		d = next
	}
	return out
}

// unixTime turns unix seconds into a time (0 gives the zero time).
func unixTime(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
