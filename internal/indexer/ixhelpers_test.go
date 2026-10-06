package indexer

// Test helpers of the scheduler/jobs tests (prefix "ix" so they never clash with the
// watcher and activity tests, which use "wa").

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/store"
)

// ixClock is a fake Clock. Time moves only with Advance.
type ixClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []ixWaiter
}

type ixWaiter struct {
	at time.Time
	ch chan time.Time
}

func newIxClock(t0 time.Time) *ixClock { return &ixClock{now: t0} }

func (c *ixClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ixClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, ixWaiter{at: c.now.Add(d), ch: ch})
	return ch
}

func (c *ixClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	keep := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			keep = append(keep, w)
		}
	}
	c.waiters = keep
}

// hasWaiterAt reports whether someone waits for exactly t.
func (c *ixClock) hasWaiterAt(t time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.waiters {
		if w.at.Equal(t) {
			return true
		}
	}
	return false
}

func ixEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// ixEnv is a temp HOME with projects, a Claude profile, a store and a hub.
type ixEnv struct {
	t        *testing.T
	root     string
	projDir  string
	profile  string
	cfg      *config.Config
	db       *store.DB
	hub      *live.Hub
	clock    *ixClock
	watcher  *FakeWatcher
	ix       *Indexer
	cancel   context.CancelFunc
	loc      *time.Location
	t0       time.Time
	hubStart uint64
}

func newIxEnv(t *testing.T) *ixEnv {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	e := &ixEnv{t: t, root: root, projDir: filepath.Join(root, "projects"), profile: filepath.Join(root, "claude-a")}
	for _, g := range []string{"personal", "tools"} {
		if err := os.MkdirAll(filepath.Join(e.projDir, g), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(e.profile, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.cfg = &config.Config{
		ProjectsDir:    e.projDir,
		Roots:          []string{"personal", "tools"},
		Hidden:         []string{"bigvenv"},
		Skip:           []string{},
		ClaudeProfiles: []string{e.profile},
	}
	db, err := store.Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e.db = db
	e.hubStart = 1
	e.hub = live.NewHub(live.HubOptions{StartSeq: e.hubStart, RingSize: 100000})
	e.loc = time.FixedZone("IST", 5*3600+1800)
	e.t0 = time.Date(2026, 9, 25, 10, 0, 0, 0, e.loc)
	e.clock = newIxClock(e.t0)
	e.watcher = NewFakeWatcher()
	return e
}

func (e *ixEnv) project(group, name string) string {
	p := filepath.Join(e.projDir, group, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *ixEnv) write(path, text string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// start makes and starts the indexer. mod can change the config first.
func (e *ixEnv) start(mod func(c *Config)) *Indexer {
	e.t.Helper()
	c := Config{
		Cfg: e.cfg, DB: e.db, Hub: e.hub, Watcher: e.watcher, Clock: e.clock, Loc: e.loc,
		NoServers: true,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	c.Exec.PageTitle = func(port int) string { return "" } // never touch the network
	if mod != nil {
		mod(&c)
	}
	e.ix = New(c)
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	if err := e.ix.Start(ctx); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { cancel(); e.ix.Wait() })
	return e.ix
}

func (e *ixEnv) waitFirstSweep() {
	e.t.Helper()
	select {
	case <-e.ix.FirstSweepDone():
	case <-time.After(30 * time.Second):
		e.t.Fatal("first sweep did not finish")
	}
}

// send pushes watch events and waits until the scheduler handled them.
func (e *ixEnv) send(evs ...WatchEvent) {
	e.t.Helper()
	before := e.ix.evCount.Load()
	for _, ev := range evs {
		if ev.Time.IsZero() {
			ev.Time = e.clock.Now()
		}
		e.watcher.Send(ev)
	}
	ixEventually(e.t, "scheduler handled the events", func() bool {
		return e.ix.evCount.Load() >= before+int64(len(evs))
	})
}

// events returns the published events of one type.
func (e *ixEnv) events(typ string) []live.Event {
	evs, _, _ := e.hub.Since(e.hubStart)
	var out []live.Event
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func (e *ixEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Read().QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *ixEnv) strings(q string, args ...any) []string {
	e.t.Helper()
	rows, err := e.db.Read().Query(q, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s.String)
	}
	sort.Strings(out)
	return out
}

// ---- git fixture ----

func ixNeedGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func ixGit(t *testing.T, dir string, when time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Tester", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	d := when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// ixCommit writes a file and commits it.
func ixCommit(t *testing.T, dir, file, text, msg string, when time.Time) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	ixGit(t, dir, when, "add", "-A")
	ixGit(t, dir, when, "commit", "-q", "-m", msg)
	return ixGit(t, dir, when, "rev-parse", "HEAD")
}

// ---- claude fixture ----

type ixRec map[string]any

func ixLine(r ixRec) string {
	b, _ := json.Marshal(r)
	return string(b) + "\n"
}

func ixUser(cwd, sid, text string, ts time.Time) string {
	return ixLine(ixRec{"type": "user", "cwd": cwd, "sessionId": sid, "timestamp": ts.UTC().Format(time.RFC3339Nano),
		"message": ixRec{"role": "user", "content": text}})
}

func ixAssistant(cwd, sid, text string, ts time.Time) string {
	return ixLine(ixRec{"type": "assistant", "cwd": cwd, "sessionId": sid, "timestamp": ts.UTC().Format(time.RFC3339Nano),
		"message": ixRec{"role": "assistant", "content": []ixRec{{"type": "text", "text": text}}}})
}

// sessionPath is where a session file of the fixture profile lives.
func (e *ixEnv) sessionPath(folder, sid string) string {
	return filepath.Join(e.profile, "projects", folder, sid+".jsonl")
}

func ixTurns(t *testing.T, db *store.DB, sid string) int {
	t.Helper()
	s, _, ok, err := db.IdxSession(context.Background(), sid)
	if err != nil || !ok {
		return -1
	}
	return s.Turns
}

func ixMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var _ = fmt.Sprint
