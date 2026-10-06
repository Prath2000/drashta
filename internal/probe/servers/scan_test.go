package servers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "lsof", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fakeScanner(listen string, listenErr error, info map[int]procInfo, calls map[int]int) *Scanner {
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	return &Scanner{
		Listen: func(context.Context) (string, error) { return listen, listenErr },
		Enrich: func(_ context.Context, s *Server) {
			calls[s.PID]++
			if i, ok := info[s.PID]; ok {
				s.Cmd, s.Cwd = i.cmd, i.cwd
			}
		},
		Self: 70000,
		Now:  func() time.Time { return at },
	}
}

func TestScanFixture(t *testing.T) {
	projects := []probe.Project{
		{ID: "personal/shop", Path: "/Users/alice/projects/personal/shop"},
		{ID: "personal/shop-web", Path: "/Users/alice/projects/personal/shop/web"},
		{ID: "personal/sh", Path: "/Users/alice/projects/personal/sh"},
	}
	info := map[int]procInfo{
		51234: {strings.TrimSpace(fixture(t, "ps.txt")), parseCwd(fixture(t, "cwd.txt"))},
		61001: {"python3 -m http.server 8090", "/Users/alice/projects/personal/shop/api"},
		904:   {"ollama serve", "/"},
	}
	calls := map[int]int{}
	sc := fakeScanner(fixture(t, "listen.txt"), nil, info, calls)
	snap, err := sc.Scan(context.Background(), projects)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.At.Equal(sc.Now()) {
		t.Fatalf("At = %v", snap.At)
	}
	got := map[int]Server{}
	for _, s := range snap.Servers {
		got[s.Port] = s
		if !s.Alive || !s.LastSeen.Equal(snap.At) {
			t.Errorf("port %d not marked alive now: %+v", s.Port, s)
		}
	}
	if _, ok := got[4455]; ok {
		t.Fatal("own PID must be excluded")
	}
	if len(got) != 5 {
		t.Fatalf("servers = %+v", snap.Servers)
	}
	want := map[int]struct{ project, kind string }{
		5173:  {"personal/shop-web", KindDev}, // longest prefix wins
		3000:  {"personal/shop-web", KindDev},
		8090:  {"personal/shop", KindDev}, // python3.1 is dev; /shop/api -> shop, not "sh"
		11434: {"", KindDev},              // cwd "/" -> other
		7000:  {"", KindHelper},           // no cwd at all
	}
	for port, w := range want {
		s := got[port]
		if s.ProjectID != w.project || s.Kind != w.kind {
			t.Errorf("port %d = project %q kind %q, want %q %q", port, s.ProjectID, s.Kind, w.project, w.kind)
		}
	}
	if got[5173].Cmd == "" || got[5173].Cwd != "/Users/alice/projects/personal/shop/web" {
		t.Fatalf("enrich not applied: %+v", got[5173])
	}
	// Second pass: cached PIDs are not enriched again; failed ones (612) are retried.
	if _, err := sc.Scan(context.Background(), projects); err != nil {
		t.Fatal(err)
	}
	if calls[51234] != 1 || calls[61001] != 1 || calls[612] != 2 {
		t.Fatalf("enrich calls = %v", calls)
	}
}

func TestScanFailureReturnsError(t *testing.T) {
	sc := fakeScanner("", errors.New("lsof: exit status 2"), nil, map[int]int{})
	snap, err := sc.Scan(context.Background(), nil)
	if err == nil {
		t.Fatal("want error")
	}
	if len(snap.Servers) != 0 || !snap.At.IsZero() {
		t.Fatalf("failed scan must return an empty snapshot, got %+v", snap)
	}
}

// A failed pass must not mark anything dead: the caller keeps the old history.
func TestScanFailureKeepsHistoryAlive(t *testing.T) {
	h := LoadHistory(&MemStore{})
	ok := fakeScanner(fixture(t, "listen.txt"), nil, nil, map[int]int{})
	snap, err := ok.Scan(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Merge(snap.Servers, snap.At)
	bad := fakeScanner("", errors.New("boom"), nil, map[int]int{})
	if _, err := bad.Scan(context.Background(), nil); err == nil {
		t.Fatal("want error")
	} // caller does not Merge on error
	active, _ := h.Snapshot()
	if len(active) == 0 {
		t.Fatal("servers lost after failed scan")
	}
}

func TestScanEmptyAndGarbage(t *testing.T) {
	sc := fakeScanner(fixture(t, "empty.txt"), nil, nil, map[int]int{})
	snap, err := sc.Scan(context.Background(), nil)
	if err != nil || len(snap.Servers) != 0 {
		t.Fatalf("empty: %+v %v", snap, err)
	}
	got := parseLsof(fixture(t, "garbage.txt"))
	if len(got) != 1 || got[0].PID != 102 || got[0].Port != 9100 {
		t.Fatalf("garbage parse = %+v", got)
	}
}

func TestLsofExit(t *testing.T) {
	if out, err := lsofExit(nil, 1, nil); err != nil || out != "" {
		t.Fatalf("exit 1 empty: %q %v", out, err)
	}
	if _, err := lsofExit([]byte("x"), 1, nil); err == nil {
		t.Fatal("exit 1 with output must fail")
	}
	if _, err := lsofExit(nil, 2, nil); err == nil {
		t.Fatal("exit 2 must fail")
	}
	if _, err := lsofExit(nil, -1, errors.New("not found")); err == nil {
		t.Fatal("run error must fail")
	}
	if out, err := lsofExit([]byte("ok"), 0, nil); err != nil || out != "ok" {
		t.Fatalf("success: %q %v", out, err)
	}
}

func TestMatchProject(t *testing.T) {
	ps := []probe.Project{{ID: "a", Path: "/p/a"}, {ID: "ab", Path: "/p/a/b"}, {ID: "none", Path: ""}}
	cases := map[string]string{
		"/p/a":       "a",
		"/p/a/x":     "a",
		"/p/a/b/c":   "ab",
		"/p/ab":      "",
		"/q":         "",
		"":           "",
		"/p/a/b/../": "a",
	}
	for cwd, want := range cases {
		if got := MatchProject(cwd, ps); got != want {
			t.Errorf("MatchProject(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestScanResolvesSymlinkedProject(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	realResolved, _ := filepath.EvalSymlinks(real)
	listen := "COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\nnode 5 u 1u IPv4 0x1 0t0 TCP 127.0.0.1:5000 (LISTEN)\n"
	sc := fakeScanner(listen, nil, map[int]procInfo{5: {"node x", realResolved}}, map[int]int{})
	snap, err := sc.Scan(context.Background(), []probe.Project{{ID: "g/p", Path: link}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Servers[0].ProjectID != "g/p" {
		t.Fatalf("symlinked project not matched: %+v", snap.Servers[0])
	}
}

func TestParseCwd(t *testing.T) {
	if got := parseCwd(fixture(t, "cwd.txt")); got != "/Users/alice/projects/personal/shop/web" {
		t.Fatalf("parseCwd = %q", got)
	}
	if parseCwd("p1\nfcwd\n") != "" {
		t.Fatal("no n line should give empty")
	}
}
