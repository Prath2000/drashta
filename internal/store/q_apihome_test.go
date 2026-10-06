package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// Tests for q_apihome.go. Everything lives in t.TempDir(). Helper names start with "ah".

func ahProj(t *testing.T, db *DB, id, grp string, hidden bool, indexed bool) {
	t.Helper()
	p := Project{ID: id, Name: id, Group: grp, Path: "/p/" + id, Hidden: hidden, Description: "desc of " + id,
		Stack: []string{"Go 1.26"}}
	if indexed {
		p.IndexedAt = time.Unix(2000, 0)
	}
	if err := db.UpsertProject(bg(), p); err != nil {
		t.Fatal(err)
	}
}

func ahNote(t *testing.T, db *DB, project, id, ref, created, body string, resolved bool) {
	t.Helper()
	if err := db.UpsertAuthoredNote(bg(), Note{ProjectID: project, ID: id, Ref: ref, Created: created, Body: body,
		Author: "alice", Resolved: resolved}); err != nil {
		t.Fatal(err)
	}
}

func TestHomeBootProjectsAndCounts(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	ahProj(t, db, "alpha", "personal", false, true)
	ahProj(t, db, "beta", "tools", true, false)
	if err := db.ReplaceDirty(ctx, "alpha", []probe.DirtyFile{{Path: "a.go", State: "M"}, {Path: "b.go", State: "?"}}); err != nil {
		t.Fatal(err)
	}

	list, err := db.HomeBootProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "alpha" || list[1].ID != "beta" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].DirtyCount != 2 || !list[0].Surveyed || list[1].DirtyCount != 0 || list[1].Surveyed || !list[1].Hidden {
		t.Errorf("flags wrong: %+v / %+v", list[0], list[1])
	}

	// counts
	if err := db.ReplaceServersNow(ctx, []ServerNow{
		{Key: "k1", Port: 5173, ProjectID: "alpha", Kind: "dev"},
		{Key: "k2", Port: 5174, Kind: "dev"}, // no project: still a live dev server
		{Key: "k3", Port: 9000, Kind: "helper"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []Task{
		{ProjectID: "alpha", ID: "T-1", Title: "one", Status: "todo"},
		{ProjectID: "alpha", ID: "T-2", Title: "two", Status: "review"},
		{ProjectID: "alpha", ID: "T-3", Title: "three", Status: "done"},
	} {
		if err := db.UpsertAuthoredTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []Snapshot{
		{ProjectID: "alpha", TaskID: "T-1", Version: 1, Verdict: "pending"},  // older, then approved: not awaiting
		{ProjectID: "alpha", TaskID: "T-1", Version: 2, Verdict: "approved"}, //
		{ProjectID: "alpha", TaskID: "T-2", Version: 1, Verdict: "changes"},  //
		{ProjectID: "alpha", TaskID: "T-2", Version: 2, Verdict: "pending"},  // awaiting
		{ProjectID: "alpha", TaskID: "T-3", Version: 1, Verdict: "pending"},  // task done: not awaiting (I10)
	} {
		if err := db.UpsertAuthoredSnapshot(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	ahNote(t, db, "alpha", "N-1", "task:T-1", "2026-09-30T10:00:00+05:30", "open one", false)
	ahNote(t, db, "alpha", "N-2", "task:T-1", "2026-09-30T11:00:00+05:30", "resolved", true)

	c, err := db.HomeBootCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := HomeCounts{LiveServers: 2, OpenTasks: 2, AwaitingReview: 1, OpenNotes: 1}
	if c != want {
		t.Errorf("counts = %+v, want %+v", c, want)
	}
}

func TestHomeBootFileCount(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	ahProj(t, db, "full", "g", false, true)        // surveyed, has files
	ahProj(t, db, "empty", "g", false, true)       // surveyed, no files: really an empty directory
	ahProj(t, db, "fresh", "g", false, false)      // not surveyed yet: the 0 means nothing
	ahProj(t, db, "hiddennever", "g", true, true)  // hidden, never walked: the 0 means nothing
	ahProj(t, db, "hiddenwalked", "g", true, true) // hidden but has files from before it was hidden
	for _, id := range []string{"full", "hiddenwalked"} {
		if err := db.ReplaceFiles(ctx, id, []File{{Path: "a.go", Kind: "code"}, {Path: "b.md", Kind: "doc"}}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := db.HomeBootProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type fc struct {
		n  int
		ok bool
	}
	got := map[string]fc{}
	for _, p := range list {
		got[p.ID] = fc{p.FileCount, p.HasFileCount}
	}
	want := map[string]fc{"full": {2, true}, "empty": {0, true}, "fresh": {0, false}, "hiddennever": {0, false}, "hiddenwalked": {2, true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file counts = %v, want %v", got, want)
	}
}

func TestHomeLastScan(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	got, err := db.HomeLastScan(ctx)
	if err != nil || !got.IsZero() {
		t.Fatalf("empty db: %v %v", got, err)
	}
	// a failed sweep does not count
	if err := db.AddSweepLog(ctx, SweepLog{TS: time.Unix(9000, 0), Scope: "all", OK: false}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeLastScan(ctx); !got.IsZero() {
		t.Errorf("failed sweep counted: %v", got)
	}
	if err := db.AddSweepLog(ctx, SweepLog{TS: time.Unix(5000, 0), Scope: "all", OK: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeLastScan(ctx); got.Unix() != 5000 {
		t.Errorf("sweep_log = %v", got)
	}
	// meta wins when it is later, in both formats
	if err := db.SetMeta(ctx, "last_sweep_at", "7000"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeLastScan(ctx); got.Unix() != 7000 {
		t.Errorf("meta seconds = %v", got)
	}
	if err := db.SetMeta(ctx, "last_sweep_at", time.Unix(8000, 0).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeLastScan(ctx); got.Unix() != 8000 {
		t.Errorf("meta rfc3339 = %v", got)
	}
	if err := db.SetMeta(ctx, "last_sweep_at", "garbage"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeLastScan(ctx); got.Unix() != 5000 {
		t.Errorf("garbage meta = %v", got)
	}
}

func TestHomeInboxNotesFilterOrderAndPaging(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	ahProj(t, db, "alpha", "g", false, true)
	// Written in two UTC offsets: plain text order would be wrong, time order is right.
	ahNote(t, db, "alpha", "N-1", "file:a.go", "2026-09-30T09:00:00+05:30", "oldest (03:30Z)", false)
	ahNote(t, db, "alpha", "N-2", "file:a.go", "2026-09-30T08:00:00+00:00", "middle (08:00Z)", false)
	ahNote(t, db, "alpha", "N-10", "file:a.go", "2026-09-30T20:00:00+05:30", "newest (14:30Z)", true)
	ahNote(t, db, "alpha", "N-3", "file:a.go", "2026-09-30T08:00:00+00:00", "tie with N-2, bigger id first", false)

	ids := func(filter string, off, lim int) []string {
		t.Helper()
		ns, err := db.HomeInboxNotes(ctx, filter, off, lim)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, n := range ns {
			out = append(out, n.ID)
		}
		return out
	}
	if got := ids("all", 0, 50); !reflect.DeepEqual(got, []string{"N-10", "N-3", "N-2", "N-1"}) {
		t.Errorf("all = %v", got)
	}
	if got := ids("open", 0, 50); !reflect.DeepEqual(got, []string{"N-3", "N-2", "N-1"}) {
		t.Errorf("open = %v", got)
	}
	if got := ids("resolved", 0, 50); !reflect.DeepEqual(got, []string{"N-10"}) {
		t.Errorf("resolved = %v", got)
	}
	if got := ids("all", 1, 2); !reflect.DeepEqual(got, []string{"N-3", "N-2"}) {
		t.Errorf("page = %v", got)
	}
	if got := ids("all", 4, 2); len(got) != 0 {
		t.Errorf("past the end = %v", got)
	}
	if _, err := db.HomeInboxNotes(ctx, "bogus", 0, 5); err == nil {
		t.Error("bad filter accepted")
	}
}

func TestHomeInboxNoteKeepsPins(t *testing.T) {
	db := openTest(t)
	x, y, line := 0.25, 0.75, 42
	if err := db.UpsertAuthoredNote(bg(), Note{ProjectID: "p", ID: "N-1", Ref: "snapshot:T-1/v3", X: &x, Y: &y,
		Created: "2026-09-30T09:00:00+05:30", Body: "pin"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertAuthoredNote(bg(), Note{ProjectID: "p", ID: "N-2", Ref: "file:a.go", Line: &line,
		Created: "2026-09-30T10:00:00+05:30", Body: "line"}); err != nil {
		t.Fatal(err)
	}
	ns, err := db.HomeInboxNotes(bg(), "all", 0, 10)
	if err != nil || len(ns) != 2 {
		t.Fatalf("%v %v", ns, err)
	}
	if ns[0].Line == nil || *ns[0].Line != 42 || ns[0].X != nil || ns[1].X == nil || *ns[1].X != 0.25 || *ns[1].Y != 0.75 {
		t.Errorf("pins lost: %+v %+v", ns[0], ns[1])
	}
}

func TestHomeInboxFeed(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	ahProj(t, db, "alpha", "g", false, true)
	ahProj(t, db, "ghost", "g", true, true) // hidden: its events are skipped
	add := func(project, kind, ref string, ts int64) {
		t.Helper()
		if _, err := db.AddEvent(ctx, Event{ProjectID: project, TS: time.Unix(ts, 0), Day: "2026-09-30", Kind: kind, Ref: ref, Label: kind}); err != nil {
			t.Fatal(err)
		}
	}
	add("alpha", "commit", "commit:a1", 1000)
	add("alpha", "session_start", "session:s1", 2000)
	add("alpha", "session_end", "session:s1", 2500) // left out: a session shows once
	add("alpha", "server_up", "server:5173", 3000)  // left out: noise
	add("alpha", "file_burst", "", 3100)            // left out: noise
	add("alpha", "task_created", "task:T-1", 4000)
	add("alpha", "note", "note:N-1", 4000) // same second as the task: tie broken by id
	add("alpha", "verdict", "snapshot:T-1/v1", 5000)
	add("ghost", "commit", "commit:g1", 6000)
	add("alpha", "commit", "commit:zero", 0) // unknown time: left out

	page := func(ts, id int64, limit int) []Event {
		t.Helper()
		ev, err := db.HomeInboxFeed(ctx, ts, id, limit)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	all := page(0, 0, 50)
	var refs []string
	for _, e := range all {
		refs = append(refs, e.Ref)
	}
	if want := []string{"snapshot:T-1/v1", "note:N-1", "task:T-1", "session:s1", "commit:a1"}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("feed = %v, want %v", refs, want)
	}
	// walk it two at a time with the (ts, id) cursor: nothing lost, nothing twice
	var walked []string
	var ts, id int64
	for i := 0; i < 10; i++ {
		got := page(ts, id, 2)
		if len(got) == 0 {
			break
		}
		for _, e := range got {
			walked = append(walked, e.Ref)
		}
		last := got[len(got)-1]
		ts, id = last.TS.Unix(), last.ID
	}
	if !reflect.DeepEqual(walked, refs) {
		t.Errorf("walked = %v, want %v", walked, refs)
	}
}

func TestHomeInboxProposals(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	ahProj(t, db, "alpha", "g", false, true)
	ahProj(t, db, "ghost", "g", true, true)
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	commit := func(project, id, msg, body string, ts time.Time) {
		t.Helper()
		if err := db.ReplaceCommits(ctx, project, []probe.Commit{{ID: id, VCS: "git", Time: ts, Msg: msg, Body: body}}); err != nil {
			t.Fatal(err)
		}
	}
	task := func(project, id, status, updated string) {
		t.Helper()
		if err := db.UpsertAuthoredTask(ctx, Task{ProjectID: project, ID: id, Title: id, Status: status, Updated: updated,
			Created: "2026-09-01T10:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"T-1", "T-2", "T-3", "T-4", "T-5", "T-6", "T-7"} {
		task("alpha", id, "in_progress", "2026-09-01T10:00:00Z")
	}
	task("alpha", "T-8", "done", "2026-09-01T10:00:00Z")
	task("alpha", "T-9", "in_progress", "2026-09-30T18:00:00Z") // touched after the commit: user decided
	task("ghost", "T-1", "in_progress", "2026-09-01T10:00:00Z")

	commit("alpha", "c1", "Fixes T-1: login bug", "", base)
	commit("alpha", "c2", "update docs", "Closes: task:T-2", base.Add(time.Minute))
	commit("alpha", "c3", "mention only, see T-3", "", base.Add(2*time.Minute)) // no verb
	commit("alpha", "c4", "resolved T-4, T-5 and T-6", "", base.Add(3*time.Minute))
	commit("alpha", "c5", "fix T-8", "", base.Add(4*time.Minute))          // already done
	commit("alpha", "c6", "fix T-99", "", base.Add(5*time.Minute))         // no such task
	commit("alpha", "c7", "fix T-9", "", base.Add(6*time.Minute))          // task changed later
	commit("alpha", "c8", "prefix T-7 fixer", "", base.Add(7*time.Minute)) // "fixer" is not "fixes"
	commit("ghost", "g1", "fix T-1", "", base.Add(8*time.Minute))          // hidden project
	commit("alpha", "c9", "fixed T-1 again", "", base.Add(9*time.Minute))  // newer closing commit of T-1

	got, err := db.HomeInboxProposals(ctx, base.Add(-time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, p := range got {
		lines = append(lines, fmt.Sprintf("%s/%s@%s", p.ProjectID, p.TaskID, p.CommitID))
	}
	want := []string{"alpha/T-1@c9", "alpha/T-6@c4", "alpha/T-5@c4", "alpha/T-4@c4", "alpha/T-2@c2"}
	// T-4..T-6 come from one commit; their order follows the order in the message, so check as a set there.
	if len(lines) != len(want) || lines[0] != want[0] || lines[4] != want[4] {
		t.Fatalf("proposals = %v, want %v", lines, want)
	}
	mid := strings.Join(lines[1:4], ",")
	for _, w := range []string{"alpha/T-4@c4", "alpha/T-5@c4", "alpha/T-6@c4"} {
		if !strings.Contains(mid, w) {
			t.Errorf("missing %s in %v", w, lines)
		}
	}
	// since: old commits are not looked at
	got, err = db.HomeInboxProposals(ctx, base.Add(5*time.Minute), 50)
	if err != nil || len(got) != 1 || got[0].CommitID != "c9" {
		t.Errorf("since = %+v %v", got, err)
	}
	// max
	got, _ = db.HomeInboxProposals(ctx, base.Add(-time.Hour), 2)
	if len(got) != 2 {
		t.Errorf("max = %d", len(got))
	}
}

func TestHomeApplyHidden(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	ahProj(t, db, "alpha", "personal", false, true)
	ahProj(t, db, "venv", "tools", true, true)
	ahProj(t, db, "tools.dup", "tools", false, true) // clash id; the folder name is "tools.dup" here too

	rule := func(group, name string) bool { return name == "alpha" }
	ch, err := db.HomeApplyHidden(ctx, rule)
	if err != nil {
		t.Fatal(err)
	}
	want := []HomeHiddenChange{{ID: "alpha", Hidden: true}, {ID: "venv", Hidden: false}}
	if !reflect.DeepEqual(ch, want) {
		t.Errorf("changes = %+v, want %+v", ch, want)
	}
	ids, _ := db.HomeHiddenIDs(ctx)
	if !reflect.DeepEqual(ids, map[string]bool{"alpha": true}) {
		t.Errorf("hidden = %v", ids)
	}
	// same rule again: nothing to do
	ch, err = db.HomeApplyHidden(ctx, rule)
	if err != nil || len(ch) != 0 {
		t.Errorf("second run = %+v %v", ch, err)
	}
	ok, _ := db.HomeProjectExists(ctx, "alpha")
	no, _ := db.HomeProjectExists(ctx, "nope")
	if !ok || no {
		t.Errorf("exists = %v %v", ok, no)
	}
}

func TestHomeSearchExtras(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	ahProj(t, db, "alpha", "g", false, true)
	ahProj(t, db, "100%sure", "g", false, true)
	if err := db.UpsertAuthoredSnapshot(ctx, Snapshot{ProjectID: "alpha", TaskID: "T-1", Version: 3, Title: "Dome pass",
		ImagePath: ".drashta/snapshots/T-1/v3.png", Verdict: "pending", Captured: "2026-09-30T10:00:00+05:30",
		ReviewNote: "needs warmer light"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceServersNow(ctx, []ServerNow{
		{Key: "k1", Port: 4455, ProjectID: "alpha", Kind: "dev", Name: "node", Cmd: "vite --port 4455", Title: "alpha ui"},
		{Key: "k2", Port: 8080, Kind: "dev", Name: "stray", Cmd: "python -m http.server"}, // no project: never a hit
	}); err != nil {
		t.Fatal(err)
	}

	find := func(q, project, kind string) []string {
		t.Helper()
		hits, err := db.HomeSearchExtras(ctx, q, project, kind, 30)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		var out []string
		for _, h := range hits {
			out = append(out, h.Kind+" "+h.Ref+" "+h.ProjectID)
		}
		return out
	}
	if got := find("alp", "", ""); !reflect.DeepEqual(got, []string{"project project:alpha alpha", "server server:4455 alpha"}) {
		t.Errorf("alp = %v", got)
	}
	if got := find("dome", "", ""); !reflect.DeepEqual(got, []string{"snapshot snapshot:T-1/v3 alpha"}) {
		t.Errorf("dome = %v", got)
	}
	if got := find("warmer", "", "snapshot"); len(got) != 1 {
		t.Errorf("review note = %v", got)
	}
	if got := find("vite", "", ""); !reflect.DeepEqual(got, []string{"server server:4455 alpha"}) {
		t.Errorf("cmd = %v", got)
	}
	if got := find(":4455", "", ""); !reflect.DeepEqual(got, []string{"server server:4455 alpha"}) {
		t.Errorf(":4455 = %v", got)
	}
	if got := find(":445", "", ""); len(got) != 1 {
		t.Errorf("partial port = %v", got)
	}
	for _, q := range []string{":", ":ab", ":44a", ":80x"} {
		if got := find(q, "", ""); len(got) != 0 {
			t.Errorf("%q = %v", q, got)
		}
	}
	if got := find(":8080", "", ""); len(got) != 0 {
		t.Errorf("project-less server found: %v", got)
	}
	// LIKE wildcards are plain text
	if got := find("100%", "", ""); !reflect.DeepEqual(got, []string{"project project:100%sure 100%sure"}) {
		t.Errorf("100%% = %v", got)
	}
	if got := find("%%%", "", ""); len(got) != 0 {
		t.Errorf("%%%% matched: %v", got)
	}
	if got := find("a_p", "", ""); len(got) != 0 {
		t.Errorf("underscore matched: %v", got)
	}
	// filters
	if got := find("alpha", "", "server"); len(got) != 1 || !strings.HasPrefix(got[0], "server ") {
		t.Errorf("kind=server = %v", got)
	}
	if got := find("alpha", "", "commit"); len(got) != 0 {
		t.Errorf("kind=commit = %v", got)
	}
	if got := find("alpha", "100%sure", ""); len(got) != 0 {
		t.Errorf("project filter = %v", got)
	}
	// too short
	if got := find("al", "", ""); len(got) != 0 {
		t.Errorf("short = %v", got)
	}
	if got := find("  ", "", ""); len(got) != 0 {
		t.Errorf("blank = %v", got)
	}
}

func TestHomeQueriesHonourContext(t *testing.T) {
	db := openTest(t)
	ctx, cancel := context.WithCancel(bg())
	cancel()
	if _, err := db.HomeBootCounts(ctx); err == nil {
		t.Error("cancelled context was ignored")
	}
}
