// Package fossil is the fossil probe. It runs the fossil command line (never a
// shell) through runx with the project folder as the working directory. Only
// read commands run. At most one fossil process runs per project at a time
// (a per-project lock), because fossil locks its repository database.
//
// Exported API
//
//	const MaxCommits = 200        check-ins read by Scan
//	const PatchCap = 400 << 10    largest patch or diff text, in bytes
//
//	var ErrBadRef       hash is not ^[0-9a-f]{6,64}$ (checked before fossil runs)
//	var ErrNotFound     the repository has no check-in with that hash
//	var ErrNotCheckout  the folder has no .fslckout or _FOSSIL_ file
//	var ErrBadPath      a history path is absolute or leaves the project
//
//	type Result struct             what Scan learned (JSON tags are camelCase)
//	    IsRepo         bool          false when the folder is not a checkout
//	    Branch         string        branch of the checked-out version ("trunk")
//	    Remote         string        default sync URL without user:password@; "" when off
//	    Head           string        full hash of the checked-out check-in
//	    Repo           string        path of the repository file (pass to CacheKey)
//	    Commits        []probe.Commit  newest first, at most MaxCommits
//	    Dirty          []probe.DirtyFile  tracked files only; codes M A D R U
//	    DirtyTruncated bool
//	    Unavailable    *probe.Unavailable  set when fossil is not installed
//
//	func Detect(path string) bool
//	    Does path hold .fslckout or _FOSSIL_.
//
//	func Available() bool
//	    Is the fossil binary on PATH.
//
//	func Scan(ctx, p probe.Project) (Result, error)
//	    Branch, remote, head, newest 200 check-ins, changed files.
//	    Not a checkout: IsRepo=false, no error. No fossil: Unavailable, no error.
//	    Timeout or failure: an error (keep the cached data).
//
//	func CacheKey(p probe.Project, repo string) string
//	    Time and size of .fslckout (and -wal), plus the repository file when
//	    repo (Result.Repo) is not empty. Starts no process. Read-only fossil
//	    commands do not change these files, so Scan never changes the key.
//	    "" when not a checkout.
//
//	func Commits(ctx, p probe.Project, skip, limit int, path string) ([]probe.Commit, error)
//	    Paging (skip, then limit, clamped to 1..500). A non-empty path (relative)
//	    limits the history to that file or folder; line counts stay per check-in.
//
//	func Detail(ctx, p probe.Project, hash string) (probe.CommitDetail, error)
//	    One check-in: files with line counts, and the patch against the first
//	    parent with 3 lines of context, cut at PatchCap. hash may be short (6+).
//	    The first check-in of a repo has no parent, so its patch is "".
//
//	const DefaultDayWindow = 35   days DayStats covers when days < 1
//
//	func DayStats(ctx, p probe.Project, days int, loc *time.Location) (map[string]probe.DayCount, error)
//	func DayStatsAt(ctx, p probe.Project, now time.Time, days int, loc *time.Location) (map[string]probe.DayCount, error)
//	    EXACT check-in COUNTS per local day for the last `days` days ending at now,
//	    from one `fossil timeline after <date> -n 0` (no 200 limit; per-project lock
//	    held). Plus and Minus are always 0: take fossil lines from Scan's commits.
//
//	func DirtyDiff(ctx, p probe.Project) (diff string, truncated bool, err error)
//	    Diff of all uncommitted changes to tracked files, cut at PatchCap.
//
// Facts for the indexer
//
//   - probe.Commit.ID is the full hash (64 hex for SHA3, 40 for SHA1). Times are UTC.
//   - Fossil keeps a comment as one line. Commit.Msg holds the whole comment
//     (at most 500 bytes) and Commit.Body is always empty.
//   - Plus, Minus, NFiles and Files cost one fossil process per check-in. Each
//     Scan or Commits call looks up at most 30 check-ins that are not known yet,
//     newest first, and remembers the answers in memory. So the first Scan
//     fills the newest 30, the next one the next 30, and so on. Unknown ones
//     are 0 until then. Binary files are not counted by fossil.
//   - Untracked files are not in Dirty (fossil extras can be very slow).
//   - Every project has fossil's own first check-in "initial empty check-in",
//     so a new repo has one commit.
//   - Each call has a 10 second timeout (runx). Output is capped at 8 MB.
package fossil
