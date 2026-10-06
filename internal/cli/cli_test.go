package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/authored"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const (
	fixtureDir = "../../testdata/authored/project"
	goldenDir  = "../../testdata/authored/golden/cli"
)

var ist = time.FixedZone("IST", 5*3600+30*60)

func fixedNow() time.Time { return time.Date(2026, 10, 1, 10, 0, 0, 0, ist) }

// harness is a temp project (a copy of the sample data) plus a way to run the CLI in it.
type harness struct {
	t      *testing.T
	root   string
	home   string
	cwd    string
	author string
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	copyTree(t, fixtureDir, root)
	return &harness{t: t, root: root, home: home, cwd: root}
}

func (h *harness) env() Env {
	return Env{Cwd: h.cwd, Home: h.home, Author: h.author, Now: fixedNow}
}

type result struct {
	code        int
	out, errOut string
}

func (h *harness) run(args ...string) result {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb, h.env())
	return result{code, out.String(), errb.String()}
}

func (h *harness) write(rel, text string) {
	h.t.Helper()
	p := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) read(rel string) string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(rel)))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

// transcript collects commands, their output and file contents into one golden file.
type transcript struct {
	h *harness
	b strings.Builder
}

func (h *harness) transcript() *transcript { return &transcript{h: h} }

func (tr *transcript) clean(s string) string {
	s = strings.ReplaceAll(s, tr.h.root, "<root>")
	return strings.ReplaceAll(s, tr.h.home, "<home>")
}

func (tr *transcript) cmd(args ...string) result {
	r := tr.h.run(args...)
	tr.b.WriteString(tr.clean("$ drashta "+strings.Join(quoteAll(args), " ")) + "\n")
	tr.b.WriteString("exit: " + itoa(r.code) + "\n")
	tr.b.WriteString("--- stdout\n" + tr.clean(r.out))
	if r.errOut != "" {
		tr.b.WriteString("--- stderr\n" + tr.clean(r.errOut))
	}
	tr.b.WriteString("\n")
	return r
}

func (tr *transcript) file(rel string) {
	tr.b.WriteString("=== " + rel + "\n" + tr.h.read(rel) + "\n")
}

func (tr *transcript) golden(t *testing.T, name string) {
	t.Helper()
	path := filepath.Join(goldenDir, name+".golden")
	got := tr.b.String()
	if *update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run the test with -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("golden %s differs.\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			out[i] = a
		}
	}
	return out
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestGoldenTaskList(t *testing.T) {
	h := newHarness(t)
	tr := h.transcript()
	tr.cmd("task", "list")
	tr.cmd("task", "list", "--status", "review")
	tr.cmd("task", "list", "--feature", "F-1")
	tr.cmd("task", "list", "--status", "done", "--feature", "F-3")
	tr.cmd("task", "list", "--status", "banana")
	tr.cmd("task", "list", "extra")
	tr.golden(t, "task_list")
}

func TestTaskListJSON(t *testing.T) {
	h := newHarness(t)
	r := h.run("task", "list", "--json", "--status", "review")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var tasks []authored.Task
	if err := json.Unmarshal([]byte(r.out), &tasks); err != nil {
		t.Fatalf("%v\n%s", err, r.out)
	}
	if len(tasks) != 1 || tasks[0].ID != "T-2" || tasks[0].Rev == "" {
		t.Errorf("%+v", tasks)
	}
	r = h.run("task", "list", "--json", "--status", "backlog", "--feature", "F-3")
	if strings.TrimSpace(r.out) != "[]" {
		t.Errorf("empty list should be [], got %q", r.out)
	}
	r = h.run("task", "show", "T-2", "--json")
	var shown struct {
		Task      authored.Task       `json:"task"`
		Snapshots []authored.Snapshot `json:"snapshots"`
		Notes     []authored.Note     `json:"notes"`
	}
	if err := json.Unmarshal([]byte(r.out), &shown); err != nil || shown.Task.ID != "T-2" || len(shown.Snapshots) != 2 || len(shown.Notes) != 2 {
		t.Errorf("show --json: %v %+v", err, shown)
	}
}

func TestGoldenTaskShow(t *testing.T) {
	h := newHarness(t)
	tr := h.transcript()
	tr.cmd("task", "show", "T-2")
	tr.cmd("task", "show", "T-5")
	tr.cmd("task", "show", "T-1")
	tr.cmd("task", "show", "T-99")
	tr.cmd("task", "show", "../x")
	tr.cmd("task", "show")
	tr.golden(t, "task_show")
}

func TestGoldenTaskNew(t *testing.T) {
	h := newHarness(t)
	tr := h.transcript()
	tr.cmd("task", "new", "Add export to CSV", "--feature", "F-1", "--assignee", "asha",
		"--body", "Export the visible rows.", "--link", "commit:abc1234", "--link", "T-3")
	tr.file(".drashta/tasks/T-6.md")
	tr.cmd("task", "new", "Quiet one", "-q")
	tr.cmd("task", "new", "Words", "without", "quotes", "--status", "backlog")
	tr.cmd("task", "new", "--feature", "F-1")
	tr.cmd("task", "new", "Bad feature", "--feature", "F-9")
	tr.cmd("task", "new", "Bad status", "--status", "parked")
	tr.cmd("task", "new", "Bad link", "--link", "commit:xyz")
	tr.cmd("task", "new", "Missing link target", "--link", "T-99")
	tr.cmd("task", "new", "Claude wants done", "--status", "done")
	tr.cmd("task", "new", "User said done", "--status", "done", "--user-said-done")
	tr.cmd("task", "new", "-q", "--", "-starts with a dash")
	tr.cmd("task", "list")
	tr.golden(t, "task_new")
}

func TestGoldenTaskStatus(t *testing.T) {
	h := newHarness(t)
	tr := h.transcript()
	tr.cmd("task", "status", "T-3", "review")
	tr.file(".drashta/tasks/T-3.md")
	tr.cmd("task", "status", "T-3", "review")
	tr.cmd("task", "status", "T-3", "done")
	tr.cmd("task", "status", "T-3", "done", "--user-said-done")
	tr.cmd("task", "status", "T-4", "done", "--author", "alice")
	tr.cmd("task", "status", "T-4", "parked")
	tr.cmd("task", "status", "T-99", "todo")
	tr.cmd("task", "status", "T-4")
	tr.file(".drashta/tasks/T-4.md")
	tr.golden(t, "task_status")
}

func TestGoldenTaskLink(t *testing.T) {
	h := newHarness(t)
	tr := h.transcript()
	tr.cmd("task", "link", "T-3", "commit:abc1234")
	tr.cmd("task", "link", "T-3", "commit:abc1234")
	tr.cmd("task", "link", "T-3", "file:src/theme.ts#L10-L20")
	tr.cmd("task", "link", "T-3", "T-2")
	tr.cmd("task", "link", "T-3", "snapshot:T-2/v2")
	tr.cmd("task", "link", "T-3", "ledger:commit:bdcb99c")
	tr.file(".drashta/tasks/T-3.md")
	tr.cmd("task", "link", "T-3", "T-3")
	tr.cmd("task", "link", "T-3", "task:T-99")
	tr.cmd("task", "link", "T-3", "not a ref")
	tr.cmd("task", "link", "T-99", "commit:abc1234")
	tr.cmd("task", "link", "T-3")
	tr.golden(t, "task_link")
}

func TestGoldenNote(t *testing.T) {
	h := newHarness(t)
	h.write("src/ui/Badge.tsx", "import x from 'x';\n\nexport const Badge = () => {\n  const size = 8;\n  return null;\n};\n")
	tr := h.transcript()
	tr.cmd("note", "add", "T-3", "Which colour space do the tokens use?")
	tr.file(".drashta/notes/N-4.md")
	tr.cmd("note", "add", "snapshot:T-2/v2", "Pulse is too fast.", "--x", "0.5", "--y", "0.9")
	tr.file(".drashta/notes/N-5.md")
	tr.cmd("note", "add", "file:src/ui/Badge.tsx#L4", "Magic number.", "--reply-to", "N-2", "-q")
	tr.file(".drashta/notes/N-6.md")
	tr.cmd("note", "add", "image:shots/a.png#x=0.25,y=0.75", "Pin from the ref.")
	tr.file(".drashta/notes/N-7.md")
	tr.cmd("note", "add", "T-3")
	tr.cmd("note", "add", "T-99", "No such task.")
	tr.cmd("note", "add", "T-3", "Half a pin.", "--x", "0.5")
	tr.cmd("note", "add", "T-3", "Pin out of range.", "--x", "2", "--y", "0.5")
	tr.cmd("note", "add", "T-3", "Bad reply.", "--reply-to", "N-99")
	tr.cmd("note", "add", "bogus", "Bad ref.")
	tr.cmd("note", "list")
	tr.cmd("note", "list", "--all")
	tr.cmd("note", "list", "--ref", "T-3")
	tr.cmd("note", "list", "--ref", "snapshot:T-2/v1", "--all")
	tr.cmd("note", "resolve", "N-2")
	tr.file(".drashta/notes/N-2.md")
	tr.cmd("note", "resolve", "N-2")
	tr.cmd("note", "resolve", "N-99")
	tr.cmd("note", "resolve")
	tr.cmd("task", "show", "T-2")
	tr.golden(t, "note")
}

func TestGoldenSnap(t *testing.T) {
	h := newHarness(t)
	outside := filepath.Join(h.home, "shot.png")
	if err := os.WriteFile(outside, []byte("\x89PNG\r\n\x1a\nfrom outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.write("shots/dome.png", "\x89PNG\r\n\x1a\ninside")
	h.write("shots/notes.txt", "not an image")
	h.cwd = filepath.Join(h.root, "shots")
	if err := os.MkdirAll(h.cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	tr := h.transcript()
	// T-3 is in_progress: a new snapshot moves it to review.
	tr.cmd("snap", "add", "--task", "T-3", "--path", "dome.png", "--note", "First pass of the tokens.")
	tr.file(".drashta/snapshots/T-3/v1.md")
	tr.file(".drashta/tasks/T-3.md")
	// T-2 is in review: it stays, and the version continues after v2.
	tr.cmd("snap", "add", "--task", "T-2", "--image", outside, "--title", "Badge, third try", "-q")
	tr.file(".drashta/snapshots/T-2/v3.md")
	tr.cmd("snap", "list")
	tr.cmd("snap", "list", "--task", "T-3")
	tr.cmd("snap", "list", "--task", "T-5")
	// Mistakes.
	tr.cmd("snap", "add", "--task", "T-3", "--path", outside)
	tr.cmd("snap", "add", "--task", "T-3", "--image", "notes.txt")
	tr.cmd("snap", "add", "--task", "T-3", "--image", "missing.png")
	tr.cmd("snap", "add", "--task", "T-99", "--image", "dome.png")
	tr.cmd("snap", "add", "--task", "../x", "--image", "dome.png")
	tr.cmd("snap", "add", "--task", "T-3", "--url", "http://localhost:5173")
	tr.cmd("snap", "add", "--task", "T-3")
	tr.cmd("snap", "add", "--image", "dome.png")
	tr.cmd("snap", "add", "--task", "T-3", "--image", "dome.png", "--path", "dome.png")
	tr.golden(t, "snap")

	for _, rel := range []string{".drashta/snapshots/T-3/v1.png", ".drashta/snapshots/T-2/v3.png"} {
		if _, err := os.Stat(filepath.Join(h.root, rel)); err != nil {
			t.Errorf("image missing: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(h.root, ".drashta/snapshots/T-3/v2.png")); err == nil {
		t.Errorf("a failed call stored an image")
	}
}

func TestSnapRecordsGitHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := newHarness(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = h.root
		cmd.Env = append(os.Environ(), "HOME="+h.home, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "first")
	h.write("shot.png", "\x89PNG\r\n\x1a\nx")
	if r := h.run("snap", "add", "--task", "T-3", "--image", "shot.png"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	s, err := authored.LoadSnapshot(h.root, "T-3/v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commit) != 10 || strings.Trim(s.Commit, "0123456789abcdef") != "" {
		t.Errorf("commit = %q", s.Commit)
	}
}

func TestGoldenUsage(t *testing.T) {
	h := newHarness(t)
	tr := h.transcript()
	tr.cmd()
	tr.cmd("bogus")
	tr.cmd("help")
	tr.cmd("--help")
	tr.cmd("task")
	tr.cmd("task", "bogus")
	tr.cmd("task", "--help")
	tr.cmd("note")
	tr.cmd("snap", "bogus")
	tr.cmd("task", "list", "--nope")
	tr.cmd("task", "list", "-z")
	tr.cmd("task", "new", "x", "--feature")
	tr.cmd("task", "new", "x", "--project")
	tr.cmd("task", "list", "--json=yes")
	tr.cmd("task", "new", "x", "--author", "two words")
	tr.golden(t, "usage")
}

func TestFindProject(t *testing.T) {
	h := newHarness(t)

	// From a sub folder, the nearest .drashta above is used.
	sub := filepath.Join(h.root, "src", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	h.cwd = sub
	if r := h.run("task", "show", "T-1"); r.code != 0 || !strings.HasPrefix(r.out, "T-1  Cache layer") {
		t.Errorf("from a sub folder: %+v", r)
	}

	// -C and --project work before and after the command.
	h.cwd = t.TempDir()
	for _, args := range [][]string{
		{"-C", h.root, "task", "show", "T-1"},
		{"task", "-C", h.root, "show", "T-1"},
		{"task", "show", "T-1", "--project", h.root},
		{"task", "show", "T-1", "--project=" + h.root},
	} {
		if r := h.run(args...); r.code != 0 {
			t.Errorf("%v: %+v", args, r)
		}
	}
	// A relative -C is relative to the working folder.
	h.cwd = filepath.Dir(h.root)
	if r := h.run("-C", filepath.Base(h.root), "task", "show", "T-1"); r.code != 0 {
		t.Errorf("relative -C: %+v", r)
	}
	if r := h.run("-C", "no-such-folder", "task", "list"); r.code != 1 {
		t.Errorf("missing -C folder: %+v", r)
	}

	// Outside every project: a clear error, and nothing is created.
	empty := t.TempDir()
	h.cwd = empty
	r := h.run("task", "list")
	if r.code != 1 || !strings.Contains(r.errOut, "no project found") {
		t.Errorf("outside: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(empty, ".drashta")); err == nil {
		t.Errorf("a .drashta folder appeared in the wrong place")
	}
}

// ~/.drashta is drashta's own data folder. It must never be taken for a project.
func TestHomeDrashtaIsNotAProject(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Join(h.home, ".drashta"), 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(h.home, "elsewhere", "thing")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	h.cwd = work
	r := h.run("task", "new", "Should not land in home")
	if r.code != 1 || !strings.Contains(r.errOut, "no project found") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".drashta", "tasks")); err == nil {
		t.Errorf("wrote into ~/.drashta")
	}
}

func TestKnownProjectRootCreatesDrashta(t *testing.T) {
	h := newHarness(t)
	projects := filepath.Join(h.home, "projects")
	proj := filepath.Join(projects, "personal", "lantern")
	deep := filepath.Join(proj, "src", "ui")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	h.cwd = deep
	env := h.env()
	env.ProjectsDir = projects
	var out, errb bytes.Buffer
	code := Run([]string{"task", "new", "First task here", "-q"}, &out, &errb, env)
	if code != 0 || strings.TrimSpace(out.String()) != "T-1" {
		t.Fatalf("code %d out %q err %q", code, out.String(), errb.String())
	}
	for _, f := range []string{"tasks/T-1.md", "README.md", ".gitignore", ".lock"} {
		if _, err := os.Stat(filepath.Join(proj, ".drashta", f)); err != nil {
			t.Errorf("missing .drashta/%s: %v", f, err)
		}
	}
	// A folder at group level is not a project.
	h.cwd = filepath.Join(projects, "personal")
	env = h.env()
	env.ProjectsDir = projects
	errb.Reset()
	if code := Run([]string{"task", "list"}, &out, &errb, env); code != 1 {
		t.Errorf("group folder: code %d", code)
	}
}

func TestAuthor(t *testing.T) {
	h := newHarness(t)
	// Default: Claude.
	if r := h.run("note", "add", "T-3", "hello", "-q"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if n, _ := authored.LoadNote(h.root, "N-4"); n == nil || n.Author != "Claude" {
		t.Errorf("default author: %+v", n)
	}
	// The environment value.
	h.author = "session:121c3b19"
	h.run("note", "add", "T-3", "hello again", "-q")
	if n, _ := authored.LoadNote(h.root, "N-5"); n == nil || n.Author != "session:121c3b19" {
		t.Errorf("env author: %+v", n)
	}
	// The option wins, and alice may set done without the flag.
	if r := h.run("task", "status", "T-3", "done", "--author", "alice"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	task, _ := authored.LoadTask(h.root, "T-3")
	if last := task.Log[len(task.Log)-1]; last.By != "alice" || last.Text != "status in_progress -> done" || last.Time != "2026-10-01T10:00+05:30" {
		t.Errorf("log: %+v", last)
	}
	// A session author is held to the same rule as Claude.
	if r := h.run("task", "status", "T-4", "done"); r.code != 1 {
		t.Errorf("session may not set done: %+v", r)
	}
}

// Several CLI runs at the same time (for example two Claude sessions) must never share an id
// and never lose a log line. No server is running.
func TestParallelRuns(t *testing.T) {
	h := newHarness(t)
	const n = 12
	var wg sync.WaitGroup
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out, errb bytes.Buffer
			if code := Run([]string{"task", "new", "Parallel " + itoa(i), "-q"}, &out, &errb, h.env()); code != 0 {
				t.Errorf("new: %d %s", code, errb.String())
			}
			ids <- strings.TrimSpace(out.String())
			out.Reset()
			if code := Run([]string{"task", "link", "T-3", "commit:" + fmt.Sprintf("%07x", i+1)}, &out, &errb, h.env()); code != 0 {
				t.Errorf("link: %d %s", code, errb.String())
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("id %s given twice", id)
		}
		seen[id] = true
	}
	task, _ := authored.LoadTask(h.root, "T-3")
	if len(task.Links) != n || len(task.Log) != n {
		t.Errorf("links %d, log %d, want %d each", len(task.Links), len(task.Log), n)
	}
}
