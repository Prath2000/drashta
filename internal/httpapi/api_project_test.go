package httpapi

// Tests for the project API (api_project.go, api_files.go, api_commits.go, api_sessions.go,
// api_servers.go, api_github.go). Everything lives in t.TempDir(): a fixture projects folder with
// a real git repo, a fixture Claude transcript, and a temp SQLite index. Nothing here reads or
// writes the real ~/projects, ~/.claude* or ~/.drashta, and nothing uses the network.
// Helper names start with "pjt" so they cannot clash with other test files of this package.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/docs"
	"github.com/Prath2000/drashta/internal/probe/git"
	"github.com/Prath2000/drashta/internal/probe/servers"
	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

const pjtSessionID = "11111111-2222-3333-4444-555555555555"

// pjtPNG is a valid 1x1 PNG.
var pjtPNG, _ = base64.StdEncoding.DecodeString(
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

type pjtClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *pjtClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *pjtClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type pjtServers struct {
	mu     sync.Mutex
	active []servers.Server
	recent []servers.Server
}

func (s *pjtServers) Snapshot() ([]servers.Server, []servers.Server) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]servers.Server(nil), s.active...), append([]servers.Server(nil), s.recent...)
}

type pjtKill struct {
	PID     int
	Key     string
	Confirm bool
}

type pjtActions struct {
	mu          sync.Mutex
	kills       []pjtKill
	restarts    []string
	killErr     error
	restartErr  error
	restartPath string
}

func (a *pjtActions) Kill(pid int, key string, confirm bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.kills = append(a.kills, pjtKill{pid, key, confirm})
	return a.killErr
}

func (a *pjtActions) Restart(key string, confirm bool) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.restarts = append(a.restarts, key)
	if a.restartErr != nil {
		return "", a.restartErr
	}
	return a.restartPath, nil
}

type pjtGH struct {
	mu    sync.Mutex
	calls int
	data  ProjectGitHubData
	err   error
}

func (g *pjtGH) GitHub(ctx context.Context, p probe.Project, remote, branch string) (ProjectGitHubData, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return g.data, g.err
}

// pjtEnv is one complete test world.
type pjtEnv struct {
	t        *testing.T
	db       *store.DB
	srv      *Server
	api      *ProjectAPI
	home     string
	projects string
	alpha    string // project folder (real git repo)
	outside  string // a folder outside every project
	commits  []string
	clock    *pjtClock
	servers  *pjtServers
	actions  *pjtActions
	gh       *pjtGH
	tdeps    ProjectDeps
}

func pjtGit(t *testing.T, dir string, date string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir,
		"GIT_AUTHOR_NAME=Fixture Author", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture Author", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func pjtWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pjtNew builds the world. mod may change the dependencies before the routes are registered.
func pjtNew(t *testing.T, mod func(*ProjectDeps)) *pjtEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	e := &pjtEnv{t: t}
	e.home = t.TempDir()
	e.projects = filepath.Join(e.home, "projects")
	e.alpha = filepath.Join(e.projects, "personal", "alpha")
	e.outside = filepath.Join(e.home, "outside")
	pjtWrite(t, filepath.Join(e.outside, "secret.txt"), "TOP SECRET OUTSIDE\n")

	// --- the alpha project: a real git repo with two commits and a dirty tree ---
	pjtWrite(t, filepath.Join(e.alpha, "README.md"), "# Alpha\n\nA fixture project.\n")
	pjtWrite(t, filepath.Join(e.alpha, "src", "main.go"), "package main\n\nfunc main() {}\n")
	pjtWrite(t, filepath.Join(e.alpha, "docs", "guide.md"), "# The Guide\n\nLine two.\n")
	pjtWrite(t, filepath.Join(e.alpha, ".env"), "TOKEN=hunter2-super-secret\n")
	pjtWrite(t, filepath.Join(e.alpha, "page.html"), "<script>alert(1)</script>\n")
	pjtWrite(t, filepath.Join(e.alpha, "data.bin"), "bin\x00ary")
	pjtWrite(t, filepath.Join(e.alpha, "lines.txt"), "one\ntwo\nthree\nfour\nfive\n")
	pjtWrite(t, filepath.Join(e.alpha, "node_modules", "pkg", "index.js"), "module.exports = 1\n")
	if err := os.WriteFile(filepath.Join(e.alpha, "logo.png"), pjtPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	pjtGit(t, e.alpha, "2026-09-01T10:00:00+00:00", "init", "-q", "-b", "main")
	pjtGit(t, e.alpha, "2026-09-01T10:00:00+00:00", "add", "README.md", "src", "docs", "page.html", "lines.txt", "logo.png")
	pjtGit(t, e.alpha, "2026-09-01T10:00:00+00:00", "commit", "-q", "-m", "first commit for T-1")
	pjtWrite(t, filepath.Join(e.alpha, "src", "main.go"), "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	pjtGit(t, e.alpha, "2026-09-02T11:00:00+00:00", "add", "src/main.go")
	pjtGit(t, e.alpha, "2026-09-02T11:00:00+00:00", "commit", "-q", "-m", "second commit")
	// Dirty: a modified tracked file and a new untracked file.
	pjtWrite(t, filepath.Join(e.alpha, "src", "main.go"), "package main\n\nfunc main() {\n\tprintln(\"dirty\")\n}\n")
	pjtWrite(t, filepath.Join(e.alpha, "new.txt"), "untracked\n")
	// Links: out of the project, and to a secret inside it.
	for name, target := range map[string]string{
		"escape":          e.outside,
		"escape-file.txt": filepath.Join(e.outside, "secret.txt"),
		"link-to-env.txt": filepath.Join(e.alpha, ".env"),
	} {
		if err := os.Symlink(target, filepath.Join(e.alpha, name)); err != nil {
			t.Fatal(err)
		}
	}
	// A second project with no history.
	beta := filepath.Join(e.projects, "tools", "beta")
	pjtWrite(t, filepath.Join(beta, "a.txt"), "beta\n")

	// --- the index ---
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e.db = db
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	pa := probe.Project{ID: "alpha", Name: "alpha", Group: "personal", Path: e.alpha}
	res, err := git.Scan(ctx, pa)
	must(err)
	for _, c := range res.Commits {
		e.commits = append(e.commits, c.ID)
	}
	must(db.UpsertProject(ctx, store.Project{
		ID: "alpha", Name: "alpha", Group: "personal", Path: e.alpha, VCS: "git", Branch: "main",
		Remote: "git@github.com:fixture/alpha.git", Description: "fixture", Stack: []string{"go"},
		ReadmePath: "README.md", LastChangeAt: time.Unix(1790000000, 0), IndexedAt: time.Unix(1790000100, 0),
	}))
	must(db.UpsertProject(ctx, store.Project{ID: "beta", Name: "beta", Group: "tools", Path: beta}))
	must(db.ReplaceCommits(ctx, "alpha", res.Commits))
	must(db.ReplaceDirty(ctx, "alpha", res.Dirty))
	walk, err := tree.Walk(ctx, e.alpha, nil)
	must(err)
	titles := map[string]docs.Doc{}
	for _, d := range docs.Extract(walk.Entries, e.alpha) {
		titles[d.Path] = d
	}
	var files []store.File
	for _, en := range walk.Entries {
		if en.Dir {
			continue
		}
		f := store.File{Path: en.Path, Size: en.Size, MTime: en.MTime, Kind: en.Kind}
		if d, ok := titles[en.Path]; ok {
			f.Title, f.Lines = d.Title, d.Lines
		}
		files = append(files, f)
	}
	must(db.ReplaceFiles(ctx, "alpha", files))

	// A Claude transcript with a secret inside, and the session rows.
	jsonl := filepath.Join(e.home, ".claude", "projects", "-fx-alpha", pjtSessionID+".jsonl")
	pjtWrite(t, jsonl, strings.Join([]string{
		`{"cwd":"` + e.alpha + `","type":"user","message":{"role":"user","content":"Please fix the build. token ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD"},"timestamp":"2026-09-02T10:00:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Looking at it now."}]},"timestamp":"2026-09-02T10:00:05Z"}`,
		`{"type":"user","message":{"role":"user","content":"Thanks, ship it."},"timestamp":"2026-09-02T10:01:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]},"timestamp":"2026-09-02T10:01:05Z"}`,
	}, "\n")+"\n")
	must(db.UpsertSessions(ctx, []store.Session{
		{ID: pjtSessionID, Profile: filepath.Join(e.home, ".claude"), JSONLPath: jsonl, CWD: e.alpha, ProjectID: "alpha",
			Start: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 2, 10, 1, 5, 0, time.UTC),
			Title: "Fix the build", Turns: 4, KB: 3, Tools: map[string]int{"Edit": 2}, Edited: []string{"src/main.go"}},
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", Profile: "p", JSONLPath: filepath.Join(e.home, "gone.jsonl"), CWD: beta,
			ProjectID: "beta", Start: time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC),
			Title: "Talked about alpha"},
	}))
	must(db.SetSessionProjects(ctx, "aaaaaaaa-0000-0000-0000-000000000001", []store.SessionProject{
		{ProjectID: "beta", Reason: "cwd"}, {ProjectID: "alpha", Reason: "mention"}}))

	// Tracker rows: one task linked to the first commit and to the session.
	first := res.Commits[len(res.Commits)-1].ID
	must(db.UpsertAuthoredTask(ctx, store.Task{ProjectID: "alpha", ID: "T-1", Title: "Build it", Status: "in_progress", FeatureID: "F-1"}))
	must(db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "auto", []store.TaskLink{
		{Ref: "commit:" + first[:10], Reason: "commit message"}, {Ref: "session:" + pjtSessionID, Reason: "mentioned"}}))
	must(db.UpsertAuthoredFeature(ctx, store.Feature{ProjectID: "alpha", ID: "F-1", Title: "Core", MilestoneID: "M-1"}))
	must(db.UpsertAuthoredMilestone(ctx, store.Milestone{ProjectID: "alpha", ID: "M-1", Title: "v1", Due: "2026-10-05", State: "open"}))
	line := 2
	must(db.UpsertAuthoredNote(ctx, store.Note{ProjectID: "alpha", ID: "N-1", Ref: "file:src/main.go#L2", Body: "check this", Author: "alice", Created: "2026-09-02T12:00:00+05:30", Line: &line}))
	must(db.UpsertAuthoredNote(ctx, store.Note{ProjectID: "alpha", ID: "N-2", Ref: "file:README.md", Body: "other file", Created: "2026-09-02T12:01:00+05:30"}))

	// Servers.
	e.servers = &pjtServers{
		active: []servers.Server{{Port: 3000, PID: 4242, Name: "node", Cmd: "node server.js", Cwd: e.alpha, ProjectID: "alpha",
			Kind: servers.KindDev, FirstSeen: time.Unix(1790000000, 0), LastSeen: time.Unix(1790000500, 0), Alive: true},
			{Port: 9000, PID: 77, Name: "ControlCenter", Cmd: "cc", Cwd: "/", Kind: servers.KindHelper, Alive: true}},
		recent: []servers.Server{{Port: 5173, PID: 1, Name: "node", Cmd: "vite", Cwd: e.alpha, ProjectID: "alpha",
			Kind: servers.KindDev, FirstSeen: time.Unix(1780000000, 0), LastSeen: time.Unix(1780000500, 0)}},
	}
	must(db.ReplaceServersNow(ctx, []store.ServerNow{
		{Key: servers.Key(e.servers.active[0]), Port: 3000, PID: 4242, Name: "node", ProjectID: "alpha", Title: "Alpha dev page"}}))
	e.actions = &pjtActions{restartPath: filepath.Join(e.home, ".drashta", "logs", "5173.log")}
	e.gh = &pjtGH{}
	e.clock = &pjtClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}

	e.tdeps = ProjectDeps{
		Store: db, Roots: []string{e.projects}, Tree: RealProjectTree{}, VCS: RealProjectVCS{},
		Claude:  RealProjectTranscripts{Profiles: []string{filepath.Join(e.home, ".claude")}},
		Servers: e.servers, Actions: e.actions, GitHub: e.gh,
		Now: e.clock.Now, Loc: time.UTC, Home: e.home,
	}
	if mod != nil {
		mod(&e.tdeps)
	}
	e.srv = New(Options{Token: []byte("0123456789abcdef0123456789abcdef"), Now: e.clock.Now})
	e.api = RegisterProject(e.srv, e.tdeps)
	return e
}

// get sends a GET with an allowed Host.
func (e *pjtEnv) get(path string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "localhost:4455"
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// getH is get with extra headers.
func (e *pjtEnv) getH(path string, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "localhost:4455"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// post sends a POST with the token and a JSON body.
func (e *pjtEnv) post(path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "localhost:4455"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(TokenHeader, e.srv.Token())
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// pjtJSON decodes a recorder body into v, failing the test on bad JSON.
func pjtJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("not JSON (%d): %q: %v", rec.Code, rec.Body.String(), err)
	}
}

// pjtWant checks the status and, for an error, the error envelope.
func pjtWant(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %.300s", rec.Code, status, rec.Body.String())
	}
	if status >= 400 {
		var e struct {
			Error struct{ Code, Message string }
		}
		pjtJSON(t, rec, &e)
		if e.Error.Code == "" || e.Error.Message == "" {
			t.Fatalf("error body has no code or message: %s", rec.Body.String())
		}
	}
}

// ---- GET /api/projects/{p} ----

func TestProjectOverview(t *testing.T) {
	e := pjtNew(t, nil)
	rec := e.get("/api/projects/alpha")
	pjtWant(t, rec, 200)
	var got struct {
		Project struct {
			ID           string
			Name, Group  string
			Path         string
			VCS          []string
			Branch       string
			LastChangeAt *string
			Hidden       bool
			Surveyed     bool
			DirtyCount   int
			Stack        []string
			Remote       string
		}
		Overview struct {
			Readme *struct {
				Path, Text string
			}
			Counts     store.ProjCounts
			Dirty      []probe.DirtyFile
			Servers    []pjServer
			Milestones []struct {
				ID         string
				Title      string
				Due        *string
				FeatureIDs []string
				Tasks      int
				Done       int
			}
		}
	}
	pjtJSON(t, rec, &got)
	p := got.Project
	if p.ID != "alpha" || p.Group != "personal" || p.Branch != "main" || len(p.VCS) != 1 || p.VCS[0] != "git" ||
		!p.Surveyed || p.LastChangeAt == nil || p.Hidden || p.Remote == "" || len(p.Stack) != 1 {
		t.Errorf("project = %+v", p)
	}
	if p.DirtyCount != len(got.Overview.Dirty) || p.DirtyCount < 2 {
		t.Errorf("dirtyCount = %d, dirty list = %d", p.DirtyCount, len(got.Overview.Dirty))
	}
	ov := got.Overview
	if ov.Readme == nil || ov.Readme.Path != "README.md" || !strings.Contains(ov.Readme.Text, "A fixture project.") {
		t.Errorf("readme = %+v", ov.Readme)
	}
	c := ov.Counts
	if c.Commits != 2 || c.Sessions != 2 || c.OpenTasks != 1 || c.OpenNotes != 2 || c.Files < 8 || c.Dirty != p.DirtyCount {
		t.Errorf("counts = %+v", c)
	}
	states := map[string]string{}
	for _, d := range ov.Dirty {
		states[d.Path] = d.State
	}
	if states["src/main.go"] != "M" || states["new.txt"] != "?" {
		t.Errorf("dirty = %+v", ov.Dirty)
	}
	if len(ov.Servers) != 1 || ov.Servers[0].Port != 3000 || ov.Servers[0].Title != "Alpha dev page" ||
		ov.Servers[0].ProjectID == nil || *ov.Servers[0].ProjectID != "alpha" {
		t.Errorf("servers = %+v", ov.Servers)
	}
	if len(ov.Milestones) != 1 || ov.Milestones[0].ID != "M-1" || len(ov.Milestones[0].FeatureIDs) != 1 ||
		ov.Milestones[0].Due == nil || ov.Milestones[0].Tasks != 1 {
		t.Errorf("milestones = %+v", ov.Milestones)
	}

	// A project with nothing: empty lists, no README, no nulls where arrays are promised.
	rec = e.get("/api/projects/beta")
	pjtWant(t, rec, 200)
	body := rec.Body.String()
	for _, want := range []string{`"readme":null`, `"dirty":[]`, `"servers":[]`, `"milestones":[]`, `"vcs":[]`, `"surveyed":false`, `"lastChangeAt":null`} {
		if !strings.Contains(body, want) {
			t.Errorf("beta overview lacks %s: %s", want, body)
		}
	}
}

func TestProjectUnknownIdIs404ForEveryRoute(t *testing.T) {
	e := pjtNew(t, nil)
	for _, p := range []string{
		"/api/projects/ghost", "/api/projects/ghost/activity", "/api/projects/ghost/tree", "/api/projects/ghost/file?path=a",
		"/api/projects/ghost/raw?path=a", "/api/projects/ghost/commits", "/api/projects/ghost/commits/abcdef1",
		"/api/projects/ghost/dirty", "/api/projects/ghost/sessions", "/api/projects/ghost/docs", "/api/projects/ghost/github",
		"/api/servers?project=ghost",
	} {
		pjtWant(t, e.get(p), 404)
	}
	// An id is never a path: an encoded traversal is just an unknown id.
	pjtWant(t, e.get("/api/projects/..%2F..%2Fetc/tree"), 404)
	pjtWant(t, e.get("/api/projects/%2Fetc/file?path=passwd"), 404)
}

func TestProjectActivity(t *testing.T) {
	e := pjtNew(t, nil)
	// Days are relative to the injected clock, never the wall clock.
	today := e.clock.Now().UTC().Format("2006-01-02")
	yesterday := e.clock.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	first := e.clock.Now().UTC().AddDate(0, 0, -6).Format("2006-01-02")
	err := e.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO daily_activity(project_id, day, files, commits, lines_plus, sessions) VALUES('alpha',?,7,3,40,1)`, yesterday)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Range int
		Days  []struct {
			Day                        string
			Files, Commits, LinesPlus  int
			Sessions, Snapshots, Notes int
			ServerMinutes              int
		}
	}
	rec := e.get("/api/projects/alpha/activity?range=7")
	pjtWant(t, rec, 200)
	pjtJSON(t, rec, &got)
	if got.Range != 7 || len(got.Days) != 7 || got.Days[6].Day != today || got.Days[0].Day != first {
		t.Fatalf("days = %+v", got.Days)
	}
	if d := got.Days[5]; d.Day != yesterday || d.Files != 7 || d.Commits != 3 || d.LinesPlus != 40 || d.Sessions != 1 {
		t.Errorf("%s = %+v", yesterday, d)
	}
	rec = e.get("/api/projects/alpha/activity")
	pjtWant(t, rec, 200)
	pjtJSON(t, rec, &got)
	if got.Range != 30 || len(got.Days) != 30 {
		t.Errorf("default range: %d days", len(got.Days))
	}
	for _, q := range []string{"5", "0", "abc", "-1", "365"} {
		pjtWant(t, e.get("/api/projects/alpha/activity?range="+q), 400)
	}
}

func TestProjectDocs(t *testing.T) {
	e := pjtNew(t, nil)
	rec := e.get("/api/projects/alpha/docs")
	pjtWant(t, rec, 200)
	var got struct {
		Docs []struct {
			Path, Title, MTime string
			Lines              int
		}
	}
	pjtJSON(t, rec, &got)
	by := map[string]string{}
	for _, d := range got.Docs {
		by[d.Path] = d.Title
		if d.MTime == "" {
			t.Errorf("doc %s has no mtime", d.Path)
		}
	}
	if by["README.md"] != "Alpha" || by["docs/guide.md"] != "The Guide" {
		t.Errorf("docs = %+v", got.Docs)
	}
	for p := range by {
		if strings.HasSuffix(p, ".go") || p == ".env" {
			t.Errorf("non-doc listed: %s", p)
		}
	}
	// A project without docs: an empty array, not null.
	if body := e.get("/api/projects/beta/docs").Body.String(); !strings.Contains(body, `"docs":[]`) {
		t.Errorf("beta docs = %s", body)
	}
}

func TestProjectNilDepsDegrade(t *testing.T) {
	e := pjtNew(t, func(d *ProjectDeps) {
		d.Tree, d.VCS, d.Claude, d.Servers, d.Actions, d.GitHub = nil, nil, nil, nil, nil, nil
	})
	pjtWant(t, e.get("/api/projects/alpha"), 200)
	rec := e.get("/api/projects/alpha/tree")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("tree = %s", rec.Body.String())
	}
	pjtWant(t, e.get("/api/projects/alpha/file?path=README.md"), 503)
	pjtWant(t, e.get("/api/projects/alpha/commits/"+e.commits[0][:8]), 404)
	pjtWant(t, e.get("/api/sessions/"+pjtSessionID), 503)
	rec = e.get("/api/servers")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"active":[]`) {
		t.Errorf("servers = %s", rec.Body.String())
	}
	pjtWant(t, e.post("/api/servers/kill", `{"pid":4242,"confirm":true}`), 503)
	pjtWant(t, e.post("/api/servers/restart", `{"key":"k","confirm":true}`), 503)
	rec = e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "unavailable") {
		t.Errorf("github = %s", rec.Body.String())
	}
	// Commits come from the cache even without a vcs reader.
	rec = e.get("/api/projects/alpha/commits")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "second commit") {
		t.Errorf("cached commits = %s", rec.Body.String())
	}
}

func TestProjectRootsGuard(t *testing.T) {
	e := pjtNew(t, nil)
	// A project row that points outside the allowed roots is refused, never read.
	if err := e.db.UpsertProject(context.Background(), store.Project{ID: "rogue", Name: "rogue", Group: "x", Path: e.outside}); err != nil {
		t.Fatal(err)
	}
	pjtWant(t, e.get("/api/projects/rogue/file?path=secret.txt"), 403)
	pjtWant(t, e.get("/api/projects/rogue/tree"), 403)
	pjtWant(t, e.get("/api/projects/rogue/raw?path=secret.txt"), 403)
	// The live history routes refuse it too. The cached part of the list and the README do not read the folder.
	pjtWant(t, e.get("/api/projects/rogue/commits?path=a.txt"), 403)
	pjtWant(t, e.get("/api/projects/rogue/commits/abcdef1"), 403)
	pjtWant(t, e.get("/api/projects/rogue/commits"), 200)
	rec := e.get("/api/projects/rogue/dirty")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "not available") {
		t.Errorf("rogue dirty = %s", rec.Body.String())
	}
	if !strings.Contains(e.get("/api/projects/rogue").Body.String(), `"readme":null`) {
		t.Error("README of a folder outside the roots was read")
	}
	// A folder that is gone is a 404.
	if err := e.db.UpsertProject(context.Background(), store.Project{ID: "gone", Name: "gone", Group: "x", Path: filepath.Join(e.projects, "x", "gone")}); err != nil {
		t.Fatal(err)
	}
	pjtWant(t, e.get("/api/projects/gone/tree"), 404)
	// The overview does not touch the folder, so it still answers.
	pjtWant(t, e.get("/api/projects/gone"), 200)
}

func TestProjectRoutesAreGuarded(t *testing.T) {
	e := pjtNew(t, nil)
	// Wrong Host: refused before any handler.
	req := httptest.NewRequest(http.MethodGet, "/api/projects/alpha", nil)
	req.Host = "evil.example:4455"
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign host status = %d", rec.Code)
	}
	// Kill and restart without the token or with the wrong method.
	req = httptest.NewRequest(http.MethodPost, "/api/servers/kill", strings.NewReader(`{"pid":4242,"confirm":true}`))
	req.Host = "localhost:4455"
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || len(e.actions.kills) != 0 {
		t.Errorf("no token: status %d, kills %v", rec.Code, e.actions.kills)
	}
	pjtWant(t, e.get("/api/servers/kill"), 405)
}

func TestProjectConcurrentReads(t *testing.T) {
	e := pjtNew(t, nil)
	var wg sync.WaitGroup
	paths := []string{
		"/api/projects/alpha", "/api/projects/alpha/commits", "/api/projects/alpha/tree?depth=2", "/api/projects/alpha/file?path=README.md",
		"/api/projects/alpha/sessions", "/api/servers", "/api/projects/alpha/github", "/api/projects/alpha/dirty",
		"/api/sessions/" + pjtSessionID, "/api/projects/alpha/activity",
	}
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			rec := e.get(p)
			if rec.Code != 200 {
				t.Errorf("%s -> %d", p, rec.Code)
			}
		}(paths[i%len(paths)])
	}
	wg.Wait()
}
