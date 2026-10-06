package httpapi

// Test helpers for the home-area API tests. Names start with "ah". Nothing here touches the real
// ~/projects, ~/.claude*, ~/.drashta or the network: every path is in t.TempDir().

import (
	"context"
	"database/sql"
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

	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/store"
)

const (
	ahTestToken     = "cafe"
	ahTestTokenSeed = "\xca\xfe"
)

// ahIST is a fixed zone with the +05:30 offset, so the tests do not depend on the machine's zone.
var ahIST = time.FixedZone("IST", 5*3600+1800)

// ahClock is the fixed clock used for the "now" field of /boot.
var ahClock = time.Date(2026, 9, 30, 14, 3, 22, 0, ahIST)

// ahFakeIndexer records Refresh calls.
type ahFakeIndexer struct {
	mu      sync.Mutex
	calls   []string      // "all" or "project:<id>:gh=<bool>"
	called  chan string   // every call is also sent here
	block   chan struct{} // when set, calls wait for it to be closed
	panicOn string        // project id that makes Refresh panic
}

func ahNewIndexer() *ahFakeIndexer { return &ahFakeIndexer{called: make(chan string, 16)} }

func (f *ahFakeIndexer) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	block := f.block
	f.mu.Unlock()
	f.called <- s
	if block != nil {
		<-block
	}
}

func (f *ahFakeIndexer) Refresh(ctx context.Context, projectID string, withGH bool) {
	gh := "false"
	if withGH {
		gh = "true"
	}
	f.record("project:" + projectID + ":gh=" + gh)
	if f.panicOn == projectID {
		panic("fake indexer panic")
	}
}

func (f *ahFakeIndexer) RefreshAll(ctx context.Context) { f.record("all") }

func (f *ahFakeIndexer) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

// ahWait waits for the next recorded call.
func (f *ahFakeIndexer) wait(t *testing.T) string {
	t.Helper()
	select {
	case s := <-f.called:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("the indexer was not called")
		return ""
	}
}

// ahNoCall checks that nothing else is called for a short time.
func (f *ahFakeIndexer) noCall(t *testing.T) {
	t.Helper()
	select {
	case s := <-f.called:
		t.Fatalf("unexpected indexer call %q", s)
	case <-time.After(150 * time.Millisecond):
	}
}

// ahFakeNotifier records published events.
type ahFakeNotifier struct {
	mu     sync.Mutex
	events []live.Event
}

func (n *ahFakeNotifier) Publish(ev live.Event) uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, ev)
	return uint64(len(n.events))
}

func (n *ahFakeNotifier) snapshot() []live.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]live.Event{}, n.events...)
}

// ahFakeResolver answers titles from a map and counts the calls.
type ahFakeResolver struct {
	mu     sync.Mutex
	titles map[string][2]string
	calls  map[string]int
}

func (r *ahFakeResolver) Title(ctx context.Context, ref string) (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	r.calls[ref]++
	t := r.titles[ref]
	return t[0], t[1]
}

// ahEnv is one test server with a temp store and a temp config.
type ahEnv struct {
	t        *testing.T
	s        *Server
	db       *store.DB
	cfg      *config.Config
	cfgPath  string
	dir      string // data dir (index.db, settings.json, config.toml)
	idx      *ahFakeIndexer
	note     *ahFakeNotifier
	resolver *ahFakeResolver
}

// ahNew builds the server. mod may change the deps before RegisterHome runs.
// The config file starts with hidden = ["beta"] and no other change from the defaults.
func ahNew(t *testing.T, mod func(*HomeDeps)) *ahEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfgPath := filepath.Join(dir, "config.toml")
	text := "# test config\nprojects_dir = \"" + filepath.Join(dir, "projects") + "\"\nroots = [\"personal\", \"tools\"]\n" +
		"hidden = [\"beta\"]\neditor = [\"code\", \"-g\", \"{path}:{line}\"]\nclaude_profiles = []\n"
	if err := os.WriteFile(cfgPath, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	e := &ahEnv{t: t, db: db, cfg: cfg, cfgPath: cfgPath, dir: dir, idx: ahNewIndexer(), note: &ahFakeNotifier{},
		resolver: &ahFakeResolver{titles: map[string][2]string{}}}
	s := New(Options{Token: []byte(ahTestTokenSeed), Now: func() time.Time { return ahClock },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	e.s = s
	deps := HomeDeps{
		Store: db, Config: NewHomeConfig(cfg, cfgPath), Indexer: e.idx, Notifier: e.note, Resolver: e.resolver,
		Loc: ahIST, Profiles: []string{"/h/.claude", "/h/.claude-work/", "/other/.claude"},
		DefaultAuthor: func(context.Context) string { return "gitname" },
	}
	if mod != nil {
		mod(&deps)
	}
	RegisterHome(s, deps)
	return e
}

// ahProject inserts a project. path may be "" (then /p/<id>).
func (e *ahEnv) project(id, group, vcs, branch string, hidden, indexed bool, path string) {
	e.t.Helper()
	if path == "" {
		path = "/p/" + id
	}
	p := store.Project{ID: id, Name: id, Group: group, Path: path, VCS: vcs, Branch: branch, Hidden: hidden,
		Description: "about " + id, Stack: []string{"Go 1.26"}, LastChangeAt: ahClock.Add(-time.Hour)}
	if indexed {
		p.IndexedAt = ahClock
	}
	if err := e.db.UpsertProject(context.Background(), p); err != nil {
		e.t.Fatal(err)
	}
}

func (e *ahEnv) event(project, kind, ref, label string, ts time.Time, meta string) {
	e.t.Helper()
	ev := store.Event{ProjectID: project, TS: ts, Day: store.DayOf(ts, ahIST), Kind: kind, Ref: ref, Label: label}
	if meta != "" {
		ev.Meta = json.RawMessage(meta)
	}
	if _, err := e.db.AddEvent(context.Background(), ev); err != nil {
		e.t.Fatal(err)
	}
}

func (e *ahEnv) commit(project, id, msg, body string, ts time.Time) {
	e.t.Helper()
	if err := e.db.ReplaceCommits(context.Background(), project, []probe.Commit{{ID: id, VCS: "git", Time: ts, Msg: msg, Body: body, Plus: 3, Minus: 1}}); err != nil {
		e.t.Fatal(err)
	}
}

// do sends a request. Mutating methods get the token and a JSON content type unless token is false.
func (e *ahEnv) do(method, path, body string, token bool) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "localhost:4455"
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		if token {
			req.Header.Set(TokenHeader, ahTestToken)
		}
	}
	rec := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(rec, req)
	return rec
}

func (e *ahEnv) get(path string) *httptest.ResponseRecorder {
	return e.do(http.MethodGet, path, "", false)
}

// decode reads the body as JSON into v and fails the test on a bad body or the wrong status.
func ahDecode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, v any) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	if v == nil {
		return
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, rec.Body.String())
	}
}

// ahErrCode checks the {error:{code,message}} reply.
func ahErrCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	var b struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	ahDecode(t, rec, wantStatus, &b)
	if b.Error.Code != wantCode || b.Error.Message == "" {
		t.Fatalf("error = %+v, want code %q", b.Error, wantCode)
	}
}

// ahRaw reads the body as a generic map, for checks on null versus missing.
func ahRaw(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) map[string]any {
	t.Helper()
	var m map[string]any
	ahDecode(t, rec, wantStatus, &m)
	return m
}

// sqlTx is the transaction type the store hands to Write callbacks.
type sqlTx = sql.Tx

func newReq(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "localhost:4455"
	return req
}

func serve(e *ahEnv, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(rec, req)
	return rec
}
