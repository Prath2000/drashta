package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/git"
	"github.com/Prath2000/drashta/internal/probe/projects"
	"github.com/Prath2000/drashta/internal/probe/servers"
	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

// ixFixture builds: alpha (git, 2 commits, dirty file, README, a task), beta (no VCS),
// bigvenv (hidden, never walked), and one Claude session in alpha.
type ixFixture struct {
	*ixEnv
	alpha, beta, venv string
	c1, c2            string
	sid               string
	sessFile          string
	commitDay         time.Time
}

func newIxFixture(t *testing.T) *ixFixture {
	ixNeedGit(t)
	e := newIxEnv(t)
	f := &ixFixture{ixEnv: e}
	f.alpha = e.project("personal", "alpha")
	f.beta = e.project("personal", "beta")
	f.venv = e.project("tools", "bigvenv")
	f.commitDay = time.Date(2026, 9, 20, 15, 0, 0, 0, e.loc)

	ixGit(t, f.alpha, f.commitDay, "init", "-q")
	e.write(filepath.Join(f.alpha, "README.md"), "# Alpha\n\nThe alpha project.\n")
	f.c1 = ixCommit(t, f.alpha, "main.go", "package main\n", "first commit", f.commitDay)
	f.c2 = ixCommit(t, f.alpha, "lib.go", "package main\n\nfunc x() {}\n", "add seismograph lane for T-1",
		f.commitDay.Add(time.Hour))
	e.write(filepath.Join(f.alpha, "main.go"), "package main\n\n// dirty\n") // dirty file
	e.write(filepath.Join(f.alpha, ".drashta", "tasks", "T-1.md"),
		"---\nid: T-1\ntitle: Seismograph lane\nstatus: todo\ncreated: 2026-09-21T10:00:00+05:30\nlinks: []\nunlinks: []\n---\nBody.\n")

	e.write(filepath.Join(f.beta, "notes.txt"), "beta notes\n")
	for n := 0; n < 30; n++ {
		e.write(filepath.Join(f.venv, "lib", "pkg", "m"+string(rune('a'+n%26))+".py"), "x = 1\n")
	}

	f.sid = "11111111-2222-3333-4444-555555555555"
	f.sessFile = e.sessionPath("-x-alpha", f.sid)
	st := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	e.write(f.sessFile, ixUser(f.alpha, f.sid, "hello indexer world", st)+
		ixAssistant(f.alpha, f.sid, "Sure.", st.Add(time.Minute)))
	return f
}

func TestIndexer_FirstSweepPopulatesIndex(t *testing.T) {
	f := newIxFixture(t)
	f.start(nil)
	f.waitFirstSweep()
	ctx := context.Background()

	list, err := f.db.ProjectList(ctx)
	ixMust(t, err)
	byID := map[string]store.Project{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if len(byID) != 3 || !byID["bigvenv"].Hidden || byID["alpha"].VCS != "git" || byID["alpha"].Branch != "main" {
		t.Fatalf("projects = %+v", list)
	}
	if byID["alpha"].Description == "" || byID["alpha"].ReadmePath != "README.md" {
		t.Errorf("alpha not described: %+v", byID["alpha"])
	}
	// commits + events + FTS
	times, err := f.db.IdxCommitTimes(ctx, "alpha")
	ixMust(t, err)
	if len(times) != 2 {
		t.Fatalf("commits = %v", times)
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE project_id='alpha' AND kind='commit'`); n != 2 {
		t.Errorf("commit events = %d", n)
	}
	hits, err := f.db.Search(ctx, "seismograph", "", "commit", 10)
	ixMust(t, err)
	if len(hits) != 1 || hits[0].Ref != "commit:"+f.c2 {
		t.Errorf("FTS commit hits = %+v", hits)
	}
	// per-day row of the commit day
	day := f.commitDay.Format(store.DayLayout)
	if n := f.count(`SELECT commits FROM daily_activity WHERE project_id='alpha' AND day=?`, day); n != 2 {
		t.Errorf("daily commits on %s = %d", day, n)
	}
	// dirty
	if n := f.count(`SELECT COUNT(*) FROM dirty WHERE project_id='alpha' AND path='main.go' AND state='M'`); n != 1 {
		t.Errorf("dirty = %v", f.strings(`SELECT path FROM dirty WHERE project_id='alpha'`))
	}
	// files + docs; hidden project not walked
	if n := f.count(`SELECT COUNT(*) FROM files WHERE project_id='alpha' AND path='README.md' AND kind='doc'`); n != 1 {
		t.Errorf("README not stored as doc")
	}
	if n := f.count(`SELECT COUNT(*) FROM files WHERE project_id='beta'`); n != 1 {
		t.Errorf("beta files = %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM files WHERE project_id='bigvenv'`); n != 0 {
		t.Errorf("hidden project was walked: %d files", n)
	}
	// the session
	s, _, ok, err := f.db.IdxSession(ctx, f.sid)
	ixMust(t, err)
	if !ok || s.ProjectID != "alpha" || s.Title == "" || s.Turns != 2 {
		t.Fatalf("session = %+v ok=%v", s, ok)
	}
	if hits, _ := f.db.Search(ctx, "indexer world", "alpha", "session", 5); len(hits) != 1 {
		t.Errorf("session not in FTS: %+v", hits)
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE kind='session_start' AND ref=?`, "session:"+f.sid); n != 1 {
		t.Errorf("session_start events = %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM session_projects WHERE session_id=? AND project_id='alpha' AND reason='cwd'`, f.sid); n != 1 {
		t.Errorf("session_projects cwd row missing")
	}
	// authored
	if n := f.count(`SELECT COUNT(*) FROM tasks WHERE project_id='alpha' AND id='T-1' AND status='todo'`); n != 1 {
		t.Errorf("task not mirrored")
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE kind='task_created' AND ref='task:T-1'`); n != 1 {
		t.Errorf("task_created event missing")
	}
	// sweep log, scan and counts events
	sw, err := f.db.RecentSweeps(ctx, 5)
	ixMust(t, err)
	if len(sw) != 1 || sw[0].Scope != "all" {
		t.Errorf("sweep log = %+v", sw)
	}
	var states []string
	for _, ev := range f.events(live.TypeScan) {
		var d live.ScanData
		_ = json.Unmarshal(ev.Data, &d)
		if d.ProjectID == "" {
			states = append(states, d.State)
		}
	}
	if strings.Join(states, ",") != "running,done" {
		t.Errorf("scan events = %v", states)
	}
	if len(f.events(live.TypeCounts)) == 0 {
		t.Error("no counts event")
	}
	c, err := f.ix.Counts(ctx)
	ixMust(t, err)
	if c.OpenTasks != 1 {
		t.Errorf("counts = %+v", c)
	}
}

// ixCountingProbes wraps Walk and GitScan to count calls per project.
type ixCounter struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *ixCounter) inc(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[k]++
}

func (c *ixCounter) get(k string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[k]
}

func TestIndexer_DebounceEndToEnd(t *testing.T) {
	f := newIxFixture(t)
	walks := &ixCounter{}
	f.start(func(c *Config) {
		c.Exec.Walk = func(ctx context.Context, root string, skip []string) (tree.Result, error) {
			walks.inc(filepath.Base(root))
			return tree.Walk(ctx, root, skip)
		}
	})
	f.waitFirstSweep()
	base := walks.get("beta")

	t0 := f.clock.Now()
	f.send(WatchEvent{Path: filepath.Join(f.beta, "a.txt"), Kind: Create})
	f.clock.Advance(500 * time.Millisecond)
	f.send(WatchEvent{Path: filepath.Join(f.beta, "b.txt"), Kind: Create})
	deadline := t0.Add(1250 * time.Millisecond)
	ixEventually(t, "scheduler waits for the debounce deadline", func() bool { return f.clock.hasWaiterAt(deadline) })
	f.clock.Advance(700 * time.Millisecond) // t0+1.2s: still quiet for less than 750 ms
	time.Sleep(30 * time.Millisecond)
	if got := walks.get("beta"); got != base {
		t.Fatalf("tree job ran before the debounce: %d -> %d", base, got)
	}
	f.clock.Advance(50 * time.Millisecond) // t0+1.25s
	ixEventually(t, "one tree walk after the debounce", func() bool { return walks.get("beta") == base+1 })
	time.Sleep(30 * time.Millisecond)
	if got := walks.get("beta"); got != base+1 {
		t.Fatalf("two events must give one walk, got %d", got-base)
	}
}

func TestIndexer_StormCapEndToEnd(t *testing.T) {
	f := newIxFixture(t)
	walks := &ixCounter{}
	f.start(func(c *Config) {
		c.Exec.Walk = func(ctx context.Context, root string, skip []string) (tree.Result, error) {
			walks.inc(filepath.Base(root))
			return tree.Walk(ctx, root, skip)
		}
	})
	f.waitFirstSweep()
	base := walks.get("beta")
	// an event every 500 ms for 6 s: the job must run once at the 5 s cap, not wait for quiet
	for n := 0; n < 10; n++ {
		f.send(WatchEvent{Path: filepath.Join(f.beta, "f.txt"), Kind: Modify})
		f.clock.Advance(500 * time.Millisecond)
	}
	ixEventually(t, "a run at the storm cap", func() bool { return walks.get("beta") >= base+1 })
	if got := walks.get("beta") - base; got != 1 {
		t.Fatalf("runs during the first 5 s of a storm = %d, want 1", got)
	}
}

func TestIndexer_PerProjectSerialisation(t *testing.T) {
	f := newIxFixture(t)
	var mu sync.Mutex
	inflight := map[string]int{}
	maxPer := 0
	enter := func(id string) func() {
		mu.Lock()
		inflight[id]++
		if inflight[id] > maxPer {
			maxPer = inflight[id]
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return func() { mu.Lock(); inflight[id]--; mu.Unlock() }
	}
	var fossilScans atomic.Int64
	f.start(func(c *Config) {
		c.Exec.Walk = func(ctx context.Context, root string, skip []string) (tree.Result, error) {
			defer enter(filepath.Base(root))()
			return tree.Walk(ctx, root, skip)
		}
		c.Exec.GitScan = func(ctx context.Context, p probe.Project) (git.Result, error) {
			defer enter(p.ID)()
			return git.Scan(ctx, p)
		}
		// pretend alpha is also a fossil checkout: git and fossil must never overlap
		c.Exec.FossilDetect = func(path string) bool { return filepath.Base(path) == "alpha" }
		c.Exec.FossilKey = func(p probe.Project, repo string) string { return "k" }
		c.Exec.FossilScan = func(ctx context.Context, p probe.Project) (fossil.Result, error) {
			defer enter(p.ID)()
			fossilScans.Add(1)
			return fossil.Result{IsRepo: true, Branch: "trunk", Commits: []probe.Commit{{ID: strings.Repeat("ab", 20),
				VCS: "fossil", Msg: "fossil checkin", Time: time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)}}}, nil
		}
	})
	f.waitFirstSweep()
	ctx := context.Background()
	for n := 0; n < 5; n++ {
		ixMust(t, f.ix.Refresh(ctx, "alpha", false))
		ixMust(t, f.ix.Refresh(ctx, "beta", false))
		f.send(WatchEvent{Path: filepath.Join(f.alpha, "x.go"), Kind: Modify},
			WatchEvent{Path: filepath.Join(f.alpha, ".git", "HEAD"), Kind: Modify})
		f.clock.Advance(time.Second)
	}
	ixEventually(t, "refresh scans done", func() bool { return fossilScans.Load() >= 3 })
	f.clock.Advance(10 * time.Second)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if maxPer != 1 {
		t.Fatalf("a project ran %d probe calls at the same time; want 1", maxPer)
	}
	// git + fossil commits are stored together
	times, err := f.db.IdxCommitTimes(ctx, "alpha")
	ixMust(t, err)
	if len(times) != 3 {
		t.Fatalf("commits = %d, want 2 git + 1 fossil", len(times))
	}
	list, _ := f.db.ProjectList(ctx)
	for _, p := range list {
		if p.ID == "alpha" && p.VCS != "git,fossil" {
			t.Errorf("alpha vcs = %q", p.VCS)
		}
	}
}

func TestIndexer_MustScanGivesFullRescan(t *testing.T) {
	f := newIxFixture(t)
	walks, scans := &ixCounter{}, &ixCounter{}
	f.start(func(c *Config) {
		c.Exec.Walk = func(ctx context.Context, root string, skip []string) (tree.Result, error) {
			walks.inc(filepath.Base(root))
			return tree.Walk(ctx, root, skip)
		}
		c.Exec.GitScan = func(ctx context.Context, p probe.Project) (git.Result, error) {
			scans.inc(p.ID)
			return git.Scan(ctx, p)
		}
	})
	f.waitFirstSweep()
	w0, s0 := walks.get("alpha"), scans.get("alpha")
	f.send(WatchEvent{Path: filepath.Join(f.alpha, "src"), Kind: MustScan})
	f.clock.Advance(time.Second)
	ixEventually(t, "full rescan of alpha", func() bool { return walks.get("alpha") == w0+1 && scans.get("alpha") == s0+1 })

	// MustScan on the projects folder starts a full sweep
	sweeps0 := len(f.events(live.TypeScan))
	f.send(WatchEvent{Path: f.projDir, Kind: MustScan})
	ixEventually(t, "a second sweep", func() bool {
		sw, _ := f.db.RecentSweeps(context.Background(), 5)
		return len(sw) == 2
	})
	if len(f.events(live.TypeScan)) <= sweeps0 {
		t.Error("no scan events for the must-scan sweep")
	}
}

func TestIndexer_HiddenProjectNotWalked(t *testing.T) {
	f := newIxFixture(t)
	walks := &ixCounter{}
	f.start(func(c *Config) {
		c.Exec.Walk = func(ctx context.Context, root string, skip []string) (tree.Result, error) {
			walks.inc(filepath.Base(root))
			return tree.Walk(ctx, root, skip)
		}
	})
	f.waitFirstSweep()
	f.send(WatchEvent{Path: filepath.Join(f.venv, "lib", "new.py"), Kind: Create})
	f.clock.Advance(10 * time.Second)
	ixMust(t, f.ix.Refresh(context.Background(), "bigvenv", false))
	ixEventually(t, "refresh of the hidden project finished", func() bool {
		for _, ev := range f.events(live.TypeScan) {
			var d live.ScanData
			_ = json.Unmarshal(ev.Data, &d)
			if d.ProjectID == "bigvenv" && d.State == "done" {
				return true
			}
		}
		return false
	})
	if n := walks.get("bigvenv"); n != 0 {
		t.Fatalf("hidden project walked %d times", n)
	}
	// un-hide it: the next discovery walks it
	f.cfg.Hidden = nil
	f.send(WatchEvent{Path: filepath.Join(f.projDir, "tools"), Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "walk after un-hide", func() bool { return walks.get("bigvenv") == 1 })
	f.clock.Advance(time.Second)
	ixEventually(t, "files of the un-hidden project", func() bool {
		return f.count(`SELECT COUNT(*) FROM files WHERE project_id='bigvenv'`) > 0
	})
}

func TestIndexer_StaleDataKeptOnProbeError(t *testing.T) {
	f := newIxFixture(t)
	var fail atomic.Bool
	f.start(func(c *Config) {
		c.Exec.GitScan = func(ctx context.Context, p probe.Project) (git.Result, error) {
			if fail.Load() {
				return git.Result{}, errors.New("git: exit status 128")
			}
			return git.Scan(ctx, p)
		}
	})
	f.waitFirstSweep()
	fail.Store(true)
	ixMust(t, f.ix.Refresh(context.Background(), "alpha", false))
	ixEventually(t, "git marked stale", func() bool { return !f.ix.Status("alpha")["git"].StaleSince.IsZero() })
	st := f.ix.Status("alpha")["git"]
	if !strings.Contains(st.Error, "128") {
		t.Errorf("status = %+v", st)
	}
	times, _ := f.db.IdxCommitTimes(context.Background(), "alpha")
	if len(times) != 2 {
		t.Fatalf("cached commits lost on error: %d", len(times))
	}
	if n := f.count(`SELECT COUNT(*) FROM dirty WHERE project_id='alpha' AND path='main.go'`); n != 1 {
		t.Errorf("cached dirty list lost")
	}
	// it recovers
	fail.Store(false)
	ixMust(t, f.ix.Refresh(context.Background(), "alpha", false))
	ixEventually(t, "stale cleared", func() bool { return f.ix.Status("alpha")["git"].StaleSince.IsZero() })
}

func TestIndexer_PanicRecoveredPerJob(t *testing.T) {
	f := newIxFixture(t)
	f.start(func(c *Config) {
		c.Exec.Walk = func(ctx context.Context, root string, skip []string) (tree.Result, error) {
			if filepath.Base(root) == "beta" {
				panic("boom in walk")
			}
			return tree.Walk(ctx, root, skip)
		}
	})
	f.waitFirstSweep()
	if n := f.count(`SELECT COUNT(*) FROM files WHERE project_id='alpha'`); n == 0 {
		t.Fatal("other projects were not indexed after a panic")
	}
	if st := f.ix.Status("beta")["full"]; !strings.Contains(st.Error, "boom") {
		t.Errorf("panic not recorded in status: %+v", f.ix.Status("beta"))
	}
	// the workers still run
	ixMust(t, f.ix.Refresh(context.Background(), "alpha", false))
	ixEventually(t, "refresh after a panic", func() bool {
		for _, ev := range f.events(live.TypeScan) {
			var d live.ScanData
			_ = json.Unmarshal(ev.Data, &d)
			if d.ProjectID == "alpha" && d.State == "done" {
				return true
			}
		}
		return false
	})
}

func TestIndexer_ClaudeIncrementalAppend(t *testing.T) {
	f := newIxFixture(t)
	f.start(nil)
	f.waitFirstSweep()
	ctx := context.Background()
	if got := ixTurns(t, f.db, f.sid); got != 2 {
		t.Fatalf("turns = %d", got)
	}
	// Change an already parsed line in place (same length, same inode). An append parse does
	// not read it again, so the title must stay the same: proof that only new lines are parsed.
	b, err := os.ReadFile(f.sessFile)
	ixMust(t, err)
	b = []byte(strings.Replace(string(b), "hello indexer world", "HELLO INDEXER WORLD", 1))
	fh, err := os.OpenFile(f.sessFile, os.O_WRONLY, 0)
	ixMust(t, err)
	_, err = fh.WriteAt(b, 0)
	ixMust(t, err)
	st := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	_, err = fh.WriteAt([]byte(ixUser(f.alpha, f.sid, "second question", st)+
		ixAssistant(f.alpha, f.sid, "Answer.", st.Add(time.Minute))+`{"type":"user","partial`), int64(len(b)))
	ixMust(t, err)
	ixMust(t, fh.Close())

	f.send(WatchEvent{Path: f.sessFile, Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "appended turns", func() bool { return ixTurns(t, f.db, f.sid) == 4 })
	s, _, _, _ := f.db.IdxSession(ctx, f.sid)
	if !strings.Contains(s.Title, "hello indexer world") {
		t.Fatalf("title %q: the old lines were parsed again", s.Title)
	}
	cf, ok, err := f.db.GetClaudeFile(ctx, f.sessFile)
	ixMust(t, err)
	fi, _ := os.Stat(f.sessFile)
	if !ok || cf.Offset >= fi.Size() {
		t.Fatalf("offset %d of %d: the partial last line must be left for later", cf.Offset, fi.Size())
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE kind='session_end' AND ref=?`, "session:"+f.sid); n != 1 {
		t.Errorf("session_end events = %d, want 1 (moved, not duplicated)", n)
	}
	// Delete the file: the session goes away.
	ixMust(t, os.Remove(f.sessFile))
	f.send(WatchEvent{Path: f.sessFile, Kind: Remove})
	f.clock.Advance(time.Second)
	ixEventually(t, "session removed", func() bool { return ixTurns(t, f.db, f.sid) == -1 })
}

func TestIndexer_ClaudeSameSessionTwoProfilesKeepsMoreTurns(t *testing.T) {
	f := newIxFixture(t)
	other := filepath.Join(f.root, "claude-b")
	f.cfg.ClaudeProfiles = append(f.cfg.ClaudeProfiles, other)
	st := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	f.write(filepath.Join(other, "projects", "-x-alpha", f.sid+".jsonl"),
		ixUser(f.alpha, f.sid, "copy", st)) // 1 turn only
	f.start(nil)
	f.waitFirstSweep()
	s, _, ok, _ := f.db.IdxSession(context.Background(), f.sid)
	if !ok || s.Turns != 2 || s.JSONLPath != f.sessFile {
		t.Fatalf("kept %+v; want the copy with 2 turns", s)
	}
}

func TestIndexer_ServersLsofFailureKeepsState(t *testing.T) {
	f := newIxFixture(t)
	var mode atomic.Int32 // 0 = one server, 1 = lsof fails, 2 = no servers
	f.start(func(c *Config) {
		c.Exec.PageTitle = func(port int) string { return "Vite App" }
		c.Exec.Servers = func(ctx context.Context, ps []probe.Project) (servers.Snapshot, error) {
			switch mode.Load() {
			case 1:
				return servers.Snapshot{}, errors.New("lsof: exit status 1")
			case 2:
				return servers.Snapshot{At: f.clock.Now()}, nil
			}
			return servers.Snapshot{At: f.clock.Now(), Servers: []servers.Server{{Port: 5173, PID: 42, Name: "node",
				Cmd: "node vite", Cwd: f.alpha, ProjectID: "alpha", Kind: servers.KindDev}}}, nil
		}
	})
	f.waitFirstSweep()
	ctx := context.Background()
	ixMust(t, f.ix.ScanServersNow(ctx))
	if n := f.count(`SELECT COUNT(*) FROM servers_now WHERE port=5173 AND project_id='alpha' AND title='Vite App' AND key=?`,
		f.alpha+"|node vite"); n != 1 {
		t.Fatalf("servers_now row (key, title) wrong: %v", f.strings(`SELECT key || ' ' || title FROM servers_now`))
	}
	if n := f.count(`SELECT COUNT(*) FROM server_runs WHERE ended IS NULL`); n != 1 {
		t.Fatalf("open runs = %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE kind='server_up' AND project_id='alpha'`); n != 1 {
		t.Errorf("server_up events = %d", n)
	}
	if len(f.events(live.TypeServers)) == 0 {
		t.Error("no servers event")
	}
	c, _ := f.ix.Counts(ctx)
	if c.LiveServers != 1 {
		t.Errorf("liveServers = %d", c.LiveServers)
	}

	f.clock.Advance(time.Minute) // still running one minute later
	ixMust(t, f.ix.ScanServersNow(ctx))

	mode.Store(1)
	f.clock.Advance(time.Minute)
	if err := f.ix.ScanServersNow(ctx); err == nil {
		t.Fatal("want the lsof error")
	}
	if n := f.count(`SELECT COUNT(*) FROM servers_now`); n != 1 {
		t.Fatalf("a failed lsof cleared servers_now (%d rows)", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM server_runs WHERE ended IS NULL`); n != 1 {
		t.Fatalf("a failed lsof ended the run")
	}

	mode.Store(2)
	f.clock.Advance(time.Minute)
	ixMust(t, f.ix.ScanServersNow(ctx))
	if n := f.count(`SELECT COUNT(*) FROM servers_now`); n != 0 {
		t.Fatalf("servers_now = %d after the server stopped", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM server_runs WHERE ended IS NOT NULL`); n != 1 {
		t.Fatalf("run not ended")
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE kind='server_down' AND project_id='alpha'`); n != 1 {
		t.Errorf("server_down events = %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM daily_activity WHERE project_id='alpha' AND server_minutes > 0`); n == 0 {
		t.Errorf("server minutes not in daily activity")
	}
}

func TestIndexer_ServersCadenceFollowsVisibility(t *testing.T) {
	f := newIxFixture(t)
	var scans atomic.Int64
	f.start(func(c *Config) {
		c.NoServers = false
		c.Exec.Servers = func(ctx context.Context, ps []probe.Project) (servers.Snapshot, error) {
			scans.Add(1)
			return servers.Snapshot{At: f.clock.Now()}, nil
		}
	})
	f.waitFirstSweep()
	ixEventually(t, "first lsof", func() bool { return scans.Load() >= 1 })
	n0 := scans.Load()
	ixEventually(t, "lsof loop waits", func() bool { return f.clock.hasWaiterAt(f.t0.Add(60 * time.Second)) })
	f.clock.Advance(5 * time.Second) // no visible page: 5 s is not enough
	time.Sleep(30 * time.Millisecond)
	if scans.Load() != n0 {
		t.Fatalf("lsof ran at 5 s with no visible page")
	}
	f.clock.Advance(55 * time.Second)
	ixEventually(t, "lsof at 60 s", func() bool { return scans.Load() == n0+1 })

	conn := f.hub.Connect(0) // a visible page: 5 s cadence
	defer conn.Close()
	n1 := scans.Load()
	now := f.clock.Now()
	ixEventually(t, "lsof loop re-checks on visibility", func() bool { return f.clock.hasWaiterAt(now.Add(5 * time.Second)) })
	f.clock.Advance(5 * time.Second)
	ixEventually(t, "lsof at 5 s while visible", func() bool { return scans.Load() == n1+1 })
}

func TestIndexer_RefreshEvents(t *testing.T) {
	f := newIxFixture(t)
	gh := make(chan string, 4)
	f.start(func(c *Config) {
		c.GH = func(ctx context.Context, p probe.Project, remote, branch string, force bool) { gh <- p.ID }
	})
	f.waitFirstSweep()
	ctx := context.Background()
	if err := f.ix.Refresh(ctx, "nope", false); err == nil {
		t.Fatal("unknown project must give an error")
	}
	ixMust(t, f.ix.Refresh(ctx, "beta", true))
	ixEventually(t, "beta scan running+done", func() bool {
		var s []string
		for _, ev := range f.events(live.TypeScan) {
			var d live.ScanData
			_ = json.Unmarshal(ev.Data, &d)
			if d.ProjectID == "beta" {
				s = append(s, d.State)
			}
		}
		return strings.Join(s, ",") == "running,done"
	})
	ixMust(t, f.ix.RefreshAll(ctx))
	ixEventually(t, "second sweep", func() bool {
		sw, _ := f.db.RecentSweeps(ctx, 5)
		return len(sw) == 2
	})
	select {
	case id := <-gh:
		t.Fatalf("gh hook called for %s, which has no remote", id)
	default:
	}
}

func TestIndexer_HourlySweep(t *testing.T) {
	f := newIxFixture(t)
	f.start(nil)
	f.waitFirstSweep()
	ctx := context.Background()
	ixEventually(t, "hourly timer armed", func() bool { return f.clock.hasWaiterAt(f.t0.Add(time.Hour)) })
	f.clock.Advance(59 * time.Minute)
	time.Sleep(30 * time.Millisecond)
	if sw, _ := f.db.RecentSweeps(ctx, 5); len(sw) != 1 {
		t.Fatalf("sweeps before an hour = %d", len(sw))
	}
	f.clock.Advance(time.Minute)
	ixEventually(t, "hourly sweep", func() bool {
		sw, _ := f.db.RecentSweeps(ctx, 5)
		return len(sw) == 2
	})
}

func TestIndexer_StartupDoesNotBlockOnSweep(t *testing.T) {
	f := newIxFixture(t)
	release := make(chan struct{})
	start := time.Now()
	f.start(func(c *Config) {
		c.Exec.Discover = func(cfg *config.Config) ([]probe.Project, error) {
			<-release
			return projects.Discover(cfg)
		}
	})
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Start took %v", d)
	}
	select {
	case <-f.ix.FirstSweepDone():
		t.Fatal("the sweep cannot be done yet")
	case <-time.After(50 * time.Millisecond):
	}
	// cached data is served meanwhile: Counts works on the empty index
	if _, err := f.ix.Counts(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	f.waitFirstSweep()
}

func TestIndexer_RewrittenHistoryDropsOldCommits(t *testing.T) {
	f := newIxFixture(t)
	f.start(nil)
	f.waitFirstSweep()
	ctx := context.Background()
	// amend the last commit: c2 disappears, a new commit replaces it
	ixGit(t, f.alpha, f.commitDay.Add(2*time.Hour), "commit", "-q", "--amend", "-m", "rewritten lane message")
	newHead := ixGit(t, f.alpha, f.commitDay, "rev-parse", "HEAD")
	f.send(WatchEvent{Path: filepath.Join(f.alpha, ".git", "HEAD"), Kind: Modify},
		WatchEvent{Path: filepath.Join(f.alpha, ".git", "refs", "heads", "main"), Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "new head indexed", func() bool {
		times, _ := f.db.IdxCommitTimes(ctx, "alpha")
		_, ok := times[newHead]
		return ok
	})
	times, _ := f.db.IdxCommitTimes(ctx, "alpha")
	if _, ok := times[f.c2]; ok || len(times) != 2 {
		t.Fatalf("old commit still stored: %v", times)
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE ref=?`, "commit:"+f.c2); n != 0 {
		t.Errorf("event of the rewritten commit kept")
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE ref=?`, "commit:"+newHead); n != 1 {
		t.Errorf("event of the new commit missing")
	}
	if hits, _ := f.db.Search(ctx, "seismograph", "alpha", "commit", 5); len(hits) != 0 {
		t.Errorf("FTS still finds the rewritten commit: %+v", hits)
	}
	if hits, _ := f.db.Search(ctx, "rewritten", "alpha", "commit", 5); len(hits) != 1 {
		t.Errorf("FTS misses the new commit: %+v", hits)
	}
}

func TestIndexer_AuthoredFileEvent(t *testing.T) {
	f := newIxFixture(t)
	f.start(nil) // the real internal/autolink: the commit message "... for T-1" links T-1
	f.waitFirstSweep()
	ixEventually(t, "auto link stored", func() bool {
		return f.count(`SELECT COUNT(*) FROM task_links WHERE task_id='T-1' AND source='auto' AND ref=?`, "commit:"+f.c2) == 1
	})
	path := filepath.Join(f.alpha, ".drashta", "tasks", "T-1.md")
	f.write(path, "---\nid: T-1\ntitle: Seismograph lane\nstatus: review\ncreated: 2026-09-21T10:00:00+05:30\n"+
		"updated: 2026-09-24T11:00:00+05:30\nlinks: [file:README.md]\nunlinks: []\n---\nBody.\n")
	f.send(WatchEvent{Path: path, Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "status mirrored", func() bool {
		return f.count(`SELECT COUNT(*) FROM tasks WHERE id='T-1' AND status='review'`) == 1
	})
	if n := f.count(`SELECT COUNT(*) FROM events WHERE kind='task_status' AND ref='task:T-1'`); n != 1 {
		t.Errorf("task_status events = %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM task_links WHERE task_id='T-1' AND source='manual' AND ref='file:README.md'`); n != 1 {
		t.Errorf("manual link not mirrored")
	}
	if n := f.count(`SELECT COUNT(*) FROM task_links WHERE task_id='T-1' AND source='auto'`); n != 1 {
		t.Errorf("auto link lost")
	}
	if len(f.events(live.TypeTracker)) == 0 {
		t.Error("no tracker event")
	}
	c, _ := f.ix.Counts(context.Background())
	if c.AwaitingReview != 1 || c.OpenTasks != 1 { // status review counts, like the Tracker filter (I10)
		t.Errorf("counts = %+v", c)
	}
	// ReindexAuthored after an API write; then the file is deleted
	ixMust(t, os.Remove(path))
	ixMust(t, f.ix.ReindexAuthored("alpha", ".drashta/tasks/T-1.md"))
	if n := f.count(`SELECT COUNT(*) FROM tasks WHERE id='T-1'`); n != 0 {
		t.Errorf("deleted task still mirrored")
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE ref='task:T-1'`); n != 0 {
		t.Errorf("events of a deleted task kept")
	}
}

func TestIndexer_FileEventRecordsActivity(t *testing.T) {
	f := newIxFixture(t)
	act := NewActivity(f.db, f.loc, f.clock.Now)
	f.start(func(c *Config) { c.Activity = act })
	f.waitFirstSweep()
	ixMust(t, os.MkdirAll(filepath.Join(f.beta, "newdir"), 0o755))
	f.write(filepath.Join(f.beta, "a.txt"), "x")
	f.send(WatchEvent{Path: filepath.Join(f.beta, "a.txt"), Kind: Create},
		WatchEvent{Path: filepath.Join(f.beta, "newdir"), Kind: Create},
		WatchEvent{Path: filepath.Join(f.beta, "node_modules", "x.js"), Kind: Create})
	day := f.clock.Now().In(f.loc).Format(store.DayLayout)
	if got := act.Count("beta", day); got != 1 {
		t.Fatalf("activity count = %d, want 1 (a folder and a skip dir do not count)", got)
	}
}

func TestIndexer_CrossProjectAutoLinksSurviveOtherRuns(t *testing.T) {
	f := newIxFixture(t)
	ixGit(t, f.beta, f.commitDay, "init", "-q")
	bc := ixCommit(t, f.beta, "b.txt", "b\n", "port the lane, see alpha:T-1", f.commitDay.Add(3*time.Hour))
	f.start(nil)
	f.waitFirstSweep()
	ctx := context.Background()
	cross := "beta:commit:" + bc
	q := `SELECT COUNT(*) FROM task_links WHERE project_id='alpha' AND task_id='T-1' AND source='auto' AND ref=?`
	if n := f.count(q, cross); n != 1 {
		t.Fatalf("cross link missing; links = %v", f.strings(`SELECT ref FROM task_links`))
	}
	if n := f.count(q, "commit:"+f.c2); n != 1 {
		t.Fatalf("own link missing")
	}
	// A new run for alpha (its own links change) must keep beta's cross link.
	ixCommit(t, f.alpha, "c.go", "package main\n", "more work on T-1", f.commitDay.Add(4*time.Hour))
	ixMust(t, f.ix.Refresh(ctx, "alpha", false))
	ixEventually(t, "alpha re-linked", func() bool {
		return f.count(`SELECT COUNT(*) FROM task_links WHERE project_id='alpha' AND source='auto' AND substr(ref,1,7)='commit:'`) == 2
	})
	if n := f.count(q, cross); n != 1 {
		t.Fatalf("alpha's run deleted beta's cross link")
	}
	// The manual link of a task always wins over the same auto ref.
	if n := f.count(`SELECT COUNT(*) FROM task_links WHERE source='manual'`); n != 0 {
		t.Fatalf("unexpected manual links")
	}
}

func TestIndexer_InboxDropIsFiledAndCountsAwaitingReview(t *testing.T) {
	f := newIxFixture(t)
	f.write(filepath.Join(f.alpha, ".drashta", "tasks", "T-1.md"),
		"---\nid: T-1\ntitle: Seismograph lane\nstatus: in_progress\ncreated: 2026-09-21T10:00:00+05:30\nlinks: []\nunlinks: []\n---\nBody.\n")
	f.start(nil)
	f.waitFirstSweep()
	ctx := context.Background()
	if c, _ := f.ix.Counts(ctx); c.AwaitingReview != 0 {
		t.Fatalf("awaitingReview before = %d", c.AwaitingReview)
	}
	drop := filepath.Join(f.alpha, ".drashta", "snapshots", "inbox", "T-1--lane-pass.png")
	f.write(drop, "\x89PNG fake image bytes")
	unnamed := filepath.Join(f.alpha, ".drashta", "snapshots", "inbox", "screenshot.png")
	f.write(unnamed, "\x89PNG other")
	f.send(WatchEvent{Path: drop, Kind: Create}, WatchEvent{Path: unnamed, Kind: Create})
	f.clock.Advance(time.Second)
	ixEventually(t, "snapshot filed and mirrored", func() bool {
		return f.count(`SELECT COUNT(*) FROM snapshots WHERE project_id='alpha' AND task_id='T-1' AND version=1 AND verdict='pending'`) == 1
	})
	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Errorf("the drop was not removed")
	}
	if _, err := os.Stat(unnamed); err != nil {
		t.Errorf("a drop without a task name must stay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.alpha, ".drashta", "snapshots", "T-1", "v1.png")); err != nil {
		t.Errorf("image not stored: %v", err)
	}
	ixEventually(t, "task moved to review", func() bool {
		return f.count(`SELECT COUNT(*) FROM tasks WHERE id='T-1' AND status='review'`) == 1
	})
	if n := f.count(`SELECT COUNT(*) FROM events WHERE kind='snapshot' AND ref='snapshot:T-1/v1' AND json_extract(meta,'$.verdict')='pending'`); n != 1 {
		t.Errorf("snapshot event with meta.verdict missing")
	}
	// awaitingReview counts tasks whose newest snapshot is pending (same rule as /api/boot)
	c, err := f.ix.Counts(ctx)
	ixMust(t, err)
	hc, err := f.db.HomeBootCounts(ctx)
	ixMust(t, err)
	if c.AwaitingReview != 1 || c.AwaitingReview != hc.AwaitingReview || c.OpenTasks != hc.OpenTasks {
		t.Fatalf("counts %+v disagree with /boot %+v", c, hc)
	}
	// a verdict on the newest snapshot clears it
	snapMD := filepath.Join(f.alpha, ".drashta", "snapshots", "T-1", "v1.md")
	b, err := os.ReadFile(snapMD)
	ixMust(t, err)
	f.write(snapMD, strings.Replace(string(b), "verdict: pending", "verdict: approved", 1))
	ixMust(t, f.ix.ReindexAuthored("alpha", ".drashta/snapshots/T-1/v1.md"))
	// The task is still in status review, so the Tracker filter still lists it (I10).
	if c, _ := f.ix.Counts(ctx); c.AwaitingReview != 1 {
		t.Fatalf("awaitingReview after approval = %d", c.AwaitingReview)
	}
	// Moving the task to done clears it.
	taskMD := filepath.Join(f.alpha, ".drashta", "tasks", "T-1.md")
	b, err = os.ReadFile(taskMD)
	ixMust(t, err)
	f.write(taskMD, strings.Replace(string(b), "status: review", "status: done", 1))
	ixMust(t, f.ix.ReindexAuthored("alpha", ".drashta/tasks/T-1.md"))
	if c, _ := f.ix.Counts(ctx); c.AwaitingReview != 0 {
		t.Fatalf("awaitingReview after done = %d", c.AwaitingReview)
	}
}

func TestIndexer_DualVCSKeepsBothCommitLists(t *testing.T) {
	f := newIxFixture(t)
	fossilID := strings.Repeat("cd", 20)
	f.start(func(c *Config) {
		c.Exec.FossilDetect = func(path string) bool { return filepath.Base(path) == "alpha" }
		c.Exec.FossilKey = func(p probe.Project, repo string) string { return "same" }
		c.Exec.FossilScan = func(ctx context.Context, p probe.Project) (fossil.Result, error) {
			// newer than every git commit, so a git-only ReplaceCommits would drop it
			return fossil.Result{IsRepo: true, Branch: "trunk", Commits: []probe.Commit{{ID: fossilID, VCS: "fossil",
				Msg: "fossil side", Time: f.commitDay.Add(30 * time.Minute)}}}, nil
		}
	})
	f.waitFirstSweep()
	ctx := context.Background()
	times, _ := f.db.IdxCommitTimes(ctx, "alpha")
	if len(times) != 3 {
		t.Fatalf("commits after sweep = %d, want 2 git + 1 fossil", len(times))
	}
	// a git-only change (the fossil key does not change)
	head := ixCommit(t, f.alpha, "d.go", "package main\n", "git side again", f.commitDay.Add(5*time.Hour))
	f.send(WatchEvent{Path: filepath.Join(f.alpha, ".git", "HEAD"), Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "new git commit", func() bool {
		times, _ := f.db.IdxCommitTimes(ctx, "alpha")
		_, ok := times[head]
		return ok
	})
	times, _ = f.db.IdxCommitTimes(ctx, "alpha")
	if _, ok := times[fossilID]; !ok || len(times) != 4 {
		t.Fatalf("the git update dropped the fossil commit: %v", times)
	}
	list, _ := f.db.ProjectList(ctx)
	for _, p := range list {
		if p.ID == "alpha" && (p.VCS != "git,fossil" || p.IndexedAt.IsZero()) {
			t.Errorf("alpha row = %+v", p)
		}
	}
}
