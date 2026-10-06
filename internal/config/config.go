package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// Config is the content of ~/.drashta/config.toml (03-architecture.md section 9).
//
// After Load, ProjectsDir and ClaudeProfiles have "~" expanded to the home folder.
// Treat the fields as read-only after Load. The only change drashta makes at run time
// is SetHidden; it takes the lock inside the Config. Code that runs while it may be
// called must read Hidden through IsHidden and HiddenList, never through the field.
// The editor can only be set by editing config.toml by hand (security I-1): no API
// writes it.
type Config struct {
	mu sync.RWMutex // guards Hidden (and Editor) after Load

	ProjectsDir    string   `toml:"projects_dir" json:"projectsDir"`
	Roots          []string `toml:"roots" json:"roots"`
	Hidden         []string `toml:"hidden" json:"hidden"`
	Skip           []string `toml:"skip" json:"skip"`
	Editor         []string `toml:"editor" json:"editor"`
	Terminal       string   `toml:"terminal" json:"terminal"`
	ClaudeProfiles []string `toml:"claude_profiles" json:"claudeProfiles"`
	GHEnabled      bool     `toml:"gh_enabled" json:"ghEnabled"`
	// DesktopScript is the plash-mode script behind the desktop mode buttons
	// (contrib/desktop/README.md). Like the editor, only config.toml sets it.
	DesktopScript string `toml:"desktop_script" json:"desktopScript"`
	// Chitralipi is the DWG reader program behind the drawing preview. Like the editor,
	// only config.toml sets it.
	Chitralipi string `toml:"chitralipi" json:"chitralipi"`

	// Warnings are problems Load fixed by itself (for example an editor command it
	// refused and replaced by the default). The app logs them once at start.
	Warnings []string `toml:"-" json:"-"`
}

// DefaultEditor is the editor argv used when config.toml has none, or a refused one.
var DefaultEditor = []string{"code", "-g", "{path}:{line}"}

// Default returns the built-in settings. The "~" is NOT expanded here.
//
// Roots and ClaudeProfiles are nil on purpose: Load fills them by looking at the
// disk (see normalize) when config.toml does not set them. Hidden and DesktopScript
// are empty.
func Default() *Config {
	return &Config{
		ProjectsDir: "~/projects",
		Hidden:      []string{},
		Skip:        []string{},
		Editor:      append([]string(nil), DefaultEditor...),
		Terminal:    "Terminal",
		GHEnabled:   true,
		Chitralipi:  "~/.local/bin/chitralipi",
	}
}

// defaultFile is written when config.toml does not exist yet.
// Keep it in step with Default(). A test checks this.
const defaultFile = `# drashta config. Changes are picked up when drashta restarts.
# Every key is optional. A key that is missing gets the default described below.

# The folder that holds your projects.
projects_dir = "~/projects"

# Group folders inside projects_dir. Every folder directly inside a group is one
# project, so the layout is projects_dir/<group>/<project>.
# If this key is missing, drashta finds the groups itself: every folder in
# projects_dir that is not a project but contains folders. Set it to choose them.
# roots = ["work", "personal"]

# Project folders to hide from the UI: a folder name ("venv"), a group and folder
# ("tools/venv"), or a project id. They are still found, so you can show them again
# at any time.
hidden = []

# Extra folder names the file tree walk should not enter (added to the built-in list).
skip = []

# Command that opens a file in your editor. {path} and {line} are filled in.
editor = ["code", "-g", "{path}:{line}"]

# The terminal app used for "open in terminal" (macOS app name).
terminal = "Terminal"

# Claude Code profile folders that hold projects/<folder>/*.jsonl sessions.
# If this key is missing, drashta uses ~/.claude plus every ~/.claude-* folder that exists.
# claude_profiles = ["~/.claude"]

# Show GitHub data (pull requests, CI) for projects with a GitHub remote. Needs the
# gh command-line tool; without it this part stays empty.
gh_enabled = true

# Optional: script behind the desktop mode buttons. It must be inside projects_dir.
# Leave it empty to turn the desktop modes off.
# desktop_script = "~/projects/drashta/desktop/plash-mode"

# Optional program that reads DWG drawings for the drawing preview (chitralipi). If it
# is missing, a DWG file shows "Open in default app" instead.
chitralipi = "~/.local/bin/chitralipi"
`

// DataDirEnv names the environment variable that moves the data folder. Tests use it.
const DataDirEnv = "DRASHTA_HOME"

// DataDir returns the folder where drashta keeps its own files (index.db, config.toml, logs).
// It is $DRASHTA_HOME when that is set, otherwise ~/.drashta. The folder is not created here.
func DataDir() string {
	if v := os.Getenv(DataDirEnv); v != "" {
		return expandHome(v)
	}
	return filepath.Join(homeDir(), ".drashta")
}

// DefaultPath returns the config.toml path inside DataDir().
func DefaultPath() string {
	return filepath.Join(DataDir(), "config.toml")
}

// Load reads the config file at path.
//
// If the file does not exist, Load creates it (and its folder) with the defaults.
// Keys that are missing in the file keep their default value. A key that is
// present always wins, so "hidden = []" really means "hide nothing".
// An invalid TOML file is an error. Load never overwrites an existing file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("config: create folder: %w", err)
		}
		if err := writeAtomic(path, []byte(defaultFile)); err != nil {
			return nil, fmt.Errorf("config: write defaults: %w", err)
		}
		data = []byte(defaultFile)
	} else if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	c := Default()
	if _, err := toml.Decode(string(data), c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	c.normalize()
	if err := ValidateEditor(c.Editor); err != nil {
		c.Warnings = append(c.Warnings, fmt.Sprintf("config: editor %q refused (%v); using %q",
			strings.Join(c.Editor, " "), err, strings.Join(DefaultEditor, " ")))
		c.Editor = append([]string(nil), DefaultEditor...)
	}
	return c, nil
}

// refusedEditors are programs that can run any code given as an argument. They are never
// accepted as the editor, also not by an absolute path ending in that name.
var refusedEditors = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "ksh": true, "csh": true, "tcsh": true,
	"osascript": true, "python": true, "python3": true, "perl": true, "ruby": true, "node": true,
	"env": true, "xargs": true, "open": true,
}

var (
	editorBinRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	placeholderRe = regexp.MustCompile(`\{[^{}]*\}`)
)

// ValidateEditor checks an editor argv template from config.toml:
//   - it is not empty and no word is empty;
//   - the first word is a bare program name ("code") or a clean absolute path
//     ("/usr/local/bin/subl"), and its base name is not a shell or an interpreter
//     (sh, bash, zsh, dash, fish, osascript, python, python3, perl, ruby, node, env, ...);
//   - the only placeholders are {path} and {line}, and there are no other braces.
func ValidateEditor(argv []string) error {
	if len(argv) == 0 {
		return errors.New("editor command is empty")
	}
	bin := argv[0]
	switch {
	case filepath.IsAbs(bin):
		if filepath.Clean(bin) != bin || strings.ContainsAny(bin, "{}") {
			return fmt.Errorf("editor path %q is not a clean absolute path", bin)
		}
	case !editorBinRe.MatchString(bin):
		return fmt.Errorf("editor %q must be a plain program name or an absolute path", bin)
	}
	base := strings.ToLower(filepath.Base(bin))
	if refusedEditors[base] || strings.HasPrefix(base, "python") {
		return fmt.Errorf("editor %q is a shell or an interpreter", bin)
	}
	for _, a := range argv {
		if strings.TrimSpace(a) == "" {
			return errors.New("editor command has an empty word")
		}
		for _, m := range placeholderRe.FindAllString(a, -1) {
			if m != "{path}" && m != "{line}" {
				return fmt.Errorf("unknown placeholder %s (only {path} and {line})", m)
			}
		}
		if strings.ContainsAny(placeholderRe.ReplaceAllString(a, ""), "{}") {
			return fmt.Errorf("bad braces in %q", a)
		}
	}
	return nil
}

// normalize expands "~", fills empty required values and removes duplicates.
func (c *Config) normalize() {
	d := Default()
	if strings.TrimSpace(c.ProjectsDir) == "" {
		c.ProjectsDir = d.ProjectsDir
	}
	c.ProjectsDir = filepath.Clean(expandHome(c.ProjectsDir))
	if len(c.Editor) == 0 {
		c.Editor = d.Editor
	}
	if strings.TrimSpace(c.Terminal) == "" {
		c.Terminal = d.Terminal
	}
	// nil means the key was not set: look at the disk. "roots = []" is an explicit empty list.
	autoRoots := c.Roots == nil
	autoProfiles := c.ClaudeProfiles == nil
	c.Roots = cleanList(c.Roots)
	if autoRoots {
		c.Roots = DiscoverRoots(c.ProjectsDir)
	}
	if autoProfiles {
		c.ClaudeProfiles = discoverProfiles()
	}
	c.Hidden = cleanList(c.Hidden)
	c.Skip = cleanList(c.Skip)
	profiles := cleanList(c.ClaudeProfiles)
	for i, p := range profiles {
		profiles[i] = filepath.Clean(expandHome(p))
	}
	c.ClaudeProfiles = profiles
	if strings.TrimSpace(c.DesktopScript) != "" {
		c.DesktopScript = filepath.Clean(expandHome(c.DesktopScript))
	}
	if strings.TrimSpace(c.Chitralipi) != "" {
		c.Chitralipi = filepath.Clean(expandHome(c.Chitralipi))
	}
}

// RootPaths returns the absolute group folders: ProjectsDir/<root> for each root, in config order.
func (c *Config) RootPaths() []string {
	out := make([]string, 0, len(c.Roots))
	for _, r := range c.Roots {
		out = append(out, filepath.Join(c.ProjectsDir, r))
	}
	return out
}

// IsHidden reports whether a project is on the hide list. An entry in Hidden
// matches the folder name ("venv"), the folder path "group/name" ("tools/venv"),
// or the clash id "group.name" that projects.Discover uses when two groups have
// a project with the same name.
// It is safe to call while SetHidden runs.
func (c *Config) IsHidden(group, name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, h := range c.Hidden {
		if h == name || h == group+"/"+name || h == group+"."+name {
			return true
		}
	}
	return false
}

// HiddenList returns a copy of the hide list. It is safe to call while SetHidden runs.
func (c *Config) HiddenList() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string{}, c.Hidden...)
}

// EditorList returns a copy of the editor argv template.
func (c *Config) EditorList() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string{}, c.Editor...)
}

// writeMu makes sure two SetHidden calls never write the file at the same time.
var writeMu sync.Mutex

// SetHidden saves a new hide list into the config file at path and updates c.Hidden.
// It is the only place where drashta writes config.toml.
//
// Only the "hidden" line is changed. Your comments and the other lines stay as they are.
// If the file does not exist yet, it is created from the default template.
// The write is atomic (temp file in the same folder, then rename).
func (c *Config) SetHidden(path string, names []string) error {
	names = cleanList(names)

	writeMu.Lock()
	defer writeMu.Unlock()

	if err := rewriteArrayKey(path, "hidden", names); err != nil {
		return err
	}
	c.mu.Lock()
	c.Hidden = names
	c.mu.Unlock()
	return nil
}

// rewriteArrayKey sets one top-level string-array key in the file at path, keeping every
// other line. Call it with writeMu held.
func rewriteArrayKey(path, key string, values []string) error {
	old, err := os.ReadFile(path)
	var out []byte
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("config: create folder: %w", err)
		}
		out = replaceArrayLine([]byte(defaultFile), key, values)
	case err != nil:
		return fmt.Errorf("config: read %s: %w", path, err)
	default:
		out = replaceArrayLine(old, key, values)
		// Safety net: the edited text must still be valid TOML with the new list.
		// If it is not (an unusual layout), fall back to a full rewrite.
		if !arrayMatches(out, key, values) {
			out, err = reencode(old, key, values)
			if err != nil {
				return err
			}
		}
	}
	if err := writeAtomic(path, out); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// keyLineRe finds the start of a top-level "<key> = ..." line.
func keyLineRe(key string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
}

// tableRe finds a table header such as [section] or [[array]].
var tableRe = regexp.MustCompile(`^\s*\[`)

// replaceArrayLine swaps the assignment of key in a TOML text for a new string array.
// A value that spans several lines is replaced as a whole. If the key is missing
// at the top level, the new line is added before the first table header (or at the end).
func replaceArrayLine(src []byte, key string, names []string) []byte {
	newLine := key + "  = " + tomlStringArray(names)
	lineRe := keyLineRe(key)
	lines := strings.Split(string(src), "\n")

	firstTable := len(lines)
	for i, ln := range lines {
		if tableRe.MatchString(ln) {
			// "hidden" must come before the first table header to be a top-level key.
			firstTable = i
			break
		}
	}
	for i := 0; i < firstTable; i++ {
		if !lineRe.MatchString(lines[i]) {
			continue
		}
		end := i
		if !arrayClosed(lines[i]) {
			for end+1 < len(lines) {
				end++
				if arrayClosed(strings.Join(lines[i:end+1], "\n")) {
					break
				}
			}
		}
		// Keep a trailing comment on a one-line value, if there was one.
		if end == i {
			if c := trailingComment(lines[i]); c != "" {
				newLine += "  " + c
			}
		}
		out := append([]string{}, lines[:i]...)
		out = append(out, newLine)
		out = append(out, lines[end+1:]...)
		return []byte(strings.Join(out, "\n"))
	}

	// No such key: insert it before the first table, or at the end of the file.
	out := append([]string{}, lines[:firstTable]...)
	if firstTable == len(lines) {
		// Drop the empty element made by a final newline, then add our line.
		for len(out) > 0 && out[len(out)-1] == "" {
			out = out[:len(out)-1]
		}
		out = append(out, newLine, "")
		return []byte(strings.Join(out, "\n"))
	}
	out = append(out, newLine, "")
	out = append(out, lines[firstTable:]...)
	return []byte(strings.Join(out, "\n"))
}

// arrayClosed reports whether the brackets in s are balanced, ignoring text
// inside strings and comments. s starts at "hidden = ...".
func arrayClosed(s string) bool {
	depth := 0
	seen := false
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inStr != 0:
			if ch == '\\' && inStr == '"' {
				i++
			} else if ch == inStr {
				inStr = 0
			}
		case ch == '"' || ch == '\'':
			inStr = ch
		case ch == '#':
			// Skip to the end of this line.
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case ch == '[':
			depth++
			seen = true
		case ch == ']':
			depth--
		}
	}
	return seen && depth <= 0
}

// trailingComment returns the "# ..." part at the end of a one-line value.
func trailingComment(line string) string {
	inStr := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case inStr != 0:
			if ch == '\\' && inStr == '"' {
				i++
			} else if ch == inStr {
				inStr = 0
			}
		case ch == '"' || ch == '\'':
			inStr = ch
		case ch == '#':
			return strings.TrimSpace(line[i:])
		}
	}
	return ""
}

// arrayMatches decodes text and checks that the string array under key equals names.
func arrayMatches(text []byte, key string, names []string) bool {
	var probe map[string]any
	if _, err := toml.Decode(string(text), &probe); err != nil {
		return false
	}
	raw, _ := probe[key].([]any)
	var list []string
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return false
		}
		list = append(list, s)
	}
	got := cleanList(list)
	if len(got) != len(names) {
		return false
	}
	for i := range got {
		if got[i] != names[i] {
			return false
		}
	}
	return true
}

// reencode is the fallback: decode the old text as it is (no "~" expansion),
// change the key, and write the whole file again. Comments are lost.
func reencode(old []byte, key string, names []string) ([]byte, error) {
	c := Default()
	if _, err := toml.Decode(string(old), c); err != nil {
		return nil, fmt.Errorf("config: parse existing file: %w", err)
	}
	switch key {
	case "hidden":
		c.Hidden = names
	default:
		return nil, fmt.Errorf("config: cannot rewrite key %q", key)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return nil, fmt.Errorf("config: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// tomlStringArray renders ["a", "b"] with TOML escaping.
func tomlStringArray(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = tomlQuote(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// writeAtomic writes data to a temp file in the same folder and renames it over path.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil { // private (security M-1)
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// cleanList trims entries, drops empty ones and removes duplicates. Order is kept.
// It always returns a non-nil slice.
func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// expandHome turns "~" and "~/x" into the home folder path. Other values are unchanged.
func expandHome(p string) string {
	if p == "~" {
		return homeDir()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}

// projectMarkers are files or folders whose presence says "this folder is a project".
var projectMarkers = []string{".git", ".fslckout", "_FOSSIL_", "go.mod", "package.json", "Cargo.toml", "pyproject.toml", ".drashta"}

func looksLikeProject(dir string) bool {
	for _, m := range projectMarkers {
		if _, err := os.Lstat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// DiscoverRoots finds the group folders inside projectsDir: every visible folder that
// is not a project itself but has at least one visible folder inside. It is what
// "roots" means when config.toml does not set it. The result is sorted; it is empty
// when projectsDir is missing or has nothing that looks like a group.
func DiscoverRoots(projectsDir string) []string {
	des, err := os.ReadDir(projectsDir)
	if err != nil && len(des) == 0 {
		return []string{}
	}
	out := []string{}
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		dir := filepath.Join(projectsDir, name)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() || looksLikeProject(dir) {
			continue
		}
		subs, _ := os.ReadDir(dir)
		for _, sub := range subs {
			if strings.HasPrefix(sub.Name(), ".") {
				continue
			}
			if st, err := os.Stat(filepath.Join(dir, sub.Name())); err == nil && st.IsDir() {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// discoverProfiles returns ~/.claude plus every existing ~/.claude-* folder, "~" expanded.
func discoverProfiles() []string {
	home := homeDir()
	out := []string{filepath.Join(home, ".claude")}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude-*"))
	sort.Strings(matches)
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.IsDir() {
			out = append(out, m)
		}
	}
	return out
}
