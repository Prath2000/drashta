package indexer

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

// Every test works in t.TempDir() with a fake clock. Nothing touches ~/.drashta or ~/projects.

var waIST = time.FixedZone("IST", 5*3600+30*60)

// waClock is a clock the test moves by hand.
type waClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *waClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *waClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func waOpenDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// waActivity makes a tracker on a fresh DB with a clock set to now.
func waActivity(t *testing.T, loc *time.Location, now time.Time) (*Activity, *store.DB, *waClock) {
	t.Helper()
	db := waOpenDB(t)
	clk := &waClock{t: now}
	return NewActivity(db, loc, clk.Now), db, clk
}

func waDayPaths(t *testing.T, db *store.DB, project, day string) []string {
	t.Helper()
	p, err := db.FileChangePaths(context.Background(), project, day)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// waFilesColumn reads daily_activity.files, or -1 when the row is missing.
func waFilesColumn(t *testing.T, db *store.DB, project, day string) int {
	t.Helper()
	var n int
	err := db.Read().QueryRow(`SELECT files FROM daily_activity WHERE project_id = ? AND day = ?`, project, day).Scan(&n)
	if err != nil {
		return -1
	}
	return n
}

func waN(t *testing.T, db *store.DB, project, day, path string) int {
	t.Helper()
	var n int
	err := db.Read().QueryRow(`SELECT n FROM file_changes WHERE project_id = ? AND day = ? AND path = ?`, project, day, path).Scan(&n)
	if err != nil {
		t.Fatalf("n of %s on %s: %v", path, day, err)
	}
	return n
}

func waEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func waBucketKeys(a *Activity) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for k := range a.buckets {
		out = append(out, k.project+"/"+k.day)
	}
	sort.Strings(out)
	return out
}

var waNoon = time.Date(2026, 10, 1, 12, 0, 0, 0, waIST)

func TestActivityTracker_DistinctPathsAndSkipDirs(t *testing.T) {
	a, _, _ := waActivity(t, waIST, waNoon)
	for _, p := range []string{
		"a.go", "a.go", "./a.go", "b/c.go",
		"node_modules/x.js", "src/node_modules/y/z.js", ".git/HEAD", ".git/refs/heads/main",
		"dist/o.js", "build/o", "target/debug/x", "venv/bin/p", ".venv/x", "__pycache__/m.pyc",
		".next/x", ".cache/x", ".DS_Store", "sub/.DS_Store", ".fslckout",
		"", ".", "../escape.go", "..",
	} {
		a.Record("p1", p, waNoon)
	}
	a.Record("", "ignored-no-project.go", waNoon)
	day := "2026-10-01"
	if got := a.Count("p1", day); got != 2 {
		t.Fatalf("Count = %d, want 2 (a.go and b/c.go)", got)
	}
	if got := a.Count("p2", day); got != 0 {
		t.Fatalf("Count of another project = %d, want 0", got)
	}
	if got := a.Count("p1", "2026-09-30"); got != 0 {
		t.Fatalf("Count of another day = %d, want 0", got)
	}
}

func TestActivityTracker_DayBucketsUseTheLocalZone(t *testing.T) {
	// 23:59:30 and 00:00:30 in IST are 18:29:30 and 18:30:30 UTC: the same UTC day, two IST days.
	before := time.Date(2026, 9, 30, 23, 59, 30, 0, waIST)
	after := time.Date(2026, 10, 1, 0, 0, 30, 0, waIST)

	a, db, _ := waActivity(t, waIST, after)
	a.Record("p", "x.go", before)
	a.Record("p", "y.go", after)
	a.Record("p", "z.go", after)
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := waDayPaths(t, db, "p", "2026-09-30"); len(got) != 1 || got[0] != "x.go" {
		t.Fatalf("2026-09-30 paths = %v, want [x.go]", got)
	}
	if got := waDayPaths(t, db, "p", "2026-10-01"); len(got) != 2 {
		t.Fatalf("2026-10-01 paths = %v, want y.go and z.go", got)
	}
	if got := waFilesColumn(t, db, "p", "2026-09-30"); got != 1 {
		t.Fatalf("daily_activity.files on 09-30 = %d, want 1", got)
	}
	if got := waFilesColumn(t, db, "p", "2026-10-01"); got != 2 {
		t.Fatalf("daily_activity.files on 10-01 = %d, want 2", got)
	}

	// The same instants cut in UTC fall on one day.
	b, db2, _ := waActivity(t, time.UTC, after)
	b.Record("p", "x.go", before)
	b.Record("p", "y.go", after)
	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := waDayPaths(t, db2, "p", "2026-09-30"); len(got) != 2 {
		t.Fatalf("UTC paths on 09-30 = %v, want both", got)
	}
}

func TestActivityTracker_FlushIsIdempotent(t *testing.T) {
	a, db, _ := waActivity(t, waIST, waNoon)
	ctx := context.Background()
	day := "2026-10-01"

	a.Record("p", "a.go", waNoon)
	a.Record("p", "b.go", waNoon)
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if waN(t, db, "p", day, "a.go") != 1 || waN(t, db, "p", day, "b.go") != 1 {
		t.Fatal("first flush: each path should have n = 1")
	}
	// Flush again with nothing new: nothing changes.
	for i := 0; i < 3; i++ {
		if err := a.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if waN(t, db, "p", day, "a.go") != 1 || waN(t, db, "p", day, "b.go") != 1 {
		t.Fatal("repeated flush changed the counters")
	}
	if got := waFilesColumn(t, db, "p", day); got != 2 {
		t.Fatalf("files = %d, want 2", got)
	}
	// A new event on a known path adds to n, but the distinct count stays 2.
	a.Record("p", "a.go", waNoon)
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if waN(t, db, "p", day, "a.go") != 2 {
		t.Fatalf("a.go n = %d, want 2", waN(t, db, "p", day, "a.go"))
	}
	if got := waFilesColumn(t, db, "p", day); got != 2 {
		t.Fatalf("files = %d after a repeat event, want 2", got)
	}
	if got := a.Count("p", day); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	tally, _ := db.FileChangeCount(ctx, "p", day)
	if tally.Events != 2 || tally.Mtime != 0 {
		t.Fatalf("tally = %+v, want 2 events rows", tally)
	}
}

func TestActivityTracker_RestartDoesNotDoubleCount(t *testing.T) {
	a1, db, clk := waActivity(t, waIST, waNoon)
	ctx := context.Background()
	day := "2026-10-01"
	a1.Record("p", "a.go", waNoon)
	a1.Record("p", "b.go", waNoon)
	if err := a1.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	// A new process: a new tracker on the same database.
	a2 := NewActivity(db, waIST, clk.Now)
	if got := a2.Count("p", day); got != 2 {
		t.Fatalf("Count before any event = %d, want the stored 2", got)
	}
	a2.Record("p", "a.go", waNoon) // the same path again
	if got := a2.Count("p", day); got != 2 {
		t.Fatalf("Count after a known path = %d, want 2 (no double count)", got)
	}
	a2.Record("p", "c.go", waNoon) // a new path counts at once, before the flush
	if got := a2.Count("p", day); got != 3 {
		t.Fatalf("Count after a new path = %d, want 3", got)
	}
	if err := a2.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := waDayPaths(t, db, "p", day); len(got) != 3 {
		t.Fatalf("stored paths = %v, want 3", got)
	}
	if got := waFilesColumn(t, db, "p", day); got != 3 {
		t.Fatalf("files = %d, want 3", got)
	}
}

func TestActivityTracker_CountIsMaxOfStoredAndSet(t *testing.T) {
	a, db, _ := waActivity(t, waIST, waNoon)
	ctx := context.Background()
	day := "2026-10-01"
	// Rows written by someone else (for example a backfill from another run) count as stored.
	if _, err := db.ApplyFileMtimes(ctx, "p", day, []string{"m1.go", "m2.go", "m3.go"}); err != nil {
		t.Fatal(err)
	}
	if got := a.Count("p", day); got != 3 {
		t.Fatalf("Count = %d, want stored 3", got)
	}
	a.Record("p", "live.go", waNoon)
	if got := a.Count("p", day); got != 4 {
		t.Fatalf("Count = %d, want 4", got)
	}
}

func TestActivityTracker_BackfillDoesNotOverwriteEvents(t *testing.T) {
	a, db, _ := waActivity(t, waIST, waNoon)
	ctx := context.Background()
	today := "2026-10-01"
	yesterday := "2026-09-30"
	since := time.Date(2026, 9, 28, 12, 0, 0, 0, waIST)

	// A live row for p1 today.
	a.Record("p", "p1.go", waNoon)
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	files := []tree.Entry{
		{Path: "p1.go", MTime: waNoon.Add(-time.Hour)},                        // already an events row today
		{Path: "p2.go", MTime: waNoon.Add(-2 * time.Hour)},                    // new today
		{Path: "sub/p3.go", MTime: time.Date(2026, 9, 30, 8, 0, 0, 0, waIST)}, // yesterday
		{Path: "old.go", MTime: since.Add(-time.Hour)},                        // older than since
		{Path: "exact.go", MTime: since},                                      // not newer than since
		{Path: "future.go", MTime: waNoon.Add(72 * time.Hour)},                // clock skew
		{Path: "zero.go"},                                         // no mtime
		{Path: "node_modules/x.js", MTime: waNoon},                // skip dir
		{Path: ".git/HEAD", MTime: waNoon},                        // skip dir
		{Path: "sub", Dir: true, MTime: waNoon},                   // folders do not count
		{Path: "dist", Dir: true, Collapsed: true, MTime: waNoon}, // skip folder
	}
	if err := a.Backfill(ctx, "p", files, since); err != nil {
		t.Fatal(err)
	}

	if got := waN(t, db, "p", today, "p1.go"); got != 1 {
		t.Fatalf("p1.go n = %d, an events row must stay as it was", got)
	}
	if got := waN(t, db, "p", today, "p2.go"); got != 0 {
		t.Fatalf("p2.go n = %d, want 0 (mtime row)", got)
	}
	if got := waN(t, db, "p", yesterday, "sub/p3.go"); got != 0 {
		t.Fatalf("sub/p3.go n = %d, want 0 (mtime row)", got)
	}
	if got := waDayPaths(t, db, "p", today); len(got) != 2 {
		t.Fatalf("today paths = %v, want p1.go and p2.go", got)
	}
	if got := waDayPaths(t, db, "p", yesterday); len(got) != 1 {
		t.Fatalf("yesterday paths = %v, want sub/p3.go", got)
	}
	tally, _ := db.FileChangeCount(ctx, "p", today)
	if tally.Events != 1 || tally.Mtime != 1 {
		t.Fatalf("today tally = %+v, want 1 events + 1 mtime", tally)
	}
	if got := waFilesColumn(t, db, "p", today); got != 2 {
		t.Fatalf("files today = %d, want 2", got)
	}
	if got := waFilesColumn(t, db, "p", yesterday); got != 1 {
		t.Fatalf("files yesterday = %d, want 1", got)
	}

	// Running the same backfill again changes nothing.
	if err := a.Backfill(ctx, "p", files, since); err != nil {
		t.Fatal(err)
	}
	tally, _ = db.FileChangeCount(ctx, "p", today)
	if tally.Events != 1 || tally.Mtime != 1 {
		t.Fatalf("tally after a repeat = %+v", tally)
	}

	// A live event on a path that only had an mtime row turns it into an events row.
	a.Record("p", "p2.go", waNoon)
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	tally, _ = db.FileChangeCount(ctx, "p", today)
	if tally.Events != 2 || tally.Mtime != 0 {
		t.Fatalf("tally after the live event = %+v, want 2 events", tally)
	}
	if got := a.Count("p", today); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
}

func TestActivityTracker_BackfillLooksBackNinetyDaysAtMost(t *testing.T) {
	a, db, _ := waActivity(t, waIST, waNoon)
	ctx := context.Background()
	files := []tree.Entry{
		{Path: "ancient.go", MTime: waNoon.AddDate(0, 0, -200)},
		{Path: "recent.go", MTime: waNoon.AddDate(0, 0, -10)},
	}
	if err := a.Backfill(ctx, "p", files, time.Time{}); err != nil { // zero since: no lower bound
		t.Fatal(err)
	}
	if got := waDayPaths(t, db, "p", store.DayOf(files[1].MTime, waIST)); len(got) != 1 || got[0] != "recent.go" {
		t.Fatalf("recent day paths = %v", got)
	}
	if got := waDayPaths(t, db, "p", store.DayOf(files[0].MTime, waIST)); len(got) != 0 {
		t.Fatalf("a 200 day old file must be ignored, got %v", got)
	}
}

func TestActivityTracker_BackfillKeepsTodaysSetExact(t *testing.T) {
	a, _, _ := waActivity(t, waIST, waNoon)
	ctx := context.Background()
	a.Record("p", "live.go", waNoon) // creates and loads today's bucket
	files := []tree.Entry{{Path: "m.go", MTime: waNoon.Add(-time.Hour)}}
	if err := a.Backfill(ctx, "p", files, waNoon.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := a.Count("p", "2026-10-01"); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	// The live set must not rewrite the backfilled row: only live.go is pending.
	a.mu.Lock()
	pending := len(a.buckets[actKey{"p", "2026-10-01"}].pending)
	a.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending = %d, want 1", pending)
	}
}

func TestActivityTracker_ExtraSkipNames(t *testing.T) {
	a, _, _ := waActivity(t, waIST, waNoon)
	a.SetExtraSkip([]string{"generated", "docs/out"})
	for _, p := range []string{"generated/x.go", "src/generated/y.go", "docs/out/a.html"} {
		a.Record("p", p, waNoon)
	}
	a.Record("p", "docs/a.md", waNoon)
	a.Record("p", "src/main.go", waNoon)
	if got := a.Count("p", "2026-10-01"); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
}

func TestActivityTracker_OnFlushCallback(t *testing.T) {
	a, _, _ := waActivity(t, waIST, waNoon)
	var mu sync.Mutex
	var got []string
	a.SetOnFlush(func(project, day string) {
		mu.Lock()
		got = append(got, project+"@"+day)
		mu.Unlock()
	})
	a.Record("p1", "a.go", waNoon)
	a.Record("p2", "b.go", waNoon)
	a.Record("p2", "c.go", waNoon.Add(-24*time.Hour))
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(context.Background()); err != nil { // nothing new: no callback
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"p1@2026-10-01", "p2@2026-09-30", "p2@2026-10-01"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("callbacks = %v, want %v", got, want)
	}
}

func TestActivityTracker_FailedFlushKeepsPendingPaths(t *testing.T) {
	a, db, _ := waActivity(t, waIST, waNoon)
	day := "2026-10-01"
	a.Record("p", "a.go", waNoon)
	a.Record("p", "b.go", waNoon)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Flush(cancelled); err == nil {
		t.Fatal("a flush with a dead context must fail")
	}
	if got := waDayPaths(t, db, "p", day); len(got) != 0 {
		t.Fatalf("nothing should be stored yet, got %v", got)
	}
	if got := a.Count("p", day); got != 2 {
		t.Fatalf("Count = %d, want 2 (the set still has the paths)", got)
	}
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := waDayPaths(t, db, "p", day); len(got) != 2 {
		t.Fatalf("stored paths = %v, want 2 after the retry", got)
	}
	if waN(t, db, "p", day, "a.go") != 1 {
		t.Fatal("a.go n should be 1 (counted once)")
	}
}

func TestActivityTracker_NilDBIsHarmless(t *testing.T) {
	clk := &waClock{t: waNoon}
	a := NewActivity(nil, waIST, clk.Now)
	a.Record("p", "a.go", waNoon)
	a.Record("p", "a.go", waNoon)
	a.Record("p", "b.go", waNoon)
	if got := a.Count("p", "2026-10-01"); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Backfill(context.Background(), "p", []tree.Entry{{Path: "x", MTime: waNoon}}, time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func TestActivityTracker_DefaultsForNilArguments(t *testing.T) {
	a := NewActivity(nil, nil, nil)
	if a.loc != time.Local || a.now == nil {
		t.Fatal("nil loc and nil clock should get defaults")
	}
	a.Record("p", "a.go", time.Time{}) // zero time means now
	if got := a.Count("p", store.DayOf(time.Now(), time.Local)); got != 1 {
		t.Fatalf("Count = %d, want 1", got)
	}
}

// waTimers is a fake timer factory for Run.
type waTimers struct {
	mu    sync.Mutex
	made  []*waTimer
	count chan struct{}
}

type waTimer struct {
	d time.Duration
	c chan time.Time
}

func newWaTimers() *waTimers { return &waTimers{count: make(chan struct{}, 100)} }

func (f *waTimers) make(d time.Duration) (<-chan time.Time, func() bool) {
	tm := &waTimer{d: d, c: make(chan time.Time, 1)}
	f.mu.Lock()
	f.made = append(f.made, tm)
	f.mu.Unlock()
	f.count <- struct{}{}
	return tm.c, func() bool { return true }
}

// wait blocks until n timers have been made, and returns them.
func (f *waTimers) wait(t *testing.T, n int) []*waTimer {
	t.Helper()
	waEventually(t, fmt.Sprintf("%d timers", n), func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.made) >= n
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*waTimer(nil), f.made...)
}

func TestActivityTracker_RunFlushesMidnightAndShutdown(t *testing.T) {
	start := time.Date(2026, 9, 30, 23, 0, 0, 0, waIST)
	a, db, clk := waActivity(t, waIST, start)
	timers := newWaTimers()
	a.timer = timers.make

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()

	made := timers.wait(t, 2)
	if made[0].d != 60*time.Second {
		t.Fatalf("first timer = %v, want the 60 s flush timer", made[0].d)
	}
	if want := time.Hour + midnightSlack; made[1].d != want {
		t.Fatalf("second timer = %v, want %v (time to midnight)", made[1].d, want)
	}

	// 1. The 60 s timer flushes.
	a.Record("p", "before-midnight.go", start)
	made[0].c <- start
	waEventually(t, "flush writes the row", func() bool { return len(waDayPaths(t, db, "p", "2026-09-30")) == 1 })
	made = timers.wait(t, 3) // the flush timer is armed again
	if made[2].d != 60*time.Second {
		t.Fatalf("re-armed flush timer = %v", made[2].d)
	}

	// 2. The midnight timer flushes and clears the old day.
	a.Record("p", "late.go", start.Add(30*time.Minute)) // still the old day, not flushed yet
	clk.Set(time.Date(2026, 10, 1, 0, 0, 1, 0, waIST))
	a.Record("p", "after-midnight.go", clk.Now())
	made[1].c <- clk.Now()
	waEventually(t, "midnight flush writes both days", func() bool {
		return len(waDayPaths(t, db, "p", "2026-09-30")) == 2 && len(waDayPaths(t, db, "p", "2026-10-01")) == 1
	})
	waEventually(t, "old day cleared from memory", func() bool {
		keys := waBucketKeys(a)
		return len(keys) == 1 && keys[0] == "p/2026-10-01"
	})
	made = timers.wait(t, 4) // the midnight timer is armed again
	if got := made[len(made)-1].d; got < 23*time.Hour || got > 24*time.Hour+time.Minute {
		t.Fatalf("next midnight timer = %v, want about 24 h", got)
	}
	if got := waFilesColumn(t, db, "p", "2026-09-30"); got != 2 {
		t.Fatalf("files on the old day = %d, want 2", got)
	}

	// 3. Shutdown flushes what is pending.
	a.Record("p", "at-shutdown.go", clk.Now())
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx ended")
	}
	if got := waDayPaths(t, db, "p", "2026-10-01"); len(got) != 2 {
		t.Fatalf("paths on the new day = %v, want after-midnight.go and at-shutdown.go", got)
	}
}

func TestActivityTracker_RunWithRealTimersStopsOnCancel(t *testing.T) {
	a, db, _ := waActivity(t, waIST, waNoon)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	a.Record("p", "x.go", waNoon)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if got := waDayPaths(t, db, "p", "2026-10-01"); len(got) != 1 {
		t.Fatalf("the final flush should have written x.go, got %v", got)
	}
}

func TestActivityTracker_ConcurrentRecordAndFlush(t *testing.T) {
	a, db, _ := waActivity(t, waIST, waNoon)
	ctx := context.Background()
	day := "2026-10-01"

	var wg sync.WaitGroup
	stop := make(chan struct{})
	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		for {
			select {
			case <-stop:
				return
			default:
				_ = a.Flush(ctx)
				_ = a.Count("p0", day)
			}
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				a.Record(fmt.Sprintf("p%d", g%2), fmt.Sprintf("dir%d/file%d.go", g, i), waNoon)
				a.Record(fmt.Sprintf("p%d", g%2), "shared.go", waNoon)
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	<-flushed
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	// p0 has goroutines 0,2,4,6: 4*150 own files + shared.go.
	for _, p := range []string{"p0", "p1"} {
		want := 4*150 + 1
		if got := len(waDayPaths(t, db, p, day)); got != want {
			t.Fatalf("%s stored %d paths, want %d", p, got, want)
		}
		if got := a.Count(p, day); got != want {
			t.Fatalf("%s Count = %d, want %d", p, got, want)
		}
		if got := waFilesColumn(t, db, p, day); got != want {
			t.Fatalf("%s files column = %d, want %d", p, got, want)
		}
	}
}

func TestCleanRel(t *testing.T) {
	for in, want := range map[string]string{
		"a.go":         "a.go",
		"./a.go":       "a.go",
		"a//b/../c.go": "a/c.go",
		"/abs/x.go":    "abs/x.go",
		"":             "",
		".":            "",
		"..":           "",
		"../x":         "",
		"a/../../x":    "",
		"dir/":         "dir",
		"sp ace/é.txt": "sp ace/é.txt",
	} {
		if got := cleanRel(in); got != want {
			t.Errorf("cleanRel(%q) = %q, want %q", in, got, want)
		}
	}
}
