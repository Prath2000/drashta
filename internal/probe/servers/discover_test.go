package servers

import (
	"errors"
	"os/exec"
	"testing"
)

const lsofFixture = `COMMAND     PID  USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME
ControlCe   612 alice    8u  IPv4 0x1a2b3c4d5e6f7081      0t0  TCP *:7000 (LISTEN)
ControlCe   612 alice    9u  IPv6 0x1a2b3c4d5e6f7082      0t0  TCP *:7000 (LISTEN)
ollama      904 alice    3u  IPv4 0x1a2b3c4d5e6f7083      0t0  TCP 127.0.0.1:11434 (LISTEN)
node      51234 alice   23u  IPv6 0x1a2b3c4d5e6f7084      0t0  TCP [::1]:5173 (LISTEN)
node      51234 alice   24u  IPv4 0x1a2b3c4d5e6f7085      0t0  TCP 127.0.0.1:3000 (LISTEN)
node      51234 alice   25u  IPv6 0x1a2b3c4d5e6f7086      0t0  TCP [::1]:3000 (LISTEN)
`

func TestParseLsof(t *testing.T) {
	got := parseLsof(lsofFixture)
	want := []struct {
		port, pid int
		name      string
	}{
		{7000, 612, "ControlCe"},
		{11434, 904, "ollama"},
		{5173, 51234, "node"},
		{3000, 51234, "node"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d servers, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Port != w.port || got[i].PID != w.pid || got[i].Name != w.name {
			t.Errorf("row %d = {%d %d %q}, want {%d %d %q}", i, got[i].Port, got[i].PID, got[i].Name, w.port, w.pid, w.name)
		}
	}
}

func TestParseLsofEmpty(t *testing.T) {
	if got := parseLsof(""); len(got) != 0 {
		t.Fatalf("got %d servers, want 0", len(got))
	}
}

func TestIsDev(t *testing.T) {
	cases := map[string]bool{
		"node":       true,
		"Python":     true,
		"python3.12": true,
		"ollama":     true,
		"ControlCe":  false,
		"rapportd":   false,
	}
	for name, want := range cases {
		if got := isDev(name); got != want {
			t.Errorf("isDev(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestEnrichAllEvictsAndSkipsFailures(t *testing.T) {
	cache := map[int]procInfo{
		1: {"old cmd", "/old"}, // PID no longer listening: must be evicted
		2: {"cached cmd", "/two"},
	}
	calls := map[int]int{}
	fake := func(s *Server) {
		calls[s.PID]++
		if s.PID == 3 {
			s.Cmd, s.Cwd = "new cmd", "/three"
		} // PID 4 fails: both empty
	}
	in := []Server{{Port: 20, PID: 2}, {Port: 30, PID: 3}, {Port: 40, PID: 4}}
	out, next := enrichAll(in, cache, fake)
	if calls[2] != 0 || calls[3] != 1 || calls[4] != 1 {
		t.Fatalf("enrich calls = %v", calls)
	}
	if out[0].Cmd != "cached cmd" || out[1].Cwd != "/three" || out[2].Cmd != "" {
		t.Fatalf("out = %+v", out)
	}
	if _, ok := next[1]; ok {
		t.Fatal("stale pid 1 retained")
	}
	if _, ok := next[4]; ok {
		t.Fatal("failed enrichment cached")
	}
	if next[2].cmd != "cached cmd" || next[3].cwd != "/three" || len(next) != 2 {
		t.Fatalf("next = %+v", next)
	}
}

func TestLsofOutput(t *testing.T) {
	exit1 := exec.Command("sh", "-c", "exit 1").Run()
	exit2 := exec.Command("sh", "-c", "exit 2").Run()
	if out, err := lsofOutput(nil, exit1); err != nil || out != "" {
		t.Fatalf("exit 1, empty stdout: out=%q err=%v", out, err)
	}
	if _, err := lsofOutput([]byte("partial"), exit1); err == nil {
		t.Fatal("exit 1 with stdout should be an error")
	}
	if _, err := lsofOutput(nil, exit2); err == nil {
		t.Fatal("exit 2 should be an error")
	}
	if _, err := lsofOutput(nil, errors.New("not found")); err == nil {
		t.Fatal("exec failure should be an error")
	}
	if out, err := lsofOutput([]byte("x"), nil); err != nil || out != "x" {
		t.Fatalf("success: out=%q err=%v", out, err)
	}
}
