// Package actions runs the native "open out" commands and the server
// kill/restart actions (03-architecture.md section 7).
//
// Rules:
//   - The page never sends a path or a command. The HTTP layer resolves a ref
//     to a Target from the index; Plan builds the command from it.
//   - No shell. Commands are argv only. The single exception is server restart,
//     which replays a command line drashta itself recorded ("sh -c").
//   - Every path goes through pathguard and must be inside Config.Roots.
//   - Data arguments are format-checked (sha, UUID, port, URL) and never start with "-".
//   - Terminal commands use osascript with a FIXED script; the command is
//     passed as argv items after "--" and quoted inside AppleScript with
//     "quoted form of". The script always targets Terminal.app.
//   - Kill and restart need confirm=true.
//
// Exported API:
//
//	ErrForbidden, ErrInvalid, ErrAction, ErrNotFound   Plan errors (path outside roots, bad format, action not allowed, missing).
//	ErrConfirm, ErrNotLive, ErrConflict                Kill/restart errors (confirm missing, pid not live, alive or pending).
//	Type* constants                                    file folder project image snapshot commit session server url task note.
//	Action* constants                                  default editor finder terminal safari resume app ("" = default).
//	                                                   app = "open -- <path>": the macOS default app (file, image, folder, project).
//	type Target                                        Resolved item: Type, Ref, ProjectRoot, Path, Line, SHA, VCS, SessionID, Profile, Port, URL.
//	type Command{Name; Args}                           A planned argv; String() gives "name arg arg".
//	type Config{Editor; Terminal; Roots}               Editor argv template ({path},{line}), terminal app, allowed roots.
//	DefaultEditor                                      ["code","-g","{path}:{line}"].
//	(Config).Plan(t, action) (Command, error)          Builds the command; runs nothing.
//	Plan(t, action) (Command, error)                   Plan with an empty Config (no roots: path targets are refused).
//	CheckURL(raw) (string, error)                      Allows http://localhost|127.0.0.1:<port>[/...] and https://github.com/... only.
//	GitHubURL(remote) (string, error)                  Git remote -> https://github.com/<owner>/<repo>; anything else ErrForbidden.
//	type Executor interface{ Run(Command) error }
//	ExecExecutor                                       Real executor through runx (no shell).
//	RecorderExecutor{Cmds; Err}                        Records commands (tests).
//	(Config).Do(ctx, ex, t, action) (ran string, err)  Plan + Run + slog "action=<type> ref=<ref> ok=<bool>".
//	Do(ctx, ex, t, action) (string, error)             Do with an empty Config.
//	RestartGrace                                       10 s: how long a restart blocks another restart of the same key.
//	type Manager                                       Holds the live set and the restarting grace map.
//	NewManager(logDir, lookup) *Manager                logDir = <DataDir>/logs; lookup = History.Lookup.
//	(*Manager).SetLive(live, now)                      After each successful scan: new live set, clears done/expired restarts.
//	(*Manager).Kill(pid, key, confirm) error           SIGTERM, SIGKILL after 3 s; pid must be live; key (if set) must match.
//	(*Manager).Restart(key, confirm) (logPath, error)  Replays recorded (cwd, cmd) with sh -c; log to <logDir>/<port>.log.
//	                                                   Refused (ErrForbidden) unless CheckRestartable passes.
//	CheckRestartable(s servers.Server) error           Only kind "dev" servers inside a project, and no shell
//	                                                   metacharacter (; | & < > $ ` \ " ' ( ) { } * ? ~ ! newline) in the cmd.
//	(*Manager).Pending(key) bool                       A restart of key is in its grace period.
//	LogPath(logDir, port) string                       <logDir>/<port>.log.
//	type PageInfo{HTTP; Title; Frameable; Reason}      What ProbePage learned: answers HTTP, <title>, may an iframe show it, why not.
//	ProbePage(port, parentOrigin) PageInfo             One GET of / (127.0.0.1, then [::1]; 2 s; 64 KiB; loopback redirects only).
//	                                                   Not frameable: X-Frame-Options DENY|SAMEORIGIN, or a Content-Security-Policy
//	                                                   frame-ancestors with no source matching parentOrigin ('none' and 'self' never
//	                                                   match, Report-Only is ignored). HTTP false: Reason "not a web page".
//	PageTitle(port) string                             ProbePage(port, "").Title; "" on failure.
//	DesktopModes                                       browse wallpaper blank (the Plash desktop modes).
//	(Config).PlanDesktop(mode) (Command, error)        "<DesktopScript> <mode>"; mode from DesktopModes (else ErrInvalid), script
//	                                                   from config.toml only (empty: ErrAction), inside Roots (pathguard).
//	ReadDesktopMode(stateFile) string                  The mode plash-mode last wrote; "" when missing or unknown.
//
// Project targets also allow action editor (the editor template on the project folder,
// "{path}:{line}" becomes "{path}") and safari (Target.URL = the git remote; only a GitHub
// remote, opened as https://github.com/<owner>/<repo>).
//
// Mapping to HTTP: ErrForbidden/ErrInvalid/ErrAction/ErrConfirm/ErrNotLive -> 400,
// ErrNotFound -> 404, ErrConflict -> 409.
package actions
