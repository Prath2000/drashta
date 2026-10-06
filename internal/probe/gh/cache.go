package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// Cache timings (03-architecture.md section 3, "gh").
const (
	// DefaultTTL is how long a good result answers the API endpoint.
	DefaultTTL = 5 * time.Minute
	// DefaultBackgroundEvery is the gap between background refreshes (Due).
	DefaultBackgroundEvery = 15 * time.Minute
	// DefaultUnavailableTTL is how long "not logged in" and similar answers are reused.
	DefaultUnavailableTTL = 1 * time.Minute
	// DefaultFailBackoff is how long Cache waits before it retries after a failed fetch.
	DefaultFailBackoff = 1 * time.Minute
	// DefaultMinForceGap is the shortest gap between two forced fetches of one project.
	DefaultMinForceGap = 10 * time.Second

	// StoreKind is the one kind Cache uses with a Store. The data is the JSON of a Result.
	StoreKind = "result"
)

// Store keeps the last good Result across restarts. The wiring code backs it
// with the gh_cache table (see doc.go for the suggested rows). Both methods
// must be safe for concurrent use and must not fail loudly: a missing or broken
// row is just "not found".
type Store interface {
	Get(project, kind string) (data []byte, at time.Time, ok bool)
	Put(project, kind string, data []byte, at time.Time)
}

// Cache holds the latest Result per project, in memory and (optionally) in a Store.
// Create it with NewCache; change the exported fields before the first Get.
type Cache struct {
	// Fetch does the real work. NewCache sets it to the default client's Fetch.
	Fetch func(ctx context.Context, p probe.Project, remote, branch string) (Result, error)
	Store Store            // optional
	Now   func() time.Time // clock; nil means time.Now

	TTL             time.Duration // 0 means DefaultTTL
	BackgroundEvery time.Duration // 0 means DefaultBackgroundEvery
	UnavailableTTL  time.Duration // 0 means DefaultUnavailableTTL
	FailBackoff     time.Duration // 0 means DefaultFailBackoff
	MinForceGap     time.Duration // 0 means DefaultMinForceGap

	// Disabled makes Get answer "GitHub is turned off" at once (config gh_enabled = false).
	Disabled bool
	// OnError is called after every failed fetch. The caller decides how often to log it.
	OnError func(projectID string, err error)

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	key         string // repo + "@" + branch the data belongs to
	res         Result
	have        bool      // res holds data
	fetched     time.Time // when res was fetched (zero after Invalidate)
	lastAttempt time.Time
	failedSince time.Time // zero when the last attempt worked
	lastErr     error     // the last failure (kept for the backoff when there is no old data)
	call        *call
	want        string // key of the newest fetch started; an older fetch for another key is ignored
}

// call is one fetch in flight. Everyone who asks meanwhile waits for it.
type call struct {
	key  string // repo + "@" + branch this fetch is for
	done chan struct{}
	res  Result
	err  error
}

// NewCache makes a Cache that uses the default gh client and the given Store (nil is fine).
func NewCache(store Store) *Cache {
	return &Cache{Fetch: Fetch, Store: store}
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func orDur(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func cacheKey(repo Repo, branch string) string { return repo.String() + "@" + branch }

// Get returns the result for the project. A good result younger than the TTL
// (5 minutes) is returned as it is. Otherwise gh is called, once even if many
// callers ask at the same time. force asks for a fresh call now, but not more
// often than MinForceGap per project.
//
// If the call fails and older data exists, the older data is returned with
// StaleSince set and a nil error (and OnError is called). With no older data the
// error is returned. A remote that is not on github.com, or Disabled, answers
// with Unavailable at once and caches nothing.
func (c *Cache) Get(ctx context.Context, p probe.Project, remote, branch string, force bool) (Result, error) {
	now := c.now()
	if c.Disabled {
		return unavailable(now, ReasonDisabled), nil
	}
	if remote == "" {
		return unavailable(now, ReasonNoRemote), nil
	}
	repo, ok := ParseRemote(remote)
	if !ok {
		return unavailable(now, ReasonNotGitHub), nil
	}
	key := cacheKey(repo, branchKey(branch))

	c.mu.Lock()
	e := c.entryLocked(p.ID)
	if e.have && e.key != key {
		// The branch (or remote) changed: old data is about something else.
		*e = entry{call: e.call, want: e.want}
	}
	if res, err, ok := c.servableLocked(e, now, force); ok {
		c.mu.Unlock()
		return res, err
	}
	if cl := e.call; cl != nil && cl.key == key {
		c.mu.Unlock()
		return c.wait(ctx, cl)
	}
	// No fetch in flight, or one for another branch: its answer would be about
	// something else, so start a new one for this key.
	cl := &call{key: key, done: make(chan struct{})}
	e.call, e.want = cl, key
	c.mu.Unlock()

	go c.doFetch(ctx, p, remote, branch, key, cl)
	return c.wait(ctx, cl)
}

func branchKey(branch string) string {
	if okBranch(branch) {
		return branch
	}
	return ""
}

func (c *Cache) wait(ctx context.Context, cl *call) (Result, error) {
	select {
	case <-cl.done:
		return cloneResult(cl.res), cl.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// servableLocked says whether the entry can answer without a new fetch. It can
// answer with data, or (no data and a recent failure) with the last error.
func (c *Cache) servableLocked(e *entry, now time.Time, force bool) (Result, error, bool) {
	gap := now.Sub(e.lastAttempt)
	minGap := orDur(c.MinForceGap, DefaultMinForceGap)
	if !e.failedSince.IsZero() {
		// The last attempt failed. Serve the old data (or the old error) until the
		// backoff ends. force skips the backoff but still respects the minimum gap.
		backoff := orDur(c.FailBackoff, DefaultFailBackoff)
		if gap < backoff && !(force && gap >= minGap) {
			if e.have {
				return c.staleCopy(e), nil, true
			}
			return Result{}, e.lastErr, true
		}
		return Result{}, nil, false
	}
	if !e.have {
		return Result{}, nil, false
	}
	if force {
		if gap < minGap {
			return cloneResult(e.res), nil, true
		}
		return Result{}, nil, false
	}
	ttl := orDur(c.TTL, DefaultTTL)
	if e.res.Unavailable != nil {
		ttl = orDur(c.UnavailableTTL, DefaultUnavailableTTL)
	}
	if !e.fetched.IsZero() && now.Sub(e.fetched) < ttl {
		return cloneResult(e.res), nil, true
	}
	return Result{}, nil, false
}

func (c *Cache) staleCopy(e *entry) Result {
	r := cloneResult(e.res)
	if r.Unavailable == nil {
		r.StaleSince = e.failedSince
	}
	return r
}

func (c *Cache) entryLocked(id string) *entry {
	if c.entries == nil {
		c.entries = map[string]*entry{}
	}
	e := c.entries[id]
	if e == nil {
		e = &entry{}
		c.entries[id] = e
		c.hydrateLocked(id, e)
	}
	return e
}

// hydrateLocked fills a new entry from the Store. The data counts as old (fetched
// at the stored time), so a restart shows it at once but refetches when it is past the TTL.
func (c *Cache) hydrateLocked(id string, e *entry) {
	if c.Store == nil {
		return
	}
	data, at, ok := c.Store.Get(id, StoreKind)
	if !ok || len(data) == 0 {
		return
	}
	var r Result
	if err := json.Unmarshal(data, &r); err != nil || r.Repo == "" || r.Unavailable != nil {
		return
	}
	e.res, e.have, e.fetched = r, true, at
	e.key = r.Repo + "@" + r.Branch
}

func (c *Cache) doFetch(ctx context.Context, p probe.Project, remote, branch, key string, cl *call) {
	// The fetch must not die when the first caller gives up; a short-lived
	// request context would waste the work for everyone waiting.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	fetch := c.Fetch
	if fetch == nil {
		fetch = Fetch
	}
	res, err := safeFetch(fetch, fctx, p, remote, branch)
	fetchErr := err
	now := c.now()

	c.mu.Lock()
	e := c.entryLocked(p.ID)
	superseded := e.want != "" && e.want != key // a newer fetch for another branch started
	if e.call == cl {
		e.call = nil
	}
	var persist []byte
	var persistAt time.Time
	if superseded {
		// Give this answer to its own waiters only; the entry belongs to the newer fetch.
	} else if err != nil {
		e.lastAttempt = now
		if e.failedSince.IsZero() {
			e.failedSince = now
		}
		e.lastErr = err
		if e.have && e.key == key {
			res = c.staleCopy(e)
			err = nil // old data is served; the error goes to OnError only
		}
	} else {
		e.lastAttempt = now
		if len(res.Partial) > 0 && e.have && e.key == key {
			res = keepGoodParts(res, e.res)
		}
		e.key, e.res, e.have, e.fetched = key, cloneResult(res), true, res.FetchedAt
		e.failedSince, e.lastErr = time.Time{}, nil
		if e.fetched.IsZero() {
			e.fetched = now
		}
		if res.Unavailable == nil && c.Store != nil {
			persist, _ = json.Marshal(res)
			persistAt = e.fetched
		}
	}
	c.mu.Unlock()

	if fetchErr != nil && c.OnError != nil {
		c.OnError(p.ID, fetchErr)
	}
	if persist != nil {
		c.Store.Put(p.ID, StoreKind, persist, persistAt)
	}
	cl.res, cl.err = res, err
	close(cl.done)
}

// keepGoodParts fills the parts of a partial result whose gh call failed with
// the previous data for the same repo and branch, so one failed call does not
// show "0 PRs" over good data. Partial still names the failed parts.
func keepGoodParts(res, prev Result) Result {
	for _, part := range res.Partial {
		switch part {
		case "prs":
			res.PRs = append([]PR{}, prev.PRs...)
		case "issues":
			res.Issues = prev.Issues
			res.Issues.Latest = append([]Issue{}, prev.Issues.Latest...)
		case "runs":
			res.Runs = append([]Run{}, prev.Runs...)
		}
	}
	return res
}

// safeFetch turns a panic in the fetch function into an error.
func safeFetch(f func(context.Context, probe.Project, string, string) (Result, error),
	ctx context.Context, p probe.Project, remote, branch string) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError{r}
		}
	}()
	return f(ctx, p, remote, branch)
}

type panicError struct{ v any }

func (e panicError) Error() string { return fmt.Sprint("gh fetch panicked: ", e.v) }

// Peek returns what is cached for the project (memory, then Store) without any
// network call and without checking the age. ok is false when there is nothing.
func (c *Cache) Peek(projectID string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(projectID)
	if !e.have {
		return Result{}, false
	}
	if !e.failedSince.IsZero() {
		return c.staleCopy(e), true
	}
	return cloneResult(e.res), true
}

// Due reports whether a background refresh is worth doing: nothing is cached yet,
// or the last good result is 15 minutes old or more. It does not call gh.
func (c *Cache) Due(projectID string) bool {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(projectID)
	if !e.have || e.fetched.IsZero() {
		return true
	}
	return now.Sub(e.fetched) >= orDur(c.BackgroundEvery, DefaultBackgroundEvery)
}

// Invalidate marks the project's data as old, so the next Get fetches again.
// The indexer calls it when it sees a push. Old data stays as the fallback.
func (c *Cache) Invalidate(projectID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(projectID)
	e.fetched = time.Time{}
	e.failedSince = time.Time{}
	e.lastAttempt = time.Time{}
}

// Forget drops the project from memory (not from the Store).
func (c *Cache) Forget(projectID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, projectID)
}

// cloneResult copies the slices so callers cannot change the cached data.
func cloneResult(r Result) Result {
	out := r
	out.PRs = append([]PR{}, r.PRs...)
	out.Runs = append([]Run{}, r.Runs...)
	out.Issues.Latest = append([]Issue{}, r.Issues.Latest...)
	if r.Partial != nil {
		out.Partial = append([]string{}, r.Partial...)
	}
	if r.Unavailable != nil {
		u := *r.Unavailable
		out.Unavailable = &u
	}
	return out
}
