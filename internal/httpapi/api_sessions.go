package httpapi

// Claude session routes.
//
//	GET /api/projects/{p}/sessions?cursor=&limit=50
//	    {sessions:[{id,profile,start,end,title,turns,kb,tools,edited,tasks,reason,cwd}], next}
//	    reason says why the session counts here: "cwd" (it ran in this project), "mention" (it
//	    talked about the project) or "edit" (it edited files here). Newest first. The cursor is
//	    opaque ("s.<unix>.<id>"). next is null on the last page.
//	GET /api/sessions/{id}?cursor=&limit=40&roles=user,claude,tool
//	    {session, turns:[{i,role,ts,text,tool}], next}
//	    The transcript is read live from the .jsonl file. The path comes from the index, never from
//	    the request. The cursor is a byte offset (the "next" of the page before). next is null at
//	    the end of the file. Text is redacted by the claude probe.

import (
	"errors"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Prath2000/drashta/internal/probe/claude"
	"github.com/Prath2000/drashta/internal/store"
)

const (
	pjSessionsDefault = 50
	pjSessionsMax     = 200
)

var (
	pjSessionIDRe  = regexp.MustCompile(`^[0-9a-fA-F][0-9a-fA-F-]{7,63}$`)
	pjSessionCurRe = regexp.MustCompile(`^s\.([0-9]{1,12})\.([0-9a-fA-F-]{1,64})$`)
)

func (a *ProjectAPI) registerSessions(s *Server) {
	s.Handle(http.MethodGet, "/api/projects/{p}/sessions", a.handleProjectSessions, false)
	s.Handle(http.MethodGet, "/api/sessions/{id}", a.handleSession, false)
}

type pjSession struct {
	ID      string         `json:"id"`
	Profile string         `json:"profile"`
	Start   string         `json:"start"`
	End     *string        `json:"end"`
	Title   string         `json:"title"`
	Turns   int            `json:"turns"`
	KB      int            `json:"kb"`
	Tools   map[string]int `json:"tools"`
	Edited  []string       `json:"edited"`
	Tasks   []string       `json:"tasks"`
	Reason  string         `json:"reason"`
	CWD     string         `json:"cwd"`
}

func (a *ProjectAPI) sessionView(s store.ProjSession, refs []store.ProjTaskRef, project string) pjSession {
	return pjSession{
		ID: s.ID, Profile: s.Profile, Start: a.iso(s.Start), End: a.isoPtr(s.End), Title: s.Title,
		Turns: s.Turns, KB: s.KB, Tools: s.Tools, Edited: s.Edited,
		Tasks: pjTasksFor(refs, project, "session", s.ID, 8), Reason: s.Reason, CWD: s.CWD,
	}
}

func (a *ProjectAPI) handleProjectSessions(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.lookup(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, bad := pjIntParam(q, "limit", pjSessionsDefault, 1, pjSessionsMax)
	if bad != "" {
		BadRequest(w, bad)
		return
	}
	var cur store.ProjCursor
	if c := q.Get("cursor"); c != "" {
		m := pjSessionCurRe.FindStringSubmatch(c)
		if m == nil {
			BadRequest(w, "bad cursor")
			return
		}
		unix, _ := strconv.ParseInt(m[1], 10, 64)
		cur = store.ProjCursor{Set: true, Unix: unix, ID: m[2]}
	}
	ctx := r.Context()
	rows, err := a.d.Store.ProjSessionsPage(ctx, pr.Row.ID, cur, limit+1)
	if err != nil {
		a.fail(w, r, "sessions", err)
		return
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		s := "s." + strconv.FormatInt(last.SortKey(), 10) + "." + last.ID
		next = &s
	}
	refs, err := a.d.Store.ProjTaskRefs(ctx, pr.Row.ID, "session")
	if err != nil {
		a.fail(w, r, "task links", err)
		return
	}
	out := make([]pjSession, 0, len(rows))
	for _, s := range rows {
		out = append(out, a.sessionView(s, refs, pr.Row.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"sessions": out, "next": next})
}

type pjTurn struct {
	I    int    `json:"i"`
	Role string `json:"role"`
	TS   string `json:"ts"`
	Text string `json:"text"`
	Tool string `json:"tool,omitempty"`
}

func (a *ProjectAPI) handleSession(w http.ResponseWriter, r *http.Request) {
	if a.d.Store == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "the index is not open")
		return
	}
	id := r.PathValue("id")
	if !pjSessionIDRe.MatchString(id) {
		BadRequest(w, "bad session id")
		return
	}
	q := r.URL.Query()
	limit, bad := pjIntParam(q, "limit", claude.PageDefault, 1, claude.PageMax)
	if bad != "" {
		BadRequest(w, bad)
		return
	}
	var offset int64
	if c := q.Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n < 0 {
			BadRequest(w, "bad cursor")
			return
		}
		offset = n
	}
	var roles []string
	if rs := q.Get("roles"); rs != "" {
		for _, role := range strings.Split(rs, ",") {
			role = strings.TrimSpace(role)
			if role != "user" && role != "claude" && role != "tool" {
				BadRequest(w, "roles must be a list of user, claude, tool")
				return
			}
			roles = append(roles, role)
		}
	}

	ctx := r.Context()
	row, found, err := a.d.Store.ProjSessionByID(ctx, id)
	if err != nil {
		a.fail(w, r, "session lookup", err)
		return
	}
	if !found {
		NotFound(w, "no such session")
		return
	}
	if a.d.Claude == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "the transcript reader is not set up")
		return
	}
	if row.JSONLPath == "" {
		NotFound(w, "the transcript file is not known")
		return
	}
	page, err := a.d.Claude.Transcript(ctx, row.JSONLPath, offset, limit, roles)
	switch {
	case err == nil:
	case errors.Is(err, claude.ErrBadOffset):
		BadRequest(w, "the cursor does not point at the start of a line")
		return
	case errors.Is(err, claude.ErrNotTranscript):
		Forbidden(w, "the transcript file is outside the Claude profile folders")
		return
	case errors.Is(err, fs.ErrNotExist):
		NotFound(w, "the transcript file is gone")
		return
	default:
		a.fail(w, r, "transcript", err)
		return
	}

	turns := make([]pjTurn, 0, len(page.Turns))
	for _, t := range page.Turns {
		turns = append(turns, pjTurn{I: t.I, Role: t.Role, TS: a.iso(t.TS), Text: t.Text, Tool: t.Tool})
	}
	var next *string
	if !page.Done {
		s := strconv.FormatInt(page.Next, 10)
		next = &s
	}
	var refs []store.ProjTaskRef
	if row.ProjectID != "" {
		if refs, err = a.d.Store.ProjTaskRefs(ctx, row.ProjectID, "session"); err != nil {
			a.fail(w, r, "task links", err)
			return
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"session": a.sessionView(row, refs, row.ProjectID), "turns": turns, "next": next,
	})
}
