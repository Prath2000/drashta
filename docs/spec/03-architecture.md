# drashta v1: system architecture

This file describes how drashta v1 is built. `00-decisions.md` overrides this file if they disagree.
The detailed SQLite schema is in `04-data-model.md`. This file only refers to it.

Facts from the local machine (checked 2026-10-01, read-only):

- Go 1.26.3 (darwin/arm64). `git` (/usr/bin), `fossil`, `gh`, `node` (/opt/homebrew/bin), `lsof` (/usr/sbin) are installed.
- `~/projects` has 3 groups and about 30 project folders. Without skip-dirs it has about 5,600 directories and 24,500 files.
- Mixed VCS: some projects are git only (lib, gadgets, widgets, gizmos), some are fossil only (acme), acme is both, and many have no VCS.
- The 5 Claude profiles hold about 1 GB of `.jsonl` (278 files). `~/.claude-c` alone is 610 MB.
- `launchctl limit maxfiles` is **256 soft**. This matters for file watching (see section 4).
- acme runs on 4444 and keeps its data in `~/.acme-monitor/`. drashta does not touch either.

---

## 1. Overview

```
 ┌──────────────── Mac desktop ─────────────────┐
 │ Plash (WKWebView wallpaper)   Safari (fallback)│
 │   GET /  (SPA)   EventSource /api/events      │
 └───────────────┬──────────────────────────────┘
                 │ HTTP, 127.0.0.1:4455 only
 ┌───────────────▼──────────────────────────────────────────────┐
 │ drashta (one Go binary, started by launchd in.drashta.plist)  │
 │                                                              │
 │  httpapi ── guard (Host/Origin/CSRF) ── embedded web/ (SPA)  │
 │     │   ▲ SSE hub (events, seq numbers)                      │
 │     ▼   │                                                    │
 │  actions (exec, no shell)      indexer (scheduler, debounce) │
 │                                    │                         │
 │         probes: projects · tree · git · fossil · gh ·        │
 │                 servers(lsof) · claude · docs · authored     │
 │                                    │                         │
 │                   store (SQLite ~/.drashta/index.db)         │
 └──────┬──────────┬──────────┬──────────┬──────────┬───────────┘
        ▼          ▼          ▼          ▼          ▼
  ~/projects/  git / fossil   gh CLI   lsof, ps   ~/.claude*/projects/**/*.jsonl
  (FSEvents)   CLIs           (api)               (FSEvents)
```

- **Summary data** (home, activity, search) comes from SQLite. **Detail data** (a diff, a file body, a full transcript) is fetched live by a probe on request (decision S7).
- SQLite is a cache and a history log. The filesystem, VCS and jsonl files are the source of truth.
- The server pushes changes over SSE. The page never polls (D5).

---

## 2. Repo layout

The Go module is `drashta` at `~/projects/personal/drashta`. Module `go 1.26`. The frontend lives in `web/` and is embedded.

```
drashta/
  go.mod  go.sum  Makefile  in.drashta.plist
  cmd/drashta/main.go            flags, config load, single-instance check, wiring
  internal/
    config/     config.go        ~/.drashta/config.toml (BurntSushi/toml)
    store/      store.go, migrate.go, migrations/*.sql, fts.go
    probe/      probe.go         interface Probe{ Name(); Scan(ctx, Project) }
      projects/ tree/ git/ fossil/ gh/ servers/ claude/ docs/ authored/
    indexer/    indexer.go, watch_fsevents.go, sweep.go, activity.go
    live/       hub.go           SSE hub, ring buffer, Last-Event-ID
    actions/    actions.go, allow.go, kill.go, restart.go   (from acme actions.go)
    httpapi/    routes.go, guard.go, api_*.go, spa.go
    runx/       run.go           exec with timeout, output cap, ctx, no shell
  web/                           React Router v7, SPA mode (ssr:false), Vite, TypeScript
    app/ routes/ components/ lib/api.ts lib/events.ts
    build/client/                build output, embedded
    embed.go                     //go:embed all:build/client
  testdata/                      fixture repos, jsonl, lsof output, golden JSON
  docs/                           docs and prototypes (not built)
```

### What is copied from acme (S3: copy, acme stays)

| acme file | Goes to | Change |
|---|---|---|
| `discover.go` (`parseLsof`, `enrich`, `enrichAll`, `lsofOutput`, `isDev`) | `internal/probe/servers/` | As-is. Add a `project` field from cwd match. |
| `history.go` (`Merge`, `collapseByKey`, `serverKey`) | `internal/probe/servers/history.go` | Keep the merge logic. Persist to the `server_history` table, not `history.json`. |
| `actions.go` (`killPID`, `restart`, `pageTitle`, `openInSafari`) | `internal/actions/` | `restart` keeps `sh -c` (it replays a recorded argv string). Logs go to `~/.drashta/logs/<port>.log`. |
| `server.go` `guard`, `writeJSON`, `writeError`, restart-grace logic | `internal/httpapi/` | Add a CSRF token (section 8). |
| `*_test.go` | beside each copied file | Keep all tests. They are the regression net for the copied code. |
| `in.acme-monitor.plist`, `Makefile` service targets | `in.drashta.plist`, `Makefile` | Label, port, paths. |

`web/index.html` of acme is not reused. Its behaviour moves into the React "Servers" tab.

### Build and dev mode

- **Prod:** `make build` runs `npm --prefix web ci && npm --prefix web run build`, then `go build -o drashta ./cmd/drashta`. The binary serves `build/client` through `embed.FS`. Any unknown non-`/api` GET returns `index.html` (SPA fallback). Hashed assets get `Cache-Control: immutable`. `index.html` gets `no-cache`.
- **Dev:** `make dev` runs `drashta -dev -port 4455` and `vite` on 5173. Vite proxies `/api` (including SSE) to 4455. `-dev` does two things only:
  it adds `localhost:5173` to the allowed Origins, and it serves nothing from `embed` (Vite serves the page).
  Vite proxy must not buffer SSE: set `proxy['/api'] = { target, changeOrigin: true }`. Vite passes `text/event-stream` through without buffering.
- The CSRF token reaches the page from `GET /api/session` in dev and from a `<meta>` tag injected into `index.html` in prod.

### Makefile targets

| Target | Does |
|---|---|
| `web` | `npm ci && npm run build` in `web/` |
| `build` | `web` then `go build -buildvcs=false -o drashta ./cmd/drashta` |
| `install` | build to `/opt/homebrew/bin/drashta` |
| `dev` | Go with `-dev` + `vite` together |
| `test` | `go vet ./... && go test ./...` and `npm --prefix web test` |
| `service` / `unservice` | copy plist, `launchctl bootout` / `bootstrap gui/$(id -u)` (acme pattern) |
| `logs` | `tail -F ~/.drashta/drashta.log ~/.drashta/launchd.log` |
| `reindex` | `drashta -reindex` (drop cache tables, full rescan, exit) |

---

## 3. Probes

Every probe implements `Scan(ctx, project) (Result, error)` for the index and, where needed, a `Detail(ctx, ref)` for live fetch.
All external commands go through `runx.Run`: `exec.CommandContext`, no shell, 10 s timeout, stdout capped at 8 MB, `LC_ALL=C`.

| Probe | Reads | How | Cost | Cache key | Re-run when |
|---|---|---|---|---|---|
| projects | `~/projects/<group>/<name>` | `os.ReadDir` on each root | < 5 ms | root dir mtimes | root/group dir event; sweep |
| tree | files of a project | `filepath.WalkDir`, skip list | 10–200 ms per project | per dir: `(path, mtime)` | FSEvents on that dir |
| git | log, status, branch, remote | `git` CLI | 20–150 ms | `.git/HEAD` + ref tip SHA + `.git/index` mtime | event in `.git/` (HEAD, refs, index, packed-refs) |
| fossil | timeline, changes | `fossil` CLI | 30–200 ms | tip checkin hash + `.fslckout` mtime | event on `.fslckout` / repo file |
| gh | PRs, issues, CI | `gh api` / `gh pr list --json` | 300–1500 ms, network | `(repo, ETag)` | on project open; git push seen; 15 min when page visible |
| servers | listening TCP | `lsof`, `ps` (acme) | 50–150 ms | pid + port | 5 s / 60 s ticker |
| claude | sessions | jsonl streaming parse | first run ~1 GB once; then deltas | `(file, size, mtime, offset)` | FSEvents on `~/.claude*/projects` |
| docs | markdown in a project | walk result filter | cheap (reuses tree) | path + mtime | tree change on `*.md` |
| authored | `.drashta/**/*.md` | YAML front matter + body | cheap | path + mtime + sha256 | FSEvents on `.drashta/` |

### projects

- A project is every directory `~/projects/<group>/<name>`. Groups come from `config.roots` (default: personal, work, tools).
- Plain files at group level (for example `tools/plan-*.md`) are ignored.
- The hide list (`config.hidden`, default `venv`, `vendor-v1`, `go`) hides a folder from the UI. It is still discovered, so it can be unhidden without a rescan.
- Project id = `<group>/<name>`. Stack detection is by marker files: `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `vite.config.*`.

### tree

- `WalkDir`, skip set: `node_modules .git dist build target venv .venv __pycache__ .next .cache .DS_Store .fslckout`, plus `config.skip`.
- Skipped dirs appear as collapsed nodes with no children. They are never walked.
- Hard caps per project: 50,000 entries or depth 12. Above that, the node is marked `truncated` and the UI says so.
- Stored: path, is_dir, size, mtime. File bodies are never stored. The viewport reads them live (first 2,000 lines, 1 MB cap, binary sniff).

### git: shell out to `git` (recommended), not go-git

Reasons:
- The user's `git` understands everything their repos use: worktrees (Claude uses `.claude/worktrees/`), packed refs, `core.fsmonitor`, sparse checkout, LFS pointers, `.gitattributes` diff drivers. go-git lags on several of these.
- `git log --numstat` and `git diff` on big histories are much faster in C git. go-git computes diffs in Go and is slow on large repos.
- fossil and gh are CLIs anyway, so one pattern (`runx`) covers all three. Parsing is simple with `-z` and fixed `--format`.
- It removes a large dependency tree from the binary.
- Cost: a process spawn per call (~5 ms). Acceptable because git runs only on change.

Commands (always `git -C <path> --no-optional-locks ...` so drashta never takes the index lock):
- `rev-parse --abbrev-ref HEAD`, `remote get-url origin`
- `log -n 200 --format=%H%x1f%an%x1f%cI%x1f%s%x1e --numstat` (index: commits + per-day commit count + plus/minus)
- `status --porcelain=v2 -z` (dirty files)
- Detail only: `show --format=... --unified=3 <sha>` capped at 400 KB, `diff` for the dirty diff.

### fossil (CLI)

- Detect `.fslckout` or `_FOSSIL_` in the project root.
- Index: `fossil timeline -n 200 -t ci -F "%h\t%a\t%d\t%c"` and `fossil changes`. Run with `cmd.Dir = project`.
- Detail: `fossil info <h>` for the parent, then `fossil diff --from <parent> --to <h>` (as in `gather2.py`).
- Fossil takes a read lock on its repo DB. Keep one fossil call per project at a time (per-project mutex).

### gh (GitHub, kept minimal)

- Only for projects whose `origin` is on github.com. Scope for v1:
  open PRs (`gh pr list --json number,title,state,isDraft,headRefName,updatedAt,url`),
  open issues count + latest 20 (`gh issue list --json ...`),
  CI status of the current branch head (`gh run list -b <branch> -L 1 --json status,conclusion,url`).
- `gh` handles auth from its own keychain entry. drashta never reads, stores, logs or forwards the token.
- If `gh auth status` fails, the probe returns `unavailable: gh not logged in`. The UI shows a muted note, not an error.
- Rate limit: calls are only made when a project page is open or after a push; results cached 15 min.

### servers (from acme)

- `lsof -iTCP -sTCP:LISTEN -P -n`, then `ps` and `lsof -d cwd` enrichment with the PID cache (acme code as-is).
- New step: map `cwd` to a project by longest path prefix. Servers outside `~/projects` show on home as "other".
- `server_history` rows keep first/last seen per `(cwd, cmd)` key (acme `serverKey`). The home seismograph draws a teal dot for each day a server of that project was alive.
- drashta excludes its own PID (as acme does). acme on 4444 appears as a normal server in project `personal/acme`.

### claude sessions

- Roots: the 5 profiles from S6, configurable in `config.claude_profiles`.
- Files: `<profile>/projects/<encoded-cwd>/<session-id>.jsonl` (and subagent files under a session folder, which are counted into the parent session).
- **Do not decode the folder name.** The encoding turns `/`, `_` and `.` into `-` (`acme` → `site-v2`), so it is lossy. Instead:
  1. Read the `cwd` field from the first records that have one. It is the exact path.
  2. Fallback when no record has `cwd`: encode each known project path the same way and match the folder name by longest prefix.
- **Roll-up:** a session belongs to the project whose path is the longest prefix of `cwd`. So `.../acme/automation/app/.claude/worktrees/x` counts toward `work/acme`.
  Sessions in `~` or outside `~/projects` go to an "unassigned" bucket (not shown on home).
- **Cross-project mentions:** while parsing, collect absolute paths from `tool_use` inputs (`file_path`, `path`, `command` text) and from user text.
  Each path that falls in a different project adds a `session_mention(session, project, count)` row. The session then shows in that project's Sessions tab as "mentioned".
- Extracted per session: id, profile, cwd, project, start/end, title (first real user text), turn count, tools histogram, edited files, size.
  Text is cleaned with the secret-redaction regex from `sessions.py`. Only the title and short summaries go into the index and FTS. The full transcript is read live.
- **Incremental parse:** table `claude_file(path, size, mtime, offset, inode)`.
  - Same size and mtime: skip.
  - Grown, same inode: seek to `offset`, parse only new complete lines (a line without a trailing `\n` is left for next time), update aggregates.
  - Shrunk or new inode: reparse from 0.
  - Parser: `bufio.Reader` with a 16 MB max line; a bad line is skipped and counted.
- First run parses about 1 GB. Target ≤ 60 s, in the background, newest files first, so recent sessions appear in seconds.

### docs

- From the tree result: `*.md`, `*.markdown`, `*.txt` at any depth outside skip dirs, plus `README*`, `CLAUDE.md`, `AGENTS.md`.
- Index: path, title (first `#` line), mtime, first 4 KB of text into FTS. Rendered live in the viewport.

### authored (`.drashta/`)

- Layout (details in `04-data-model.md`): `.drashta/{milestones,features,tasks,snapshots,notes}/<id>.md`, markdown with YAML front matter (`gopkg.in/yaml.v3`).
- Files are the source of truth. The indexer loads them into tracker tables.
- Writes from the UI (new task, status change, note, verdict) go through `internal/authored`: write to a temp file in the same dir, `rename`, then reindex that one file.
  Before writing, compare the file's sha256 with the indexed one. If it changed on disk (for example Claude edited it), return 409 and let the UI reload.
- Unknown front-matter keys are kept on rewrite, so hand edits and Claude edits are not lost.

---

## 4. Indexer

### File watching: FSEvents, not kqueue

- **There is no file-count limit in this design.** drashta uses **FSEvents** through `github.com/fsnotify/fsevents` (cgo, macOS only; approved in A1). One recursive stream watches all of `~/projects` and costs no open file per watched file. It works the same for 30k, 300k or 3 million files. Spotlight and Time Machine use the same mechanism.
- Why not the pure-Go default: `github.com/fsnotify/fsnotify` uses **kqueue** on macOS, which needs one open file handle (fd) for every watched file and folder.
  - Measured on 2026-10-01: `~/projects` has 161,898 files and 24,792 folders. After skip-dirs it has 24,480 files and 5,581 folders, about 30k watch items **today**. That is a snapshot count, not a limit, and it grows over time.
  - macOS limits: `kern.maxfilesperproc` = 92,160, and a launchd job starts with a soft limit of 256. kqueue would break as the folder grows. FSEvents does not have this problem.
- What does grow with more files: the hourly sweep takes longer and `index.db` gets somewhat bigger. Both stay small because only summaries are stored.
- Streams:
  1. `~/projects` (one stream, recursive, latency 1 s, flag `FileEvents`).
  2. One stream over the 5 `~/.claude*/projects` dirs (latency 2 s).
- Hide `watcher` behind an interface in `internal/indexer/watch.go` so tests use a fake, and so a kqueue fallback is possible.
- Event routing, by path:

| Path pattern | Action |
|---|---|
| inside a skip dir (`node_modules`, `dist`, …) | ignored, not counted |
| `<proj>/.git/{HEAD,index,refs/**,packed-refs,FETCH_HEAD}` | git probe for that project |
| `<proj>/.fslckout`, fossil repo file | fossil probe |
| `<proj>/.drashta/**` | authored probe, that file only |
| any other file in `<proj>` | tree update for that dir + activity count |
| `~/projects/<group>/` direct children | projects probe |
| `~/.claude*/projects/**/*.jsonl` | claude incremental parse of that file |

- **Debounce:** events are collected per `(project, probe)` key. A job runs 750 ms after the last event, and at most every 5 s during a storm (for example `npm install` or `git checkout`).
- A job queue with 4 workers. At most one job per project at a time. Git and fossil jobs for the same project are serialized.
- FSEvents may report `MustScanSubDirs` or dropped events. Then that project gets a full rescan.

### Sweeps and cadences

| Job | When |
|---|---|
| Full sweep: all probes except gh, for all projects | at startup (after serving the cached page), then every hour |
| Per-project refresh | `POST /api/refresh {project}`: all probes including gh |
| Refresh all | `POST /api/refresh {}`: full sweep + gh for projects opened in the last 7 days |
| lsof | every 5 s while at least one SSE client is visible; every 60 s otherwise |
| gh | on project page open (if older than 15 min), after a push is seen in `.git/refs/remotes` |

- "Visible" is reported by the page: `POST /api/presence {visible: true|false}` on `visibilitychange`. The server keeps a per-connection flag and drops it when the SSE connection closes.

### Per-day activity (seismograph)

Table `activity_day(project, day, files_changed, commits, sessions, server_up, source)`. Day = local date in the system time zone.

- **files_changed** = count of distinct file paths changed that day, excluding skip dirs.
  - Live: each FSEvents file event (created, modified, renamed, removed) adds the path to an in-memory set per `(project, day)`. The set is flushed to SQLite every 60 s and at shutdown. The count is `max(stored, len(set))`, so a restart does not double-count.
    The set is kept for today only; at midnight it is flushed and cleared.
  - Backfill (first run, and after drashta was down): during the sweep, every file whose mtime is newer than the last sweep adds 1 to the day of its mtime, if that path was not already counted that day.
    This is lossy (a file changed on 3 days counts only on its last day), so rows written this way are marked `source='mtime'`. Live rows are `source='events'`.
- **commits** = from `git log` / `fossil timeline` dates, grouped by day. Fully rebuildable.
- **sessions** = Claude sessions active on that day (any record timestamp in the day). Rebuildable from jsonl.
- **server_up** = 1 if any server of the project was seen that day. Written by the lsof loop.
- Only `files_changed` from events and `server_up` cannot be rebuilt. That is why they live in history tables (section 5).
- **Exact commit counts** (`daily_commits`, store migration 002): the `commits` table keeps only the newest 200 commits, which is too few for a busy project. So after a VCS job whose commits changed, and once per local day, the indexer runs `git log` / `fossil timeline` over the last 35 local days (`git.DayStats`, `fossil.DayStats`) and writes the exact count and line totals per day into `daily_commits` (a cache table; a day with no commit is stored as 0). The per-day row (04 section 5, `daily_activity`) takes its commit numbers from there. Older days, and days after a Reindex dropped `daily_commits`, keep the commit and session numbers they already had: a partial cache never lowers history.
- Day keys are stepped with calendar math (`store.AddDays`), and the start of a day is found with `store.DayStart`. Never step a local midnight instant: some zones (for example America/Santiago) have no 00:00 on the day the clocks go forward.

---

## 5. Storage

- Driver: `modernc.org/sqlite` (pure Go; the rest of the binary still uses cgo for FSEvents, see risk R1). `database/sql`, one writer connection + a read pool.
- File: `~/.drashta/index.db`. Pragmas: `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`.
- All writes go through one goroutine (`store.Writer`) that batches changes in one transaction per job. Reads use the pool.

Two kinds of tables (column detail in `04-data-model.md`):

| Kind | Examples | Rebuildable? |
|---|---|---|
| **cache** | `project`, `file`, `commit`, `dirty`, `session`, `session_mention`, `claude_file`, `doc`, `gh_*`, tracker tables from `.drashta/` | Yes, from sources |
| **history** | `activity_day` (event rows), `server_history`, `sweep_log` | No; lost if the file is deleted |

- **FTS5** (built into modernc sqlite): one virtual table `search(kind, project, ref, title, body)` over file paths, doc text, commit messages, session titles, and tracker items. Updated in the same transaction as the source row. The search palette uses `MATCH` with prefix queries and `bm25()` ranking.
- **Rebuild from scratch:** `drashta -reindex` (or the "Rebuild index" button) drops and recreates all cache tables in one transaction, keeps history tables, then runs a full sweep. Deleting `index.db` also works; drashta recreates it and loses only history.
- **Migrations:** numbered SQL files in `internal/store/migrations/NNN_name.sql`, embedded, applied in order inside a transaction, tracked by `PRAGMA user_version`.
  A cache-table change may just drop and rebuild. A history-table change must migrate data.
- `index.db` is backed up nowhere by design. It holds no source of truth and no secrets.

---

## 6. Live updates

- Endpoint: `GET /api/events` (`text/event-stream`). Guarded by the Host/Origin check. A `: ping` comment every 25 s keeps the connection open.
- Each event has a monotonic `id` (the hub's sequence number). The hub keeps the last 1,000 events in a ring buffer.
- Event types (payloads are small; the page fetches detail itself):

| event | data | Page does |
|---|---|---|
| `project` | `{project, parts:["tree","git",...]}` | refetch that project's summary |
| `activity` | `{project, day}` | refetch seismograph lane |
| `servers` | `{project?, changed:[...]}` | refetch servers |
| `session` | `{project, session}` | refetch sessions list |
| `tracker` | `{project, kind, id}` | refetch tracker item |
| `scan` | `{state:"running"|"idle", scope}` | show "rescanning…" / "updated 12:04" |
| `resync` | `{}` | refetch everything visible |

- **Resync:** EventSource reconnects with `Last-Event-ID`. If that id is still in the ring, the hub replays the missed events. If not, it sends `resync`.
- **Visibility:** on `visibilitychange` to visible, the page calls `GET /api/state?since=<last id>`. The response is either the list of missed events or `{resync:true}`. This covers the case where WebKit suspended the page and the SSE socket died silently.
- **Plash:** Plash shows the page in a WKWebView behind all windows. WebKit may throttle timers and may suspend the page while it is covered.
  So: no timers in the page for correctness, no animation loop, and all catch-up happens on visibility and reconnect. The page reads `html.plash-is-browsing-mode` (D3) only for layout, never for data.
- The server coalesces events: at most one `project` event per project per second.

---

## 7. Native actions ("open out")

- Endpoint: `POST /api/open {ref, action?}`. The server resolves the ref to a real path from the index. The page never sends a path or a command.
- Execution: `exec.Command(name, args...)` with a fixed binary per action. Never `sh -c`, except acme's restart (below).
- Path rule: the resolved path is `filepath.Clean`ed, symlinks resolved with `EvalSymlinks`, and it must be inside a known project root. Otherwise 400.
- Every argument that comes from data is placed after `--` where the tool supports it, or checked to not start with `-`.

| Item | Command | Notes |
|---|---|---|
| file | `<editor> -g <path>:<line>` | `editor` from config, default `code`; argv template like `["code","-g","{path}:{line}"]` |
| folder / project | `open -R <path>` (Finder), `open -a <terminal> <path>` | terminal app from config, default `Terminal` |
| image / snapshot | `open -a Preview <path>` | |
| commit (git) | `open -a <terminal>` running `git -C <path> show <sha>` | sha must match `^[0-9a-f]{7,40}$` |
| commit (fossil) | same, `fossil diff --checkin <h>` | hash must match `^[0-9a-f]{6,64}$` |
| session | terminal in the project dir: `claude --resume <id>` with `CLAUDE_CONFIG_DIR=<profile>` | id must be a UUID |
| server / URL | `open -a Safari http://localhost:<port>` | only `http://localhost:<port>` or `http://127.0.0.1:<port>`; other URLs (for example gh PR links) must be `https://github.com/...` |
| task / note | `<editor> <path>/.drashta/tasks/<id>.md` | |

- Opening a terminal with a command: write nothing to disk; use `open -a Terminal` with an `osascript` argv (`-e`, a fixed script, and the command as a separate `argv` item passed through `on run argv`), so no string is built into AppleScript source.
- **Destructive actions** (kill, restart): the UI shows a confirm step (button turns into "Confirm kill :5173" for 5 s). The server also requires `{confirm:true}` in the body.
  Kill only works on a PID that is in the current live lsof set (acme rule). Restart only replays a `(cwd, cmd)` that drashta itself recorded, keeps acme's `restarting` grace map, and logs to `~/.drashta/logs/<port>.log`.
- Actions are logged to `drashta.log` as `action=<kind> ref=<ref> ok=<bool>`.

---

## 8. Security

- Bind `127.0.0.1:4455` only. Never `0.0.0.0`, never IPv6 wildcard. No TLS, no auth; no remote access in any form.
- **guard** (copied from acme `server.go`), on every `/api` route including SSE:
  - `Host` must be `localhost:4455` or `127.0.0.1:4455` (stops DNS rebinding).
  - `Origin`, if present, must be one of those (stops cross-site calls).
  - POSTs must be `application/json` (forces a CORS preflight, which drashta never approves) and bodies are capped (acme uses 4 KB; drashta uses 64 KB for notes).
- **Added: CSRF token.** At startup the server makes 32 random bytes. It is put in `index.html` as `<meta name="drashta-token">` and must come back as `X-Drashta-Token` on every POST. Compare with `subtle.ConstantTimeCompare`. The token changes on every restart; on 403 the page reloads once.
- No CORS headers are ever sent.
- Static SPA files are served without the guard (they are public code), but `index.html` is only served for an allowed Host, so the token cannot leak to a rebinding page.
- No secrets stored: jsonl text is redacted before indexing; `.env*`, `*.pem`, `*.key`, `id_*` files are listed in the tree but their bodies are never shown or indexed.
- `gh` runs as the user and reads its own token. drashta never calls `gh auth token` and never passes `GH_TOKEN`.
- File reads for preview go through the same "inside a project root" check as actions.
- Opening a `.dwg` runs `chitralipi` (libredwg, native C code) on that file with the user's rights. It runs out of process through `runx` (argv, no shell) with a 60 s timeout and a 128 MB output cap. A later option: a `sandbox-exec` profile around it.

---

## 9. Startup and operations

`in.drashta.plist` (copy of acme's, changed fields):

```xml
<key>Label</key><string>in.drashta</string>
<key>ProgramArguments</key><array>
  <string>/opt/homebrew/bin/drashta</string><string>-port</string><string>4455</string></array>
<key>EnvironmentVariables</key><dict><key>PATH</key>
  <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ProcessType</key><string>Interactive</string>
<key>SoftResourceLimits</key><dict><key>NumberOfFiles</key><integer>4096</integer></dict>
<key>Umask</key><integer>63</integer>   <!-- 077: files the service creates are private -->
<key>StandardOutPath</key><string>~/.drashta/launchd.log</string>
<key>StandardErrorPath</key><string>~/.drashta/launchd.log</string>
```

- launchd's stdout and stderr (for example a panic trace) go to `launchd.log`, NOT to `drashta.log`. drashta writes and rotates `drashta.log` itself; if launchd held that file open, a rotation would leave launchd writing into the renamed `.1` file or a deleted one. `make logs` tails both files.
- `~/.drashta` is 0700 and drashta's files in it (`index.db*`, `drashta.log*`, `launchd.log`, `config.toml`, `settings.json`) are 0600. drashta fixes the modes of an older install at start.

- `PATH` must include `/opt/homebrew/bin` because launchd does not load the shell profile (fossil, gh, node, code live there).
- `make service` installs and bootstraps; `make unservice` removes. Same flow as acme.
- Single instance: at start, dial `127.0.0.1:4455`. If something answers `GET /api/health` as drashta, exit 0. If another program holds the port, log and exit 1 (KeepAlive will retry; the log shows why).
- Startup order: open DB, migrate, start HTTP (home renders from cache at once), then start watchers, then the background sweep.
- Logs: `~/.drashta/drashta.log` (stdlib `log/slog`, text). Rotate at 10 MB, keep 3 (done in-process, since launchd does not rotate).
- Config `~/.drashta/config.toml`. Created with defaults if missing. Changes are picked up on restart (v1; no hot reload).

```toml
projects_dir = "~/projects"
roots   = ["personal", "work", "tools"]
hidden  = ["venv", "vendor-v1", "go"]
skip    = []                       # extra skip dirs for the tree walk
editor  = ["code", "-g", "{path}:{line}"]
terminal = "Terminal"
claude_profiles = ["~/.claude", "~/.claude-a", "~/.claude-b", "~/.claude-c", "~/.claude-d"]
gh_enabled = true
```

- Hiding a folder from the UI also works from the UI; it writes `hidden` back to config.toml (the only file drashta writes outside `.drashta/` dirs and `~/.drashta/`).
- **Coexistence with acme:** different port (4444 vs 4455), different label (`in.acme-monitor` vs `in.drashta`), different data dir (`~/.acme-monitor` vs `~/.drashta`). Both run lsof; the cost is small. drashta does not import acme's `history.json`.
- Plash URL: `http://localhost:4455/`. Use `localhost`, not `127.0.0.1`, everywhere, so Origin checks match in one form.

---

## 10. Performance budgets and failure handling

| Budget | Target |
|---|---|
| Binary start to first HTTP response | < 300 ms (cache present) |
| Home (seismograph, 30 projects × 30 days) API response | < 50 ms, one SQL query per lane group |
| Home first paint in Plash | < 1 s |
| First-ever full index (no DB) | usable in < 10 s (projects, git, fossil, tree); Claude backfill < 60 s in background |
| Change on disk to page update | < 2 s (FSEvents latency 1 s + debounce 0.75 s) |
| Idle CPU | < 1% average (lsof every 60 s, no timers otherwise) |
| RSS | < 150 MB steady; < 300 MB during Claude backfill |
| `index.db` size | < 200 MB |
| JS bundle | < 400 KB gzip |

Failure handling:

- **Missing tool** (`fossil`, `gh`, `git`): checked with `exec.LookPath` at start. The probe reports `unavailable`, the UI greys that part out with a one-line reason. Nothing else stops.
- **Command timeout or non-zero exit:** the probe keeps the last good cached data and marks it `stale_since`. The error is logged once per project per hour, not on every event.
- **Huge repos:** git log capped at 200 commits in the index (older ones fetched on scroll by the detail API); diffs capped at 400 KB; tree capped at 50,000 entries; file preview capped at 1 MB.
- **Permission errors** (for example a root-owned dir, or macOS TCC blocking a folder): `WalkDir` skips that dir and marks it `denied`. The service needs no Full Disk Access, because everything is under the home folder.
- **Corrupt jsonl lines:** skipped and counted; the session still shows.
- **Corrupt `index.db`:** on open error or failed `PRAGMA quick_check`, rename it to `index.db.corrupt-<ts>`, start fresh, log it.
- **lsof failure:** keep last server state (acme's `scanOnce` rule: a failed scan never marks all servers dead).
- **Panics** in a probe are recovered per job and logged with the project id.

---

## 11. Testing strategy

- **Copied acme tests** run unchanged in their new packages (`parseLsof`, `Merge`, guard tests, restart grace).
- **Probe unit tests, with fixtures built in the test** (`t.TempDir()`):
  - git: a helper creates a repo with `git init`, commits with fixed `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE`, a worktree, and a dirty file. Assert parsed commits, numstat, status.
  - fossil: same with `fossil init` / `fossil open` / `fossil commit --date-override`; skipped with `t.Skip` if fossil is missing.
  - claude: `testdata/claude/` with hand-made jsonl: subfolder cwd, worktree cwd, no-cwd file (fallback decode), cross-project paths, a secret to redact, a truncated last line. Incremental test: append lines, assert only new lines are parsed; truncate file, assert full reparse.
  - tree: skip list, caps, denied dir (chmod 000).
  - authored: round-trip keeps unknown keys; concurrent edit gives 409.
  - servers: recorded lsof outputs in `testdata/lsof/*.txt`.
- **Golden files:** each probe's `Result` for fixtures is written as JSON to `testdata/golden/*.json`; `go test -update` rewrites them. API responses for home and project pages have golden files too.
- **Indexer tests** use the fake watcher: feed events, advance a fake clock, assert debounce, per-day counts, midnight rollover, no double count after restart.
- **Store tests:** migrations from 0 to latest on an empty DB; `-reindex` keeps history tables; FTS finds a commit by message word.
- **API integration test:** start the full server on a random port with a temp HOME that holds fixture projects and profiles. Check guard (bad Host, bad Origin, missing token → 403), SSE event after touching a fixture file, and that `/api/open` rejects paths outside roots and never runs a command (executor swapped for a recorder).
- **Frontend:** Vitest + React Testing Library for components (seismograph lane from fixture JSON, viewport per item type, confirm-kill flow); MSW to mock `/api`. One Playwright smoke test, headless, against `drashta -dev` with fixture HOME. It also checks the page with and without `plash-is-browsing-mode`.
- `make test` runs all Go and web tests. No test touches the real `~/projects` or `~/.claude*`.

---

## 12. Risks and open technical questions

| # | Risk / question | Proposal |
|---|---|---|
| R1 | FSEvents needs **cgo**, while S7 asks for a pure-Go SQLite driver. | **Decided (A1, 2026-10-01): accept cgo for FSEvents**; SQLite stays pure Go (modernc). No file-count limit follows from this choice. |
| R2 | Does Plash's WKWebView report `visibilityState = hidden` when the wallpaper is covered? If it stays "visible", lsof always runs at 5 s. | Test on the machine in week 1. If it never goes hidden, treat "visible" as "browsing mode on or Safari tab focused" instead. 5 s lsof costs ~2% of one core, which is acceptable anyway. |
| R3 | Does WKWebView in Plash keep an SSE connection across sleep/wake and long covered periods? | Built to survive it (reconnect + `/api/state?since`). Verify with a 24 h soak test. |
| R4 | File-change counts from events cannot be rebuilt. Deleting `index.db` (allowed by S7) loses them. | Accept; after a rebuild, past days come from mtime backfill and are marked `source='mtime'`. Optional later: a separate `history.db`. |
| R5 | Claude jsonl format is not a public contract. Fields (`cwd`, `message.content` blocks) may change. | Parser is tolerant and tested with fixtures; unknown record types are ignored; add a "parser saw N unknown records" counter in the log. |
| R6 | Build-tool storms (`npm install`, `vite build`) in a project produce thousands of events and inflate `files_changed`. | Skip dirs are excluded before counting; debounce limits work; open question: also exclude files listed in `.gitignore` from the count (costs a `git check-ignore` per batch). |
| R7 | Opening a terminal with a command needs AppleScript for Terminal/Ghostty, which asks for Automation permission once. | Accept the one-time prompt; document it in the install step. |
| R8 | Concurrent edits of `.drashta/*.md` by the UI and by a Claude session. | sha256 check before write + 409; no locking beyond that in v1. |
