package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Prath2000/drashta/internal/probe/servers"
)

// More errors for the destructive actions.
var (
	ErrConfirm  = errors.New("actions: confirm required")
	ErrNotLive  = errors.New("actions: pid is not a listed server")
	ErrConflict = errors.New("actions: server is already running or restarting")
)

// RestartGrace bounds how long a launched key blocks further restarts if it never comes up.
const RestartGrace = 10 * time.Second

// Manager runs kill and restart (copied from ledger server.go / actions.go).
// It holds the current live set and the "restarting" grace map. The indexer
// calls SetLive after every successful lsof pass.
type Manager struct {
	logDir string
	lookup func(key string) (servers.Server, bool)

	mu         sync.Mutex
	live       map[int]servers.Server // by PID
	restarting map[string]time.Time   // key -> launch time

	// Swapped in tests so nothing is ever signalled or launched.
	kill   func(pid int) error
	launch func(s servers.Server, logDir string) error
	now    func() time.Time
}

// NewManager makes a Manager. logDir is <DataDir>/logs. lookup returns a
// server that drashta recorded itself (normally History.Lookup).
func NewManager(logDir string, lookup func(key string) (servers.Server, bool)) *Manager {
	return &Manager{
		logDir:     logDir,
		lookup:     lookup,
		live:       map[int]servers.Server{},
		restarting: map[string]time.Time{},
		kill:       killPID,
		launch:     restart,
		now:        time.Now,
	}
}

// SetLive replaces the live set and clears pending restarts whose key is now
// live or whose grace period ran out. Call it only after a successful scan.
func (m *Manager) SetLive(live []servers.Server, now time.Time) {
	byPID := make(map[int]servers.Server, len(live))
	alive := make(map[string]bool, len(live))
	for _, s := range live {
		byPID[s.PID] = s
		alive[servers.Key(s)] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.live = byPID
	for k, started := range m.restarting {
		if alive[k] || now.Sub(started) > RestartGrace {
			delete(m.restarting, k)
		}
	}
}

// Pending reports whether key has a restart in its grace period.
func (m *Manager) Pending(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.restarting[key]
	return ok
}

// Kill stops pid: SIGTERM, then SIGKILL after 3 s. confirm must be true and
// pid must be in the current live set. If key is not empty it must match the
// live server's key (so a reused PID is never killed by a stale page).
func (m *Manager) Kill(pid int, key string, confirm bool) error {
	if !confirm {
		return ErrConfirm
	}
	m.mu.Lock()
	s, known := m.live[pid]
	m.mu.Unlock()
	if !known {
		return ErrNotLive
	}
	if key != "" && key != servers.Key(s) && key != servers.RefKey(servers.Key(s)) {
		return fmt.Errorf("%w: key does not match pid", ErrNotLive)
	}
	err := m.kill(pid)
	slog.Info("action", "action", "kill", "ref", "server:"+strconv.Itoa(s.Port), "ok", err == nil)
	return err
}

// Restart replays the recorded (cwd, cmd) of key with "sh -c" and returns the
// log file path (<logDir>/<port>.log). confirm must be true. It fails with
// ErrNotFound for an unknown key, ErrForbidden when the server may not be
// replayed (see CheckRestartable) and ErrConflict when the server is alive or
// a restart is still pending.
func (m *Manager) Restart(key string, confirm bool) (logPath string, err error) {
	if !confirm {
		return "", ErrConfirm
	}
	s, found := m.lookup(key)
	if !found {
		return "", fmt.Errorf("%w: unknown server", ErrNotFound)
	}
	if err := CheckRestartable(s); err != nil {
		return "", err
	}
	k := servers.Key(s)
	m.mu.Lock()
	_, pending := m.restarting[k]
	liveNow := false
	for _, l := range m.live {
		if servers.Key(l) == k {
			liveNow = true
			break
		}
	}
	if s.Alive || pending || liveNow {
		m.mu.Unlock()
		return "", ErrConflict
	}
	m.restarting[k] = m.now()
	m.mu.Unlock()
	defer func() {
		slog.Info("action", "action", "restart", "ref", "server:"+strconv.Itoa(s.Port), "ok", err == nil)
	}()
	if err := m.launch(s, m.logDir); err != nil {
		m.mu.Lock()
		delete(m.restarting, k)
		m.mu.Unlock()
		return "", err
	}
	return LogPath(m.logDir, s.Port), nil
}

// shellMeta holds the characters that mean something to /bin/sh. ps joins
// argv with spaces and no quoting, so a recorded command holding any of them
// would not run as it first ran (and an argument could turn into commands).
const shellMeta = ";|&<>$`\\\"'(){}*?~!\n\r"

// CheckRestartable says whether a recorded server may be replayed through
// /bin/sh -c. It must be a dev server (kind "dev") inside a project, and its
// command must hold no shell metacharacter. Otherwise it returns ErrForbidden
// with the reason.
func CheckRestartable(s servers.Server) error {
	if s.ProjectID == "" {
		return fmt.Errorf("%w: restart is only for servers inside a project", ErrForbidden)
	}
	if s.Kind != servers.KindDev {
		return fmt.Errorf("%w: restart is only for dev servers (this one is %q)", ErrForbidden, s.Kind)
	}
	if strings.TrimSpace(s.Cmd) == "" {
		return fmt.Errorf("%w: no command recorded", ErrForbidden)
	}
	if i := strings.IndexAny(s.Cmd, shellMeta); i >= 0 {
		return fmt.Errorf("%w: the command holds the shell character %q and cannot be replayed safely", ErrForbidden, s.Cmd[i])
	}
	return nil
}

// LogPath is <logDir>/<port>.log.
func LogPath(logDir string, port int) string {
	return filepath.Join(logDir, strconv.Itoa(port)+".log")
}

// killPID sends SIGTERM, waits up to 3s for the process to exit, then SIGKILLs it.
func killPID(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to kill pid %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return nil
		}
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// restart relaunches s.Cmd in s.Cwd as a detached session, logging to logDir/<port>.log.
// This is the only place drashta uses a shell: it replays a command line that
// drashta itself recorded from ps.
func restart(s servers.Server, logDir string) error {
	if s.Cmd == "" || s.Cwd == "" {
		return errors.New("no command or working directory recorded")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(LogPath(logDir, s.Port), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close() // the child keeps its own descriptor
	cmd := exec.Command("/bin/sh", "-c", s.Cmd)
	cmd.Dir = s.Cwd
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the child so it never lingers as a zombie
	return nil
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// PageInfo is what ProbePage learned about the page a localhost port serves.
type PageInfo struct {
	HTTP      bool   // the port answered with an HTTP response
	Title     string // <title> of the page, "" when none
	Frameable bool   // the page may be embedded in an iframe on the parent origin
	Reason    string // why not frameable (or not a web page); "" when frameable
}

// ProbePage makes one GET of / on the port (127.0.0.1 first, then [::1]; 2 s in all, body capped
// at 64 KiB) and reports the title and whether an iframe on parentOrigin (for example
// "http://localhost:4455") may show it. Only loopback is ever contacted: a redirect to another
// host is not followed, and the headers of the redirect response are judged.
//
// Not frameable: X-Frame-Options DENY or SAMEORIGIN (the port differs from the parent's, so
// SAMEORIGIN fails; ALLOW-FROM is obsolete and ignored), or a Content-Security-Policy
// frame-ancestors list that has no source matching parentOrigin. Report-Only policies do not block.
func ProbePage(port int, parentOrigin string) PageInfo {
	if port <= 0 || port > 65535 {
		return PageInfo{Reason: "not a web page"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !isLoopbackHost(req.URL.Hostname()) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	var resp *http.Response
	for _, host := range []string{"127.0.0.1", "[::1]"} { // vite on macOS often binds only ::1
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s:%d/", host, port), nil)
		if err != nil {
			continue
		}
		if resp, err = client.Do(req); err == nil {
			break
		}
		resp = nil
	}
	if resp == nil {
		return PageInfo{Reason: "not a web page"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	info := PageInfo{HTTP: true}
	if m := titleRe.FindSubmatch(body); m != nil {
		info.Title = strings.TrimSpace(string(m[1]))
	}
	info.Reason = frameBlocker(resp.Header, parentOrigin)
	info.Frameable = info.Reason == ""
	return info
}

// PageTitle returns the <title> of http://127.0.0.1:<port>/, or "" on any failure.
func PageTitle(port int) string {
	return ProbePage(port, "").Title
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// frameBlocker returns the reason a page with these headers cannot be shown in an iframe on
// parentOrigin, or "" when it can.
func frameBlocker(h http.Header, parentOrigin string) string {
	// CSP frame-ancestors wins: a browser ignores X-Frame-Options when any policy has it.
	parent, _ := url.Parse(parentOrigin)
	seenCSP := false
	for _, v := range h.Values("Content-Security-Policy") { // Report-Only is another header: ignored
		for _, policy := range strings.Split(v, ",") { // a header may carry several policies; all are enforced
			for _, directive := range strings.Split(policy, ";") {
				f := strings.Fields(directive)
				if len(f) == 0 || !strings.EqualFold(f[0], "frame-ancestors") {
					continue
				}
				seenCSP = true
				if !anyAncestorMatches(f[1:], parent) {
					return "CSP frame-ancestors " + strings.Join(f[1:], " ")
				}
				break // only the first frame-ancestors of a policy counts
			}
		}
	}
	if seenCSP {
		return ""
	}
	for _, v := range h.Values("X-Frame-Options") {
		for _, tok := range strings.Split(v, ",") {
			if tok = strings.ToUpper(strings.TrimSpace(tok)); tok == "DENY" || tok == "SAMEORIGIN" {
				return "X-Frame-Options: " + tok
			}
		}
	}
	return ""
}

func anyAncestorMatches(sources []string, parent *url.URL) bool {
	for _, src := range sources {
		if ancestorMatches(strings.ToLower(src), parent) {
			return true
		}
	}
	return false
}

// ancestorMatches says whether one CSP frame-ancestors source allows the parent page.
// 'none' and 'self' never do: 'self' is the framed app's own origin, not drashta's.
func ancestorMatches(src string, parent *url.URL) bool {
	if parent == nil || parent.Host == "" {
		return src == "*"
	}
	sameScheme := func(s string) bool { return s == parent.Scheme || (s == "http" && parent.Scheme == "https") }
	switch {
	case src == "*":
		return true
	case strings.HasPrefix(src, "'"):
		return false
	case strings.HasSuffix(src, ":") && !strings.Contains(src, "/"): // scheme source, "http:"
		return sameScheme(strings.TrimSuffix(src, ":"))
	}
	// Host source: [scheme://]host[:port][/path]. host may be "*" or "*.example.com".
	if i := strings.Index(src, "://"); i >= 0 {
		if !sameScheme(src[:i]) {
			return false
		}
		src = src[i+3:]
	}
	if i := strings.IndexAny(src, "/?#"); i >= 0 {
		src = src[:i]
	}
	host, port := src, ""
	if h, p, err := net.SplitHostPort(src); err == nil {
		host, port = h, p
	}
	host = strings.Trim(host, "[]")
	parentPort := parent.Port()
	if parentPort == "" { // no port in the source means the scheme's default port
		parentPort = map[string]string{"http": "80", "https": "443"}[parent.Scheme]
	}
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[parent.Scheme]
	}
	if port != "*" && port != parentPort {
		return false
	}
	ph := parent.Hostname()
	return host == "*" || host == ph || (strings.HasPrefix(host, "*.") && len(ph) > len(host)-1 && strings.HasSuffix(ph, host[1:]))
}
