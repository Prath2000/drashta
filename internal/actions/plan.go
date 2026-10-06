package actions

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// Errors. The HTTP layer maps ErrForbidden and ErrInvalid to 400,
// ErrNotFound to 404, ErrConfirm to 400, ErrConflict to 409.
var (
	ErrForbidden = errors.New("actions: forbidden")
	ErrInvalid   = errors.New("actions: invalid target")
	ErrAction    = errors.New("actions: action not allowed for this item")
	ErrNotFound  = errors.New("actions: not found")
)

// Target types.
const (
	TypeFile     = "file"
	TypeFolder   = "folder"
	TypeProject  = "project"
	TypeImage    = "image"
	TypeSnapshot = "snapshot"
	TypeCommit   = "commit"
	TypeSession  = "session"
	TypeServer   = "server"
	TypeURL      = "url"
	TypeTask     = "task"
	TypeNote     = "note"
)

// Action names (04 §6 POST /open). "" means ActionDefault.
const (
	ActionDefault  = "default"
	ActionEditor   = "editor"
	ActionFinder   = "finder"
	ActionTerminal = "terminal"
	ActionSafari   = "safari"
	ActionResume   = "resume"
	ActionApp      = "app" // "open <path>": the macOS default app (Finder for a folder)
)

// Target is an item already resolved from a ref by the index. Nothing in it
// is trusted: Plan checks every field it uses.
type Target struct {
	Type        string // one of the Type* constants
	Ref         string // the original ref, for the log only
	ProjectRoot string // absolute project folder (from the index)
	Path        string // file/folder/image/snapshot/task/note path, relative to ProjectRoot (absolute is allowed if inside Roots)
	Line        int    // file line, 0 = none
	SHA         string // commit id
	VCS         string // "git" | "fossil"
	SessionID   string // Claude session UUID
	Profile     string // Claude profile dir, absolute (CLAUDE_CONFIG_DIR)
	Port        string // server port as text, for example "5173"
	URL         string // url target
}

// Command is a program and its argv. It is never run through a shell.
type Command struct {
	Name string
	Args []string
}

// String gives a readable one-line form for logs and the API "ran" field.
func (c Command) String() string {
	return strings.Join(append([]string{c.Name}, c.Args...), " ")
}

// Config holds what Plan needs from config.toml.
type Config struct {
	Editor   []string // argv template, for example ["code","-g","{path}:{line}"]
	Terminal string   // app for "open -a <terminal> <path>", default "Terminal"
	Roots    []string // allowed roots (the projects dir); every path must be inside one
	// DesktopScript is the plash-mode script (config.toml desktop_script); PlanDesktop runs it.
	DesktopScript string
}

// DefaultEditor is the editor template used when Config.Editor is empty.
var DefaultEditor = []string{"code", "-g", "{path}:{line}"}

var (
	gitSHARe    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	fossilSHARe = regexp.MustCompile(`^[0-9a-f]{6,64}$`)
	uuidRe      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	portRe      = regexp.MustCompile(`^[0-9]{1,5}$`)
	appNameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
)

// terminalScript is the FIXED AppleScript used to run a command in Terminal.
// argv item 1 is the folder, the rest is the command argv. Every item is
// shell-quoted with "quoted form of", so no data is ever built into script
// source and no data is ever parsed by the shell as code.
var terminalScript = []string{
	"on run argv",
	"set cmd to \"cd \" & quoted form of (item 1 of argv) & \" &&\"",
	"repeat with i from 2 to count of argv",
	"set cmd to cmd & \" \" & quoted form of (item i of argv)",
	"end repeat",
	"tell application \"Terminal\"",
	"activate",
	"do script cmd",
	"end tell",
	"end run",
}

// terminalCommand wraps argv to run in a new Terminal window inside dir.
func terminalCommand(dir string, argv ...string) Command {
	args := make([]string, 0, 2*len(terminalScript)+2+len(argv))
	for _, line := range terminalScript {
		args = append(args, "-e", line)
	}
	args = append(args, "--", dir)
	args = append(args, argv...)
	return Command{Name: "osascript", Args: args}
}

// Plan builds the command for action on t with the default Config
// (no Roots, so every path target is refused). Use Config.Plan in real code.
func Plan(t Target, action string) (Command, error) {
	return Config{}.Plan(t, action)
}

// Plan builds the native command for action on t. It runs nothing. Every
// path goes through pathguard and must be inside c.Roots; every data
// argument is format-checked and never starts with "-".
func (c Config) Plan(t Target, action string) (Command, error) {
	if action == "" {
		action = ActionDefault
	}
	switch t.Type {
	case TypeFile:
		switch action {
		case ActionDefault, ActionEditor:
			p, err := c.path(t)
			if err != nil {
				return Command{}, err
			}
			return c.editor(p, t.Line)
		case ActionFinder:
			return c.reveal(t)
		case ActionApp:
			return c.defaultApp(t)
		}
	case TypeFolder, TypeProject:
		switch action {
		case ActionDefault, ActionFinder:
			return c.reveal(t)
		case ActionApp:
			return c.defaultApp(t)
		case ActionEditor:
			if t.Type != TypeProject {
				break
			}
			// The project folder in the editor: the file template without a line number.
			p, err := c.path(t)
			if err != nil {
				return Command{}, err
			}
			return c.editorNoLine(p)
		case ActionSafari:
			if t.Type != TypeProject {
				break
			}
			// Only a GitHub remote, as https://github.com/<owner>/<repo>.
			u, err := GitHubURL(t.URL)
			if err != nil {
				return Command{}, err
			}
			if _, err := c.root(t); err != nil {
				return Command{}, err
			}
			return safari(u)
		case ActionTerminal:
			p, err := c.path(t)
			if err != nil {
				return Command{}, err
			}
			term, err := c.terminal()
			if err != nil {
				return Command{}, err
			}
			return Command{Name: "open", Args: []string{"-a", term, p}}, nil
		}
	case TypeImage, TypeSnapshot:
		switch action {
		case ActionDefault:
			p, err := c.path(t)
			if err != nil {
				return Command{}, err
			}
			return Command{Name: "open", Args: []string{"-a", "Preview", p}}, nil
		case ActionFinder:
			return c.reveal(t)
		case ActionApp:
			return c.defaultApp(t)
		}
	case TypeCommit:
		if action == ActionDefault || action == ActionTerminal {
			return c.commit(t)
		}
	case TypeSession:
		if action == ActionDefault || action == ActionResume || action == ActionTerminal {
			return c.session(t)
		}
	case TypeServer:
		if action == ActionDefault || action == ActionSafari {
			if !validPort(t.Port) {
				return Command{}, fmt.Errorf("%w: port %q", ErrInvalid, t.Port)
			}
			return safari("http://localhost:" + t.Port)
		}
	case TypeURL:
		if action == ActionDefault || action == ActionSafari {
			u, err := CheckURL(t.URL)
			if err != nil {
				return Command{}, err
			}
			return safari(u)
		}
	case TypeTask, TypeNote:
		if action == ActionDefault || action == ActionEditor {
			p, err := c.path(t)
			if err != nil {
				return Command{}, err
			}
			root, _ := c.root(t)
			if !pathguard.Inside(filepath.Join(root, ".drashta"), p) || !strings.HasSuffix(p, ".md") {
				return Command{}, fmt.Errorf("%w: %s is not a .drashta file", ErrForbidden, t.Type)
			}
			return c.editor(p, 0)
		}
	default:
		return Command{}, fmt.Errorf("%w: unknown type %q", ErrInvalid, t.Type)
	}
	return Command{}, fmt.Errorf("%w: %q on %s", ErrAction, action, t.Type)
}

// root resolves t.ProjectRoot and requires it inside c.Roots.
func (c Config) root(t Target) (string, error) {
	if t.ProjectRoot == "" {
		return "", fmt.Errorf("%w: no project root", ErrInvalid)
	}
	r, err := pathguard.ResolveAny(c.Roots, t.ProjectRoot)
	return r, guardErr(err)
}

// path resolves t.Path inside the project root (symlinks resolved).
func (c Config) path(t Target) (string, error) {
	root, err := c.root(t)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(t.Path) {
		p, err := pathguard.ResolveAny([]string{root}, t.Path)
		return p, guardErr(err)
	}
	// Resolve maps "../x" onto root/x; refuse ".." outright so a bad ref never
	// silently opens a different file.
	for _, part := range strings.Split(filepath.ToSlash(t.Path), "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: path has ..", ErrForbidden)
		}
	}
	p, err := pathguard.Resolve(root, t.Path)
	return p, guardErr(err)
}

func guardErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pathguard.ErrOutside):
		return fmt.Errorf("%w: %v", ErrForbidden, err)
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", ErrForbidden, err)
	}
}

func (c Config) reveal(t Target) (Command, error) {
	p, err := c.path(t)
	if err != nil {
		return Command{}, err
	}
	return Command{Name: "open", Args: []string{"-R", p}}, nil
}

// defaultApp opens the path with the app macOS picks for it ("--" so a path is never a flag).
func (c Config) defaultApp(t Target) (Command, error) {
	p, err := c.path(t)
	if err != nil {
		return Command{}, err
	}
	return Command{Name: "open", Args: []string{"--", p}}, nil
}

func (c Config) terminal() (string, error) {
	term := c.Terminal
	if term == "" {
		term = "Terminal"
	}
	if !appNameRe.MatchString(term) {
		return "", fmt.Errorf("%w: terminal app %q", ErrInvalid, term)
	}
	return term, nil
}

// editor fills the template. {path} and {line} are replaced; if no item
// holds {path}, the path is appended. Line 0 becomes 1.
func (c Config) editor(p string, line int) (Command, error) {
	tpl := c.Editor
	if len(tpl) == 0 {
		tpl = DefaultEditor
	}
	if strings.Contains(tpl[0], "{") || strings.HasPrefix(tpl[0], "-") {
		return Command{}, fmt.Errorf("%w: bad editor template", ErrInvalid)
	}
	if err := noDash(p); err != nil {
		return Command{}, err
	}
	if line < 1 {
		line = 1
	}
	r := strings.NewReplacer("{path}", p, "{line}", strconv.Itoa(line))
	args := make([]string, 0, len(tpl))
	hasPath := false
	for _, a := range tpl[1:] {
		if strings.Contains(a, "{path}") {
			hasPath = true
		}
		args = append(args, r.Replace(a))
	}
	if !hasPath {
		args = append(args, p)
	}
	return Command{Name: tpl[0], Args: args}, nil
}

// editorNoLine fills the editor template for a folder: "{path}:{line}" becomes "{path}",
// any other {line} becomes 1.
func (c Config) editorNoLine(p string) (Command, error) {
	tpl := c.Editor
	if len(tpl) == 0 {
		tpl = DefaultEditor
	}
	noLine := make([]string, len(tpl))
	for i, a := range tpl {
		noLine[i] = strings.ReplaceAll(a, "{path}:{line}", "{path}")
	}
	return Config{Editor: noLine}.editor(p, 1)
}

var ghPartRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)

// GitHubURL normalises a git remote to https://github.com/<owner>/<repo>. It accepts
// https://github.com/o/r(.git), http(s)://user@github.com/o/r, git@github.com:o/r(.git) and
// ssh://git@github.com/o/r(.git). Any other remote (another host, file://, a local path)
// gives ErrForbidden.
func GitHubURL(remote string) (string, error) {
	s := strings.TrimSpace(remote)
	bad := fmt.Errorf("%w: remote %q is not a GitHub repository", ErrForbidden, remote)
	var rest string
	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		rest = strings.TrimPrefix(s, "git@github.com:")
	default:
		u, err := url.Parse(s)
		if err != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
			return "", bad
		}
		switch u.Scheme {
		case "https", "ssh", "git":
		default:
			return "", bad
		}
		if !strings.EqualFold(u.Hostname(), "github.com") {
			return "", bad
		}
		rest = strings.TrimPrefix(u.Path, "/")
	}
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || !ghPartRe.MatchString(parts[0]) || !ghPartRe.MatchString(parts[1]) ||
		parts[0] == ".." || parts[1] == ".." {
		return "", bad
	}
	return CheckURL("https://github.com/" + parts[0] + "/" + parts[1])
}

func (c Config) commit(t Target) (Command, error) {
	root, err := c.root(t)
	if err != nil {
		return Command{}, err
	}
	switch t.VCS {
	case "git", "":
		if !gitSHARe.MatchString(t.SHA) {
			return Command{}, fmt.Errorf("%w: git sha %q", ErrInvalid, t.SHA)
		}
		// Hardened like the git probe: the project's own .git/config must not pick
		// the pager, an external diff, a textconv filter or an fsmonitor hook.
		return terminalCommand(root, "git", "-c", "core.pager=less", "-c", "core.fsmonitor=false",
			"-C", root, "show", "--no-ext-diff", "--no-textconv", t.SHA, "--"), nil
	case "fossil":
		if !fossilSHARe.MatchString(t.SHA) {
			return Command{}, fmt.Errorf("%w: fossil hash %q", ErrInvalid, t.SHA)
		}
		return terminalCommand(root, "fossil", "diff", "--checkin", t.SHA), nil
	}
	return Command{}, fmt.Errorf("%w: vcs %q", ErrInvalid, t.VCS)
}

func (c Config) session(t Target) (Command, error) {
	if !uuidRe.MatchString(t.SessionID) {
		return Command{}, fmt.Errorf("%w: session id %q", ErrInvalid, t.SessionID)
	}
	root, err := c.root(t)
	if err != nil {
		return Command{}, err
	}
	argv := []string{}
	if t.Profile != "" {
		prof := filepath.Clean(t.Profile)
		if !filepath.IsAbs(prof) || strings.ContainsAny(prof, "\n\r\x00") {
			return Command{}, fmt.Errorf("%w: profile %q", ErrInvalid, t.Profile)
		}
		argv = append(argv, "env", "CLAUDE_CONFIG_DIR="+prof)
	}
	argv = append(argv, "claude", "--resume", t.SessionID)
	return terminalCommand(root, argv...), nil
}

func validPort(s string) bool {
	if !portRe.MatchString(s) {
		return false
	}
	n, _ := strconv.Atoi(s)
	return n > 0 && n <= 65535
}

// CheckURL allows only http://localhost:<port>[/path] or
// http://127.0.0.1:<port>[/path], and https://github.com/... . It returns the
// normalized URL.
func CheckURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || strings.HasPrefix(raw, "-") {
		return "", fmt.Errorf("%w: url %q", ErrForbidden, raw)
	}
	switch u.Scheme {
	case "http":
		h := u.Hostname()
		if (h != "localhost" && h != "127.0.0.1") || !validPort(u.Port()) {
			return "", fmt.Errorf("%w: url %q", ErrForbidden, raw)
		}
	case "https":
		if u.Host != "github.com" {
			return "", fmt.Errorf("%w: url %q", ErrForbidden, raw)
		}
	default:
		return "", fmt.Errorf("%w: url %q", ErrForbidden, raw)
	}
	return u.String(), nil
}

func safari(u string) (Command, error) {
	if err := noDash(u); err != nil {
		return Command{}, err
	}
	return Command{Name: "open", Args: []string{"-a", "Safari", u}}, nil
}

func noDash(s string) error {
	if s == "" || strings.HasPrefix(s, "-") {
		return fmt.Errorf("%w: argument %q", ErrInvalid, s)
	}
	return nil
}
