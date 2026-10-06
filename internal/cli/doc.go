// Package cli implements the "drashta task|note|snap ..." command line (04-data-model.md
// section 2.5, roadmap phase 3 step 9).
//
// It talks to internal/authored directly, so it works with the server stopped. Every write
// is a locked read-modify-write (authored.Update*), so two sessions can use it at the same
// time. The CLI also applies the ownership rules for Claude sessions: a session cannot set
// a task to done unless the caller adds --user-said-done, and the CLI has no command that
// edits titles, bodies, assignees or verdicts, or deletes files.
//
// # Exported API
//
//	Run(args []string, stdout, stderr io.Writer, env Env) int
//	    Runs one command. args are the command line arguments WITHOUT the program name:
//	    args[0] is "task", "note" or "snap" (cmd/drashta/main.go passes os.Args[1:] when
//	    the first argument is one of these). Results go to stdout, problems to stderr.
//	    Exit code: 0 done, 1 failed, 2 wrong usage. "help" and "-h" print the usage and give 0.
//	Env
//	    Cwd          folder to run in ("" = process working folder)
//	    Home         user home; ~/.drashta is never a project ("" = os.UserHomeDir)
//	    ProjectsDir  ~/projects; <ProjectsDir>/<group>/<name> is a known project root ("" = off)
//	    Author       who is acting ("" = "Claude")
//	    Now          clock (nil = time.Now)
//	DefaultEnv() Env
//	    Env from the real process: working folder, home, ~/projects, $DRASHTA_AUTHOR.
//	DefaultAuthor
//	    "Claude".
//
// # Finding the project
//
// 1. -C dir (or --project dir). 2. The nearest folder above Cwd that has a .drashta folder
// (the search stops at Home). 3. The folder <ProjectsDir>/<group>/<name> that contains Cwd;
// its .drashta (with README.md and .gitignore) is created by the first write.
//
// # Commands
//
//	task new <title> [--feature F-3] [--status todo] [--assignee name] [--body text] [--link ref]... [-q]
//	task list [--status s] [--feature F-1] [--json]
//	task show <T-id> [--json]
//	task status <T-id> <status> [--user-said-done]     adds a log line
//	task link <T-id> <ref>                              adds to links:, adds a log line
//	note add <ref|id> <text> [--x --y] [--line N] [--reply-to N-1] [-q]
//	note list [--ref ref] [--all] [--json]
//	note resolve <N-id>
//	snap add --task T-12 (--image file | --path file) [--title text] [--note text] [-q]
//	    Copies the file to snapshots/T-12/v<N>.<ext>, writes v<N>.md (verdict pending, HEAD
//	    commit if the project is a git repo), moves the task from in_progress to review
//	    and adds a log line. --path must be inside the project. URL capture is not built.
//	snap list [--task T-12] [--json]
//	Options on every command: -C/--project dir, --author name, -h/--help.
package cli
