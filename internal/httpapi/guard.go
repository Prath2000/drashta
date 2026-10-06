package httpapi

import (
	"crypto/subtle"
	"mime"
	"net/http"
	"strings"
)

// Limits and header names.
const (
	// TokenHeader carries the CSRF token on every mutating call.
	TokenHeader = "X-Drashta-Token"
	// DefaultMaxBody is the body cap for a mutating route (64 KB).
	DefaultMaxBody int64 = 64 << 10
	// DefaultUploadMaxBody is the body cap for a route flagged Upload (32 MB).
	DefaultUploadMaxBody int64 = 32 << 20
	// DevOrigin is the Vite dev server. It is an allowed Origin only in Dev mode.
	DevOrigin = "http://localhost:5173"
)

// RouteOptions tune the guard for one route.
type RouteOptions struct {
	// Mutating makes the route need the token even for a method that is
	// normally read-only. POST, PATCH, PUT and DELETE always need it.
	Mutating bool
	// Upload also accepts multipart/form-data and raises the body cap to
	// DefaultUploadMaxBody (unless MaxBody is set).
	Upload bool
	// MaxBody overrides the body cap in bytes. 0 means the default.
	MaxBody int64
}

func (ro RouteOptions) maxBody() int64 {
	switch {
	case ro.MaxBody > 0:
		return ro.MaxBody
	case ro.Upload:
		return DefaultUploadMaxBody
	default:
		return DefaultMaxBody
	}
}

// bodyMethod reports whether the method changes state, so the guard demands
// the JSON content type and the token.
func bodyMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// hostAllowed checks the Host header against localhost:<port> and 127.0.0.1:<port>
// (plus Options.AllowedHosts). This stops DNS rebinding.
func (s *Server) hostAllowed(host string) bool {
	s.hostMu.RLock()
	defer s.hostMu.RUnlock()
	_, ok := s.hosts[strings.ToLower(host)]
	return ok
}

// originAllowed checks an Origin header. Only http://<allowed host> passes,
// plus the Vite origin in Dev mode. "null" and anything else is refused.
func (s *Server) originAllowed(origin string) bool {
	if s.opts.Dev && origin == DevOrigin {
		return true
	}
	host, ok := strings.CutPrefix(origin, "http://")
	if !ok {
		return false
	}
	return s.hostAllowed(host)
}

// tokenOK compares the X-Drashta-Token header with the server token in constant time.
func (s *Server) tokenOK(r *http.Request) bool {
	got := r.Header.Get(TokenHeader)
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

// checkHostOrigin runs the two checks that apply to every /api request
// (and to index.html, which holds the token). It writes the 403 itself.
func (s *Server) checkHostOrigin(w http.ResponseWriter, r *http.Request) bool {
	if !s.hostAllowed(r.Host) {
		Forbidden(w, "forbidden host")
		return false
	}
	if vals := r.Header.Values("Origin"); len(vals) > 0 {
		if len(vals) != 1 || !s.originAllowed(vals[0]) {
			Forbidden(w, "forbidden origin")
			return false
		}
	}
	return true
}

// fetchSiteAllowed checks the Sec-Fetch-Site header the browser adds. A page
// on another site can still make a no-cors request (<img>, <link>, <video>)
// that carries no Origin header, so this is the check that stops it.
// "same-origin" and "none" (typed URL, bookmark) pass, and so does a request
// without the header (curl, old browsers). "cross-site" is refused, and so is
// "same-site" (another port or sub-domain of localhost), except in Dev mode
// where the Vite page on localhost:5173 is same-site.
func (s *Server) fetchSiteAllowed(r *http.Request) bool {
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "", "same-origin", "none":
		return true
	case "same-site":
		return s.opts.Dev
	}
	return false
}

// guard wraps an /api handler. In order it checks:
//  1. Host is ours (403);
//  2. Origin, if sent, is ours (403), and Sec-Fetch-Site, if sent, is
//     same-origin or none (403);
//  3. for POST/PATCH/PUT/DELETE (or a route flagged Mutating): the token (403);
//  4. for POST/PATCH/PUT/DELETE: Content-Type application/json, or
//     multipart/form-data on an Upload route (415);
//  5. the body is capped (413 when Content-Length is already too big; later
//     reads fail with *http.MaxBytesError, which ReadJSON turns into 413).
//
// It never sends CORS headers, so a browser never lets another site read a reply,
// and it sends Cross-Origin-Resource-Policy: same-origin, so another site cannot
// even embed one (an image from /raw, for example).
func (s *Server) guard(ro RouteOptions, next http.Handler) http.Handler {
	limit := ro.maxBody()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")

		if !s.checkHostOrigin(w, r) {
			return
		}
		if !s.fetchSiteAllowed(r) {
			Forbidden(w, "cross-site request")
			return
		}
		if ro.Mutating || bodyMethod(r.Method) {
			if !s.tokenOK(r) {
				Forbidden(w, "missing or wrong token")
				return
			}
		}
		if bodyMethod(r.Method) {
			if !contentTypeOK(r.Header.Get("Content-Type"), ro.Upload) {
				if ro.Upload {
					UnsupportedMediaType(w, "content type must be application/json or multipart/form-data")
				} else {
					UnsupportedMediaType(w, "content type must be application/json")
				}
				return
			}
			if r.ContentLength > limit {
				PayloadTooLarge(w, limit)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func contentTypeOK(header string, upload bool) bool {
	mt, _, err := mime.ParseMediaType(header)
	if err != nil {
		return false
	}
	return mt == "application/json" || (upload && mt == "multipart/form-data")
}
