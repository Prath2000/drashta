package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/probe/servers"
)

type pjtServersReply struct {
	Active, Recent []pjServer
}

func TestServersList(t *testing.T) {
	e := pjtNew(t, nil)
	rec := e.get("/api/servers")
	pjtWant(t, rec, 200)
	var r pjtServersReply
	pjtJSON(t, rec, &r)
	if len(r.Active) != 2 || len(r.Recent) != 1 {
		t.Fatalf("servers = %+v", r)
	}
	dev := r.Active[0]
	if dev.Port != 3000 || dev.PID != 4242 || dev.Name != "node" || dev.Cmd != "node server.js" || dev.CWD != e.alpha ||
		dev.ProjectID == nil || *dev.ProjectID != "alpha" || dev.Kind != "dev" || dev.Title != "Alpha dev page" ||
		dev.Key != e.alpha+"|node server.js" || !dev.Alive || dev.FirstSeen == "" || dev.LastSeen == "" {
		t.Errorf("dev server = %+v", dev)
	}
	// A helper outside every project: projectId is null.
	if h := r.Active[1]; h.ProjectID != nil || h.Kind != "helper" {
		t.Errorf("helper = %+v", h)
	}
	if !strings.Contains(rec.Body.String(), `"projectId":null`) {
		t.Errorf("a server outside every project must have projectId null: %s", rec.Body.String())
	}
	// Recent: dead, no title borrowed from a live server that reuses the port.
	if old := r.Recent[0]; old.Port != 5173 || old.Alive || old.Title != "" || old.LastSeen == "" {
		t.Errorf("recent = %+v", old)
	}

	// Filter by project.
	rec = e.get("/api/servers?project=alpha")
	pjtWant(t, rec, 200)
	pjtJSON(t, rec, &r)
	if len(r.Active) != 1 || r.Active[0].Port != 3000 || len(r.Recent) != 1 {
		t.Errorf("alpha servers = %+v", r)
	}
	rec = e.get("/api/servers?project=beta")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"active":[]`) || !strings.Contains(rec.Body.String(), `"recent":[]`) {
		t.Errorf("beta servers = %s", rec.Body.String())
	}
	pjtWant(t, e.get("/api/servers?project=ghost"), 404)
}

func TestServersKill(t *testing.T) {
	e := pjtNew(t, nil)
	key := e.alpha + "|node server.js"

	// Without confirm nothing runs.
	for _, body := range []string{
		`{"pid":4242,"key":"` + key + `"}`,
		`{"pid":4242,"key":"` + key + `","confirm":false}`,
	} {
		rec := e.post("/api/servers/kill", body)
		pjtWant(t, rec, 400)
		if !strings.Contains(rec.Body.String(), "confirm") {
			t.Errorf("message does not say why: %s", rec.Body.String())
		}
	}
	if len(e.actions.kills) != 0 {
		t.Fatalf("kill ran without confirm: %v", e.actions.kills)
	}
	// Bad bodies.
	pjtWant(t, e.post("/api/servers/kill", `{"confirm":true}`), 400)         // no pid
	pjtWant(t, e.post("/api/servers/kill", `{"pid":1,"confirm":true}`), 400) // never pid 1
	pjtWant(t, e.post("/api/servers/kill", `{"pid":-5,"confirm":true}`), 400)
	pjtWant(t, e.post("/api/servers/kill", `not json`), 400)
	pjtWant(t, e.post("/api/servers/kill", `{"pid":"4242","confirm":true}`), 400)

	rec := e.post("/api/servers/kill", `{"pid":4242,"key":"`+strings.ReplaceAll(key, `"`, `\"`)+`","confirm":true}`)
	pjtWant(t, rec, 200)
	if strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Errorf("body = %s", rec.Body.String())
	}
	if len(e.actions.kills) != 1 || e.actions.kills[0] != (pjtKill{4242, key, true}) {
		t.Errorf("kills = %+v", e.actions.kills)
	}

	// Errors from the manager.
	e.actions.killErr = fmt.Errorf("%w: pid 9", actions.ErrNotLive)
	pjtWant(t, e.post("/api/servers/kill", `{"pid":9,"confirm":true}`), 400)
	e.actions.killErr = errors.New("operation not permitted: /secret/place")
	rec = e.post("/api/servers/kill", `{"pid":9,"confirm":true}`)
	pjtWant(t, rec, 500)
	if strings.Contains(rec.Body.String(), "secret") {
		t.Error("error detail leaked")
	}
}

func TestServersKillOnlyLivePidsWithRealManager(t *testing.T) {
	// The real Manager refuses a PID that is not in the live set, so nothing is signalled.
	m := actions.NewManager(t.TempDir(), func(string) (servers.Server, bool) { return servers.Server{}, false })
	e := pjtNew(t, func(d *ProjectDeps) { d.Actions = m })
	pjtWant(t, e.post("/api/servers/kill", `{"pid":4242,"confirm":true}`), 400)
	pjtWant(t, e.post("/api/servers/kill", `{"pid":99999999,"key":"x","confirm":true}`), 400)
	pjtWant(t, e.post("/api/servers/restart", `{"key":"never recorded","confirm":true}`), 404)
}

func TestServersRestart(t *testing.T) {
	e := pjtNew(t, nil)
	pjtWant(t, e.post("/api/servers/restart", `{"key":"k"}`), 400)
	pjtWant(t, e.post("/api/servers/restart", `{"key":"k","confirm":false}`), 400)
	pjtWant(t, e.post("/api/servers/restart", `{"confirm":true}`), 400) // no key
	if len(e.actions.restarts) != 0 {
		t.Fatalf("restart ran without confirm or key: %v", e.actions.restarts)
	}

	rec := e.post("/api/servers/restart", `{"key":"dir|vite","confirm":true}`)
	pjtWant(t, rec, 200)
	var r struct {
		OK  bool
		Log string
	}
	pjtJSON(t, rec, &r)
	if !r.OK || r.Log != "~/.drashta/logs/5173.log" {
		t.Errorf("restart = %+v (the home folder should show as ~)", r)
	}
	if len(e.actions.restarts) != 1 || e.actions.restarts[0] != "dir|vite" {
		t.Errorf("restarts = %v", e.actions.restarts)
	}

	// A restart that is already pending (or a server that is alive) is a 409; an unknown key is a 404.
	e.actions.restartErr = actions.ErrConflict
	rec = e.post("/api/servers/restart", `{"key":"dir|vite","confirm":true}`)
	pjtWant(t, rec, 409)
	var c struct{ Error struct{ Code string } }
	pjtJSON(t, rec, &c)
	if c.Error.Code != "conflict" {
		t.Errorf("code = %q", c.Error.Code)
	}
	e.actions.restartErr = fmt.Errorf("%w: unknown server", actions.ErrNotFound)
	pjtWant(t, e.post("/api/servers/restart", `{"key":"nope","confirm":true}`), 404)
	e.actions.restartErr = actions.ErrConfirm
	pjtWant(t, e.post("/api/servers/restart", `{"key":"k","confirm":true}`), 400)
	e.actions.restartErr = errors.New("sh: exec failed in /private/dir")
	rec = e.post("/api/servers/restart", `{"key":"k","confirm":true}`)
	pjtWant(t, rec, 500)
	if strings.Contains(rec.Body.String(), "private") {
		t.Error("error detail leaked")
	}
}

func TestServersTitleFallsBackToPortOnlyForLiveServers(t *testing.T) {
	e := pjtNew(t, nil)
	e.servers.mu.Lock()
	// The same app restarted with another cmd: the key changed, the port did not.
	e.servers.active[0].Cmd = "node server.js --watch"
	e.servers.active[0].LastSeen = time.Now()
	e.servers.mu.Unlock()
	var r pjtServersReply
	pjtJSON(t, e.get("/api/servers"), &r)
	if r.Active[0].Title != "Alpha dev page" {
		t.Errorf("a live server should keep the title of its port: %+v", r.Active[0])
	}
}

func TestServersEmbed(t *testing.T) {
	type call struct {
		port   int
		parent string
	}
	var calls []call
	info := actions.PageInfo{HTTP: true, Title: "Alpha dev page", Frameable: false, Reason: "X-Frame-Options: DENY"}
	e := pjtNew(t, func(d *ProjectDeps) {
		d.ProbePage = func(port int, parent string) actions.PageInfo {
			calls = append(calls, call{port, parent})
			return info
		}
	})

	// A live port: 200 with the exact JSON contract, probed against http://<Host>.
	rec := e.get("/api/servers/embed?port=3000")
	pjtWant(t, rec, 200)
	if got, want := strings.TrimSpace(rec.Body.String()),
		`{"http":true,"frameable":false,"reason":"X-Frame-Options: DENY","title":"Alpha dev page"}`; got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
	if len(calls) != 1 || calls[0] != (call{3000, "http://localhost:4455"}) {
		t.Errorf("probe calls = %+v", calls)
	}

	// A frameable page and a non-HTTP listener keep every key in the reply.
	info = actions.PageInfo{HTTP: true, Frameable: true}
	rec = e.get("/api/servers/embed?port=9000")
	pjtWant(t, rec, 200)
	if got, want := strings.TrimSpace(rec.Body.String()), `{"http":true,"frameable":true,"reason":"","title":""}`; got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
	info = actions.PageInfo{Reason: "not a web page"}
	rec = e.get("/api/servers/embed?port=3000")
	pjtWant(t, rec, 200)
	if got, want := strings.TrimSpace(rec.Body.String()), `{"http":false,"frameable":false,"reason":"not a web page","title":""}`; got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}

	// The parent origin follows the Host of the request.
	calls = nil
	req := httptest.NewRequest(http.MethodGet, "/api/servers/embed?port=3000", nil)
	req.Host = "127.0.0.1:4455"
	rec = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	pjtWant(t, rec, 200)
	if len(calls) != 1 || calls[0].parent != "http://127.0.0.1:4455" {
		t.Errorf("probe calls = %+v", calls)
	}

	// Ports that are not in the live list are never probed: a stopped server (5173, only in
	// "recent"), and a port nobody runs.
	calls = nil
	for _, port := range []string{"5173", "22", "65535"} {
		rec = e.get("/api/servers/embed?port=" + port)
		pjtWant(t, rec, 404)
		var errBody struct {
			Error struct{ Code, Message string }
		}
		pjtJSON(t, rec, &errBody)
		if errBody.Error.Code != "not_found" || errBody.Error.Message == "" {
			t.Errorf("port %s: error body = %s", port, rec.Body.String())
		}
	}
	// A port that the scan stopped reporting is a 404 too.
	e.servers.mu.Lock()
	e.servers.active = nil
	e.servers.mu.Unlock()
	pjtWant(t, e.get("/api/servers/embed?port=3000"), 404)
	if len(calls) != 0 {
		t.Errorf("probed a port that is not live: %+v", calls)
	}

	// Bad ports are a 400 and are never probed.
	for _, q := range []string{"", "?port=", "?port=abc", "?port=0", "?port=-1", "?port=65536", "?port=3000x", "?port=1.5", "?port=%20"} {
		pjtWant(t, e.get("/api/servers/embed"+q), 400)
	}
	if len(calls) != 0 {
		t.Errorf("probed a bad port: %+v", calls)
	}
}

func TestServersEmbedWithoutDeps(t *testing.T) {
	// No server list: every port is unknown (404), never a crash.
	e := pjtNew(t, func(d *ProjectDeps) { d.Servers = nil })
	pjtWant(t, e.get("/api/servers/embed?port=3000"), 404)
}

func TestServersEmbedRealProbe(t *testing.T) {
	// With the default probe (actions.ProbePage): a real loopback server that sets X-Frame-Options,
	// listed as live. The Host the request carries becomes the parent origin.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "frame-ancestors http://localhost:4455")
		fmt.Fprint(w, "<title>Real page</title>")
	}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	e := pjtNew(t, nil)
	e.servers.mu.Lock()
	e.servers.active = append(e.servers.active, servers.Server{Port: port, PID: 99, Name: "node", Cmd: "x", Cwd: e.alpha,
		ProjectID: "alpha", Kind: servers.KindDev, Alive: true})
	e.servers.mu.Unlock()
	var r pjEmbed
	rec := e.get(fmt.Sprintf("/api/servers/embed?port=%d", port))
	pjtWant(t, rec, 200)
	pjtJSON(t, rec, &r)
	if !r.HTTP || !r.Frameable || r.Reason != "" || r.Title != "Real page" {
		t.Errorf("embed = %+v", r)
	}
}
