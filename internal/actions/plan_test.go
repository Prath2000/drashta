package actions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fix struct {
	cfg     Config
	root    string // project root, unresolved
	realDir string // project root, symlinks resolved
	outside string // a real file outside every root
}

func newFix(t *testing.T) fix {
	t.Helper()
	base := t.TempDir()
	projects := filepath.Join(base, "projects")
	root := filepath.Join(projects, "g", "p")
	for _, d := range []string{"src", "shots", ".drashta/tasks", ".drashta/notes", "docs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"src/x.go", "shots/a.png", ".drashta/tasks/T-1.md", ".drashta/notes/N-1.md", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(outDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "src", "escape.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outDir, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return fix{
		cfg:     Config{Editor: []string{"code", "-g", "{path}:{line}"}, Terminal: "Terminal", Roots: []string{projects}},
		root:    root,
		realDir: real,
		outside: outside,
	}
}

func mustPlan(t *testing.T, c Config, tg Target, action string) Command {
	t.Helper()
	cmd, err := c.Plan(tg, action)
	if err != nil {
		t.Fatalf("Plan(%+v, %q): %v", tg, action, err)
	}
	return cmd
}

func wantCmd(t *testing.T, got Command, name string, args ...string) {
	t.Helper()
	if got.Name != name || !reflect.DeepEqual(got.Args, args) {
		t.Fatalf("got %s %q\nwant %s %q", got.Name, got.Args, name, args)
	}
}

// termArgv returns the argv after the fixed script ("--" and what follows).
func termArgv(t *testing.T, c Command) []string {
	t.Helper()
	if c.Name != "osascript" {
		t.Fatalf("want osascript, got %s", c.Name)
	}
	for i := 0; i+1 < len(c.Args); i += 2 {
		if c.Args[i] != "-e" {
			if c.Args[i] != "--" {
				t.Fatalf("unexpected osascript arg %q", c.Args[i])
			}
			script := []string{}
			for j := 1; j < i; j += 2 {
				script = append(script, c.Args[j])
			}
			if !reflect.DeepEqual(script, terminalScript) {
				t.Fatalf("script changed: %q", script)
			}
			return c.Args[i+1:]
		}
	}
	t.Fatalf("no -- in %q", c.Args)
	return nil
}

// Row: file -> <editor> -g <path>:<line>
func TestPlanFileEditor(t *testing.T) {
	f := newFix(t)
	cmd := mustPlan(t, f.cfg, Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go", Line: 40}, "")
	wantCmd(t, cmd, "code", "-g", filepath.Join(f.realDir, "src/x.go")+":40")
	cmd = mustPlan(t, f.cfg, Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, ActionEditor)
	wantCmd(t, cmd, "code", "-g", filepath.Join(f.realDir, "src/x.go")+":1")
	// Custom template without {path}: path is appended.
	c := f.cfg
	c.Editor = []string{"zed"}
	cmd = mustPlan(t, c, Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, "")
	wantCmd(t, cmd, "zed", filepath.Join(f.realDir, "src/x.go"))
	// Default template when config is empty.
	c.Editor = nil
	cmd = mustPlan(t, c, Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go", Line: 2}, "")
	wantCmd(t, cmd, "code", "-g", filepath.Join(f.realDir, "src/x.go")+":2")
	// Bad template.
	c.Editor = []string{"-x"}
	if _, err := c.Plan(Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad editor template: %v", err)
	}
	// File reveal in Finder.
	cmd = mustPlan(t, f.cfg, Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, ActionFinder)
	wantCmd(t, cmd, "open", "-R", filepath.Join(f.realDir, "src/x.go"))
	// File in its default app.
	cmd = mustPlan(t, f.cfg, Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, ActionApp)
	wantCmd(t, cmd, "open", "--", filepath.Join(f.realDir, "src/x.go"))
	if _, err := f.cfg.Plan(Target{Type: TypeFile, ProjectRoot: f.root, Path: "../x.go"}, ActionApp); !errors.Is(err, ErrForbidden) {
		t.Fatalf("app with ..: %v", err)
	}
}

// Row: folder / project -> open -R <path>, open -a <terminal> <path>
func TestPlanFolderProject(t *testing.T) {
	f := newFix(t)
	cmd := mustPlan(t, f.cfg, Target{Type: TypeFolder, ProjectRoot: f.root, Path: "docs"}, "")
	wantCmd(t, cmd, "open", "-R", filepath.Join(f.realDir, "docs"))
	cmd = mustPlan(t, f.cfg, Target{Type: TypeProject, ProjectRoot: f.root}, ActionFinder)
	wantCmd(t, cmd, "open", "-R", f.realDir)
	cmd = mustPlan(t, f.cfg, Target{Type: TypeFolder, ProjectRoot: f.root, Path: "docs"}, ActionApp)
	wantCmd(t, cmd, "open", "--", filepath.Join(f.realDir, "docs"))
	cmd = mustPlan(t, f.cfg, Target{Type: TypeProject, ProjectRoot: f.root}, ActionTerminal)
	wantCmd(t, cmd, "open", "-a", "Terminal", f.realDir)
	c := f.cfg
	c.Terminal = "Ghostty"
	cmd = mustPlan(t, c, Target{Type: TypeFolder, ProjectRoot: f.root, Path: "src"}, ActionTerminal)
	wantCmd(t, cmd, "open", "-a", "Ghostty", filepath.Join(f.realDir, "src"))
	c.Terminal = "-n"
	if _, err := c.Plan(Target{Type: TypeProject, ProjectRoot: f.root}, ActionTerminal); err == nil {
		t.Fatal("terminal app starting with - must fail")
	}
}

// Row: image / snapshot -> open -a Preview <path>
func TestPlanImageSnapshot(t *testing.T) {
	f := newFix(t)
	for _, typ := range []string{TypeImage, TypeSnapshot} {
		cmd := mustPlan(t, f.cfg, Target{Type: typ, ProjectRoot: f.root, Path: "shots/a.png"}, "")
		wantCmd(t, cmd, "open", "-a", "Preview", filepath.Join(f.realDir, "shots/a.png"))
	}
}

// Row: commit (git) -> terminal running a hardened git show (review M-7)
func TestPlanCommitGit(t *testing.T) {
	f := newFix(t)
	cmd := mustPlan(t, f.cfg, Target{Type: TypeCommit, ProjectRoot: f.root, VCS: "git", SHA: "abc1234"}, "")
	got := termArgv(t, cmd)
	want := []string{f.realDir, "git", "-c", "core.pager=less", "-c", "core.fsmonitor=false",
		"-C", f.realDir, "show", "--no-ext-diff", "--no-textconv", "abc1234", "--"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

// Row: commit (fossil) -> terminal running fossil diff --checkin <h>
func TestPlanCommitFossil(t *testing.T) {
	f := newFix(t)
	cmd := mustPlan(t, f.cfg, Target{Type: TypeCommit, ProjectRoot: f.root, VCS: "fossil", SHA: "bdcb99"}, ActionTerminal)
	got := termArgv(t, cmd)
	want := []string{f.realDir, "fossil", "diff", "--checkin", "bdcb99"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

// Row: session -> terminal in project dir: claude --resume <id> with CLAUDE_CONFIG_DIR
func TestPlanSession(t *testing.T) {
	f := newFix(t)
	id := "121c3b19-ada3-495e-a518-7fe5acf0a71e"
	cmd := mustPlan(t, f.cfg, Target{Type: TypeSession, ProjectRoot: f.root, SessionID: id, Profile: "/Users/alice/.claude-work"}, ActionResume)
	got := termArgv(t, cmd)
	want := []string{f.realDir, "env", "CLAUDE_CONFIG_DIR=/Users/alice/.claude-work", "claude", "--resume", id}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	cmd = mustPlan(t, f.cfg, Target{Type: TypeSession, ProjectRoot: f.root, SessionID: id}, "")
	if got := termArgv(t, cmd); !reflect.DeepEqual(got, []string{f.realDir, "claude", "--resume", id}) {
		t.Fatalf("no-profile argv = %q", got)
	}
	if _, err := f.cfg.Plan(Target{Type: TypeSession, ProjectRoot: f.root, SessionID: id, Profile: "rel/.claude"}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative profile: %v", err)
	}
}

// Row: server / URL -> open -a Safari http://localhost:<port>
func TestPlanServerURL(t *testing.T) {
	c := Config{}
	wantCmd(t, mustPlan(t, c, Target{Type: TypeServer, Port: "5173"}, ""), "open", "-a", "Safari", "http://localhost:5173")
	wantCmd(t, mustPlan(t, c, Target{Type: TypeServer, Port: "8090"}, ActionSafari), "open", "-a", "Safari", "http://localhost:8090")
	ok := []string{
		"http://localhost:3000",
		"http://127.0.0.1:3000/app?x=1",
		"https://github.com/alice/shop/pull/3",
	}
	for _, u := range ok {
		wantCmd(t, mustPlan(t, c, Target{Type: TypeURL, URL: u}, ""), "open", "-a", "Safari", u)
	}
}

// Row: task / note -> <editor> <path>/.drashta/tasks/<id>.md
func TestPlanTaskNote(t *testing.T) {
	f := newFix(t)
	cmd := mustPlan(t, f.cfg, Target{Type: TypeTask, ProjectRoot: f.root, Path: ".drashta/tasks/T-1.md"}, "")
	wantCmd(t, cmd, "code", "-g", filepath.Join(f.realDir, ".drashta/tasks/T-1.md")+":1")
	cmd = mustPlan(t, f.cfg, Target{Type: TypeNote, ProjectRoot: f.root, Path: ".drashta/notes/N-1.md"}, ActionEditor)
	wantCmd(t, cmd, "code", "-g", filepath.Join(f.realDir, ".drashta/notes/N-1.md")+":1")
	if _, err := f.cfg.Plan(Target{Type: TypeTask, ProjectRoot: f.root, Path: "README.md"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("task outside .drashta: %v", err)
	}
}

func TestPlanInjection(t *testing.T) {
	f := newFix(t)
	id := "121c3b19-ada3-495e-a518-7fe5acf0a71e"
	cases := map[string]Target{
		"sha option":          {Type: TypeCommit, ProjectRoot: f.root, VCS: "git", SHA: "--output=/x"},
		"fossil sha option":   {Type: TypeCommit, ProjectRoot: f.root, VCS: "fossil", SHA: "--output=/x"},
		"sha uppercase":       {Type: TypeCommit, ProjectRoot: f.root, VCS: "git", SHA: "ABC1234"},
		"sha short":           {Type: TypeCommit, ProjectRoot: f.root, VCS: "git", SHA: "abc12"},
		"sha with newline":    {Type: TypeCommit, ProjectRoot: f.root, VCS: "git", SHA: "abc1234\n"},
		"unknown vcs":         {Type: TypeCommit, ProjectRoot: f.root, VCS: "hg", SHA: "abc1234"},
		"dotdot":              {Type: TypeFile, ProjectRoot: f.root, Path: "../../../outside/secret.txt"},
		"dotdot inside":       {Type: TypeFile, ProjectRoot: f.root, Path: "src/../README.md"},
		"absolute outside":    {Type: TypeFile, ProjectRoot: f.root, Path: f.outside},
		"symlink file out":    {Type: TypeFile, ProjectRoot: f.root, Path: "src/escape.go"},
		"symlink dir out":     {Type: TypeFolder, ProjectRoot: f.root, Path: "linkdir"},
		"root outside roots":  {Type: TypeProject, ProjectRoot: filepath.Dir(f.outside)},
		"empty root":          {Type: TypeFile, Path: "src/x.go"},
		"missing file":        {Type: TypeFile, ProjectRoot: f.root, Path: "src/nope.go"},
		"port injection":      {Type: TypeServer, Port: "80;rm"},
		"port option":         {Type: TypeServer, Port: "-1"},
		"port zero":           {Type: TypeServer, Port: "0"},
		"port too big":        {Type: TypeServer, Port: "70000"},
		"port empty":          {Type: TypeServer, Port: ""},
		"file url":            {Type: TypeURL, URL: "file:///etc/passwd"},
		"evil https":          {Type: TypeURL, URL: "https://evil.com"},
		"github lookalike":    {Type: TypeURL, URL: "https://github.com.evil.com/x"},
		"github userinfo":     {Type: TypeURL, URL: "https://x@github.com/a"},
		"http github":         {Type: TypeURL, URL: "http://github.com/a"},
		"localhost no port":   {Type: TypeURL, URL: "http://localhost/"},
		"localhost https":     {Type: TypeURL, URL: "https://localhost:3000"},
		"other host":          {Type: TypeURL, URL: "http://10.0.0.1:80"},
		"javascript url":      {Type: TypeURL, URL: "javascript:alert(1)"},
		"dash url":            {Type: TypeURL, URL: "-a"},
		"session not uuid":    {Type: TypeSession, ProjectRoot: f.root, SessionID: "--dangerously-skip-permissions"},
		"session upper uuid":  {Type: TypeSession, ProjectRoot: f.root, SessionID: strings.ToUpper(id)},
		"session uuid suffix": {Type: TypeSession, ProjectRoot: f.root, SessionID: id + ";rm"},
		"session outside":     {Type: TypeSession, ProjectRoot: filepath.Dir(f.outside), SessionID: id},
		"unknown type":        {Type: "shell", Path: "x"},
	}
	for name, tg := range cases {
		cmd, err := f.cfg.Plan(tg, "")
		if err == nil {
			t.Errorf("%s: want error, got %v", name, cmd)
			continue
		}
		if cmd.Name != "" || cmd.Args != nil {
			t.Errorf("%s: error with a command %v", name, cmd)
		}
	}
	// Specific error kinds.
	if _, err := f.cfg.Plan(cases["symlink file out"], ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("symlink: %v", err)
	}
	if _, err := f.cfg.Plan(cases["dotdot"], ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("dotdot: %v", err)
	}
	if _, err := f.cfg.Plan(cases["file url"], ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("file url: %v", err)
	}
	if _, err := f.cfg.Plan(cases["missing file"], ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	// No roots configured: every path target is refused.
	if _, err := Plan(Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, ""); err == nil {
		t.Error("Plan without roots must refuse paths")
	}
}

func TestPlanWrongAction(t *testing.T) {
	f := newFix(t)
	cases := []struct {
		tg     Target
		action string
	}{
		{Target{Type: TypeServer, Port: "3000"}, ActionEditor},
		{Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, ActionSafari},
		{Target{Type: TypeTask, ProjectRoot: f.root, Path: ".drashta/tasks/T-1.md"}, ActionTerminal},
		{Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, "rm -rf"},
	}
	for _, c := range cases {
		if _, err := f.cfg.Plan(c.tg, c.action); !errors.Is(err, ErrAction) {
			t.Errorf("%s/%s: %v", c.tg.Type, c.action, err)
		}
	}
}

// Every data argument in every planned command must not start with "-"
// unless it is a fixed flag of the tool.
func TestPlanNoDashData(t *testing.T) {
	f := newFix(t)
	cmd := mustPlan(t, f.cfg, Target{Type: TypeFile, ProjectRoot: f.root, Path: "src/x.go"}, "")
	if strings.HasPrefix(cmd.Args[len(cmd.Args)-1], "-") {
		t.Fatal("path arg starts with -")
	}
}

func TestDoRecorder(t *testing.T) {
	f := newFix(t)
	rec := &RecorderExecutor{}
	ran, err := f.cfg.Do(context.Background(), rec, Target{Type: TypeServer, Port: "5173", Ref: "server:5173"}, "")
	if err != nil || ran != "open -a Safari http://localhost:5173" {
		t.Fatalf("ran=%q err=%v", ran, err)
	}
	if len(rec.Cmds) != 1 {
		t.Fatalf("recorded %d", len(rec.Cmds))
	}
	// A refused plan never reaches the executor.
	if _, err := f.cfg.Do(context.Background(), rec, Target{Type: TypeURL, URL: "file:///etc/passwd"}, ""); err == nil {
		t.Fatal("want error")
	}
	if len(rec.Cmds) != 1 {
		t.Fatal("refused plan was executed")
	}
	// Executor errors come back with the command line.
	rec.Err = errors.New("boom")
	ran, err = f.cfg.Do(context.Background(), rec, Target{Type: TypeServer, Port: "1"}, "")
	if err == nil || ran == "" {
		t.Fatalf("ran=%q err=%v", ran, err)
	}
	// Cancelled context: nothing runs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Do(ctx, rec, Target{Type: TypeServer, Port: "1"}, ""); err == nil {
		t.Fatal("cancelled ctx must fail")
	}
	if len(rec.Cmds) != 2 {
		t.Fatalf("recorded %d", len(rec.Cmds))
	}
}
