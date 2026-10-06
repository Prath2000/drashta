package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Prath2000/drashta/internal/live"
)

// Defaults.
const (
	// DefaultAddr is where drashta listens (decision S2).
	DefaultAddr = "127.0.0.1:4455"
	// DefaultPingEvery is the SSE keep-alive interval.
	DefaultPingEvery = 25 * time.Second
)

// Options configure a Server. Every field is optional.
type Options struct {
	// Addr is the listen address. Default 127.0.0.1:4455. Only a loopback
	// IPv4 address (or "localhost") is accepted; Listen refuses anything else.
	// Port 0 picks a free port (tests); the allowed Host values follow it.
	Addr string
	// Dev is the -dev flag: allow Origin http://localhost:5173 (and
	// Sec-Fetch-Site same-site), and serve no static files (Vite serves the page).
	Dev bool
	// Hub is the event hub. A new one is made if nil.
	Hub *live.Hub
	// Static is the built web client (web.Client()). index.html must be at its
	// root; a "build/client" folder inside it is found automatically.
	Static fs.FS
	// Token is the CSRF token seed. Empty means 32 random bytes. The value
	// the page sends is hex(Token); read it back with Server.Token.
	Token []byte
	// AllowedHosts adds Host header values (like "localhost:5173") to the two
	// default ones, localhost:<port> and 127.0.0.1:<port>.
	AllowedHosts []string
	// Now is the clock for the hello event. Default time.Now.
	Now func() time.Time
	// Counts supplies the counts in the hello event. Nil means all zero.
	Counts func() live.Counts
	// Logger is used for errors and panics. Default slog.Default().
	Logger *slog.Logger
	// PingEvery is the SSE ping interval. Default 25 s.
	PingEvery time.Duration
	// NewTicker makes the ping ticker. Tests swap it for a fake. Default is a
	// time.Ticker. It must return a stop function.
	NewTicker func(d time.Duration) (tick <-chan time.Time, stop func())
}

// Deps is what every api_*.go file may need besides its own services.
// It is a plain struct. Each api file declares its own small interfaces and a
// RegisterXxx(s *Server, ...) function that takes them, so nothing here has to
// import the store or the probes. Main builds them and calls the Register
// functions. Get this struct from Server.Deps.
type Deps struct {
	Hub    *live.Hub
	Logger *slog.Logger
	Now    func() time.Time
}

// Server is the HTTP core: guard, JSON helpers, the core endpoints, the SPA.
type Server struct {
	opts  Options
	log   *slog.Logger
	hub   *live.Hub
	now   func() time.Time
	mux   *http.ServeMux
	root  http.Handler
	token string // hex text of the token; this is what the page sends

	hostMu sync.RWMutex
	hosts  map[string]struct{}

	closing   chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	ln      net.Listener
	httpSrv *http.Server
}

// New builds the server and registers the core routes:
// /api/health, /api/session, /api/events, /api/state, /api/presence,
// a JSON 404/405 for any other /api path, and the SPA for everything else.
func New(opts Options) *Server {
	if opts.Addr == "" {
		opts.Addr = DefaultAddr
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PingEvery <= 0 {
		opts.PingEvery = DefaultPingEvery
	}
	if opts.NewTicker == nil {
		opts.NewTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	s := &Server{
		opts:    opts,
		log:     opts.Logger,
		hub:     opts.Hub,
		now:     opts.Now,
		mux:     http.NewServeMux(),
		hosts:   map[string]struct{}{},
		closing: make(chan struct{}),
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.hub == nil {
		s.hub = live.NewHub(live.HubOptions{Now: opts.Now})
	}
	seed := opts.Token
	if len(seed) == 0 {
		seed = make([]byte, 32)
		if _, err := rand.Read(seed); err != nil {
			panic("httpapi: no randomness for the token: " + err.Error())
		}
	}
	s.token = hex.EncodeToString(seed)

	if _, port, err := net.SplitHostPort(opts.Addr); err == nil && port != "0" {
		s.addHostsForPort(port)
	}
	for _, h := range opts.AllowedHosts {
		s.AllowHost(h)
	}

	s.routes()
	s.root = s.recoverMiddleware(s.mux)
	return s
}

func (s *Server) routes() {
	s.Handle(http.MethodGet, "/api/health", s.handleHealth, false)
	// /healthz is a plain-text alias for scripts and the setup docs. It is outside
	// /api (no token rule), but the Host/Origin check still applies.
	s.mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealthz))
	// /blank is an empty, transparent page: Plash shows it to reveal the normal
	// macOS wallpaper (desktop mode N, contrib/desktop/README.md).
	s.mux.Handle("GET /blank", http.HandlerFunc(s.handleBlank))
	// /api/session hands the page the current token. A tab that outlived a
	// server restart uses it to pick up the new token instead of reloading
	// (and losing drafts). It is as safe as index.html, which holds the same
	// token: the guard checks Host, Origin and Sec-Fetch-Site, no CORS headers
	// are sent, and CORP same-origin stops embedding, so another site can never
	// read the reply.
	s.Handle(http.MethodGet, "/api/session", s.handleSession, false)
	s.Handle(http.MethodGet, "/api/events", s.handleEvents, false)
	s.Handle(http.MethodGet, "/api/state", s.handleState, false)
	s.Handle(http.MethodPost, "/api/presence", s.handlePresence, true)
	// Anything else under /api is a JSON 404 or 405, never the SPA page.
	s.Mount("/api/", http.HandlerFunc(s.handleAPIFallback))
	s.mux.Handle("/api", s.guard(RouteOptions{}, http.HandlerFunc(s.handleAPIFallback)))

	s.mux.Handle("/", newSPA(spaConfig{
		static:      s.opts.Static,
		dev:         s.opts.Dev,
		token:       s.token,
		checkAccess: s.checkHostOrigin,
	}))
}

// Handler is the root handler: pass it to http.Server or httptest.
func (s *Server) Handler() http.Handler { return s.root }

// Deps returns the shared services for api_*.go files.
func (s *Server) Deps() Deps { return Deps{Hub: s.hub, Logger: s.log, Now: s.now} }

// Hub returns the event hub.
func (s *Server) Hub() *live.Hub { return s.hub }

// Token returns the CSRF token text the page must send as X-Drashta-Token.
func (s *Server) Token() string { return s.token }

// Handle registers a guarded /api route. method is an HTTP method, pattern is
// the full path starting with "/api/" and may use {name} wildcards (Go 1.22
// mux syntax), for example Handle("GET", "/api/projects/{p}/tree", h, false).
// POST, PATCH, PUT and DELETE always need the token and a JSON body whatever
// mutating says; mutating=true adds the same token rule to other methods.
// It panics on a bad pattern, like http.ServeMux.
func (s *Server) Handle(method, pattern string, h http.HandlerFunc, mutating bool) {
	s.HandleOpts(method, pattern, h, RouteOptions{Mutating: mutating})
}

// HandleOpts is Handle with the full RouteOptions (upload flag, body cap).
func (s *Server) HandleOpts(method, pattern string, h http.HandlerFunc, ro RouteOptions) {
	mustAPIPattern(pattern)
	s.mux.Handle(method+" "+pattern, s.guard(ro, h))
}

// Mount registers a guarded handler for a pattern under /api/, for example a
// subtree "/api/projects/" that routes by itself. The pattern may start with a
// method ("GET /api/x"). The guard rules are the same as for Handle.
func (s *Server) Mount(pattern string, h http.Handler) {
	s.MountOpts(pattern, h, RouteOptions{})
}

// MountOpts is Mount with RouteOptions.
func (s *Server) MountOpts(pattern string, h http.Handler, ro RouteOptions) {
	path := pattern
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		path = strings.TrimSpace(rest)
	}
	mustAPIPattern(path)
	s.mux.Handle(pattern, s.guard(ro, h))
}

func mustAPIPattern(path string) {
	if !strings.HasPrefix(path, "/api/") {
		panic(fmt.Sprintf("httpapi: route %q must start with /api/", path))
	}
}

// AllowHost adds one Host header value (like "localhost:5173") to the allowed set.
func (s *Server) AllowHost(host string) {
	s.hostMu.Lock()
	defer s.hostMu.Unlock()
	s.hosts[strings.ToLower(host)] = struct{}{}
}

func (s *Server) addHostsForPort(port string) {
	s.AllowHost("localhost:" + port)
	s.AllowHost("127.0.0.1:" + port)
}

// recoverMiddleware turns a handler panic into a JSON 500 and a log line.
// It does not wrap the ResponseWriter, so streaming (SSE) keeps working.
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.log.Error("handler panic", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(rec))
				Internal(w, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---- lifecycle ----

// ErrPortBusy is wrapped by the errors that say the port is taken by another program.
var ErrPortBusy = errors.New("port is busy")

// Listen binds the address. It refuses anything but loopback IPv4, so drashta
// can never listen on 0.0.0.0 (section 8). With port 0 it picks a free port and
// allows the matching Host values.
func (s *Server) Listen() (net.Listener, error) {
	host, port, err := net.SplitHostPort(s.opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("httpapi: bad address %q: %w", s.opts.Addr, err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("httpapi: refusing to listen on %q: only 127.0.0.1 is allowed", s.opts.Addr)
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	s.addHostsForPort(strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	return ln, nil
}

// Serve serves on ln until Shutdown. It returns nil after a clean Shutdown.
func (s *Server) Serve(ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.root,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: it would cut SSE streams.
		ErrorLog: slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ListenAndServe is Listen then Serve.
func (s *Server) ListenAndServe() error {
	ln, err := s.Listen()
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Addr returns the bound address once Listen has run, else the configured one.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.opts.Addr
}

// Shutdown ends every SSE stream first (they never finish on their own), then
// stops the HTTP server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.closing) })
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	err := srv.Shutdown(ctx)
	if err != nil {
		_ = srv.Close()
	}
	return err
}

// ---- JSON helpers (shared with api_*.go) ----

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		b, _ = json.Marshal(errorBody("internal", "could not encode the reply"))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func errorBody(code, msg string) errorEnvelope {
	return errorEnvelope{Error: errorDetail{Code: code, Message: msg}}
}

// WriteError writes the error shape {error:{code,message}}.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorBody(code, message))
}

// BadRequest sends 400.
func BadRequest(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusBadRequest, "bad_request", msg)
}

// Forbidden sends 403.
func Forbidden(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusForbidden, "forbidden", msg)
}

// NotFound sends 404.
func NotFound(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusNotFound, "not_found", msg)
}

// Conflict sends 409 (for example a revision conflict).
func Conflict(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusConflict, "conflict", msg)
}

// Unprocessable sends 422 (the JSON is valid but the values are not).
func Unprocessable(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusUnprocessableEntity, "unprocessable", msg)
}

// Internal sends 500.
func Internal(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusInternalServerError, "internal", msg)
}

// MethodNotAllowed sends 405 with an Allow header.
func MethodNotAllowed(w http.ResponseWriter, allow []string) {
	if len(allow) > 0 {
		w.Header().Set("Allow", strings.Join(allow, ", "))
	}
	WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

// UnsupportedMediaType sends 415.
func UnsupportedMediaType(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", msg)
}

// PayloadTooLarge sends 413.
func PayloadTooLarge(w http.ResponseWriter, limit int64) {
	WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
		fmt.Sprintf("request body is larger than %d bytes", limit))
}

// ReadJSON decodes the request body into v. On failure it writes the error
// reply itself (413 too large, 400 bad JSON) and returns false; the caller
// just returns. It rejects data after the first JSON value.
func ReadJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	err := dec.Decode(v)
	if err == nil {
		// Only whitespace may follow.
		if _, err2 := dec.Token(); err2 != io.EOF {
			if err2 == nil {
				err = errors.New("unexpected data after the JSON value")
			} else {
				err = err2
			}
		}
	}
	if err == nil {
		return true
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		PayloadTooLarge(w, tooBig.Limit)
	case errors.Is(err, io.EOF):
		BadRequest(w, "request body is empty")
	default:
		BadRequest(w, "invalid JSON: "+err.Error())
	}
	return false
}

// ---- core endpoints ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"app": "drashta", "ok": true})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !s.checkHostOrigin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok")
}

const blankPage = `<!doctype html><html style="background:transparent"><head><meta charset="utf-8"><title>blank</title></head><body style="background:transparent"></body></html>`

func (s *Server) handleBlank(w http.ResponseWriter, r *http.Request) {
	if !s.checkHostOrigin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, blankPage)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"token": s.token})
}

// handleAPIFallback answers any /api path nobody registered: 405 with an Allow
// header if the path exists for another method, else 404. It is JSON, so the
// SPA fallback never hides a wrong API URL.
func (s *Server) handleAPIFallback(w http.ResponseWriter, r *http.Request) {
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if m == r.Method {
			continue
		}
		probe := &http.Request{Method: m, URL: &url.URL{Path: r.URL.Path}, Host: r.Host, Header: http.Header{}}
		if _, pat := s.mux.Handler(probe); pat != "" && pat != "/api/" && pat != "/api" {
			allow = append(allow, m)
		}
	}
	if len(allow) > 0 {
		MethodNotAllowed(w, allow)
		return
	}
	NotFound(w, "no such API route")
}

type stateReply struct {
	Resync bool         `json:"resync"`
	Head   uint64       `json:"head"`
	Events []live.Event `json:"events"`
}

// handleState answers GET /api/state?since=<id>: the events the page missed, or
// {resync:true}. A missing or zero "since" means the page knows nothing: resync.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	var since uint64
	if q := r.URL.Query().Get("since"); q != "" {
		n, err := strconv.ParseUint(q, 10, 64)
		if err != nil {
			BadRequest(w, "since must be a non-negative integer")
			return
		}
		since = n
	}
	events, resync, head := s.hub.Since(since)
	if since == 0 {
		resync = true
	}
	if resync || events == nil {
		events = []live.Event{}
	}
	WriteJSON(w, http.StatusOK, stateReply{Resync: resync, Head: head, Events: events})
}

type presenceRequest struct {
	Visible *bool  `json:"visible"`
	Conn    string `json:"conn"`
}

// handlePresence answers POST /api/presence {visible, conn?}. conn is the id from
// the hello event. Without it the flag is applied to every open stream.
func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	var req presenceRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Visible == nil {
		Unprocessable(w, "visible is required")
		return
	}
	if req.Conn == "" {
		s.hub.SetAllVisible(*req.Visible)
	} else if !s.hub.SetVisible(req.Conn, *req.Visible) {
		NotFound(w, "unknown connection")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleEvents is the SSE stream (GET /api/events).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	lastID, ok := parseLastEventID(r)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // a stream has no write deadline

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no") // do not let a proxy buffer the stream
	w.WriteHeader(http.StatusOK)

	conn := s.hub.Connect(lastID)
	defer conn.Close()
	tick, stopTick := s.opts.NewTicker(s.opts.PingEvery)
	defer stopTick()

	var counts live.Counts
	if s.opts.Counts != nil {
		counts = s.opts.Counts()
	}
	hello := live.HelloEvent(live.Hello{Now: s.now(), Counts: counts, Conn: conn.ID})

	// The page reconnects by itself after 3 s.
	out := &bytes.Buffer{}
	out.WriteString("retry: 3000\n\n")
	// hello carries an id only when the page starts over (first connect or
	// resync): then "head" is the right place to continue from. When we replay,
	// each replayed event carries its own id and hello must not move the cursor.
	resync := conn.Resync || !ok
	if (lastID == 0 && ok) || resync {
		appendSSE(out, conn.Head, true, hello)
	} else {
		appendSSE(out, 0, false, hello)
	}
	if resync {
		appendSSE(out, conn.Head, true, live.ResyncEvent())
	} else {
		for _, e := range conn.Replay {
			appendSSE(out, e.ID, true, e)
		}
	}
	if !s.flushWrite(w, rc, out) {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.closing:
			return
		case e, open := <-conn.Events:
			if !open {
				return // closed by the hub (slow stream or shutdown): the page reconnects
			}
			out.Reset()
			appendSSE(out, e.ID, true, e)
			if !s.flushWrite(w, rc, out) {
				return
			}
		case <-tick:
			out.Reset()
			out.WriteString(": ping\n\n")
			if !s.flushWrite(w, rc, out) {
				return
			}
		}
	}
}

// parseLastEventID reads the Last-Event-ID header (or ?lastEventId= when a
// client cannot set headers). ok is false when a value is present but is not a
// number: the caller then treats the page as needing a resync.
func parseLastEventID(r *http.Request) (id uint64, ok bool) {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("lastEventId")
	}
	if v == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (s *Server) flushWrite(w http.ResponseWriter, rc *http.ResponseController, b *bytes.Buffer) bool {
	if _, err := w.Write(b.Bytes()); err != nil {
		return false
	}
	if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return false
	}
	return true
}

// appendSSE writes one SSE frame. Data with line breaks is split over several
// "data:" lines, as the SSE format requires.
func appendSSE(b *bytes.Buffer, id uint64, withID bool, e live.Event) {
	if withID {
		fmt.Fprintf(b, "id: %d\n", id)
	}
	fmt.Fprintf(b, "event: %s\n", e.Type)
	data := e.Data
	if len(data) == 0 {
		data = []byte("{}")
	}
	for _, line := range bytes.Split(bytes.ReplaceAll(data, []byte("\r"), nil), []byte("\n")) {
		b.WriteString("data: ")
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// ---- single instance ----

// AlreadyRunning tells main whether another drashta already holds addr.
//
//   - Nothing listens on addr: (false, nil). Go ahead and start.
//   - GET /api/health answers {app:"drashta", ok:true}: (true, nil). Exit 0.
//   - Something else holds the port, or it does not answer like drashta:
//     (false, err). Log it and exit 1.
func AlreadyRunning(addr string) (bool, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false, fmt.Errorf("httpapi: bad address %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	target := net.JoinHostPort(host, port)

	c, err := (&net.Dialer{Timeout: 500 * time.Millisecond}).Dial("tcp", target)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return false, nil
		}
		return false, fmt.Errorf("httpapi: cannot check %s: %w", target, err)
	}
	_ = c.Close()

	// Never use a proxy from the environment for a loopback check.
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	}
	resp, err := client.Get("http://" + target + "/api/health")
	if err != nil {
		return false, fmt.Errorf("httpapi: port %s is in use by another program: %w (%v)", port, ErrPortBusy, err)
	}
	defer resp.Body.Close()
	var body struct {
		App string `json:"app"`
		OK  bool   `json:"ok"`
	}
	if resp.StatusCode == http.StatusOK &&
		json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) == nil &&
		body.App == "drashta" && body.OK {
		return true, nil
	}
	return false, fmt.Errorf("httpapi: port %s is in use by another program (health answered %d): %w", port, resp.StatusCode, ErrPortBusy)
}
