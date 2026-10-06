package servers

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/probe"
)

// Kind values. A "dev" server looks like a dev runtime (node, python, go, ...).
// Everything else is a "helper" (system or app daemons such as ControlCenter).
const (
	KindDev    = "dev"
	KindHelper = "helper"
)

// Server is a locally listening TCP process.
type Server struct {
	Port      int       `json:"port"`
	PID       int       `json:"pid"`
	Name      string    `json:"name"` // lsof COMMAND column
	Cmd       string    `json:"cmd"`  // full argv from ps
	Cwd       string    `json:"cwd"`
	ProjectID string    `json:"projectId"` // "" when cwd is outside every project ("other")
	Kind      string    `json:"kind"`      // KindDev | KindHelper
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Alive     bool      `json:"alive"`
}

var devNames = map[string]bool{
	"node": true, "bun": true, "deno": true, "ruby": true, "go": true,
	"java": true, "php": true, "vite": true, "next-server": true,
	"uvicorn": true, "gunicorn": true, "flask": true, "cargo": true,
	"rails": true, "dotnet": true, "ollama": true,
}

// isDev reports whether a process name looks like a dev server runtime.
func isDev(name string) bool {
	n := strings.ToLower(name)
	return devNames[n] || strings.HasPrefix(n, "python")
}

// KindOf returns KindDev or KindHelper for an lsof COMMAND name.
func KindOf(name string) string {
	if isDev(name) {
		return KindDev
	}
	return KindHelper
}

// parseLsof parses `lsof -iTCP -sTCP:LISTEN -P -n` output, deduping on (pid, port).
func parseLsof(out string) []Server {
	type key struct{ pid, port int }
	seen := map[key]bool{}
	var servers []Server
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 9 {
			continue
		}
		pid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		addr := f[8] // e.g. *:7000, 127.0.0.1:11434, [::1]:8080
		idx := strings.LastIndex(addr, ":")
		if idx < 0 {
			continue
		}
		port, err := strconv.Atoi(addr[idx+1:])
		if err != nil {
			continue
		}
		k := key{pid, port}
		if seen[k] {
			continue
		}
		seen[k] = true
		servers = append(servers, Server{Port: port, PID: pid, Name: f[0]})
	}
	return servers
}

// parseCwd reads the cwd from `lsof -a -d cwd -p <pid> -Fn` output (the first "n" line).
func parseCwd(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}

type procInfo struct{ cmd, cwd string }

// enrichAll fills Cmd/Cwd from cache or via enrich, and returns the next cache:
// only PIDs present in servers are kept (so a reused PID never inherits a dead
// process's cmd/cwd), and failed enrichments (both empty) are not cached.
func enrichAll(servers []Server, cache map[int]procInfo, enrich func(*Server)) ([]Server, map[int]procInfo) {
	next := make(map[int]procInfo, len(servers))
	for i := range servers {
		s := &servers[i]
		info, ok := next[s.PID]
		if !ok {
			info, ok = cache[s.PID]
		}
		if !ok {
			enrich(s)
			info = procInfo{s.Cmd, s.Cwd}
		}
		s.Cmd, s.Cwd = info.cmd, info.cwd
		if info.cmd != "" || info.cwd != "" {
			next[s.PID] = info
		}
	}
	return servers, next
}

// lsofOutput interprets the result of the listen-socket lsof call. lsof exits 1
// with no output when nothing is listening; that is zero servers, not a failure.
func lsofOutput(out []byte, err error) (string, error) {
	if err == nil {
		return string(out), nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 && len(bytes.TrimSpace(out)) == 0 {
		return "", nil
	}
	return "", fmt.Errorf("lsof: %w", err)
}

// lsofExit is lsofOutput for a runx result: exit 1 with empty stdout means
// "nothing listening"; any other non-zero exit, or a run error, is a failure.
func lsofExit(out []byte, exitCode int, err error) (string, error) {
	if err != nil {
		return "", fmt.Errorf("lsof: %w", err)
	}
	switch {
	case exitCode == 0:
		return string(out), nil
	case exitCode == 1 && len(bytes.TrimSpace(out)) == 0:
		return "", nil
	default:
		return "", fmt.Errorf("lsof: exit status %d", exitCode)
	}
}

// MatchProject returns the ID of the project whose path is the longest prefix
// of cwd, or "" when none matches. It is a pure string check on cleaned paths.
func MatchProject(cwd string, projects []probe.Project) string {
	if cwd == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	best, bestLen := "", -1
	for _, p := range projects {
		if p.Path == "" {
			continue
		}
		root := filepath.Clean(p.Path)
		if pathguard.Inside(root, cwd) && len(root) > bestLen {
			best, bestLen = p.ID, len(root)
		}
	}
	return best
}
