package httpapi

import (
	"bytes"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Cache headers (03 section 2, "Build and dev mode").
const (
	cacheIndex     = "no-cache"
	cacheAssets    = "public, max-age=31536000, immutable"
	cacheOtherFile = "no-cache"
)

// spaConfig is what the SPA handler needs from the Server.
type spaConfig struct {
	static fs.FS
	dev    bool
	token  string
	// checkAccess is the Host/Origin check. It writes the 403 reply itself.
	checkAccess func(http.ResponseWriter, *http.Request) bool
}

// spaHandler serves the built web client from an fs.FS.
//
//   - Files that exist are served as they are. Under /assets/ they are
//     immutable for a year (Vite hashes the names); everything else is no-cache.
//   - index.html is read once. A <meta name="drashta-token"> is put in it. It is
//     sent only to an allowed Host, so a DNS-rebinding page cannot get the token.
//   - An unknown GET that is not under /api and not under /assets/ gets index.html
//     (client-side routes). A missing file with an extension (and no text/html
//     in Accept) is a plain 404.
//   - In Dev mode nothing is served: Vite serves the page.
type spaHandler struct {
	cfg   spaConfig
	root  fs.FS
	index []byte // index.html with the token, nil if the client is not built
}

func newSPA(cfg spaConfig) *spaHandler {
	sp := &spaHandler{cfg: cfg}
	if cfg.dev || cfg.static == nil {
		return sp
	}
	sp.root = findClientRoot(cfg.static)
	if b, err := fs.ReadFile(sp.root, "index.html"); err == nil {
		sp.index = injectToken(b, cfg.token)
	}
	return sp
}

// findClientRoot accepts the client files at the top of fsys or under
// build/client (the layout of web.Static).
func findClientRoot(fsys fs.FS) fs.FS {
	if _, err := fs.Stat(fsys, "index.html"); err == nil {
		return fsys
	}
	if sub, err := fs.Sub(fsys, "build/client"); err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			return sub
		}
	}
	return fsys
}

var tokenMetaRe = regexp.MustCompile(`(?i)<meta\s+name=["']drashta-token["'][^>]*>`)

// injectToken puts the token meta tag into the HTML. An existing placeholder
// tag is replaced; otherwise the tag goes before </head>.
func injectToken(page []byte, token string) []byte {
	tag := []byte(`<meta name="drashta-token" content="` + html.EscapeString(token) + `">`)
	if tokenMetaRe.Match(page) {
		return tokenMetaRe.ReplaceAllLiteral(page, tag)
	}
	lower := bytes.ToLower(page)
	for _, marker := range [][]byte{[]byte("</head>"), []byte("<body")} {
		if i := bytes.Index(lower, marker); i >= 0 {
			out := make([]byte, 0, len(page)+len(tag))
			out = append(out, page[:i]...)
			out = append(out, tag...)
			return append(out, page[i:]...)
		}
	}
	return append(append([]byte(nil), page...), tag...)
}

func (sp *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if p == "/api" || strings.HasPrefix(p, "/api/") {
		NotFound(w, "no such API route") // never answer an API path with the page
		return
	}
	if sp.cfg.dev {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "drashta -dev: the page is served by Vite on http://localhost:5173\n")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		MethodNotAllowed(w, []string{http.MethodGet, http.MethodHead})
		return
	}

	clean := path.Clean("/" + p)
	name := strings.TrimPrefix(clean, "/")
	if name == "" || name == "index.html" {
		sp.serveIndex(w, r)
		return
	}
	if hasDotSegment(name) {
		http.NotFound(w, r) // .keep, .DS_Store and friends are never served
		return
	}
	if sp.root != nil && fs.ValidPath(name) {
		if f, err := sp.root.Open(name); err == nil {
			info, err := f.Stat()
			if err == nil && info.Mode().IsRegular() {
				sp.serveFile(w, r, name, f)
				_ = f.Close()
				return
			}
			_ = f.Close()
		}
	}
	// Not a file. Missing assets must stay 404, or the browser gets HTML where it expects a script.
	if clean == "/assets" || strings.HasPrefix(clean, "/assets/") {
		http.NotFound(w, r)
		return
	}
	if path.Ext(clean) != "" && !strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.NotFound(w, r)
		return
	}
	sp.serveIndex(w, r)
}

func hasDotSegment(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func (sp *spaHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if !sp.cfg.checkAccess(w, r) {
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	if sp.index == nil {
		h.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "drashta: the web page is not built. Run `make web`, then rebuild drashta.\n")
		return
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", cacheIndex)
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Length", strconv.Itoa(len(sp.index)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(sp.index)
	}
}

func (sp *spaHandler) serveFile(w http.ResponseWriter, r *http.Request, name string, f fs.File) {
	h := w.Header()
	h.Set("Content-Type", contentTypeFor(name))
	h.Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", cacheAssets)
	} else {
		h.Set("Cache-Control", cacheOtherFile)
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(f)
		if err != nil {
			Internal(w, "could not read file")
			return
		}
		rs = bytes.NewReader(data)
	}
	http.ServeContent(w, r, name, time.Time{}, rs)
}

// extTypes is checked before the system MIME table, which differs between machines.
var extTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".wasm":        "application/wasm",
}

func contentTypeFor(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := extTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}
