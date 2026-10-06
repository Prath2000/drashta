package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// DefaultAuthor is used when the caller does not know who is acting (04-data-model.md,
// open question 5): a Claude session without its id is just "Claude".
const DefaultAuthor = "Claude"

// Env is everything Run needs from the outside, so tests can control it.
type Env struct {
	// Cwd is the folder the command runs in. Empty means the process working folder.
	Cwd string
	// Home is the user's home folder. A .drashta folder directly in Home is drashta's own data
	// folder (~/.drashta), never a project, and the search for a project stops there.
	// Empty means os.UserHomeDir().
	Home string
	// ProjectsDir is ~/projects. A folder <ProjectsDir>/<group>/<name> is a known project root,
	// used when no .drashta folder is found above Cwd. Empty turns this off.
	ProjectsDir string
	// Author is written into new items and log lines. Empty means "Claude".
	Author string
	// Now gives the current time. Nil means time.Now.
	Now func() time.Time
}

// DefaultEnv builds an Env from the real process: working folder, home, ~/projects, and the
// DRASHTA_AUTHOR variable (for example "session:121c3b19-ada3-495e-a518-7fe5acf0a71e").
func DefaultEnv() Env {
	env := Env{Author: os.Getenv("DRASHTA_AUTHOR"), Now: time.Now}
	env.Cwd, _ = os.Getwd()
	if home, err := os.UserHomeDir(); err == nil {
		env.Home = home
		env.ProjectsDir = filepath.Join(home, "projects")
	}
	return env
}

func (e Env) fill() Env {
	if e.Cwd == "" {
		e.Cwd, _ = os.Getwd()
	}
	if e.Home == "" {
		e.Home, _ = os.UserHomeDir()
	}
	if strings.TrimSpace(e.Author) == "" {
		e.Author = DefaultAuthor
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	return e
}

// app is one run of the command.
type app struct {
	out  io.Writer
	err  io.Writer
	env  Env
	root string // project folder; set by project()
}

const usageText = `usage: drashta <command> ...

Tasks
  drashta task new <title> [--feature F-3] [--status todo] [--assignee name] [--body text] [--link ref]... [-q]
  drashta task list [--status review] [--feature F-1] [--json]
  drashta task show <T-id> [--json]
  drashta task status <T-id> <backlog|todo|in_progress|review|done> [--user-said-done]
  drashta task link <T-id> <ref>

Notes
  drashta note add <ref|id> <text> [--x 0.46 --y 0.2] [--line 30] [--reply-to N-1] [-q]
  drashta note list [--ref ref] [--all] [--json]
  drashta note resolve <N-id>

Snapshots
  drashta snap add --task T-12 (--image file | --path file) [--title text] [--note text] [-q]
  drashta snap list [--task T-12] [--json]

Options (any command)
  -C, --project dir   the project folder (default: found by walking up from here)
  --author name       who is acting (default: $DRASHTA_AUTHOR, else Claude)
  -h, --help          show this help

A ref looks like commit:abc1234, file:src/x.go#L40, server:8090, task:T-12, snapshot:T-12/v3.
A bare id (T-12, F-3, M-1, N-40, T-12/v3) works as a ref too.
A text that starts with a dash needs "--" before it.
`

// Run runs one "drashta task|note|snap ..." command. args are the command line arguments
// without the program name (args[0] is "task", "note" or "snap"). It writes results to
// stdout and problems to stderr, and returns the exit code: 0 done, 1 failed, 2 wrong usage.
//
// It talks to internal/authored directly, so it works with the server stopped.
func Run(args []string, stdout, stderr io.Writer, env Env) int {
	a := &app{out: stdout, err: stderr, env: env.fill()}
	err := a.dispatch(args)
	var help helpRequested
	var uerr *usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &help):
		fmt.Fprint(stdout, usageText)
		return 0
	case errors.As(err, &uerr):
		if strings.HasPrefix(uerr.msg, "usage:") {
			fmt.Fprintln(stderr, uerr.msg)
		} else {
			fmt.Fprintf(stderr, "drashta: %s\n", uerr.msg)
			fmt.Fprintln(stderr, "run 'drashta --help' to see the commands")
		}
		return 2
	default:
		fmt.Fprintf(stderr, "drashta: %v\n", err)
		return 1
	}
}

// splitLead takes the options that every command shares (-C, --project, --author, -h) from the
// front of args, so "drashta -C dir task list" and "drashta task -C dir list" both work.
func splitLead(args []string) (lead, rest []string, err error) {
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "-C" || a == "--project" || a == "--author":
			if len(args) < 2 {
				return nil, nil, usagef("option %s needs a value", a)
			}
			lead = append(lead, a, args[1])
			args = args[2:]
		case strings.HasPrefix(a, "--project=") || strings.HasPrefix(a, "--author=") || a == "-h" || a == "--help":
			lead = append(lead, a)
			args = args[1:]
		default:
			return lead, args, nil
		}
	}
	return lead, args, nil
}

func (a *app) dispatch(args []string) error {
	lead, args, err := splitLead(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		for _, l := range lead {
			if l == "-h" || l == "--help" {
				return helpRequested{}
			}
		}
		return usagef("usage: drashta <task|note|snap> ...")
	}
	cmd, rest := args[0], append(append([]string{}, args[1:]...), lead...)
	switch cmd {
	case "help":
		return helpRequested{}
	case "task":
		return a.task(rest)
	case "note":
		return a.note(rest)
	case "snap":
		return a.snap(rest)
	}
	return usagef("unknown command %q", cmd)
}

// sub finds the handler for "<subcommand> args...".
func sub(args []string, group string, handlers map[string]func([]string) error) error {
	lead, args, err := splitLead(args)
	if err != nil {
		return err
	}
	names := strings.Join(slices.Sorted(maps.Keys(handlers)), "|")
	if len(args) == 0 {
		for _, l := range lead {
			if l == "-h" || l == "--help" {
				return helpRequested{}
			}
		}
		return usagef("usage: drashta %s <%s>", group, names)
	}
	h, ok := handlers[args[0]]
	if !ok {
		if args[0] == "-h" || args[0] == "--help" {
			return helpRequested{}
		}
		return usagef("unknown command \"%s %s\" (use one of: %s)", group, args[0], strings.ReplaceAll(names, "|", ", "))
	}
	return h(append(append([]string{}, args[1:]...), lead...))
}

func (a *app) author(p *parsed) (string, error) {
	who := a.env.Author
	if v := p.str("author"); v != "" {
		who = v
	}
	who = strings.TrimSpace(who)
	if who == "" || strings.ContainsAny(who, " \t\r\n") {
		return "", usagef("author %q must be one word, for example alice, Claude or session:<uuid>", who)
	}
	return who, nil
}

// isHuman is false for Claude: the CLI keeps a Claude session inside the ownership rules of
// 04-data-model.md section 2.5.
func isHuman(author string) bool {
	return !strings.EqualFold(author, "claude") && !strings.HasPrefix(strings.ToLower(author), "session:")
}

func (a *app) printf(format string, args ...any) { fmt.Fprintf(a.out, format, args...) }

func (a *app) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.out, "%s\n", b)
	return err
}

// warnAll prints loader problems to stderr and keeps going.
func (a *app) warnAll(errs []error) {
	for _, e := range errs {
		fmt.Fprintf(a.err, "drashta: %v\n", e)
	}
}

// oneLine shortens text to a single line of at most n characters.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}
