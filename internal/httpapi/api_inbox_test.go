package httpapi

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/store"
)

type ahInboxResp struct {
	Notes []struct {
		ID             string   `json:"id"`
		Ref            string   `json:"ref"`
		ProjectID      string   `json:"projectId"`
		Text           string   `json:"text"`
		Author         string   `json:"author"`
		Date           string   `json:"date"`
		Resolved       bool     `json:"resolved"`
		ResolvedAt     string   `json:"resolvedAt"`
		Line           *int     `json:"line"`
		X              *float64 `json:"x"`
		Y              *float64 `json:"y"`
		Anchor         string   `json:"anchor"`
		ReplyTo        string   `json:"replyTo"`
		TargetTitle    string   `json:"targetTitle"`
		TargetSubtitle string   `json:"targetSubtitle"`
	} `json:"notes"`
	UnfiledSnapshots []struct {
		Name      string `json:"name"`
		ProjectID string `json:"projectId"`
		Path      string `json:"path"`
		Size      int64  `json:"size"`
		Date      string `json:"date"`
	} `json:"unfiledSnapshots"`
	Proposals []struct {
		Kind      string `json:"kind"`
		TaskRef   string `json:"taskRef"`
		CommitRef string `json:"commitRef"`
	} `json:"proposals"`
	Feed []struct {
		TS        string `json:"ts"`
		Kind      string `json:"kind"`
		Event     string `json:"event"`
		Ref       string `json:"ref"`
		Label     string `json:"label"`
		ProjectID string `json:"projectId"`
		Context   string `json:"context"`
	} `json:"feed"`
	Next *string `json:"next"`
}

func (e *ahEnv) addNote(project, id, ref, created, body string, resolved bool) {
	e.t.Helper()
	if err := e.db.UpsertAuthoredNote(context.Background(), store.Note{ProjectID: project, ID: id, Ref: ref, Created: created,
		Body: body, Author: "alice", Resolved: resolved}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *ahEnv) inbox(query string) ahInboxResp {
	e.t.Helper()
	var r ahInboxResp
	ahDecode(e.t, e.get("/api/inbox"+query), 200, &r)
	return r
}

func ahNoteIDs(r ahInboxResp) []string {
	out := []string{}
	for _, n := range r.Notes {
		out = append(out, n.ProjectID+"/"+n.ID)
	}
	return out
}

func TestHomeInboxNotesFiltersAndTitles(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	e.project("beta", "g", "", "", true, true, "") // hidden: its notes still show (the feed is what skips it)
	e.addNote("alpha", "N-1", "snapshot:T-1/v3", "2026-09-30T09:00:00+05:30", "too bright", false)
	e.addNote("alpha", "N-2", "file:src/main.go#L42", "2026-09-30T10:00:00+05:30", "check this", true)
	e.addNote("alpha", "N-3", "ledger:commit:abc1234", "2026-09-30T11:00:00+05:30", "other project's commit", false)
	e.addNote("beta", "N-1", "task:T-9", "2026-09-30T12:00:00+05:30", "from a hidden project", false)
	e.resolver.titles["alpha:snapshot:T-1/v3"] = [2]string{"Dome pass", "Snapshot v3"}
	e.resolver.titles["alpha:file:src/main.go#L42"] = [2]string{"main.go", "src"}
	e.resolver.titles["ledger:commit:abc1234"] = [2]string{"add servers", "git abc1234"}
	x, y, line := 0.46, 0.2, 42
	_ = x
	_ = y
	if err := e.db.UpsertAuthoredNote(context.Background(), store.Note{ProjectID: "alpha", ID: "N-4", Ref: "snapshot:T-1/v3", X: &x, Y: &y,
		Created: "2026-09-30T08:00:00+05:30", Body: "pinned", ReplyTo: "N-1", Anchor: "the text"}); err != nil {
		t.Fatal(err)
	}
	_ = line

	open := e.inbox("") // default filter is open
	if want := []string{"beta/N-1", "alpha/N-3", "alpha/N-1", "alpha/N-4"}; !reflect.DeepEqual(ahNoteIDs(open), want) {
		t.Errorf("open = %v, want %v", ahNoteIDs(open), want)
	}
	if got := ahNoteIDs(e.inbox("?filter=open")); !reflect.DeepEqual(got, ahNoteIDs(open)) {
		t.Errorf("filter=open = %v", got)
	}
	if got := ahNoteIDs(e.inbox("?filter=resolved")); !reflect.DeepEqual(got, []string{"alpha/N-2"}) {
		t.Errorf("resolved = %v", got)
	}
	if got := ahNoteIDs(e.inbox("?filter=all")); len(got) != 5 {
		t.Errorf("all = %v", got)
	}

	byKey := map[string]int{}
	for i, n := range open.Notes {
		byKey[n.ProjectID+"/"+n.ID] = i
	}
	n1 := open.Notes[byKey["alpha/N-1"]]
	if n1.Ref != "alpha:snapshot:T-1/v3" || n1.TargetTitle != "Dome pass" || n1.TargetSubtitle != "Snapshot v3" || n1.Text != "too bright" ||
		n1.Author != "alice" || n1.Date != "2026-09-30T09:00:00+05:30" || n1.Resolved {
		t.Errorf("N-1 = %+v", n1)
	}
	n3 := open.Notes[byKey["alpha/N-3"]]
	if n3.Ref != "ledger:commit:abc1234" || n3.TargetTitle != "add servers" {
		t.Errorf("a ref that names another project is kept: %+v", n3)
	}
	nb := open.Notes[byKey["beta/N-1"]]
	if nb.Ref != "beta:task:T-9" || nb.TargetTitle != "beta:task:T-9" {
		t.Errorf("a ref the resolver does not know falls back to the ref: %+v", nb)
	}
	n4 := open.Notes[byKey["alpha/N-4"]]
	if n4.X == nil || *n4.X != 0.46 || n4.Y == nil || n4.Line != nil || n4.ReplyTo != "N-1" || n4.Anchor != "the text" {
		t.Errorf("N-4 = %+v", n4)
	}
	res := e.inbox("?filter=resolved").Notes[0]
	if res.Line != nil || !res.Resolved || res.TargetTitle != "main.go" {
		t.Errorf("resolved note = %+v", res)
	}
}

func TestHomeInboxResolverCalledOncePerRef(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "", "", false, true, "")
	for i, id := range []string{"N-1", "N-2", "N-3"} {
		e.addNote("alpha", id, "task:T-1", "2026-09-30T0"+string(rune('1'+i))+":00:00+05:30", "same target", false)
	}
	e.resolver.titles["alpha:task:T-1"] = [2]string{"the task", ""}
	r := e.inbox("")
	if len(r.Notes) != 3 {
		t.Fatalf("notes = %d", len(r.Notes))
	}
	if n := e.resolver.calls["alpha:task:T-1"]; n != 1 {
		t.Errorf("resolver called %d times for one ref", n)
	}
}

func TestHomeInboxWithoutResolver(t *testing.T) {
	e := ahNew(t, func(d *HomeDeps) { d.Resolver = nil })
	e.project("alpha", "g", "", "", false, true, "")
	e.addNote("alpha", "N-1", "file:a.go", "2026-09-30T09:00:00+05:30", "x", false)
	r := e.inbox("")
	if len(r.Notes) != 1 || r.Notes[0].TargetTitle != "alpha:file:a.go" {
		t.Errorf("notes = %+v", r.Notes)
	}
}

func TestHomeInboxEmpty(t *testing.T) {
	e := ahNew(t, nil)
	m := ahRaw(t, e.get("/api/inbox"), 200)
	for _, k := range []string{"notes", "unfiledSnapshots", "proposals", "feed"} {
		if l, ok := m[k].([]any); !ok || len(l) != 0 {
			t.Errorf("%s = %#v (want [])", k, m[k])
		}
	}
	if v, ok := m["next"]; !ok || v != nil {
		t.Errorf("next = %#v (want null)", v)
	}
}

func TestHomeInboxFeedSkipsHiddenAndNoise(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	e.project("beta", "g", "", "", true, true, "")
	base := time.Date(2026, 9, 30, 9, 0, 0, 0, ahIST)
	e.event("alpha", "commit", "commit:a1", "first", base, `{"plus":5,"minus":2}`)
	e.event("alpha", "session_start", "session:s1", "session title", base.Add(time.Hour), `{"profile":".claude"}`)
	e.event("alpha", "server_up", "server:5173", "vite", base.Add(2*time.Hour), "")
	e.event("alpha", "verdict", "snapshot:T-1/v1", "approved", base.Add(3*time.Hour), "")
	e.event("beta", "commit", "commit:b1", "hidden project commit", base.Add(4*time.Hour), "")
	r := e.inbox("")
	var got []string
	for _, f := range r.Feed {
		got = append(got, f.Kind+"/"+f.Event+" "+f.ProjectID+" "+f.Ref)
	}
	want := []string{"snapshot/verdict alpha alpha:snapshot:T-1/v1", "session/session_start alpha alpha:session:s1", "commit/commit alpha alpha:commit:a1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("feed = %v, want %v", got, want)
	}
	if r.Feed[2].TS != "2026-09-30T09:00:00+05:30" {
		t.Errorf("ts = %q", r.Feed[2].TS)
	}
}

func TestHomeInboxPaging(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	base := time.Date(2026, 9, 30, 9, 0, 0, 0, ahIST)
	for i := 0; i < 7; i++ {
		id := "N-" + string(rune('1'+i))
		e.addNote("alpha", id, "file:a.go", base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), "note "+id, false)
	}
	for i := 0; i < 5; i++ {
		e.event("alpha", "commit", "commit:c"+string(rune('0'+i)), "commit", base.Add(time.Duration(i)*time.Minute), "")
	}
	// two same-second events: the cursor must not skip or repeat one of them
	e.event("alpha", "note", "note:N-1", "same second a", base.Add(10*time.Minute), "")
	e.event("alpha", "task_created", "task:T-1", "same second b", base.Add(10*time.Minute), "")

	var notes, feed []string
	pages, cursor := 0, ""
	for {
		q := "?limit=3"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		r := e.inbox(q)
		pages++
		if len(r.Feed) > 3 {
			t.Fatalf("page %d has %d feed events, limit is 3", pages, len(r.Feed))
		}
		if pages == 1 && len(r.Notes) != 7 {
			t.Errorf("the first page must hold every note (the page relies on it), got %d", len(r.Notes))
		}
		if pages > 1 && len(r.Notes) != 0 {
			t.Errorf("page %d repeats %d notes", pages, len(r.Notes))
		}
		if cursor != "" && (len(r.UnfiledSnapshots) != 0 || len(r.Proposals) != 0) {
			t.Errorf("page %d repeats unfiled snapshots or proposals", pages)
		}
		for _, n := range r.Notes {
			notes = append(notes, n.ID)
		}
		for _, f := range r.Feed {
			feed = append(feed, f.Ref)
		}
		if r.Next == nil {
			break
		}
		cursor = *r.Next
		if pages > 10 {
			t.Fatal("paging does not end")
		}
	}
	if want := []string{"N-7", "N-6", "N-5", "N-4", "N-3", "N-2", "N-1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes over pages = %v, want %v", notes, want)
	}
	if len(feed) != 7 {
		t.Errorf("feed over pages = %v (want 7 distinct events)", feed)
	}
	seen := map[string]bool{}
	for _, f := range feed {
		if seen[f] {
			t.Errorf("event %q came twice", f)
		}
		seen[f] = true
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3 (7 feed events, 3 per page)", pages)
	}
	// feed newest first, ties ordered by id (later row first)
	if feed[0] != "alpha:task:T-1" || feed[1] != "alpha:note:N-1" {
		t.Errorf("feed head = %v", feed[:2])
	}
}

// With more notes than the ceiling, the same cursor carries on with the notes.
func TestHomeInboxNotesBeyondTheCeilingPage(t *testing.T) {
	old := ahInboxNotePage
	ahInboxNotePage = 4
	t.Cleanup(func() { ahInboxNotePage = old })
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	base := time.Date(2026, 9, 30, 9, 0, 0, 0, ahIST)
	for i := 0; i < 10; i++ {
		e.addNote("alpha", "N-"+string(rune('a'+i)), "file:a.go", base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), "x", false)
	}
	var got []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		q := ""
		if cursor != "" {
			q = "?cursor=" + url.QueryEscape(cursor)
		}
		r := e.inbox(q)
		if len(r.Notes) > 4 {
			t.Fatalf("page holds %d notes, the ceiling is 4", len(r.Notes))
		}
		for _, n := range r.Notes {
			got = append(got, n.ID)
		}
		if r.Next == nil {
			break
		}
		cursor = *r.Next
	}
	if len(got) != 10 {
		t.Errorf("notes over pages = %v", got)
	}
}

func TestHomeInboxFeedSnapshotContextIsTheVerdict(t *testing.T) {
	e := ahNew(t, nil)
	ctx := context.Background()
	e.project("alpha", "g", "git", "main", false, true, "")
	for _, s := range []store.Snapshot{
		{ProjectID: "alpha", TaskID: "T-1", Version: 1, Verdict: "approved", Title: "v1"},
		{ProjectID: "alpha", TaskID: "T-1", Version: 2, Verdict: "pending", Title: "v2"},
		{ProjectID: "alpha", TaskID: "T-1", Version: 3, Verdict: "changes", Title: "v3"},
	} {
		if err := e.db.UpsertAuthoredSnapshot(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 9, 30, 9, 0, 0, 0, ahIST)
	e.event("alpha", "snapshot", "snapshot:T-1/v1", "Dome v1", base, "")                                         // row says approved
	e.event("alpha", "verdict", "snapshot:T-1/v1", "approved", base.Add(time.Minute), "")                        // same
	e.event("alpha", "snapshot", "snapshot:T-1/v2", "Dome v2", base.Add(2*time.Minute), "")                      // pending
	e.event("alpha", "verdict", "snapshot:T-1/v3", "changes", base.Add(3*time.Minute), "")                       // changes
	e.event("alpha", "snapshot", "snapshot:T-9/v1", "Gone", base.Add(4*time.Minute), "")                         // no row: new snapshot
	e.event("alpha", "verdict", "snapshot:T-9/v2", "approved", base.Add(5*time.Minute), "")                      // no row: label
	e.event("alpha", "verdict", "snapshot:T-9/v3", "reviewed", base.Add(6*time.Minute), `{"verdict":"changes"}`) // no row: meta
	e.event("alpha", "verdict", "snapshot:T-9/v4", "odd words", base.Add(7*time.Minute), "")                     // nothing known
	e.event("alpha", "commit", "commit:abc1234", "a commit", base.Add(8*time.Minute), `{"plus":1,"minus":0}`)

	r := e.inbox("")
	got := map[string]string{}
	for _, f := range r.Feed {
		got[f.Event+" "+f.Ref] = f.Kind + ":" + f.Context
	}
	want := map[string]string{
		"snapshot alpha:snapshot:T-1/v1": "snapshot:approved",
		"verdict alpha:snapshot:T-1/v1":  "snapshot:approved",
		"snapshot alpha:snapshot:T-1/v2": "snapshot:pending",
		"verdict alpha:snapshot:T-1/v3":  "snapshot:changes",
		"snapshot alpha:snapshot:T-9/v1": "snapshot:pending",
		"verdict alpha:snapshot:T-9/v2":  "snapshot:approved",
		"verdict alpha:snapshot:T-9/v3":  "snapshot:changes",
		"verdict alpha:snapshot:T-9/v4":  "snapshot:",
		"commit alpha:commit:abc1234":    "commit:+1 \u22120",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("feed contexts:\n got  %v\n want %v", got, want)
	}
}

func TestHomeInboxParamValidation(t *testing.T) {
	e := ahNew(t, nil)
	for _, q := range []string{"?filter=bogus", "?filter=", "?filter=OPEN", "?limit=0", "?limit=-1", "?limit=abc", "?limit=201", "?cursor=!!!", "?cursor=" + url.QueryEscape("e30"),
		"?cursor=" + url.QueryEscape("eyJuIjotMX0")} {
		rec := e.get("/api/inbox" + q)
		if q == "?cursor="+url.QueryEscape("e30") { // "{}" is a valid empty cursor
			if rec.Code != 200 {
				t.Errorf("%s gave %d", q, rec.Code)
			}
			continue
		}
		if rec.Code != 400 {
			t.Errorf("%s gave %d, want 400", q, rec.Code)
		}
	}
	if rec := e.get("/api/inbox?filter=all&limit=200"); rec.Code != 200 {
		t.Errorf("limit=200 gave %d", rec.Code)
	}
}

func TestHomeInboxUnfiledSnapshots(t *testing.T) {
	e := ahNew(t, nil)
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	inbox := filepath.Join(alpha, ".drashta", "snapshots", "inbox")
	if err := os.MkdirAll(filepath.Join(inbox, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(beta, ".drashta", "snapshots", "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path string, age time.Duration) {
		t.Helper()
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(inbox, "idea.png"), 3*time.Hour)
	write(filepath.Join(inbox, "newer.JPG"), time.Hour)
	write(filepath.Join(inbox, "T-1--filed.png"), time.Hour)     // T-1 exists: the indexer files it
	write(filepath.Join(inbox, "T-77--orphan.png"), 2*time.Hour) // T-77 does not exist: listed
	write(filepath.Join(inbox, "notes.txt"), time.Hour)          // not an image
	write(filepath.Join(inbox, ".hidden.png"), time.Hour)        // dot file
	write(filepath.Join(beta, ".drashta", "snapshots", "inbox", "hidden-project.png"), time.Hour)
	outside := filepath.Join(root, "secret.png")
	write(outside, time.Hour)
	if err := os.Symlink(outside, filepath.Join(inbox, "link.png")); err != nil {
		t.Fatal(err)
	}
	e.project("alpha", "g", "", "", false, true, alpha)
	e.project("beta", "g", "", "", true, true, beta)
	if err := e.db.UpsertAuthoredTask(context.Background(), store.Task{ProjectID: "alpha", ID: "T-1", Title: "t"}); err != nil {
		t.Fatal(err)
	}

	r := e.inbox("")
	var names []string
	for _, u := range r.UnfiledSnapshots {
		names = append(names, u.ProjectID+"/"+u.Name)
		if u.Path != ".drashta/snapshots/inbox/"+u.Name || u.Size != 1 || len(u.Date) != len("2006-01-02T15:04:05+05:30") {
			t.Errorf("bad item %+v", u)
		}
	}
	if want := []string{"alpha/newer.JPG", "alpha/T-77--orphan.png", "alpha/idea.png"}; !reflect.DeepEqual(names, want) {
		t.Errorf("unfiled = %v, want %v (newest first)", names, want)
	}
}

func TestHomeInboxProposals(t *testing.T) {
	e := ahNew(t, nil)
	ctx := context.Background()
	e.project("alpha", "g", "git", "main", false, true, "")
	for _, id := range []string{"T-12", "T-13"} {
		if err := e.db.UpsertAuthoredTask(ctx, store.Task{ProjectID: "alpha", ID: id, Title: id, Status: "in_progress",
			Created: "2026-09-01T10:00:00+05:30", Updated: "2026-09-01T10:00:00+05:30"}); err != nil {
			t.Fatal(err)
		}
	}
	e.commit("alpha", "deadbeef1234", "Fixes T-12 for good", "", ahClock.Add(-2*time.Hour))
	e.commit("alpha", "cafe0000cafe", "unrelated work", "", ahClock.Add(-time.Hour))
	r := e.inbox("")
	if len(r.Proposals) != 1 {
		t.Fatalf("proposals = %+v", r.Proposals)
	}
	p := r.Proposals[0]
	if p.Kind != "close_task" || p.TaskRef != "alpha:task:T-12" || p.CommitRef != "alpha:commit:deadbeef1234" {
		t.Errorf("proposal = %+v", p)
	}
	// once the task is done there is nothing to propose
	if err := e.db.UpsertAuthoredTask(ctx, store.Task{ProjectID: "alpha", ID: "T-12", Title: "T-12", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if r := e.inbox(""); len(r.Proposals) != 0 {
		t.Errorf("proposals after done = %+v", r.Proposals)
	}
}

// The JSON of a note must not carry null pins: absent keys keep the page's optional fields simple.
func TestHomeInboxNoteJSONOmitsEmptyPins(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "", "", false, true, "")
	e.addNote("alpha", "N-1", "file:a.go", "2026-09-30T09:00:00+05:30", "x", false)
	m := ahRaw(t, e.get("/api/inbox"), 200)
	n := m["notes"].([]any)[0].(map[string]any)
	for _, k := range []string{"line", "x", "y", "anchor", "replyTo", "resolvedAt"} {
		if _, ok := n[k]; ok {
			t.Errorf("note has %q although it is empty: %v", k, n)
		}
	}
	if _, err := json.Marshal(m); err != nil {
		t.Fatal(err)
	}
}

// A .drashta folder that is a symlink out of the project must not be read.
func TestHomeInboxUnfiledRefusesSymlinkEscape(t *testing.T) {
	e := ahNew(t, nil)
	root := t.TempDir()
	outside := filepath.Join(root, "outside", "snapshots", "inbox")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(proj, ".drashta")); err != nil {
		t.Fatal(err)
	}
	e.project("proj", "g", "", "", false, true, proj)
	if r := e.inbox(""); len(r.UnfiledSnapshots) != 0 {
		t.Errorf("read through a symlink: %+v", r.UnfiledSnapshots)
	}
}
