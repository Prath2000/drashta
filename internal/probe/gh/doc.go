// Package gh is the GitHub probe. It runs the gh command line (never a shell)
// through runx and keeps the answer in a small cache. The scope is small on
// purpose (03-architecture.md section 3, "gh"; decision A6): open pull requests,
// open issues (count + latest 20) and the latest CI run of the current branch.
//
// Token rule (03 section 8): gh reads its own login. This package never reads,
// stores, logs or forwards a token, never runs the gh token command, and never
// sets a token variable. Only the exit code of `gh auth status` is used.
// The only variables it adds to gh's environment are in childEnv (prompt, update
// notice, spinner and colour switches). A test checks all of this.
//
// Exported API
//
//	const Host = "github.com"
//	const URLPrefix = "https://github.com/"   every URL in a Result starts with this
//	const MaxLatestIssues = 20
//	const ReasonNoGH, ReasonNotLoggedIn, ReasonNotGitHub, ReasonNoRemote,
//	      ReasonNoAccess, ReasonDisabled      the texts used in Unavailable.Reason
//
//	type Repo struct{ Owner, Name string }
//	    func (Repo) String() string           "owner/name"
//	    func (Repo) Spec() string             "github.com/owner/name" (what we pass to gh -R)
//	func ParseRemote(remote string) (Repo, bool)
//	    git@github.com:o/r.git, https://github.com/o/r(.git), ssh://, git:// forms.
//	    Anything not on github.com, or with odd characters, gives false.
//
//	type PR struct       {Number, Title, State ("open"|"closed"|"merged"), IsDraft, Branch, Author, UpdatedAt, URL}
//	type Issue struct    {Number, Title, State, UpdatedAt, URL}
//	type IssueSummary    {OpenCount int; Capped bool; Latest []Issue}   Latest: newest update first, at most 20.
//	                     OpenCount is exact up to 99; Capped=true means "100 or more".
//	type Run struct      {ID, Name, Status, Conclusion, URL, CreatedAt, UpdatedAt}
//	type Result struct   {Repo, Branch, PRs, Issues, Runs, FetchedAt, StaleSince, Partial, Unavailable}
//	    The lists are never nil (JSON gives [] not null). When Unavailable is set,
//	    every list is empty. Partial names the parts that failed ("prs", "issues",
//	    "runs"). StaleSince is set by Cache only (see Get). Time fields use
//	    "omitzero", so a missing time is left out of the JSON.
//
//	type Client struct { Bin string; Now func() time.Time }   zero value is ready
//	    func (*Client) Available() bool
//	    func (*Client) LoggedIn(ctx) (bool, string)           the string is the reason when false
//	    func (*Client) Fetch(ctx, p probe.Project, remote, branch string) (Result, error)
//	func Available() bool
//	func LoggedIn(ctx context.Context) (bool, string)
//	func Fetch(ctx, p probe.Project, remote, branch string) (Result, error)
//	    The package-level functions use a Client that runs "gh" from PATH.
//
//	type Store interface {                                    a persistent copy for Cache
//	    Get(project, kind string) (data []byte, at time.Time, ok bool)
//	    Put(project, kind string, data []byte, at time.Time)
//	}
//	const StoreKind = "result"                                the only kind Cache uses
//	type Cache struct { Fetch, Store, Now, TTL, BackgroundEvery, UnavailableTTL,
//	                    FailBackoff, MinForceGap, Disabled, OnError }
//	func NewCache(store Store) *Cache                         uses the default Fetch
//	func (*Cache) Get(ctx, p, remote, branch string, force bool) (Result, error)
//	    A partial result keeps the previous data (same repo and branch) for the
//	    failed parts; Partial still names them. A fetch in flight is shared only
//	    by callers asking for the same branch.
//	func (*Cache) Peek(projectID string) (Result, bool)       memory then Store, no network
//	func (*Cache) Due(projectID string) bool                  true if never fetched or 15 min old
//	func (*Cache) Invalidate(projectID string)                next Get refetches (call it after a push)
//	func (*Cache) Forget(projectID string)
//
// Behaviour
//
//   - Fetch: a missing remote, a non-GitHub remote, gh missing, gh not logged in,
//     a token that no longer works, or a repo that is not found: all give a Result
//     with Unavailable and a nil error. A timeout, or all three calls failing,
//     gives an error. If only some calls fail, the rest is returned and Partial
//     says which. An issue-less repo (issues turned off) is not a failure.
//   - Fetch runs `gh auth status -h github.com` and the list calls at the same
//     time (one round trip). Each call uses `-R github.com/owner/repo`.
//     The CI run is skipped when the branch is "", "HEAD" (detached), starts with
//     '-' or has control characters.
//   - Every PR, issue and run whose URL does not start with https://github.com/
//     is dropped. Titles lose control characters and are cut at 300 characters.
//   - Cache.Get: a good Result younger than 5 minutes (TTL) is returned as it is.
//     Many callers at once share one fetch. A forced Get is refused (the cached
//     result is returned) when the last attempt was less than 10 s ago. If the
//     fetch fails and older data exists, the old data comes back with StaleSince
//     set, a nil error, and OnError is called (the caller decides how often to
//     log: once per project per hour). gh is not called again for 1 minute after
//     a failure. "Unavailable" answers are reused for 1 minute only.
//     A remote that is not on github.com, or Cache.Disabled (config gh_enabled =
//     false), answers at once and caches nothing.
//   - Background refresh (15 minutes): the indexer asks Due(projectID) and, if it
//     is true and the project page is visible, calls Get(..., force=true).
//
// # Wiring notes
//
// Store has one blob per project. Suggested gh_cache row (no schema change needed):
// project_id = project, kind = StoreKind ("result"), number = 0, title = the JSON,
// fetched_at = at; use INSERT OR REPLACE. Do not store Unavailable results (Cache
// never calls Put for them). Get must return ok=false when there is no row.
//
// The /api/projects/{p}/github endpoint (04 section 6) can marshal a Result
// directly: {prs, issues:{openCount, capped?, latest}, runs, fetchedAt, ...}.
// The web type GithubResponse has issues as an array: map Issues.Latest to it.
package gh
