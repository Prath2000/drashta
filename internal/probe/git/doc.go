// Package git is the git probe. It runs the git command line (never a shell)
// through runx, always as `git -C <path> --no-optional-locks ...`, so drashta
// never takes the index lock and never changes a repo. Only read commands run.
//
// Exported API
//
//	const MaxCommits = 200        commits read by Scan
//	const PatchCap = 400 << 10    largest patch or diff text, in bytes
//
//	var ErrBadRef    commit id is not ^[0-9a-f]{7,40}$ (checked before git runs)
//	var ErrNotFound  git has no commit with that id
//	var ErrNotRepo   the project folder has no .git entry
//	var ErrBadPath   a history path is absolute or leaves the project
//
//	type Result struct             what Scan learned (JSON tags are camelCase)
//	    IsRepo         bool          false when the folder has no .git
//	    Branch         string        current branch; "HEAD" when detached
//	    Detached       bool          HEAD is not on a branch
//	    Remote         string        URL of "origin", user:password@ removed
//	    Head           string        full hash of HEAD; "" in a repo with no commit
//	    Commits        []probe.Commit  newest first, at most MaxCommits, each with Files
//	    Dirty          []probe.DirtyFile  codes M A D R ? U (see probe.DirtyFile)
//	    DirtyTruncated bool          more than 5000 dirty paths
//	    Unavailable    *probe.Unavailable  set when git is not installed
//
//	func Available() bool
//	    Is the git binary on PATH.
//
//	func Scan(ctx, p probe.Project) (Result, error)
//	    Branch, remote, HEAD, newest 200 commits with numstat, dirty files.
//	    No .git: IsRepo=false, no error. No git binary: Unavailable, no error.
//	    Timeout or git failure: an error (keep the cached data).
//
//	func CacheKey(p probe.Project) string
//	    HEAD text + branch tip hash + .git/index time and size. Reads files only,
//	    starts no process. "" when not a repo. Works for worktrees and packed refs.
//	    It cannot see edits to files that were never staged.
//
//	func Commits(ctx, p probe.Project, skip, limit int, path string) ([]probe.Commit, error)
//	    Paging (skip, then limit, clamped to 1..500). A non-empty path (relative,
//	    may be a deleted file) gives the history of that path only; then Plus,
//	    Minus and Files cover only that path.
//
//	func Detail(ctx, p probe.Project, sha string) (probe.CommitDetail, error)
//	    One commit: full body, all files, patch with 3 lines of context cut at
//	    PatchCap (PatchTruncated). sha may be short (7 to 40 hex).
//
//	const DefaultDayWindow = 35   days DayStats covers when days < 1
//
//	func DayStats(ctx, p probe.Project, days int, loc *time.Location) (map[string]probe.DayCount, error)
//	func DayStatsAt(ctx, p probe.Project, now time.Time, days int, loc *time.Location) (map[string]probe.DayCount, error)
//	    EXACT commits and lines per local day ("YYYY-MM-DD" in loc) for the last
//	    `days` days ending at now (DayStats uses time.Now). One `git log --numstat`
//	    pass, no 200 cap. Same rules as Scan: committer date, merges count as
//	    commits with 0 lines, binary files 0 lines. Only days with commits are in
//	    the map. Past the 8 MB output cap a counts-only pass fills the counts and
//	    the lines of the commits past the cap are 0. Not a repo / no commit: empty map.
//
//	func DirtyDiff(ctx, p probe.Project) (diff string, truncated bool, err error)
//	    All uncommitted changes of tracked files against HEAD (staged and
//	    unstaged), cut at PatchCap. Untracked files have no diff.
//
// Facts for the indexer
//
//   - probe.Commit.ID is the full 40 character hash. Show the first 7.
//   - Time is the committer date with the committer's own zone. For the per-day
//     numbers use DayStats (exact); probe.DayCounts over Scan's commits is capped at 200.
//   - Commit.Msg is the subject (at most 500 bytes). Commit.Body is the rest
//     (at most 4 KB in Scan and Commits, 64 KB in Detail). Commit.Branch is
//     empty: git does not say which branch a commit "belongs" to.
//   - A merge commit has no files and 0 plus and minus (git shows no diff for
//     it by default). A binary file counts as 0 and 0 but is listed. A rename
//     is listed once, under the new path. Files is cut at 500 per commit in
//     Scan and Commits (NFiles, Plus and Minus still count all of them).
//   - Each call has a 10 second timeout (runx). Output is capped at 8 MB.
//   - A project folder whose own .git is broken is an error. git is not
//     allowed to walk up into a parent repo (GIT_CEILING_DIRECTORIES).
package git
