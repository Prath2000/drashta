// Package httpapi is the HTTP core of drashta: the guard, the JSON helpers,
// the core endpoints, live updates over SSE and the single-page app.
//
// This file and server.go, guard.go, spa.go are the "core". Other files in this
// package (api_*.go) add the data routes. They register routes with
// Server.Handle / Server.Mount and use the helpers below. They never edit the
// core files. Rules come from 03-architecture.md sections 2, 6, 8, 9 and
// 04-data-model.md section 6.
//
// # How an api_*.go file plugs in
//
//	func RegisterHome(s *Server, h HomeSource) {      // HomeSource: your own small interface
//	    s.Handle("GET", "/api/home", func(w http.ResponseWriter, r *http.Request) {
//	        WriteJSON(w, 200, h.Home(r.Context()))
//	    }, false)
//	    s.Handle("POST", "/api/refresh", func(w http.ResponseWriter, r *http.Request) {
//	        var req struct{ ProjectID string `json:"projectId"` }
//	        if !ReadJSON(w, r, &req) { return }        // ReadJSON already wrote the 400/413
//	        ...
//	    }, true)
//	}
//
// Patterns are full paths and must start with "/api/" (Handle panics
// otherwise). Go 1.22 syntax works: "/api/projects/{p}/tasks/{id}", read with
// r.PathValue("p"). Every route registered this way is behind the guard.
// Publish live events with s.Hub().Publish(live.ProjectEvent(...)).
//
// # Exported API
//
// Server and options:
//   - New(Options) *Server              build the server and register the core routes.
//   - Options                           Addr (default 127.0.0.1:4455), Dev, Hub, Static, Token, AllowedHosts, Now, Counts, Logger, PingEvery, NewTicker.
//   - Deps                              plain struct {Hub, Logger, Now}; get it from Server.Deps().
//   - (*Server).Handler() http.Handler  root handler (for http.Server or httptest).
//   - (*Server).Handle(method, pattern, h http.HandlerFunc, mutating bool)
//     register a guarded route. POST, PATCH, PUT, DELETE always need the token and a JSON body.
//     mutating=true adds the token rule to other methods too.
//   - (*Server).HandleOpts(method, pattern, h, RouteOptions)  same, with the upload flag and body cap.
//   - (*Server).Mount(pattern, http.Handler)                  guarded handler for a subtree such as "/api/x/".
//   - (*Server).MountOpts(pattern, http.Handler, RouteOptions)
//   - RouteOptions                      {Mutating bool; Upload bool; MaxBody int64}. Upload accepts multipart/form-data and
//     raises the default cap to 32 MB. MaxBody sets the cap in bytes (default 64 KB).
//   - (*Server).Hub() *live.Hub, Deps() Deps, Token() string, AllowHost(host string), Addr() string
//   - (*Server).Listen() (net.Listener, error)  binds loopback IPv4 only; refuses 0.0.0.0, [::], other IPs.
//   - (*Server).Serve(net.Listener) error, ListenAndServe() error, Shutdown(ctx) error
//     Shutdown ends the SSE streams first, then stops the server.
//   - AlreadyRunning(addr string) (bool, error)  true: our own drashta answers /api/health (exit 0).
//     false,nil: port free. false,err: another program holds the port (exit 1).
//
// JSON helpers (reply shape {error:{code,message}}):
//   - WriteJSON(w, status, v)
//   - WriteError(w, status, code, message)
//   - BadRequest 400, Forbidden 403, NotFound 404, Conflict 409, Unprocessable 422, Internal 500
//     each is func(w, message)
//   - MethodNotAllowed(w, allow []string) 405, UnsupportedMediaType(w, msg) 415, PayloadTooLarge(w, limit) 413
//   - ReadJSON(w, r, &v) bool           decode the body; on failure it already replied (400, or 413 over the cap).
//
// Constants: DefaultAddr, DefaultPingEvery, DefaultMaxBody, DefaultUploadMaxBody, TokenHeader, DevOrigin.
//
// # Core routes
//
//   - GET  /api/health    {app:"drashta", ok:true}. Used for single-instance detection.
//   - GET  /healthz       plain text "ok" (alias for scripts and docs). Host/Origin checked, no token.
//   - GET  /blank         an empty transparent HTML page (Plash desktop mode N). Host/Origin checked, no token.
//   - GET  /api/session   {token}. Always on (the token is also in index.html). A tab that outlived a restart
//     fetches the new token here and retries instead of reloading. Guarded like every /api route.
//   - GET  /api/events    SSE. First frame "retry: 3000", then event "hello" {now, counts, conn}.
//     Each later frame has "id: <n>", "event: <type>", "data: <json>". ": ping" every 25 s.
//     Send Last-Event-ID (EventSource does this by itself) to get missed events, or a "resync" event.
//     Listen with addEventListener for the named events; they are not "message" events.
//   - GET  /api/state?since=<id>  {resync, head, events:[{id,type,data}]}. resync is true when the id fell out
//     of the ring, came from before a restart, or is missing/0.
//   - POST /api/presence  {visible:bool, conn?:string}. conn is the id from the hello event; without it the flag
//     is applied to every open stream. 404 for an unknown conn.
//   - any other /api path: JSON 404, or 405 with an Allow header. Never the page.
//   - everything else (GET/HEAD): the SPA. See spa.go.
//
// The data routes live in api_*.go. One that the page depends on for layout decisions:
// GET /api/servers/embed?port=N (api_servers.go) answers {http, frameable, reason, title} for a
// live server's port: can its page be shown in an iframe on this page (X-Frame-Options and CSP
// frame-ancestors, judged by actions.ProbePage)? 400 for a bad port, 404 when the port is not
// in the live server list. ProjectDeps.ProbePage replaces the probe in tests.
//
// GET /api/projects/{p}/files/{path...} (api_files.go) sends the same bytes and headers as
// /api/projects/{p}/raw?path=, with the path in the URL, so a .gltf finds its .bin and textures by
// relative URL. A literal ".." is cleaned by the mux (a redirect); an encoded one is a 400.
// GET /api/projects/{p}/dwg/layers?path= (JSON) and /dwg/entities?path= (JSONL, one entity per
// line) (api_dwg.go) read a .dwg through ProjectDeps.Drawings, which runs chitralipi and caches
// the answers. 400 for a path that is not a .dwg, 404 for a missing file, 503 "unavailable" when
// chitralipi is missing (or no reader is set up) and 422 "bad_drawing" when it cannot read the file.
//
// The desktop mode buttons (api_desktop.go, registered by RegisterTracker): GET /api/desktop gives
// {available, mode, modes}; POST /api/desktop {mode} runs actions.Config.PlanDesktop (the
// desktop_script from config.toml, argv only, one run at a time) and answers the same shape.
// mode is read from TrackerDeps.DesktopState ("" when unknown). 400 for an unknown mode, 404
// when the script is missing, 500 action_failed when it fails.
//
// # Guard (every /api route, SSE included)
//
//  1. Host must be localhost:<port> or 127.0.0.1:<port> (plus Options.AllowedHosts). 403 otherwise.
//  2. Origin, if sent, must be http://<one of those hosts>; in Dev also http://localhost:5173. 403.
//     Sec-Fetch-Site, if sent, must be same-origin or none (same-site too in Dev). 403.
//     Every reply carries Cross-Origin-Resource-Policy: same-origin (so /raw cannot be embedded elsewhere).
//  3. POST/PATCH/PUT/DELETE need header X-Drashta-Token (hex of the token, constant-time compare). 403.
//  4. POST/PATCH/PUT/DELETE need Content-Type application/json (multipart/form-data too on Upload routes). 415.
//     This includes DELETE without a body: the web client must still send the header.
//  5. The body is capped (64 KB by default). 413.
//
// No CORS header is ever sent. API replies carry Cache-Control: no-store.
//
// # SPA (spa.go)
//
// Options.Static is an fs.FS with index.html at its root (web.Client()), or a
// build/client folder inside it (web.Static). index.html is read once, a
// <meta name="drashta-token"> is put in it and it is served only for an
// allowed Host and Origin. /assets/* is immutable for a year, everything else
// is no-cache. Unknown GETs outside /api and /assets get index.html. In Dev
// nothing static is served. If the client is not built, the page answers 503
// with a hint to run make web.
//
// # Names taken in this package by the core
//
// Test helpers in the core's *_test.go files start with "core". Unexported
// names in the core files (do not reuse): bodyMethod, contentTypeOK, errorEnvelope,
// errorDetail, errorBody, stateReply, presenceRequest, appendSSE, parseLastEventID,
// mustAPIPattern, spaConfig, spaHandler, newSPA, findClientRoot, tokenMetaRe,
// injectToken, hasDotSegment, extTypes, contentTypeFor, cacheIndex, cacheAssets,
// cacheOtherFile.
package httpapi
