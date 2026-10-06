package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/runx"
)

const (
	// MaxCommits is how many commits Scan reads (03-architecture.md section 10).
	MaxCommits = 200
	// PatchCap is the largest patch or diff text we return, in bytes.
	PatchCap = 400 << 10

	maxPageLimit   = 500
	maxMsgBytes    = 500
	maxBodyScan    = 4 << 10  // body kept per commit in Scan and Commits
	maxBodyDetail  = 64 << 10 // body kept in Detail
	maxFilesScan   = 500      // files listed per commit in Scan and Commits
	maxFilesDetail = 5000     // files listed per commit in Detail
	maxDirty       = 5000
	smallOut       = 64 << 10
	callTimeout    = 10 * time.Second
)

var (
	// ErrBadRef means the commit id is not 7 to 40 lowercase hex characters.
	ErrBadRef = errors.New("git: invalid commit id")
	// ErrNotFound means git has no commit with that id.
	ErrNotFound = errors.New("git: commit not found")
	// ErrNotRepo means the project folder has no .git entry.
	ErrNotRepo = errors.New("git: not a repository")
	// ErrBadPath means a history path is absolute or leaves the project.
	ErrBadPath = errors.New("git: path is outside the project")

	shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// Result is what Scan learned about one project.
type Result struct {
	IsRepo         bool               `json:"isRepo"`
	Branch         string             `json:"branch,omitempty"`   // "HEAD" when detached
	Detached       bool               `json:"detached,omitempty"` // HEAD is not on a branch
	Remote         string             `json:"remote,omitempty"`   // origin URL without credentials
	Head           string             `json:"head,omitempty"`     // full hash; empty when there is no commit yet
	Commits        []probe.Commit     `json:"commits,omitempty"`  // newest first, at most MaxCommits
	Dirty          []probe.DirtyFile  `json:"dirty,omitempty"`
	DirtyTruncated bool               `json:"dirtyTruncated,omitempty"`
	Unavailable    *probe.Unavailable `json:"unavailable,omitempty"`
}

// Available reports whether the git binary is on PATH.
func Available() bool { return runx.Available("git") }

// hasDotGit reports whether root has a .git entry (a folder, or a file for a
// linked worktree or submodule). Only the project folder itself is checked, so
// a project inside some bigger repo is never mistaken for that repo.
func hasDotGit(root string) (bool, error) {
	if !filepath.IsAbs(root) {
		return false, fmt.Errorf("git: project path is not absolute: %q", root)
	}
	_, err := os.Lstat(filepath.Join(root, ".git"))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// run executes one read-only git command in the project.
//
// Every call is `git -C <path> --no-optional-locks ...`, so git never takes the
// index lock. The extra -c options keep the user's own git config (colours,
// signature printing, fsmonitor, pager) from changing what we parse.
// GIT_CEILING_DIRECTORIES stops git from walking up into a parent repo when the
// project's own .git is broken.
func run(ctx context.Context, path string, maxOut int, args ...string) (runx.Result, error) {
	full := []string{
		"-C", path,
		"--no-optional-locks", "--no-pager", "--literal-pathspecs",
		"-c", "core.quotepath=off",
		"-c", "core.fsmonitor=false",
		"-c", "color.ui=never",
		"-c", "log.showSignature=false",
		"-c", "i18n.logOutputEncoding=UTF-8",
	}
	full = append(full, args...)
	res, err := runx.Run(ctx, runx.Options{
		Env: []string{
			"GIT_OPTIONAL_LOCKS=0",
			"GIT_TERMINAL_PROMPT=0",
			"GIT_CEILING_DIRECTORIES=" + filepath.Dir(path),
		},
		Timeout: callTimeout,
		MaxOut:  maxOut,
	}, "git", full...)
	if err != nil {
		if errors.Is(err, runx.ErrNotFound) {
			return res, err
		}
		return res, fmt.Errorf("git %s: %w", args[0], err)
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
	return fmt.Errorf("git %s: exit %d: %s", sub, res.ExitCode, msg)
}

// Scan reads branch, remote, the newest MaxCommits commits (with numstat) and
// the dirty files of one project.
//
// A folder without .git gives IsRepo=false and no error. A missing git binary
// gives Unavailable and no error. A timeout or a git failure returns the error,
// and the caller keeps its cached data.
func Scan(ctx context.Context, p probe.Project) (Result, error) {
	if !Available() {
		return Result{Unavailable: &probe.Unavailable{Reason: "git is not installed"}}, nil
	}
	ok, err := hasDotGit(p.Path)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, nil
	}
	res := Result{IsRepo: true}

	st, err := run(ctx, p.Path, runx.MaxStdout, "status", "--porcelain=v2", "--branch", "--untracked-files=normal", "-z")
	if err != nil {
		return res, err
	}
	if err := failed(st, "status"); err != nil {
		return res, err
	}
	si := parseStatus(st.Stdout, st.Truncated)
	res.Branch, res.Detached, res.Head = si.head, si.detached, si.oid
	res.Dirty, res.DirtyTruncated = si.files, si.truncated

	if res.Head != "" {
		commits, err := logCommits(ctx, p.Path, 0, MaxCommits, "")
		if err != nil {
			return res, err
		}
		res.Commits = commits
	}

	rm, err := run(ctx, p.Path, smallOut, "remote", "get-url", "origin")
	if err != nil {
		return res, err
	}
	if rm.ExitCode == 0 { // exit 2 means there is no origin
		res.Remote = stripCreds(string(rm.Stdout))
	}
	return res, nil
}

// logCommits runs git log with numstat and parses it. An empty repo gives nil.
func logCommits(ctx context.Context, path string, skip, limit int, pathspec string) ([]probe.Commit, error) {
	args := []string{
		"log", "-z", "--numstat", "--no-ext-diff", "--no-textconv",
		"--skip=" + fmt.Sprint(skip), "-n", fmt.Sprint(limit), logFormat,
	}
	if pathspec != "" {
		args = append(args, "--", pathspec)
	}
	res, err := run(ctx, path, runx.MaxStdout, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(string(res.Stderr), "does not have any commits yet") {
			return nil, nil
		}
		return nil, failed(res, "log")
	}
	return parseLog(res.Stdout, res.Truncated, maxBodyScan, maxFilesScan), nil
}

// Commits pages through history: skip commits, then return up to limit.
// A non-empty path limits the history to that file or folder (relative to the
// project root; the file may no longer exist). With a path, Plus, Minus and
// Files count only the changes inside that path.
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
	ok, err := hasDotGit(p.Path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotRepo
	}
	return logCommits(ctx, p.Path, skip, limit, spec)
}

// historyPath checks a path used as a git pathspec. It is not required to exist
// (deleted files have history, so pathguard.Resolve cannot be used). Instead it
// must be relative, and after cleaning it must stay inside the project. git
// runs with --literal-pathspecs, so ":(top)" and "*" have no special meaning.
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

// Detail returns one commit with its file list and its patch (3 lines of context).
//
// sha must be 7 to 40 lowercase hex characters; it is checked before any
// command runs. The patch is cut at PatchCap bytes (PatchTruncated).
func Detail(ctx context.Context, p probe.Project, sha string) (probe.CommitDetail, error) {
	if !shaRe.MatchString(sha) {
		return probe.CommitDetail{}, ErrBadRef
	}
	ok, err := hasDotGit(p.Path)
	if err != nil {
		return probe.CommitDetail{}, err
	}
	if !ok {
		return probe.CommitDetail{}, ErrNotRepo
	}

	meta, err := run(ctx, p.Path, runx.MaxStdout,
		"log", "-z", "--numstat", "--no-ext-diff", "--no-textconv", "-n", "1", logFormat, sha, "--")
	if err != nil {
		return probe.CommitDetail{}, err
	}
	if meta.ExitCode != 0 {
		if notFoundText(string(meta.Stderr)) {
			return probe.CommitDetail{}, ErrNotFound
		}
		return probe.CommitDetail{}, failed(meta, "log")
	}
	commits := parseLog(meta.Stdout, meta.Truncated, maxBodyDetail, maxFilesDetail)
	if len(commits) == 0 {
		return probe.CommitDetail{}, ErrNotFound
	}

	pr, err := run(ctx, p.Path, PatchCap,
		"show", "--no-color", "--no-ext-diff", "--no-textconv", "--format=", "--unified=3", sha, "--")
	if err != nil {
		return probe.CommitDetail{}, err
	}
	if err := failed(pr, "show"); err != nil {
		return probe.CommitDetail{}, err
	}
	patch, truncated := capText(pr.Stdout, pr.Truncated)
	return probe.CommitDetail{Commit: commits[0], Patch: patch, PatchTruncated: truncated}, nil
}

// notFoundText reports whether git's error text means "no such commit".
func notFoundText(stderr string) bool {
	for _, m := range []string{"unknown revision", "bad revision", "bad object", "Not a valid object", "ambiguous argument", "does not have any commits yet"} {
		if strings.Contains(stderr, m) {
			return true
		}
	}
	return false
}

// capText makes a patch safe to return: valid UTF-8, and when git output was
// cut at the cap, trimmed back to the last whole line.
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

// DirtyDiff returns the diff of all uncommitted changes to tracked files
// (staged and unstaged together, against HEAD), capped at PatchCap bytes.
// Untracked files are listed by Scan but have no diff.
func DirtyDiff(ctx context.Context, p probe.Project) (string, bool, error) {
	ok, err := hasDotGit(p.Path)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, ErrNotRepo
	}
	base := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--unified=3"}
	res, err := run(ctx, p.Path, PatchCap, append(base, "HEAD", "--")...)
	if err != nil {
		return "", false, err
	}
	if res.ExitCode == 0 {
		s, cut := capText(res.Stdout, res.Truncated)
		return s, cut, nil
	}
	if !notFoundText(string(res.Stderr)) {
		return "", false, failed(res, "diff")
	}
	// No commit yet: show staged changes, then unstaged changes.
	staged, err := run(ctx, p.Path, PatchCap, append(base, "--cached", "--")...)
	if err != nil {
		return "", false, err
	}
	if err := failed(staged, "diff"); err != nil {
		return "", false, err
	}
	s1, cut1 := capText(staged.Stdout, staged.Truncated)
	if cut1 {
		return s1, true, nil
	}
	work, err := run(ctx, p.Path, PatchCap-len(s1), append(base, "--")...)
	if err != nil {
		return "", false, err
	}
	if err := failed(work, "diff"); err != nil {
		return "", false, err
	}
	s2, cut2 := capText(work.Stdout, work.Truncated)
	return s1 + s2, cut2, nil
}

// CacheKey is a cheap key that changes when the branch, its tip commit or the
// index changes. It reads three small files and starts no process. The indexer
// compares it with the stored key to skip a project that did not change.
//
// It does not see edits to files that were never staged: those do not touch
// .git. The indexer must treat the dirty list as stale after any file event.
//
// It returns "" when the folder is not a repo or the files cannot be read.
func CacheKey(p probe.Project) string {
	if ok, err := hasDotGit(p.Path); err != nil || !ok {
		return ""
	}
	gitDir, common, err := gitDirs(p.Path)
	if err != nil {
		return ""
	}
	head := readSmall(filepath.Join(gitDir, "HEAD"))
	if head == "" {
		return ""
	}
	tip := ""
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		tip = refTip(common, ref)
	}
	idx := "none"
	if fi, err := os.Stat(filepath.Join(gitDir, "index")); err == nil {
		idx = fmt.Sprintf("%d.%d", fi.ModTime().UnixNano(), fi.Size())
	}
	return head + "|" + tip + "|" + idx
}

// gitDirs finds the git dir (HEAD and index live here) and the common dir
// (refs live here). For a normal repo they are the same folder. For a linked
// worktree .git is a file that points to <main>/.git/worktrees/<name>, and a
// "commondir" file in there points back to <main>/.git.
func gitDirs(root string) (gitDir, common string, err error) {
	dot := filepath.Join(root, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return "", "", err
	}
	if fi.IsDir() {
		return dot, dot, nil
	}
	line, ok := strings.CutPrefix(readSmall(dot), "gitdir:")
	if !ok {
		return "", "", errors.New("git: .git file has no gitdir line")
	}
	gitDir = strings.TrimSpace(line)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	common = gitDir
	if c := readSmall(filepath.Join(gitDir, "commondir")); c != "" {
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitDir, c)
		}
		common = filepath.Clean(c)
	}
	return gitDir, common, nil
}

// readSmall returns the trimmed first 4 KB of a file, or "" on any error.
func readSmall(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 4<<10))
	return strings.TrimSpace(string(b))
}

// refTip returns the hash a ref points to, from the loose file or packed-refs.
// A ref name that is not under refs/ or that climbs with ".." is not read, so a
// hand-made HEAD cannot make us open other files.
func refTip(common, ref string) string {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "refs/") {
		return ""
	}
	full := filepath.Join(common, filepath.FromSlash(ref))
	if !pathguard.Inside(common, full) {
		return ""
	}
	if v := readSmall(full); v != "" {
		return v
	}
	f, err := os.Open(filepath.Join(common, "packed-refs"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	suffix := " " + ref
	for sc.Scan() {
		line := sc.Text()
		if strings.HasSuffix(line, suffix) && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "^") {
			return strings.TrimSuffix(line, suffix)
		}
	}
	return ""
}
