package httpapi

// Server routes (the lsof side of drashta).
//
//	GET  /api/servers[?project=p]   {active:[server], recent:[server]}
//	GET  /api/servers/embed?port=N  {http, frameable, reason, title}  (can the page be shown in an iframe?)
//	POST /api/servers/kill          {pid, key, confirm:true}  ->  {ok}
//	POST /api/servers/restart       {key, confirm:true}       ->  {ok, log}
//
// server = {key, port, pid, name, cmd, cwd, projectId (null when outside every project), kind
// (dev|helper), title (the page <title>, when known), firstSeen, lastSeen, alive}.
// key is the stable "<cwd>|<cmd>" key of the servers package.
//
// Rules (03-architecture.md section 7):
//   - confirm must be true in the body, or the answer is 400 and nothing runs.
//   - Kill only works on a PID that is in the live set of the last lsof pass (the Manager checks it),
//     and a given key must match that PID. A PID that is not live is a 400.
//   - Restart replays a command that drashta itself recorded. An unknown key is a 404. A server that
//     is alive, or a restart still in its 10 second grace period, is a 409.
//   - Both are POST: they need the CSRF token and a JSON body (the guard checks that).
//   - Embed probes one port with a single GET on loopback (actions.ProbePage). The port must be in the live
//     server list (404 otherwise), so the route cannot be used to poke other ports; a bad port is a 400.
//     The parent origin is "http://" + the request Host (the guard already limits it to localhost or
//     127.0.0.1 plus the configured AllowedHosts). frameable is false for X-Frame-Options DENY or
//     SAMEORIGIN, or a CSP frame-ancestors that does not list that origin; reason says which. http is
//     false when nothing answers with HTTP (reason "not a web page"); title is the page <title>.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/probe/servers"
)

func (a *ProjectAPI) registerServers(s *Server) {
	s.Handle(http.MethodGet, "/api/servers", a.handleServers, false)
	s.Handle(http.MethodGet, "/api/servers/embed", a.handleEmbed, false)
	s.Handle(http.MethodPost, "/api/servers/kill", a.handleKill, true)
	s.Handle(http.MethodPost, "/api/servers/restart", a.handleRestart, true)
}

type pjServer struct {
	Key       string  `json:"key"`
	Port      int     `json:"port"`
	PID       int     `json:"pid"`
	Name      string  `json:"name"`
	Cmd       string  `json:"cmd"`
	CWD       string  `json:"cwd"`
	ProjectID *string `json:"projectId"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	FirstSeen string  `json:"firstSeen,omitempty"`
	LastSeen  string  `json:"lastSeen,omitempty"`
	Alive     bool    `json:"alive"`
}

// serverTitles returns the page titles of the running servers, by key and by port.
func (a *ProjectAPI) serverTitles(ctx context.Context) (byKey map[string]string, byPort map[int]string) {
	byKey, byPort = map[string]string{}, map[int]string{}
	if a.d.Store == nil {
		return
	}
	rows, err := a.d.Store.ProjServerTitles(ctx)
	if err != nil {
		a.log.Warn("server titles", "err", err)
		return
	}
	for _, t := range rows {
		byKey[t.Key] = t.Title
		byPort[t.Port] = t.Title
	}
	return
}

func (a *ProjectAPI) serverView(s servers.Server, byKey map[string]string, byPort map[int]string) pjServer {
	key := servers.Key(s)
	v := pjServer{
		Key: key, Port: s.Port, PID: s.PID, Name: s.Name, Cmd: s.Cmd, CWD: s.Cwd, Kind: s.Kind,
		FirstSeen: a.iso(s.FirstSeen), LastSeen: a.iso(s.LastSeen), Alive: s.Alive,
	}
	if v.Kind == "" {
		v.Kind = servers.KindOf(s.Name)
	}
	if s.ProjectID != "" {
		id := s.ProjectID
		v.ProjectID = &id
	}
	if t, ok := byKey[key]; ok {
		v.Title = t
	} else if s.Alive {
		v.Title = byPort[s.Port] // a port can be reused later, so only live servers use it
	}
	return v
}

// views converts a list. A non-empty project keeps only the servers of that project.
func (a *ProjectAPI) serverViews(ctx context.Context, list []servers.Server, project string) []pjServer {
	byKey, byPort := a.serverTitles(ctx)
	out := make([]pjServer, 0, len(list))
	for _, s := range list {
		if project != "" && s.ProjectID != project {
			continue
		}
		out = append(out, a.serverView(s, byKey, byPort))
	}
	return out
}

// serversOf returns the servers running now in one project.
func (a *ProjectAPI) serversOf(ctx context.Context, project string) []pjServer {
	if a.d.Servers == nil {
		return []pjServer{}
	}
	active, _ := a.d.Servers.Snapshot()
	return a.serverViews(ctx, active, project)
}

func (a *ProjectAPI) handleServers(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	if project != "" && a.d.Store != nil {
		_, ok, err := a.d.Store.ProjGet(r.Context(), project)
		if err != nil {
			a.fail(w, r, "project lookup", err)
			return
		}
		if !ok {
			NotFound(w, "no such project")
			return
		}
	}
	reply := map[string]any{"active": []pjServer{}, "recent": []pjServer{}}
	if a.d.Servers != nil {
		active, recent := a.d.Servers.Snapshot()
		reply["active"] = a.serverViews(r.Context(), active, project)
		reply["recent"] = a.serverViews(r.Context(), recent, project)
	}
	WriteJSON(w, http.StatusOK, reply)
}

// pjEmbed is the reply of GET /api/servers/embed.
type pjEmbed struct {
	HTTP      bool   `json:"http"`
	Frameable bool   `json:"frameable"`
	Reason    string `json:"reason"`
	Title     string `json:"title"`
}

func (a *ProjectAPI) handleEmbed(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 {
		BadRequest(w, "port must be a number from 1 to 65535")
		return
	}
	live := false
	if a.d.Servers != nil {
		active, _ := a.d.Servers.Snapshot()
		for _, s := range active {
			if s.Port == port {
				live = true
				break
			}
		}
	}
	if !live {
		NotFound(w, "no server is listening on that port")
		return
	}
	probe := a.d.ProbePage
	if probe == nil {
		probe = actions.ProbePage
	}
	info := probe(port, "http://"+r.Host)
	WriteJSON(w, http.StatusOK, pjEmbed{HTTP: info.HTTP, Frameable: info.Frameable, Reason: info.Reason, Title: info.Title})
}

type pjKillRequest struct {
	PID     int    `json:"pid"`
	Key     string `json:"key"`
	Confirm bool   `json:"confirm"`
}

type pjRestartRequest struct {
	Key     string `json:"key"`
	Confirm bool   `json:"confirm"`
}

func (a *ProjectAPI) handleKill(w http.ResponseWriter, r *http.Request) {
	var req pjKillRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if !req.Confirm {
		BadRequest(w, "confirm must be true to stop a server")
		return
	}
	if req.PID <= 1 {
		BadRequest(w, "pid is required")
		return
	}
	if len(req.Key) > 4096 {
		BadRequest(w, "key is too long")
		return
	}
	if a.d.Actions == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "server actions are not set up")
		return
	}
	if err := a.d.Actions.Kill(req.PID, req.Key, true); err != nil {
		a.actionError(w, r, "kill", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *ProjectAPI) handleRestart(w http.ResponseWriter, r *http.Request) {
	var req pjRestartRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if !req.Confirm {
		BadRequest(w, "confirm must be true to start a server again")
		return
	}
	if req.Key == "" || len(req.Key) > 4096 {
		BadRequest(w, "key is required")
		return
	}
	if a.d.Actions == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "server actions are not set up")
		return
	}
	logPath, err := a.d.Actions.Restart(req.Key, true)
	if err != nil {
		a.actionError(w, r, "restart", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "log": a.tilde(logPath)})
}

// tilde writes the home folder as "~" so the reply reads like the docs.
func (a *ProjectAPI) tilde(p string) string {
	if a.home != "" && (p == a.home || strings.HasPrefix(p, a.home+"/")) {
		return "~" + strings.TrimPrefix(p, a.home)
	}
	return p
}

// actionError maps the errors of actions.Manager (see the actions package doc).
func (a *ProjectAPI) actionError(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, actions.ErrConfirm), errors.Is(err, actions.ErrNotLive), errors.Is(err, actions.ErrInvalid),
		errors.Is(err, actions.ErrForbidden), errors.Is(err, actions.ErrAction):
		BadRequest(w, pjShortErr(err))
	case errors.Is(err, actions.ErrNotFound):
		NotFound(w, "no such server")
	case errors.Is(err, actions.ErrConflict):
		Conflict(w, "the server is already running or a restart is pending")
	default:
		a.log.Error("server action", "what", what, "err", err)
		Internal(w, what+" failed")
	}
}
