package gh

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// fakeClock is a clock the test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// memStore is an in-memory Store.
type memStore struct {
	mu   sync.Mutex
	data map[string][]byte
	at   map[string]time.Time
	puts int
}

func newMemStore() *memStore {
	return &memStore{data: map[string][]byte{}, at: map[string]time.Time{}}
}

func (m *memStore) Get(project, kind string) ([]byte, time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[project+"|"+kind]
	return b, m.at[project+"|"+kind], ok
}

func (m *memStore) Put(project, kind string, data []byte, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[project+"|"+kind] = append([]byte(nil), data...)
	m.at[project+"|"+kind] = at
	m.puts++
}

// countingFetch builds a Cache.Fetch that counts calls and can be told to fail.
type countingFetch struct {
	clock *fakeClock
	n     atomic.Int32
	fail  atomic.Bool
	delay time.Duration
	pr    atomic.Int32 // number of the PR returned, so tests see which fetch answered
}

func (f *countingFetch) fetch(ctx context.Context, p probe.Project, remote, branch string) (Result, error) {
	f.n.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.fail.Load() {
		return Result{}, errors.New("network is down")
	}
	repo, _ := ParseRemote(remote)
	r := emptyResult(f.clock.Now())
	r.Repo = repo.String()
	if okBranch(branch) {
		r.Branch = branch
	}
	r.PRs = []PR{{Number: int(f.pr.Add(1)), Title: "t", State: "open", URL: URLPrefix + "acme/widgets/pull/1"}}
	return r, nil
}

func newTestCache(store Store) (*Cache, *countingFetch, *fakeClock) {
	clock := newClock()
	cf := &countingFetch{clock: clock}
	c := NewCache(store)
	c.Fetch = cf.fetch
	c.Now = clock.Now
	return c, cf, clock
}

func get(t *testing.T, c *Cache, force bool) Result {
	t.Helper()
	res, err := c.Get(context.Background(), testProject, goodRemote, "main", force)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return res
}

func TestCacheTTL(t *testing.T) {
	c, cf, clock := newTestCache(nil)

	first := get(t, c, false)
	if first.PRs[0].Number != 1 || cf.n.Load() != 1 {
		t.Fatalf("first: pr=%d calls=%d", first.PRs[0].Number, cf.n.Load())
	}
	clock.Advance(4*time.Minute + 59*time.Second)
	if get(t, c, false); cf.n.Load() != 1 {
		t.Errorf("under 5 minutes must come from the cache; calls=%d", cf.n.Load())
	}
	clock.Advance(2 * time.Second) // now 5 min 1 s old
	res := get(t, c, false)
	if cf.n.Load() != 2 || res.PRs[0].Number != 2 {
		t.Errorf("past 5 minutes must refetch; calls=%d pr=%d", cf.n.Load(), res.PRs[0].Number)
	}
}

func TestCacheForceHasMinimumGap(t *testing.T) {
	c, cf, clock := newTestCache(nil)
	get(t, c, false)
	clock.Advance(3 * time.Second)
	if get(t, c, true); cf.n.Load() != 1 {
		t.Errorf("force within 10 s must not refetch; calls=%d", cf.n.Load())
	}
	clock.Advance(8 * time.Second)
	if get(t, c, true); cf.n.Load() != 2 {
		t.Errorf("force after 10 s must refetch; calls=%d", cf.n.Load())
	}
}

func TestCacheDueAfter15Minutes(t *testing.T) {
	c, _, clock := newTestCache(nil)
	if !c.Due("widgets") {
		t.Error("Due must be true before the first fetch")
	}
	get(t, c, false)
	if c.Due("widgets") {
		t.Error("Due right after a fetch")
	}
	clock.Advance(14*time.Minute + 59*time.Second)
	if c.Due("widgets") {
		t.Error("Due before 15 minutes")
	}
	clock.Advance(2 * time.Second)
	if !c.Due("widgets") {
		t.Error("not Due after 15 minutes")
	}
	if c.Due("another-project") != true {
		t.Error("an unknown project is always due")
	}
}

func TestCacheInvalidate(t *testing.T) {
	c, cf, clock := newTestCache(nil)
	get(t, c, false)
	clock.Advance(time.Second)
	c.Invalidate("widgets")
	if !c.Due("widgets") {
		t.Error("Invalidate must make the project due")
	}
	if get(t, c, false); cf.n.Load() != 2 {
		t.Errorf("Get after Invalidate must refetch even inside the TTL; calls=%d", cf.n.Load())
	}
}

func TestCacheBranchChangeRefetches(t *testing.T) {
	c, cf, _ := newTestCache(nil)
	ctx := context.Background()
	c.Get(ctx, testProject, goodRemote, "main", false)
	c.Get(ctx, testProject, goodRemote, "main", false)
	if cf.n.Load() != 1 {
		t.Fatalf("calls=%d", cf.n.Load())
	}
	res, _ := c.Get(ctx, testProject, goodRemote, "feature", false)
	if cf.n.Load() != 2 || res.Branch != "feature" {
		t.Errorf("a new branch must refetch; calls=%d branch=%q", cf.n.Load(), res.Branch)
	}
}

func TestCacheStaleOnError(t *testing.T) {
	c, cf, clock := newTestCache(nil)
	var errs []error
	c.OnError = func(id string, err error) {
		if id != "widgets" {
			t.Errorf("OnError project = %q", id)
		}
		errs = append(errs, err)
	}
	good := get(t, c, false)
	cf.fail.Store(true)
	clock.Advance(6 * time.Minute)

	res, err := c.Get(context.Background(), testProject, goodRemote, "main", false)
	if err != nil {
		t.Fatalf("old data exists, so no error: %v", err)
	}
	if res.StaleSince.IsZero() || !res.StaleSince.Equal(clock.Now()) {
		t.Errorf("StaleSince = %v", res.StaleSince)
	}
	if len(res.PRs) != 1 || res.PRs[0].Number != good.PRs[0].Number {
		t.Errorf("the old PRs must be kept: %+v", res.PRs)
	}
	if len(errs) != 1 {
		t.Errorf("OnError calls = %d", len(errs))
	}

	// Backoff: another Get right away does not call gh again.
	clock.Advance(10 * time.Second)
	res, _ = c.Get(context.Background(), testProject, goodRemote, "main", false)
	if cf.n.Load() != 2 || res.StaleSince.IsZero() {
		t.Errorf("inside the backoff: calls=%d stale=%v", cf.n.Load(), res.StaleSince)
	}
	// After the backoff it tries again, and a good answer clears the stale mark.
	clock.Advance(time.Minute)
	cf.fail.Store(false)
	res = get(t, c, false)
	if !res.StaleSince.IsZero() || cf.n.Load() != 3 || res.PRs[0].Number == good.PRs[0].Number {
		t.Errorf("after recovery: stale=%v calls=%d pr=%d", res.StaleSince, cf.n.Load(), res.PRs[0].Number)
	}
	if peek, ok := c.Peek("widgets"); !ok || !peek.StaleSince.IsZero() {
		t.Errorf("Peek after recovery: %+v %v", peek.StaleSince, ok)
	}
}

func TestCacheErrorWithoutOldData(t *testing.T) {
	c, cf, clock := newTestCache(nil)
	cf.fail.Store(true)
	if _, err := c.Get(context.Background(), testProject, goodRemote, "main", false); err == nil {
		t.Fatal("no old data: want the error")
	}
	// The failure is remembered for a minute, so a broken gh is not called on every page open.
	if _, err := c.Get(context.Background(), testProject, goodRemote, "main", false); err == nil {
		t.Fatal("want the remembered error")
	}
	if cf.n.Load() != 1 {
		t.Errorf("calls=%d; the second Get should not call gh", cf.n.Load())
	}
	clock.Advance(61 * time.Second)
	cf.fail.Store(false)
	if res := get(t, c, false); cf.n.Load() != 2 || len(res.PRs) != 1 {
		t.Errorf("after the backoff: calls=%d", cf.n.Load())
	}
}

func TestCacheSingleFlight(t *testing.T) {
	c, cf, _ := newTestCache(nil)
	cf.delay = 80 * time.Millisecond
	var wg sync.WaitGroup
	results := make([]Result, 12)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.Get(context.Background(), testProject, goodRemote, "main", false)
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}()
	}
	wg.Wait()
	if cf.n.Load() != 1 {
		t.Errorf("12 callers at once must share one fetch; calls=%d", cf.n.Load())
	}
	for _, r := range results {
		if len(r.PRs) != 1 || r.PRs[0].Number != 1 {
			t.Errorf("result = %+v", r.PRs)
		}
	}
}

func TestCacheReturnsCopies(t *testing.T) {
	c, _, _ := newTestCache(nil)
	a := get(t, c, false)
	a.PRs[0].Title = "changed by the caller"
	b := get(t, c, false)
	if b.PRs[0].Title != "t" {
		t.Errorf("the cache handed out its own slice: %q", b.PRs[0].Title)
	}
}

func TestCacheCallerCanGiveUp(t *testing.T) {
	c, cf, _ := newTestCache(nil)
	cf.delay = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Get(ctx, testProject, goodRemote, "main", false); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	// The fetch still finishes and fills the cache for the next caller.
	time.Sleep(250 * time.Millisecond)
	if get(t, c, false); cf.n.Load() != 1 {
		t.Errorf("calls=%d; the abandoned fetch should have filled the cache", cf.n.Load())
	}
}

func TestCacheNonGitHubDisabledAndUnavailable(t *testing.T) {
	c, cf, clock := newTestCache(nil)
	ctx := context.Background()

	res, err := c.Get(ctx, testProject, "git@gitlab.com:acme/widgets.git", "main", false)
	if err != nil || res.Unavailable == nil || res.Unavailable.Reason != ReasonNotGitHub || cf.n.Load() != 0 {
		t.Errorf("non-GitHub: %+v %v calls=%d", res.Unavailable, err, cf.n.Load())
	}
	res, _ = c.Get(ctx, testProject, "", "main", false)
	if res.Unavailable == nil || res.Unavailable.Reason != ReasonNoRemote || cf.n.Load() != 0 {
		t.Errorf("no remote: %+v calls=%d", res.Unavailable, cf.n.Load())
	}

	c.Disabled = true
	res, _ = c.Get(ctx, testProject, goodRemote, "main", false)
	if res.Unavailable == nil || res.Unavailable.Reason != ReasonDisabled || cf.n.Load() != 0 {
		t.Errorf("disabled: %+v calls=%d", res.Unavailable, cf.n.Load())
	}
	c.Disabled = false

	// "Unavailable" answers are reused for one minute only, so a fresh login shows up soon.
	n := 0
	c.Fetch = func(context.Context, probe.Project, string, string) (Result, error) {
		n++
		return unavailable(clock.Now(), ReasonNotLoggedIn), nil
	}
	c.Get(ctx, testProject, goodRemote, "main", false)
	clock.Advance(30 * time.Second)
	c.Get(ctx, testProject, goodRemote, "main", false)
	if n != 1 {
		t.Errorf("calls=%d inside 1 minute", n)
	}
	clock.Advance(31 * time.Second)
	c.Get(ctx, testProject, goodRemote, "main", false)
	if n != 2 {
		t.Errorf("calls=%d after 1 minute", n)
	}
}

func TestCacheStore(t *testing.T) {
	store := newMemStore()
	c, cf, clock := newTestCache(store)
	get(t, c, false)
	if store.puts != 1 {
		t.Fatalf("puts = %d", store.puts)
	}
	data, at, ok := store.Get("widgets", StoreKind)
	if !ok || !at.Equal(clock.Now()) {
		t.Fatalf("stored: ok=%v at=%v", ok, at)
	}
	var back Result
	if err := json.Unmarshal(data, &back); err != nil || back.Repo != "acme/widgets" || len(back.PRs) != 1 {
		t.Fatalf("stored JSON: %v %+v", err, back)
	}

	// A new process: a fresh Cache over the same Store.
	c2, cf2, clock2 := newTestCache(store)
	clock2.t = clock.Now().Add(2 * time.Minute)
	if res, ok := c2.Peek("widgets"); !ok || len(res.PRs) != 1 {
		t.Errorf("Peek must read the Store: %v %+v", ok, res.PRs)
	}
	if res := get(t, c2, false); cf2.n.Load() != 0 || len(res.PRs) != 1 {
		t.Errorf("2 minutes old data from the Store is fresh; calls=%d", cf2.n.Load())
	}
	clock2.Advance(14 * time.Minute)
	if !c2.Due("widgets") {
		t.Error("stored data from 16 minutes ago is due")
	}
	get(t, c2, false)
	if cf2.n.Load() != 1 {
		t.Errorf("old stored data refetches; calls=%d", cf2.n.Load())
	}

	// A fetch that fails on a new process still shows the stored data, marked stale.
	c3, cf3, clock3 := newTestCache(store)
	clock3.t = clock.Now().Add(time.Hour)
	cf3.fail.Store(true)
	res, err := c3.Get(context.Background(), testProject, goodRemote, "main", false)
	if err != nil || res.StaleSince.IsZero() || len(res.PRs) != 1 {
		t.Errorf("stored fallback: err=%v stale=%v prs=%d", err, res.StaleSince, len(res.PRs))
	}
	_ = cf
}

func TestCacheDoesNotStoreUnavailable(t *testing.T) {
	store := newMemStore()
	c, _, clock := newTestCache(store)
	c.Fetch = func(context.Context, probe.Project, string, string) (Result, error) {
		return unavailable(clock.Now(), ReasonNotLoggedIn), nil
	}
	c.Get(context.Background(), testProject, goodRemote, "main", false)
	if store.puts != 0 {
		t.Error("an Unavailable answer must not replace stored data")
	}
}

func TestCachePanicInFetchIsAnError(t *testing.T) {
	c, _, _ := newTestCache(nil)
	c.Fetch = func(context.Context, probe.Project, string, string) (Result, error) { panic("boom") }
	if _, err := c.Get(context.Background(), testProject, goodRemote, "main", false); err == nil {
		t.Error("want an error")
	}
}

// End to end: the Cache with the real Fetch and the fake gh program.
func TestCacheWithFakeGH(t *testing.T) {
	cl, dir := fakeGH(t, map[string]string{"issue.json": issuesJSON(3)})
	clock := newClock()
	cl.Now = clock.Now
	c := NewCache(newMemStore())
	c.Fetch = cl.Fetch
	c.Now = clock.Now

	res := get(t, c, false)
	if len(res.PRs) != 2 || res.Issues.OpenCount != 3 || len(res.Runs) != 1 {
		t.Fatalf("res = %+v", res)
	}
	get(t, c, false)
	if n := countCalls(t, dir, "pr list"); n != 1 {
		t.Errorf("pr list ran %d times; the second Get must use the cache", n)
	}
	clock.Advance(6 * time.Minute)
	get(t, c, false)
	if n := countCalls(t, dir, "pr list"); n != 2 {
		t.Errorf("pr list ran %d times after the TTL", n)
	}
}
