package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Helpers in the *_test.go files of the core use the "core" prefix so that
// api_*_test.go files written later cannot clash with them.

const coreTestPort = "4455"

var coreTestToken = []byte{0xde, 0xad, 0xbe, 0xef}

const coreTestTokenText = "deadbeef"

// coreNewServer builds a server with a fixed token and a few test routes.
func coreNewServer(t *testing.T, mod func(*Options)) *Server {
	t.Helper()
	o := Options{Token: coreTestToken}
	if mod != nil {
		mod(&o)
	}
	s := New(o)
	echo := func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if !ReadJSON(w, r, &v) {
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}
	s.Handle(http.MethodPost, "/api/echo", echo, true)
	s.Handle(http.MethodPatch, "/api/echo", echo, true)
	s.Handle(http.MethodPut, "/api/echo", echo, true)
	s.Handle(http.MethodDelete, "/api/echo", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}, true)
	s.Handle(http.MethodGet, "/api/read", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}, false)
	s.Handle(http.MethodGet, "/api/read-secret", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}, true)
	s.HandleOpts(http.MethodPost, "/api/big", echo, RouteOptions{Mutating: true, MaxBody: 256 << 10})
	s.HandleOpts(http.MethodPost, "/api/upload", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"ct": r.Header.Get("Content-Type")})
	}, RouteOptions{Mutating: true, Upload: true})
	s.Handle(http.MethodGet, "/api/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	}, false)
	s.Handle(http.MethodGet, "/api/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"id": r.PathValue("id")})
	}, false)
	return s
}

// coreDo sends a well-formed request: allowed Host, and for POST/PATCH/PUT/DELETE a JSON
// content type and the token. hdr overrides headers; an empty value deletes the header;
// the key "Host" sets r.Host.
func coreDo(t *testing.T, s *Server, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "localhost:" + coreTestPort
	if bodyMethod(method) {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(TokenHeader, coreTestTokenText)
	}
	for k, v := range hdr {
		switch {
		case k == "Host":
			req.Host = v
		case v == "":
			req.Header.Del(k)
		default:
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

type coreErrBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func coreErr(t *testing.T, rec *httptest.ResponseRecorder) coreErrBody {
	t.Helper()
	var e coreErrBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("body is not an error object: %q", rec.Body.String())
	}
	return e
}

func coreNoCORS(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for k := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("CORS header %s present", k)
		}
	}
}

func TestGuardRejectsForeignHost(t *testing.T) {
	s := coreNewServer(t, nil)
	for _, host := range []string{"evil.example:4455", "localhost:5555", "127.0.0.1", "localhost", "localhost.:4455", "[::1]:4455", "0.0.0.0:4455", ""} {
		rec := coreDo(t, s, http.MethodGet, "/api/read", "", map[string]string{"Host": host})
		if rec.Code != http.StatusForbidden {
			t.Errorf("Host %q status = %d", host, rec.Code)
		}
	}
	// A foreign host is refused on mutating calls and on SSE too.
	rec := coreDo(t, s, http.MethodPost, "/api/echo", `{}`, map[string]string{"Host": "evil.example:4455"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST foreign host status = %d", rec.Code)
	}
	rec = coreDo(t, s, http.MethodGet, "/api/events", "", map[string]string{"Host": "evil.example:4455"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("SSE foreign host status = %d", rec.Code)
	}
}

func TestGuardAllowsBothLoopbackHostsAndIsCaseInsensitive(t *testing.T) {
	s := coreNewServer(t, nil)
	for _, host := range []string{"localhost:4455", "127.0.0.1:4455", "LOCALHOST:4455"} {
		rec := coreDo(t, s, http.MethodGet, "/api/read", "", map[string]string{"Host": host})
		if rec.Code != http.StatusOK {
			t.Errorf("Host %q status = %d", host, rec.Code)
		}
	}
}

func TestGuardAllowedHostsOption(t *testing.T) {
	s := coreNewServer(t, func(o *Options) { o.AllowedHosts = []string{"drashta.test:4455"} })
	if rec := coreDo(t, s, http.MethodGet, "/api/read", "", map[string]string{"Host": "drashta.test:4455"}); rec.Code != http.StatusOK {
		t.Errorf("extra host status = %d", rec.Code)
	}
	s2 := coreNewServer(t, func(o *Options) { o.Addr = "127.0.0.1:9999" })
	if rec := coreDo(t, s2, http.MethodGet, "/api/read", "", map[string]string{"Host": "localhost:9999"}); rec.Code != http.StatusOK {
		t.Errorf("custom port status = %d", rec.Code)
	}
	if rec := coreDo(t, s2, http.MethodGet, "/api/read", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("default port on custom-port server status = %d", rec.Code)
	}
}

func TestGuardRejectsForeignOrigin(t *testing.T) {
	s := coreNewServer(t, nil)
	origins := []string{"http://evil.example", "http://localhost:5555", "null", "https://localhost:4455",
		"http://localhost:4455/x", "http://localhost:5173", "http://127.0.0.1:5173", ""}
	for _, origin := range origins {
		for _, req := range []struct{ method, path string }{
			{http.MethodPost, "/api/echo"}, {http.MethodGet, "/api/read"}, {http.MethodGet, "/api/events"},
		} {
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(req.method, req.path, strings.NewReader(`{}`))
			r.Host = "localhost:4455"
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set(TokenHeader, coreTestTokenText)
			r.Header["Origin"] = []string{origin} // set even when empty
			s.Handler().ServeHTTP(rec, r)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s Origin %q status = %d", req.method, req.path, origin, rec.Code)
			}
		}
	}
}

func TestGuardAllowsSameOrigin(t *testing.T) {
	s := coreNewServer(t, nil)
	for _, h := range []map[string]string{
		{"Host": "localhost:4455", "Origin": "http://localhost:4455", "Content-Type": "application/json; charset=utf-8"},
		{"Host": "127.0.0.1:4455", "Origin": "http://127.0.0.1:4455"},
	} {
		rec := coreDo(t, s, http.MethodPost, "/api/echo", `{"a":1}`, h)
		if rec.Code != http.StatusOK {
			t.Errorf("headers %v status = %d body = %s", h, rec.Code, rec.Body.String())
		}
	}
}

func TestGuardDevOriginOnlyInDevMode(t *testing.T) {
	prod := coreNewServer(t, nil)
	dev := coreNewServer(t, func(o *Options) { o.Dev = true })
	h := map[string]string{"Origin": "http://localhost:5173"}
	if rec := coreDo(t, prod, http.MethodGet, "/api/read", "", h); rec.Code != http.StatusForbidden {
		t.Errorf("prod with vite origin status = %d", rec.Code)
	}
	if rec := coreDo(t, dev, http.MethodGet, "/api/read", "", h); rec.Code != http.StatusOK {
		t.Errorf("dev with vite origin status = %d", rec.Code)
	}
	if rec := coreDo(t, dev, http.MethodPost, "/api/echo", `{}`, h); rec.Code != http.StatusOK {
		t.Errorf("dev POST with vite origin status = %d", rec.Code)
	}
	// Dev still refuses other origins.
	if rec := coreDo(t, dev, http.MethodGet, "/api/read", "", map[string]string{"Origin": "http://localhost:5174"}); rec.Code != http.StatusForbidden {
		t.Errorf("dev with other origin status = %d", rec.Code)
	}
}

func TestGuardTokenRequiredForMutatingMethods(t *testing.T) {
	s := coreNewServer(t, nil)
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		for name, hdr := range map[string]map[string]string{
			"missing": {TokenHeader: ""},
			"wrong":   {TokenHeader: "00000000"},
			"prefix":  {TokenHeader: coreTestTokenText[:4]},
			"longer":  {TokenHeader: coreTestTokenText + "00"},
			"upper":   {TokenHeader: strings.ToUpper(coreTestTokenText)},
		} {
			rec := coreDo(t, s, method, "/api/echo", `{}`, hdr)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s with %s token: status = %d", method, name, rec.Code)
				continue
			}
			if e := coreErr(t, rec); e.Error.Code != "forbidden" {
				t.Errorf("%s %s token: code = %q", method, name, e.Error.Code)
			}
		}
		if rec := coreDo(t, s, method, "/api/echo", `{}`, nil); rec.Code != http.StatusOK {
			t.Errorf("%s with the right token: status = %d body = %s", method, rec.Code, rec.Body.String())
		}
	}
}

func TestGuardTokenNotNeededForPlainGet(t *testing.T) {
	s := coreNewServer(t, nil)
	if rec := coreDo(t, s, http.MethodGet, "/api/read", "", nil); rec.Code != http.StatusOK {
		t.Errorf("GET without token status = %d", rec.Code)
	}
}

func TestGuardMutatingFlagOnGetNeedsToken(t *testing.T) {
	s := coreNewServer(t, nil)
	if rec := coreDo(t, s, http.MethodGet, "/api/read-secret", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("flagged GET without token status = %d", rec.Code)
	}
	if rec := coreDo(t, s, http.MethodGet, "/api/read-secret", "", map[string]string{TokenHeader: coreTestTokenText}); rec.Code != http.StatusOK {
		t.Errorf("flagged GET with token status = %d", rec.Code)
	}
}

func TestGuardRandomTokenWhenNoneGiven(t *testing.T) {
	a, b := New(Options{}), New(Options{})
	if len(a.Token()) != 64 || len(b.Token()) != 64 {
		t.Fatalf("token lengths = %d, %d (want 64 hex chars = 32 bytes)", len(a.Token()), len(b.Token()))
	}
	if a.Token() == b.Token() {
		t.Fatal("two servers share a token")
	}
}

func TestGuardPostRequiresJSONContentType(t *testing.T) {
	s := coreNewServer(t, nil)
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		for _, ct := range []string{"text/plain", "", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonp"} {
			rec := coreDo(t, s, method, "/api/echo", `{}`, map[string]string{"Content-Type": ct})
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s Content-Type %q status = %d", method, ct, rec.Code)
			}
		}
	}
	rec := coreDo(t, s, http.MethodPost, "/api/echo", `{}`, map[string]string{"Content-Type": "text/plain"})
	if e := coreErr(t, rec); e.Error.Code != "unsupported_media_type" {
		t.Errorf("code = %q", e.Error.Code)
	}
}

func TestGuardUploadRouteAcceptsMultipartAndJSON(t *testing.T) {
	s := coreNewServer(t, nil)
	for _, ct := range []string{"multipart/form-data; boundary=abc", "application/json"} {
		rec := coreDo(t, s, http.MethodPost, "/api/upload", `x`, map[string]string{"Content-Type": ct})
		if rec.Code != http.StatusOK {
			t.Errorf("upload Content-Type %q status = %d", ct, rec.Code)
		}
	}
	if rec := coreDo(t, s, http.MethodPost, "/api/upload", `x`, map[string]string{"Content-Type": "text/plain"}); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("upload text/plain status = %d", rec.Code)
	}
	// The token is still needed on an upload route.
	if rec := coreDo(t, s, http.MethodPost, "/api/upload", `x`, map[string]string{"Content-Type": "multipart/form-data; boundary=abc", TokenHeader: ""}); rec.Code != http.StatusForbidden {
		t.Errorf("upload without token status = %d", rec.Code)
	}
}

func TestGuardBodyCap(t *testing.T) {
	s := coreNewServer(t, nil)
	// Known length over the cap: refused before the handler runs.
	big := `{"x":"` + strings.Repeat("a", int(DefaultMaxBody)) + `"}`
	rec := coreDo(t, s, http.MethodPost, "/api/echo", big, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d", rec.Code)
	}
	if e := coreErr(t, rec); e.Error.Code != "payload_too_large" {
		t.Errorf("code = %q", e.Error.Code)
	}
	// Just under the cap is fine.
	ok := `{"x":"` + strings.Repeat("a", int(DefaultMaxBody)-100) + `"}`
	if rec := coreDo(t, s, http.MethodPost, "/api/echo", ok, nil); rec.Code != http.StatusOK {
		t.Errorf("body under the cap status = %d", rec.Code)
	}
}

func TestGuardBodyCapWhenLengthIsUnknown(t *testing.T) {
	s := coreNewServer(t, nil)
	big := `{"x":"` + strings.Repeat("a", int(DefaultMaxBody)+10) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/echo", io.NopCloser(strings.NewReader(big)))
	req.ContentLength = -1 // chunked: the guard cannot know the size up front
	req.Host = "localhost:4455"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(TokenHeader, coreTestTokenText)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked oversized body status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestGuardRouteCanRaiseBodyCap(t *testing.T) {
	s := coreNewServer(t, nil)
	body := `{"x":"` + strings.Repeat("a", 100<<10) + `"}`
	if rec := coreDo(t, s, http.MethodPost, "/api/echo", body, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("default route status = %d", rec.Code)
	}
	if rec := coreDo(t, s, http.MethodPost, "/api/big", body, nil); rec.Code != http.StatusOK {
		t.Errorf("raised route status = %d", rec.Code)
	}
	huge := `{"x":"` + strings.Repeat("a", 300<<10) + `"}`
	if rec := coreDo(t, s, http.MethodPost, "/api/big", huge, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("raised route over its own cap status = %d", rec.Code)
	}
}

func TestGuardNeverSendsCORSHeaders(t *testing.T) {
	s := coreNewServer(t, func(o *Options) { o.Dev = true })
	cases := []struct {
		method, path string
		hdr          map[string]string
	}{
		{http.MethodGet, "/api/read", nil},
		{http.MethodGet, "/api/read", map[string]string{"Origin": "http://localhost:5173"}},
		{http.MethodGet, "/api/read", map[string]string{"Origin": "http://evil.example"}},
		{http.MethodGet, "/api/read", map[string]string{"Host": "evil.example"}},
		{http.MethodPost, "/api/echo", map[string]string{"Origin": "http://evil.example"}},
		{http.MethodPost, "/api/echo", map[string]string{TokenHeader: ""}},
		{http.MethodOptions, "/api/echo", map[string]string{"Origin": "http://evil.example", "Access-Control-Request-Method": "POST"}},
		{http.MethodOptions, "/api/read", map[string]string{"Origin": "http://localhost:5173", "Access-Control-Request-Method": "GET"}},
		{http.MethodGet, "/api/nope", nil},
		{http.MethodGet, "/", nil},
		{http.MethodGet, "/api/health", nil},
	}
	for _, c := range cases {
		rec := coreDo(t, s, c.method, c.path, "", c.hdr)
		coreNoCORS(t, rec)
		if c.method == http.MethodOptions && rec.Code < 400 {
			t.Errorf("OPTIONS preflight %s was approved: %d", c.path, rec.Code)
		}
	}
}

func TestGuardAPIResponsesAreNotCached(t *testing.T) {
	s := coreNewServer(t, nil)
	rec := coreDo(t, s, http.MethodGet, "/api/read", "", nil)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestGuardMethodNotAllowedAndUnknownRoutes(t *testing.T) {
	s := coreNewServer(t, nil)
	rec := coreDo(t, s, http.MethodGet, "/api/echo", "", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on a POST-only route status = %d", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	for _, m := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		if !strings.Contains(allow, m) {
			t.Errorf("Allow %q lacks %s", allow, m)
		}
	}
	if e := coreErr(t, rec); e.Error.Code != "method_not_allowed" {
		t.Errorf("code = %q", e.Error.Code)
	}

	rec = coreDo(t, s, http.MethodPost, "/api/read", `{}`, nil)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET" {
		t.Errorf("POST on a GET-only route: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}

	rec = coreDo(t, s, http.MethodGet, "/api/does-not-exist", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d", rec.Code)
	}
	if e := coreErr(t, rec); e.Error.Code != "not_found" {
		t.Errorf("code = %q", e.Error.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	// A POST to an unknown route still has to pass the guard first.
	if rec := coreDo(t, s, http.MethodPost, "/api/does-not-exist", `{}`, map[string]string{TokenHeader: ""}); rec.Code != http.StatusForbidden {
		t.Errorf("unknown POST without token status = %d", rec.Code)
	}
}

func TestGuardPathWildcardsWork(t *testing.T) {
	s := coreNewServer(t, nil)
	rec := coreDo(t, s, http.MethodGet, "/api/item/personal", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"personal"`) {
		t.Errorf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestMountGuardsASubtree(t *testing.T) {
	s := coreNewServer(t, nil)
	s.Mount("/api/sub/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"path": r.URL.Path, "method": r.Method})
	}))
	if rec := coreDo(t, s, http.MethodGet, "/api/sub/a/b", "", nil); rec.Code != http.StatusOK {
		t.Errorf("mounted GET status = %d", rec.Code)
	}
	if rec := coreDo(t, s, http.MethodGet, "/api/sub/a", "", map[string]string{"Host": "evil.example:4455"}); rec.Code != http.StatusForbidden {
		t.Errorf("mounted GET foreign host status = %d", rec.Code)
	}
	if rec := coreDo(t, s, http.MethodPost, "/api/sub/a", `{}`, map[string]string{TokenHeader: ""}); rec.Code != http.StatusForbidden {
		t.Errorf("mounted POST without token status = %d", rec.Code)
	}
	if rec := coreDo(t, s, http.MethodPost, "/api/sub/a", `{}`, nil); rec.Code != http.StatusOK {
		t.Errorf("mounted POST status = %d", rec.Code)
	}
}

func TestRoutesOutsideAPIArePanics(t *testing.T) {
	s := coreNewServer(t, nil)
	for _, fn := range []func(){
		func() { s.Handle("GET", "/other", func(http.ResponseWriter, *http.Request) {}, false) },
		func() { s.Handle("GET", "/api", func(http.ResponseWriter, *http.Request) {}, false) },
		func() { s.Mount("/static/", http.NotFoundHandler()) },
		func() { s.Mount("GET /x", http.NotFoundHandler()) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected a panic")
				}
			}()
			fn()
		}()
	}
}

func TestPanicInHandlerBecomesJSON500(t *testing.T) {
	s := coreNewServer(t, nil)
	rec := coreDo(t, s, http.MethodGet, "/api/boom", "", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := coreErr(t, rec); e.Error.Code != "internal" {
		t.Errorf("code = %q", e.Error.Code)
	}
}

func TestErrorHelpersShape(t *testing.T) {
	cases := []struct {
		fn     func(http.ResponseWriter, string)
		status int
		code   string
	}{
		{BadRequest, 400, "bad_request"},
		{Forbidden, 403, "forbidden"},
		{NotFound, 404, "not_found"},
		{Conflict, 409, "conflict"},
		{Unprocessable, 422, "unprocessable"},
		{Internal, 500, "internal"},
		{UnsupportedMediaType, 415, "unsupported_media_type"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		c.fn(rec, "msg here")
		if rec.Code != c.status {
			t.Errorf("%s: status = %d", c.code, rec.Code)
		}
		want := `{"error":{"code":"` + c.code + `","message":"msg here"}}`
		if got := strings.TrimSpace(rec.Body.String()); got != want {
			t.Errorf("%s: body = %s", c.code, got)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type = %q", c.code, ct)
		}
	}
}

func TestWriteJSONEncodeFailureIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, map[string]any{"bad": make(chan int)})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestReadJSONProblems(t *testing.T) {
	s := coreNewServer(t, nil)
	cases := []struct {
		name, body string
		status     int
	}{
		{"empty", ``, 400},
		{"not json", `{nope`, 400},
		{"trailing data", `{"a":1} {"b":2}`, 400},
		{"trailing garbage", `{"a":1} x`, 400},
		{"trailing space is fine", "{\"a\":1}  \n", 200},
	}
	for _, c := range cases {
		rec := coreDo(t, s, http.MethodPost, "/api/echo", c.body, nil)
		if rec.Code != c.status {
			t.Errorf("%s: status = %d body = %s", c.name, rec.Code, rec.Body.String())
		}
	}
}

// TestGuardSecFetchSite (review M-2): a no-cors request from another site
// (an <img> on /raw, an EventSource) has no Origin header, so the guard reads
// Sec-Fetch-Site instead.
func TestGuardSecFetchSite(t *testing.T) {
	prod := coreNewServer(t, nil)
	dev := coreNewServer(t, func(o *Options) { o.Dev = true })
	cases := []struct {
		s    *Server
		site string
		want int
	}{
		{prod, "", http.StatusOK},
		{prod, "same-origin", http.StatusOK},
		{prod, "none", http.StatusOK},
		{prod, "cross-site", http.StatusForbidden},
		{prod, "Cross-Site", http.StatusForbidden},
		{prod, "same-site", http.StatusForbidden},
		{prod, "something-new", http.StatusForbidden},
		{dev, "same-site", http.StatusOK},
		{dev, "cross-site", http.StatusForbidden},
	}
	for _, c := range cases {
		for _, path := range []string{"/api/read", "/api/item/x", "/api/nope"} {
			want := c.want
			if path == "/api/nope" && want == http.StatusOK {
				want = http.StatusNotFound
			}
			rec := coreDo(t, c.s, http.MethodGet, path, "", map[string]string{"Sec-Fetch-Site": c.site})
			if rec.Code != want {
				t.Errorf("dev=%v site=%q %s: status %d, want %d", c.s.opts.Dev, c.site, path, rec.Code, want)
			}
		}
	}
	// The SSE stream is guarded the same way.
	if rec := coreDo(t, prod, http.MethodGet, "/api/events", "", map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site SSE = %d", rec.Code)
	}
	// Mutating calls with the token are refused cross-site too.
	if rec := coreDo(t, prod, http.MethodPost, "/api/echo", `{}`, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d", rec.Code)
	}
}

func TestGuardSendsCORP(t *testing.T) {
	s := coreNewServer(t, nil)
	for _, path := range []string{"/api/read", "/api/nope"} {
		rec := coreDo(t, s, http.MethodGet, path, "", nil)
		if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
			t.Errorf("%s: CORP = %q", path, got)
		}
	}
}
