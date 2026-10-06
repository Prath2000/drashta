package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeHome points HOME at a temp dir so "~" never means the real home.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(DataDirEnv, "")
	return home
}

func TestLoadCreatesDefaults(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, "sub", "config.toml")

	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file was not created: %v", err)
	}

	want := Default()
	want.normalize() // expands "~" with the fake HOME
	if !reflect.DeepEqual(c, want) {
		t.Errorf("loaded config differs from defaults\n got: %#v\nwant: %#v", c, want)
	}
	if c.ProjectsDir != filepath.Join(home, "projects") {
		t.Errorf("projects_dir not expanded: %q", c.ProjectsDir)
	}
	for _, p := range c.ClaudeProfiles {
		if strings.HasPrefix(p, "~") {
			t.Errorf("profile not expanded: %q", p)
		}
	}
	if len(c.Hidden) != 0 || c.DesktopScript != "" {
		t.Errorf("owner-specific defaults leaked: hidden=%v desktop=%q", c.Hidden, c.DesktopScript)
	}
	if len(c.Roots) != 0 {
		t.Errorf("empty home should have no roots, got %v", c.Roots)
	}
	if want := []string{filepath.Join(home, ".claude")}; !reflect.DeepEqual(c.ClaudeProfiles, want) {
		t.Errorf("profiles on an empty home = %v", c.ClaudeProfiles)
	}

	// A second load reads the file that was just written and gives the same result.
	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, c2) {
		t.Errorf("round trip changed the config\n%#v\n%#v", c, c2)
	}
}

func TestLoadRoundTripCustom(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, "config.toml")
	text := `
projects_dir = "~/code"
roots = ["a", "b"]
hidden = ["x"]
skip = ["out", "docs/generated"]
editor = ["subl", "{path}:{line}"]
terminal = "Ghostty"
claude_profiles = ["~/.claude", "/abs/profile"]
gh_enabled = false
desktop_script = "~/code/a/tool/plash-mode"
chitralipi = "/opt/x/chitralipi"
`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		ProjectsDir:    filepath.Join(home, "code"),
		Roots:          []string{"a", "b"},
		Hidden:         []string{"x"},
		Skip:           []string{"out", "docs/generated"},
		Editor:         []string{"subl", "{path}:{line}"},
		Terminal:       "Ghostty",
		ClaudeProfiles: []string{filepath.Join(home, ".claude"), "/abs/profile"},
		GHEnabled:      false,
		DesktopScript:  filepath.Join(home, "code", "a", "tool", "plash-mode"),
		Chitralipi:     "/opt/x/chitralipi",
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got %#v\nwant %#v", c, want)
	}
	gotRoots := c.RootPaths()
	if len(gotRoots) != 2 || gotRoots[0] != filepath.Join(home, "code", "a") {
		t.Errorf("RootPaths wrong: %v", gotRoots)
	}
}

func TestLoadMissingKeysKeepDefaults(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("terminal = \"iTerm\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Terminal != "iTerm" {
		t.Errorf("terminal = %q", c.Terminal)
	}
	if !c.GHEnabled {
		t.Errorf("defaults lost: gh=%v", c.GHEnabled)
	}
}

func mkdirs(t *testing.T, base string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(base, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAutoRootsFindGroups(t *testing.T) {
	home := fakeHome(t)
	pd := filepath.Join(home, "projects")
	mkdirs(t, pd, "work/api/.git", "personal/blog", "solo/.git", "empty", ".hidden/x", "node_modules/y", "app")
	os.WriteFile(filepath.Join(pd, "app", "go.mod"), nil, 0o644)
	os.WriteFile(filepath.Join(pd, "notes.txt"), nil, 0o644)

	path := filepath.Join(home, "config.toml")
	os.WriteFile(path, []byte("terminal = \"iTerm\"\n"), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"personal", "work"}; !reflect.DeepEqual(c.Roots, want) {
		t.Errorf("auto roots = %v, want %v", c.Roots, want)
	}
}

func TestExplicitRootsWin(t *testing.T) {
	home := fakeHome(t)
	mkdirs(t, filepath.Join(home, "projects"), "work/api", "other/x")
	path := filepath.Join(home, "config.toml")

	os.WriteFile(path, []byte("roots = [\"other\"]\n"), 0o644)
	c, _ := Load(path)
	if !reflect.DeepEqual(c.Roots, []string{"other"}) {
		t.Errorf("explicit roots = %v", c.Roots)
	}
	os.WriteFile(path, []byte("roots = []\n"), 0o644)
	c, _ = Load(path)
	if len(c.Roots) != 0 {
		t.Errorf("roots = [] must stay empty, got %v", c.Roots)
	}
}

func TestAutoProfiles(t *testing.T) {
	home := fakeHome(t)
	mkdirs(t, home, ".claude", ".claude-work", ".claude-b")
	os.WriteFile(filepath.Join(home, ".claude-file"), nil, 0o644)
	path := filepath.Join(home, "config.toml")
	os.WriteFile(path, []byte("terminal = \"x\"\n"), 0o644)
	c, _ := Load(path)
	want := []string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude-b"), filepath.Join(home, ".claude-work")}
	if !reflect.DeepEqual(c.ClaudeProfiles, want) {
		t.Errorf("profiles = %v, want %v", c.ClaudeProfiles, want)
	}

	os.WriteFile(path, []byte("claude_profiles = [\"~/.claude-work\"]\n"), 0o644)
	c, _ = Load(path)
	if !reflect.DeepEqual(c.ClaudeProfiles, []string{filepath.Join(home, ".claude-work")}) {
		t.Errorf("explicit profiles = %v", c.ClaudeProfiles)
	}
}

func TestLoadChitralipi(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, "config.toml")

	// A file without the key gives the default, with "~" expanded.
	if err := os.WriteFile(path, []byte("terminal = \"iTerm\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "bin", "chitralipi"); c.Chitralipi != want {
		t.Errorf("default chitralipi = %q, want %q", c.Chitralipi, want)
	}

	// A "~" path in the file is expanded.
	if err := os.WriteFile(path, []byte("chitralipi = \"~/bin/chitralipi\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(path); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "bin", "chitralipi"); c.Chitralipi != want {
		t.Errorf("~ chitralipi = %q, want %q", c.Chitralipi, want)
	}

	// An absolute path wins as it is.
	if err := os.WriteFile(path, []byte("chitralipi = \"/opt/x/chitralipi\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(path); err != nil {
		t.Fatal(err)
	}
	if c.Chitralipi != "/opt/x/chitralipi" {
		t.Errorf("chitralipi = %q", c.Chitralipi)
	}
}

func TestLoadEmptyHiddenMeansNothingHidden(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("hidden = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Hidden) != 0 {
		t.Errorf("hidden should be empty, got %v", c.Hidden)
	}
	if c.IsHidden("g", "venv") {
		t.Error("venv should not be hidden")
	}
}

func TestLoadBadTOML(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("roots = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("want an error for invalid TOML")
	}
	// The broken file must be left alone.
	b, _ := os.ReadFile(path)
	if string(b) != "roots = [\n" {
		t.Errorf("Load changed an existing file: %q", b)
	}
}

func TestIsHidden(t *testing.T) {
	c := Default()
	c.Hidden = []string{"venv", "tools/gizmo-v0.9.0", "work.dup"}
	cases := []struct {
		group, name string
		want        bool
	}{
		{"personal", "venv", true},
		{"tools", "venv", true},
		{"tools", "gizmo-v0.9.0", true},
		{"work", "gizmo-v0.9.0", false},
		{"tools", "atlas", false},
		{"work", "dup", true},
		{"tools", "dup", false},
	}
	for _, tc := range cases {
		if got := c.IsHidden(tc.group, tc.name); got != tc.want {
			t.Errorf("IsHidden(%q,%q) = %v, want %v", tc.group, tc.name, got, tc.want)
		}
	}
}

func TestSetHiddenKeepsCommentsAndOtherKeys(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, "config.toml")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetHidden(path, []string{"venv", "old_stuff", "has\"quote"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Hidden, []string{"venv", "old_stuff", "has\"quote"}) {
		t.Errorf("c.Hidden not updated: %v", c.Hidden)
	}

	raw, _ := os.ReadFile(path)
	text := string(raw)
	if !strings.Contains(text, "# drashta config.") || !strings.Contains(text, "# Extra folder names") {
		t.Errorf("comments were lost:\n%s", text)
	}
	if strings.Count(text, "hidden  =") != 1 {
		t.Errorf("expected exactly one hidden line:\n%s", text)
	}

	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c2.Hidden, c.Hidden) {
		t.Errorf("reloaded hidden = %v", c2.Hidden)
	}
	// Everything else is untouched.
	c2.Hidden = Default().Hidden
	want := Default()
	want.normalize()
	if !reflect.DeepEqual(c2, want) {
		t.Errorf("other keys changed\n got %#v\nwant %#v", c2, want)
	}

	// Empty list is allowed and round trips.
	if err := c.SetHidden(path, nil); err != nil {
		t.Fatal(err)
	}
	c3, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c3.Hidden) != 0 {
		t.Errorf("hidden should be empty: %v", c3.Hidden)
	}
}

func TestSetHiddenMultilineAndMissingKey(t *testing.T) {
	home := fakeHome(t)

	// Multi-line array with a comment inside.
	p1 := filepath.Join(home, "one.toml")
	one := "roots = [\"a\"]\nhidden = [\n  \"a\", # first\n  \"b\",\n]\nterminal = \"Term\"\n"
	if err := os.WriteFile(p1, []byte(one), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetHidden(p1, []string{"z"}); err != nil {
		t.Fatal(err)
	}
	c, err = Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Hidden, []string{"z"}) || c.Terminal != "Term" || !reflect.DeepEqual(c.Roots, []string{"a"}) {
		t.Errorf("multi-line rewrite broke the file: %#v", c)
	}

	// No hidden key at all, and a table in the file: the key must land before the table.
	p2 := filepath.Join(home, "two.toml")
	two := "terminal = \"Term\"\n\n[other]\nhidden = \"not ours\"\n"
	if err := os.WriteFile(p2, []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}
	c = Default()
	if err := c.SetHidden(p2, []string{"q"}); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(p2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c2.Hidden, []string{"q"}) || c2.Terminal != "Term" {
		t.Errorf("insert before table failed: %#v", c2)
	}
	raw, _ := os.ReadFile(p2)
	if !strings.Contains(string(raw), "hidden = \"not ours\"") {
		t.Errorf("the [other] table was damaged:\n%s", raw)
	}
}

func TestSetHiddenCreatesMissingFile(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, "new", "config.toml")
	c := Default()
	if err := c.SetHidden(path, []string{"only"}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Hidden, []string{"only"}) {
		t.Errorf("hidden = %v", got.Hidden)
	}
}

func TestDataDir(t *testing.T) {
	home := fakeHome(t)

	if got, want := DataDir(), filepath.Join(home, ".drashta"); got != want {
		t.Errorf("default DataDir = %q, want %q", got, want)
	}

	custom := filepath.Join(t.TempDir(), "state")
	t.Setenv(DataDirEnv, custom)
	if got := DataDir(); got != custom {
		t.Errorf("DataDir with env = %q, want %q", got, custom)
	}
	if got, want := DefaultPath(), filepath.Join(custom, "config.toml"); got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
	// DataDir does not create the folder.
	if _, err := os.Stat(custom); err == nil {
		t.Error("DataDir must not create the folder")
	}

	t.Setenv(DataDirEnv, "~/elsewhere")
	if got, want := DataDir(), filepath.Join(home, "elsewhere"); got != want {
		t.Errorf("DataDir with ~ = %q, want %q", got, want)
	}
}

func TestReencodeFallback(t *testing.T) {
	old := []byte("terminal = \"Term\"\nhidden = [\"a\"]\n")
	out, err := reencode(old, "hidden", []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if !arrayMatches(out, "hidden", []string{"b"}) {
		t.Errorf("fallback output has wrong hidden list:\n%s", out)
	}
	if !strings.Contains(string(out), "Term") {
		t.Errorf("fallback lost other keys:\n%s", out)
	}
}

// Security I-1: the editor comes only from config.toml, and a dangerous one is refused
// at load time (default editor + one warning).
func TestEditorFromConfigIsValidated(t *testing.T) {
	fakeHome(t)
	for _, tc := range []struct {
		line string
		ok   bool
	}{
		{`editor = ["zed", "{path}:{line}"]`, true},
		{`editor = ["/usr/local/bin/subl", "{path}:{line}"]`, true},
		{`editor = ["code", "-g", "{path}:{line}"]`, true},
		{`editor = ["sh", "-c", "touch x"]`, false},
		{`editor = ["/bin/bash", "-c", "x"]`, false},
		{`editor = ["osascript", "-e", "x"]`, false},
		{`editor = ["python3", "-c", "x"]`, false},
		{`editor = ["/usr/bin/env", "sh"]`, false},
		{`editor = ["node", "-e", "x"]`, false},
		{`editor = ["code", "{path}", "{cmd}"]`, false},
		{`editor = ["code", "{path"]`, false},
		{`editor = ["./code", "{path}"]`, false},
		{`editor = ["/usr/../bin/zsh", "{path}"]`, false},
		{`editor = ["code", ""]`, false},
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(tc.line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if tc.ok {
			if len(c.Warnings) != 0 || reflect.DeepEqual(c.Editor, DefaultEditor) && !strings.Contains(tc.line, `"code", "-g"`) {
				t.Errorf("%s: refused: %v %v", tc.line, c.Editor, c.Warnings)
			}
			continue
		}
		if !reflect.DeepEqual(c.EditorList(), DefaultEditor) || len(c.Warnings) != 1 {
			t.Errorf("%s: editor %v, warnings %v; want the default and one warning", tc.line, c.Editor, c.Warnings)
		}
	}
}

// Security M-1: a new config.toml is private.
func TestConfigFileIsPrivate(t *testing.T) {
	fakeHome(t)
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config.toml mode %v (%v)", fi.Mode().Perm(), err)
	}
	if err := c.SetHidden(path, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("after SetHidden mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v", fi.Mode().Perm())
	}
}

// TestConcurrentHiddenAccess is for -race: readers use IsHidden/HiddenList while SetHidden writes.
func TestConcurrentHiddenAccess(t *testing.T) {
	fakeHome(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			_ = c.SetHidden(path, []string{"venv", "x" + string(rune('a'+i))})
		}
	}()
	for {
		select {
		case <-done:
			if got := c.HiddenList(); len(got) != 2 {
				t.Errorf("HiddenList = %v", got)
			}
			return
		default:
			_ = c.IsHidden("g", "venv")
			_ = c.HiddenList()
			_ = c.EditorList()
		}
	}
}
