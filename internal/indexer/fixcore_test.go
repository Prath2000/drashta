package indexer

// Regression tests for the Wave 3 fix wave (fix-core). Each test names the finding it covers.

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/autolink"
	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/git"
	"github.com/Prath2000/drashta/internal/probe/projects"
	"github.com/Prath2000/drashta/internal/probe/tree"
)

func fcScanDone(f *ixFixture, id string, n int) bool {
	got := 0
	for _, ev := range f.events(live.TypeScan) {
		var d live.ScanData
		_ = json.Unmarshal(ev.Data, &d)
		if d.ProjectID == id && d.State == "done" {
			got++
		}
	}
	return got >= n
}

// I2: git+fossil, fossil's oldest commit is older than git's. A git commit that drops off
// git's cap must not be deleted (with its event) on the next sweep.
func TestFixI2IndexerCutOffPerVCS(t *testing.T) {
	f := newIxFixture(t)
	day := func(d int) time.Time { return time.Date(2026, 9, d, 12, 0, 0, 0, f.loc) }
	g := func(id string, d int) probe.Commit {
		return probe.Commit{ID: strings.Repeat(id, 20), VCS: "git", Msg: "git " + id, Time: day(d)}
	}
	var mu sync.Mutex
	gitList := []probe.Commit{g("c3", 13), g("c2", 12), g("c1", 11)}
	gitKey := "k1"
	f.start(func(c *Config) {
		c.Exec.GitKey = func(p probe.Project) string {
			if p.ID != "alpha" {
				return ""
			}
			mu.Lock()
			defer mu.Unlock()
			return gitKey
		}
		c.Exec.GitScan = func(ctx context.Context, p probe.Project) (git.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			return git.Result{IsRepo: true, Branch: "main", Commits: append([]probe.Commit(nil), gitList...)}, nil
		}
		c.Exec.GitDayStats = func(context.Context, probe.Project, time.Time, int, *time.Location) (map[string]probe.DayCount, error) {
			return map[string]probe.DayCount{}, nil
		}
		c.Exec.FossilDetect = func(path string) bool { return filepath.Base(path) == "alpha" }
		c.Exec.FossilKey = func(p probe.Project, repo string) string { return "same" }
		c.Exec.FossilScan = func(ctx context.Context, p probe.Project) (fossil.Result, error) {
			return fossil.Result{IsRepo: true, Commits: []probe.Commit{{ID: strings.Repeat("f1", 20), VCS: "fossil",
				Msg: "old fossil", Time: day(1)}}}, nil
		}
		c.Exec.FossilDayStats = func(context.Context, probe.Project, time.Time, int, *time.Location) (map[string]probe.DayCount, error) {
			return map[string]probe.DayCount{}, nil
		}
	})
	f.waitFirstSweep()
	c1 := strings.Repeat("c1", 20)
	if n := f.count(`SELECT COUNT(*) FROM commits WHERE id = ?`, c1); n != 1 {
		t.Fatalf("c1 not stored after the first sweep")
	}
	mu.Lock()
	gitList = []probe.Commit{g("c4", 14), g("c3", 13), g("c2", 12)} // c1 fell off git's cap
	gitKey = "k2"
	mu.Unlock()
	ixMust(t, f.ix.Refresh(context.Background(), "alpha", false))
	ixEventually(t, "c4 stored", func() bool {
		return f.count(`SELECT COUNT(*) FROM commits WHERE id = ?`, strings.Repeat("c4", 20)) == 1
	})
	if n := f.count(`SELECT COUNT(*) FROM commits WHERE id = ?`, c1); n != 1 {
		t.Fatalf("c1 rows after the second sweep = %d, want 1", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM events WHERE ref = ?`, "commit:"+c1); n != 1 {
		t.Fatalf("c1 event lost")
	}
}

// I3: hiding a project in Settings is not undone by the next file event, and showing it
// again walks it at once (also when it was walked before).
func TestFixI3HideAndUnhideStick(t *testing.T) {
	f := newIxFixture(t)
	walks := &ixCounter{}
	f.start(func(c *Config) {
		c.Exec.Walk = func(ctx context.Context, root string, skip []string) (tree.Result, error) {
			walks.inc(filepath.Base(root))
			return tree.Walk(ctx, root, skip)
		}
	})
	f.waitFirstSweep()
	ctx := context.Background()
	cfgPath := filepath.Join(f.root, "config.toml")
	hidden := func(id string) int {
		return f.count(`SELECT hidden FROM projects WHERE id = ?`, id)
	}

	// Hide beta the way PATCH /api/settings does: config, then the projects table.
	ixMust(t, f.cfg.SetHidden(cfgPath, []string{"bigvenv", "beta"}))
	_, err := f.db.HomeApplyHidden(ctx, f.cfg.IsHidden)
	ixMust(t, err)
	if hidden("beta") != 1 {
		t.Fatal("beta not hidden by the API step")
	}
	// A file event and a refresh in beta must not write hidden=0 back.
	f.send(WatchEvent{Path: filepath.Join(f.beta, "notes.txt"), Kind: Modify})
	f.clock.Advance(10 * time.Second)
	ixMust(t, f.ix.Refresh(ctx, "beta", false))
	ixEventually(t, "beta refresh done", func() bool { return fcScanDone(f, "beta", 1) })
	if hidden("beta") != 1 {
		t.Fatalf("beta hidden flag written back to 0 by the indexer")
	}

	// Show beta again (it was walked before): the refresh the API starts walks it.
	before := walks.get("beta")
	ixMust(t, f.cfg.SetHidden(cfgPath, []string{"bigvenv"}))
	_, err = f.db.HomeApplyHidden(ctx, f.cfg.IsHidden)
	ixMust(t, err)
	ixMust(t, f.ix.Refresh(ctx, "beta", false))
	ixEventually(t, "beta walked after unhide", func() bool { return walks.get("beta") > before })
	ixEventually(t, "beta refresh 2 done", func() bool { return fcScanDone(f, "beta", 2) })
	if hidden("beta") != 0 {
		t.Fatalf("beta written back as hidden after unhide")
	}

	// Unhide bigvenv (never walked): the refresh walks it and stores its files.
	ixMust(t, f.cfg.SetHidden(cfgPath, []string{}))
	_, err = f.db.HomeApplyHidden(ctx, f.cfg.IsHidden)
	ixMust(t, err)
	ixMust(t, f.ix.Refresh(ctx, "bigvenv", false))
	ixEventually(t, "files of bigvenv", func() bool {
		return f.count(`SELECT COUNT(*) FROM files WHERE project_id='bigvenv'`) > 0
	})
	ixEventually(t, "bigvenv refresh done", func() bool { return fcScanDone(f, "bigvenv", 1) })
	if hidden("bigvenv") != 0 {
		t.Fatalf("bigvenv still hidden in the index")
	}
}

// I4: when the DB write of a git job fails, the next run must try again (memory keeps the
// old cache key), even though the git cache key does not change any more.
func TestFixI4RetryAfterFailedWrite(t *testing.T) {
	f := newIxFixture(t)
	f.start(nil)
	f.waitFirstSweep()
	ctx := context.Background()
	ixMust(t, f.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fc_fail BEFORE INSERT ON commits BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
		return err
	}))
	head := ixCommit(t, f.alpha, "e.go", "package main\n", "after the failure", f.commitDay.Add(6*time.Hour))
	f.send(WatchEvent{Path: filepath.Join(f.alpha, ".git", "HEAD"), Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "commits marked stale", func() bool { return !f.ix.Status("alpha")["commits"].StaleSince.IsZero() })
	ixMust(t, f.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TRIGGER fc_fail`)
		return err
	}))
	// The next event (HEAD unchanged since) must still store the commit.
	f.send(WatchEvent{Path: filepath.Join(f.alpha, ".git", "HEAD"), Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "commit stored after recovery", func() bool {
		return f.count(`SELECT COUNT(*) FROM commits WHERE id = ?`, head) == 1
	})
}

// I5: a panic in the auto-link rules must not kill the process; the sweep finishes.
func TestFixI5AutoLinkPanicIsRecovered(t *testing.T) {
	f := newIxFixture(t)
	var calls atomic.Int32
	f.start(func(c *Config) {
		c.AutoLink = func(in autolink.Input) []autolink.Link {
			calls.Add(1)
			panic("bad task data")
		}
	})
	f.waitFirstSweep()
	if calls.Load() == 0 {
		t.Fatal("AutoLink was never called")
	}
	// The indexer still works after the panic.
	ixMust(t, f.ix.RefreshAll(context.Background()))
	ixEventually(t, "second sweep logged", func() bool { return f.count(`SELECT COUNT(*) FROM sweep_log`) >= 2 })
}

// I5: the long-running loops are restarted after a panic.
func TestFixI5GuardRestartsLoop(t *testing.T) {
	e := newIxEnv(t)
	ix := New(Config{Cfg: e.cfg, DB: e.db, Clock: RealClock(), Logger: discardLog()})
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ix.guard(ctx, "test", func(ctx context.Context) {
			if runs.Add(1) == 1 {
				panic("boom")
			}
			<-ctx.Done()
		})
		close(done)
	}()
	ixEventually(t, "loop restarted", func() bool { return runs.Load() == 2 })
	cancel()
	<-done
}

// I6: in a linked worktree project, a commit only changes <main>/.git/worktrees/<n>/HEAD.
// That path must pass the watcher and reach the worktree project's VCS job.
func TestFixI6WorktreeEventsReachWorktreeProject(t *testing.T) {
	if IsIgnoredWatchPath("g/main/.git/worktrees/wt/HEAD", nil) || IsIgnoredWatchPath("g/main/.git/worktrees/wt/index", nil) {
		t.Fatal("worktree HEAD/index dropped by the watcher filter")
	}
	if !IsIgnoredWatchPath("g/main/.git/worktrees/wt/logs/HEAD", nil) {
		t.Fatal("other worktree files must stay filtered")
	}
	f := newIxFixture(t)
	wt := filepath.Join(f.projDir, "personal", "wt")
	ixGit(t, f.alpha, f.commitDay, "worktree", "add", "-q", "-b", "side", wt)
	f.start(nil)
	f.waitFirstSweep()
	ixEventually(t, "wt known", func() bool { return f.count(`SELECT COUNT(*) FROM projects WHERE id='wt' AND vcs='git'`) == 1 })
	head := ixCommit(t, wt, "w.go", "package main\n", "worktree commit", f.commitDay.Add(7*time.Hour))
	// Only the main repo's worktree folder changes on a worktree commit.
	f.send(WatchEvent{Path: filepath.Join(f.alpha, ".git", "worktrees", "wt", "HEAD"), Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "worktree commit indexed", func() bool {
		return f.count(`SELECT COUNT(*) FROM commits WHERE project_id='wt' AND id = ?`, head) == 1
	})
}

// M2: the same session id in two profiles: the row with more turns wins, on a tie the newer
// file. The newer file is parsed first (newest first), so the old rule let the older one win.
func TestFixM2SameSessionTwoProfilesIsDeterministic(t *testing.T) {
	f := newIxFixture(t)
	prof2 := filepath.Join(f.root, "claude-b")
	ixMust(t, os.MkdirAll(filepath.Join(prof2, "projects"), 0o755))
	f.cfg.ClaudeProfiles = append(f.cfg.ClaudeProfiles, prof2)
	sid := "99999999-2222-3333-4444-555555555555"
	st := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	body := ixUser(f.alpha, sid, "same session", st) + ixAssistant(f.alpha, sid, "Sure.", st.Add(time.Minute))
	older := f.sessionPath("-x-alpha", sid)
	newer := filepath.Join(prof2, "projects", "-x-alpha", sid+".jsonl")
	f.write(older, body)
	f.write(newer, body)
	ixMust(t, os.Chtimes(older, st, st))
	ixMust(t, os.Chtimes(newer, st.Add(time.Hour), st.Add(time.Hour)))
	f.start(nil)
	f.waitFirstSweep()
	s, _, ok, err := f.db.IdxSession(context.Background(), sid)
	if err != nil || !ok {
		t.Fatalf("session missing: %v", err)
	}
	if s.JSONLPath != newer {
		t.Fatalf("winner = %s, want the newer file %s", s.JSONLPath, newer)
	}
	if !sessionBeats(5, st, "/b", 4, st.Add(time.Hour), "/a") || sessionBeats(4, st, "/b", 5, st, "/a") ||
		sessionBeats(5, st, "/b", 5, st, "/a") == sessionBeats(5, st, "/a", 5, st, "/b") {
		t.Fatal("sessionBeats is not a strict order")
	}
}

// M11: a tree job that finishes after its project was removed writes nothing.
func TestFixM11JobAfterRemovalWritesNothing(t *testing.T) {
	f := newIxFixture(t)
	var block atomic.Bool
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	f.start(func(c *Config) {
		c.Exec.Walk = func(ctx context.Context, root string, skip []string) (tree.Result, error) {
			res, err := tree.Walk(ctx, root, skip)
			if block.Load() && filepath.Base(root) == "beta" {
				entered <- struct{}{}
				<-release
			}
			return res, err
		}
	})
	f.waitFirstSweep()
	block.Store(true)
	ixMust(t, f.ix.Refresh(context.Background(), "beta", false))
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("walk of beta did not start")
	}
	ixMust(t, os.RemoveAll(f.beta))
	f.send(WatchEvent{Path: filepath.Join(f.projDir, "personal"), Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "beta removed", func() bool { return !f.ix.known("beta") })
	ixEventually(t, "beta rows deleted", func() bool { return f.count(`SELECT COUNT(*) FROM projects WHERE id='beta'`) == 0 })
	close(release)
	ixEventually(t, "beta job done", func() bool { return fcScanDone(f, "beta", 1) })
	if n := f.count(`SELECT COUNT(*) FROM projects WHERE id = '' OR id = 'beta'`); n != 0 {
		t.Fatalf("project rows written after removal: %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM files WHERE project_id = 'beta' OR project_id = ''`); n != 0 {
		t.Fatalf("orphan file rows: %d", n)
	}
}

// M12: a burst of sweep requests while a sweep runs gives at most one extra sweep.
func TestFixM12SweepBurstCoalesced(t *testing.T) {
	f := newIxFixture(t)
	var block atomic.Bool
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	f.start(func(c *Config) {
		c.Exec.Discover = func(cfg *config.Config) ([]probe.Project, error) {
			if block.Load() {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
			}
			return projects.Discover(cfg)
		}
	})
	f.waitFirstSweep()
	block.Store(true)
	ixMust(t, f.ix.RefreshAll(context.Background()))
	<-entered
	for n := 0; n < 10; n++ {
		ixMust(t, f.ix.RefreshAll(context.Background()))
	}
	block.Store(false)
	close(release)
	ixEventually(t, "sweeps done", func() bool { return f.count(`SELECT COUNT(*) FROM sweep_log`) >= 3 })
	time.Sleep(500 * time.Millisecond)
	ixEventually(t, "no sweep running", func() bool { return !f.ix.sweeping.Load() })
	if n := f.count(`SELECT COUNT(*) FROM sweep_log`); n != 3 {
		t.Fatalf("sweeps = %d, want 3 (startup, the blocked one, one merged)", n)
	}
}

// M13: a session parsed before its project existed is given that project once it appears.
func TestFixM13SessionResolvedWhenProjectAppears(t *testing.T) {
	f := newIxFixture(t)
	gamma := filepath.Join(f.projDir, "personal", "gamma")
	sid := "77777777-2222-3333-4444-555555555555"
	st := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	f.write(f.sessionPath("-x-gamma", sid), ixUser(gamma, sid, "early session", st)+ixAssistant(gamma, sid, "ok", st.Add(time.Minute)))
	f.start(nil)
	f.waitFirstSweep()
	if n := f.count(`SELECT COUNT(*) FROM sessions WHERE id = ? AND project_id = ''`, sid); n != 1 {
		t.Fatalf("session should be unassigned before gamma exists")
	}
	f.project("personal", "gamma")
	f.send(WatchEvent{Path: filepath.Join(f.projDir, "personal"), Kind: Modify})
	f.clock.Advance(time.Second)
	ixEventually(t, "session moved to gamma", func() bool {
		return f.count(`SELECT COUNT(*) FROM sessions WHERE id = ? AND project_id = 'gamma'`, sid) == 1
	})
	ixEventually(t, "gamma day row", func() bool {
		return f.count(`SELECT COUNT(*) FROM daily_activity WHERE project_id = 'gamma' AND sessions = 1`) == 1
	})
}

// I1: day loops in zones whose clocks change at midnight.
func TestFixI1IndexerDayMathSantiago(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skip("no tz data")
	}
	i := &Indexer{loc: loc}
	a := time.Date(2026, 9, 5, 10, 0, 0, 0, loc)
	b := time.Date(2026, 9, 7, 10, 0, 0, 0, loc)
	got := i.daysBetween(a, b, 62)
	if strings.Join(got, ",") != "2026-09-05,2026-09-06,2026-09-07" {
		t.Fatalf("daysBetween = %v", got)
	}
	now := time.Date(2026, 9, 5, 23, 30, 0, 0, loc)
	act := NewActivity(nil, loc, func() time.Time { return now })
	d := act.untilMidnight()
	// Sep 6 starts at 01:00 (00:00 is skipped): 30 minutes, plus the slack.
	if d < 29*time.Minute || d > 32*time.Minute {
		t.Fatalf("untilMidnight = %v, want about 30m", d)
	}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
