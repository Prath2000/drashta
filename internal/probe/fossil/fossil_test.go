package fossil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// isolate points HOME and fossil's config at empty places and sets the login
// name, so the real user config never changes what a test sees. It skips the
// test when fossil is missing.
func isolate(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("fossil is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FOSSIL_HOME", home)
	t.Setenv("USER", "tester")
	t.Setenv("LOGNAME", "tester")
}

func fossilDo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("fossil", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("fossil %s: %v\n%s%s", strings.Join(args, " "), err, out.String(), errb.String())
	}
	return out.String()
}

// newCheckout makes a repository dated 2026-09-01 and opens a checkout of it.
// The repository file lives outside the checkout.
func newCheckout(t *testing.T) (dir, repo string) {
	t.Helper()
	base := t.TempDir()
	repo = filepath.Join(base, "repo.fossil")
	dir = filepath.Join(base, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fossilDo(t, base, "new", "--admin-user", "tester", "--date-override", "2026-09-01T00:00:00", repo)
	fossilDo(t, dir, "open", repo)
	return dir, repo
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

// commit adds every new file and commits at the given UTC time.
func commit(t *testing.T, dir, when, msg string) {
	t.Helper()
	fossilDo(t, dir, "addremove")
	fossilDo(t, dir, "commit", "-m", msg, "--date-override", when, "--no-warnings", "--no-prompt")
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

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	if Detect(dir) {
		t.Error("empty dir is not a checkout")
	}
	if err := os.WriteFile(filepath.Join(dir, ".fslckout"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !Detect(dir) {
		t.Error(".fslckout should be detected")
	}
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "_FOSSIL_"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !Detect(dir2) {
		t.Error("_FOSSIL_ should be detected")
	}
}

func TestScanNormalCheckout(t *testing.T) {
	isolate(t)
	dir, repo := newCheckout(t)
	write(t, dir, "a.txt", lines(3, "alpha"))
	commit(t, dir, "2026-09-30T18:00:00", "first commit\n\nwith a second paragraph")
	write(t, dir, "a.txt", lines(3, "alpha")+"added\n")
	write(t, dir, "sub/b.txt", "hello\n")
	commit(t, dir, "2026-10-01T02:30:00", "second")

	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsRepo || res.Unavailable != nil {
		t.Fatalf("result: %+v", res)
	}
	if res.Branch != "trunk" {
		t.Errorf("branch = %q", res.Branch)
	}
	if res.Remote != "" {
		t.Errorf("remote = %q, want empty (sync is off)", res.Remote)
	}
	wantRepo, _ := filepath.EvalSymlinks(repo)
	gotRepo, _ := filepath.EvalSymlinks(res.Repo)
	if gotRepo != wantRepo {
		t.Errorf("repo = %q, want %q", res.Repo, repo)
	}
	if len(res.Commits) != 3 { // two commits plus fossil's own first check-in
		t.Fatalf("got %d commits: %+v", len(res.Commits), res.Commits)
	}
	c0, c1, root := res.Commits[0], res.Commits[1], res.Commits[2]
	if c0.ID != res.Head || len(c0.ID) < 40 || c0.VCS != "fossil" {
		t.Errorf("head/id/vcs: head=%q c0=%+v", res.Head, c0)
	}
	if c0.Msg != "second" || c0.Author != "tester" {
		t.Errorf("c0 msg/author = %q / %q", c0.Msg, c0.Author)
	}
	if want := time.Date(2026, 10, 1, 2, 30, 0, 0, time.UTC); !c0.Time.Equal(want) {
		t.Errorf("c0 time = %v, want %v", c0.Time, want)
	}
	if c0.NFiles != 2 || c0.Plus != 2 || c0.Minus != 0 || len(c0.Files) != 2 {
		t.Errorf("c0 counts: %+v", c0)
	}
	if c1.Msg != "first commit with a second paragraph" || c1.Plus != 3 || c1.NFiles != 1 {
		t.Errorf("c1: %+v", c1)
	}
	if root.NFiles != 0 || root.Plus != 0 {
		t.Errorf("first check-in has no changes: %+v", root)
	}
	if len(res.Dirty) != 0 {
		t.Errorf("clean checkout reported dirty files: %+v", res.Dirty)
	}
}

func TestScanNotACheckout(t *testing.T) {
	isolate(t)
	res, err := Scan(context.Background(), proj(t.TempDir()))
	if err != nil || res.IsRepo || res.Unavailable != nil {
		t.Errorf("non-checkout: %+v %v", res, err)
	}
	if _, err := Commits(context.Background(), proj(t.TempDir()), 0, 5, ""); !errors.Is(err, ErrNotCheckout) {
		t.Errorf("Commits: %v", err)
	}
	if _, _, err := DirtyDiff(context.Background(), proj(t.TempDir())); !errors.Is(err, ErrNotCheckout) {
		t.Errorf("DirtyDiff: %v", err)
	}
	if k := CacheKey(proj(t.TempDir()), ""); k != "" {
		t.Errorf("CacheKey = %q", k)
	}
}

func TestScanDirtyFiles(t *testing.T) {
	isolate(t)
	dir, _ := newCheckout(t)
	write(t, dir, "edit.txt", "one\n")
	write(t, dir, "rm.txt", "gone\n")
	write(t, dir, "missing.txt", "lost\n")
	write(t, dir, "old-name.txt", lines(5, "x"))
	commit(t, dir, "2026-09-30T18:00:00", "c1")

	write(t, dir, "edit.txt", "one\nmodified line\n")                    // EDITED
	fossilDo(t, dir, "rm", "rm.txt")                                     // DELETED
	if err := os.Remove(filepath.Join(dir, "missing.txt")); err != nil { // MISSING
		t.Fatal(err)
	}
	fossilDo(t, dir, "mv", "--hard", "old-name.txt", "new-name.txt") // RENAMED
	write(t, dir, "added.txt", "brand new\n")
	fossilDo(t, dir, "add", "added.txt")  // ADDED
	write(t, dir, "untracked.txt", "x\n") // not listed

	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range res.Dirty {
		got[d.Path] = d.State
	}
	want := map[string]string{
		"edit.txt": "M", "rm.txt": "D", "missing.txt": "D", "new-name.txt": "R", "added.txt": "A",
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
	for _, s := range []string{"+modified line", "+brand new"} {
		if !strings.Contains(diff, s) {
			t.Errorf("dirty diff misses %q:\n%s", s, diff)
		}
	}
}

func TestDetail(t *testing.T) {
	isolate(t)
	dir, _ := newCheckout(t)
	write(t, dir, "a.txt", lines(3, "alpha"))
	commit(t, dir, "2026-09-30T18:00:00", "first")
	write(t, dir, "a.txt", lines(3, "alpha")+"new line\n")
	write(t, dir, "b.txt", "hi\n")
	commit(t, dir, "2026-10-01T02:30:00", "second")

	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	newest, first, root := res.Commits[0], res.Commits[1], res.Commits[2]
	for _, h := range []string{newest.ID, newest.ID[:10], newest.ID[:6]} {
		d, err := Detail(context.Background(), proj(dir), h)
		if err != nil {
			t.Fatalf("Detail(%s): %v", h, err)
		}
		if d.ID != newest.ID || d.Msg != "second" || d.VCS != "fossil" || d.Author != "tester" {
			t.Errorf("meta: %+v", d.Commit)
		}
		if d.Plus != 2 || d.NFiles != 2 || len(d.Files) != 2 {
			t.Errorf("counts: %+v", d.Commit)
		}
		if !strings.Contains(d.Patch, "+new line") || !strings.Contains(d.Patch, "+hi") {
			t.Errorf("patch:\n%s", d.Patch)
		}
		if d.PatchTruncated {
			t.Error("small patch should not be truncated")
		}
	}
	d, err := Detail(context.Background(), proj(dir), first.ID)
	if err != nil || !strings.Contains(d.Patch, "+alpha line 0") {
		t.Errorf("first commit detail: %v\n%s", err, d.Patch)
	}
	// fossil's own first check-in has no parent: empty patch, no error.
	d, err = Detail(context.Background(), proj(dir), root.ID)
	if err != nil || d.Patch != "" || d.ID != root.ID {
		t.Errorf("root detail: %+v err=%v", d, err)
	}
	// Well-formed but unknown.
	if _, err := Detail(context.Background(), proj(dir), "abcdef123456"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown hash: %v", err)
	}
	// All digits: fossil would read this as a date, but it is no hash prefix.
	if _, err := Detail(context.Background(), proj(dir), "20260930"); !errors.Is(err, ErrNotFound) {
		t.Errorf("date-like hash: %v", err)
	}
}

func TestDetailRejectsBadHashBeforeRunning(t *testing.T) {
	// The project path does not exist. A bad hash must still give ErrBadRef,
	// which proves the check happens before fossil runs.
	p := proj(filepath.Join(t.TempDir(), "nope"))
	bad := []string{
		"", "abc", "abcde", "ABCDEF1",
		"--output=/tmp/x", "-123456", "abc123 --", "abc123;rm", "../abcdef", "abcdef\n",
		strings.Repeat("a", 65), "g12345",
	}
	for _, h := range bad {
		if _, err := Detail(context.Background(), p, h); !errors.Is(err, ErrBadRef) {
			t.Errorf("hash %q: err = %v, want ErrBadRef", h, err)
		}
	}
}

func TestDetailPatchCap(t *testing.T) {
	isolate(t)
	dir, _ := newCheckout(t)
	write(t, dir, "small.txt", "x\n")
	commit(t, dir, "2026-09-30T18:00:00", "c1")
	write(t, dir, "big.txt", lines(40000, "padding to make this file big"))
	commit(t, dir, "2026-10-01T02:30:00", "big add")
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Detail(context.Background(), proj(dir), res.Commits[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.PatchTruncated || len(d.Patch) > PatchCap || len(d.Patch) < PatchCap/2 || !strings.HasSuffix(d.Patch, "\n") {
		t.Errorf("patch cap: len=%d truncated=%v", len(d.Patch), d.PatchTruncated)
	}
	if d.Plus != 40000 {
		t.Errorf("numstat must cover the whole check-in: plus=%d", d.Plus)
	}
	write(t, dir, "big.txt", lines(40000, "different padding text for the dirty diff"))
	diff, cut, err := DirtyDiff(context.Background(), proj(dir))
	if err != nil || !cut || len(diff) > PatchCap {
		t.Errorf("dirty diff cap: len=%d cut=%v err=%v", len(diff), cut, err)
	}
}

func TestCommitsPagingAndPath(t *testing.T) {
	isolate(t)
	dir, _ := newCheckout(t)
	for i := 1; i <= 6; i++ {
		write(t, dir, "a.txt", lines(i, "a"))
		if i%2 == 0 {
			write(t, dir, "even.txt", lines(i, "e"))
		}
		commit(t, dir, fmt.Sprintf("2026-09-30T1%d:00:00", i), fmt.Sprintf("commit %d", i))
	}
	ctx := context.Background()
	all, err := Commits(ctx, proj(dir), 0, 100, "")
	if err != nil || len(all) != 7 { // 6 plus fossil's first check-in
		t.Fatalf("all: %d, %v", len(all), err)
	}
	if all[0].Msg != "commit 6" || all[5].Msg != "commit 1" {
		t.Errorf("order: %q ... %q", all[0].Msg, all[5].Msg)
	}
	page, err := Commits(ctx, proj(dir), 2, 2, "")
	if err != nil || len(page) != 2 || page[0].ID != all[2].ID || page[1].ID != all[3].ID {
		t.Errorf("page: %v %v", page, err)
	}
	if past, err := Commits(ctx, proj(dir), 50, 5, ""); err != nil || len(past) != 0 {
		t.Errorf("past the end: %v %v", past, err)
	}
	hist, err := Commits(ctx, proj(dir), 0, 50, "even.txt")
	if err != nil || len(hist) != 3 {
		t.Fatalf("even.txt history: %d, %v", len(hist), err)
	}
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../x", ".."} {
		if _, err := Commits(ctx, proj(dir), 0, 5, bad); !errors.Is(err, ErrBadPath) {
			t.Errorf("path %q should be rejected, got %v", bad, err)
		}
	}
	if cs, err := Commits(ctx, proj(dir), -3, 0, ""); err != nil || len(cs) != 1 {
		t.Errorf("clamp: %d %v", len(cs), err)
	}
}

func TestStatBudgetFillsOverSeveralScans(t *testing.T) {
	isolate(t)
	old := statBudget
	statBudget = 3
	t.Cleanup(func() { statBudget = old })

	dir, _ := newCheckout(t)
	for i := 1; i <= 8; i++ {
		write(t, dir, "a.txt", lines(i, "a"))
		commit(t, dir, fmt.Sprintf("2026-09-30T0%d:00:00", i), fmt.Sprintf("c%d", i))
	}
	counted := func() (n int, newestOK bool) {
		res, err := Scan(context.Background(), proj(dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range res.Commits {
			if c.NFiles > 0 {
				n++
			}
		}
		return n, res.Commits[0].NFiles == 1
	}
	if n, ok := counted(); n != 3 || !ok {
		t.Errorf("scan 1: %d check-ins with counts, newest ok=%v (want 3, true)", n, ok)
	}
	if n, _ := counted(); n != 6 {
		t.Errorf("scan 2: %d check-ins with counts, want 6", n)
	}
	if n, _ := counted(); n != 8 {
		t.Errorf("scan 3: %d check-ins with counts, want 8", n)
	}
	// Once known, the counts are stable and cost no lookups.
	if n, _ := counted(); n != 8 {
		t.Errorf("scan 4: %d", n)
	}
}

func TestCacheKey(t *testing.T) {
	isolate(t)
	dir, repo := newCheckout(t)
	write(t, dir, "a.txt", "a\n")
	commit(t, dir, "2026-09-30T18:00:00", "c1")

	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	k1 := CacheKey(proj(dir), res.Repo)
	if k1 == "" || !strings.Contains(k1, "repo=") {
		t.Fatalf("key = %q", k1)
	}
	// Scanning again (read-only commands) must not change the key.
	if _, err := Scan(context.Background(), proj(dir)); err != nil {
		t.Fatal(err)
	}
	if k := CacheKey(proj(dir), res.Repo); k != k1 {
		t.Errorf("a scan changed the key:\n%s\n%s", k1, k)
	}
	// A commit changes the checkout database and the repository.
	time.Sleep(10 * time.Millisecond)
	write(t, dir, "a.txt", "a\nb\n")
	commit(t, dir, "2026-10-01T02:30:00", "c2")
	if k := CacheKey(proj(dir), res.Repo); k == k1 {
		t.Error("key unchanged after a commit")
	}
	if k := CacheKey(proj(dir), ""); k == "" || strings.Contains(k, "repo=") {
		t.Errorf("key without a repo path = %q", k)
	}
	_ = repo
}

func TestMissingFossilBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if Available() {
		t.Fatal("Available should be false with an empty PATH")
	}
	res, err := Scan(context.Background(), proj(t.TempDir()))
	if err != nil {
		t.Fatalf("missing fossil must not be an error: %v", err)
	}
	if res.Unavailable == nil || res.Unavailable.Reason == "" || res.IsRepo {
		t.Errorf("want Unavailable, got %+v", res)
	}
}

// ---- per-project lock ------------------------------------------------------

func TestPerProjectLockSerializesOneProject(t *testing.T) {
	var active, most int32
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := withLock(context.Background(), "/p/one", func() error {
				n := atomic.AddInt32(&active, 1)
				for {
					m := atomic.LoadInt32(&most)
					if n <= m || atomic.CompareAndSwapInt32(&most, m, n) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&active, -1)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if most != 1 {
		t.Errorf("up to %d calls ran at once in one project, want 1", most)
	}
}

func TestPerProjectLockAllowsOtherProjects(t *testing.T) {
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = withLock(context.Background(), "/p/a", func() error {
			close(held)
			<-release
			return nil
		})
		close(done)
	}()
	<-held
	ok := make(chan struct{})
	go func() {
		_ = withLock(context.Background(), "/p/b", func() error { close(ok); return nil })
	}()
	select {
	case <-ok:
	case <-time.After(2 * time.Second):
		t.Error("project b was blocked by project a")
	}
	close(release)
	<-done
}

func TestPerProjectLockHonoursContext(t *testing.T) {
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = withLock(context.Background(), "/p/ctx", func() error {
			close(held)
			<-release
			return nil
		})
		close(done)
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	called := false
	err := withLock(ctx, "/p/ctx", func() error { called = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Errorf("err=%v called=%v, want deadline exceeded and no call", err, called)
	}
	close(release)
	<-done
}

// A real Scan must wait for the project lock, and many Scans at once must all work.
func TestScanUsesTheProjectLock(t *testing.T) {
	isolate(t)
	dir, _ := newCheckout(t)
	write(t, dir, "a.txt", "a\n")
	commit(t, dir, "2026-09-30T18:00:00", "c1")

	held := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan struct{})
	go func() {
		_ = withLock(context.Background(), filepath.Clean(dir), func() error {
			close(held)
			<-release
			return nil
		})
		close(lockDone)
	}()
	<-held
	scanDone := make(chan error, 1)
	go func() {
		_, err := Scan(context.Background(), proj(dir))
		scanDone <- err
	}()
	select {
	case <-scanDone:
		t.Fatal("Scan finished while the project lock was held")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	<-lockDone
	select {
	case err := <-scanDone:
		if err != nil {
			t.Errorf("scan after release: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Scan did not finish after the lock was released")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := Scan(context.Background(), proj(dir))
			if err == nil && len(res.Commits) != 2 {
				err = fmt.Errorf("got %d commits", len(res.Commits))
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent scan: %v", err)
		}
	}
	if n := len(lockFor(filepath.Clean(dir))); n != 0 {
		t.Errorf("lock slot still taken after all scans: %d", n)
	}
}

func TestStripCredsAndParse(t *testing.T) {
	if got := stripCreds("https://me:pw@host.example/repo"); got != "https://host.example/repo" {
		t.Errorf("stripCreds = %q", got)
	}
	files := parseNumstat([]byte("  INSERTED    DELETED\n         1          0 a b.txt\n         2          3 dir/c.txt\n         3          3 TOTAL over 2 changed files\n"))
	if len(files) != 2 || files[0].Path != "a b.txt" || files[1].Minus != 3 {
		t.Errorf("parseNumstat = %+v", files)
	}
	dirty, _ := parseChanges([]byte("EDITED     a.txt\nRENAMED    old.txt  ->  new.txt\nMERGED_WITH 1234abcd\nCONFLICT   c.txt\nweird line\n"))
	if len(dirty) != 3 || dirty[1].Path != "new.txt" || dirty[1].State != "R" || dirty[2].State != "U" {
		t.Errorf("parseChanges = %+v", dirty)
	}
	cs := parseTimeline([]byte("+++ header\n" + strings.Repeat("a", 40) + "\x1fme\x1f2026-10-01 01:02:03\x1ffix  thing\n"))
	if len(cs) != 1 || cs[0].Msg != "fix thing" || cs[0].Time.Hour() != 1 || cs[0].Time.Location() != time.UTC {
		t.Errorf("parseTimeline = %+v", cs)
	}
}

func TestRemoteHasNoCredentials(t *testing.T) {
	isolate(t)
	dir, _ := newCheckout(t)
	fossilDo(t, dir, "remote", "https://me:pw@example.invalid/repo")
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Remote != "https://example.invalid/repo" {
		t.Errorf("remote = %q", res.Remote)
	}
}

// `fossil info` cuts the checkout hash to 40 characters. Head must be the full hash,
// also when the checked-out version is not the newest.
func TestHeadIsFullHashForOlderCheckout(t *testing.T) {
	isolate(t)
	dir, _ := newCheckout(t)
	write(t, dir, "a.txt", "a\n")
	commit(t, dir, "2026-09-30T18:00:00", "c1")
	write(t, dir, "a.txt", "a\nb\n")
	commit(t, dir, "2026-10-01T02:30:00", "c2")
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	older := res.Commits[1] // c1
	fossilDo(t, dir, "update", older.ID[:12])

	res, err = Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Head != older.ID || len(res.Head) < 64 {
		t.Errorf("head = %q, want %q", res.Head, older.ID)
	}
	// Without the commit list, fullHash asks fossil.
	got, err := fullHash(context.Background(), proj(dir), older.ID[:40], nil)
	if err != nil || got != older.ID {
		t.Errorf("fullHash = %q, %v", got, err)
	}
	if got, _ := fullHash(context.Background(), proj(dir), "", nil); got != "" {
		t.Errorf("fullHash of empty = %q", got)
	}
}
