package projects

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/probe"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ids(ps []probe.Project) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func testConfig(projectsDir string, roots ...string) *config.Config {
	c := config.Default()
	c.ProjectsDir = projectsDir
	c.Roots = roots
	return c
}

func TestDiscoverOrderIdsAndHidden(t *testing.T) {
	base := t.TempDir()
	// Roots in config order: tools, then alpha. Names inside sort A-Z.
	mkdir(t, base, "tools", "zed")
	mkdir(t, base, "tools", "atlas")
	mkdir(t, base, "tools", "venv")
	mkdir(t, base, "alpha", "relay")
	mkdir(t, base, "alpha", "Beta")
	mkdir(t, base, "alpha", "go")
	mkdir(t, base, "ignored_group", "nope") // not in roots
	write(t, filepath.Join(base, "tools"), "atlas-handoff.md", "x")
	write(t, filepath.Join(base, "alpha"), ".python-version", "3.12")
	write(t, filepath.Join(base, "alpha"), ".DS_Store", "x")

	cfg := testConfig(base, "tools", "alpha", "missing_root", "tools")
	cfg.Hidden = []string{"venv", "go"}

	got, err := Discover(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"atlas", "venv", "zed", "Beta", "go", "relay"}
	if !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("ids = %v\nwant %v", ids(got), want)
	}

	byID := map[string]probe.Project{}
	for _, p := range got {
		byID[p.ID] = p
	}
	l := byID["atlas"]
	if l.Name != "atlas" || l.Group != "tools" || l.Path != filepath.Join(base, "tools", "atlas") || l.Hidden {
		t.Errorf("atlas = %+v", l)
	}
	for id, wantHidden := range map[string]bool{
		"venv": true, "go": true, "zed": false, "Beta": false, "atlas": false,
	} {
		if byID[id].Hidden != wantHidden {
			t.Errorf("%s Hidden = %v, want %v", id, byID[id].Hidden, wantHidden)
		}
	}

	// Stable: a second call gives the same list.
	again, _ := Discover(cfg)
	if !reflect.DeepEqual(ids(again), ids(got)) {
		t.Error("order changed between calls")
	}
}

func TestDiscoverSymlinkedProject(t *testing.T) {
	base := t.TempDir()
	elsewhere := mkdir(t, t.TempDir(), "real_project")
	mkdir(t, base, "g")
	if err := os.Symlink(elsewhere, filepath.Join(base, "g", "linked")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(base, "g"), "target.txt", "x")
	if err := os.Symlink(filepath.Join(base, "g", "target.txt"), filepath.Join(base, "g", "filelink")); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(testConfig(base, "g"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(got), []string{"linked"}) {
		t.Fatalf("ids = %v", ids(got))
	}
	if got[0].Path != filepath.Join(base, "g", "linked") {
		t.Errorf("Path must not be resolved: %q", got[0].Path)
	}
}

func TestDiscoverIDsAreNamesAndClashesGetGroupPrefix(t *testing.T) {
	base := t.TempDir()
	// "x" is in two groups, "y" in three, "solo" in one. A clash also counts
	// when one of the two is hidden.
	mkdir(t, base, "personal", "x")
	mkdir(t, base, "personal", "solo")
	mkdir(t, base, "personal", "y")
	mkdir(t, base, "tools", "x")
	mkdir(t, base, "tools", "y")
	mkdir(t, base, "work", "y")
	mkdir(t, base, "work", "z")

	cfg := testConfig(base, "personal", "tools", "work")
	cfg.Hidden = []string{"tools/x"}

	got, err := Discover(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"solo", "personal.x", "personal.y", "tools.x", "tools.y", "work.y", "z"}
	if !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("ids = %v\nwant %v", ids(got), want)
	}
	// Name and Group stay the plain values. Ids never contain a slash.
	for _, p := range got {
		if strings.Contains(p.ID, "/") {
			t.Errorf("id %q has a slash", p.ID)
		}
		if p.Name == "" || p.Group == "" || p.Path != filepath.Join(base, p.Group, p.Name) {
			t.Errorf("bad project %+v", p)
		}
	}
	byID := map[string]probe.Project{}
	for _, p := range got {
		byID[p.ID] = p
	}
	if p := byID["tools.x"]; p.Name != "x" || p.Group != "tools" || !p.Hidden {
		t.Errorf("tools.x = %+v", p)
	}
	if p := byID["personal.x"]; p.Name != "x" || p.Group != "personal" || p.Hidden {
		t.Errorf("personal.x = %+v", p)
	}
}

func TestDiscoverIDsStableAcrossRunsAndRootOrder(t *testing.T) {
	base := t.TempDir()
	mkdir(t, base, "personal", "x")
	mkdir(t, base, "tools", "x")
	mkdir(t, base, "tools", "only_tools")

	ab := testConfig(base, "personal", "tools")
	ba := testConfig(base, "tools", "personal")

	first, _ := Discover(ab)
	for i := 0; i < 5; i++ {
		again, _ := Discover(ab)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d gave a different result", i)
		}
	}

	// Another root order changes the list order but not which id a folder gets.
	idOf := func(ps []probe.Project) map[string]string {
		m := map[string]string{}
		for _, p := range ps {
			m[p.Path] = p.ID
		}
		return m
	}
	other, _ := Discover(ba)
	if !reflect.DeepEqual(idOf(first), idOf(other)) {
		t.Errorf("ids depend on root order:\n%v\n%v", idOf(first), idOf(other))
	}

	// Hiding a project never changes ids.
	ab.Hidden = []string{"personal.x", "only_tools"}
	hid, _ := Discover(ab)
	if !reflect.DeepEqual(idOf(first), idOf(hid)) {
		t.Errorf("hide list changed ids: %v", idOf(hid))
	}
	for _, p := range hid {
		wantHidden := p.ID == "personal.x" || p.ID == "only_tools"
		if p.Hidden != wantHidden {
			t.Errorf("%s Hidden = %v, want %v", p.ID, p.Hidden, wantHidden)
		}
	}
}

func TestDiscoverHiddenByGroupAndName(t *testing.T) {
	base := t.TempDir()
	mkdir(t, base, "a", "same")
	mkdir(t, base, "b", "same")
	cfg := testConfig(base, "a", "b")
	cfg.Hidden = []string{"b/same"}
	got, _ := Discover(cfg)
	if len(got) != 2 || got[0].Hidden || !got[1].Hidden {
		t.Errorf("only b/same should be hidden: %+v", got)
	}
	if got[0].ID != "a.same" || got[1].ID != "b.same" {
		t.Errorf("clash ids wrong: %v", ids(got))
	}
}

func TestDiscoverEmptyAndUnreadable(t *testing.T) {
	base := t.TempDir()
	got, err := Discover(testConfig(base, "nothing_here"))
	if err != nil || len(got) != 0 {
		t.Errorf("missing root: %v %v", got, err)
	}

	if os.Geteuid() == 0 {
		return
	}
	mkdir(t, base, "ok", "p1")
	locked := mkdir(t, base, "locked", "p2")
	if err := os.Chmod(filepath.Dir(locked), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Dir(locked), 0o755) })
	got, err = Discover(testConfig(base, "locked", "ok"))
	if err == nil || !errors.Is(err, os.ErrPermission) {
		t.Errorf("want a permission error, got %v", err)
	}
	if !reflect.DeepEqual(ids(got), []string{"p1"}) {
		t.Errorf("the readable root must still be returned: %v", ids(got))
	}
}

func TestDescribeStack(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name  string
		files []string
		want  []string
	}{
		{"none", []string{"notes.txt"}, []string{}},
		{"go", []string{"go.mod", "main.go"}, []string{"go"}},
		{"node+vite", []string{"package.json", "vite.config.ts"}, []string{"node", "vite"}},
		{"vite js", []string{"vite.config.js"}, []string{"vite"}},
		{"rust", []string{"Cargo.toml"}, []string{"rust"}},
		{"python", []string{"pyproject.toml"}, []string{"python"}},
		{"mixed fixed order", []string{"pyproject.toml", "vite.config.mjs", "Cargo.toml", "package.json", "go.mod"},
			[]string{"go", "node", "rust", "python", "vite"}},
	}
	for _, tc := range cases {
		dir := mkdir(t, base, strings.ReplaceAll(tc.name, " ", "_"))
		for _, f := range tc.files {
			write(t, dir, f, "x")
		}
		got := Describe(dir)
		if !reflect.DeepEqual(got.Stack, tc.want) {
			t.Errorf("%s: stack = %v, want %v", tc.name, got.Stack, tc.want)
		}
		if got.Stack == nil {
			t.Errorf("%s: Stack must not be nil", tc.name)
		}
	}

	// A marker that is a folder does not count.
	dir := mkdir(t, base, "folder_marker")
	mkdir(t, dir, "go.mod")
	if s := Describe(dir).Stack; len(s) != 0 {
		t.Errorf("folder named go.mod counted: %v", s)
	}

	// Missing folder: empty info, no panic.
	got := Describe(filepath.Join(base, "does_not_exist"))
	if got.Stack == nil || len(got.Stack) != 0 || got.Description != "" || got.ReadmePath != "" {
		t.Errorf("missing folder: %+v", got)
	}
}

func TestDescribeReadme(t *testing.T) {
	cases := []struct {
		name       string
		readmeName string
		body       string
		want       string
	}{
		{"simple", "README.md", "# Title\n\nA small tool that does one thing.\n\nSecond paragraph.\n", "A small tool that does one thing."},
		{"no heading", "README.md", "Just a line of prose.\n", "Just a line of prose."},
		{"multi-line paragraph", "README.md", "# T\n\nThis paragraph\nruns over\nthree lines.\n\nNext.", "This paragraph runs over three lines."},
		{"badges and html first",
			"README.md",
			"<p align=\"center\">\n  <img src=\"logo.png\">\n</p>\n\n# Name\n\n[![Build](https://x.dev/b.svg)](https://x.dev) ![v](https://x.dev/v.svg)\n\nA **fast** tool with [links](https://example.com) and `code`.\n",
			"A fast tool with links and code."},
		{"setext heading", "README.md", "Name\n====\n\nReal text here.\n", "Real text here."},
		{"setext h2", "README.md", "Name\n----\n\nMore real text.\n", "More real text."},
		{"code block and list skipped", "README.md",
			"# T\n\n```sh\nmake build\n```\n\n- item one\n- item two\n\n| a | b |\n|---|---|\n\nFinally prose.\n", "Finally prose."},
		{"front matter", "README.md", "---\ntitle: x\n---\n\n# H\n\nBody text.\n", "Body text."},
		{"comment skipped", "README.md", "<!-- hidden\nmulti line -->\n\n# H\n\nVisible.\n", "Visible."},
		{"blockquote", "README.md", "# H\n\n> Quoted tagline.\n", "Quoted tagline."},
		{"only headings", "README.md", "# One\n\n## Two\n", ""},
		{"empty", "README.md", "", ""},
		{"txt readme", "README.txt", "Plain text readme.\n\nMore.\n", "Plain text readme."},
		{"no extension", "README", "Bare readme file.\n", "Bare readme file."},
		{"lowercase name", "readme.md", "Lower case name.\n", "Lower case name."},
		{"crlf", "README.md", "# T\r\n\r\nWindows line endings.\r\nSecond line.\r\n", "Windows line endings. Second line."},
		{"heading right after text", "README.md", "Intro line\n# Heading\n\nOther.", "Intro line"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		write(t, dir, tc.readmeName, tc.body)
		got := Describe(dir)
		if got.Description != tc.want {
			t.Errorf("%s: description = %q, want %q", tc.name, got.Description, tc.want)
		}
		wantPath := tc.readmeName
		if got.ReadmePath != wantPath {
			t.Errorf("%s: ReadmePath = %q, want %q", tc.name, got.ReadmePath, wantPath)
		}
	}
}

func TestDescribeNoReadme(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "main.go", "package main")
	got := Describe(dir)
	if got.Description != "" || got.ReadmePath != "" {
		t.Errorf("no README: %+v", got)
	}
}

func TestDescribePicksBestReadme(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.txt", "From txt.\n")
	write(t, dir, "README.md", "From md.\n")
	write(t, dir, "README.rst", "From rst.\n")
	got := Describe(dir)
	if got.ReadmePath != "README.md" || got.Description != "From md." {
		t.Errorf("got %+v", got)
	}
}

func TestDescribeLongDescriptionIsCut(t *testing.T) {
	dir := t.TempDir()
	words := strings.Repeat("alpha beta gamma delta ", 40) // 920 chars
	write(t, dir, "README.md", "# T\n\n"+words+"\n")
	d := Describe(dir).Description
	if n := utf8.RuneCountInString(d); n > 300 || n < 200 {
		t.Errorf("length = %d, want 200..300", n)
	}
	if !strings.HasSuffix(d, "…") {
		t.Errorf("cut text should end with an ellipsis: %q", d[len(d)-10:])
	}
	if strings.Contains(strings.TrimSuffix(d, "…"), "  ") || strings.HasSuffix(strings.TrimSuffix(d, "…"), " ") {
		t.Errorf("odd spacing at the cut: %q", d)
	}

	// Multi-byte text is cut by characters, not bytes.
	write(t, dir, "README.md", strings.Repeat("é", 500)+"\n")
	d = Describe(dir).Description
	if !utf8.ValidString(d) || utf8.RuneCountInString(d) != 300 {
		t.Errorf("rune cut wrong: %d runes, valid=%v", utf8.RuneCountInString(d), utf8.ValidString(d))
	}

	// Exactly 300 characters stays whole.
	write(t, dir, "README.md", strings.Repeat("x", 300)+"\n")
	if d = Describe(dir).Description; d != strings.Repeat("x", 300) {
		t.Errorf("300 chars must stay whole, got %d runes", utf8.RuneCountInString(d))
	}
}

func TestDescribeReadmeSymlinkOutsideIsIgnored(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "secret.md", "Private words that must not appear.\n")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	got := Describe(dir)
	if got.Description != "" || got.ReadmePath != "" {
		t.Errorf("README link out of the project was read: %+v", got)
	}

	// A link that stays inside is fine.
	dir2 := t.TempDir()
	write(t, dir2, "docs/intro.md", "Inside words.\n")
	if err := os.Symlink(filepath.Join(dir2, "docs", "intro.md"), filepath.Join(dir2, "README.md")); err != nil {
		t.Fatal(err)
	}
	if got := Describe(dir2); got.Description != "Inside words." {
		t.Errorf("inside link: %+v", got)
	}
}

func TestEmptyNotice(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ProjectsDir: filepath.Join(dir, "nope")}
	if n := EmptyNotice(cfg, "/x/config.toml"); !strings.Contains(n, "does not exist") || !strings.Contains(n, "/x/config.toml") {
		t.Errorf("missing dir notice = %q", n)
	}
	cfg = &config.Config{ProjectsDir: dir}
	if n := EmptyNotice(cfg, "/x/config.toml"); !strings.Contains(n, "no projects found under "+dir) {
		t.Errorf("no roots notice = %q", n)
	}
	if err := os.MkdirAll(filepath.Join(dir, "g", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Roots = []string{"g"}
	if n := EmptyNotice(cfg, "/x/config.toml"); n != "" {
		t.Errorf("projects exist but notice = %q", n)
	}
}
