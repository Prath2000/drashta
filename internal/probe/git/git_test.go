package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// isolate points HOME and git's config at empty places, so the real user config
// never changes what a test sees. It skips the test when git is missing.
func isolate(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

// gitDo runs a git write command in a test repo with fixed identity and dates.
func gitDo(t *testing.T, dir, date string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=Test Author", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return out.String()
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDo(t, dir, "2026-09-30T10:00:00+05:30", "init", "-q", "-b", "main")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, date, msg string) {
	t.Helper()
	gitDo(t, dir, date, "add", "-A")
	gitDo(t, dir, date, "commit", "-q", "-m", msg)
}

func lines(n int, prefix string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%s line %d\n", prefix, i)
	}
	return b.String()
}

func proj(dir string) probe.Project {
	return probe.Project{ID: "g/proj", Name: "proj", Group: "g", Path: dir}
}

func TestScanNormalRepo(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "a.txt", lines(10, "alpha"))
	commit(t, dir, "2026-09-30T23:30:00+05:30", "first\n\nbody text here")
	write(t, dir, "a.txt", lines(10, "alpha")+"extra\n")
	write(t, dir, "src/b.go", "package b\n")
	commit(t, dir, "2026-10-01T10:00:00+05:30", "second: café")
	gitDo(t, dir, "2026-10-01T10:00:00+05:30", "remote", "add", "origin", "https://user:secret-token@example.com/me/proj.git")

	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsRepo || res.Unavailable != nil {
		t.Fatalf("IsRepo/Unavailable wrong: %+v", res)
	}
	if res.Branch != "main" || res.Detached {
		t.Errorf("branch = %q detached=%v", res.Branch, res.Detached)
	}
	if res.Remote != "https://example.com/me/proj.git" {
		t.Errorf("remote = %q (credentials must be stripped)", res.Remote)
	}
	if len(res.Head) != 40 {
		t.Errorf("head = %q", res.Head)
	}
	if len(res.Commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(res.Commits))
	}
	c0, c1 := res.Commits[0], res.Commits[1]
	if c0.ID != res.Head || c0.VCS != "git" {
		t.Errorf("newest commit id/vcs wrong: %+v", c0)
	}
	if c0.Msg != "second: café" || c0.Author != "Test Author" {
		t.Errorf("msg/author = %q / %q", c0.Msg, c0.Author)
	}
	wantTime := time.Date(2026, 10, 1, 10, 0, 0, 0, time.FixedZone("", 5*3600+1800))
	if !c0.Time.Equal(wantTime) {
		t.Errorf("time = %v, want %v", c0.Time, wantTime)
	}
	if c0.NFiles != 2 || c0.Plus != 2 || c0.Minus != 0 {
		t.Errorf("newest numstat: nfiles=%d plus=%d minus=%d (%+v)", c0.NFiles, c0.Plus, c0.Minus, c0.Files)
	}
	if len(c0.Files) != 2 || c0.Files[0].Path != "a.txt" || c0.Files[1].Path != "src/b.go" {
		t.Errorf("files = %+v", c0.Files)
	}
	if c1.Msg != "first" || c1.Body != "body text here" || c1.Plus != 10 || c1.NFiles != 1 {
		t.Errorf("first commit wrong: %+v", c1)
	}
	if len(res.Dirty) != 0 {
		t.Errorf("clean repo reported dirty files: %+v", res.Dirty)
	}
}

func TestScanRenameBinaryAndMerge(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "a.txt", lines(20, "alpha"))
	commit(t, dir, "2026-09-30T10:00:00+05:30", "c1")

	// c2: rename a.txt to a name with a tab, with one added line, plus a binary file.
	gitDo(t, dir, "2026-09-30T11:00:00+05:30", "mv", "a.txt", "renamed\tname.txt")
	write(t, dir, "renamed\tname.txt", lines(20, "alpha")+"added\n")
	write(t, dir, "bin.dat", "ab\x00cd\x00\x01\x02")
	commit(t, dir, "2026-09-30T11:00:00+05:30", "c2 rename and binary")

	// c3: side branch + main commit + merge.
	gitDo(t, dir, "2026-09-30T12:00:00+05:30", "checkout", "-q", "-b", "feature")
	write(t, dir, "f.txt", "feature\n")
	commit(t, dir, "2026-09-30T12:00:00+05:30", "feature work")
	gitDo(t, dir, "2026-09-30T13:00:00+05:30", "checkout", "-q", "main")
	write(t, dir, "m.txt", "main\n")
	commit(t, dir, "2026-09-30T13:00:00+05:30", "main work")
	gitDo(t, dir, "2026-09-30T14:00:00+05:30", "merge", "-q", "--no-ff", "-m", "merge feature", "feature")

	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 5 {
		t.Fatalf("got %d commits, want 5: %+v", len(res.Commits), res.Commits)
	}
	byMsg := map[string]probe.Commit{}
	for _, c := range res.Commits {
		byMsg[c.Msg] = c
	}
	merge := byMsg["merge feature"]
	if merge.NFiles != 0 || merge.Plus != 0 {
		t.Errorf("merge commit should have no numstat: %+v", merge)
	}
	if res.Commits[0].Msg != "merge feature" {
		t.Errorf("merge commit should be newest, got %q", res.Commits[0].Msg)
	}
	c2 := byMsg["c2 rename and binary"]
	if c2.NFiles != 2 {
		t.Fatalf("c2 files = %+v", c2.Files)
	}
	var sawRename, sawBinary bool
	for _, f := range c2.Files {
		switch f.Path {
		case "renamed\tname.txt":
			sawRename = true
			if f.Plus != 1 || f.Minus != 0 {
				t.Errorf("rename should count only the added line: %+v", f)
			}
		case "bin.dat":
			sawBinary = true
			if f.Plus != 0 || f.Minus != 0 {
				t.Errorf("binary file should count 0/0: %+v", f)
			}
		}
	}
	if !sawRename || !sawBinary {
		t.Errorf("rename=%v binary=%v in %+v", sawRename, sawBinary, c2.Files)
	}
	if c2.Plus != 1 {
		t.Errorf("c2 plus = %d, want 1", c2.Plus)
	}
}

func TestScanEmptyRepo(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsRepo || res.Branch != "main" || res.Head != "" || len(res.Commits) != 0 {
		t.Errorf("empty repo result wrong: %+v", res)
	}
	cs, err := Commits(context.Background(), proj(dir), 0, 10, "")
	if err != nil || len(cs) != 0 {
		t.Errorf("Commits on empty repo = %v, %v", cs, err)
	}
	if CacheKey(proj(dir)) == "" {
		t.Error("CacheKey of an empty repo should not be empty")
	}
	// Staged files in an empty repo still produce a diff.
	write(t, dir, "x.txt", "hello\n")
	gitDo(t, dir, "2026-09-30T10:00:00+05:30", "add", "x.txt")
	diff, cut, err := DirtyDiff(context.Background(), proj(dir))
	if err != nil || cut || !strings.Contains(diff, "+hello") {
		t.Errorf("DirtyDiff in empty repo = %q cut=%v err=%v", diff, cut, err)
	}
}

func TestScanDetachedHead(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "a.txt", "a\n")
	commit(t, dir, "2026-09-30T10:00:00+05:30", "c1")
	write(t, dir, "a.txt", "a\nb\n")
	commit(t, dir, "2026-09-30T11:00:00+05:30", "c2")
	gitDo(t, dir, "2026-09-30T11:00:00+05:30", "checkout", "-q", "--detach", "HEAD~1")
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Detached || res.Branch != "HEAD" || res.Head == "" {
		t.Errorf("detached result wrong: branch=%q detached=%v head=%q", res.Branch, res.Detached, res.Head)
	}
	if len(res.Commits) != 1 || res.Commits[0].Msg != "c1" {
		t.Errorf("detached history = %+v", res.Commits)
	}
}

func TestScanDirtyFiles(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "mod.txt", "one\n")
	write(t, dir, "del.txt", "gone\n")
	write(t, dir, "ren-old.txt", lines(15, "renamed"))
	commit(t, dir, "2026-09-30T10:00:00+05:30", "c1")

	write(t, dir, "mod.txt", "one\ntwo\n")                           // M
	if err := os.Remove(filepath.Join(dir, "del.txt")); err != nil { // D
		t.Fatal(err)
	}
	write(t, dir, "new.txt", "untracked\n") // ?
	write(t, dir, "added.txt", "staged\n")  // A
	gitDo(t, dir, "2026-09-30T10:00:00+05:30", "add", "added.txt")
	gitDo(t, dir, "2026-09-30T10:00:00+05:30", "mv", "ren-old.txt", "ren-new.txt") // R

	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range res.Dirty {
		got[d.Path] = d.State
	}
	want := map[string]string{
		"mod.txt": "M", "del.txt": "D", "new.txt": "?", "added.txt": "A", "ren-new.txt": "R",
	}
	if len(got) != len(want) {
		t.Errorf("dirty = %v, want %v", got, want)
	}
	for p, s := range want {
		if got[p] != s {
			t.Errorf("dirty[%s] = %q, want %q (all: %v)", p, got[p], s, got)
		}
	}

	diff, cut, err := DirtyDiff(context.Background(), proj(dir))
	if err != nil || cut {
		t.Fatalf("DirtyDiff err=%v cut=%v", err, cut)
	}
	for _, s := range []string{"+two", "-gone", "+staged"} {
		if !strings.Contains(diff, s) {
			t.Errorf("dirty diff misses %q", s)
		}
	}
}

func TestScanNonRepoAndMissingDir(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	res, err := Scan(context.Background(), proj(dir))
	if err != nil || res.IsRepo || res.Unavailable != nil || len(res.Commits) != 0 {
		t.Errorf("non-repo: %+v, %v", res, err)
	}
	res, err = Scan(context.Background(), proj(filepath.Join(dir, "does-not-exist")))
	if err != nil || res.IsRepo {
		t.Errorf("missing dir: %+v, %v", res, err)
	}
	if k := CacheKey(proj(dir)); k != "" {
		t.Errorf("CacheKey of a non-repo = %q", k)
	}
	if _, err := Commits(context.Background(), proj(dir), 0, 5, ""); !errors.Is(err, ErrNotRepo) {
		t.Errorf("Commits on non-repo: %v", err)
	}
	if _, _, err := DirtyDiff(context.Background(), proj(dir)); !errors.Is(err, ErrNotRepo) {
		t.Errorf("DirtyDiff on non-repo: %v", err)
	}
}

// A project with a broken .git folder inside a bigger repo must not report the
// bigger repo.
func TestBrokenDotGitDoesNotLeakParentRepo(t *testing.T) {
	isolate(t)
	outer := newRepo(t)
	write(t, outer, "outer.txt", "x\n")
	commit(t, outer, "2026-09-30T10:00:00+05:30", "outer commit")
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Scan(context.Background(), proj(inner))
	if err == nil {
		t.Fatalf("expected an error for a broken .git, got %+v", res)
	}
	if len(res.Commits) != 0 {
		t.Errorf("parent repo commits leaked: %+v", res.Commits)
	}
}

func TestScanWorktree(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "a.txt", "a\n")
	commit(t, dir, "2026-09-30T10:00:00+05:30", "c1")
	wt := filepath.Join(t.TempDir(), "wt")
	gitDo(t, dir, "2026-09-30T10:00:00+05:30", "worktree", "add", "-q", "-b", "wt-branch", wt)

	if fi, err := os.Stat(filepath.Join(wt, ".git")); err != nil || fi.IsDir() {
		t.Fatalf("worktree .git should be a file: %v", err)
	}
	res, err := Scan(context.Background(), proj(wt))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsRepo || res.Branch != "wt-branch" || len(res.Commits) != 1 {
		t.Errorf("worktree result: %+v", res)
	}
	k1 := CacheKey(proj(wt))
	if k1 == "" || !strings.Contains(k1, "refs/heads/wt-branch") {
		t.Errorf("worktree CacheKey = %q", k1)
	}
	write(t, wt, "w.txt", "w\n")
	commit(t, wt, "2026-09-30T11:00:00+05:30", "wt commit")
	if k2 := CacheKey(proj(wt)); k2 == k1 {
		t.Error("CacheKey did not change after a commit in the worktree")
	}
}

func TestCacheKey(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "a.txt", "a\n")
	commit(t, dir, "2026-09-30T10:00:00+05:30", "c1")
	k1 := CacheKey(proj(dir))
	if k1 == "" || k1 != CacheKey(proj(dir)) {
		t.Fatalf("key empty or unstable: %q", k1)
	}
	// A commit moves the branch tip.
	write(t, dir, "a.txt", "a\nb\n")
	commit(t, dir, "2026-09-30T11:00:00+05:30", "c2")
	k2 := CacheKey(proj(dir))
	if k2 == k1 {
		t.Error("key unchanged after a commit")
	}
	// Staging changes the index.
	write(t, dir, "c.txt", "c\n")
	gitDo(t, dir, "2026-09-30T11:00:00+05:30", "add", "c.txt")
	k3 := CacheKey(proj(dir))
	if k3 == k2 {
		t.Error("key unchanged after git add")
	}
	// Packed refs: the tip must still be found.
	gitDo(t, dir, "2026-09-30T11:00:00+05:30", "pack-refs", "--all")
	if _, err := os.Stat(filepath.Join(dir, ".git", "refs", "heads", "main")); err == nil {
		t.Fatal("expected the loose ref to be packed")
	}
	k4 := CacheKey(proj(dir))
	head := strings.TrimSpace(gitDo(t, dir, "2026-09-30T11:00:00+05:30", "rev-parse", "HEAD"))
	if !strings.Contains(k4, head) {
		t.Errorf("packed ref tip %s not in key %q", head, k4)
	}
}

func TestCommitsPagingAndPath(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	for i := 1; i <= 6; i++ {
		write(t, dir, "a.txt", lines(i, "a"))
		if i%2 == 0 {
			write(t, dir, "even.txt", lines(i, "e"))
		}
		commit(t, dir, fmt.Sprintf("2026-09-30T1%d:00:00+05:30", i), fmt.Sprintf("commit %d", i))
	}
	ctx := context.Background()
	all, err := Commits(ctx, proj(dir), 0, 100, "")
	if err != nil || len(all) != 6 {
		t.Fatalf("all: %d, %v", len(all), err)
	}
	if all[0].Msg != "commit 6" || all[5].Msg != "commit 1" {
		t.Errorf("order wrong: %q ... %q", all[0].Msg, all[5].Msg)
	}
	page, err := Commits(ctx, proj(dir), 2, 2, "")
	if err != nil || len(page) != 2 || page[0].ID != all[2].ID || page[1].ID != all[3].ID {
		t.Errorf("page 2..4 wrong: %v %v", page, err)
	}
	past, err := Commits(ctx, proj(dir), 10, 5, "")
	if err != nil || len(past) != 0 {
		t.Errorf("past the end: %v %v", past, err)
	}
	hist, err := Commits(ctx, proj(dir), 0, 50, "even.txt")
	if err != nil || len(hist) != 3 {
		t.Fatalf("even.txt history: %d, %v", len(hist), err)
	}
	for _, c := range hist {
		if len(c.Files) != 1 || c.Files[0].Path != "even.txt" {
			t.Errorf("path history should list only the path: %+v", c.Files)
		}
	}
	// A deleted file still has history.
	if err := os.Remove(filepath.Join(dir, "even.txt")); err != nil {
		t.Fatal(err)
	}
	commit(t, dir, "2026-09-30T18:00:00+05:30", "remove even")
	hist, err = Commits(ctx, proj(dir), 0, 50, "even.txt")
	if err != nil || len(hist) != 4 {
		t.Errorf("deleted file history: %d, %v", len(hist), err)
	}
	// Bad paths.
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../x", ".."} {
		if _, err := Commits(ctx, proj(dir), 0, 5, bad); !errors.Is(err, ErrBadPath) {
			t.Errorf("path %q should be rejected, got %v", bad, err)
		}
	}
	// A path that merely looks like a pathspec magic is taken literally.
	if cs, err := Commits(ctx, proj(dir), 0, 5, ":(top)a.txt"); err != nil || len(cs) != 0 {
		t.Errorf("magic pathspec: %v %v", cs, err)
	}
	// limit and skip are clamped.
	if cs, err := Commits(ctx, proj(dir), -5, 0, ""); err != nil || len(cs) != 1 {
		t.Errorf("clamp: %d %v", len(cs), err)
	}
}

func TestDetail(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "a.txt", lines(5, "alpha"))
	commit(t, dir, "2026-09-30T10:00:00+05:30", "first")
	write(t, dir, "a.txt", lines(5, "alpha")+"new line\n")
	commit(t, dir, "2026-09-30T11:00:00+05:30", "second\n\nlong body\nmore body")
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	newest := res.Commits[0]
	for _, id := range []string{newest.ID, newest.ID[:7], newest.ID[:12]} {
		d, err := Detail(context.Background(), proj(dir), id)
		if err != nil {
			t.Fatalf("Detail(%s): %v", id, err)
		}
		if d.ID != newest.ID || d.Msg != "second" || d.Body != "long body\nmore body" {
			t.Errorf("detail meta wrong: %+v", d.Commit)
		}
		if d.Plus != 1 || d.NFiles != 1 || len(d.Files) != 1 {
			t.Errorf("detail numstat wrong: %+v", d.Commit)
		}
		if !strings.Contains(d.Patch, "diff --git a/a.txt b/a.txt") || !strings.Contains(d.Patch, "+new line") {
			t.Errorf("patch wrong:\n%s", d.Patch)
		}
		if d.PatchTruncated {
			t.Error("small patch should not be truncated")
		}
	}
	// A root commit has a patch too.
	d, err := Detail(context.Background(), proj(dir), res.Commits[1].ID)
	if err != nil || !strings.Contains(d.Patch, "+alpha line 0") {
		t.Errorf("root commit detail: %v\n%s", err, d.Patch)
	}
	// Unknown but well-formed id.
	if _, err := Detail(context.Background(), proj(dir), "deadbeefdeadbeef"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown sha: %v", err)
	}
}

func TestDetailRejectsBadShaBeforeRunning(t *testing.T) {
	// The project path does not exist. A bad sha must still give ErrBadRef,
	// which proves the check happens before git runs.
	p := proj(filepath.Join(t.TempDir(), "nope"))
	bad := []string{
		"", "abc", "abcdef", // too short (6)
		"ABCDEF1",                                   // upper case
		"--output=/tmp/x", "-1234567", "abc1234 --", // option-like or with a space
		"abc1234;rm", "../abcdef1", "abcdef1\n",
		strings.Repeat("a", 41), // too long
		"g123456",               // not hex
	}
	for _, s := range bad {
		if _, err := Detail(context.Background(), p, s); !errors.Is(err, ErrBadRef) {
			t.Errorf("sha %q: err = %v, want ErrBadRef", s, err)
		}
	}
}

func TestDetailPatchCap(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "small.txt", "x\n")
	commit(t, dir, "2026-09-30T10:00:00+05:30", "c1")
	write(t, dir, "big.txt", lines(40000, "padding to make this file big")) // about 1.2 MB
	commit(t, dir, "2026-09-30T11:00:00+05:30", "big add")
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Detail(context.Background(), proj(dir), res.Commits[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.PatchTruncated {
		t.Error("PatchTruncated should be true")
	}
	if len(d.Patch) > PatchCap || len(d.Patch) < PatchCap/2 {
		t.Errorf("patch length %d not near cap %d", len(d.Patch), PatchCap)
	}
	if !strings.HasSuffix(d.Patch, "\n") {
		t.Error("truncated patch should end on a whole line")
	}
	if d.Plus != 40000 {
		t.Errorf("numstat must still cover the whole commit: plus = %d", d.Plus)
	}

	// The dirty diff has the same cap.
	write(t, dir, "big.txt", lines(40000, "different padding text for the dirty diff"))
	diff, cut, err := DirtyDiff(context.Background(), proj(dir))
	if err != nil || !cut || len(diff) > PatchCap {
		t.Errorf("dirty diff cap: len=%d cut=%v err=%v", len(diff), cut, err)
	}
}

func TestScanManyCommitsCappedAt200(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "a.txt", "x\n")
	commit(t, dir, "2026-09-01T10:00:00+05:30", "c0")
	// 205 empty commits are cheap.
	for i := 1; i <= 205; i++ {
		gitDo(t, dir, fmt.Sprintf("2026-09-01T10:%02d:00+05:30", i%60), "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("e%d", i))
	}
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != MaxCommits {
		t.Errorf("got %d commits, want %d", len(res.Commits), MaxCommits)
	}
	more, err := Commits(context.Background(), proj(dir), MaxCommits, 50, "")
	if err != nil || len(more) != 6 {
		t.Errorf("paging past 200: %d %v", len(more), err)
	}
}

func TestMissingGitBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no git here
	if Available() {
		t.Fatal("Available should be false with an empty PATH")
	}
	res, err := Scan(context.Background(), proj(t.TempDir()))
	if err != nil {
		t.Fatalf("missing git must not be an error: %v", err)
	}
	if res.Unavailable == nil || res.Unavailable.Reason == "" || res.IsRepo {
		t.Errorf("want Unavailable, got %+v", res)
	}
}

func TestStripCreds(t *testing.T) {
	cases := map[string]string{
		"https://user:pw@github.com/a/b.git": "https://github.com/a/b.git",
		"https://token@github.com/a/b.git":   "https://github.com/a/b.git",
		"ssh://git@host.example/a/b":         "ssh://host.example/a/b",
		"git@github.com:a/b.git":             "git@github.com:a/b.git",
		"https://github.com/a/b.git":         "https://github.com/a/b.git",
		"/local/path/repo.git":               "/local/path/repo.git",
		"  https://x:y@h/z \n":               "https://h/z",
	}
	for in, want := range cases {
		if got := stripCreds(in); got != want {
			t.Errorf("stripCreds(%q) = %q, want %q", in, got, want)
		}
	}
}
