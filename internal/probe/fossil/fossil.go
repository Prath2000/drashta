package fossil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/runx"
)

const (
	// MaxCommits is how many check-ins Scan reads (03-architecture.md section 10).
	MaxCommits = 200
	// PatchCap is the largest patch or diff text we return, in bytes.
	PatchCap = 400 << 10

	maxPageLimit    = 500
	maxMsgBytes     = 500
	maxDirty        = 5000
	maxFilesPerStat = 200
	maxStatEntries  = 20000
	smallOut        = 64 << 10
	callTimeout     = 10 * time.Second
)

// statBudget is how many check-ins without known line counts one call may
// look up. Each lookup is one fossil process. Counts of a check-in never change,
// so they are remembered; later calls fill in the older check-ins. It is a
// variable so a test can make it small.
var statBudget = 30

var (
	// ErrBadRef means the hash is not 6 to 64 lowercase hex characters.
	ErrBadRef = errors.New("fossil: invalid check-in hash")
	// ErrNotFound means the repository has no check-in with that hash.
	ErrNotFound = errors.New("fossil: check-in not found")
	// ErrNotCheckout means the project folder is not an open fossil checkout.
	ErrNotCheckout = errors.New("fossil: not an open checkout")
	// ErrBadPath means a history path is absolute or leaves the project.
	ErrBadPath = errors.New("fossil: path is outside the project")
)

// Result is what Scan learned about one project.
type Result struct {
	IsRepo         bool               `json:"isRepo"`
	Branch         string             `json:"branch,omitempty"`
	Remote         string             `json:"remote,omitempty"` // default sync URL without credentials; empty when "off"
	Head           string             `json:"head,omitempty"`   // full hash of the checked-out check-in
	Repo           string             `json:"repo,omitempty"`   // path of the repository file; pass it to CacheKey
	Commits        []probe.Commit     `json:"commits,omitempty"`
	Dirty          []probe.DirtyFile  `json:"dirty,omitempty"` // tracked files only; untracked files are not listed
	DirtyTruncated bool               `json:"dirtyTruncated,omitempty"`
	Unavailable    *probe.Unavailable `json:"unavailable,omitempty"`
}

// Available reports whether the fossil binary is on PATH.
func Available() bool { return runx.Available("fossil") }

// Detect reports whether path is the root of a fossil checkout: it holds a
// .fslckout (Unix) or _FOSSIL_ (Windows style) file.
func Detect(path string) bool {
	for _, name := range []string{".fslckout", "_FOSSIL_"} {
		if _, err := os.Lstat(filepath.Join(path, name)); err == nil {
			return true
		}
	}
	return false
}

// call runs one read-only fossil command in the project folder, alone: the
// per-project lock allows one fossil process per project at a time.
func call(ctx context.Context, p probe.Project, maxOut int, args ...string) (runx.Result, error) {
	var res runx.Result
	err := withLock(ctx, filepath.Clean(p.Path), func() error {
		var e error
		res, e = runx.Run(ctx, runx.Options{Dir: p.Path, Timeout: callTimeout, MaxOut: maxOut}, "fossil", args...)
		return e
	})
	if err != nil {
		if errors.Is(err, runx.ErrNotFound) {
			return res, err
		}
		return res, fmt.Errorf("fossil %s: %w", args[0], err)
	}
	return res, nil
}

// failed turns a non-zero exit into an error that carries the first stderr text.
func failed(res runx.Result, sub string) error {
	if res.ExitCode == 0 {
		return nil
	}
	msg := strings.TrimSpace(string(res.Stderr))
	if len(msg) > 300 {
		msg = cutBytes(msg, 300) + "..."
	}
	return fmt.Errorf("fossil %s: exit %d: %s", sub, res.ExitCode, msg)
}

// Scan reads branch, remote, the newest MaxCommits check-ins and the changed
// files of one project.
//
// Line counts (Plus, Minus, NFiles, Files) cost one process per check-in, so
// each call looks up at most statBudget check-ins that are not yet known,
// newest first. The rest stay 0 until a later call fills them in.
//
// A folder that is not a checkout gives IsRepo=false and no error. A missing
// fossil binary gives Unavailable and no error. A timeout or a fossil failure
// returns the error, and the caller keeps its cached data.
func Scan(ctx context.Context, p probe.Project) (Result, error) {
	if !Available() {
		return Result{Unavailable: &probe.Unavailable{Reason: "fossil is not installed"}}, nil
	}
	if !filepath.IsAbs(p.Path) {
		return Result{}, fmt.Errorf("fossil: project path is not absolute: %q", p.Path)
	}
	if !Detect(p.Path) {
		return Result{}, nil
	}
	res := Result{IsRepo: true}

	info, err := call(ctx, p, smallOut, "info")
	if err != nil {
		return res, err
	}
	if err := failed(info, "info"); err != nil {
		return res, err
	}
	if v := infoValues(info.Stdout, "checkout"); len(v) > 0 {
		res.Head = firstWord(v[0])
	}
	if v := infoValues(info.Stdout, "repository"); len(v) > 0 {
		res.Repo = v[0]
	}

	br, err := call(ctx, p, smallOut, "branch", "current")
	if err != nil {
		return res, err
	}
	if br.ExitCode == 0 {
		res.Branch = strings.TrimSpace(string(br.Stdout))
	}

	rm, err := call(ctx, p, smallOut, "remote")
	if err != nil {
		return res, err
	}
	if rm.ExitCode == 0 {
		line, _, _ := strings.Cut(strings.TrimSpace(string(rm.Stdout)), "\n")
		if line = strings.TrimSpace(line); line != "" && line != "off" && line != "none" {
			res.Remote = stripCreds(line)
		}
	}

	commits, err := timeline(ctx, p, 0, MaxCommits, "")
	if err != nil {
		return res, err
	}
	res.Commits = commits
	res.Head, err = fullHash(ctx, p, res.Head, commits)
	if err != nil {
		return res, err
	}

	ch, err := call(ctx, p, runx.MaxStdout, "changes", "--rel-paths")
	if err != nil {
		return res, err
	}
	if err := failed(ch, "changes"); err != nil {
		return res, err
	}
	res.Dirty, res.DirtyTruncated = parseChanges(ch.Stdout)

	if err := fillStats(ctx, p, res.Commits, statBudget); err != nil {
		return res, err
	}
	return res, nil
}

// fullHash widens the hash that `fossil info` prints (it is cut to 40
// characters) to the full hash. It looks in the commits already read first,
// and asks fossil only when the checked-out version is older than those.
// When nothing matches it returns short unchanged.
func fullHash(ctx context.Context, p probe.Project, short string, commits []probe.Commit) (string, error) {
	if short == "" {
		return "", nil
	}
	for _, c := range commits {
		if strings.HasPrefix(c.ID, short) {
			return c.ID, nil
		}
	}
	res, err := call(ctx, p, smallOut, "timeline", short, "--utc", "-q", "-t", "ci", "-n", "1", "-F", timelineFormat)
	if err != nil {
		return short, err
	}
	if cs := parseTimeline(res.Stdout); len(cs) == 1 && strings.HasPrefix(cs[0].ID, short) {
		return cs[0].ID, nil
	}
	return short, nil
}

// timeline lists check-ins, newest first. skip and limit page through them.
func timeline(ctx context.Context, p probe.Project, skip, limit int, pathspec string) ([]probe.Commit, error) {
	args := []string{
		"timeline", "--utc", "-q", "-t", "ci",
		"-n", strconv.Itoa(limit), "--offset", strconv.Itoa(skip),
		"-F", timelineFormat,
	}
	if pathspec != "" {
		args = append(args, "-p", pathspec)
	}
	res, err := call(ctx, p, runx.MaxStdout, args...)
	if err != nil {
		return nil, err
	}
	if err := failed(res, "timeline"); err != nil {
		return nil, err
	}
	return parseTimeline(res.Stdout), nil
}

// Commits pages through history: skip check-ins, then return up to limit.
// A non-empty path limits the history to that file or folder (relative to the
// project root). Line counts are those of the whole check-in, not of the path.
// The same statBudget rule as Scan applies to the line counts.
func Commits(ctx context.Context, p probe.Project, skip, limit int, path string) ([]probe.Commit, error) {
	if skip < 0 {
		skip = 0
	}
	if limit < 1 {
		limit = 1
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	spec, err := historyPath(p.Path, path)
	if err != nil {
		return nil, err
	}
	if !Detect(p.Path) {
		return nil, ErrNotCheckout
	}
	commits, err := timeline(ctx, p, skip, limit, spec)
	if err != nil {
		return nil, err
	}
	if err := fillStats(ctx, p, commits, statBudget); err != nil {
		return nil, err
	}
	return commits, nil
}

// historyPath checks a path used for `fossil timeline -p`. It must be relative
// and, after cleaning, stay inside the project. It may name a file that no
// longer exists, so pathguard.Resolve (which needs the file) is not used.
func historyPath(root, rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	if filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return "", ErrBadPath
	}
	clean := filepath.Clean(rel)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		!pathguard.Inside(root, filepath.Join(root, clean)) {
		return "", ErrBadPath
	}
	return filepath.ToSlash(clean), nil
}

// Detail returns one check-in with its file list and its patch (3 lines of context).
//
// hash must be 6 to 64 lowercase hex characters; it is checked before any
// command runs. The patch is the diff against the first parent, cut at
// PatchCap bytes (PatchTruncated). The first check-in of a repo has no parent
// and an empty patch. Fossil keeps a comment as one line, so Msg holds all of
// it and Body is empty.
func Detail(ctx context.Context, p probe.Project, hash string) (probe.CommitDetail, error) {
	if !hashRe.MatchString(hash) {
		return probe.CommitDetail{}, ErrBadRef
	}
	if !Detect(p.Path) {
		return probe.CommitDetail{}, ErrNotCheckout
	}

	// 1. Author, date, comment and the full hash.
	tl, err := call(ctx, p, smallOut, "timeline", hash, "--utc", "-q", "-t", "ci", "-n", "1", "-F", timelineFormat)
	if err != nil {
		return probe.CommitDetail{}, err
	}
	commits := parseTimeline(tl.Stdout)
	// fossil also reads a bare number such as 20260930 as a date. Only a real
	// prefix of the hash counts as a match.
	if len(commits) == 0 || !strings.HasPrefix(commits[0].ID, hash) {
		if len(commits) == 0 && tl.ExitCode != 0 && strings.TrimSpace(string(tl.Stderr)) != "" {
			return probe.CommitDetail{}, failed(tl, "timeline")
		}
		return probe.CommitDetail{}, ErrNotFound
	}
	c := commits[0]

	// 2. Line counts per file.
	files, err := checkinNumstat(ctx, p, c.ID)
	if err != nil {
		return probe.CommitDetail{}, err
	}
	applyStat(&c, files)

	// 3. Parent, then the patch against it.
	info, err := call(ctx, p, smallOut, "info", c.ID)
	if err != nil {
		return probe.CommitDetail{}, err
	}
	if err := failed(info, "info"); err != nil {
		return probe.CommitDetail{}, err
	}
	d := probe.CommitDetail{Commit: c}
	parent := ""
	if v := infoValues(info.Stdout, "parent"); len(v) > 0 {
		parent = firstWord(v[0])
	}
	if parent != "" && hashRe.MatchString(parent) {
		pr, err := call(ctx, p, PatchCap, "diff", "-i", "-N", "-c", "3", "--from", parent, "--to", c.ID)
		if err != nil {
			return probe.CommitDetail{}, err
		}
		if err := failed(pr, "diff"); err != nil {
			return probe.CommitDetail{}, err
		}
		d.Patch, d.PatchTruncated = capText(pr.Stdout, pr.Truncated)
	}
	return d, nil
}

// capText makes a patch safe to return: valid UTF-8, and when fossil output
// was cut at the cap, trimmed back to the last whole line.
func capText(out []byte, cut bool) (string, bool) {
	s := cleanText(string(out))
	if !cut {
		return s, false
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[:i+1]
	}
	return s, true
}

// DirtyDiff returns the diff of all uncommitted changes to tracked files,
// capped at PatchCap bytes. fossil's own diff code is used (-i), never a
// diff program from the user's settings.
func DirtyDiff(ctx context.Context, p probe.Project) (string, bool, error) {
	if !Detect(p.Path) {
		return "", false, ErrNotCheckout
	}
	res, err := call(ctx, p, PatchCap, "diff", "-i", "-N", "-c", "3")
	if err != nil {
		return "", false, err
	}
	if err := failed(res, "diff"); err != nil {
		return "", false, err
	}
	s, cut := capText(res.Stdout, res.Truncated)
	return s, cut, nil
}

// CacheKey is a cheap key that changes when the checkout database changes
// (commit, update, add, rename, ...). It uses file sizes and times and starts
// no process. repo is Result.Repo from the last Scan; when it is not empty the
// repository file is part of the key too, so a check-in made from another
// checkout of the same repository is noticed.
//
// Read-only commands such as `fossil changes` do not touch these files, so
// running Scan does not change the key. It returns "" when the folder is not a checkout.
func CacheKey(p probe.Project, repo string) string {
	var parts []string
	for _, name := range []string{".fslckout", "_FOSSIL_"} {
		if s := statKey(filepath.Join(p.Path, name)); s != "" {
			parts = append(parts, name+"="+s)
			if w := statKey(filepath.Join(p.Path, name+"-wal")); w != "" {
				parts = append(parts, name+"-wal="+w)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if repo != "" {
		parts = append(parts, "repo="+statKey(repo))
		if w := statKey(repo + "-wal"); w != "" {
			parts = append(parts, "repo-wal="+w)
		}
	}
	return strings.Join(parts, "|")
}

func statKey(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d.%d", fi.ModTime().UnixNano(), fi.Size())
}

// ---- line counts ----------------------------------------------------------

type stat struct {
	files       []probe.CommitFile // at most maxFilesPerStat
	n           int                // number of changed files
	plus, minus int
}

var statCache = struct {
	sync.Mutex
	m map[string]stat
}{m: map[string]stat{}}

func statKeyFor(p probe.Project, id string) string { return filepath.Clean(p.Path) + "\x00" + id }

// applyStat copies parsed numstat rows into c. The totals cover every file;
// the stored list is cut at maxFilesPerStat.
func applyStat(c *probe.Commit, files []probe.CommitFile) {
	c.Plus, c.Minus, c.NFiles = 0, 0, len(files)
	for _, f := range files {
		c.Plus += f.Plus
		c.Minus += f.Minus
	}
	c.Files = files
	if len(c.Files) > maxFilesPerStat {
		c.Files = c.Files[:maxFilesPerStat]
	}
}

// checkinNumstat returns the per-file counts of one check-in against its first
// parent (one `fossil diff --numstat --checkin` process). A check-in without a
// parent gives no files.
func checkinNumstat(ctx context.Context, p probe.Project, id string) ([]probe.CommitFile, error) {
	res, err := call(ctx, p, runx.MaxStdout, "diff", "-i", "--numstat", "--checkin", id)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(string(res.Stderr), "has no parent") {
			return nil, nil
		}
		return nil, failed(res, "diff")
	}
	return parseNumstat(res.Stdout), nil
}

// fillStats adds line counts to commits, newest first. Known counts come from
// the cache. At most budget unknown check-ins are looked up. A failed lookup
// leaves that check-in at 0; only a cancelled ctx is returned as an error.
func fillStats(ctx context.Context, p probe.Project, commits []probe.Commit, budget int) error {
	for i := range commits {
		c := &commits[i]
		key := statKeyFor(p, c.ID)
		statCache.Lock()
		st, ok := statCache.m[key]
		statCache.Unlock()
		if ok {
			c.Plus, c.Minus, c.NFiles = st.plus, st.minus, st.n
			c.Files = append([]probe.CommitFile(nil), st.files...)
			continue
		}
		if budget <= 0 {
			continue
		}
		budget--
		files, err := checkinNumstat(ctx, p, c.ID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		applyStat(c, files)
		statCache.Lock()
		if len(statCache.m) >= maxStatEntries {
			statCache.m = map[string]stat{}
		}
		statCache.m[key] = stat{files: c.Files, n: c.NFiles, plus: c.Plus, minus: c.Minus}
		statCache.Unlock()
	}
	return nil
}
