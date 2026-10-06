package httpapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/store"
)

type ahSearchResp struct {
	Hits []struct {
		Kind      string  `json:"kind"`
		Ref       string  `json:"ref"`
		ProjectID string  `json:"projectId"`
		Title     string  `json:"title"`
		Snippet   string  `json:"snippet"`
		TS        *string `json:"ts"`
	} `json:"hits"`
}

// ahSearchEnv builds a store with one of every searchable thing.
func ahSearchEnv(t *testing.T) *ahEnv {
	t.Helper()
	e := ahNew(t, nil)
	ctx := context.Background()
	e.project("alpha", "personal", "git", "main", false, true, "")
	e.project("beta", "tools", "git", "main", true, true, "") // hidden
	e.commit("alpha", "a1b2c3d4e5f6", "Rework the discovery loop", "body mentions café latte", ahClock.Add(-time.Hour))
	e.commit("beta", "b1b2c3d4e5f6", "discovery in the hidden project", "", ahClock.Add(-2*time.Hour))
	if err := e.db.ReplaceFiles(ctx, "alpha", []store.File{
		{Path: "docs/plan.md", Kind: "doc", Title: "Plan", Body: "# Plan\nThe seismograph shows every lane.", MTime: ahClock},
		{Path: "internal/discover.go", Kind: "code", MTime: ahClock},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertAuthoredTask(ctx, store.Task{ProjectID: "alpha", ID: "T-4", Title: "Polish the dome chamber", Status: "todo",
		Body: "stars and a compass", Created: "2026-09-29T10:00:00+05:30"}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertAuthoredNote(ctx, store.Note{ProjectID: "alpha", ID: "N-1", Ref: "task:T-4", Body: "constellation lines too bright",
		Created: "2026-09-30T10:00:00+05:30"}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertAuthoredSnapshot(ctx, store.Snapshot{ProjectID: "alpha", TaskID: "T-4", Version: 2, Title: "Dome pass two",
		ImagePath: ".drashta/snapshots/T-4/v2.png", Verdict: "pending", Captured: "2026-09-30T10:00:00+05:30"}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.ReplaceServersNow(ctx, []store.ServerNow{
		{Key: "k1", Port: 4455, ProjectID: "alpha", Kind: "dev", Name: "node", Cmd: "vite --port 4455", Title: "alpha ui", LastSeen: ahClock},
		{Key: "k2", Port: 4456, ProjectID: "beta", Kind: "dev", Name: "node", Cmd: "vite --port 4456"},
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *ahEnv) search(query string) ahSearchResp {
	e.t.Helper()
	var r ahSearchResp
	ahDecode(e.t, e.get("/api/search?"+query), 200, &r)
	return r
}

func ahHitKeys(r ahSearchResp) []string {
	out := []string{}
	for _, h := range r.Hits {
		out = append(out, h.Kind+" "+h.Ref)
	}
	return out
}

func TestHomeSearchFindsEveryKind(t *testing.T) {
	e := ahSearchEnv(t)
	cases := []struct {
		q    string
		want string // "kind ref" that must be among the hits
	}{
		{"discovery", "commit alpha:commit:a1b2c3d4e5f6"},
		{"a1b2c3d", "commit alpha:commit:a1b2c3d4e5f6"}, // by hash fragment
		{"seismograph", "doc alpha:file:docs/plan.md"},
		{"discover.go", "file alpha:file:internal/discover.go"},
		{"dome chamber", "task alpha:task:T-4"},
		{"constellation", "note alpha:note:N-1"},
		{"Dome pass", "snapshot alpha:snapshot:T-4/v2"},
		{"alpha", "project alpha:project:alpha"},
		{"vite", "server alpha:server:4455"},
		{":4455", "server alpha:server:4455"},
		{"café", "commit alpha:commit:a1b2c3d4e5f6"}, // unicode
		{"CAFÉ LATTE", "commit alpha:commit:a1b2c3d4e5f6"},
	}
	for _, c := range cases {
		r := e.search("q=" + url.QueryEscape(c.q))
		found := false
		for _, k := range ahHitKeys(r) {
			if k == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("q=%q: %q not in %v", c.q, c.want, ahHitKeys(r))
		}
	}
}

func TestHomeSearchHitShape(t *testing.T) {
	e := ahSearchEnv(t)
	r := e.search("q=" + url.QueryEscape("rework"))
	if len(r.Hits) != 1 {
		t.Fatalf("hits = %v", ahHitKeys(r))
	}
	h := r.Hits[0]
	if h.Kind != "commit" || h.ProjectID != "alpha" || h.Title != "Rework the discovery loop" || h.Snippet == "" ||
		h.TS == nil || *h.TS != "2026-09-30T13:03:22+05:30" {
		t.Errorf("hit = %+v", h)
	}
	// a project hit carries the project's change time; a hit without a time has ts null
	pr := e.search("q=alpha&kind=project")
	if len(pr.Hits) != 1 || pr.Hits[0].TS == nil {
		t.Errorf("project hit = %+v", pr.Hits)
	}
	m := ahRaw(t, e.get("/api/search?q="+url.QueryEscape("constellation")), 200)
	if _, ok := m["hits"].([]any); !ok {
		t.Errorf("hits must be an array: %#v", m)
	}
}

func TestHomeSearchShortAndOddInputNeverFails(t *testing.T) {
	e := ahSearchEnv(t)
	odd := []string{
		"", " ", "a", "ab", "  ab  ", "éé", ":", ":4", `"`, `""`, `"""""`, "*", "***", "a*", "NEAR(", "NEAR(a b)", "a OR b",
		"a AND b", "title:x", "-x", "^x", "(", ")", ") (", "a)b(c", "%", "%%%", "_", "a_b", `\`, `\\\`, "' OR 1=1 --", "'; DROP TABLE x; --",
		"\x00", "ab\x00cd", "\xff\xfe\xfd", "\xc3\x28", strings.Repeat("x", 5000), strings.Repeat("é", 3000), "\U0001F600\U0001F600\U0001F600",
		"\t\n\r", "word word word word word word word word word word word word", "col:", "{x}", "[x]", "x:y:z", "a‮b", "...", "///",
	}
	for _, q := range odd {
		rec := e.get("/api/search?q=" + url.QueryEscape(q))
		var r ahSearchResp
		ahDecode(t, rec, 200, &r)
		if r.Hits == nil {
			t.Errorf("q=%q: hits is null, want []", q)
		}
	}
	// two characters and shorter: always an empty list, even if something matches
	for _, q := range []string{"al", "T-", "a", "  al  ", ":4", "éé"} {
		if r := e.search("q=" + url.QueryEscape(q)); len(r.Hits) != 0 {
			t.Errorf("q=%q gave hits %v", q, ahHitKeys(r))
		}
	}
	// odd values for the other parameters
	for _, extra := range []string{"&limit=abc", "&limit=-5", "&limit=0", "&limit=99999999999999999999", "&kind=%00", "&kind=nope", "&project=%00",
		"&project=" + url.QueryEscape(strings.Repeat("p", 500)), "&kind=" + url.QueryEscape("commit' OR 1=1")} {
		rec := e.get("/api/search?q=discovery" + extra)
		var r ahSearchResp
		ahDecode(t, rec, 200, &r)
		if r.Hits == nil {
			t.Errorf("%s: hits is null", extra)
		}
	}
	// no q at all
	if r := e.search(""); r.Hits == nil || len(r.Hits) != 0 {
		t.Errorf("no q: %v", r.Hits)
	}
}

func TestHomeSearchFTSSyntaxIsPlainText(t *testing.T) {
	e := ahSearchEnv(t)
	// "OR" and "NEAR" are words, not operators: a query that names two words needs both.
	if r := e.search("q=" + url.QueryEscape("discovery OR zzzzzz")); len(r.Hits) != 0 {
		t.Errorf("OR acted like an operator: %v", ahHitKeys(r))
	}
	if r := e.search("q=" + url.QueryEscape(`"discovery`)); len(r.Hits) == 0 {
		t.Errorf("a stray quote lost the word: %v", ahHitKeys(r))
	}
	if r := e.search("q=" + url.QueryEscape("disc*")); len(r.Hits) != 0 {
		t.Errorf("a star acted as a wildcard operator: %v", ahHitKeys(r))
	}
}

func TestHomeSearchFiltersAndHidden(t *testing.T) {
	e := ahSearchEnv(t)
	// hidden project: no hits unless asked for by name
	r := e.search("q=discovery")
	for _, h := range r.Hits {
		if h.ProjectID == "beta" {
			t.Errorf("hidden project leaked: %+v", h)
		}
	}
	r = e.search("q=discovery&project=beta")
	if len(r.Hits) != 1 || r.Hits[0].ProjectID != "beta" {
		t.Errorf("explicit project=beta = %v", ahHitKeys(r))
	}
	if r := e.search("q=" + url.QueryEscape(":4456")); len(r.Hits) != 0 {
		t.Errorf("hidden project's server leaked: %v", ahHitKeys(r))
	}
	if r := e.search("q=" + url.QueryEscape(":4456") + "&project=beta"); len(r.Hits) != 1 {
		t.Errorf("explicit project server = %v", ahHitKeys(r))
	}
	// kind filter
	r = e.search("q=discovery&kind=commit")
	if len(r.Hits) != 1 || r.Hits[0].Kind != "commit" {
		t.Errorf("kind=commit = %v", ahHitKeys(r))
	}
	if r := e.search("q=discovery&kind=task"); len(r.Hits) != 0 {
		t.Errorf("kind=task = %v", ahHitKeys(r))
	}
	if r := e.search("q=alpha&kind=server"); len(r.Hits) != 1 || r.Hits[0].Kind != "server" {
		t.Errorf("kind=server = %v", ahHitKeys(r))
	}
	if r := e.search("q=discovery&kind=nonsense"); len(r.Hits) != 0 {
		t.Errorf("unknown kind = %v", ahHitKeys(r))
	}
	// a port query finds servers only
	for _, h := range e.search("q=" + url.QueryEscape(":4455")).Hits {
		if h.Kind != "server" {
			t.Errorf("port query returned %+v", h)
		}
	}
}

func TestHomeSearchLimitAndOrder(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	// ReplaceCommits makes the stored list equal to the log, so all commits go in one call.
	var cs []probe.Commit
	for i := 0; i < 8; i++ {
		cs = append(cs, probe.Commit{ID: fmt.Sprintf("c0ffee%02d", i), VCS: "git", Msg: "needle commit number " + string(rune('a'+i)),
			Time: ahClock.Add(-time.Duration(i) * time.Hour)})
	}
	for i := 0; i < 120; i++ {
		cs = append(cs, probe.Commit{ID: fmt.Sprintf("feed%04d", i), VCS: "git", Msg: "bulk haystack item",
			Time: ahClock.Add(-time.Duration(i+10) * time.Minute)})
	}
	if err := e.db.ReplaceCommits(context.Background(), "alpha", cs); err != nil {
		t.Fatal(err)
	}
	if r := e.search("q=needle&limit=3"); len(r.Hits) != 3 {
		t.Errorf("limit=3 gave %d", len(r.Hits))
	}
	if r := e.search("q=needle"); len(r.Hits) != 8 {
		t.Errorf("default limit gave %d", len(r.Hits))
	}
	// the project hit comes first, then the ranked FTS hits
	e.project("needleproject", "g", "git", "main", false, true, "")
	r := e.search("q=needle")
	if len(r.Hits) == 0 || r.Hits[0].Kind != "project" {
		t.Errorf("order = %v", ahHitKeys(r))
	}
	// limit is capped at 100
	if r := e.search("q=haystack&limit=500"); len(r.Hits) != 100 {
		t.Errorf("limit=500 gave %d, want the cap of 100", len(r.Hits))
	}
	if r := e.search("q=haystack"); len(r.Hits) != 30 {
		t.Errorf("default gave %d, want 30", len(r.Hits))
	}
}
