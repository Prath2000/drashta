package indexer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/servers"
)

// dsHistory writes the commits with one `git fast-import` and returns the true
// per-day numbers in loc. Commit n adds a file of 1+n%5 lines; every 7th commit
// also deletes the file of the commit before it.
func dsHistory(t *testing.T, dir string, times []time.Time, loc *time.Location) map[string]probe.DayCount {
	t.Helper()
	var b bytes.Buffer
	want := map[string]probe.DayCount{}
	size := map[int]int{}
	data := func(s string) { fmt.Fprintf(&b, "data %d\n%s\n", len(s), s) }
	for n, tm := range times {
		stamp := fmt.Sprintf("%d %s", tm.Unix(), tm.Format("-0700"))
		fmt.Fprintf(&b, "commit refs/heads/main\nmark :%d\n", n+1)
		fmt.Fprintf(&b, "author T <t@example.com> %s\ncommitter T <t@example.com> %s\n", stamp, stamp)
		data(fmt.Sprintf("commit %d", n))
		if n > 0 {
			fmt.Fprintf(&b, "from :%d\n", n)
		}
		k := 1 + n%5
		var text strings.Builder
		for j := 0; j < k; j++ {
			fmt.Fprintf(&text, "file %d line %d\n", n, j)
		}
		fmt.Fprintf(&b, "M 100644 inline f%04d.txt\n", n)
		data(strings.TrimSuffix(text.String(), "\n"))
		size[n] = k
		d := want[tm.In(loc).Format("2006-01-02")]
		d.Commits++
		d.Plus += k
		if n > 0 && n%7 == 0 {
			fmt.Fprintf(&b, "D f%04d.txt\n", n-1)
			d.Minus += size[n-1]
		}
		want[tm.In(loc).Format("2006-01-02")] = d
	}
	cmd := exec.Command("git", "-C", dir, "fast-import", "--quiet")
	cmd.Stdin = &b
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("fast-import: %v\n%s", err, errb.String())
	}
	return want
}

// TestIndexer_DayStatsExactPastCommitCap: 280 commits over 10 local days (some in
// UTC right around IST midnight). The commits table keeps 200, but daily_activity
// must carry the exact numbers of every day, and keep them after other jobs.
func TestIndexer_DayStatsExactPastCommitCap(t *testing.T) {
	ixNeedGit(t)
	e := newIxEnv(t)
	busy := e.project("personal", "busy")
	ixGit(t, busy, e.t0, "init", "-q")

	var times []time.Time
	for d := 16; d <= 25; d++ { // e.t0 is 2026-09-25 10:00 IST
		for k := 0; k < 28; k++ {
			var tm time.Time
			switch k {
			case 0: // 23:50 IST of the day before, written in UTC
				tm = time.Date(2026, 9, d-1, 18, 20, 0, 0, time.UTC)
			case 1: // 00:10 IST of this day, written in UTC (still the day before in UTC)
				tm = time.Date(2026, 9, d-1, 18, 40, 0, 0, time.UTC)
			default:
				tm = time.Date(2026, 9, d, 8, k, 0, 0, e.loc)
			}
			times = append(times, tm)
		}
	}
	// Keep parents older than children.
	for i := 1; i < len(times); i++ {
		for j := i; j > 0 && times[j].Before(times[j-1]); j-- {
			times[j], times[j-1] = times[j-1], times[j]
		}
	}
	want := dsHistory(t, busy, times, e.loc)
	ixGit(t, busy, e.t0, "checkout", "-q", "-f", "main")

	var realCalls atomic.Int32
	e.start(func(c *Config) {
		c.Exec.GitDayStats = func(ctx context.Context, p probe.Project, now time.Time, days int, loc *time.Location) (map[string]probe.DayCount, error) {
			realCalls.Add(1)
			if days != DayStatsWindow {
				t.Errorf("days = %d", days)
			}
			return defaultGitDayStats(ctx, p, now, days, loc)
		}
	})
	e.waitFirstSweep()

	if n := e.count(`SELECT COUNT(*) FROM commits WHERE project_id='busy'`); n != 200 {
		t.Fatalf("commit rows = %d, want the 200 cap", n)
	}
	total := 0
	for day, w := range want {
		var c, p, m int
		err := e.db.Read().QueryRow(`SELECT commits, lines_plus, lines_minus FROM daily_activity
			WHERE project_id='busy' AND day=?`, day).Scan(&c, &p, &m)
		if err != nil {
			t.Fatalf("day %s: %v", day, err)
		}
		if c != w.Commits || p != w.Plus || m != w.Minus {
			t.Errorf("day %s = %d/+%d/-%d, want %d/+%d/-%d", day, c, p, m, w.Commits, w.Plus, w.Minus)
		}
		total += c
	}
	if total != len(times) || total <= 250 {
		t.Fatalf("total %d, want %d (> 250)", total, len(times))
	}
	if n := e.count(`SELECT COALESCE(SUM(commits),0) FROM daily_activity WHERE project_id='busy'`); n != len(times) {
		t.Fatalf("sum of daily commits = %d, want %d", n, len(times))
	}

	// Another job recomputes a day (as the session or tracker jobs do): the numbers stay exact.
	day := "2026-09-16"
	ixMust(t, e.db.RecomputeDaily(context.Background(), "busy", day, e.loc))
	if n := e.count(`SELECT commits FROM daily_activity WHERE project_id='busy' AND day=?`, day); n != want[day].Commits {
		t.Fatalf("after RecomputeDaily %s = %d, want %d", day, n, want[day].Commits)
	}

	// Nothing changed: a second vcs run does not read the day stats again.
	before := realCalls.Load()
	ixMust(t, e.ix.Refresh(context.Background(), "busy", false))
	time.Sleep(200 * time.Millisecond)
	if realCalls.Load() != before {
		t.Fatalf("day stats read again without a change (%d -> %d)", before, realCalls.Load())
	}
}

var defaultGitDayStats = func() func(ctx context.Context, p probe.Project, now time.Time, days int, loc *time.Location) (map[string]probe.DayCount, error) {
	var pr Probes
	pr.fill()
	return pr.GitDayStats
}()

// TestIndexer_DayStatsGitPlusFossil: a project with both VCS gets the sum. fossil gives
// counts only; its lines come from the per-commit list.
func TestIndexer_DayStatsGitPlusFossil(t *testing.T) {
	f := newIxFixture(t) // alpha: 2 git commits on 2026-09-20 (IST)
	fday := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	f.start(func(c *Config) {
		c.Exec.FossilDetect = func(path string) bool { return path == f.alpha }
		c.Exec.FossilKey = func(p probe.Project, repo string) string { return "k" }
		c.Exec.FossilScan = func(ctx context.Context, p probe.Project) (fossil.Result, error) {
			return fossil.Result{IsRepo: true, Branch: "trunk", Commits: []probe.Commit{
				{ID: strings.Repeat("ab", 20), VCS: "fossil", Msg: "fossil checkin", Time: fday, Plus: 4, Minus: 1},
			}}, nil
		}
		c.Exec.FossilDayStats = func(ctx context.Context, p probe.Project, now time.Time, days int, loc *time.Location) (map[string]probe.DayCount, error) {
			// 3 check-ins that day (only the newest is in the capped list), and 2 on the git day.
			return map[string]probe.DayCount{"2026-09-19": {Commits: 3}, "2026-09-20": {Commits: 2},
				"2026-07-01": {Commits: 9}}, nil // outside the window: ignored
		}
	})
	f.waitFirstSweep()
	row := func(day string) (c, p, m int) {
		_ = f.db.Read().QueryRow(`SELECT commits, lines_plus, lines_minus FROM daily_activity
			WHERE project_id='alpha' AND day=?`, day).Scan(&c, &p, &m)
		return
	}
	if c, p, m := row("2026-09-19"); c != 3 || p != 4 || m != 1 {
		t.Errorf("fossil day = %d/+%d/-%d, want 3/+4/-1", c, p, m)
	}
	if c, _, _ := row("2026-09-20"); c != 4 {
		t.Errorf("git+fossil day commits = %d, want 2 git + 2 fossil", c)
	}
	if n := f.count(`SELECT COUNT(*) FROM daily_activity WHERE project_id='alpha' AND day='2026-07-01'`); n != 0 {
		t.Errorf("a day outside the window was written")
	}
	if n := f.count(`SELECT COUNT(*) FROM daily_commits WHERE project_id='alpha'`); n != DayStatsWindow {
		t.Errorf("daily_commits rows = %d, want %d", n, DayStatsWindow)
	}
}

// dsFakeServers is an lsof fake that maps a server to a project the way the real
// scanner does: only when the project is in the list it is given.
func dsFakeServers(cwd string, scans *atomic.Int64, firstLen *atomic.Int64) func(ctx context.Context, ps []probe.Project) (servers.Snapshot, error) {
	return func(ctx context.Context, ps []probe.Project) (servers.Snapshot, error) {
		if scans.Add(1) == 1 {
			firstLen.Store(int64(len(ps)))
		}
		pid := ""
		for _, p := range ps {
			if p.Path == cwd {
				pid = p.ID
			}
		}
		return servers.Snapshot{Servers: []servers.Server{{Port: 5173, PID: 42, Name: "node",
			Cmd: "node vite", Cwd: cwd, ProjectID: pid, Kind: servers.KindDev}}}, nil
	}
}

// TestIndexer_ServersFirstScanWaitsForProjects: on a cold start (empty index) the
// first lsof pass runs only after the first discovery, so the server is mapped at once.
func TestIndexer_ServersFirstScanWaitsForProjects(t *testing.T) {
	f := newIxFixture(t)
	var scans, firstLen atomic.Int64
	f.start(func(c *Config) {
		c.NoServers = false
		c.Exec.Servers = dsFakeServers(f.alpha, &scans, &firstLen)
	})
	f.waitFirstSweep()
	ixEventually(t, "server mapped to alpha", func() bool {
		return f.count(`SELECT COUNT(*) FROM servers_now WHERE port=5173 AND project_id='alpha'`) == 1
	})
	if firstLen.Load() == 0 {
		t.Fatal("the first lsof pass ran before the projects were discovered")
	}
	if n := f.count(`SELECT COUNT(*) FROM server_runs WHERE project_id=''`); n != 0 {
		t.Fatalf("%d server runs without a project", n)
	}
}

// TestIndexer_ServersRemappedWhenProjectsLand: a server whose folder is not a known
// project yet has project_id ”. When the project lands, the row is mapped by one
// extra lsof pass at once, long before the next 60 s tick.
func TestIndexer_ServersRemappedWhenProjectsLand(t *testing.T) {
	f := newIxFixture(t)
	gamma := filepath.Join(f.projDir, "personal", "gamma")
	var scans, firstLen atomic.Int64
	f.start(func(c *Config) {
		c.NoServers = false
		c.Exec.Servers = dsFakeServers(gamma, &scans, &firstLen)
	})
	f.waitFirstSweep()
	ixEventually(t, "first lsof row", func() bool {
		return f.count(`SELECT COUNT(*) FROM servers_now WHERE port=5173 AND project_id=''`) == 1
	})

	// The project folder appears; the watcher reports it.
	f.project("personal", "gamma")
	f.send(WatchEvent{Path: gamma, Kind: Create})
	for n := 0; n < 20 && f.count(`SELECT COUNT(*) FROM projects WHERE id='gamma'`) == 0; n++ {
		f.clock.Advance(500 * time.Millisecond) // debounce only: far below the 60 s lsof tick
		time.Sleep(20 * time.Millisecond)
	}
	ixEventually(t, "server mapped to gamma", func() bool {
		return f.count(`SELECT COUNT(*) FROM servers_now WHERE port=5173 AND project_id='gamma'`) == 1
	})
	if f.clock.Now().Sub(f.t0) >= DefaultLsofHidden {
		t.Fatalf("mapping waited for the lsof tick")
	}
	if n := f.count(`SELECT COUNT(*) FROM server_runs WHERE ended IS NULL AND project_id='gamma'`); n != 1 {
		t.Errorf("open run not moved to gamma: %v", f.strings(`SELECT project_id FROM server_runs`))
	}
}
