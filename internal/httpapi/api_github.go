package httpapi

// GitHub block of the project Overview (decision A6).
//
//	GET /api/projects/{p}/github   {prs:[], issues:[], issueCount, runs:[], fetchedAt, unavailable?:{reason}}
//
// "unavailable" is a normal answer with status 200: gh is missing, not logged in, the remote is not on
// github.com, there is no remote, or the call failed. The page shows a muted one-line note. The arrays
// are then empty. drashta never touches a GitHub token: the gh command does that by itself.
//
// issues is a flat array: the latest open issues (up to 20). issueCount is the number of open issues
// (issueCountCapped is true when it means "at least"). staleSince and partial appear only when the
// probe served old data or one of its three calls failed.
//
// The answer is cached for 5 minutes per project (1 minute when unavailable). The wiring code calls
// ProjectAPI.InvalidateGitHub(projectID) after a forced refresh so the next request fetches again.
//
// Plugging in the real probe: ProjectGitHubFromCache{Cache: ghCache} where ghCache is a *gh.Cache.
// ProjectGitHubFromResult is the mapping from gh.Result to the reply (it is the part to test).

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/gh"
)

const (
	pjGitHubTTL            = 5 * time.Minute
	pjGitHubUnavailableTTL = time.Minute
	pjGitHubURLPrefix      = gh.URLPrefix
)

// ProjectGHPR is one open pull request.
type ProjectGHPR struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	IsDraft   bool   `json:"isDraft"`
	Branch    string `json:"headRefName"`
	URL       string `json:"url"`
	Author    string `json:"author,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// ProjectGHIssue is one open issue.
type ProjectGHIssue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	URL       string `json:"url"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// ProjectGHRun is one CI run.
type ProjectGHRun struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"` // null while the run is not finished
	URL        string  `json:"url"`
	CreatedAt  string  `json:"createdAt,omitempty"`
}

// ProjectGitHubData is what a ProjectGitHub source returns.
type ProjectGitHubData struct {
	PRs          []ProjectGHPR
	Issues       []ProjectGHIssue // the latest open issues (up to 20)
	IssueCount   int              // all open issues
	IssuesCapped bool             // IssueCount means "at least"
	Runs         []ProjectGHRun
	FetchedAt    time.Time
	StaleSince   time.Time          // set when old data was served because a refresh failed
	Partial      []string           // parts that failed: "prs", "issues", "runs"
	Unavailable  *probe.Unavailable // set when GitHub data cannot be had; not an error
}

// ProjectGHCache is the part of *gh.Cache that this API uses.
type ProjectGHCache interface {
	Get(ctx context.Context, p probe.Project, remote, branch string, force bool) (gh.Result, error)
}

// ProjectGitHubFromCache turns a gh cache into a ProjectGitHub. It never forces a fetch: this
// route only reads. A forced refresh belongs to the refresh button (POST /api/refresh).
type ProjectGitHubFromCache struct{ Cache ProjectGHCache }

// GitHub implements ProjectGitHub.
func (g ProjectGitHubFromCache) GitHub(ctx context.Context, p probe.Project, remote, branch string) (ProjectGitHubData, error) {
	res, err := g.Cache.Get(ctx, p, remote, branch, false)
	if err != nil {
		return ProjectGitHubData{}, err
	}
	return ProjectGitHubFromResult(res), nil
}

func ghISO(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ProjectGitHubFromResult maps a gh.Result to the data of the route. Issues.Latest becomes the flat
// issues array and Issues.OpenCount becomes IssueCount. A run that is not finished has no conclusion
// (null in JSON). Times are RFC 3339 in UTC.
func ProjectGitHubFromResult(res gh.Result) ProjectGitHubData {
	d := ProjectGitHubData{
		PRs:          make([]ProjectGHPR, 0, len(res.PRs)),
		Issues:       make([]ProjectGHIssue, 0, len(res.Issues.Latest)),
		IssueCount:   res.Issues.OpenCount,
		IssuesCapped: res.Issues.Capped,
		Runs:         make([]ProjectGHRun, 0, len(res.Runs)),
		FetchedAt:    res.FetchedAt,
		StaleSince:   res.StaleSince,
		Partial:      res.Partial,
		Unavailable:  res.Unavailable,
	}
	for _, p := range res.PRs {
		d.PRs = append(d.PRs, ProjectGHPR{
			Number: p.Number, Title: p.Title, State: p.State, IsDraft: p.IsDraft, Branch: p.Branch,
			URL: p.URL, Author: p.Author, UpdatedAt: ghISO(p.UpdatedAt),
		})
	}
	for _, i := range res.Issues.Latest {
		d.Issues = append(d.Issues, ProjectGHIssue{
			Number: i.Number, Title: i.Title, State: i.State, URL: i.URL, UpdatedAt: ghISO(i.UpdatedAt),
		})
	}
	for _, r := range res.Runs {
		run := ProjectGHRun{ID: r.ID, Name: r.Name, Status: r.Status, URL: r.URL, CreatedAt: ghISO(r.CreatedAt)}
		if r.Conclusion != "" {
			c := r.Conclusion
			run.Conclusion = &c
		}
		d.Runs = append(d.Runs, run)
	}
	return d
}

// ProjectGitHub fetches GitHub data for one project. The wiring code writes a small adapter
// over probe/gh (its Cache already has a TTL; this handler adds its own 5 minute cache).
type ProjectGitHub interface {
	GitHub(ctx context.Context, p probe.Project, remote, branch string) (ProjectGitHubData, error)
}

type pjGitHubEntry struct {
	data   ProjectGitHubData
	at     time.Time
	remote string
	branch string
}

type pjGitHubReply struct {
	PRs          []ProjectGHPR      `json:"prs"`
	Issues       []ProjectGHIssue   `json:"issues"`
	IssueCount   int                `json:"issueCount"`
	IssuesCapped bool               `json:"issueCountCapped,omitempty"`
	Runs         []ProjectGHRun     `json:"runs"`
	FetchedAt    string             `json:"fetchedAt"`
	StaleSince   string             `json:"staleSince,omitempty"`
	Partial      []string           `json:"partial,omitempty"`
	Unavailable  *probe.Unavailable `json:"unavailable,omitempty"`
}

func (a *ProjectAPI) registerGitHub(s *Server) {
	s.Handle(http.MethodGet, "/api/projects/{p}/github", a.handleGitHub, false)
}

// InvalidateGitHub forgets the cached GitHub answer of a project ("" forgets all).
func (a *ProjectAPI) InvalidateGitHub(projectID string) {
	a.ghMu.Lock()
	defer a.ghMu.Unlock()
	if projectID == "" {
		a.ghCache = map[string]pjGitHubEntry{}
		return
	}
	delete(a.ghCache, projectID)
}

func (a *ProjectAPI) handleGitHub(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.lookup(w, r)
	if !ok {
		return
	}
	data := a.github(r.Context(), pr)
	reply := pjGitHubReply{
		PRs: data.PRs, Issues: data.Issues, IssueCount: data.IssueCount, IssuesCapped: data.IssuesCapped, Runs: data.Runs,
		FetchedAt: a.iso(data.FetchedAt), StaleSince: a.iso(data.StaleSince), Partial: data.Partial, Unavailable: data.Unavailable,
	}
	if reply.PRs == nil {
		reply.PRs = []ProjectGHPR{}
	}
	if reply.Issues == nil {
		reply.Issues = []ProjectGHIssue{}
	}
	if reply.Runs == nil {
		reply.Runs = []ProjectGHRun{}
	}
	if reply.FetchedAt == "" {
		reply.FetchedAt = a.iso(a.now())
	}
	WriteJSON(w, http.StatusOK, reply)
}

func pjUnavailable(now time.Time, reason string) ProjectGitHubData {
	return ProjectGitHubData{FetchedAt: now, Unavailable: &probe.Unavailable{Reason: reason}}
}

// github returns cached data when it is fresh, else asks the source. It never fails: a problem
// becomes an "unavailable" answer.
func (a *ProjectAPI) github(ctx context.Context, pr pjProject) ProjectGitHubData {
	now := a.now()
	remote, branch := pr.Row.Remote, pr.Row.Branch
	if remote == "" {
		return pjUnavailable(now, gh.ReasonNoRemote)
	}
	if a.d.GitHub == nil {
		return pjUnavailable(now, "GitHub is not set up")
	}

	a.ghMu.Lock()
	e, have := a.ghCache[pr.Row.ID]
	a.ghMu.Unlock()
	if have && e.remote == remote && e.branch == branch {
		ttl := pjGitHubTTL
		if e.data.Unavailable != nil {
			ttl = pjGitHubUnavailableTTL
		}
		if now.Sub(e.at) < ttl {
			return e.data
		}
	}

	data, err := a.d.GitHub.GitHub(ctx, pr.Probe, remote, branch)
	if err != nil {
		a.log.Warn("github", "project", pr.Row.ID, "err", err)
		if have && e.remote == remote {
			return e.data // an old answer beats none
		}
		return pjUnavailable(now, "GitHub could not be reached")
	}
	if data.FetchedAt.IsZero() {
		data.FetchedAt = now
	}
	pjCleanGitHubURLs(&data)
	a.ghMu.Lock()
	a.ghCache[pr.Row.ID] = pjGitHubEntry{data: data, at: now, remote: remote, branch: branch}
	a.ghMu.Unlock()
	return data
}

// pjCleanGitHubURLs blanks any link that does not start with https://github.com/, so the page can
// never be handed another kind of link.
func pjCleanGitHubURLs(d *ProjectGitHubData) {
	ok := func(u string) string {
		if strings.HasPrefix(u, pjGitHubURLPrefix) {
			return u
		}
		return ""
	}
	for i := range d.PRs {
		d.PRs[i].URL = ok(d.PRs[i].URL)
	}
	for i := range d.Issues {
		d.Issues[i].URL = ok(d.Issues[i].URL)
	}
	for i := range d.Runs {
		d.Runs[i].URL = ok(d.Runs[i].URL)
	}
}
