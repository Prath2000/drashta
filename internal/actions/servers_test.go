package actions

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe/servers"
)

type fakeMgr struct {
	m        *Manager
	killed   []int
	launched []servers.Server
	hist     map[string]servers.Server
	now      time.Time
}

func newFakeMgr(t *testing.T) *fakeMgr {
	f := &fakeMgr{hist: map[string]servers.Server{}, now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	f.m = NewManager(filepath.Join(t.TempDir(), "logs"), func(k string) (servers.Server, bool) {
		s, ok := f.hist[k]
		return s, ok
	})
	f.m.kill = func(pid int) error { f.killed = append(f.killed, pid); return nil } // never signal in tests
	f.m.launch = func(s servers.Server, _ string) error { f.launched = append(f.launched, s); return nil }
	f.m.now = func() time.Time { return f.now }
	return f
}

var (
	alive = servers.Server{Port: 5173, PID: 4242, Name: "node", Cwd: "/p/a", Cmd: "node a", ProjectID: "a", Kind: servers.KindDev, Alive: true}
	dead  = servers.Server{Port: 3000, PID: 4343, Name: "node", Cwd: "/p/b", Cmd: "node b", ProjectID: "b", Kind: servers.KindDev}
)

func TestKillRequiresConfirm(t *testing.T) {
	f := newFakeMgr(t)
	f.m.SetLive([]servers.Server{alive}, f.now)
	if err := f.m.Kill(alive.PID, "", false); !errors.Is(err, ErrConfirm) {
		t.Fatalf("err = %v", err)
	}
	if len(f.killed) != 0 {
		t.Fatal("killed without confirm")
	}
}

func TestKillOnlyLivePID(t *testing.T) {
	f := newFakeMgr(t)
	f.m.SetLive([]servers.Server{alive}, f.now)
	if err := f.m.Kill(999999, "", true); !errors.Is(err, ErrNotLive) {
		t.Fatalf("unknown pid: %v", err)
	}
	if err := f.m.Kill(alive.PID, servers.Key(dead), true); !errors.Is(err, ErrNotLive) {
		t.Fatalf("key mismatch: %v", err)
	}
	if len(f.killed) != 0 {
		t.Fatal("killed a non-live pid")
	}
	if err := f.m.Kill(alive.PID, servers.Key(alive), true); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Kill(alive.PID, servers.RefKey(servers.Key(alive)), true); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Kill(alive.PID, "", true); err != nil {
		t.Fatal(err)
	}
	if len(f.killed) != 3 || f.killed[0] != alive.PID {
		t.Fatalf("killed = %v", f.killed)
	}
	// After the next scan the pid is gone: kill is refused again.
	f.m.SetLive(nil, f.now)
	if err := f.m.Kill(alive.PID, "", true); !errors.Is(err, ErrNotLive) {
		t.Fatalf("stale pid: %v", err)
	}
}

func TestKillPIDRefusesLowPID(t *testing.T) {
	for _, pid := range []int{-1, 0, 1} {
		if err := killPID(pid); err == nil {
			t.Fatalf("killPID(%d) must refuse", pid)
		}
	}
}

func TestRestartRequiresConfirm(t *testing.T) {
	f := newFakeMgr(t)
	f.hist[servers.Key(dead)] = dead
	if _, err := f.m.Restart(servers.Key(dead), false); !errors.Is(err, ErrConfirm) {
		t.Fatalf("err = %v", err)
	}
	if len(f.launched) != 0 {
		t.Fatal("launched without confirm")
	}
}

func TestRestartUnknownKeyNotFound(t *testing.T) {
	f := newFakeMgr(t)
	if _, err := f.m.Restart("/nope|nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestRestartAliveKeyConflict(t *testing.T) {
	f := newFakeMgr(t)
	f.hist[servers.Key(alive)] = alive
	if _, err := f.m.Restart(servers.Key(alive), true); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	// Also a conflict when history says dead but the live set has the key.
	d := alive
	d.Alive = false
	f.hist[servers.Key(alive)] = d
	f.m.SetLive([]servers.Server{alive}, f.now)
	if _, err := f.m.Restart(servers.Key(alive), true); !errors.Is(err, ErrConflict) {
		t.Fatalf("live-set conflict: %v", err)
	}
	if len(f.launched) != 0 {
		t.Fatal("launched an alive server")
	}
}

func TestRestartTwiceConflicts(t *testing.T) {
	f := newFakeMgr(t)
	k := servers.Key(dead)
	f.hist[k] = dead
	log, err := f.m.Restart(k, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(log, filepath.Join("logs", "3000.log")) {
		t.Fatalf("log path = %q", log)
	}
	if _, err := f.m.Restart(k, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("second restart: %v", err)
	}
	if !f.m.Pending(k) || len(f.launched) != 1 {
		t.Fatalf("pending=%v launched=%d", f.m.Pending(k), len(f.launched))
	}
}

func TestRestartLaunchFailureClearsPending(t *testing.T) {
	f := newFakeMgr(t)
	k := servers.Key(dead)
	f.hist[k] = dead
	f.m.launch = func(servers.Server, string) error { return errors.New("no") }
	if _, err := f.m.Restart(k, true); err == nil {
		t.Fatal("want error")
	}
	if f.m.Pending(k) {
		t.Fatal("failed launch left key pending")
	}
}

func TestRestartingClearedWhenAliveOrExpired(t *testing.T) {
	f := newFakeMgr(t)
	now := f.now
	a := servers.Server{Port: 1, PID: 1, Cwd: "/a", Cmd: "node a"}
	f.m.restarting = map[string]time.Time{
		servers.Key(a): now,                                  // now live: cleared
		"/b|node b":    now.Add(-RestartGrace - time.Second), // expired: cleared
		"/c|node c":    now.Add(-time.Second),                // in grace: kept
	}
	f.m.SetLive([]servers.Server{a}, now)
	if len(f.m.restarting) != 1 || f.m.restarting["/c|node c"].IsZero() {
		t.Fatalf("restarting = %v", f.m.restarting)
	}
	// Grace ran out: a new restart is allowed.
	c := servers.Server{Port: 9, PID: 9, Cwd: "/c", Cmd: "node c", ProjectID: "c", Kind: servers.KindDev}
	f.hist[servers.Key(c)] = c
	if _, err := f.m.Restart(servers.Key(c), true); !errors.Is(err, ErrConflict) {
		t.Fatalf("in grace: %v", err)
	}
	f.m.SetLive(nil, now.Add(RestartGrace+time.Second))
	if _, err := f.m.Restart(servers.Key(c), true); err != nil {
		t.Fatalf("after grace: %v", err)
	}
}

// restart() with a harmless command: it writes to <logDir>/<port>.log.
func TestRestartWritesLog(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	s := servers.Server{Port: 6553, Cwd: dir, Cmd: "echo restarted-ok"}
	if err := restart(s, logDir); err != nil {
		t.Fatal(err)
	}
	path := LogPath(logDir, 6553)
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), "restarted-ok") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log = %q", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := restart(servers.Server{Port: 1, Cwd: dir}, logDir); err == nil {
		t.Fatal("empty cmd must fail")
	}
}

func TestPageTitle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><head><TITLE> Shop dev </TITLE></head></html>")
	}))
	defer ts.Close()
	_, portStr, _ := net.SplitHostPort(ts.Listener.Addr().String())
	var port int
	fmt.Sscan(portStr, &port)
	if got := PageTitle(port); got != "Shop dev" {
		t.Fatalf("title = %q", got)
	}
	if PageTitle(0) != "" || PageTitle(70000) != "" {
		t.Fatal("bad port must give empty title")
	}
}

// TestRestartRefusesUnsafeCommands (review M-3): ps text is replayed through
// /bin/sh -c, so any shell metacharacter, a helper or a server outside every
// project is refused before anything is launched.
func TestRestartRefusesUnsafeCommands(t *testing.T) {
	base := servers.Server{Port: 3001, PID: 5000, Name: "node", Cwd: "/p/b", ProjectID: "b", Kind: servers.KindDev}
	for _, ch := range []string{";", "|", "&", "<", ">", "$", "`", "\\", "\"", "'", "(", ")", "{", "}", "*", "?", "~", "!", "\n"} {
		f := newFakeMgr(t)
		s := base
		s.Cmd = "node server.js a" + ch + "touch /tmp/x"
		f.hist[servers.Key(s)] = s
		if _, err := f.m.Restart(servers.Key(s), true); !errors.Is(err, ErrForbidden) {
			t.Errorf("cmd with %q: err = %v, want ErrForbidden", ch, err)
		}
		if len(f.launched) != 0 || f.m.Pending(servers.Key(s)) {
			t.Errorf("cmd with %q was launched", ch)
		}
	}
	refuse := map[string]servers.Server{
		"helper":          {Port: 3002, Cwd: "/p/b", Cmd: "node x", ProjectID: "b", Kind: servers.KindHelper},
		"no kind":         {Port: 3003, Cwd: "/p/b", Cmd: "node x", ProjectID: "b"},
		"outside project": {Port: 3004, Cwd: "/tmp", Cmd: "node x", Kind: servers.KindDev},
		"helper outside":  {Port: 3005, Cwd: "/Applications", Cmd: "SomeApp --serve", Kind: servers.KindHelper},
	}
	for name, s := range refuse {
		f := newFakeMgr(t)
		f.hist[servers.Key(s)] = s
		if _, err := f.m.Restart(servers.Key(s), true); !errors.Is(err, ErrForbidden) || len(f.launched) != 0 {
			t.Errorf("%s: err = %v launched=%d", name, err, len(f.launched))
		}
	}
	for _, cmd := range []string{"node design/tools/serve.mjs", "fossil ui --nobrowser -P 8090", "npm run dev -- --port=5173"} {
		f := newFakeMgr(t)
		s := base
		s.Cmd = cmd
		f.hist[servers.Key(s)] = s
		if _, err := f.m.Restart(servers.Key(s), true); err != nil || len(f.launched) != 1 {
			t.Errorf("%q: err = %v launched=%d, want allowed", cmd, err, len(f.launched))
		}
	}
}
