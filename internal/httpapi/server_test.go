package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/live"
)

func TestCoreHealth(t *testing.T) {
	s := coreNewServer(t, nil)
	rec := coreDo(t, s, http.MethodGet, "/api/health", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"app":"drashta","ok":true}` {
		t.Fatalf("body = %s", got)
	}
}

func TestCoreBlankPage(t *testing.T) {
	s := coreNewServer(t, nil)
	rec := coreDo(t, s, http.MethodGet, "/blank", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type = %q", ct)
	}
	if b := rec.Body.String(); !strings.Contains(b, "background:transparent") || strings.Contains(b, "drashta-token") {
		t.Fatalf("body = %s", b)
	}
}

func TestCoreSessionHandsOutTheToken(t *testing.T) {
	// Review I12: /api/session is on in production too, so a tab that outlived
	// a restart can pick up the new token instead of reloading.
	for _, dev := range []bool{false, true} {
		s := coreNewServer(t, func(o *Options) { o.Dev = dev })
		rec := coreDo(t, s, http.MethodGet, "/api/session", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("dev=%v status = %d", dev, rec.Code)
		}
		var got struct{ Token string }
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Token != coreTestTokenText || got.Token != s.Token() {
			t.Fatalf("token = %q", got.Token)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Errorf("headers = %v", rec.Header())
		}
		coreNoCORS(t, rec)
		// The token it hands out really works.
		if r := coreDo(t, s, http.MethodPost, "/api/echo", `{}`, map[string]string{TokenHeader: got.Token}); r.Code != http.StatusOK {
			t.Errorf("POST with session token status = %d", r.Code)
		}
		// A foreign host, a foreign Origin or a cross-site fetch gets nothing.
		for _, h := range []map[string]string{
			{"Host": "evil.example:4455"},
			{"Origin": "http://evil.example"},
			{"Sec-Fetch-Site": "cross-site"},
		} {
			if rec := coreDo(t, s, http.MethodGet, "/api/session", "", h); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), coreTestTokenText) {
				t.Fatalf("dev=%v %v status = %d body %s", dev, h, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestCoreDepsAndAccessors(t *testing.T) {
	hub := live.NewHub(live.HubOptions{})
	s := coreNewServer(t, func(o *Options) { o.Hub = hub })
	d := s.Deps()
	if d.Hub != hub || s.Hub() != hub || d.Logger == nil || d.Now == nil {
		t.Fatalf("deps = %+v", d)
	}
	if New(Options{}).Hub() == nil {
		t.Fatal("a hub should be made when none is given")
	}
}

func TestCoreStateEndpoint(t *testing.T) {
	hub := live.NewHub(live.HubOptions{StartSeq: 100, RingSize: 5})
	s := coreNewServer(t, func(o *Options) { o.Hub = hub })
	for i := 0; i < 8; i++ { // ids 101..108; the ring keeps 104..108
		hub.Publish(live.ActivityEvent("g/a", "2026-10-01"))
	}
	type reply struct {
		Resync bool `json:"resync"`
		Head   uint64
		Events []struct {
			ID   uint64          `json:"id"`
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"events"`
	}
	get := func(q string) (int, reply, string) {
		rec := coreDo(t, s, http.MethodGet, "/api/state"+q, "", nil)
		var r reply
		_ = json.Unmarshal(rec.Body.Bytes(), &r)
		return rec.Code, r, rec.Body.String()
	}

	code, r, raw := get("?since=106")
	if code != 200 || r.Resync || len(r.Events) != 2 || r.Events[0].ID != 107 || r.Events[1].ID != 108 || r.Head != 108 {
		t.Fatalf("since=106: %d %s", code, raw)
	}
	if r.Events[0].Type != "activity" || !strings.Contains(string(r.Events[0].Data), `"day":"2026-10-01"`) {
		t.Errorf("event body = %+v", r.Events[0])
	}
	// Caught up: empty list (an array, not null), no resync.
	code, r, raw = get("?since=108")
	if code != 200 || r.Resync || !strings.Contains(raw, `"events":[]`) {
		t.Fatalf("since=head: %d %s", code, raw)
	}
	// Oldest event still in the ring is 104, so asking from 103 works...
	if _, r, raw = get("?since=103"); r.Resync || len(r.Events) != 5 {
		t.Fatalf("since=103: %s", raw)
	}
	// ...and from 102 it has fallen out.
	code, r, raw = get("?since=102")
	if code != 200 || !r.Resync || !strings.Contains(raw, `"events":[]`) || r.Head != 108 {
		t.Fatalf("since=102: %d %s", code, raw)
	}
	// An id from the future (another server run) resyncs.
	if _, r, raw = get("?since=999999"); !r.Resync {
		t.Fatalf("future id: %s", raw)
	}
	// Missing or zero since: the page knows nothing.
	for _, q := range []string{"", "?since=0", "?since="} {
		if _, r, raw = get(q); !r.Resync {
			t.Fatalf("%q: %s", q, raw)
		}
	}
	for _, q := range []string{"?since=abc", "?since=-1", "?since=1.5"} {
		if code, _, _ = get(q); code != http.StatusBadRequest {
			t.Errorf("%q: status = %d", q, code)
		}
	}
}

func TestCorePresenceEndpoint(t *testing.T) {
	hub := live.NewHub(live.HubOptions{})
	s := coreNewServer(t, func(o *Options) { o.Hub = hub })
	a := hub.Connect(0)
	b := hub.Connect(0)
	defer a.Close()
	defer b.Close()

	post := func(body string, hdr map[string]string) *httptest.ResponseRecorder {
		return coreDo(t, s, http.MethodPost, "/api/presence", body, hdr)
	}
	// By connection id.
	if rec := post(`{"visible":false,"conn":"`+a.ID+`"}`, nil); rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if a.Visible() || !b.Visible() || !hub.AnyVisible() {
		t.Fatal("only a should be hidden")
	}
	if rec := post(`{"visible":false,"conn":"`+b.ID+`"}`, nil); rec.Code != 200 || hub.AnyVisible() {
		t.Fatal("both hidden now")
	}
	// Without an id it applies to every open stream.
	if rec := post(`{"visible":true}`, nil); rec.Code != 200 || !a.Visible() || !b.Visible() {
		t.Fatal("visible:true without conn should show all")
	}
	// Unknown id.
	if rec := post(`{"visible":true,"conn":"nope"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown conn status = %d", rec.Code)
	}
	// Missing visible.
	if rec := post(`{"conn":"`+a.ID+`"}`, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing visible status = %d", rec.Code)
	}
	if rec := post(`not json`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json status = %d", rec.Code)
	}
	// It is a mutating call: token and JSON are required.
	if rec := post(`{"visible":true}`, map[string]string{TokenHeader: ""}); rec.Code != http.StatusForbidden {
		t.Errorf("no token status = %d", rec.Code)
	}
	if rec := post(`{"visible":true}`, map[string]string{"Content-Type": "text/plain"}); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain status = %d", rec.Code)
	}
}

func TestCoreListenRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "192.168.1.5:4455", "[::]:0", "[::1]:0", "example.com:4455", "nonsense"} {
		s := New(Options{Addr: addr})
		ln, err := s.Listen()
		if err == nil {
			ln.Close()
			t.Errorf("Listen(%q) succeeded", addr)
		}
	}
}

func TestCoreListenOnRandomPortAllowsItsHosts(t *testing.T) {
	s := New(Options{Addr: "127.0.0.1:0", Token: coreTestToken})
	ln, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if ln.Addr().(*net.TCPAddr).IP.String() != "127.0.0.1" {
		t.Fatalf("bound to %v", ln.Addr())
	}
	if s.Addr() != ln.Addr().String() {
		t.Errorf("Addr() = %q", s.Addr())
	}
	host := net.JoinHostPort("localhost", strconv.Itoa(port))
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Host %s status = %d", host, rec.Code)
	}
}

func TestCoreListenLocalhostName(t *testing.T) {
	s := New(Options{Addr: "localhost:0"})
	ln, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
}

// coreStart runs a real server on a free port and stops it at the end of the test.
func coreStart(t *testing.T, mod func(*Options)) (*Server, string) {
	t.Helper()
	o := Options{Addr: "127.0.0.1:0", Token: coreTestToken}
	if mod != nil {
		mod(&o)
	}
	s := New(o)
	ln, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after Shutdown")
		}
	})
	return s, ln.Addr().String()
}

func TestCoreAlreadyRunningFalseWhenPortIsFree(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // now nothing listens there
	running, err := AlreadyRunning(addr)
	if running || err != nil {
		t.Fatalf("running=%v err=%v", running, err)
	}
}

func TestCoreAlreadyRunningTrueForDrashta(t *testing.T) {
	_, addr := coreStart(t, nil)
	running, err := AlreadyRunning(addr)
	if !running || err != nil {
		t.Fatalf("running=%v err=%v", running, err)
	}
	// "localhost:<port>" works too, and so does the host-less form.
	_, port, _ := net.SplitHostPort(addr)
	if running, err := AlreadyRunning("localhost:" + port); !running || err != nil {
		t.Fatalf("localhost: running=%v err=%v", running, err)
	}
	if running, err := AlreadyRunning(":" + port); !running || err != nil {
		t.Fatalf("host-less: running=%v err=%v", running, err)
	}
}

func TestCoreAlreadyRunningErrorForOtherPrograms(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"another json app": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"app":"ledger","ok":true}`))
		},
		"html server": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<html>hi</html>`))
		},
		"drashta but not ok": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"app":"drashta","ok":false}`))
		},
		"404": http.NotFound,
	}
	for name, h := range cases {
		srv := httptest.NewServer(h)
		running, err := AlreadyRunning(strings.TrimPrefix(srv.URL, "http://"))
		srv.Close()
		if running || err == nil {
			t.Errorf("%s: running=%v err=%v", name, running, err)
		}
	}
}

func TestCoreAlreadyRunningErrorForSilentListener(t *testing.T) {
	// Accepts the connection and never answers.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	running, err := AlreadyRunning(ln.Addr().String())
	if running || err == nil {
		t.Fatalf("running=%v err=%v", running, err)
	}
}

func TestCoreAlreadyRunningBadAddress(t *testing.T) {
	if _, err := AlreadyRunning("no-port"); err == nil {
		t.Fatal("expected an error")
	}
}
