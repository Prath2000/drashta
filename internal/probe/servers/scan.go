package servers

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/runx"
)

const (
	scanTimeout   = 5 * time.Second
	enrichTimeout = 2 * time.Second
)

// Snapshot is the result of one lsof pass: every live listening server.
type Snapshot struct {
	At      time.Time `json:"at"`
	Servers []Server  `json:"servers"`
}

// Scanner runs lsof passes. It keeps the PID cache between passes, so ps and
// the cwd lookup run only for new PIDs. The zero value is not ready: use
// NewScanner. The function fields can be swapped in tests.
type Scanner struct {
	// Listen returns the raw `lsof -iTCP -sTCP:LISTEN -P -n` output.
	// An error means the pass failed (the caller keeps its old snapshot).
	Listen func(ctx context.Context) (string, error)
	// Enrich fills Cmd and Cwd of one server, best effort.
	Enrich func(ctx context.Context, s *Server)
	// Self is a PID to leave out (drashta itself).
	Self int
	// Now returns the scan time.
	Now func() time.Time

	mu    sync.Mutex
	cache map[int]procInfo
}

// NewScanner returns a Scanner that runs the real lsof and ps through runx.
func NewScanner() *Scanner {
	return &Scanner{
		Listen: listenLsof,
		Enrich: enrichRunx,
		Self:   os.Getpid(),
		Now:    time.Now,
		cache:  map[int]procInfo{},
	}
}

var defaultScanner = NewScanner()

// Scan runs one pass with the shared default Scanner.
func Scan(ctx context.Context, projects []probe.Project) (Snapshot, error) {
	return defaultScanner.Scan(ctx, projects)
}

// Scan lists the listening servers, leaves out Self, fills Cmd/Cwd (cached by
// PID), Kind and ProjectID. If lsof fails it returns the error and an empty
// Snapshot; the caller must keep its previous snapshot and must NOT mark
// servers dead (ledger scanOnce rule).
func (sc *Scanner) Scan(ctx context.Context, projects []probe.Project) (Snapshot, error) {
	out, err := sc.Listen(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	var parsed []Server
	for _, s := range parseLsof(out) {
		if s.PID != sc.Self {
			parsed = append(parsed, s)
		}
	}
	sc.mu.Lock()
	if sc.cache == nil {
		sc.cache = map[int]procInfo{}
	}
	servers, next := enrichAll(parsed, sc.cache, func(s *Server) { sc.Enrich(ctx, s) })
	sc.cache = next
	sc.mu.Unlock()

	match := withResolvedPaths(projects)
	now := sc.Now()
	for i := range servers {
		s := &servers[i]
		s.Kind = KindOf(s.Name)
		s.ProjectID = MatchProject(s.Cwd, match)
		s.Alive = true
		s.LastSeen = now
	}
	return Snapshot{At: now, Servers: servers}, nil
}

// withResolvedPaths adds a symlink-resolved copy of each project path, because
// lsof reports the real cwd while Project.Path is not resolved.
func withResolvedPaths(projects []probe.Project) []probe.Project {
	out := make([]probe.Project, 0, 2*len(projects))
	for _, p := range projects {
		out = append(out, p)
		if real, err := filepath.EvalSymlinks(p.Path); err == nil && real != filepath.Clean(p.Path) {
			q := p
			q.Path = real
			out = append(out, q)
		}
	}
	return out
}

func listenLsof(ctx context.Context) (string, error) {
	res, err := runx.Run(ctx, runx.Options{Timeout: scanTimeout}, "lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n")
	return lsofExit(res.Stdout, res.ExitCode, err)
}

// enrichRunx fills Cmd from ps and Cwd from lsof, best effort.
func enrichRunx(ctx context.Context, s *Server) {
	pid := strconv.Itoa(s.PID)
	o := runx.Options{Timeout: enrichTimeout}
	if res, err := runx.Run(ctx, o, "ps", "-o", "command=", "-p", pid); err == nil && res.ExitCode == 0 {
		s.Cmd = strings.TrimSpace(string(res.Stdout))
	}
	if res, err := runx.Run(ctx, o, "lsof", "-a", "-d", "cwd", "-p", pid, "-Fn"); err == nil && res.ExitCode == 0 {
		s.Cwd = parseCwd(string(res.Stdout))
	}
}
