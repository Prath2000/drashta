package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/resolve"
	"github.com/Prath2000/drashta/internal/store"
)

// ---- fixture ----

const trkUUID = "121c3b19-ada3-495e-a518-7fe5acf0a71e"

type trkReindex struct {
	mu    sync.Mutex
	paths []string
}

func (r *trkReindex) ReindexAuthored(project, rel string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, project+":"+rel)
	return nil
}

func (r *trkReindex) has(s string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.paths {
		if p == s {
			return true
		}
	}
	return false
}

type trkFix struct {
	t        *testing.T
	base     string
	projects string
	root     string // project "alpha"
	beta     string // project "beta"
	outside  string // a folder outside every root
	db       *store.DB
	srv      *Server
	rec      *actions.RecorderExecutor
	reindex  *trkReindex
	author   string
	authorMu sync.Mutex
	home     string
}

func trkWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func trkRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var trkPNG = "\x89PNG\r\n\x1a\nfake image bytes"

func newTrkFix(t *testing.T) *trkFix {
	t.Helper()
	base := t.TempDir()
	if real, err := filepath.EvalSymlinks(base); err == nil {
		base = real
	}
	f := &trkFix{t: t, base: base, projects: filepath.Join(base, "projects"), author: "alice",
		rec: &actions.RecorderExecutor{}, reindex: &trkReindex{}, home: filepath.Join(base, "home")}
	f.root = filepath.Join(f.projects, "g", "alpha")
	f.beta = filepath.Join(f.projects, "g", "beta")
	f.outside = filepath.Join(base, "outside")

	// Observed files.
	trkWrite(t, filepath.Join(f.root, "src/x.go"), "package x\n\nfunc Hello() {}\n// line four\n")
	trkWrite(t, filepath.Join(f.root, "README.md"), "# Alpha\n\nA test project.\n")
	trkWrite(t, filepath.Join(f.root, ".env"), "TOKEN=very-secret\nOTHER=x\n")
	trkWrite(t, filepath.Join(f.root, "shots/a.png"), trkPNG)
	trkWrite(t, filepath.Join(f.outside, "secret.png"), trkPNG)
	trkWrite(t, filepath.Join(f.outside, "secret.txt"), "outside\n")
	if err := os.Symlink(filepath.Join(f.outside, "secret.png"), filepath.Join(f.root, "shots/escape.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.outside, "secret.txt"), filepath.Join(f.root, "src/escape.go")); err != nil {
		t.Fatal(err)
	}

	// Authored files.
	d := filepath.Join(f.root, ".drashta")
	trkWrite(t, filepath.Join(d, "milestones/M-1.md"), `---
id: M-1
title: First release
due: 2026-11-15
features: [F-1]
state: open
created: 2026-09-20T09:00:00+05:30
---
Ship it.
`)
	trkWrite(t, filepath.Join(d, "features/F-1.md"), `---
id: F-1
title: Offline sync
milestone: M-1
created: 2026-09-20T09:05:00+05:30
---
Works with no network.
`)
	trkWrite(t, filepath.Join(d, "features/F-2.md"), `---
id: F-2
title: Dark mode
created: 2026-09-20T09:06:00+05:30
---
`)
	trkWrite(t, filepath.Join(d, "tasks/T-1.md"), `---
id: T-1
title: Cache layer
status: in_progress
feature: F-1
assignee: alice
created: 2026-09-21T09:00:00+05:30
created_by: alice
updated: 2026-09-26T17:40:00+05:30
priority: high   # an unknown key with a comment
links:
  - file:src/x.go#L3
unlinks: []
---
Store notes locally.

## Log
- 2026-09-24T11:00+05:30 alice status todo -> in_progress
`)
	trkWrite(t, filepath.Join(d, "tasks/T-2.md"), `---
id: T-2
title: Dark theme pass
status: todo
feature: F-2
created: 2026-09-22T09:00:00+05:30
created_by: alice
links: []
unlinks: []
---
`)
	trkWrite(t, filepath.Join(d, "notes/N-1.md"), `---
id: N-1
ref: task:T-1
author: Claude
created: 2026-09-29T09:11:00+05:30
resolved: false
---
Is the cache bounded?
`)
	trkWrite(t, filepath.Join(f.beta, "README.md"), "# Beta\n")
	trkWrite(t, filepath.Join(f.outside, "gamma", "README.md"), "# Gamma\n")

	// Index.
	db, err := store.Open(filepath.Join(base, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.db = db
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.UpsertProject(ctx, store.Project{ID: "alpha", Name: "alpha", Group: "g", Path: f.root, VCS: "git",
		Remote: "git@github.com:example/alpha.git", Stack: []string{}}))
	must(db.UpsertProject(ctx, store.Project{ID: "beta", Name: "beta", Group: "g", Path: f.beta, VCS: "fossil",
		Remote: "https://gitlab.com/example/beta", Stack: []string{}}))
	// gamma's folder is outside the allowed roots: open must refuse it.
	must(db.UpsertProject(ctx, store.Project{ID: "gamma", Name: "gamma", Group: "g",
		Path: filepath.Join(f.outside, "gamma"), Stack: []string{}}))
	ts := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	must(db.ReplaceCommits(ctx, "alpha", []probe.Commit{
		{ID: "b7dc920bef0123456789abcdef0123456789abcd", VCS: "git", Author: "alice", Time: ts,
			Msg: "T-1: cache layer", Plus: 10, Minus: 2, NFiles: 1, Files: []probe.CommitFile{{Path: "src/x.go", Plus: 10, Minus: 2}}},
		{ID: "aaaa111122223333444455556666777788889999", VCS: "git", Author: "alice", Time: ts.Add(-time.Hour), Msg: "init"},
	}))
	must(db.ReplaceCommits(ctx, "beta", []probe.Commit{
		{ID: "cccc0000dddd1111eeee2222ffff3333aaaa4444bbbb5555cccc6666dddd7777", VCS: "fossil", Author: "alice", Time: ts, Msg: "fossil checkin"},
	}))
	jsonl := filepath.Join(f.home, ".claude-work", "projects", "-x-alpha", trkUUID+".jsonl")
	must(db.UpsertSessions(ctx, []store.Session{{ID: trkUUID, Profile: ".claude-work", JSONLPath: jsonl, CWD: f.root,
		ProjectID: "alpha", Start: ts.Add(-2 * time.Hour), End: ts, Title: "Build the cache", Turns: 12, KB: 40}}))
	must(db.ReplaceServersNow(ctx, []store.ServerNow{{Key: f.root + "|npm run dev", Port: 5173, PID: 42, Name: "node",
		Cmd: "npm run dev", CWD: f.root, ProjectID: "alpha", Kind: "dev", Title: "Alpha dev", FirstSeen: ts, LastSeen: ts}}))
	must(db.UpsertAuthoredTaskLinks(ctx, "alpha", "T-1", "auto", []store.TaskLink{{Ref: "commit:b7dc920bef", Reason: "commit message"}}))
	must(db.ReplaceFiles(ctx, "alpha", []store.File{{Path: "src/x.go", Size: 40, MTime: ts, Kind: "code", Lines: 4},
		{Path: "README.md", Size: 30, MTime: ts, Kind: "doc", Lines: 3, Title: "Alpha"}}))

	res := resolve.New(resolve.Options{DB: db, Home: f.home, Actions: actions.Config{
		Editor: []string{"code", "-g", "{path}:{line}"}, Terminal: "Terminal", Roots: []string{f.projects},
		DesktopScript: filepath.Join(f.root, "desktop", "plash-mode")}})
	f.srv = New(Options{Addr: "127.0.0.1:4455", Token: []byte("tok"), Logger: trkQuietLogger()})
	RegisterTracker(f.srv, TrackerDeps{
		Resolver: res, Reindex: f.reindex, Exec: f.rec, DesktopState: filepath.Join(f.home, "plash-mode.state"),
		Author: func() string { f.authorMu.Lock(); defer f.authorMu.Unlock(); return f.author },
		Now:    func() time.Time { return time.Date(2026, 10, 1, 10, 0, 0, 0, time.FixedZone("IST", 5*3600+1800)) },
	})
	return f
}

func (f *trkFix) setAuthor(a string) {
	f.authorMu.Lock()
	f.author = a
	f.authorMu.Unlock()
}

// do sends a request with the right Host, token and content type.
func (f *trkFix) do(method, target string, body any, hdr ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	var rd io.Reader
	ctype := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case string:
		rd = strings.NewReader(b)
	default:
		js, err := json.Marshal(b)
		if err != nil {
			f.t.Fatal(err)
		}
		rd = bytes.NewReader(js)
	}
	req := httptest.NewRequest(method, target, rd)
	req.Host = "localhost:4455"
	if method != http.MethodGet {
		req.Header.Set(TokenHeader, hex.EncodeToString([]byte("tok")))
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	return w
}

func trkJSON(t *testing.T, w *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad JSON %q: %v", w.Body.String(), err)
	}
	return m
}

func trkCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	m := trkJSON(t, w, status)
	e, _ := m["error"].(map[string]any)
	if e == nil || (code != "" && e["code"] != code) {
		t.Fatalf("want error code %q, got %v", code, m)
	}
}

func trkGet(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

// trackerEvents returns the tracker events published after head.
func (f *trkFix) trackerEvents(head uint64) []live.TrackerData {
	evs, _, _ := f.srv.Hub().Since(head)
	out := []live.TrackerData{}
	for _, e := range evs {
		if e.Type != live.TypeTracker {
			continue
		}
		var d live.TrackerData
		_ = json.Unmarshal(e.Data, &d)
		out = append(out, d)
	}
	return out
}

func (f *trkFix) taskFile(id string) string {
	return filepath.Join(f.root, ".drashta", "tasks", id+".md")
}

// ---- tests: tasks ----

func TestTrkTaskRoundTrip(t *testing.T) {
	f := newTrkFix(t)
	head := f.srv.Hub().Head()

	// Create.
	w := f.do("POST", "/api/projects/alpha/tasks", map[string]any{
		"title": "New offline banner", "featureId": "F-1", "assignee": "alice", "body": "Show a banner.",
		"links": []string{"alpha:commit:b7dc920", "beta:commit:cccc0000dd"},
	})
	m := trkJSON(t, w, http.StatusCreated)
	if trkGet(m, "task", "id") != "T-3" || trkGet(m, "task", "status") != "todo" || trkGet(m, "rev") == "" {
		t.Fatalf("create reply: %v", m)
	}
	if trkGet(m, "task", "milestoneId") != "M-1" {
		t.Fatalf("milestone from feature: %v", m["task"])
	}
	text := trkRead(t, f.taskFile("T-3"))
	for _, want := range []string{"title: New offline banner", "- commit:b7dc920", "- beta:commit:cccc0000dd", "created_by: alice", "alice created"} {
		if !strings.Contains(text, want) {
			t.Fatalf("T-3.md lacks %q:\n%s", want, text)
		}
	}
	if !f.reindex.has("alpha:.drashta/tasks/T-3.md") {
		t.Fatalf("no reindex: %v", f.reindex.paths)
	}
	if evs := f.trackerEvents(head); len(evs) == 0 || evs[0].ID != "T-3" || evs[0].Kind != "task" {
		t.Fatalf("tracker event: %v", evs)
	}

	// Read.
	m = trkJSON(t, f.do("GET", "/api/projects/alpha/tasks/T-3", nil), 200)
	rev := m["rev"].(string)
	links := m["links"].([]any)
	if len(links) != 2 || trkGet(links[0].(map[string]any), "resolved", "title") != "T-1: cache layer" {
		t.Fatalf("links: %v", links)
	}
	if trkGet(links[1].(map[string]any), "resolved", "title") != "fossil checkin" {
		t.Fatalf("cross-project link not resolved: %v", links[1])
	}

	// Patch.
	w = f.do("PATCH", "/api/projects/alpha/tasks/T-3", map[string]any{"status": "in_progress", "title": "Offline banner"},
		"If-Match", `"`+rev+`"`)
	m = trkJSON(t, w, 200)
	if trkGet(m, "task", "status") != "in_progress" || trkGet(m, "task", "title") != "Offline banner" || m["rev"] == rev {
		t.Fatalf("patch reply: %v", m)
	}
	if !strings.Contains(trkRead(t, f.taskFile("T-3")), "alice status todo -> in_progress") {
		t.Fatal("no log line for the status move")
	}
	// featureId null clears the feature.
	w = f.do("PATCH", "/api/projects/alpha/tasks/T-3", map[string]any{"featureId": nil}, "If-Match", m["rev"].(string))
	m = trkJSON(t, w, 200)
	if trkGet(m, "task", "featureId") != nil {
		t.Fatalf("featureId not cleared: %v", m["task"])
	}

	// Delete moves the file to the trash.
	trkJSON(t, f.do("DELETE", "/api/projects/alpha/tasks/T-3", nil), 200)
	if _, err := os.Stat(f.taskFile("T-3")); !os.IsNotExist(err) {
		t.Fatal("task file still there")
	}
	if _, err := os.Stat(filepath.Join(f.root, ".drashta/.trash/tasks/T-3.md")); err != nil {
		t.Fatal("task not in the trash:", err)
	}
	trkCode(t, f.do("GET", "/api/projects/alpha/tasks/T-3", nil), 404, "not_found")
	// The id is never reused.
	m = trkJSON(t, f.do("POST", "/api/projects/alpha/tasks", map[string]any{"title": "Next"}), 201)
	if trkGet(m, "task", "id") != "T-4" {
		t.Fatalf("id reused: %v", m["task"])
	}
}

func TestTrkTaskValidation(t *testing.T) {
	f := newTrkFix(t)
	trkCode(t, f.do("POST", "/api/projects/alpha/tasks", map[string]any{"title": " "}), 422, "unprocessable")
	trkCode(t, f.do("POST", "/api/projects/alpha/tasks", map[string]any{"title": "x", "status": "weird"}), 422, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/tasks", map[string]any{"title": "x", "featureId": "F-9"}), 422, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/tasks", map[string]any{"title": "x", "links": []string{"commit:zz"}}), 422, "")
	trkCode(t, f.do("POST", "/api/projects/nope/tasks", map[string]any{"title": "x"}), 404, "")
	// Traversal and odd ids in the path never reach the disk.
	for _, id := range []string{"..%2F..%2Fetc", "T-1.md", "t-1", "T-1x", "%2E%2E"} {
		w := f.do("GET", "/api/projects/alpha/tasks/"+id, nil)
		if w.Code != 400 && w.Code != 404 {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body.String())
		}
	}
	trkCode(t, f.do("PATCH", "/api/projects/alpha/tasks/..%2FT-1", map[string]any{"title": "x"}, "If-Match", "x"), 400, "")
	// No token: the guard refuses.
	req := httptest.NewRequest("POST", "/api/projects/alpha/tasks", strings.NewReader(`{"title":"x"}`))
	req.Host = "localhost:4455"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("no token: %d", w.Code)
	}
}

func TestTrkPatchConflicts(t *testing.T) {
	f := newTrkFix(t)
	m := trkJSON(t, f.do("GET", "/api/projects/alpha/tasks/T-1", nil), 200)
	rev := m["rev"].(string)

	// No If-Match: 428.
	trkCode(t, f.do("PATCH", "/api/projects/alpha/tasks/T-1", map[string]any{"status": "review"}), 428, "precondition_required")

	// Stale rev: 409 with the current rev.
	w := f.do("PATCH", "/api/projects/alpha/tasks/T-1", map[string]any{"status": "review"}, "If-Match", "deadbeef")
	m = trkJSON(t, w, 409)
	if trkGet(m, "error", "code") != "conflict" || m["rev"] != rev {
		t.Fatalf("stale rev reply: %v", m)
	}

	// An edit on disk after the read (for example by Claude): 409, and the file keeps the edit.
	path := f.taskFile("T-1")
	edited := strings.Replace(trkRead(t, path), "assignee: alice", "assignee: Claude", 1)
	trkWrite(t, path, edited)
	w = f.do("PATCH", "/api/projects/alpha/tasks/T-1", map[string]any{"status": "review"}, "If-Match", rev)
	m = trkJSON(t, w, 409)
	if m["rev"] == rev || trkGet(m, "current", "assignee") != "Claude" {
		t.Fatalf("disk edit reply: %v", m)
	}
	if trkRead(t, path) != edited {
		t.Fatal("the file changed after a conflict")
	}

	// With the fresh rev it works, and the unknown key with its comment survives.
	w = f.do("PATCH", "/api/projects/alpha/tasks/T-1", map[string]any{"status": "review"}, "If-Match", m["rev"].(string))
	trkJSON(t, w, 200)
	text := trkRead(t, path)
	if !strings.Contains(text, "priority: high   # an unknown key with a comment") || !strings.Contains(text, "status: review") {
		t.Fatalf("unknown key lost or status not set:\n%s", text)
	}
	if !strings.Contains(text, "- 2026-09-24T11:00+05:30 alice status todo -> in_progress") {
		t.Fatalf("old log line changed:\n%s", text)
	}
}

func TestTrkTrackerList(t *testing.T) {
	f := newTrkFix(t)
	m := trkJSON(t, f.do("GET", "/api/tracker?project=alpha", nil), 200)
	tasks := m["tasks"].([]any)
	if len(tasks) != 2 || len(m["features"].([]any)) != 2 || len(m["milestones"].([]any)) != 1 {
		t.Fatalf("tracker: %v", m)
	}
	t1 := tasks[0].(map[string]any)
	// 1 manual + 1 auto link; 1 open note.
	if t1["id"] != "T-1" || t1["linkCount"] != 2.0 || t1["openNotes"] != 1.0 || t1["milestoneId"] != "M-1" {
		t.Fatalf("T-1 view: %v", t1)
	}
	if ms := m["milestones"].([]any)[0].(map[string]any); ms["due"] != "2026-11-15" {
		t.Fatalf("milestone: %v", ms)
	}

	m = trkJSON(t, f.do("GET", "/api/tracker?project=alpha&status=todo", nil), 200)
	if ts := m["tasks"].([]any); len(ts) != 1 || ts[0].(map[string]any)["id"] != "T-2" {
		t.Fatalf("status filter: %v", ts)
	}
	m = trkJSON(t, f.do("GET", "/api/tracker?feature=F-1", nil), 200)
	if ts := m["tasks"].([]any); len(ts) != 1 || ts[0].(map[string]any)["id"] != "T-1" {
		t.Fatalf("feature filter: %v", ts)
	}
	m = trkJSON(t, f.do("GET", "/api/tracker?filter=review", nil), 200)
	if ts := m["tasks"].([]any); len(ts) != 0 {
		t.Fatalf("review filter before a snapshot: %v", ts)
	}
	trkCode(t, f.do("GET", "/api/tracker?filter=nope", nil), 400, "")
	trkCode(t, f.do("GET", "/api/tracker?project=nope", nil), 404, "")
}

func TestTrkLinks(t *testing.T) {
	f := newTrkFix(t)
	base := "/api/projects/alpha/tasks/T-1/links"

	// Add a manual link (a server, cross-project form of the same project).
	m := trkJSON(t, f.do("POST", base, map[string]any{"ref": "alpha:server:5173"}), 200)
	if len(m["links"].([]any)) != 3 {
		t.Fatalf("links after add: %v", m["links"])
	}
	if !strings.Contains(trkRead(t, f.taskFile("T-1")), "- server:5173") {
		t.Fatal("manual link not stored in local form")
	}
	// Adding it again changes nothing.
	m = trkJSON(t, f.do("POST", base, map[string]any{"ref": "server:5173"}), 200)
	if len(m["links"].([]any)) != 3 {
		t.Fatalf("duplicate link: %v", m["links"])
	}
	trkCode(t, f.do("POST", base, map[string]any{"ref": "commit:not-hex"}), 422, "")
	trkCode(t, f.do("POST", base, map[string]any{"ref": "task:T-1"}), 422, "")

	// Remove the manual one: gone from links.
	m = trkJSON(t, f.do("DELETE", base+"?ref=server:5173", nil), 200)
	if len(m["links"].([]any)) != 2 || strings.Contains(trkRead(t, f.taskFile("T-1")), "- server:5173") {
		t.Fatalf("manual link not removed: %v", m["links"])
	}
	// Remove the auto one: it goes to unlinks and leaves the list.
	m = trkJSON(t, f.do("DELETE", base+"?ref=commit:b7dc920bef", nil), 200)
	links := m["links"].([]any)
	if len(links) != 1 || links[0].(map[string]any)["source"] != "manual" {
		t.Fatalf("auto link still shown: %v", links)
	}
	if !strings.Contains(trkRead(t, f.taskFile("T-1")), "- commit:b7dc920bef") {
		t.Fatalf("not in unlinks:\n%s", trkRead(t, f.taskFile("T-1")))
	}
	// The resolver no longer lists T-1 for that commit.
	r := trkJSON(t, f.do("GET", "/api/resolve?ref=alpha:commit:b7dc920", nil), 200)
	if len(r["tasks"].([]any)) != 0 {
		t.Fatalf("unlinked task still listed: %v", r["tasks"])
	}
	// Unknown link: 404.
	trkCode(t, f.do("DELETE", base+"?ref=commit:aaaa111122", nil), 404, "")
	trkCode(t, f.do("DELETE", base, nil), 400, "")
	// Linking it again by hand brings it back and clears unlinks.
	m = trkJSON(t, f.do("POST", base, map[string]any{"ref": "commit:b7dc920bef"}), 200)
	if len(m["links"].([]any)) != 2 {
		t.Fatalf("relink: %v", m["links"])
	}
	r = trkJSON(t, f.do("GET", "/api/resolve?ref=alpha:commit:b7dc920", nil), 200)
	if len(r["tasks"].([]any)) != 1 {
		t.Fatalf("relinked task not listed: %v", r["tasks"])
	}
}

func TestTrkFeaturesMilestones(t *testing.T) {
	f := newTrkFix(t)
	m := trkJSON(t, f.do("POST", "/api/projects/alpha/milestones", map[string]any{"title": "Second", "due": "2026-12-01"}), 201)
	if trkGet(m, "milestone", "id") != "M-2" {
		t.Fatalf("milestone: %v", m)
	}
	m = trkJSON(t, f.do("POST", "/api/projects/alpha/features", map[string]any{"title": "Sync log", "summary": "A log.", "milestoneId": "M-2"}), 201)
	if trkGet(m, "feature", "id") != "F-3" || trkGet(m, "feature", "milestoneId") != "M-2" || trkGet(m, "feature", "summary") != "A log." {
		t.Fatalf("feature: %v", m)
	}
	ms := trkRead(t, filepath.Join(f.root, ".drashta/milestones/M-2.md"))
	if !strings.Contains(ms, "F-3") {
		t.Fatalf("milestone does not list the feature:\n%s", ms)
	}
	// Move F-3 to M-1: M-2 drops it, M-1 lists it.
	rev := m["rev"].(string)
	m = trkJSON(t, f.do("PATCH", "/api/projects/alpha/features/F-3", map[string]any{"milestoneId": "M-1"}, "If-Match", rev), 200)
	if trkGet(m, "feature", "milestoneId") != "M-1" {
		t.Fatalf("feature move: %v", m)
	}
	if strings.Contains(trkRead(t, filepath.Join(f.root, ".drashta/milestones/M-2.md")), "F-3") ||
		!strings.Contains(trkRead(t, filepath.Join(f.root, ".drashta/milestones/M-1.md")), "F-3") {
		t.Fatal("milestone lists not updated")
	}
	trkCode(t, f.do("PATCH", "/api/projects/alpha/features/F-3", map[string]any{"title": "x"}), 428, "")
	trkCode(t, f.do("PATCH", "/api/projects/alpha/features/F-3", map[string]any{"title": "x"}, "If-Match", rev), 409, "conflict")

	m = trkJSON(t, f.do("GET", "/api/tracker?project=alpha", nil), 200)
	var mrev string
	for _, x := range m["milestones"].([]any) {
		if x.(map[string]any)["id"] == "M-2" {
			mrev = x.(map[string]any)["rev"].(string)
		}
	}
	m = trkJSON(t, f.do("PATCH", "/api/projects/alpha/milestones/M-2", map[string]any{"state": "closed", "featureIds": []string{"F-2"}}, "If-Match", mrev), 200)
	if trkGet(m, "milestone", "state") != "closed" {
		t.Fatalf("milestone patch: %v", m)
	}
	trkCode(t, f.do("PATCH", "/api/projects/alpha/milestones/M-2", map[string]any{"featureIds": []string{"F-99"}}, "If-Match", m["rev"].(string)), 422, "")
	trkJSON(t, f.do("DELETE", "/api/projects/alpha/features/F-3", nil), 200)
	trkJSON(t, f.do("DELETE", "/api/projects/alpha/milestones/M-2", nil), 200)
	trkCode(t, f.do("DELETE", "/api/projects/alpha/milestones/M-2", nil), 404, "")
	if _, err := os.Stat(filepath.Join(f.root, ".drashta/.trash/features/F-3.md")); err != nil {
		t.Fatal(err)
	}
}

func trkQuietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Details the Tracker screen needs (lead request).
func TestTrkScreenContract(t *testing.T) {
	f := newTrkFix(t)

	// (1) every task in the list carries the same rev as its detail.
	m := trkJSON(t, f.do("GET", "/api/tracker?project=alpha", nil), 200)
	for _, x := range m["tasks"].([]any) {
		task := x.(map[string]any)
		rev, _ := task["rev"].(string)
		d := trkJSON(t, f.do("GET", "/api/projects/alpha/tasks/"+task["id"].(string), nil), 200)
		if rev == "" || d["rev"] != rev {
			t.Fatalf("task %v: list rev %q, detail rev %v", task["id"], rev, d["rev"])
		}
	}

	// (2) verdict reply: task + snapshot, and suggestion on approve.
	trkJSON(t, f.upload("T-1", "a.png", []byte(trkPNG), nil), 201)
	m = trkJSON(t, f.do("PUT", "/api/projects/alpha/tasks/T-1/snapshots/1/verdict", map[string]any{"verdict": "approved"}), 200)
	if trkGet(m, "task", "id") != "T-1" || trkGet(m, "task", "rev") == "" || trkGet(m, "snapshot", "version") != 1.0 ||
		trkGet(m, "snapshot", "verdict") != "approved" || trkGet(m, "suggestion", "status") != "done" {
		t.Fatalf("verdict reply: %v", m)
	}

	// (3) feature create with milestoneId lands in the milestone's featureIds.
	m = trkJSON(t, f.do("POST", "/api/projects/alpha/features", map[string]any{"title": "Sync log", "milestoneId": "M-1"}), 201)
	if trkGet(m, "feature", "milestoneId") != "M-1" {
		t.Fatalf("feature: %v", m)
	}
	ms := func(id string) map[string]any {
		m := trkJSON(t, f.do("GET", "/api/tracker?project=alpha", nil), 200)
		for _, x := range m["milestones"].([]any) {
			if x.(map[string]any)["id"] == id {
				return x.(map[string]any)
			}
		}
		t.Fatalf("no milestone %s", id)
		return nil
	}
	if ids := ms("M-1")["featureIds"].([]any); len(ids) != 2 || ids[1] != "F-3" {
		t.Fatalf("M-1 featureIds: %v", ids)
	}
	// A milestone created with featureIds takes F-1 from M-1; the feature file follows.
	trkJSON(t, f.do("POST", "/api/projects/alpha/milestones", map[string]any{"title": "Two", "featureIds": []string{"F-1"}}), 201)
	if ids := ms("M-1")["featureIds"].([]any); len(ids) != 1 || ids[0] != "F-3" {
		t.Fatalf("M-1 still lists F-1: %v", ids)
	}
	if !strings.Contains(trkRead(t, filepath.Join(f.root, ".drashta/features/F-1.md")), "milestone: M-2") {
		t.Fatal("F-1 file not moved to M-2")
	}
	// Patching M-2 to drop F-1 clears F-1's milestone.
	m2 := ms("M-2")
	trkJSON(t, f.do("PATCH", "/api/projects/alpha/milestones/M-2", map[string]any{"featureIds": []string{"F-2"}}, "If-Match", m2["rev"].(string)), 200)
	m = trkJSON(t, f.do("GET", "/api/tracker?project=alpha", nil), 200)
	got := map[string]any{}
	for _, x := range m["features"].([]any) {
		got[x.(map[string]any)["id"].(string)] = x.(map[string]any)["milestoneId"]
	}
	if got["F-1"] != nil || got["F-2"] != "M-2" || got["F-3"] != "M-1" {
		t.Fatalf("feature milestones: %v", got)
	}
}
