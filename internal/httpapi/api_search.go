package httpapi

// GET /api/search?q=&project=&kind=&limit=30
//
// Reply: {hits:[{kind, ref, projectId, title, snippet, ts}]}.
//
// Sources: the FTS table through store.Search (file, doc, commit, session, task, feature, milestone, note)
// and three LIKE searches in the store (project, snapshot, server). A query ":4455" (colon and digits)
// finds server ports only. The text must have 3 characters or more; anything shorter, or any odd input
// (quotes, stars, FTS words), gives an empty list, never an error. Hits of hidden projects are left out
// unless that project is asked for with ?project=. Refs in the reply are project-qualified.

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/store"
)

const (
	ahSearchDefaultLimit = 30
	ahSearchMaxLimit     = 100
	ahSearchMinRunes     = 3
)

// ahSearchKinds are the values of ?kind= that make sense. Anything else gives no hits.
var ahSearchKinds = map[string]bool{
	"file": true, "doc": true, "commit": true, "session": true, "task": true, "feature": true,
	"milestone": true, "note": true, "project": true, "snapshot": true, "server": true,
}

type ahHit struct {
	Kind      string  `json:"kind"`
	Ref       string  `json:"ref"`
	ProjectID string  `json:"projectId"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet"`
	TS        *string `json:"ts"`
}

type ahSearch struct {
	Hits []ahHit `json:"hits"`
}

func (h *homeAPI) registerSearch() {
	h.s.Handle(http.MethodGet, "/api/search", h.handleSearch, false)
}

func (h *homeAPI) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	qv := r.URL.Query()
	empty := ahSearch{Hits: []ahHit{}}

	q := strings.TrimSpace(strings.ToValidUTF8(qv.Get("q"), " "))
	project := strings.TrimSpace(qv.Get("project"))
	kind := strings.TrimSpace(qv.Get("kind"))
	limit := ahSearchDefaultLimit
	if n, err := strconv.Atoi(qv.Get("limit")); err == nil && n > 0 {
		limit = n
	}
	if limit > ahSearchMaxLimit {
		limit = ahSearchMaxLimit
	}

	if utf8.RuneCountInString(q) < ahSearchMinRunes || len(project) > 200 || !ahPlain(project) ||
		(kind != "" && !ahSearchKinds[kind]) {
		WriteJSON(w, http.StatusOK, empty)
		return
	}

	var fts, extras []store.Hit
	portSearch := strings.HasPrefix(q, ":")
	extraKind := kind == "project" || kind == "snapshot" || kind == "server"
	if !portSearch && !extraKind {
		// Ask for more than we need: hits of hidden projects are dropped below.
		var err error
		fts, err = h.d.Store.Search(ctx, q, project, kind, limit*3)
		if err != nil {
			h.fail(w, "search", err)
			return
		}
	}
	if kind == "" || extraKind {
		var err error
		extras, err = h.d.Store.HomeSearchExtras(ctx, q, project, kind, limit)
		if err != nil {
			h.fail(w, "search", err)
			return
		}
	}

	hidden, err := h.d.Store.HomeHiddenIDs(ctx)
	if err != nil {
		h.fail(w, "search", err)
		return
	}

	// Order: projects, ranked FTS hits, snapshots, servers.
	ordered := make([]store.Hit, 0, len(fts)+len(extras))
	for _, x := range extras {
		if x.Kind == "project" {
			ordered = append(ordered, x)
		}
	}
	ordered = append(ordered, fts...)
	for _, x := range extras {
		if x.Kind != "project" {
			ordered = append(ordered, x)
		}
	}

	out := ahSearch{Hits: make([]ahHit, 0, limit)}
	seen := map[string]bool{}
	for _, x := range ordered {
		if hidden[x.ProjectID] && x.ProjectID != project {
			continue
		}
		ref := ahQualify(x.ProjectID, x.Ref)
		key := x.Kind + "\x00" + ref
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Hits = append(out.Hits, ahHit{
			Kind: x.Kind, Ref: ref, ProjectID: x.ProjectID, Title: x.Title, Snippet: x.Snippet,
			TS: ahTimePtr(x.TS, h.loc),
		})
		if len(out.Hits) >= limit {
			break
		}
	}
	WriteJSON(w, http.StatusOK, out)
}
