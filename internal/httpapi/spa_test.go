package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const coreIndexHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>drashta</title>
<link rel="stylesheet" href="/assets/app.4f3a9c.css"></head>
<body><div id="root"></div><script type="module" src="/assets/app.9b1d77.js"></script></body></html>
`

func coreStatic() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             {Data: []byte(coreIndexHTML)},
		"assets/app.9b1d77.js":   {Data: []byte("console.log('drashta');")},
		"assets/app.4f3a9c.css":  {Data: []byte("body{margin:0}")},
		"assets/font.aa11.woff2": {Data: []byte("wOF2....")},
		"favicon.svg":            {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"robots.txt":             {Data: []byte("User-agent: *")},
		".keep":                  {Data: []byte("")},
	}
}

func coreSPAServer(t *testing.T, static fstest.MapFS, mod func(*Options)) *Server {
	t.Helper()
	return coreNewServer(t, func(o *Options) {
		o.Static = static
		if mod != nil {
			mod(o)
		}
	})
}

func TestSPAServesIndexWithTokenInjected(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	rec := coreDo(t, s, http.MethodGet, "/", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	meta := `<meta name="drashta-token" content="` + coreTestTokenText + `">`
	if strings.Count(body, meta) != 1 {
		t.Fatalf("token meta tag count = %d in:\n%s", strings.Count(body, meta), body)
	}
	if strings.Index(body, meta) > strings.Index(body, "</head>") {
		t.Error("token meta must be inside <head>")
	}
	if !strings.Contains(body, `<div id="root">`) {
		t.Error("the rest of index.html is missing")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("index Cache-Control = %q", cc)
	}
	coreNoCORS(t, rec)
}

func TestSPAReplacesTokenPlaceholder(t *testing.T) {
	static := coreStatic()
	static["index.html"] = &fstest.MapFile{Data: []byte(`<html><head><META NAME="drashta-token" content=""></head><body></body></html>`)}
	s := coreSPAServer(t, static, nil)
	body := coreDo(t, s, http.MethodGet, "/", "", nil).Body.String()
	if strings.Count(strings.ToLower(body), "drashta-token") != 1 || !strings.Contains(body, `content="`+coreTestTokenText+`"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestSPAInjectTokenOddPages(t *testing.T) {
	cases := map[string]string{
		"no head":      `<html><body>x</body></html>`,
		"no html":      `plain text`,
		"uppercase":    `<HTML><HEAD></HEAD><BODY></BODY></HTML>`,
		"empty":        ``,
		"single quote": `<head><meta name='drashta-token' content='x'></head>`,
	}
	for name, page := range cases {
		out := string(injectToken([]byte(page), "abc123"))
		if strings.Count(out, "drashta-token") != 1 || !strings.Contains(out, `content="abc123"`) {
			t.Errorf("%s: %q", name, out)
		}
	}
	// A token with odd characters cannot break out of the attribute.
	if out := string(injectToken([]byte("<head></head>"), `"><script>`)); strings.Contains(out, "<script>") {
		t.Errorf("token was not escaped: %s", out)
	}
}

func TestSPAIndexAlsoAtIndexHTMLPathButNeverRaw(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	rec := coreDo(t, s, http.MethodGet, "/index.html", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), coreTestTokenText) {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSPAFallbackForClientRoutes(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	for _, p := range []string{"/p/personal/ledger", "/p/personal/ledger/files", "/tracker", "/inbox", "/tracker?filter=review", "/deep/er/route/"} {
		rec := coreDo(t, s, http.MethodGet, p, "", nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<div id="root">`) || !strings.Contains(rec.Body.String(), coreTestTokenText) {
			t.Errorf("%s: status = %d", p, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", p, cc)
		}
	}
}

func TestSPAMissingFilesAre404NotHTML(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	for _, p := range []string{"/assets/missing.js", "/assets/", "/assets", "/missing.js", "/style.css", "/.keep", "/assets/.keep", "/.env"} {
		rec := coreDo(t, s, http.MethodGet, p, "", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), coreTestTokenText) {
			t.Errorf("%s: leaked the token page", p)
		}
	}
}

func TestSPADottedRouteNeedsHTMLAccept(t *testing.T) {
	// A project folder like gizmo-v0.9.0 has a dot. A browser navigation sends Accept: text/html.
	s := coreSPAServer(t, coreStatic(), nil)
	p := "/p/tools/gizmo-v0.9.0"
	if rec := coreDo(t, s, http.MethodGet, p, "", map[string]string{"Accept": "text/html,application/xhtml+xml,*/*;q=0.8"}); rec.Code != 200 {
		t.Errorf("browser navigation status = %d", rec.Code)
	}
	if rec := coreDo(t, s, http.MethodGet, p, "", map[string]string{"Accept": "*/*"}); rec.Code != 404 {
		t.Errorf("script-like request status = %d", rec.Code)
	}
}

func TestSPAAssetsAreImmutableOthersNoCache(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	cases := []struct{ path, ctype, cache string }{
		{"/assets/app.9b1d77.js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/assets/app.4f3a9c.css", "text/css; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/assets/font.aa11.woff2", "font/woff2", "public, max-age=31536000, immutable"},
		{"/favicon.svg", "image/svg+xml", "no-cache"},
		{"/robots.txt", "text/plain; charset=utf-8", "no-cache"},
	}
	for _, c := range cases {
		rec := coreDo(t, s, http.MethodGet, c.path, "", nil)
		if rec.Code != 200 {
			t.Errorf("%s: status = %d", c.path, rec.Code)
			continue
		}
		if got := rec.Header().Get("Cache-Control"); got != c.cache {
			t.Errorf("%s: Cache-Control = %q, want %q", c.path, got, c.cache)
		}
		if got := rec.Header().Get("Content-Type"); got != c.ctype {
			t.Errorf("%s: Content-Type = %q, want %q", c.path, got, c.ctype)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", c.path)
		}
	}
	if body := coreDo(t, s, http.MethodGet, "/assets/app.9b1d77.js", "", nil).Body.String(); body != "console.log('drashta');" {
		t.Errorf("asset body = %q", body)
	}
}

func TestSPAAssetRangeRequest(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	rec := coreDo(t, s, http.MethodGet, "/assets/app.9b1d77.js", "", map[string]string{"Range": "bytes=0-6"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "console" {
		t.Errorf("status = %d body = %q", rec.Code, rec.Body.String())
	}
}

func TestSPAIndexOnlyForAllowedHost(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	for _, p := range []string{"/", "/index.html", "/p/a/b", "/tracker"} {
		for _, host := range []string{"evil.example:4455", "localhost:9999", "127.0.0.1", ""} {
			rec := coreDo(t, s, http.MethodGet, p, "", map[string]string{"Host": host})
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s with Host %q: status = %d", p, host, rec.Code)
			}
			if strings.Contains(rec.Body.String(), coreTestTokenText) {
				t.Errorf("%s with Host %q leaked the token", p, host)
			}
		}
	}
	// A foreign Origin does not get the page either.
	if rec := coreDo(t, s, http.MethodGet, "/", "", map[string]string{"Origin": "http://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("foreign origin status = %d", rec.Code)
	}
	// Static assets are public code: no host check.
	if rec := coreDo(t, s, http.MethodGet, "/assets/app.9b1d77.js", "", map[string]string{"Host": "evil.example:4455"}); rec.Code != 200 {
		t.Errorf("asset with foreign host status = %d", rec.Code)
	}
	// Both allowed hosts get the page.
	for _, host := range []string{"localhost:4455", "127.0.0.1:4455"} {
		if rec := coreDo(t, s, http.MethodGet, "/", "", map[string]string{"Host": host}); rec.Code != 200 {
			t.Errorf("Host %s status = %d", host, rec.Code)
		}
	}
}

func TestSPAAPIPathsNeverFallBackToIndex(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	for _, p := range []string{"/api/nope", "/api/", "/api", "/api/projects/x/y"} {
		rec := coreDo(t, s, http.MethodGet, p, "", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type = %q", p, ct)
		}
		if e := coreErr(t, rec); e.Error.Code != "not_found" {
			t.Errorf("%s: code = %q", p, e.Error.Code)
		}
	}
}

func TestSPAMethods(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	rec := coreDo(t, s, http.MethodPost, "/", `{}`, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / status = %d", rec.Code)
	}
	rec = coreDo(t, s, http.MethodHead, "/", "", nil)
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD /: status = %d body = %d bytes", rec.Code, rec.Body.Len())
	}
	rec = coreDo(t, s, http.MethodOptions, "/", "", map[string]string{"Origin": "http://evil.example"})
	if rec.Code < 400 {
		t.Errorf("OPTIONS / status = %d", rec.Code)
	}
	coreNoCORS(t, rec)
}

func TestSPAPathTraversalStaysInsideTheFS(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), nil)
	for _, p := range []string{"/../../etc/passwd", "/assets/../../etc/passwd", "/%2e%2e/%2e%2e/etc/passwd", "/assets/%2e%2e/index.html"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Host = "localhost:4455"
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "root:") {
			t.Errorf("%s leaked a system file", p)
		}
	}
}

func TestSPADevServesNothing(t *testing.T) {
	s := coreSPAServer(t, coreStatic(), func(o *Options) { o.Dev = true })
	for _, p := range []string{"/", "/index.html", "/assets/app.9b1d77.js", "/tracker", "/favicon.svg"} {
		rec := coreDo(t, s, http.MethodGet, p, "", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), coreTestTokenText) || strings.Contains(rec.Body.String(), "console.log") {
			t.Errorf("%s: served static content in dev", p)
		}
	}
	if rec := coreDo(t, s, http.MethodGet, "/api/health", "", nil); rec.Code != 200 {
		t.Errorf("API in dev status = %d", rec.Code)
	}
}

func TestSPANotBuiltGivesHelpfulPage(t *testing.T) {
	// Only the placeholder file, as before `npm run build`.
	static := fstest.MapFS{".keep": {Data: nil}}
	s := coreSPAServer(t, static, nil)
	rec := coreDo(t, s, http.MethodGet, "/", "", nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "make web") {
		t.Errorf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	// No Static at all behaves the same.
	s = coreNewServer(t, nil)
	if rec := coreDo(t, s, http.MethodGet, "/some/route", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil Static status = %d", rec.Code)
	}
	// The host check comes first, even for the help page.
	if rec := coreDo(t, s, http.MethodGet, "/", "", map[string]string{"Host": "evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("foreign host status = %d", rec.Code)
	}
}

func TestSPAFindsBuildClientSubfolder(t *testing.T) {
	// web.Static has the files under build/client.
	static := fstest.MapFS{
		"build/client/index.html":           {Data: []byte(coreIndexHTML)},
		"build/client/assets/app.9b1d77.js": {Data: []byte("x")},
		"build/client/.keep":                {Data: nil},
	}
	s := coreSPAServer(t, static, nil)
	if rec := coreDo(t, s, http.MethodGet, "/", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), coreTestTokenText) {
		t.Fatalf("index status = %d", rec.Code)
	}
	if rec := coreDo(t, s, http.MethodGet, "/assets/app.9b1d77.js", "", nil); rec.Code != 200 {
		t.Fatalf("asset status = %d", rec.Code)
	}
	// The sub path itself is not reachable through the URL.
	if rec := coreDo(t, s, http.MethodGet, "/build/client/index.html", "", nil); rec.Code != 200 && rec.Code != 404 {
		t.Fatalf("status = %d", rec.Code)
	} else if rec.Code == 200 && !strings.Contains(rec.Body.String(), coreTestTokenText) {
		t.Fatal("served a raw index.html")
	}
}

func TestSPAIndexIsReadOnce(t *testing.T) {
	static := coreStatic()
	s := coreSPAServer(t, static, nil)
	first := coreDo(t, s, http.MethodGet, "/", "", nil).Body.String()
	static["index.html"] = &fstest.MapFile{Data: []byte("<html><head></head><body>CHANGED</body></html>")}
	second := coreDo(t, s, http.MethodGet, "/", "", nil).Body.String()
	if first != second || strings.Contains(second, "CHANGED") {
		t.Error("index.html was read again")
	}
}
