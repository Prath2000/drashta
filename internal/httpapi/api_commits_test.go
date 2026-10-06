package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/git"
	"github.com/Prath2000/drashta/internal/store"
)

// pjtVCS is a fake history reader. Lists are newest first.
type pjtVCS struct {
	mu        sync.Mutex
	list      map[string][]probe.Commit // vcs -> commits
	pathList  map[string][]probe.Commit // vcs+"|"+path -> commits (used when a path is given)
	detail    map[string]probe.CommitDetail
	detailErr error
	commitErr map[string]error // vcs -> error from Commits
	diff      map[string]string
	diffCut   bool
	diffErr   error
	calls     []string
}

func (v *pjtVCS) Commits(ctx context.Context, vcs string, p probe.Project, skip, limit int, path string) ([]probe.Commit, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls = append(v.calls, fmt.Sprintf("%s skip=%d limit=%d path=%q", vcs, skip, limit, path))
	if err := v.commitErr[vcs]; err != nil {
		return nil, err
	}
	all := v.list[vcs]
	if path != "" {
		all = v.pathList[vcs+"|"+path]
	}
	if skip >= len(all) {
		return nil, nil
	}
	end := skip + limit
	if end > len(all) {
		end = len(all)
	}
	return append([]probe.Commit(nil), all[skip:end]...), nil
}

func (v *pjtVCS) Detail(ctx context.Context, vcs string, p probe.Project, id string) (probe.CommitDetail, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.detailErr != nil {
		return probe.CommitDetail{}, v.detailErr
	}
	for k, d := range v.detail {
		if strings.HasPrefix(k, vcs+":") && strings.HasPrefix(strings.TrimPrefix(k, vcs+":"), id) {
			return d, nil
		}
	}
	return probe.CommitDetail{}, git.ErrNotFound
}

func (v *pjtVCS) DirtyDiff(ctx context.Context, vcs string, p probe.Project) (string, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.diffErr != nil {
		return "", false, v.diffErr
	}
	return v.diff[vcs], v.diffCut, nil
}

func pjtCommits(vcs string, n int, newest time.Time) []probe.Commit {
	out := make([]probe.Commit, n)
	for i := range out {
		out[i] = probe.Commit{
			ID: fmt.Sprintf("%s%038x", map[string]string{"git": "a", "fossil": "f"}[vcs], n-i), VCS: vcs, Author: "A",
			Time: newest.Add(-time.Duration(i) * time.Hour), Msg: fmt.Sprintf("%s commit %d", vcs, n-i), Plus: 1, Minus: 1, NFiles: 1,
		}
	}
	return out
}

type pjtCommitsReply struct {
	Commits []struct {
		ID, VCS, Author, TS, Msg string
		Plus, Minus, NFiles      int
		Tasks                    []string
	}
	Next *string
}

func (e *pjtEnv) commitsPage(q string) pjtCommitsReply {
	e.t.Helper()
	rec := e.get("/api/projects/alpha/commits" + q)
	pjtWant(e.t, rec, 200)
	var r pjtCommitsReply
	pjtJSON(e.t, rec, &r)
	return r
}

func TestProjectCommitsCachedPaging(t *testing.T) {
	e := pjtNew(t, nil)
	p1 := e.commitsPage("?limit=1")
	if len(p1.Commits) != 1 || p1.Next == nil {
		t.Fatalf("page 1 = %+v", p1)
	}
	c := p1.Commits[0]
	if c.ID != e.commits[0] || len(c.ID) != 40 || c.VCS != "git" || c.Author != "Fixture Author" || c.Msg != "second commit" ||
		c.Plus == 0 || c.NFiles != 1 || c.Tasks == nil || len(c.Tasks) != 0 {
		t.Errorf("commit = %+v", c)
	}
	if _, err := time.Parse(time.RFC3339, c.TS); err != nil {
		t.Errorf("ts %q is not RFC 3339", c.TS)
	}

	p2 := e.commitsPage("?limit=1&cursor=" + url.QueryEscape(*p1.Next))
	if len(p2.Commits) != 1 || p2.Commits[0].ID != e.commits[1] || p2.Next != nil {
		t.Fatalf("page 2 = %+v", p2)
	}
	// The task link (a 10 character hash prefix) is found on the first commit only.
	if got := p2.Commits[0].Tasks; len(got) != 1 || got[0] != "T-1" {
		t.Errorf("tasks = %v, want [T-1]", got)
	}

	// One big page has both, no next.
	all := e.commitsPage("")
	if len(all.Commits) != 2 || all.Next != nil {
		t.Errorf("all = %+v", all)
	}

	for name, q := range map[string]string{
		"junk cursor":     "?cursor=x",
		"bad store":       "?cursor=c.abc.def",
		"bad hash":        "?cursor=c.5.ZZ",
		"negative live":   "?cursor=l.-1.0",
		"live text":       "?cursor=l.a.b",
		"unknown phase":   "?cursor=z.1.2",
		"limit zero":      "?limit=0",
		"limit too big":   "?limit=201",
		"limit text":      "?limit=abc",
		"path dotdot":     "?path=../x",
		"path absolute":   "?path=/etc/passwd",
		"path via cursor": "?path=a%00b",
	} {
		pjtWant(t, e.get("/api/projects/alpha/commits"+q), 400)
		_ = name
	}

	// No version control at all: an empty page.
	rec := e.get("/api/projects/beta/commits")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"commits":[]`) || !strings.Contains(rec.Body.String(), `"next":null`) {
		t.Errorf("beta = %s", rec.Body.String())
	}
}

func TestProjectCommitsPathIsLive(t *testing.T) {
	e := pjtNew(t, nil)
	r := e.commitsPage("?path=src/main.go")
	if len(r.Commits) != 2 || r.Commits[0].Msg != "second commit" {
		t.Fatalf("history of main.go = %+v", r)
	}
	if r := e.commitsPage("?path=README.md"); len(r.Commits) != 1 || r.Commits[0].Msg != "first commit for T-1" {
		t.Errorf("history of README.md = %+v", r)
	}
	if r := e.commitsPage("?path=no/such/file.txt"); len(r.Commits) != 0 || r.Next != nil {
		t.Errorf("history of a missing path = %+v", r)
	}
	// A live cursor continues the listing; follow it to the end without repeats.
	seen := map[string]bool{}
	q := "?path=src/main.go&limit=1"
	for i := 0; i < 5; i++ {
		page := e.commitsPage(q)
		for _, c := range page.Commits {
			if seen[c.ID] {
				t.Fatalf("commit %s came twice", c.ID)
			}
			seen[c.ID] = true
		}
		if page.Next == nil {
			break
		}
		if !strings.HasPrefix(*page.Next, "l.") {
			t.Fatalf("a path listing must use live cursors, got %q", *page.Next)
		}
		q = "?path=src/main.go&limit=1&cursor=" + url.QueryEscape(*page.Next)
	}
	if len(seen) != 2 {
		t.Errorf("followed the cursors and saw %d commits, want 2", len(seen))
	}
}

func TestProjectCommitsLivePhaseAfterCache(t *testing.T) {
	newest := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	fake := &pjtVCS{list: map[string][]probe.Commit{"git": pjtCommits("git", 230, newest)}}
	e := pjtNew(t, func(d *ProjectDeps) { d.VCS = fake })
	ctx := context.Background()
	big := filepath.Join(e.projects, "g", "big")
	if err := os.MkdirAll(big, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertProject(ctx, store.Project{ID: "big", Name: "big", Group: "g", Path: big, VCS: "git"}); err != nil {
		t.Fatal(err)
	}
	// The index holds the newest 200 of 230.
	if err := e.db.ReplaceCommits(ctx, "big", fake.list["git"][:200]); err != nil {
		t.Fatal(err)
	}
	var ids []string
	cursor := ""
	pages := 0
	for {
		rec := e.get("/api/projects/big/commits?limit=100" + cursor)
		pjtWant(t, rec, 200)
		var r pjtCommitsReply
		pjtJSON(t, rec, &r)
		for _, c := range r.Commits {
			ids = append(ids, c.ID)
		}
		pages++
		if r.Next == nil {
			break
		}
		cursor = "&cursor=" + url.QueryEscape(*r.Next)
		if pages > 6 {
			t.Fatal("paging does not end")
		}
	}
	if len(ids) != 230 {
		t.Fatalf("got %d commits over %d pages, want 230", len(ids), pages)
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if seen[id] {
			t.Fatalf("commit %s twice", id)
		}
		seen[id] = true
		if id != fake.list["git"][i].ID {
			t.Fatalf("commit %d is %s, want %s (order broken)", i, id, fake.list["git"][i].ID)
		}
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3 (100 cached, 100 cached, 30 live)", pages)
	}

	// A small cache (fewer than 200 rows) never asks the probes: the list is complete.
	fake.calls = nil
	small := &pjtVCS{list: map[string][]probe.Commit{"git": pjtCommits("git", 50, newest)}}
	e2 := pjtNew(t, func(d *ProjectDeps) { d.VCS = small })
	r := e2.commitsPage("?limit=200")
	if len(r.Commits) != 2 || r.Next != nil || len(small.calls) != 0 {
		t.Errorf("small cache: %d commits, next %v, probe calls %v", len(r.Commits), r.Next, small.calls)
	}
}

func TestProjectCommitsMergeGitAndFossil(t *testing.T) {
	newest := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	g := pjtCommits("git", 4, newest)                         // 12:00, 11:00, 10:00, 09:00
	f := pjtCommits("fossil", 4, newest.Add(-30*time.Minute)) // 11:30, 10:30, 09:30, 08:30
	fake := &pjtVCS{
		list:     map[string][]probe.Commit{"git": g, "fossil": f},
		pathList: map[string][]probe.Commit{"git|x.go": g, "fossil|x.go": f},
	}
	e := pjtNew(t, func(d *ProjectDeps) { d.VCS = fake })
	dual := filepath.Join(e.projects, "g", "dual")
	if err := os.MkdirAll(dual, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertProject(context.Background(), store.Project{ID: "dual", Name: "dual", Group: "g", Path: dual, VCS: "git,fossil"}); err != nil {
		t.Fatal(err)
	}
	var times []string
	cursor := ""
	for i := 0; i < 6; i++ {
		rec := e.get("/api/projects/dual/commits?path=x.go&limit=3" + cursor)
		pjtWant(t, rec, 200)
		var r pjtCommitsReply
		pjtJSON(t, rec, &r)
		for _, c := range r.Commits {
			times = append(times, c.TS+" "+c.VCS)
		}
		if r.Next == nil {
			break
		}
		cursor = "&cursor=" + url.QueryEscape(*r.Next)
	}
	want := []string{
		"12:00 git", "11:30 fossil", "11:00 git", "10:30 fossil", "10:00 git", "09:30 fossil", "09:00 git", "08:30 fossil",
	}
	if len(times) != len(want) {
		t.Fatalf("got %d commits: %v", len(times), times)
	}
	for i, w := range want {
		hm, vcs, _ := strings.Cut(w, " ")
		if !strings.Contains(times[i], "T"+hm+":00") || !strings.HasSuffix(times[i], " "+vcs) {
			t.Errorf("commit %d = %q, want %s", i, times[i], w)
		}
	}
}

func TestProjectCommitDetail(t *testing.T) {
	e := pjtNew(t, nil)
	first, second := e.commits[1], e.commits[0]
	type reply struct {
		Commit struct {
			ID, VCS, Msg string
			Tasks        []string
			NFiles       int
		}
		Body  string
		Files []struct {
			Path        string
			Plus, Minus int
		}
		Patch          string
		PatchTruncated bool
	}
	for _, id := range []string{second, second[:10], second[:7], strings.ToUpper(second[:8])} {
		rec := e.get("/api/projects/alpha/commits/" + id)
		pjtWant(t, rec, 200)
		var r reply
		pjtJSON(t, rec, &r)
		if r.Commit.ID != second || r.Commit.Msg != "second commit" || r.Commit.VCS != "git" || r.PatchTruncated ||
			len(r.Files) != 1 || r.Files[0].Path != "src/main.go" || r.Files[0].Plus == 0 || !strings.Contains(r.Patch, "+\tprintln(\"hi\")") {
			t.Errorf("detail of %s = %+v", id, r)
		}
	}
	rec := e.get("/api/projects/alpha/commits/" + first[:12])
	pjtWant(t, rec, 200)
	var r reply
	pjtJSON(t, rec, &r)
	if len(r.Commit.Tasks) != 1 || r.Commit.Tasks[0] != "T-1" || r.Commit.NFiles < 3 {
		t.Errorf("first commit = %+v", r.Commit)
	}

	for _, id := range []string{"zzzz1234", "abc12", "..", "%2e%2e", "abc%2Fdef", "g123456", strings.Repeat("a", 65), "abc.123"} {
		// The mux may redirect a dotted path (307) before our code runs. Whatever happens, it is not a 200.
		if rec := e.get("/api/projects/alpha/commits/" + id); rec.Code == 200 {
			t.Errorf("id %q was accepted", id)
		}
	}
	for _, id := range []string{"zzzz1234", "abc12", "g123456", strings.Repeat("a", 65)} {
		pjtWant(t, e.get("/api/projects/alpha/commits/"+id), 400)
	}
	pjtWant(t, e.get("/api/projects/alpha/commits/deadbeefdeadbeef"), 404)
}

func TestProjectCommitDetailCapsAndErrors(t *testing.T) {
	huge := strings.Repeat("+x\n", 200_000) // 600 KB
	full := "a" + strings.Repeat("0", 39)
	fake := &pjtVCS{detail: map[string]probe.CommitDetail{
		"git:" + full: {Commit: probe.Commit{ID: full, VCS: "git", Msg: "big", Body: "why\n", Files: []probe.CommitFile{{Path: "x", Plus: 1}}}, Patch: huge},
	}}
	e := pjtNew(t, func(d *ProjectDeps) { d.VCS = fake })
	rec := e.get("/api/projects/alpha/commits/" + full[:12])
	pjtWant(t, rec, 200)
	var r struct {
		Patch          string
		PatchTruncated bool
		Body           string
	}
	pjtJSON(t, rec, &r)
	if !r.PatchTruncated || len(r.Patch) > pjPatchCap || len(r.Patch) < pjPatchCap-8 || r.Body != "why\n" {
		t.Errorf("huge patch: truncated=%v len=%d", r.PatchTruncated, len(r.Patch))
	}
	// A probe that says "truncated" itself is passed on.
	fake.detail["git:"+full] = probe.CommitDetail{Commit: probe.Commit{ID: full, VCS: "git"}, Patch: "small", PatchTruncated: true}
	pjtJSON(t, e.get("/api/projects/alpha/commits/"+full[:12]), &r)
	if !r.PatchTruncated || r.Patch != "small" {
		t.Errorf("probe truncated flag lost: %+v", r)
	}
	// An unexpected failure is a 500 that does not repeat the error text.
	fake.detailErr = errors.New("boom: /private/path/secret")
	rec = e.get("/api/projects/alpha/commits/" + full[:12])
	pjtWant(t, rec, 500)
	if strings.Contains(rec.Body.String(), "secret") {
		t.Error("the error detail leaked into the reply")
	}
	fake.detailErr = git.ErrNotFound
	pjtWant(t, e.get("/api/projects/alpha/commits/"+full[:12]), 404)
}

func TestProjectDirty(t *testing.T) {
	e := pjtNew(t, nil)
	rec := e.get("/api/projects/alpha/dirty")
	pjtWant(t, rec, 200)
	var r struct {
		Files         []struct{ Path, State string }
		Diff          string
		DiffTruncated bool
		Unavailable   *string
		FetchedAt     string
	}
	pjtJSON(t, rec, &r)
	states := map[string]string{}
	for _, f := range r.Files {
		states[f.Path] = f.State
	}
	if states["src/main.go"] != "M" || states["new.txt"] != "?" {
		t.Errorf("files = %+v", r.Files)
	}
	if !strings.Contains(r.Diff, "+\tprintln(\"dirty\")") || r.DiffTruncated || r.Unavailable != nil || r.FetchedAt == "" {
		t.Errorf("diff = %q truncated=%v unavailable=%v", r.Diff, r.DiffTruncated, r.Unavailable)
	}
	if body := e.get("/api/projects/beta/dirty").Body.String(); !strings.Contains(body, `"files":[]`) || !strings.Contains(body, `"diff":""`) {
		t.Errorf("beta dirty = %s", body)
	}

	// An oversized diff is cut and flagged.
	fake := &pjtVCS{diff: map[string]string{"git": strings.Repeat("+line\n", 100_000)}}
	e = pjtNew(t, func(d *ProjectDeps) { d.VCS = fake })
	pjtJSON(t, e.get("/api/projects/alpha/dirty"), &r)
	if !r.DiffTruncated || len(r.Diff) > pjPatchCap || len(r.Diff) < pjPatchCap-8 {
		t.Errorf("huge diff: truncated=%v len=%d", r.DiffTruncated, len(r.Diff))
	}
	// A probe that cut the diff itself is passed on.
	fake.diff["git"], fake.diffCut = "small", true
	pjtJSON(t, e.get("/api/projects/alpha/dirty"), &r)
	if !r.DiffTruncated || r.Diff != "small" {
		t.Errorf("probe truncated flag lost: %+v", r)
	}
	// A failing diff keeps the file list and says why the diff is missing.
	fake.diffErr = errors.New("git exploded")
	rec = e.get("/api/projects/alpha/dirty")
	pjtWant(t, rec, 200)
	r.Unavailable = nil
	pjtJSON(t, rec, &r)
	if r.Unavailable == nil || len(r.Files) < 2 || strings.Contains(rec.Body.String(), "exploded") {
		t.Errorf("failing diff: %+v / %s", r, rec.Body.String())
	}
}

func TestProjectCommitsLiveErrors(t *testing.T) {
	newest := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	g := pjtCommits("git", 3, newest)
	f := pjtCommits("fossil", 3, newest.Add(-time.Minute))
	fake := &pjtVCS{
		list:      map[string][]probe.Commit{"git": g, "fossil": f},
		pathList:  map[string][]probe.Commit{"git|x.go": g, "fossil|x.go": f},
		commitErr: map[string]error{},
	}
	e := pjtNew(t, func(d *ProjectDeps) { d.VCS = fake })
	dual := filepath.Join(e.projects, "g", "dual")
	if err := os.MkdirAll(dual, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertProject(context.Background(), store.Project{ID: "dual", Name: "dual", Group: "g", Path: dual, VCS: "git,fossil"}); err != nil {
		t.Fatal(err)
	}
	url := "/api/projects/dual/commits?path=x.go"

	// One vcs failing does not lose the other one's commits.
	fake.commitErr["fossil"] = errors.New("fossil is locked")
	rec := e.get(url)
	pjtWant(t, rec, 200)
	var r pjtCommitsReply
	pjtJSON(t, rec, &r)
	if len(r.Commits) != 3 || r.Commits[0].VCS != "git" {
		t.Errorf("git only = %+v", r.Commits)
	}
	// A folder that is not a checkout is skipped without noise.
	fake.commitErr["fossil"] = fossil.ErrNotCheckout
	fake.commitErr["git"] = git.ErrNotRepo
	rec = e.get(url)
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"commits":[]`) {
		t.Errorf("no repo = %s", rec.Body.String())
	}
	// Every vcs failing is a 500 that keeps the detail out of the reply.
	fake.commitErr["fossil"] = errors.New("boom /private/one")
	fake.commitErr["git"] = errors.New("boom /private/two")
	rec = e.get(url)
	pjtWant(t, rec, 500)
	if strings.Contains(rec.Body.String(), "private") {
		t.Error("error detail leaked")
	}
	// A bad path from the probe is the caller's mistake.
	fake.commitErr["git"], fake.commitErr["fossil"] = git.ErrBadPath, nil
	pjtWant(t, e.get(url), 400)
	fake.commitErr["git"], fake.commitErr["fossil"] = nil, fossil.ErrBadPath
	pjtWant(t, e.get(url), 400)
}

func TestProjectTasksForRefs(t *testing.T) {
	full := "abc1234def0000000000000000000000000000ff"
	refs := []store.ProjTaskRef{
		{TaskID: "T-10", Ref: "commit:abc1234"},
		{TaskID: "T-2", Ref: "commit:abc1234"},
		{TaskID: "T-3", Ref: "alpha:commit:abc1234def"},
		{TaskID: "T-4", Ref: "commit:abc12"},         // too short to trust
		{TaskID: "T-5", Ref: "commit:ABC1234#L3"},    // fragment and case are ignored
		{TaskID: "T-6", Ref: "file:abc1234"},         // other type
		{TaskID: "T-7", Ref: "commit:ffffff"},        // other commit
		{TaskID: "T-8", Ref: "other:commit:abc1234"}, // another project's ref
		{TaskID: "T-2", Ref: "commit:abc1234def"},    // duplicate task
	}
	got := pjTasksFor(refs, "alpha", "commit", full, 6)
	want := []string{"T-2", "T-3", "T-5", "T-10"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tasks = %v, want %v", got, want)
	}
	if got := pjTasksFor(nil, "alpha", "commit", full, 6); got == nil || len(got) != 0 {
		t.Errorf("no refs must give an empty list, got %#v", got)
	}
	for _, c := range []struct {
		a, b string
		less bool
	}{{"T-2", "T-10", true}, {"T-10", "T-2", false}, {"F-1", "T-1", true}, {"x", "y", true}, {"T-1", "T-1", false}} {
		if pjNatLess(c.a, c.b) != c.less {
			t.Errorf("pjNatLess(%q, %q) != %v", c.a, c.b, c.less)
		}
	}
}
