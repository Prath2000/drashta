package httpapi

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Prath2000/drashta/internal/store"
)

type pjtSession = store.Session

func pjtProject(id, path string) store.Project {
	return store.Project{ID: id, Name: id, Group: "g", Path: path}
}

type pjtSessionsReply struct {
	Sessions []struct {
		ID, Profile, Start string
		End                *string
		Title              string
		Turns, KB          int
		Tools              map[string]int
		Edited, Tasks      []string
		Reason, CWD        string
	}
	Next *string
}

func TestProjectSessionsList(t *testing.T) {
	e := pjtNew(t, nil)
	rec := e.get("/api/projects/alpha/sessions")
	pjtWant(t, rec, 200)
	var r pjtSessionsReply
	pjtJSON(t, rec, &r)
	if len(r.Sessions) != 2 || r.Next != nil {
		t.Fatalf("sessions = %+v", r)
	}
	// Newest first: the mentioned one (Sept 3) comes before the one that ran here (Sept 2).
	m, own := r.Sessions[0], r.Sessions[1]
	if m.ID != "aaaaaaaa-0000-0000-0000-000000000001" || m.Reason != "mention" || m.Title != "Talked about alpha" {
		t.Errorf("mentioned session = %+v", m)
	}
	if own.ID != pjtSessionID || own.Reason != "cwd" || own.Title != "Fix the build" || own.Turns != 4 || own.KB != 3 ||
		own.Tools["Edit"] != 2 || len(own.Edited) != 1 || own.Edited[0] != "src/main.go" || own.End == nil ||
		own.Start != "2026-09-02T10:00:00Z" || own.Profile == "" || own.CWD != e.alpha {
		t.Errorf("own session = %+v", own)
	}
	// The task link on the session is shown.
	if len(own.Tasks) != 1 || own.Tasks[0] != "T-1" || m.Tasks == nil || len(m.Tasks) != 0 {
		t.Errorf("tasks: own %v, mentioned %v", own.Tasks, m.Tasks)
	}

	// Cursor paging.
	rec = e.get("/api/projects/alpha/sessions?limit=1")
	pjtWant(t, rec, 200)
	var p1 pjtSessionsReply
	pjtJSON(t, rec, &p1)
	if len(p1.Sessions) != 1 || p1.Next == nil || p1.Sessions[0].ID != m.ID {
		t.Fatalf("page 1 = %+v", p1)
	}
	rec = e.get("/api/projects/alpha/sessions?limit=1&cursor=" + url.QueryEscape(*p1.Next))
	pjtWant(t, rec, 200)
	var p2 pjtSessionsReply
	pjtJSON(t, rec, &p2)
	if len(p2.Sessions) != 1 || p2.Sessions[0].ID != pjtSessionID || p2.Next != nil {
		t.Fatalf("page 2 = %+v", p2)
	}

	for _, q := range []string{"?cursor=x", "?cursor=s.1", "?cursor=s.a.b", "?cursor=s.1.zz", "?limit=0", "?limit=201", "?limit=x"} {
		pjtWant(t, e.get("/api/projects/alpha/sessions"+q), 400)
	}
	// Another project does not see them as its own.
	rec = e.get("/api/projects/beta/sessions")
	pjtWant(t, rec, 200)
	pjtJSON(t, rec, &r)
	if len(r.Sessions) != 1 || r.Sessions[0].Reason != "cwd" {
		t.Errorf("beta sessions = %+v", r.Sessions)
	}
	// No sessions at all: empty array.
	if err := e.db.UpsertProject(t.Context(), pjtProject("lonely", filepath.Join(e.projects, "g", "lonely"))); err != nil {
		t.Fatal(err)
	}
	if body := e.get("/api/projects/lonely/sessions").Body.String(); !strings.Contains(body, `"sessions":[]`) {
		t.Errorf("lonely = %s", body)
	}
}

func TestSessionTranscript(t *testing.T) {
	e := pjtNew(t, nil)
	type reply struct {
		Session struct{ ID, Title, Reason string }
		Turns   []struct {
			I          int
			Role, TS   string
			Text, Tool string
		}
		Next *string
	}
	rec := e.get("/api/sessions/" + pjtSessionID)
	pjtWant(t, rec, 200)
	var all reply
	pjtJSON(t, rec, &all)
	if all.Session.ID != pjtSessionID || all.Session.Title != "Fix the build" || all.Session.Reason != "cwd" {
		t.Errorf("session = %+v", all.Session)
	}
	if len(all.Turns) != 4 || all.Next != nil {
		t.Fatalf("turns = %+v next=%v", all.Turns, all.Next)
	}
	roles := []string{}
	for _, tr := range all.Turns {
		roles = append(roles, tr.Role)
		if tr.TS == "" {
			t.Errorf("turn %d has no ts", tr.I)
		}
	}
	if strings.Join(roles, ",") != "user,claude,user,claude" {
		t.Errorf("roles = %v", roles)
	}
	// The secret in the user text never leaves the claude probe.
	if strings.Contains(rec.Body.String(), "ghp_abcdefghijklmnop") {
		t.Fatal("a token was served")
	}

	// Page by byte offset: two turns at a time, same turns as the single read.
	var paged []string
	q := "?limit=2"
	for i := 0; i < 5; i++ {
		rec := e.get("/api/sessions/" + pjtSessionID + q)
		pjtWant(t, rec, 200)
		var r reply
		pjtJSON(t, rec, &r)
		for _, tr := range r.Turns {
			paged = append(paged, tr.Text)
		}
		if r.Next == nil {
			break
		}
		q = "?limit=2&cursor=" + *r.Next
	}
	if len(paged) != 4 || paged[3] != "Done." {
		t.Errorf("paged = %v", paged)
	}

	// Role filter.
	rec = e.get("/api/sessions/" + pjtSessionID + "?roles=claude")
	pjtWant(t, rec, 200)
	var only reply
	pjtJSON(t, rec, &only)
	if len(only.Turns) != 2 || only.Turns[0].Text != "Looking at it now." {
		t.Errorf("claude only = %+v", only.Turns)
	}
	if r := e.get("/api/sessions/" + pjtSessionID + "?roles=user,claude"); r.Code != 200 {
		t.Errorf("two roles: %d", r.Code)
	}

	for name, q := range map[string]string{
		"role unknown": "?roles=admin",
		"role empty":   "?roles=user,,claude",
		"cursor text":  "?cursor=abc",
		"cursor neg":   "?cursor=-5",
		"mid line":     "?cursor=7",
		"limit zero":   "?limit=0",
		"limit big":    "?limit=501",
	} {
		if rec := e.get("/api/sessions/" + pjtSessionID + q); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
	pjtWant(t, e.get("/api/sessions/bbbbbbbb-0000-0000-0000-000000000000"), 404) // not in the index
	pjtWant(t, e.get("/api/sessions/aaaaaaaa-0000-0000-0000-000000000001"), 404) // in the index, file is gone
	for _, id := range []string{"zz", "not-a-uuid-at-all", "1234567", "..%2Fetc"} {
		if rec := e.get("/api/sessions/" + id); rec.Code == 200 {
			t.Errorf("id %q accepted", id)
		}
	}
	pjtWant(t, e.get("/api/sessions/not-a-uuid-at-all"), 400)
}

func TestSessionPathComesFromTheIndexAndStaysInProfiles(t *testing.T) {
	e := pjtNew(t, nil)
	// An index row that points outside the Claude profile folders is never opened.
	outsideFile := filepath.Join(e.outside, "evil.jsonl")
	if err := os.WriteFile(outsideFile, []byte(`{"type":"user","message":{"role":"user","content":"TOP SECRET OUTSIDE"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertSessions(t.Context(), []pjtSession{{ID: "cccccccc-0000-0000-0000-000000000000", JSONLPath: outsideFile, ProjectID: "alpha"}}); err != nil {
		t.Fatal(err)
	}
	rec := e.get("/api/sessions/cccccccc-0000-0000-0000-000000000000")
	pjtWant(t, rec, 403)
	if strings.Contains(rec.Body.String(), "TOP SECRET") {
		t.Error("a file outside the profiles was read")
	}
	// The request has no way to name a path: extra query values are ignored.
	rec = e.get("/api/sessions/" + pjtSessionID + "?path=" + url.QueryEscape(outsideFile) + "&jsonl=" + url.QueryEscape(outsideFile))
	pjtWant(t, rec, 200)
	if strings.Contains(rec.Body.String(), "TOP SECRET") {
		t.Error("a path in the query was used")
	}
	// A row with no path.
	if err := e.db.UpsertSessions(t.Context(), []pjtSession{{ID: "dddddddd-0000-0000-0000-000000000000", ProjectID: "alpha"}}); err != nil {
		t.Fatal(err)
	}
	pjtWant(t, e.get("/api/sessions/dddddddd-0000-0000-0000-000000000000"), 404)
}
