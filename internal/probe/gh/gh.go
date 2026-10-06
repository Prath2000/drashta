package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/runx"
)

const (
	// Host is the only host drashta talks to.
	Host = "github.com"
	// URLPrefix is the start every URL in a Result must have.
	URLPrefix = "https://github.com/"
	// MaxLatestIssues is the most issues kept in IssueSummary.Latest.
	MaxLatestIssues = 20

	prLimit    = 30
	issueLimit = 100 // OpenCount is exact up to this many open issues
	maxTitle   = 300 // runes

	// Reasons used in Unavailable.
	ReasonNoGH        = "gh is not installed"
	ReasonNotLoggedIn = "gh not logged in"
	ReasonNotGitHub   = "remote is not on github.com"
	ReasonNoRemote    = "no git remote"
	ReasonNoAccess    = "repo not found on GitHub, or no access"
	ReasonDisabled    = "GitHub is turned off in the config"
)

// PR is one open pull request.
type PR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"` // open | closed | merged
	IsDraft   bool      `json:"isDraft"`
	Branch    string    `json:"branch"` // head branch
	Author    string    `json:"author,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
	URL       string    `json:"url"`
}

// Issue is one open issue.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
	URL       string    `json:"url"`
}

// IssueSummary is the open issue count plus the most recently updated issues.
type IssueSummary struct {
	OpenCount int     `json:"openCount"`
	Capped    bool    `json:"capped,omitempty"` // OpenCount is "at least": gh was asked for 100 and gave 100
	Latest    []Issue `json:"latest"`           // newest update first, at most MaxLatestIssues
}

// Run is the latest CI run of the branch.
type Run struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`     // queued | in_progress | completed ...
	Conclusion string    `json:"conclusion"` // success | failure ... ; "" while running
	URL        string    `json:"url"`
	CreatedAt  time.Time `json:"createdAt,omitzero"`
	UpdatedAt  time.Time `json:"updatedAt,omitzero"`
}

// Result is what Fetch learned. When Unavailable is set every other list is empty.
type Result struct {
	Repo        string             `json:"repo,omitempty"`   // "owner/name"
	Branch      string             `json:"branch,omitempty"` // the branch the CI run is for
	PRs         []PR               `json:"prs"`
	Issues      IssueSummary       `json:"issues"`
	Runs        []Run              `json:"runs"`
	FetchedAt   time.Time          `json:"fetchedAt"`
	StaleSince  time.Time          `json:"staleSince,omitzero"` // set by Cache when a refresh failed and old data is served
	Partial     []string           `json:"partial,omitempty"`   // parts that failed: "prs", "issues", "runs"
	Unavailable *probe.Unavailable `json:"unavailable,omitempty"`
}

// Client runs the gh command. The zero value is ready to use.
type Client struct {
	Bin string           // program to run; "" means "gh". A path with a slash is used as it is.
	Now func() time.Time // clock; nil means time.Now
}

var defaultClient = &Client{}

func (c *Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "gh"
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// childEnv is every variable this package adds to gh's environment. It never
// holds a token: gh reads its own login (keychain or hosts file). Tests check
// that no key here mentions TOKEN.
func childEnv() []string {
	return []string{
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"GH_SPINNER_DISABLED=1",
		"NO_COLOR=1",
	}
}

func (c *Client) run(ctx context.Context, args ...string) (runx.Result, error) {
	return runx.Run(ctx, runx.Options{Env: childEnv()}, c.bin(), args...)
}

// Available reports whether the gh program can be found.
func (c *Client) Available() bool { return runx.Available(c.bin()) }

type loginState int

const (
	loginOK loginState = iota
	loginNoGH
	loginNo
)

// login runs `gh auth status -h github.com` and reads only the exit code.
// An error means gh could not answer (timeout, cancelled): the login state is unknown.
func (c *Client) login(ctx context.Context) (loginState, error) {
	res, err := c.run(ctx, "auth", "status", "--hostname", Host)
	if errors.Is(err, runx.ErrNotFound) {
		return loginNoGH, nil
	}
	if err != nil {
		return loginNo, err
	}
	if res.ExitCode != 0 {
		return loginNo, nil
	}
	return loginOK, nil
}

// LoggedIn reports whether gh is installed and logged in to github.com. The
// string says why not ("" when true). Only the exit code of `gh auth status`
// is used: its text is never read or kept.
func (c *Client) LoggedIn(ctx context.Context) (bool, string) {
	st, err := c.login(ctx)
	switch {
	case err != nil:
		return false, "could not check gh login: " + shortErr(err)
	case st == loginNoGH:
		return false, ReasonNoGH
	case st == loginNo:
		return false, ReasonNotLoggedIn
	}
	return true, ""
}

// Available reports whether gh is on PATH.
func Available() bool { return defaultClient.Available() }

// LoggedIn is Client.LoggedIn for the default client.
func LoggedIn(ctx context.Context) (bool, string) { return defaultClient.LoggedIn(ctx) }

// Fetch is Client.Fetch for the default client.
func Fetch(ctx context.Context, p probe.Project, remote, branch string) (Result, error) {
	return defaultClient.Fetch(ctx, p, remote, branch)
}

func emptyResult(now time.Time) Result {
	return Result{
		PRs: []PR{}, Runs: []Run{}, FetchedAt: now,
		Issues: IssueSummary{Latest: []Issue{}},
	}
}

func unavailable(now time.Time, reason string) Result {
	r := emptyResult(now)
	r.Unavailable = &probe.Unavailable{Reason: reason}
	return r
}

// part is the outcome of one gh call.
type part struct {
	res runx.Result
	err error
}

// Fetch asks GitHub (through gh) for the open PRs, the open issues and the
// latest CI run of branch. remote is the project's "origin" URL.
//
// Not a GitHub remote, gh missing and gh not logged in all return a Result with
// Unavailable set and a nil error. An error means gh could not answer in time or
// failed in a way that says nothing about login (the caller keeps old data).
// If only some of the three calls fail, the rest is returned and Partial names
// the failed parts.
func (c *Client) Fetch(ctx context.Context, p probe.Project, remote, branch string) (Result, error) {
	now := c.now()
	if strings.TrimSpace(remote) == "" {
		return unavailable(now, ReasonNoRemote), nil
	}
	repo, ok := ParseRemote(remote)
	if !ok {
		return unavailable(now, ReasonNotGitHub), nil
	}
	if !c.Available() {
		return unavailable(now, ReasonNoGH), nil
	}
	spec := repo.Spec()
	wantRun := okBranch(branch)

	// Four calls in parallel: login, PRs, issues, runs. Parallel keeps the whole
	// fetch near the time of one call.
	var wg sync.WaitGroup
	var auth, prs, iss, runs part
	var authState loginState
	call := func(dst *part, args ...string) {
		defer wg.Done()
		dst.res, dst.err = c.run(ctx, args...)
	}
	wg.Add(2)
	go call(&prs, "pr", "list", "-R", spec, "-L", fmt.Sprint(prLimit),
		"--json", "number,title,state,isDraft,headRefName,author,updatedAt,url")
	go call(&iss, "issue", "list", "-R", spec, "-L", fmt.Sprint(issueLimit),
		"--json", "number,title,state,updatedAt,url")
	if wantRun {
		wg.Add(1)
		go call(&runs, "run", "list", "-R", spec, "-b", branch, "-L", "1",
			"--json", "databaseId,name,status,conclusion,url,createdAt,updatedAt")
	}
	authState, auth.err = c.login(ctx)
	wg.Wait()

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if auth.err != nil {
		return Result{}, fmt.Errorf("gh auth status: %s", shortErr(auth.err))
	}
	switch authState {
	case loginNoGH:
		return unavailable(now, ReasonNoGH), nil
	case loginNo:
		return unavailable(now, ReasonNotLoggedIn), nil
	}

	out := emptyResult(now)
	out.Repo = repo.String()
	if wantRun {
		out.Branch = branch
	}

	var firstErr error
	fail := func(name string, err error) {
		out.Partial = append(out.Partial, name)
		if firstErr == nil {
			firstErr = fmt.Errorf("gh %s: %w", name, err)
		}
	}
	var denied string // set when a call says "not logged in" or "no access"

	check := func(name string, pt part) (ok bool) {
		if pt.err != nil {
			fail(name, pt.err)
			return false
		}
		if pt.res.ExitCode != 0 {
			switch classify(string(pt.res.Stderr)) {
			case errAuth:
				denied = ReasonNotLoggedIn
			case errNotFound:
				denied = ReasonNoAccess
			case errIssuesOff:
				if name == "issues" {
					return false // not a failure: this repo has no issues
				}
			}
			fail(name, fmt.Errorf("exit %d: %s", pt.res.ExitCode, firstLine(redact(string(pt.res.Stderr)))))
			return false
		}
		return true
	}

	if check("prs", prs) {
		list, err := parsePRs(prs.res.Stdout)
		if err != nil {
			fail("prs", err)
		} else {
			out.PRs = list
		}
	}
	if check("issues", iss) {
		sum, err := parseIssues(iss.res.Stdout)
		if err != nil {
			fail("issues", err)
		} else {
			out.Issues = sum
		}
	}
	if wantRun && check("runs", runs) {
		list, err := parseRuns(runs.res.Stdout)
		if err != nil {
			fail("runs", err)
		} else {
			out.Runs = list
		}
	}

	if denied != "" {
		return unavailable(now, denied), nil
	}
	total := 2
	if wantRun {
		total = 3
	}
	if len(out.Partial) >= total {
		// Every call failed. Say so; the caller keeps older data.
		return Result{}, firstErr
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// parsing

type rawAuthor struct {
	Login string `json:"login"`
}

type rawPR struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	IsDraft     bool      `json:"isDraft"`
	HeadRefName string    `json:"headRefName"`
	Author      rawAuthor `json:"author"`
	UpdatedAt   string    `json:"updatedAt"`
	URL         string    `json:"url"`
}

func parsePRs(b []byte) ([]PR, error) {
	var raw []rawPR
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("bad pr list JSON: %w", err)
	}
	out := make([]PR, 0, len(raw))
	for _, r := range raw {
		if r.Number <= 0 || !goodURL(r.URL) {
			continue
		}
		out = append(out, PR{
			Number: r.Number, Title: cleanText(r.Title), State: strings.ToLower(r.State),
			IsDraft: r.IsDraft, Branch: cleanText(r.HeadRefName), Author: cleanText(r.Author.Login),
			UpdatedAt: parseTime(r.UpdatedAt), URL: r.URL,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].Number > out[j].Number
	})
	return out, nil
}

type rawIssue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	UpdatedAt string `json:"updatedAt"`
	URL       string `json:"url"`
}

func parseIssues(b []byte) (IssueSummary, error) {
	var raw []rawIssue
	if err := json.Unmarshal(b, &raw); err != nil {
		return IssueSummary{}, fmt.Errorf("bad issue list JSON: %w", err)
	}
	all := make([]Issue, 0, len(raw))
	for _, r := range raw {
		if r.Number <= 0 || !goodURL(r.URL) {
			continue
		}
		all = append(all, Issue{
			Number: r.Number, Title: cleanText(r.Title), State: strings.ToLower(r.State),
			UpdatedAt: parseTime(r.UpdatedAt), URL: r.URL,
		})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].UpdatedAt.Equal(all[j].UpdatedAt) {
			return all[i].UpdatedAt.After(all[j].UpdatedAt)
		}
		return all[i].Number > all[j].Number
	})
	sum := IssueSummary{OpenCount: len(all), Capped: len(raw) >= issueLimit, Latest: all}
	if len(sum.Latest) > MaxLatestIssues {
		sum.Latest = sum.Latest[:MaxLatestIssues]
	}
	return sum, nil
}

type rawRun struct {
	DatabaseID int64  `json:"databaseId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

func parseRuns(b []byte) ([]Run, error) {
	var raw []rawRun
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("bad run list JSON: %w", err)
	}
	out := make([]Run, 0, len(raw))
	for _, r := range raw {
		if !goodURL(r.URL) {
			continue
		}
		out = append(out, Run{
			ID: r.DatabaseID, Name: cleanText(r.Name), Status: strings.ToLower(r.Status),
			Conclusion: strings.ToLower(r.Conclusion), URL: r.URL,
			CreatedAt: parseTime(r.CreatedAt), UpdatedAt: parseTime(r.UpdatedAt),
		})
	}
	return out, nil
}

// goodURL keeps only links on github.com over https. The viewport opens them
// with `open -a Safari`, so nothing else may get through.
func goodURL(u string) bool {
	if !strings.HasPrefix(u, URLPrefix) || len(u) > 2048 {
		return false
	}
	return strings.IndexFunc(u, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// cleanText turns control characters into spaces and cuts long text.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxTitle {
		s = string(r[:maxTitle-1]) + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// errors

type errKind int

const (
	errOther errKind = iota
	errAuth
	errNotFound
	errIssuesOff
)

// classify reads gh's error text (stderr of a list call, never of auth status).
func classify(stderr string) errKind {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "gh auth login"), strings.Contains(s, "http 401"),
		strings.Contains(s, "bad credentials"), strings.Contains(s, "authentication required"):
		return errAuth
	case strings.Contains(s, "disabled issues"):
		return errIssuesOff
	case strings.Contains(s, "could not resolve to a repository"), strings.Contains(s, "http 404"):
		return errNotFound
	}
	return errOther
}

var tokenRE = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}`)

// redact removes anything that looks like a GitHub token from text we may log.
func redact(s string) string { return tokenRE.ReplaceAllString(s, "[redacted]") }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func shortErr(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return firstLine(redact(err.Error()))
}
