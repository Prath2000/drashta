package actions

// Tests for ProbePage (servers.go). Every server here is an httptest server or a raw listener on
// loopback with a random port; nothing leaves the machine.

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

const ppParent = "http://localhost:4455"

// ppPort returns the port of a listener address.
func ppPort(t *testing.T, addr net.Addr) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr.String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// ppServe starts a server that answers every request with the headers and a titled page.
func ppServe(t *testing.T, headers map[string][]string) int {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, vs := range headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		fmt.Fprint(w, "<html><head><title> Shop dev </title></head><body>hi</body></html>")
	}))
	t.Cleanup(ts.Close)
	return ppPort(t, ts.Listener.Addr())
}

func TestProbePageFrameable(t *testing.T) {
	h := func(k string, v ...string) map[string][]string { return map[string][]string{k: v} }
	csp, xfo := "Content-Security-Policy", "X-Frame-Options"
	tests := []struct {
		name      string
		headers   map[string][]string
		parent    string
		frameable bool
		reason    string
	}{
		{"no headers", nil, ppParent, true, ""},
		{"xfo deny", h(xfo, "DENY"), ppParent, false, "X-Frame-Options: DENY"},
		{"xfo deny lowercase", h(xfo, " deny "), ppParent, false, "X-Frame-Options: DENY"},
		{"xfo sameorigin", h(xfo, "SAMEORIGIN"), ppParent, false, "X-Frame-Options: SAMEORIGIN"},
		{"xfo sameorigin lowercase", h(xfo, "sameorigin"), ppParent, false, "X-Frame-Options: SAMEORIGIN"},
		{"xfo two values in one header", h(xfo, "ALLOW-FROM http://x.test, DENY"), ppParent, false, "X-Frame-Options: DENY"},
		{"xfo allow-from is ignored", h(xfo, "ALLOW-FROM http://localhost:4455"), ppParent, true, ""},
		{"csp none", h(csp, "frame-ancestors 'none'"), ppParent, false, "CSP frame-ancestors 'none'"},
		{"csp self", h(csp, "frame-ancestors 'self'"), ppParent, false, "CSP frame-ancestors 'self'"},
		{"csp star", h(csp, "frame-ancestors *"), ppParent, true, ""},
		{"csp exact origin", h(csp, "frame-ancestors http://localhost:4455"), ppParent, true, ""},
		{"csp self plus origin", h(csp, "default-src 'self'; frame-ancestors 'self' http://localhost:4455"), ppParent, true, ""},
		{"csp other origin", h(csp, "frame-ancestors https://example.com"), ppParent, false, "CSP frame-ancestors https://example.com"},
		{"csp other port", h(csp, "frame-ancestors http://localhost:3000"), ppParent, false, "CSP frame-ancestors http://localhost:3000"},
		{"csp scheme source", h(csp, "frame-ancestors http:"), ppParent, true, ""},
		{"csp wrong scheme source", h(csp, "frame-ancestors https:"), ppParent, false, "CSP frame-ancestors https:"},
		{"csp host without scheme", h(csp, "frame-ancestors localhost:4455"), ppParent, true, ""},
		{"csp host without port needs default port", h(csp, "frame-ancestors localhost"), ppParent, false, "CSP frame-ancestors localhost"},
		{"csp port wildcard", h(csp, "frame-ancestors http://localhost:*"), ppParent, true, ""},
		{"csp port wildcard other host", h(csp, "frame-ancestors http://example.com:*"), ppParent, false, "CSP frame-ancestors http://example.com:*"},
		{"csp host wildcard", h(csp, "frame-ancestors *.example.com"), "http://app.example.com:4455", false, "CSP frame-ancestors *.example.com"},
		{"csp host wildcard match", h(csp, "frame-ancestors *.example.com:4455"), "http://app.example.com:4455", true, ""},
		{"csp host wildcard needs a label", h(csp, "frame-ancestors *.example.com:4455"), "http://example.com:4455", false, "CSP frame-ancestors *.example.com:4455"},
		{"csp 127.0.0.1 is not localhost", h(csp, "frame-ancestors http://127.0.0.1:4455"), ppParent, false, "CSP frame-ancestors http://127.0.0.1:4455"},
		{"csp 127.0.0.1 parent", h(csp, "frame-ancestors http://127.0.0.1:4455"), "http://127.0.0.1:4455", true, ""},
		{"csp source case is ignored", h(csp, "FRAME-ANCESTORS HTTP://LOCALHOST:4455"), ppParent, true, ""},
		{"csp without frame-ancestors", h(csp, "default-src 'self'; script-src 'none'"), ppParent, true, ""},
		{"csp report-only is ignored", h("Content-Security-Policy-Report-Only", "frame-ancestors 'none'"), ppParent, true, ""},
		{"csp second header blocks", h(csp, "frame-ancestors *", "frame-ancestors 'none'"), ppParent, false, "CSP frame-ancestors 'none'"},
		{"csp two policies in one header", h(csp, "frame-ancestors *, frame-ancestors http://example.com"), ppParent, false, "CSP frame-ancestors http://example.com"},
		{"csp only the first frame-ancestors counts", h(csp, "frame-ancestors *; frame-ancestors 'none'"), ppParent, true, ""},
		{"csp with empty parent origin", h(csp, "frame-ancestors http://localhost:4455"), "", false, "CSP frame-ancestors http://localhost:4455"},
		{"csp star with empty parent origin", h(csp, "frame-ancestors *"), "", true, ""},
		{"csp frame-ancestors overrides xfo", map[string][]string{xfo: {"DENY"}, csp: {"frame-ancestors *"}}, ppParent, true, ""},
		{"xfo still counts when csp has no frame-ancestors", map[string][]string{xfo: {"DENY"}, csp: {"default-src 'self'"}}, ppParent, false, "X-Frame-Options: DENY"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ProbePage(ppServe(t, tc.headers), tc.parent)
			if !got.HTTP || got.Title != "Shop dev" {
				t.Errorf("HTTP=%v Title=%q, want true and the title", got.HTTP, got.Title)
			}
			if got.Frameable != tc.frameable || got.Reason != tc.reason {
				t.Errorf("Frameable=%v Reason=%q, want %v %q", got.Frameable, got.Reason, tc.frameable, tc.reason)
			}
		})
	}
}

func TestProbePageNotHTTP(t *testing.T) {
	listen := func(t *testing.T, serve func(net.Conn)) int {
		t.Helper()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() { defer c.Close(); serve(c) }()
			}
		}()
		return ppPort(t, ln.Addr())
	}
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := ppPort(t, closed.Addr())
	closed.Close()

	cases := map[string]int{
		"garbage":         listen(t, func(c net.Conn) { fmt.Fprint(c, "\x00\x01 not http at all\r\n\r\n") }),
		"closes at once":  listen(t, func(c net.Conn) {}),
		"nothing listens": closedPort,
		"port 0":          0,
		"port too big":    70000,
	}
	for name, port := range cases {
		t.Run(name, func(t *testing.T) {
			got := ProbePage(port, ppParent)
			if got.HTTP || got.Frameable || got.Title != "" || got.Reason != "not a web page" {
				t.Errorf("got %+v, want not HTTP, not frameable, reason %q", got, "not a web page")
			}
		})
	}
}

// A server that listens on [::1] only (vite on macOS) is reached by the fallback.
func TestProbePageIPv6Only(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback: ", err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		fmt.Fprint(w, "<title>v6 app</title>")
	}))
	ts.Listener.Close()
	ts.Listener = ln
	ts.Start()
	defer ts.Close()
	got := ProbePage(ppPort(t, ln.Addr()), ppParent)
	if !got.HTTP || got.Title != "v6 app" || got.Frameable || got.Reason != "X-Frame-Options: DENY" {
		t.Errorf("got %+v", got)
	}
}

func TestProbePageRedirects(t *testing.T) {
	// A redirect to another host is not followed: HTTP is true and the redirect's headers are judged.
	off := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		http.Redirect(w, r, "http://example.invalid/never", http.StatusFound)
	}))
	defer off.Close()
	got := ProbePage(ppPort(t, off.Listener.Addr()), ppParent)
	if !got.HTTP || got.Title != "" || got.Frameable || got.Reason != "X-Frame-Options: DENY" {
		t.Errorf("off-host redirect: %+v", got)
	}

	// A redirect that stays on localhost is followed, so the title and headers are the final page's.
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/login", http.StatusFound) })
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "frame-ancestors http://localhost:*")
		fmt.Fprint(w, "<title>Log in</title>")
	})
	local := httptest.NewServer(mux)
	defer local.Close()
	got = ProbePage(ppPort(t, local.Listener.Addr()), ppParent)
	if !got.HTTP || got.Title != "Log in" || !got.Frameable || got.Reason != "" {
		t.Errorf("local redirect: %+v", got)
	}

	// A redirect loop ends instead of hanging.
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusFound) }))
	defer loop.Close()
	if got = ProbePage(ppPort(t, loop.Listener.Addr()), ppParent); !got.HTTP {
		t.Errorf("redirect loop: %+v", got)
	}
}

func TestPageTitleStillWorksThroughProbePage(t *testing.T) {
	if got := PageTitle(ppServe(t, map[string][]string{"X-Frame-Options": {"DENY"}})); got != "Shop dev" {
		t.Errorf("title = %q", got)
	}
}
