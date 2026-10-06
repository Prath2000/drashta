package app

// End-to-end test (03-architecture.md section 11, "API integration test"): the full app
// on a random port with a temp HOME that holds fixture projects (git, fossil when it is
// installed, a dirty file, a .drashta tracker with a task and a snapshot) and a fixture
// Claude profile. Nothing here touches the real ~/projects, ~/.claude*, ~/.drashta or the
// network: lsof is faked, gh is turned off, native actions go to a recorder.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/cli"
	"github.com/Prath2000/drashta/internal/indexer"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/claude"
	"github.com/Prath2000/drashta/internal/probe/servers"
)

// e2eRecorder records native commands instead of running them. It has its own lock so
// the test goroutine can read what the HTTP handlers recorded.
type e2eRecorder struct {
	mu   sync.Mutex
	cmds []actions.Command
}

func (r *e2eRecorder) Run(c actions.Command) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, c)
	return nil
}

func (r *e2eRecorder) list() []actions.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]actions.Command{}, r.cmds...)
}

// e2eLog is a locked buffer for the app log; it is printed when the test fails.
type e2eLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *e2eLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *e2eLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func e2eRun(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func e2eWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type e2eWorld struct {
	home, projects, alpha, beta string
	commitDay, sessionDay       string
	hasFossil                   bool
}

// e2eFixtures builds the temp HOME. Times are relative to now so the commits and the
// session fall inside the 7-day home range.
func e2eFixtures(t *testing.T) e2eWorld {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	w := e2eWorld{home: t.TempDir()}
	t.Setenv("HOME", w.home)
	t.Setenv("DRASHTA_HOME", filepath.Join(w.home, ".drashta"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("FOSSIL_HOME", w.home)
	t.Setenv("USER", "tester")
	t.Setenv("LOGNAME", "tester")
	t.Setenv("DRASHTA_AUTHOR", "")

	w.projects = filepath.Join(w.home, "projects")
	w.alpha = filepath.Join(w.projects, "personal", "alpha")
	w.beta = filepath.Join(w.projects, "personal", "beta")
	now := time.Now()
	commitAt := time.Date(now.Year(), now.Month(), now.Day()-2, 12, 0, 0, 0, time.Local)
	sessionAt := time.Date(now.Year(), now.Month(), now.Day()-1, 10, 0, 0, 0, time.Local)
	w.commitDay = commitAt.Format("2006-01-02")
	w.sessionDay = sessionAt.Format("2006-01-02")

	// alpha: a git repo with two commits and one dirty file.
	gitEnv := []string{
		"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_AUTHOR_DATE=" + commitAt.Format(time.RFC3339), "GIT_COMMITTER_DATE=" + commitAt.Format(time.RFC3339),
	}
	e2eWrite(t, filepath.Join(w.alpha, "README.md"), "# Alpha\n\nA small fixture project for the drashta e2e test.\n")
	e2eWrite(t, filepath.Join(w.alpha, "main.go"), "package main\n\nfunc main() {}\n")
	e2eRun(t, w.alpha, gitEnv, "git", "init", "-q", "-b", "main")
	e2eRun(t, w.alpha, gitEnv, "git", "add", "README.md", "main.go")
	e2eRun(t, w.alpha, gitEnv, "git", "commit", "-q", "-m", "Start the alpha project")
	e2eWrite(t, filepath.Join(w.alpha, "flux.go"), "package main\n\nconst flux = 88\n")
	e2eRun(t, w.alpha, gitEnv, "git", "add", "flux.go")
	e2eRun(t, w.alpha, gitEnv, "git", "commit", "-q", "-m", "Tune the flux capacitor for T-1")
	e2eWrite(t, filepath.Join(w.alpha, "main.go"), "package main\n\nfunc main() { println(1) }\n") // dirty
	// a link out of the project, for the /api/open test
	if err := os.Symlink("/etc", filepath.Join(w.alpha, "outside")); err != nil {
		t.Fatal(err)
	}

	// alpha/.drashta: one task and one snapshot (through the CLI, like a session would).
	png := filepath.Join(w.home, "shot.png")
	e2eWrite(t, png, "\x89PNG\r\n\x1a\nfake image bytes")
	cliEnv := cli.Env{Cwd: w.alpha, Home: w.home, ProjectsDir: w.projects, Author: "Claude", Now: time.Now}
	for _, args := range [][]string{
		{"task", "new", "Wire the flux capacitor", "--status", "in_progress", "-q"},
		{"snap", "add", "--task", "T-1", "--image", png, "--title", "first look", "-q"},
	} {
		var out, errb bytes.Buffer
		if code := cli.Run(args, &out, &errb, cliEnv); code != 0 {
			t.Fatalf("cli %v: exit %d\n%s%s", args, code, out.String(), errb.String())
		}
	}

	// beta: a fossil checkout (repository file outside the projects folder).
	if _, err := exec.LookPath("fossil"); err == nil {
		w.hasFossil = true
		repo := filepath.Join(w.home, "repos", "beta.fossil")
		if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(w.beta, 0o755); err != nil {
			t.Fatal(err)
		}
		utc := commitAt.UTC().Format("2006-01-02T15:04:05")
		e2eRun(t, w.home, nil, "fossil", "new", "--admin-user", "tester", "--date-override", utc, repo)
		e2eRun(t, w.beta, nil, "fossil", "open", repo)
		e2eWrite(t, filepath.Join(w.beta, "notes.txt"), "beta notes\n")
		e2eRun(t, w.beta, nil, "fossil", "addremove")
		e2eRun(t, w.beta, nil, "fossil", "commit", "-m", "Beta first check-in", "--date-override", utc,
			"--no-warnings", "--no-prompt")
	} else {
		e2eWrite(t, filepath.Join(w.beta, "notes.txt"), "beta notes\n")
	}

	// A Claude profile with one session in alpha, yesterday.
	profile := filepath.Join(w.home, ".claude-e2e")
	sessID := "0b7c2d4e-1111-4222-8333-944455556666"
	ts := func(d time.Duration) string { return sessionAt.Add(d).UTC().Format("2006-01-02T15:04:05.000Z") }
	var jl strings.Builder
	for _, rec := range []map[string]any{
		{"type": "user", "cwd": w.alpha, "sessionId": sessID, "uuid": "u1", "timestamp": ts(0),
			"message": map[string]any{"role": "user", "content": "Please check the flux capacitor wiring"}},
		{"type": "assistant", "cwd": w.alpha, "sessionId": sessID, "uuid": "u2", "timestamp": ts(5 * time.Minute),
			"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "Done."}}}},
		{"type": "user", "cwd": w.alpha, "sessionId": sessID, "uuid": "u3", "timestamp": ts(40 * time.Minute),
			"message": map[string]any{"role": "user", "content": "Thanks"}},
	} {
		b, _ := json.Marshal(rec)
		jl.Write(b)
		jl.WriteByte('\n')
	}
	e2eWrite(t, filepath.Join(profile, "projects", claude.Encode(w.alpha), sessID+".jsonl"), jl.String())

	// config.toml
	cfg := fmt.Sprintf(`projects_dir = %q
roots = ["personal"]
hidden = []
skip = []
editor = ["code", "-g", "{path}:{line}"]
terminal = "Terminal"
claude_profiles = [%q]
gh_enabled = false
`, w.projects, profile)
	e2eWrite(t, filepath.Join(w.home, ".drashta", "config.toml"), cfg)
	return w
}

// e2eClient talks to the running app.
type e2eClient struct {
	t     *testing.T
	base  string
	host  string
	token string
	hc    *http.Client
}

func (c *e2eClient) req(method, path string, body any, hdr map[string]string) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (c *e2eClient) getJSON(path string, v any) {
	c.t.Helper()
	code, body := c.req("GET", path, nil, nil)
	if code != 200 {
		c.t.Fatalf("GET %s: %d %s", path, code, body)
	}
	if err := json.Unmarshal(body, v); err != nil {
		c.t.Fatalf("GET %s: %v\n%s", path, err, body)
	}
}

func (c *e2eClient) post(path string, body any) (int, []byte) {
	return c.req("POST", path, body, map[string]string{"X-Drashta-Token": c.token})
}

type sseEvent struct{ typ, data string }

// e2eSSE opens /api/events and sends every event to the returned channel.
func e2eSSE(t *testing.T, ctx context.Context, base string) <-chan sseEvent {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("SSE status %d", resp.StatusCode)
	}
	ch := make(chan sseEvent, 256)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.typ = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data += strings.TrimPrefix(line, "data: ")
			case line == "":
				if ev.typ != "" {
					select {
					case ch <- ev:
					default:
					}
				}
				ev = sseEvent{}
			}
		}
	}()
	return ch
}

type bootReply struct {
	Counts struct {
		OpenTasks      int `json:"openTasks"`
		AwaitingReview int `json:"awaitingReview"`
	} `json:"counts"`
	Projects []struct {
		ID         string   `json:"id"`
		VCS        []string `json:"vcs"`
		DirtyCount int      `json:"dirtyCount"`
		FileCount  *int     `json:"fileCount"`
		Surveyed   bool     `json:"surveyed"`
	} `json:"projects"`
}

func (b bootReply) fileCount(id string) int {
	for _, p := range b.Projects {
		if p.ID == id && p.FileCount != nil {
			return *p.FileCount
		}
	}
	return -1
}

func TestE2E(t *testing.T) {
	w := e2eFixtures(t)
	logBuf := &e2eLog{}
	defer func() {
		if t.Failed() {
			t.Logf("app log:\n%s", logBuf.String())
		}
	}()
	rec := &e2eRecorder{}

	type ready struct{ addr, token string }
	readyCh := make(chan ready, 1)
	startedCh := make(chan *indexer.Indexer, 1)
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(ctx, Options{
			Addr:     "127.0.0.1:0",
			Logger:   slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
			Exec:     rec,
			Debounce: 150 * time.Millisecond,
			Probes: indexer.Probes{
				Servers: func(ctx context.Context, ps []probe.Project) (servers.Snapshot, error) {
					return servers.Snapshot{At: time.Now()}, nil // no real lsof
				},
				PageTitle: func(int) string { return "" },
			},
			Ready:   func(addr, token string) { readyCh <- ready{addr, token} },
			Started: func(ix *indexer.Indexer) { startedCh <- ix },
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("Run returned %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("Run did not stop within 20 s")
		}
	}()

	var r ready
	select {
	case r = <-readyCh:
	case err := <-runErr:
		t.Fatalf("Run failed early: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("server did not start")
	}
	c := &e2eClient{t: t, base: "http://" + r.addr, host: r.addr, token: r.token, hc: &http.Client{Timeout: 15 * time.Second}}

	// Health, before the sweep (HTTP starts first).
	if code, body := c.req("GET", "/api/health", nil, nil); code != 200 || !strings.Contains(string(body), `"drashta"`) {
		t.Fatalf("/api/health: %d %s", code, body)
	}
	if code, body := c.req("GET", "/healthz", nil, nil); code != 200 || string(body) != "ok" {
		t.Errorf("/healthz: %d %q", code, body)
	}
	if code, _ := c.req("GET", "/healthz", nil, map[string]string{"Host": "evil.example"}); code != 403 {
		t.Errorf("/healthz with a bad Host: %d, want 403", code)
	}

	// Guard: bad Host, bad Origin, missing or wrong token.
	if code, _ := c.req("GET", "/api/boot", nil, map[string]string{"Host": "evil.example:80"}); code != 403 {
		t.Errorf("bad Host: %d, want 403", code)
	}
	if code, _ := c.req("GET", "/api/boot", nil, map[string]string{"Origin": "http://evil.example"}); code != 403 {
		t.Errorf("bad Origin: %d, want 403", code)
	}
	if code, _ := c.req("POST", "/api/refresh", map[string]any{}, nil); code != 403 {
		t.Errorf("no token: %d, want 403", code)
	}
	if code, _ := c.req("POST", "/api/refresh", map[string]any{}, map[string]string{"X-Drashta-Token": strings.Repeat("0", 64)}); code != 403 {
		t.Errorf("wrong token: %d, want 403", code)
	}

	// Wait for the first sweep.
	var ix *indexer.Indexer
	select {
	case ix = <-startedCh:
	case <-time.After(10 * time.Second):
		t.Fatal("indexer did not start")
	}
	select {
	case <-ix.FirstSweepDone():
	case <-time.After(90 * time.Second):
		t.Fatal("first sweep did not finish in 90 s")
	}

	// /api/boot lists the projects, with VCS and the dirty file; counts see the task.
	var boot bootReply
	c.getJSON("/api/boot", &boot)
	ids := map[string]int{}
	for i, p := range boot.Projects {
		ids[p.ID] = i
	}
	ai, ok := ids["alpha"]
	if !ok {
		t.Fatalf("/api/boot has no alpha: %+v", boot.Projects)
	}
	if _, ok := ids["beta"]; !ok {
		t.Errorf("/api/boot has no beta: %+v", boot.Projects)
	}
	if a := boot.Projects[ai]; strings.Join(a.VCS, ",") != "git" || a.DirtyCount < 1 || !a.Surveyed {
		t.Errorf("alpha in boot: %+v", a)
	}
	if w.hasFossil {
		if b := boot.Projects[ids["beta"]]; strings.Join(b.VCS, ",") != "fossil" {
			t.Errorf("beta vcs = %v, want fossil", b.VCS)
		}
	}
	if boot.Counts.OpenTasks != 1 || boot.Counts.AwaitingReview != 1 {
		t.Errorf("counts = %+v, want 1 open task awaiting review", boot.Counts)
	}

	// /api/home has real lanes: commits and sessions on the right days and projects.
	var home struct {
		Days  []string `json:"days"`
		Lanes []struct {
			ProjectID string `json:"projectId"`
			Days      []struct {
				Commits  int `json:"commits"`
				Sessions int `json:"sessions"`
			} `json:"days"`
		} `json:"lanes"`
	}
	c.getJSON("/api/home?range=7", &home)
	cell := func(project, day string) (commits, sessions int, found bool) {
		di := -1
		for i, d := range home.Days {
			if d == day {
				di = i
			}
		}
		for _, l := range home.Lanes {
			if l.ProjectID == project && di >= 0 && di < len(l.Days) {
				return l.Days[di].Commits, l.Days[di].Sessions, true
			}
		}
		return 0, 0, false
	}
	if n, _, ok := cell("alpha", w.commitDay); !ok || n != 2 {
		t.Errorf("alpha commits on %s = %d (lane found %v), want 2; days %v", w.commitDay, n, ok, home.Days)
	}
	if _, s, ok := cell("alpha", w.sessionDay); !ok || s != 1 {
		t.Errorf("alpha sessions on %s = %d, want 1", w.sessionDay, s)
	}
	if n, s, _ := cell("alpha", w.sessionDay); w.sessionDay != w.commitDay && n != 0 {
		t.Errorf("alpha commits on the session day = %d (sessions %d), want 0", n, s)
	}
	if w.hasFossil {
		// fossil new makes an "initial empty check-in" on the same date, so 2.
		if n, _, ok := cell("beta", w.commitDay); !ok || n != 2 {
			t.Errorf("beta commits on %s = %d, want 2", w.commitDay, n)
		}
	}
	if _, s, _ := cell("beta", w.sessionDay); s != 0 {
		t.Errorf("beta sessions = %d, want 0 (the session is alpha's)", s)
	}

	// /api/search finds a commit by a fragment of its message.
	var search struct {
		Hits []struct {
			Kind      string `json:"kind"`
			ProjectID string `json:"projectId"`
			Ref       string `json:"ref"`
		} `json:"hits"`
	}
	c.getJSON("/api/search?q=capacit", &search)
	foundCommit := false
	for _, h := range search.Hits {
		if h.Kind == "commit" && h.ProjectID == "alpha" {
			foundCommit = true
		}
	}
	if !foundCommit {
		t.Errorf("search did not find the commit: %+v", search.Hits)
	}

	// /api/open: paths outside the roots are refused and run nothing; a good ref runs the
	// recorder only.
	for _, ref := range []string{"alpha:file:../../../../etc/passwd", "alpha:file:outside/passwd", "alpha:file:/etc/passwd"} {
		if code, body := c.post("/api/open", map[string]any{"ref": ref}); code < 400 || code >= 500 {
			t.Errorf("open %s: %d %s, want a 4xx", ref, code, body)
		}
	}
	if n := len(rec.list()); n != 0 {
		t.Fatalf("refused opens ran %d commands: %v", n, rec.list())
	}
	if code, body := c.post("/api/open", map[string]any{"ref": "alpha:file:README.md"}); code != 200 {
		t.Errorf("open README: %d %s", code, body)
	}
	if cmds := rec.list(); len(cmds) != 1 || cmds[0].Name != "code" || !strings.Contains(strings.Join(cmds[0].Args, " "), "README.md") {
		t.Errorf("recorded commands = %+v", cmds)
	}

	// Settings: the editor is read only through the API (security I-1). PATCH editorCmd is
	// 400 read_only, config.toml is unchanged, and /open still runs the config.toml editor.
	cfgFile := filepath.Join(w.home, ".drashta", "config.toml")
	cfgBefore, _ := os.ReadFile(cfgFile)
	for _, v := range []any{"zed {path}", []string{"sh", "-c", "touch x"}} {
		code, body := c.req("PATCH", "/api/settings", map[string]any{"editorCmd": v},
			map[string]string{"X-Drashta-Token": c.token})
		if code != 400 || !strings.Contains(string(body), "read_only") {
			t.Fatalf("PATCH settings editorCmd %v: %d %s, want 400 read_only", v, code, body)
		}
	}
	if b, _ := os.ReadFile(cfgFile); string(b) != string(cfgBefore) {
		t.Errorf("config.toml changed by a refused PATCH:\n%s", b)
	}
	if code, body := c.post("/api/open", map[string]any{"ref": "alpha:file:README.md"}); code != 200 {
		t.Errorf("open README after refused editor change: %d %s", code, body)
	}
	if cmds := rec.list(); len(cmds) != 2 || cmds[1].Name != "code" {
		t.Errorf("editor changed by the API: %+v", cmds)
	}
	var st struct {
		EditorCmd string `json:"editorCmd"`
	}
	c.getJSON("/api/settings", &st)
	if st.EditorCmd != "code -g {path}:{line}" {
		t.Errorf("settings show %q, want the config.toml editor", st.EditorCmd)
	}

	// Kill without confirm is 400.
	if code, body := c.post("/api/servers/kill", map[string]any{"pid": 4242, "key": "x"}); code != 400 {
		t.Errorf("kill without confirm: %d %s, want 400", code, body)
	}

	// Live update: touching a file gives an SSE project/activity event and a changed count.
	sseCtx, sseCancel := context.WithCancel(ctx)
	defer sseCancel()
	events := e2eSSE(t, sseCtx, c.base)
	select {
	case ev := <-events:
		if ev.typ != "hello" {
			t.Fatalf("first SSE event = %q, want hello", ev.typ)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no hello event")
	}
	before := boot.fileCount("alpha")
	e2eWrite(t, filepath.Join(w.alpha, "docs", "new-note.md"), "# A new note\n")
	deadline := time.After(15 * time.Second)
	gotEvent := false
	for !gotEvent {
		select {
		case ev, open := <-events:
			if !open {
				t.Fatal("SSE stream closed")
			}
			if (ev.typ == "project" || ev.typ == "activity") && strings.Contains(ev.data, `"alpha"`) {
				gotEvent = true
			}
		case <-deadline:
			t.Fatal("no project/activity event for alpha within 15 s of a file change")
		}
	}
	changed := false
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		var b bootReply
		c.getJSON("/api/boot", &b)
		if b.fileCount("alpha") > before {
			changed = true
			break
		}
	}
	if !changed {
		t.Errorf("alpha fileCount did not grow from %d after a new file", before)
	}
	sseCancel()

	// The page itself (only when the web client is built into the binary).
	if code, body := c.req("GET", "/", nil, nil); code == 200 {
		if !strings.Contains(string(body), r.token) {
			t.Error("index.html does not carry the token")
		}
	} else if code != 503 {
		t.Errorf("GET /: %d", code)
	}

	if !w.hasFossil {
		t.Log("fossil is not installed: the fossil parts were skipped")
	}
}
