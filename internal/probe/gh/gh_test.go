package gh

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

var testProject = probe.Project{ID: "widgets", Name: "widgets", Group: "personal", Path: "/nonexistent/widgets"}

const goodRemote = "git@github.com:acme/widgets.git"

func TestParseRemote(t *testing.T) {
	ok := map[string]string{
		"git@github.com:acme/widgets.git":           "acme/widgets",
		"git@github.com:acme/widgets":               "acme/widgets",
		"https://github.com/acme/widgets":           "acme/widgets",
		"https://github.com/acme/widgets.git":       "acme/widgets",
		"https://github.com/acme/widgets/":          "acme/widgets",
		"http://github.com/acme/widgets.git":        "acme/widgets",
		"https://user@github.com/acme/widgets.git":  "acme/widgets",
		"https://github.com:443/acme/widgets.git":   "acme/widgets",
		"ssh://git@github.com/acme/widgets.git":     "acme/widgets",
		"ssh://git@github.com:22/acme/widgets.git":  "acme/widgets",
		"git://github.com/acme/widgets.git":         "acme/widgets",
		"HTTPS://GitHub.com/Acme/Wid-gets_1.x":      "Acme/Wid-gets_1.x",
		"github.com:acme/widgets.git":               "acme/widgets",
		"  https://github.com/acme/widgets.git  ":   "acme/widgets",
		"https://www.github.com/acme/widgets":       "acme/widgets",
		"https://github.com/acme/my.repo.js.git":    "acme/my.repo.js",
		"git@github.com:a/b.git":                    "a/b",
		"https://github.com/acme/widgets.github.io": "acme/widgets.github.io",
	}
	for in, want := range ok {
		r, got := ParseRemote(in)
		if !got || r.String() != want {
			t.Errorf("ParseRemote(%q) = %q, %v; want %q", in, r.String(), got, want)
		}
	}
	bad := []string{
		"", "   ",
		"git@gitlab.com:acme/widgets.git",
		"https://gitlab.com/acme/widgets",
		"https://github.com.evil.example/acme/widgets",
		"https://evil.example/github.com/acme/widgets",
		"https://github.com@evil.example/acme/widgets",
		"git@github.com.evil.example:acme/widgets.git",
		"https://github.com/acme",
		"https://github.com/acme/widgets/tree/main",
		"https://github.com//widgets",
		"https://github.com/acme/",
		"https://github.com/-acme/widgets",
		"https://github.com/acme/-widgets",
		"https://github.com/../widgets",
		"https://github.com/acme/..",
		"https://github.com/ac me/widgets",
		"https://github.com/acme/wid\tgets",
		"file:///Users/x/github.com/acme/widgets",
		"ftp://github.com/acme/widgets",
		"/Users/fx/projects/widgets",
		"../widgets",
		"git@github.com:acme/widgets;rm -rf",
		"https://github.com/acme/widgets$(x)",
		"https://github.com/acme/wid`gets",
		"https://github.com/acme/wid\x00gets",
		"git@github.com:",
		"git@github.com:/",
	}
	for _, in := range bad {
		if r, got := ParseRemote(in); got {
			t.Errorf("ParseRemote(%q) = %q, true; want false", in, r)
		}
	}
	if r, _ := ParseRemote(goodRemote); r.Spec() != "github.com/acme/widgets" {
		t.Errorf("Spec = %q", r.Spec())
	}
}

func TestFetchParsesEverything(t *testing.T) {
	c, dir := fakeGH(t, map[string]string{"issue.json": issuesJSON(25)})
	c.Now = func() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC) }
	res, err := c.Fetch(context.Background(), testProject, goodRemote, "main")
	if err != nil {
		t.Fatal(err)
	}
	if res.Unavailable != nil {
		t.Fatalf("unavailable: %+v", res.Unavailable)
	}
	if res.Repo != "acme/widgets" || res.Branch != "main" {
		t.Errorf("repo/branch = %q/%q", res.Repo, res.Branch)
	}
	if !res.FetchedAt.Equal(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("FetchedAt = %v", res.FetchedAt)
	}

	// Only the two PRs with a https://github.com/ URL stay. Newest update first.
	if len(res.PRs) != 2 {
		t.Fatalf("PRs = %+v", res.PRs)
	}
	if res.PRs[0].Number != 9 || res.PRs[1].Number != 7 {
		t.Errorf("PR order = %d, %d", res.PRs[0].Number, res.PRs[1].Number)
	}
	pr := res.PRs[0]
	if !pr.IsDraft || pr.State != "open" || pr.Branch != "refactor" || pr.Author != "bob" {
		t.Errorf("PR 9 = %+v", pr)
	}
	if pr.Title != "WIP refactor of the widget loader" {
		t.Errorf("newline in title not cleaned: %q", pr.Title)
	}
	if pr.UpdatedAt.IsZero() {
		t.Error("UpdatedAt not parsed")
	}

	// 25 issues: count is 25, latest is 20, newest update first.
	if res.Issues.OpenCount != 25 || res.Issues.Capped {
		t.Errorf("issues = %d capped=%v", res.Issues.OpenCount, res.Issues.Capped)
	}
	if len(res.Issues.Latest) != MaxLatestIssues || res.Issues.Latest[0].Number != 25 || res.Issues.Latest[19].Number != 6 {
		t.Errorf("latest = %+v", res.Issues.Latest)
	}

	if len(res.Runs) != 1 {
		t.Fatalf("runs = %+v", res.Runs)
	}
	r := res.Runs[0]
	if r.ID != 4242 || r.Name != "CI" || r.Status != "completed" || r.Conclusion != "success" {
		t.Errorf("run = %+v", r)
	}

	// The exact command lines.
	got := strings.Join(calls(t, dir), "\n")
	for _, want := range []string{
		"auth status --hostname github.com",
		"pr list -R github.com/acme/widgets -L 30 --json number,title,state,isDraft,headRefName,author,updatedAt,url",
		"issue list -R github.com/acme/widgets -L 100 --json number,title,state,updatedAt,url",
		"run list -R github.com/acme/widgets -b main -L 1 --json ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("calls missing %q\n%s", want, got)
		}
	}
}

func TestFetchIssueCountCap(t *testing.T) {
	c, _ := fakeGH(t, map[string]string{"issue.json": issuesJSON(100)})
	res, err := c.Fetch(context.Background(), testProject, goodRemote, "main")
	if err != nil {
		t.Fatal(err)
	}
	if res.Issues.OpenCount != 100 || !res.Issues.Capped || len(res.Issues.Latest) != MaxLatestIssues {
		t.Errorf("issues = %+v", res.Issues)
	}
}

func TestFetchBothRemoteForms(t *testing.T) {
	for _, remote := range []string{"git@github.com:acme/widgets.git", "https://github.com/acme/widgets"} {
		c, dir := fakeGH(t, nil)
		res, err := c.Fetch(context.Background(), testProject, remote, "main")
		if err != nil || res.Unavailable != nil || res.Repo != "acme/widgets" {
			t.Errorf("%s: res=%+v err=%v", remote, res, err)
		}
		if countCalls(t, dir, "pr list -R github.com/acme/widgets ") != 1 {
			t.Errorf("%s: calls = %v", remote, calls(t, dir))
		}
	}
}

func TestFetchUnavailable(t *testing.T) {
	ctx := context.Background()

	t.Run("not logged in", func(t *testing.T) {
		c, _ := fakeGH(t, map[string]string{"auth_fail": "You are not logged into any GitHub hosts.\n"})
		res, err := c.Fetch(ctx, testProject, goodRemote, "main")
		if err != nil {
			t.Fatalf("not logged in must not be an error: %v", err)
		}
		if res.Unavailable == nil || res.Unavailable.Reason != ReasonNotLoggedIn {
			t.Fatalf("unavailable = %+v", res.Unavailable)
		}
		if len(res.PRs) != 0 || res.PRs == nil {
			t.Errorf("PRs should be an empty, non-nil list: %#v", res.PRs)
		}
		if ok, why := c.LoggedIn(ctx); ok || why != ReasonNotLoggedIn {
			t.Errorf("LoggedIn = %v, %q", ok, why)
		}
	})

	t.Run("missing binary", func(t *testing.T) {
		c := &Client{Bin: filepath.Join(t.TempDir(), "no-such-gh")}
		if c.Available() {
			t.Error("Available should be false")
		}
		res, err := c.Fetch(ctx, testProject, goodRemote, "main")
		if err != nil || res.Unavailable == nil || res.Unavailable.Reason != ReasonNoGH {
			t.Errorf("res=%+v err=%v", res.Unavailable, err)
		}
		if ok, why := c.LoggedIn(ctx); ok || why != ReasonNoGH {
			t.Errorf("LoggedIn = %v, %q", ok, why)
		}
	})

	t.Run("not a github remote", func(t *testing.T) {
		c, dir := fakeGH(t, nil)
		for _, remote := range []string{"git@gitlab.com:acme/widgets.git", "https://example.com/acme/widgets", "/some/local/path"} {
			res, err := c.Fetch(ctx, testProject, remote, "main")
			if err != nil || res.Unavailable == nil || res.Unavailable.Reason != ReasonNotGitHub {
				t.Errorf("%s: unavailable=%+v err=%v", remote, res.Unavailable, err)
			}
		}
		res, err := c.Fetch(ctx, testProject, "", "main")
		if err != nil || res.Unavailable == nil || res.Unavailable.Reason != ReasonNoRemote {
			t.Errorf("empty remote: %+v %v", res.Unavailable, err)
		}
		if n := len(calls(t, dir)); n != 0 {
			t.Errorf("gh must not run for a non-GitHub remote; calls = %v", calls(t, dir))
		}
	})

	t.Run("login ok", func(t *testing.T) {
		c, _ := fakeGH(t, nil)
		if ok, why := c.LoggedIn(ctx); !ok || why != "" {
			t.Errorf("LoggedIn = %v, %q", ok, why)
		}
		if !c.Available() {
			t.Error("Available should be true")
		}
	})

	t.Run("token expired during the calls", func(t *testing.T) {
		c, _ := fakeGH(t, map[string]string{
			"pr_fail": "To get started with GitHub CLI, please run:  gh auth login\n",
		})
		res, err := c.Fetch(ctx, testProject, goodRemote, "main")
		if err != nil || res.Unavailable == nil || res.Unavailable.Reason != ReasonNotLoggedIn {
			t.Errorf("res=%+v err=%v", res.Unavailable, err)
		}
	})

	t.Run("repo not found", func(t *testing.T) {
		c, _ := fakeGH(t, map[string]string{
			"pr_fail":    "GraphQL: Could not resolve to a Repository with the name 'acme/widgets'. (repository)\n",
			"issue_fail": "GraphQL: Could not resolve to a Repository with the name 'acme/widgets'. (repository)\n",
			"run_fail":   "HTTP 404: Not Found (https://api.github.com/repos/acme/widgets/actions/runs)\n",
		})
		res, err := c.Fetch(ctx, testProject, goodRemote, "main")
		if err != nil || res.Unavailable == nil || res.Unavailable.Reason != ReasonNoAccess {
			t.Errorf("res=%+v err=%v", res.Unavailable, err)
		}
	})
}

func TestFetchPartialAndTotalFailure(t *testing.T) {
	ctx := context.Background()

	t.Run("issues disabled is not a failure", func(t *testing.T) {
		c, _ := fakeGH(t, map[string]string{"issue_fail": "the 'acme/widgets' repository has disabled issues\n"})
		res, err := c.Fetch(ctx, testProject, goodRemote, "main")
		if err != nil || res.Unavailable != nil {
			t.Fatalf("res=%+v err=%v", res.Unavailable, err)
		}
		if len(res.Partial) != 0 || res.Issues.OpenCount != 0 || len(res.PRs) != 2 {
			t.Errorf("partial=%v issues=%+v prs=%d", res.Partial, res.Issues, len(res.PRs))
		}
	})

	t.Run("one part fails", func(t *testing.T) {
		c, _ := fakeGH(t, map[string]string{"run_fail": "something odd happened\n"})
		res, err := c.Fetch(ctx, testProject, goodRemote, "main")
		if err != nil || res.Unavailable != nil {
			t.Fatalf("res=%+v err=%v", res.Unavailable, err)
		}
		if len(res.Partial) != 1 || res.Partial[0] != "runs" || len(res.PRs) != 2 || len(res.Runs) != 0 {
			t.Errorf("partial=%v prs=%d runs=%d", res.Partial, len(res.PRs), len(res.Runs))
		}
	})

	t.Run("all parts fail", func(t *testing.T) {
		c, _ := fakeGH(t, map[string]string{
			"pr_fail": "boom ghp_abcdefghijklmnopqrstuvwxyz0123456789\n", "issue_fail": "boom\n", "run_fail": "boom\n",
		})
		_, err := c.Fetch(ctx, testProject, goodRemote, "main")
		if err == nil {
			t.Fatal("want an error when every call fails")
		}
		if strings.Contains(err.Error(), "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
			t.Errorf("token-like text leaked into the error: %v", err)
		}
	})

	t.Run("bad JSON", func(t *testing.T) {
		c, _ := fakeGH(t, map[string]string{"pr.json": "not json at all"})
		res, err := c.Fetch(ctx, testProject, goodRemote, "main")
		if err != nil || len(res.Partial) != 1 || res.Partial[0] != "prs" {
			t.Errorf("res.Partial=%v err=%v", res.Partial, err)
		}
	})
}

func TestFetchNoRunForDetachedOrOddBranch(t *testing.T) {
	for _, branch := range []string{"", "HEAD", "-evil", "a\nb"} {
		c, dir := fakeGH(t, nil)
		res, err := c.Fetch(context.Background(), testProject, goodRemote, branch)
		if err != nil || res.Unavailable != nil {
			t.Fatalf("branch %q: %+v %v", branch, res.Unavailable, err)
		}
		if n := countCalls(t, dir, "run list"); n != 0 {
			t.Errorf("branch %q: run list was called", branch)
		}
		if res.Branch != "" || len(res.Runs) != 0 {
			t.Errorf("branch %q: res.Branch=%q runs=%d", branch, res.Branch, len(res.Runs))
		}
	}
}

func TestFetchCancelled(t *testing.T) {
	c, _ := fakeGH(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Fetch(ctx, testProject, goodRemote, "main"); err == nil {
		t.Error("want an error for a cancelled context")
	}
}

func TestJSONShape(t *testing.T) {
	c, _ := fakeGH(t, map[string]string{"auth_fail": "no\n"})
	res, _ := c.Fetch(context.Background(), testProject, goodRemote, "main")
	b, _ := json.Marshal(res)
	s := string(b)
	for _, want := range []string{`"prs":[]`, `"runs":[]`, `"latest":[]`, `"openCount":0`, `"unavailable":{"reason":"gh not logged in"}`, `"fetchedAt"`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, "staleSince") || strings.Contains(s, "partial") {
		t.Errorf("empty fields should be left out: %s", s)
	}
}

// The package-level functions use the real PATH. Point PATH at the fake.
func TestPackageFunctionsUsePATH(t *testing.T) {
	c, dir := fakeGH(t, nil)
	_ = c
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if !Available() {
		t.Fatal("Available() false with a gh on PATH")
	}
	if ok, why := LoggedIn(context.Background()); !ok {
		t.Fatalf("LoggedIn = false, %q", why)
	}
	res, err := Fetch(context.Background(), testProject, goodRemote, "main")
	if err != nil || res.Unavailable != nil || len(res.PRs) != 2 {
		t.Errorf("res=%+v err=%v", res.Unavailable, err)
	}
	t.Setenv("PATH", t.TempDir()) // no gh anywhere
	if Available() {
		t.Error("Available() true with an empty PATH")
	}
	if ok, why := LoggedIn(context.Background()); ok || why != ReasonNoGH {
		t.Errorf("LoggedIn = %v, %q", ok, why)
	}
}

// Tokens: the package must never set, read or forward one.
func TestNeverSetsAToken(t *testing.T) {
	tokenVars := []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}

	// 1. What the package adds to gh's environment holds no token.
	for _, kv := range childEnv() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.Contains(strings.ToUpper(key), "TOKEN") {
			t.Errorf("childEnv sets %s", key)
		}
	}

	// 2. Run everything with a clean environment and look at what gh really received.
	for _, k := range tokenVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	c, dir := fakeGH(t, nil)
	ctx := context.Background()
	c.LoggedIn(ctx)
	if _, err := c.Fetch(ctx, testProject, goodRemote, "main"); err != nil {
		t.Fatal(err)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "env.*.log"))
	if len(logs) < 4 {
		t.Fatalf("expected an env dump per gh call, got %d", len(logs))
	}
	parent := map[string]bool{}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		parent[key] = true
	}
	for _, f := range logs {
		b, _ := os.ReadFile(f)
		for _, line := range strings.Split(string(b), "\n") {
			key, _, _ := strings.Cut(line, "=")
			for _, tv := range tokenVars {
				if key == tv {
					t.Errorf("gh received %s", key)
				}
			}
			// Any other token-like name may only come from the test machine itself.
			if strings.Contains(strings.ToUpper(key), "TOKEN") && !parent[key] {
				t.Errorf("gh received %s, which the package added", key)
			}
		}
	}

	// 3. The source has no token code: no Setenv, no token command, no token variable name.
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		for _, bad := range []string{"Setenv(", `"token"`, "--show-token", `"GH_TOKEN`, `"GITHUB_TOKEN`, "Getenv("} {
			if strings.Contains(src, bad) {
				t.Errorf("%s contains %q", f, bad)
			}
		}
	}
}
