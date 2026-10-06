package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/gh"
)

type pjtGHReply struct {
	PRs []struct {
		Number    int
		Title     string
		IsDraft   bool
		URL       string
		HeadRef   string `json:"headRefName"`
		UpdatedAt string
	} `json:"prs"`
	Issues []struct {
		Number int
		URL    string
	}
	IssueCount int
	Runs       []struct {
		ID         int64
		Name       string
		Status     string
		Conclusion *string
		URL        string
	}
	FetchedAt   string
	Unavailable *struct{ Reason string }
}

func pjtGHData(now time.Time) ProjectGitHubData {
	failure := "failure"
	return ProjectGitHubData{
		PRs:        []ProjectGHPR{{Number: 7, Title: "Add a thing", State: "OPEN", IsDraft: true, Branch: "thing", URL: "https://github.com/fixture/alpha/pull/7", UpdatedAt: "2026-09-30T10:00:00Z"}},
		Issues:     []ProjectGHIssue{{Number: 3, Title: "Bug", State: "OPEN", URL: "https://github.com/fixture/alpha/issues/3"}},
		IssueCount: 12,
		Runs:       []ProjectGHRun{{ID: 99, Name: "ci", Status: "completed", Conclusion: &failure, URL: "https://github.com/fixture/alpha/actions/runs/99"}},
		FetchedAt:  now,
	}
}

func TestGitHubBlock(t *testing.T) {
	e := pjtNew(t, nil)
	e.gh.data = pjtGHData(e.clock.Now())
	rec := e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	var r pjtGHReply
	pjtJSON(t, rec, &r)
	if len(r.PRs) != 1 || r.PRs[0].Number != 7 || !r.PRs[0].IsDraft || r.PRs[0].HeadRef != "thing" || r.PRs[0].UpdatedAt == "" ||
		len(r.Issues) != 1 || r.IssueCount != 12 || len(r.Runs) != 1 || r.Runs[0].Conclusion == nil || *r.Runs[0].Conclusion != "failure" ||
		r.FetchedAt == "" || r.Unavailable != nil {
		t.Errorf("github = %+v", r)
	}
	if strings.Contains(rec.Body.String(), `"unavailable"`) {
		t.Error("a good answer must not carry unavailable")
	}
}

func TestGitHubCachedForFiveMinutes(t *testing.T) {
	e := pjtNew(t, nil)
	e.gh.data = pjtGHData(e.clock.Now())
	for i := 0; i < 3; i++ {
		pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	}
	if e.gh.calls != 1 {
		t.Fatalf("source called %d times for 3 requests, want 1", e.gh.calls)
	}
	e.clock.Advance(4*time.Minute + 59*time.Second)
	pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	if e.gh.calls != 1 {
		t.Errorf("still inside 5 minutes, but the source was called again (%d)", e.gh.calls)
	}
	e.clock.Advance(2 * time.Second)
	pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	if e.gh.calls != 2 {
		t.Errorf("after 5 minutes the source should be asked again (%d calls)", e.gh.calls)
	}
	// A forced refresh by the wiring code drops the cache.
	e.api.InvalidateGitHub("alpha")
	pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	if e.gh.calls != 3 {
		t.Errorf("after InvalidateGitHub the source should be asked again (%d calls)", e.gh.calls)
	}
	e.api.InvalidateGitHub("")
	pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	if e.gh.calls != 4 {
		t.Errorf("after InvalidateGitHub(\"\") (%d calls)", e.gh.calls)
	}
}

func TestGitHubUnavailableIsA200(t *testing.T) {
	e := pjtNew(t, nil)
	e.gh.data = ProjectGitHubData{Unavailable: &probe.Unavailable{Reason: "gh is not logged in"}}
	rec := e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	var r pjtGHReply
	pjtJSON(t, rec, &r)
	if r.Unavailable == nil || r.Unavailable.Reason != "gh is not logged in" || r.FetchedAt == "" {
		t.Errorf("unavailable = %+v", r)
	}
	body := rec.Body.String()
	for _, want := range []string{`"prs":[]`, `"issues":[]`, `"runs":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s: %s", want, body)
		}
	}
	// An unavailable answer is kept for one minute only, so a login is noticed soon.
	pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	if e.gh.calls != 1 {
		t.Errorf("calls = %d, want 1", e.gh.calls)
	}
	e.clock.Advance(61 * time.Second)
	e.gh.data = pjtGHData(e.clock.Now())
	var fresh pjtGHReply
	pjtJSON(t, e.get("/api/projects/alpha/github"), &fresh)
	if e.gh.calls != 2 || fresh.Unavailable != nil || len(fresh.PRs) != 1 {
		t.Errorf("after the minute: calls %d, %+v", e.gh.calls, fresh)
	}
}

func TestGitHubErrorsAndMissingParts(t *testing.T) {
	// A source error is a muted "unavailable", not a 500, and its text is not repeated.
	e := pjtNew(t, nil)
	e.gh.err = errors.New("dial tcp: lookup api.github.com: secret-proxy.corp")
	rec := e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "unavailable") || strings.Contains(rec.Body.String(), "secret-proxy") {
		t.Errorf("error body = %s", rec.Body.String())
	}
	// With an old good answer, a later failure serves the old answer.
	e.gh.err = nil
	e.gh.data = pjtGHData(e.clock.Now())
	e.api.InvalidateGitHub("")
	pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	e.clock.Advance(10 * time.Minute)
	e.gh.err = errors.New("offline")
	var stale pjtGHReply
	pjtJSON(t, e.get("/api/projects/alpha/github"), &stale)
	if len(stale.PRs) != 1 || stale.Unavailable != nil {
		t.Errorf("stale answer expected: %+v", stale)
	}

	// A project without a remote never reaches the source.
	e.gh.calls = 0
	rec = e.get("/api/projects/beta/github")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "no git remote") || e.gh.calls != 0 {
		t.Errorf("beta: %s (calls %d)", rec.Body.String(), e.gh.calls)
	}

	// No GitHub source wired.
	e2 := pjtNew(t, func(d *ProjectDeps) { d.GitHub = nil })
	rec = e2.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "unavailable") {
		t.Errorf("no source: %s", rec.Body.String())
	}
}

func TestGitHubOnlyHandsOutGitHubLinks(t *testing.T) {
	e := pjtNew(t, nil)
	d := pjtGHData(e.clock.Now())
	d.PRs[0].URL = "javascript:alert(1)"
	d.Issues[0].URL = "http://evil.example/x"
	d.Runs[0].URL = "https://github.com.evil.example/x"
	e.gh.data = d
	rec := e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	var r pjtGHReply
	pjtJSON(t, rec, &r)
	if r.PRs[0].URL != "" || r.Issues[0].URL != "" || r.Runs[0].URL != "" {
		t.Errorf("foreign links were passed on: %+v", r)
	}
}

// ---- the mapping from the gh probe ----

func pjtGHResult(at time.Time) gh.Result {
	return gh.Result{
		Repo: "fixture/alpha", Branch: "main",
		PRs: []gh.PR{
			{Number: 7, Title: "Add a thing", State: "open", IsDraft: true, Branch: "thing", Author: "octo", UpdatedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC), URL: "https://github.com/fixture/alpha/pull/7"},
			{Number: 5, Title: "Old one", State: "merged", Branch: "old", URL: "https://github.com/fixture/alpha/pull/5"}, // no UpdatedAt
		},
		Issues: gh.IssueSummary{OpenCount: 12, Capped: false, Latest: []gh.Issue{
			{Number: 3, Title: "Bug", State: "open", UpdatedAt: time.Date(2026, 9, 29, 8, 30, 0, 0, time.UTC), URL: "https://github.com/fixture/alpha/issues/3"},
			{Number: 2, Title: "Other", State: "open", URL: "https://github.com/fixture/alpha/issues/2"},
		}},
		Runs: []gh.Run{{ID: 99, Name: "ci", Status: "in_progress", Conclusion: "", URL: "https://github.com/fixture/alpha/actions/runs/99",
			CreatedAt: time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC)}},
		FetchedAt: at,
	}
}

func TestGitHubMapsTheProbeResult(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	d := ProjectGitHubFromResult(pjtGHResult(at))
	if len(d.PRs) != 2 || d.PRs[0].Number != 7 || !d.PRs[0].IsDraft || d.PRs[0].Branch != "thing" || d.PRs[0].Author != "octo" ||
		d.PRs[0].State != "open" || d.PRs[0].UpdatedAt != "2026-09-30T10:00:00Z" || d.PRs[1].UpdatedAt != "" || d.PRs[1].State != "merged" {
		t.Errorf("PRs = %+v", d.PRs)
	}
	// Issues.Latest is the flat issues list; OpenCount is the count.
	if len(d.Issues) != 2 || d.Issues[0].Number != 3 || d.Issues[0].UpdatedAt != "2026-09-29T08:30:00Z" || d.IssueCount != 12 || d.IssuesCapped {
		t.Errorf("issues = %+v count=%d", d.Issues, d.IssueCount)
	}
	// A run that has not finished has no conclusion.
	if len(d.Runs) != 1 || d.Runs[0].ID != 99 || d.Runs[0].Status != "in_progress" || d.Runs[0].Conclusion != nil || d.Runs[0].CreatedAt != "2026-09-30T11:00:00Z" {
		t.Errorf("runs = %+v", d.Runs)
	}
	if !d.FetchedAt.Equal(at) || d.Unavailable != nil || !d.StaleSince.IsZero() {
		t.Errorf("meta = %+v", d)
	}

	// Capped count, finished run, partial and stale markers pass through.
	r := pjtGHResult(at)
	r.Issues.Capped, r.Issues.OpenCount = true, 100
	r.Runs[0].Conclusion = "success"
	r.Partial = []string{"issues"}
	r.StaleSince = at.Add(-time.Hour)
	d = ProjectGitHubFromResult(r)
	if !d.IssuesCapped || d.IssueCount != 100 || d.Runs[0].Conclusion == nil || *d.Runs[0].Conclusion != "success" ||
		len(d.Partial) != 1 || d.StaleSince.IsZero() {
		t.Errorf("flags = %+v", d)
	}

	// An unavailable result keeps its reason and has empty lists, never nil ones.
	d = ProjectGitHubFromResult(gh.Result{Unavailable: &probe.Unavailable{Reason: gh.ReasonNotLoggedIn}})
	if d.Unavailable == nil || d.Unavailable.Reason != gh.ReasonNotLoggedIn || d.PRs == nil || d.Issues == nil || d.Runs == nil {
		t.Errorf("unavailable = %+v", d)
	}
}

type pjtGHCacheFake struct {
	res   gh.Result
	err   error
	calls int
	force []bool
}

func (c *pjtGHCacheFake) Get(ctx context.Context, p probe.Project, remote, branch string, force bool) (gh.Result, error) {
	c.calls++
	c.force = append(c.force, force)
	return c.res, c.err
}

func TestGitHubRouteThroughTheCacheAdapter(t *testing.T) {
	fake := &pjtGHCacheFake{}
	e := pjtNew(t, func(d *ProjectDeps) { d.GitHub = ProjectGitHubFromCache{Cache: fake} })
	fake.res = pjtGHResult(e.clock.Now())

	rec := e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	body := rec.Body.String()
	// issues is a flat array and the count sits beside it, as web/app/lib/types.ts expects.
	for _, want := range []string{`"issues":[{"number":3`, `"issueCount":12`, `"headRefName":"thing"`, `"conclusion":null`,
		`"updatedAt":"2026-09-30T10:00:00Z"`, `"fetchedAt":"2026-10-01T12:00:00Z"`} {
		if !strings.Contains(body, want) {
			t.Errorf("reply lacks %s: %s", want, body)
		}
	}
	for _, not := range []string{`"openCount"`, `"latest"`, `"issueCountCapped"`, `"staleSince"`, `"partial"`, `"unavailable"`} {
		if strings.Contains(body, not) {
			t.Errorf("reply has %s: %s", not, body)
		}
	}
	// The read route never forces a fetch.
	if fake.calls != 1 || fake.force[0] {
		t.Errorf("cache calls = %d force = %v", fake.calls, fake.force)
	}

	// Unavailable and old-data markers survive the trip as a normal 200.
	e.api.InvalidateGitHub("")
	fake.res = gh.Result{Unavailable: &probe.Unavailable{Reason: gh.ReasonNotLoggedIn}}
	rec = e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"unavailable":{"reason":"gh not logged in"}`) || !strings.Contains(rec.Body.String(), `"issues":[]`) {
		t.Errorf("unavailable = %s", rec.Body.String())
	}
	e.api.InvalidateGitHub("")
	r := pjtGHResult(e.clock.Now())
	r.StaleSince, r.Partial = e.clock.Now().Add(-10*time.Minute), []string{"runs"}
	r.Issues.Capped = true
	fake.res = r
	rec = e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	for _, want := range []string{`"staleSince":"2026-10-01T11:50:00Z"`, `"partial":["runs"]`, `"issueCountCapped":true`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("reply lacks %s: %s", want, rec.Body.String())
		}
	}
	// A cache error becomes the muted note, not a 500.
	e.api.InvalidateGitHub("")
	fake.err = errors.New("gh timed out")
	rec = e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"unavailable"`) {
		t.Errorf("error = %s", rec.Body.String())
	}
}

func TestGitHubRouteWithTheRealCacheAndAFakeFetch(t *testing.T) {
	fetches := 0
	var cache *gh.Cache
	e := pjtNew(t, func(d *ProjectDeps) {
		clock := &pjtClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
		cache = &gh.Cache{
			Now: clock.Now,
			Fetch: func(ctx context.Context, p probe.Project, remote, branch string) (gh.Result, error) {
				fetches++
				if remote != "git@github.com:fixture/alpha.git" || branch != "main" || p.ID != "alpha" {
					t.Errorf("fetch got project %q remote %q branch %q", p.ID, remote, branch)
				}
				return pjtGHResult(clock.Now()), nil
			},
		}
		d.GitHub = ProjectGitHubFromCache{Cache: cache}
		d.Now = clock.Now
	})
	var r pjtGHReply
	rec := e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	pjtJSON(t, rec, &r)
	if len(r.PRs) != 2 || len(r.Issues) != 2 || r.IssueCount != 12 || len(r.Runs) != 1 || r.Runs[0].Conclusion != nil || fetches != 1 {
		t.Errorf("reply = %+v fetches=%d", r, fetches)
	}
	pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	if fetches != 1 {
		t.Errorf("fetches = %d, want 1", fetches)
	}
}

// TestGitHubPartialKeepsOldParts (review I8): when the PR call fails on a
// refresh, the reply keeps the earlier PRs and says partial:["prs"].
func TestGitHubPartialKeepsOldParts(t *testing.T) {
	partial := false
	var cache *gh.Cache
	e := pjtNew(t, func(d *ProjectDeps) {
		clock := &pjtClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
		cache = &gh.Cache{
			Now: clock.Now,
			Fetch: func(ctx context.Context, p probe.Project, remote, branch string) (gh.Result, error) {
				r := pjtGHResult(clock.Now())
				if partial {
					r.PRs, r.Partial = nil, []string{"prs"}
				}
				return r, nil
			},
		}
		d.GitHub = ProjectGitHubFromCache{Cache: cache}
		d.Now = clock.Now
	})
	pjtWant(t, e.get("/api/projects/alpha/github"), 200)
	partial = true
	cache.Invalidate("alpha")
	e.api.InvalidateGitHub("alpha")
	rec := e.get("/api/projects/alpha/github")
	pjtWant(t, rec, 200)
	var r pjtGHReply
	pjtJSON(t, rec, &r)
	if len(r.PRs) != 2 || !strings.Contains(rec.Body.String(), `"partial":["prs"]`) {
		t.Errorf("reply = %s", rec.Body.String())
	}
}
