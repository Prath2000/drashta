# drashta: design specification (v1)

- Date: 2026-10-01.
- Status: draft. It waits for the user's approval (roadmap Phase 2 gate).
- This is the ONE merged design. It replaces reading `docs/spec/02-ui-spec.md`, `docs/spec/03-architecture.md` and `docs/spec/04-data-model.md` together, and it adds the answers in `docs/spec/06-open-questions.md`.
- `docs/decisions.md` is binding. If any document disagrees with it, `00-decisions.md` wins. Section 5 lists every conflict found and how it was resolved.
- Visual authority: the frozen prototype v3 (the design prototype). The real app matches it unless the user says otherwise.
- Terms are defined in `CONTEXT.md`. Big choices have an ADR in `docs/adr/`.
- No new decisions are made here. Where a source left a question open, the proposed default is adopted and listed in section 4 so the user can overrule it.

---

## 1. What drashta is

- drashta (द्रष्टा, "the one who sees") is a local command center for every project in `~/projects/<group>/<name>`.
- It **observes**: commits (git and fossil), files, localhost servers, Claude Code sessions, docs, GitHub (through `gh`) and daily activity.
- It **tracks**: features, milestones, tasks, snapshots under review, and notes on any item. The tracking side follows the style of ShotGrid and Autodesk Construction Cloud.
- The main rule: nothing is pushed at you, and any detail is a few clicks away.
- It is also the opening screen of the Mac. The same page is the desktop wallpaper (through Plash) and the full command center.
- It is one Go binary that serves an embedded React SPA on `127.0.0.1:4455`. It starts at login through launchd.

## 2. Goals and non-goals

### Goals

- G1. Show the recent activity of every project on one screen: the seismograph (U1).
- G2. Make any detail reachable in at most three clicks: Home, lane, day, item, Open ↗.
- G3. Observe everything listed in S5: history and files, running servers and their actions, docs, Claude sessions, GitHub, activity.
- G4. Track work per project with data that lives in the project (S9): features, milestones, tasks, snapshots, notes.
- G5. Work as the Mac desktop (D1): a calm readout, and the full app one hotkey away.
- G6. Be safe on a personal machine: loopback only, no shell for data-driven commands, every path guarded.
- G7. Be cheap: under 1% idle CPU, no polling in the page, no animation at rest.
- G8. Be safe to wipe: the SQLite file is a cache and can be deleted (S7).

### Non-goals (v1)

- Not a git client. drashta never commits, pushes or edits project source. It writes only `.drashta/` folders and its own `~/.drashta/` data.
- Not multi-user, not remote, no login. No access from other machines in any form.
- Not cross-platform. It targets macOS (FSEvents, `open`, `launchd`).
- The full list is in section 17.

## 3. Decisions

Every ID below comes from `docs/decisions.md`. The right column says where this spec uses it.

| ID | Decision (short) | Used in |
|---|---|---|
| S1 | Local web app. Go backend, React Router frontend, one binary, 127.0.0.1 only. | 6, 12 |
| S2 | Port 4455. Starts at login through a launchd plist, like acme. | 13.1 |
| S3 | acme is copied into drashta. acme itself stays untouched. | 6.3 |
| S4 | Discovery by folder convention: every `~/projects/<group>/<name>` is a project. Folders can be hidden in the UI. | 7.2, 11.3 |
| S5 | v1 scope is everything: history, files, servers and actions, docs, Claude sessions, GitHub, activity, tracker. | 2, 7 |
| S6 | Claude sessions come from 5 profiles. A session started in a subfolder counts toward its parent project. A session can also be about another project. | 7.6 |
| S7 | Data model B: SQLite summary index plus live detail fetch. SQLite is a cache and history log, never the source of truth. `~/.drashta/index.db`, pure-Go driver. | 9.7 |
| S8 | The indexer runs on change, not on a timer. Hourly sweep. lsof every 5 s with a tab open, every 60 s otherwise. Skip dirs. Refresh buttons. | 8 |
| S9 | Authored data lives per project in `.drashta/` as markdown with YAML front matter. | 9.3 |
| U1 | The seismograph is the home screen. | 11.3 |
| U2 | No home or map button. The name "drashta" is the link to home. | 11.1 |
| U3 | No Pulse section, no Reviews section. Review happens inside the Tracker as a filter. | 11.6 |
| U4 | Sections: Home, Project, Tracker, Inbox, plus the right-hand viewport. | 11.1 |
| U5 | Font: JetBrainsMono Nerd Font Mono, 400 only. No italics, no bold. | 11.8 |
| U6 | Map-style touches: spaced uppercase labels and cartouche boxes with a double hairline. | 11.8 |
| U7 | The chart map is kept for later. It is not in v1. | 17 |
| U8 | Dark instrument-panel visual system and the signal colours. | 11.8 |
| U9 | "Preview in, open out." | 10.4, 11.1 |
| D1 | The full, working home screen is the Mac wallpaper, shown through Plash. | 13.6 |
| D2 | Interact through Plash Browsing Mode on a hotkey, or leave it on. | 11.4 |
| D3 | The page is Plash-aware: readout without the `plash-is-browsing-mode` class, full app with it. | 11.4 |
| D4 | Links and "Open ↗" never navigate inside Plash. The server runs every native command. | 10.4, 11.4 |
| D5 | The server pushes changes. The page resyncs when visible. No reload timer, no constant animation. | 10.3 |
| D6 | Fallback: a Safari web app in its own full-screen Space. A native helper only if both fail. | 13.6 |
| A1 | cgo is accepted for FSEvents. SQLite stays pure Go. | 6, 8.1 |
| A2 | Commit `.drashta/` text to each project's git. Snapshot images are git-ignored by default. | 9.3 |
| A3 | URL snapshots use Safari (`safaridriver`), not Chrome. Details later. | 9.6 |
| A4 | Approving the latest snapshot only suggests moving the task to done. | 9.6 |
| A5 | Claude sessions put a small violet tick on the seismograph lane. | 11.3 |
| A6 | GitHub data is a block in the project Overview, not its own tab. | 11.5 |

## 4. Adopted defaults

Q1 to Q6 are answered (A1 to A6). Q7 to Q14 had proposed defaults, and this spec adopts all of them. The user can overrule any row. "Cost" says how much work a change would be.

| # | Adopted default | Where | Cost to change |
|---|---|---|---|
| Q7 | `?desktop=1` in the Plash URL marks "running as wallpaper". | 11.4 | Low: one CSS selector and one URL. |
| Q8 | **Superseded by decision U10 (2026-10-01):** the server preview embeds the live app in a sandboxed frame, with Expand; the last capture is the fallback. Originally: last capture plus Open ↗, no live frame. | 11.2 | Medium: a sandboxed frame component. |
| Q9 | Search has one prefix in v1: `:port`. No `group:` or `kind:` filters. | 11.2 | Low. |
| Q10 | Task IDs can clash across git branches (both create `T-13`). Accepted. Add `drashta renumber` if it happens. | 9.3 | Low until it happens. |
| Q11 | The CLI learns the calling Claude session from an exported session id (a Claude Code hook). Fallback: the project's newest `.jsonl` by modified time. Without either, the author is `Claude`. | 9.4 | Low. |
| Q12 | Line notes after the file is edited: re-anchor by matching the stored line text. If that fails, show "line moved?". | 11.2 | Low. |
| Q13 | Manual task links to a server store the stable key `server-key:<sha1(cwd\|cmd)[:10]>` next to the port. The UI shows the port. | 9.2 | Low. |
| Q14 | File-change counts exclude skip dirs and common build outputs. Files in `.gitignore` are not excluded in v1. | 8.4 | Low. |
| Q3 detail | Snapshots from a file path or an upload work first. A snapshot from a URL gives 501 `not_implemented` until Safari capture (`safaridriver`) is built behind a small interface. | 9.6 | Medium. |
| 02 UI Q1 to Q5 | Same as Q5, Q7, Q8, Q6 and Q9. `docs/spec/02-ui-spec.md` section 9 lists them under different numbers. | 11 | n/a |

Checks that need the real Mac (Phase 5). They are not decisions.

| # | Check | Source |
|---|---|---|
| C1 | Does Plash report the page as hidden when windows cover it? This decides whether lsof can slow down (R2). | 16 |
| C2 | Does the live connection survive sleep and long covered periods (R3)? | 16 |
| C3 | Does Plash have its own Browsing Mode hotkey, and do desktop icons still work? | 13.6 |
| C4 | Opening a terminal with a command asks once for macOS Automation permission (R7). | 10.4 |

## 5. Conflicts resolved

The source documents disagree in these places. `00-decisions.md` decides first. Where it is silent, the newer or more specific document wins, and the reason is given. The code that already exists is treated as fact.

1. **File watching: fsnotify or FSEvents.** S8 names "fsnotify". A1 (newer) accepts cgo for FSEvents, and `03` section 4 explains why kqueue does not scale. Ruling: FSEvents through `github.com/fsnotify/fsevents`. The wording "fsnotify" in S8 and in `04` section 2.1 means "a file watcher".
2. **Project ID.** `03` says `<group>/<name>`. `04` says `<name>`, or `<group>.<name>` on a name clash across groups. Ruling: `04`. The ID is the folder name. If the same folder name exists in two or more groups, every one of them gets `<group>.<name>`. An ID never holds a slash, which keeps `/p/:project` routes and refs such as `acme:commit:abc1234` simple. The prototype also uses plain names. The discovery code decides this in a second pass over the full list, so it does not depend on scan order or on the hide list. A hide-list entry matches the folder name, `group/name` or `group.name`.
3. **Default hidden folders.** `03` section 9 lists `venv`, `vendor-v1`, `go`. `02` section 3.3, S4 and the frozen prototype (`data.hidden`) list `venv` and `vendor-v1`, and the prototype shows `go` as a normal lane. Ruling: the default is `venv` and `vendor-v1`. The user can hide `go` from its project page. (The discovery code and its test use the 3-name list today. Task W2-0 in the plan changes both.)
4. **Where the hidden list lives.** `03` says `config.toml`. `04` puts it in the `settings` table. Ruling: `config.toml` (`hidden`) holds it, written by `config.SetHidden`, so it is the same in every window (`02` section 3.3). The `settings` table keeps the author name, the editor override and the saved range. `PATCH /api/settings {hiddenFolders}` calls `SetHidden`.
5. **Health endpoint.** `03` and the HTTP core say `GET /api/health` (JSON). `05` and the roadmap gate say `GET /healthz` returning `ok`. Ruling: `/api/health` is the real endpoint (single-instance check). The wiring task adds `GET /healthz` as a plain-text `ok` alias, outside the guard's token rule.
6. **SQLite names.** `03` section 5 uses `project`, `file`, `activity_day`, `server_history`, `session_mention`. `04` section 5 uses `projects`, `files`, `daily_activity`, `server_runs`, `file_changes`, `session_projects`. `03` says the detailed schema is in `04`. Ruling: `04` wins. Two tables from `03` are added: `claude_file` (incremental parse state) and `sweep_log` (history). The `session_mention` count column is dropped. `session_projects.reason` carries `cwd`, `mention` or `edit`.
7. **Full-text search shape.** `03` shows `search(kind, project, ref, title, body)` with prefix queries. `04` shows `fts5(... tokenize='trigram')`. Ruling: `04` (trigram). Paths and code fragments then match mid-word. A query under 3 characters returns no hits.
8. **Live event set.** `03` lists `project`, `activity`, `servers`, `session`, `tracker`, `scan`, `resync`. `04` lists `project`, `servers`, `tracker`, `activity`, `scan`, `counts`, plus `hello`. Ruling: the union without `session`. `project` carries `what: "sessions"` for session changes. Payloads follow `04`. `resync` and `hello` both exist.
9. **API shapes.** `03` uses `POST /api/refresh {project}`. `04` uses `{projectId?}`. Ruling: `04`. `03` adds `GET /api/health`, `GET /api/session` (dev only), `GET /api/state?since=` and `POST /api/presence`. They are not in `04` and are added to the table in section 10.
10. **GitHub cache time.** `03` says 15 minutes. `04` says 5 minutes. Both can hold, because they describe different things. Ruling: the `/github` endpoint caches for 5 minutes (`04`), and the background refresh runs at most every 15 minutes while a page is visible (`03`). A project refresh button forces a reload.
11. **Plash URL host.** `02` section 4 says `http://127.0.0.1:4455/?desktop=1`. `03` section 9 and `05` say `localhost`. Ruling: the Plash URL is `http://localhost:4455/?desktop=1`. The server binds `127.0.0.1` only, and the guard accepts both host names. The rule "one host form everywhere" in `03` keeps Origin checks simple. Step 3 of `05` leaves out `?desktop=1`, but readout mode needs the marker (Q7, D3), so the setup uses it.
12. **Server preview.** `02` section 2.4 shows a sandboxed live frame. `02` section 1 says drashta never loads another app's page inside itself. Q8 and the frozen prototype show "last capture plus Open ↗". Ruling: last capture plus Open ↗. **Superseded by U10:** live sandboxed frame, capture as fallback.
13. **URL snapshots.** `04` section 4 says headless Chrome. A3 says Safari (`safaridriver`). Ruling: A3.
14. **Finder action.** `02` section 5.2 says `open <path>`. `03` section 7 says `open -R <path>` (reveal in Finder), and the actions code follows `03`. Ruling: `03`. Reveal is the action contract. If the user prefers "open the folder itself", it is a one-line change in `actions.Plan`.
15. **Reindex command.** `03` has the flag `drashta -reindex` (full). `04` has `drashta reindex [project]`. Ruling: both are one code path. The optional project argument limits the rebuild.
16. **Importing acme history.** `04` says `server_runs` is "seeded once from acme's history JSON". `03` section 9 says drashta does not import it. Ruling: `03`. Nothing is imported in v1. A one-time import command is possible later.
17. **Probe interface.** `03` says every probe implements `Scan(ctx, project) (Result, error)`. The built `probe.Probe` interface has only `Name()`, and each probe package exposes typed `Scan` and `Detail` functions, because the results differ. Ruling: the code. The indexer calls probes directly.
18. **Go dependency list.** `go.mod` lists the pre-fetched modules as `// indirect`. They become direct when the first package imports them. No agent edits `go.mod`.
19. **Where a command runs.** `03` section 7 says terminal commands run in the configured terminal app. The built planner runs them in Terminal.app through a fixed AppleScript (`do script`). The configured `terminal` is used only for `open -a <terminal> <path>`. Ruling: the code. A second terminal app would need a different script.
20. **Doc text in FTS.** `03` says the first 4 KB of a doc. `04` says 64 KB. Ruling: 4 KB. The docs probe passes 4 KB (enough for v1). The store accepts up to 64 KB, but a 64 KB body costs three to four times its size in the trigram index.
21. **Commit IDs.** `04` says the ID is the short hash. The probes store the **full hash** for git and fossil, as a stable key. The UI shows the first 7 characters. A ref may use any prefix of 7 or more hex characters. (`03` used the short fossil hash `%h`.)
22. **Live stream details.** The built HTTP core adds to `04` section 6: `hello` carries `conn`; `POST /api/presence` takes `{visible, conn?}`; a `project` event can have `what:"all"` with `parts` when events of different kinds were merged; `/api/state` answers `{resync, head, events}`; event IDs start at the server start time in microseconds. Section 10.3 has the rules.
23. **`server_runs` columns.** `04` has no `kind` and `last_seen`. The servers code keeps both per run, so the table gets them.
24. **Page padding.** `02` section 8.3 says 40px at 1440px and wider. The frozen prototype uses 24px at every width. Ruling: 24px. The prototype wins on looks (the rule at the top of section 11).
25. **Global keyboard shortcuts.** `02` section 2.6 lists them. The first build rules said "no global key listeners", which came from the prototype's own limits. Ruling: the shortcuts stay in v1 as one global handler that ignores keys while a field has focus and is off in readout mode. They are built in Wave 3.
26. **Hash for `rev`.** `04` section 2.5 says sha1 of the file bytes. `03` says sha256. Ruling: sha256, as hex (`03` and the built `authored` package).
27. **CLI command forms.** `04` shows `drashta task set T-12 status=review`, `drashta note T-12 "text"` and `drashta snap T-12 <file|url>`. The built CLI has `task new|list|show|status|link`, `note add|resolve` and `snap add --task T-12 --image|--path <file>`. Ruling: the built forms. `snap --url` is refused with a clear message until Safari capture exists (A3). The generated `.drashta/README.md` names only commands that exist.

## 6. Architecture

### 6.1 Overview

```
 ┌──────────────── Mac desktop ─────────────────┐
 │ Plash (WKWebView wallpaper)   Safari (fallback)│
 │   GET /  (SPA)   EventSource /api/events      │
 └───────────────┬──────────────────────────────┘
                 │ HTTP, 127.0.0.1:4455 only
 ┌───────────────▼──────────────────────────────────────────────┐
 │ drashta (one Go binary, started by launchd in.drashta)       │
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

- **Summary data** (home, activity, search) comes from SQLite. **Detail data** (a diff, a file body, a full transcript) is fetched live by a probe on request (S7).
- SQLite is a cache and a history log. The filesystem, VCS and jsonl files are the source of truth for observed data. The `.drashta/` files are the source of truth for authored data.
- The server pushes changes over SSE. The page never polls (D5).
- The machine facts that shaped the design (checked 2026-10-01, read-only): Go 1.26.3 darwin/arm64; `git`, `fossil`, `gh`, `node`, `lsof` installed; `~/projects` has 3 groups and about 20 project folders, 161,898 files and 24,792 folders (24,480 files and 5,581 folders after skip dirs); mixed VCS (git only, fossil only, both, none); 5 Claude profiles with about 1 GB of `.jsonl`; `launchctl limit maxfiles` is 256 soft; acme runs on 4444 with data in `~/.acme-monitor/`.

### 6.2 Repo layout and build status

The Go module is `drashta` at `~/projects/personal/drashta`. The frontend lives in `web/` and is embedded.

```
drashta/
  go.mod  go.sum  Makefile  in.drashta.plist
  cmd/drashta/main.go            flags, subcommands, calls internal/app
  internal/
    runx/       exec with timeout, output cap, no shell                       [done]
    pathguard/  Resolve, ResolveAny, Inside                                   [done]
    probe/      shared types (Project, Commit, CommitFile, CommitDetail,
                DirtyFile, Unavailable) and the Probe{Name()} interface       [done]
      projects/ tree/ docs/          discovery, tree walk, docs                [done]
      git/ fossil/                   commits, status, diffs                    [done]
      claude/                        jsonl sessions                            [done]
      servers/                       lsof servers (from acme)                [done]
      gh/                            GitHub through gh                         [wave 2]
    config/     ~/.drashta/config.toml                                         [done]
    store/      SQLite, migrations, FTS, typed helpers                         [wave 1]
    authored/   .drashta/ read and safe write, refs                            [wave 1]
    cli/        drashta task|note|snap                                         [wave 1]
    actions/    native actions, kill, restart (from acme)                    [done]
    live/       SSE hub, ring buffer                                           [done]
    httpapi/    server, guard, spa [done]; api_*.go handlers [wave 2]
    indexer/    watcher, queue, routing, sweep, jobs, activity                 [wave 2]
    autolink/   derive task links from commits and sessions                    [wave 2]
    resolve/    ref to title, subtitle and native target                       [wave 2]
    app/        wiring of everything, end-to-end test                          [wave 2]
  web/                           React Router v7 SPA mode, Vite, TypeScript
    app/ routes/ components/ lib/api.ts lib/events.ts
    build/client/                build output, embedded
    embed.go                     //go:embed all:build/client
  testdata/                      fixture repos, jsonl, lsof output, golden JSON
  docs/                     planning docs, frozen prototype, this spec
```

- "Done" means the package exists and its tests pass. "Wave 1" means it is still being written, and "wave 2" means it starts later (see the build plan). The status is from the Wave 1 reports read on 2026-10-01; `authored`, `cli`, `store` and `web` were still running. Each package states its final API in its own `doc.go`. Treat `doc.go` as the truth.
- Reality of the shared base:
  - `runx.Run(ctx, runx.Options{Dir, Env, Timeout, MaxOut}, name, args...)` runs `exec.CommandContext` with no shell. Default timeout 10 s. Stdout cap 8 MB (`Truncated` is set when it is hit). `LC_ALL=C`. A non-zero exit is not an error: read `Result.ExitCode`. A missing binary gives `runx.ErrNotFound`. `runx.Available(name)` checks PATH.
  - `pathguard.Resolve(root, rel)` joins, resolves symlinks, and fails with `ErrOutside` if the result leaves `root`. `ResolveAny(roots, abs)` does the same for an absolute path against several roots. `Inside(root, p)` is a pure path-element check (`/a/bc` is not inside `/a/b`).
  - `probe.Project{ID, Name, Group, Path, Hidden}` is the input to every probe.

### 6.3 What is copied from acme (S3)

acme stays untouched. Its code is copied and changed as shown.

| acme file | Goes to | Change |
|---|---|---|
| `discover.go` (`parseLsof`, `enrich`, `enrichAll`, `lsofOutput`, `isDev`) | `internal/probe/servers/` | As is. Add a `ProjectID` from the cwd match. |
| `history.go` (`Merge`, `collapseByKey`, `serverKey`) | `internal/probe/servers/` | Keep the merge logic. Persist through a `HistoryStore` interface that the store implements over `server_runs`, not `history.json`. |
| `actions.go` (`killPID`, `restart`, `pageTitle`, `openInSafari`) | `internal/actions/` | `restart` keeps `sh -c` because it replays a recorded argv string. Logs go to `~/.drashta/logs/<port>.log`. |
| `server.go` `guard`, `writeJSON`, `writeError`, restart grace | `internal/httpapi/` | Add the CSRF token (section 12). |
| `*_test.go` | beside each copied file | Keep all tests. They are the regression net. |
| `in.acme-monitor.plist`, `Makefile` service targets | `in.drashta.plist`, `Makefile` | Label, port, paths. |

`web/index.html` of acme is not reused. Its behaviour moves into the React Servers tab.

### 6.4 Build and dev mode

- **Prod:** `make build` runs `npm --prefix web ci && npm --prefix web run build`, then `go build -buildvcs=false -o drashta ./cmd/drashta`. The binary serves `build/client` through `embed.FS`. Any unknown non-`/api` GET returns `index.html` (SPA fallback). Hashed assets under `/assets/` get `Cache-Control: public, max-age=31536000, immutable`. `index.html` gets `no-cache`.
- **Dev:** `make dev` runs `drashta -dev -port 4455` and `vite` on 5173. Vite proxies `/api`, including SSE, to 4455. `-dev` does two things only: it adds `http://localhost:5173` to the allowed Origins, and it serves nothing from `embed`.
- The CSRF token reaches the page from `GET /api/session` in dev and from `<meta name="drashta-token">` injected into `index.html` in prod.
- `npm run dev:mock` runs the SPA with MSW and no Go server, for screen work.

| Makefile target | Does |
|---|---|
| `web` | `npm ci && npm run build` in `web/` |
| `build` | `web`, then `go build -buildvcs=false -o drashta ./cmd/drashta` |
| `install` | build to `/opt/homebrew/bin/drashta` |
| `dev` | Go with `-dev` and `vite` together |
| `test` | `go vet ./... && go test ./...` and `npm --prefix web test` |
| `service` / `unservice` | copy plist, then `launchctl bootstrap gui/$(id -u)` / `bootout` (acme pattern) |
| `logs` | `tail -f ~/.drashta/drashta.log` |
| `reindex` | `drashta -reindex` (drop cache tables, full rescan, exit) |

## 7. Probes

Every probe has a typed `Scan` for the index and, where needed, a `Detail` for live fetch (conflict 17). Every external command goes through `runx.Run`: no shell, 10 s timeout, stdout capped at 8 MB, `LC_ALL=C`.

### 7.1 Overview

| Probe | Reads | How | Cost | Cache key | Re-run when |
|---|---|---|---|---|---|
| projects | `~/projects/<group>/<name>` | `os.ReadDir` on each root | under 5 ms | root dir mtimes | root or group dir event; sweep |
| tree | files of a project | `filepath.WalkDir`, skip list | 10 to 200 ms per project | per dir `(path, mtime)` | FSEvents on that dir |
| git | log, status, branch, remote | `git` CLI | 20 to 150 ms | `.git/HEAD` + ref tip SHA + `.git/index` mtime | event in `.git/` (HEAD, refs, index, packed-refs) |
| fossil | timeline, changes | `fossil` CLI | 30 to 200 ms | tip check-in hash + `.fslckout` mtime | event on `.fslckout` or the repo file |
| gh | PRs, issues, CI | `gh` CLI | 300 to 1500 ms, network | `(repo, ETag)` | project page opened; push seen; 15 min while page visible |
| servers | listening TCP | `lsof`, `ps` (from acme) | 50 to 150 ms | pid + port | 5 s or 60 s ticker |
| claude | sessions | streaming jsonl parse | first run about 1 GB once, then deltas | `(file, size, mtime, offset)` | FSEvents on `~/.claude*/projects` |
| docs | markdown in a project | filter of the tree result | cheap | path + mtime | tree change on `*.md` |
| authored | `.drashta/**/*.md` | YAML front matter + body | cheap | path + mtime + sha256 | FSEvents on `.drashta/` |

### 7.2 projects

- A project is every directory `~/projects/<group>/<name>`. Groups come from `config.roots` (default: personal, work, tools).
- Plain files at group level (for example `tools/plan-2026-09-19c.md`) are ignored.
- The hide list (`config.hidden`, default `venv`, `vendor-v1`; conflict 3) hides a folder from the UI. The folder is still discovered, so it can be unhidden without a rescan (S4).
- ID: the folder name, or `<group>.<name>` for every folder in a name clash (conflict 2). `Project.Group` and `Project.Name` keep the parts.
- A hide-list entry matches the folder name (hides that name in every group), `group/name`, or `group.name`. `hidden = []` hides nothing.
- The indexer does not walk the tree of a hidden project until it is shown. (`venv` alone is 31 thousand entries.)
- Stack detection uses marker files: `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `vite.config.*`. Description is the first non-heading paragraph of the README (300 characters at most).

### 7.3 tree

- `WalkDir`. Skip set: `node_modules .git dist build target venv .venv __pycache__ .next .cache .DS_Store .fslckout`, plus `config.skip`.
- Skipped dirs appear as collapsed nodes with no children. They are never walked.
- Caps per project: 50,000 entries or depth 12. Above that, the result is marked `truncated` and the UI says so.
- A directory that cannot be read is skipped and listed as `denied`.
- Stored: path, is_dir, size, mtime, kind (code, doc, image, other). File bodies are never stored.
- A file preview reads live: first 2,000 lines, 1 MB cap, binary sniff (a NUL byte in the first 8 KB), through `pathguard`.
- Secret files (`.env*`, `*.pem`, `*.key`, `id_*`, `*.p12`) are listed, but their bodies are never shown or indexed.

### 7.4 git

drashta shells out to the `git` CLI. It does not use go-git (ADR 0001).

- Every call is `git -C <path> --no-optional-locks ...`, so drashta never takes the index lock.
- `remote get-url origin`. The remote URL has any `user:password@` removed.
- `log -n 200 --format=%H%x1f%an%x1f%cI%x1f%s%x1e --numstat`: commits, per-day commit counts, plus and minus.
- `status --porcelain=v2 --branch -z`: branch, head and dirty files in one call (it also works in an empty repo). The dirty list is capped at 5,000 paths.
- Detail only: `show --unified=3 <sha>` capped at 400 KB, and `diff` for the dirty diff. The sha must match `^[0-9a-f]{7,40}$` before the command runs.
- Worktrees (`.git` is a file), detached HEAD, empty repos, merge commits, renames and binary numstat rows (`-\t-`) are handled.
- The index keeps the newest 200 commits. Older ones are fetched by the detail API when the user scrolls.
- Commit IDs are full hashes (conflict 21). Extra `-c` options switch off fsmonitor, colour, signatures and path quoting. `GIT_CEILING_DIRECTORIES` stops a broken `.git` from reporting a parent repo.
- `git.CacheKey` (HEAD, ref tip, index mtime) does not change when a tracked file is edited but not staged. The indexer therefore re-runs `status` on every file event in the project, even when the key is equal.
- A probe does not set `Commit.Branch`. The branch rule in section 9.5 needs its own call, which task W2-K adds (`git.Ahead`).

### 7.5 fossil

- Detect `.fslckout` or `_FOSSIL_` in the project root.
- Index: `fossil timeline` (check-ins, newest 200) and `fossil changes`, with `cmd.Dir` set to the project.
- Detail: `fossil info <h>` for the parent, then `fossil diff --from <parent> --to <h>`. The hash must match `^[0-9a-f]{6,64}$`.
- Fossil takes a read lock on its repo DB. A per-project mutex allows one fossil call per project at a time.
- Timeline calls use the full hash (`%H`) and `--utc`. Diffs use fossil's own diff (`-i`), never the user's diff command.
- A check-in's line counts need one process each. A scan fills at most 30 unknown check-ins, newest first, and caches them in memory. Older ones show 0 until later scans. A commit with 0 lines draws at minimum height on the lane. The indexer upserts commits and does not skip rows it already has.
- `fossil changes` lists tracked files only. `fossil extras` is not run, because it walks the whole tree.
- `fossil.CacheKey(p, repo)` uses file times and sizes of `.fslckout` and the repo file. `repo` comes from the last scan result.

### 7.6 claude sessions

- Roots: the 5 profiles from S6 (`config.claude_profiles`).
- Files: `<profile>/projects/<encoded-cwd>/<session-id>.jsonl`. Subagent files under a session folder count toward the parent session.
- **Do not decode the folder name.** The encoding turns `/`, `_` and `.` into `-`, so it is lossy (`acme` becomes `site-v2`). Instead:
  1. Read the `cwd` field from the first records that have one. It is the exact path.
  2. If no record has `cwd`, encode each known project path the same way and match the folder name by longest prefix.
- **Roll-up:** a session belongs to the project whose path is the longest prefix of `cwd`. A session in `.../acme/automation/app/.claude/worktrees/x` counts toward `work/acme`. Sessions outside `~/projects` go to an "unassigned" bucket that Home does not show.
- **Cross-project mentions:** the parser collects absolute paths from `tool_use` inputs (`file_path`, `path`, `command`) and from user text. A path in a different project adds a `session_projects` row with reason `mention` (or `edit`). The session then shows in that project's Sessions tab as "mentioned" (S6).
- Extracted per session: id, profile, cwd, project, start, end, title (first real user text, 90 characters at most), turn count, tools histogram, edited files, size.
- Text is cleaned with the secret-redaction regex ported from the prototype's `sessions.py`. Only the title and short summaries go into the index and FTS. The full transcript is read live, paged by byte offset, with tool output cut to 2 KB per turn.
- **Incremental parse** with the `claude_file(path, size, mtime, offset, inode)` table:
  - Same size and mtime: skip.
  - Grown with the same inode: seek to `offset`, parse only new complete lines, update the aggregates. A line without a trailing newline waits for the next pass.
  - Shrunk or new inode: reparse from 0.
  - Parser: `bufio.Reader` with a 16 MB maximum line. A bad line is skipped and counted. Unknown record types are ignored and counted (R5).
- The first run parses about 1 GB. The target is 60 s at most, in the background, newest files first, so recent sessions appear in seconds.
- The indexer groups a session's files (the parent and its subagents) with `claude.Group` and parses them with `ParseFamily`. A plain `Parse` of one file would double count or drop subagent data on a full reparse. Subagent files add tools, edited files, mentions, end time and size to the parent. They add no turns and no title.
- The same session id can appear under two profiles. `sessions.id` is the primary key, so the indexer keeps the row with more turns.
- A session with no `cwd` whose folder name matches two projects equally (`acme` and `site-v2`) stays unassigned. drashta does not guess.
- A session with no real user text yet has an empty title. The UI shows "claude session".
- The parser follows the prototype parsers and the known record format. The build agent could not read real session files, so it has not run on real data yet. Wave 3 runs it on real files and fixes gaps (R5).

### 7.7 servers (from acme)

- `lsof -iTCP -sTCP:LISTEN -P -n`, then `ps` and `lsof -d cwd` enrichment with the PID cache (acme code as is).
- New step: map `cwd` to a project by longest path prefix. Servers outside `~/projects` show as "other".
- `server_runs` keeps first and last seen per `(cwd, cmd)` key (acme `serverKey`). The Home seismograph draws a teal dot for each day a server of that project was alive.
- drashta excludes its own PID. acme on 4444 shows up as a normal server of `personal/acme`.
- A failed lsof pass keeps the last server state. It never marks all servers dead.

### 7.8 gh (GitHub, minimal)

- Only for projects whose `origin` is on github.com.
- Scope for v1: open PRs (`gh pr list --json number,title,state,isDraft,headRefName,updatedAt,url`), open issues (count and the latest 20), and CI status of the current branch head (`gh run list -b <branch> -L 1 --json status,conclusion,url`).
- `gh` handles its own auth. drashta never reads, stores, logs or forwards the token. It never calls `gh auth token` and never passes `GH_TOKEN`.
- If `gh auth status` fails, the probe returns `unavailable: gh not logged in`. The UI shows a muted note, not an error.
- Calls happen when a project page opens, after a push is seen in `.git/refs/remotes`, and on the project refresh button. The `/github` endpoint caches for 5 minutes. The background refresh runs at most every 15 minutes while a page is visible (conflict 10).

### 7.9 docs

- From the tree result: `*.md`, `*.markdown`, `*.txt` at any depth outside skip dirs, plus `README*`, `CLAUDE.md`, `AGENTS.md`.
- Index: path, title (first `#` line, else the file name), mtime, line count, and the first 4 KB of text for FTS. Rendering happens live in the viewport. Files over 1 MB and secret files are not read.

### 7.10 authored

- Reads `.drashta/{milestones,features,tasks,snapshots,notes}/<id>.md`. Files are the source of truth. The indexer loads them into the tracker tables.
- Details are in section 9.3 and 9.4.

## 8. Indexer

### 8.1 File watching: FSEvents, not kqueue

- drashta uses **FSEvents** through `github.com/fsnotify/fsevents` (cgo, macOS only; A1). One recursive stream watches all of `~/projects`. It needs no open file per watched file. It works the same for 30 thousand or 3 million files.
- Why not kqueue (the pure-Go default of `fsnotify`): kqueue needs one open file descriptor for every watched file and folder. `~/projects` has about 30 thousand watch items today, and the number grows. A launchd job starts with a soft limit of 256 files (`kern.maxfilesperproc` is 92,160). kqueue would break as the folder grows.
- What does grow with more files: the hourly sweep takes longer and `index.db` gets somewhat bigger. Both stay small because only summaries are stored.
- Streams:
  1. `~/projects` (one stream, recursive, latency 1 s, flag `FileEvents`).
  2. One stream over the 5 `~/.claude*/projects` dirs (latency 2 s).
- The watcher sits behind an interface in `internal/indexer/watch.go`. Tests use a fake. A kqueue fallback stays possible.

### 8.2 Event routing

| Path pattern | Action |
|---|---|
| inside a skip dir (`node_modules`, `dist`, ...) | ignored, not counted |
| `<proj>/.git/{HEAD,index,refs/**,packed-refs,FETCH_HEAD}` | git probe for that project |
| `<proj>/.fslckout`, fossil repo file | fossil probe |
| `<proj>/.drashta/**` | authored probe, that file only |
| any other file in `<proj>` | tree update for that dir + activity count |
| `~/projects/<group>/` direct children | projects probe |
| `~/.claude*/projects/**/*.jsonl` | claude incremental parse of that file |

- **Debounce:** events are collected per `(project, probe)` key. A job runs 750 ms after the last event, and at most every 5 s during a storm (for example `npm install` or `git checkout`).
- A queue with 4 workers. At most one job per project at a time. Git and fossil jobs for the same project are serialised.
- If FSEvents reports `MustScanSubDirs` or dropped events, that project gets a full rescan.
- A panic inside a probe is recovered per job and logged with the project ID.

### 8.3 Sweeps and cadences

| Job | When |
|---|---|
| Full sweep: all probes except gh, all projects | at startup (after the cached page is served), then every hour |
| Per-project refresh | `POST /api/refresh {projectId}`: all probes including gh |
| Refresh all | `POST /api/refresh {}`: full sweep, plus gh for projects opened in the last 7 days |
| lsof | every 5 s while at least one SSE client is visible, every 60 s otherwise |
| gh | on project page open (if older than 15 min), and after a push is seen |

- "Visible" is reported by the page: `POST /api/presence {visible}` on `visibilitychange`. The server keeps a flag per SSE connection and drops it when the connection closes.

### 8.4 Per-day activity (the seismograph)

Day means the local calendar date in the system time zone. The row lives in `daily_activity` (section 9.8).

- **files_changed** = count of distinct file paths changed that day, excluding skip dirs and common build outputs (Q14).
  - Live: every FSEvents file event (created, modified, renamed, removed) adds the path to an in-memory set for `(project, day)`. The set is flushed to SQLite every 60 s and at shutdown. The count is `max(stored, len(set))`, so a restart does not count twice. The set is kept for today only. At midnight it is flushed and cleared.
  - Backfill (first run, and after drashta was down): during the sweep, every file whose mtime is newer than the last sweep adds 1 to the day of its mtime, if that path was not already counted that day. This is lossy (a file changed on 3 days counts only on its last day), so those rows are marked `source='mtime'`. Live rows are `source='events'`.
- **commits** = from `git log` and `fossil timeline` dates, grouped by day. Fully rebuildable.
- **sessions** = Claude sessions active on that day (any record timestamp in the day). Rebuildable from jsonl.
- **server_up** = 1 if any server of the project was seen that day. Written by the lsof loop.
- Only `files_changed` from events and `server_up` cannot be rebuilt. That is why they live in history tables (section 9.7).
- Build tools (`npm install`, `vite build`) create thousands of events. Skip dirs are excluded before counting, and debounce limits the work (R6).

## 9. Data model

### 9.1 Principles

- **Observed** data comes from disk and tools (git, fossil, lsof, ps, Claude `.jsonl`, `gh`). drashta never writes it.
- **Authored** data lives per project in `<project>/.drashta/` as markdown with YAML front matter (S9). Those files are the source of truth.
- SQLite (`~/.drashta/index.db`) is a cache plus a history log (S7). You can delete it. Only the history tables are lost.

### 9.2 Entities and refs

| Entity | Kind | What it is | Ref |
|---|---|---|---|
| Project | observed | One folder `~/projects/<group>/<name>`. | `project:acme` |
| Group | observed | The parent folder: `personal`, `work`, `tools`. | none |
| file | observed | A file in the tree. Can take a line range. Images and docs are files with another viewer. | `file:discover.go#L30` |
| image | observed | A file with an image extension. `image:` is an alias of `file:`. | `image:f0_gate.png` |
| commit | observed | A git or fossil check-in. The ID is the short hash (7 characters or more). | `commit:bdcb99c` |
| session | observed | One Claude Code session: one `.jsonl` in one profile. The ID is the UUID. | `session:121c3b19-ada3-...` |
| server | observed | A listening localhost process. Live ref = port. Stable key = `cwd\|cmd`. | `server:3000` |
| Activity event | mixed | One dated tremor: file change, commit, session start or end, server up or down, snapshot, note, task change. | none |
| Feature | authored | A named part of a project that groups tasks. | `feature:F-3` |
| Milestone | authored | A dated target that holds features. | `milestone:M-1` |
| Task | authored | One unit of work. Status: `backlog, todo, in_progress, review, done`. | `task:T-12` |
| Snapshot | authored | One numbered version of a task's visible deliverable: an image plus a verdict. | `snapshot:T-12/v3` |
| Note | authored | A comment on any ref. It can pin to a line or to a point `x,y` (0..1). Open or resolved. | `note:N-40` |

**Ref syntax:** `[<project>:]<type>:<id>[#<fragment>]`

- If the first segment is a known type, the ref is local. Otherwise the first segment is a project ID.
- Fragments: `#L40` or `#L40-L52` (lines), `#x=0.46,y=0.20` (a point on an image).
- `server:3000` is readable, but ports get reused. drashta stores the stable key `server-key:<sha1(cwd|cmd)[:10]>` next to it (Q13).
- `acme:commit:bdcb99c` points at a commit in another project.
- In JSON the API uses the object form `{type, projectId, id, frag?}`. `"ref"` string fields accept either form.

### 9.3 `.drashta/` on disk

```
<project>/.drashta/
  README.md                 # one paragraph that tells humans and Claude the rules in 9.4
  milestones/M-1.md
  features/F-3.md
  tasks/T-12.md
  snapshots/T-12/v3.png     # image (png|jpg|jpeg|webp|gif)
  snapshots/T-12/v3.md      # metadata + verdict
  snapshots/inbox/          # drop folder (see 9.6)
  notes/N-40.md             # one file per note
  .gitignore                # snapshot image patterns (A2), .lock, .trash/, .tmp-*
  .trash/                   # deleted entities
  .lock                     # empty; flock target for the CLI and server
```

- One entity per file. Two writers therefore rarely touch the same file.
- A2: the text files are committed to the project's git. Snapshot images are git-ignored by default through `.drashta/.gitignore`.
- The indexer watches `.drashta/**`. A change updates SQLite within about 1 s and sends an SSE event.
- Unknown front-matter keys are kept on rewrite, and keys keep their order.

**IDs**

- Format: prefix + integer, per project: `M-1`, `F-3`, `T-12`, `N-40`. Snapshot IDs are scoped to the task: `T-12/v3`.
- Allocation is collision-safe. The writer computes `max(existing)+1` and creates the file with `O_CREAT|O_EXCL`. If the file exists, it tries `+1`. Two Claude sessions can never get the same ID on one machine.
- A Claude session without the CLI can do the same in shell: `set -o noclobber; echo ... > tasks/T-13.md`. If that fails, try T-14.
- An ID is never reused, including after a delete. Deleted entities move to `.drashta/.trash/`.
- The file name is the ID. If `id:` in the front matter differs, the indexer uses the file name and shows a warning.
- Two git branches can both create `T-13`. This is accepted in v1 (Q10).

**Front matter**

Milestone `milestones/M-1.md`:

```markdown
---
id: M-1
title: Chambers v3
due: 2026-10-05
features: [F-1, F-2]
state: open            # open | closed
created: 2026-09-29T13:00:00+05:30
---
All three relic chambers reviewed and approved.
```

Feature `features/F-1.md`: `id`, `title`, `milestone` (optional; the milestone file wins on conflict), `created`, and a body.

Task `tasks/T-1.md`:

```markdown
---
id: T-1
title: "Philosophy chamber: star dome over a compass rose"
status: review         # backlog | todo | in_progress | review | done
feature: F-1
assignee: alex + Claude
created: 2026-09-29T13:10:00+05:30
created_by: alex      # or session:<uuid>
updated: 2026-09-30T12:02:00+05:30
links:                 # manual links only; auto links live in SQLite
  - commit:b7dc920bef
  - file:notes/HANDOFF.md#L3
  - server:3000
unlinks: []            # auto links the user rejected
---
The dome should feel like standing under the night sky.

## Log
- 2026-09-30T12:02+05:30 session:121c3b19 posted snapshot v3
- 2026-09-30T13:05+05:30 alex status review -> in_progress
```

Snapshot `snapshots/T-1/v3.md` (next to `v3.png`):

```markdown
---
task: T-1
version: 3
title: Philosophy chamber
image: v3.png
source: url:http://localhost:5173/#/philosophy   # or file:notes/screenshots/ph3.png, or upload
captured: 2026-09-30T12:02:00+05:30
author: session:121c3b19-ada3-495e-a518-7fe5acf0a71e
commit: b7dc920bef     # HEAD at capture time, if any
verdict: pending       # pending | approved | changes
reviewed_by: ""
reviewed_at: ""
review_note: ""
---
Constellation pass; lines now follow the dome curvature.
```

Note `notes/N-40.md`:

```markdown
---
id: N-40
ref: snapshot:T-1/v3   # any ref, with an optional fragment
x: 0.46                # optional, image or snapshot pins (0..1)
y: 0.20
line: null             # optional, for file refs (same as #L30 in ref)
anchor: ""             # optional: text of the pinned line, used to re-find it after edits (Q12)
author: alex
created: 2026-09-30T13:00:00+05:30
resolved: false
resolved_by: ""
resolved_at: ""
reply_to: null         # N-id for a threaded reply
---
Constellation lines slightly too bright against the dome.
```

Author values: `alex` (from `git config user.name`, set once in settings), `Claude`, or `session:<uuid>` when the session is known.

### 9.4 Safe writes (server, CLI and Claude sessions)

1. **Patch fields, never rewrite the file.** Read the file, change only the keys you own for this action, keep the rest byte for byte, write a temp file in the same folder, fsync, then `rename`. The CLI takes `flock(.drashta/.lock)` around read-modify-write.
2. **Optimistic concurrency.** Every entity has a `rev` (sha of the file bytes). `PATCH` sends `If-Match: <rev>`. On a mismatch the server returns 409 with the current entity, and the UI merges or asks. Before any write the server also compares the on-disk hash with the indexed one (R8).
3. **Append-only zones.** Other writers only append to these:
   - the task `## Log` section and the `links:` list: append a line, never edit an old one;
   - notes: to reply, add a new note with `reply_to`. Never change someone else's note text. Resolving changes only `resolved*`;
   - snapshot versions: a new version is a new file. Never overwrite `vN.png`.
4. **Ownership rules for Claude sessions** (written into `.drashta/README.md`):
   - A session can create tasks, notes and snapshots. It can set `status` on a task (never to `done` unless the user said so), append links and log lines.
   - It must not change `title`, the body, `assignee`, `verdict` or `review_note` of items the user wrote, and it must not delete files.
   - The easy path is the CLI, which applies these rules: `drashta task new "title" --feature F-3`, `drashta task status T-12 review`, `drashta task link T-12 commit:abc1234`, `drashta note add T-12 "text"`, `drashta snap add --task T-12 --image <file>` (conflict 27). Author `Claude` or `session:*` cannot set a task to `done` unless the caller passes `--user-said-done`.
5. **Session identity (Q11).** A Claude Code hook exports the session id for the CLI. Fallback: the project's newest `.jsonl` by modified time. Without either, the author is `Claude`.

### 9.5 Auto-linking tasks to commits and sessions

Auto links are **derived**. They live only in SQLite `task_links` with `source='auto'` and a `reason`. A rebuild recreates them. Manual links live in the task file.

| Signal | Rule | Link |
|---|---|---|
| Commit message | Word-boundary match of `\b[Tt]-\d+\b` against tasks that exist in the project. Also trailers `Task: T-12`, `Refs: T-12, T-14`. `Fixes T-12` or `Closes T-12` also **proposes** a move to `done` in the Inbox. The user confirms. | task and commit |
| Branch name | `t-12-...`, `T-12/...`, `feat/T-12-...`. Every commit on that branch that is not on the default branch links to the task. | task and commit |
| Session text | A user turn mentions `T-12` or `task:T-12`. | task and session |
| Session edits | A session writes `.drashta/tasks/T-12.md` or runs `drashta ... T-12`. | task and session |
| Session output | A session edited files in commits made within 2 h of its turns, in the same project. | session and commit (shown on the task through the session) |
| Snapshot | `commit:` in the snapshot front matter. | task and commit |
| Cross-project | `acme:T-12` anywhere above links to the other project's task. | task and commit or session |

- The user can link manually from any preview with "Link to task...". This appends to `links:`. It works for every ref type.
- "Unlink" on an auto link appends it to `unlinks:`, so the indexer stops showing it.
- The badge on each link shows `auto: commit message` or `manual`.

### 9.6 Snapshots: capture, versions and verdicts

One code path, `snap(task, source)`, with three ways in:

1. **CLI** `drashta snap add --task T-12 --image|--path <file> [--title ..] [--note ..]`. (`--path` must be inside the project.)
   - A file is copied to `snapshots/T-12/v<N>.<ext>`. Extensions: png, jpg, jpeg, webp, gif. Size cap 20 MB. `source:` is `file:<rel>` when the file is inside the project, else `upload`. `commit:` is the first 10 characters of git HEAD when the project has `.git`.
   - A `http://localhost:*` URL (loopback only) is captured with **Safari through `safaridriver`** (A3). It is built into macOS and needs "Allow Remote Automation" turned on once. A WKWebView helper is the fallback. The details are decided later, and the capture sits behind an interface. Until it is built, the API answers a `{url}` source with 501 `not_implemented`, and the CLI refuses `--url`.
   - The CLI writes through the `authored` package directly, so it works with the server stopped.
2. **Viewport "Snap" button** on a server or page preview. The user picks a task. The server runs the same URL capture, once it exists.
3. **Drop folder.** Put an image in `.drashta/snapshots/inbox/`, or name it `T-12--anything.png`. The indexer files named images at once. Unnamed ones appear in the Inbox as "unfiled snapshot: pick a task".

Every new version:

- gets `verdict: pending`;
- moves the task from `in_progress` to `review` and adds a log line.

Verdict rules:

- `changes` moves the task back to `in_progress`.
- `approved` leaves the status alone. The UI only **suggests** "move to done" as a one-click chip (A4).
- `pending` resets the verdict.

Not in v1: `screencapture -i` region grab (can be added as `drashta snap T-12 --region`), clipboard paste (same endpoint with multipart), and automatic pickup of Claude's Playwright screenshots (too magic; use the drop folder).

### 9.7 SQLite storage

- Driver: `modernc.org/sqlite` (pure Go). The binary still uses cgo, for FSEvents only (R1). `database/sql`, one writer connection and a read pool.
- File: `~/.drashta/index.db`. Pragmas: `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`.
- All writes go through one goroutine that runs one transaction per job. Reads use the pool.

| Kind | Examples | Rebuildable? |
|---|---|---|
| **cache (R)** | `projects`, `files`, `dirty`, `commits`, `commit_files`, `sessions`, `session_projects`, `claude_file`, `servers_now`, `gh_cache`, the tracker tables from `.drashta/`, `events`, `search` | Yes, from sources |
| **history (H)** | `server_runs`, `file_changes`, `daily_activity` (event-derived columns), `sweep_log`, `settings` | No. Lost if the file is deleted. `settings` is mirrored to `~/.drashta/settings.json`. |

- **FTS5** (built into modernc sqlite): one virtual table `search` with the trigram tokenizer. It covers file paths, doc text (first 4 KB, conflict 20), commit subject and body, session titles and user turns (not tool output), and tracker titles, bodies and note text. It is updated in the same transaction as the source row. A query uses `MATCH` with `bm25()` ranking, then recency. User input is quoted, so it can never be an FTS syntax error.
- **Rebuild:** `drashta -reindex` (or the "Rebuild index" action) drops and recreates all cache tables in one transaction, keeps history tables, then runs a full sweep. Deleting `index.db` also works: drashta recreates it and loses only history.
- **Migrations:** numbered SQL files in `internal/store/migrations/NNN_name.sql`, embedded, applied in order inside a transaction, tracked by `PRAGMA user_version`. A cache-table change may drop and rebuild. A history-table change must migrate data.
- `index.db` is not backed up by design. It holds no source of truth and no secrets.
- A corrupt file is renamed to `index.db.corrupt-<ts>` and a fresh one starts (section 14).

### 9.8 Schema (`~/.drashta/index.db`)

Legend: **R** = rebuildable from disk and tools. **H** = history only.

```sql
-- meta
meta(key TEXT PRIMARY KEY, value TEXT);                -- schema_version, last_sweep_at, token_secret   R
settings(key TEXT PRIMARY KEY, value TEXT);            -- author name, editor override, saved range       H*
                                                       -- (the hidden list lives in config.toml, not here)

-- observed cache (R)
projects(id TEXT PK /*folder name, or <group>.<name> on a clash*/, name, grp, path UNIQUE, hidden INT, vcs TEXT /*git,fossil*/,
         branch, remote, description, stack JSON, readme_path, last_change_at, indexed_at);
files(project_id, path, size, mtime, kind /*code|doc|image|other*/, lines, title /*docs: first H1*/,
      PRIMARY KEY(project_id, path));                  INDEX files_mtime(project_id, mtime DESC)
dirty(project_id, path, state, PRIMARY KEY(project_id, path));
commits(project_id, id, vcs, author, ts, msg, body, plus, minus, nfiles, branch,
        PRIMARY KEY(project_id, id));                  INDEX commits_ts(project_id, ts DESC)
commit_files(project_id, commit_id, path, plus, minus); INDEX cf_path(project_id, path)
sessions(id TEXT PK, profile, jsonl_path, cwd, project_id, start, end, title, turns INT, kb INT,
         tools JSON, edited JSON, jsonl_size, jsonl_mtime);   INDEX sessions_end(project_id, end DESC)
session_projects(session_id, project_id, reason /*cwd|mention|edit*/, PRIMARY KEY(session_id, project_id));
claude_file(path TEXT PK, size, mtime, offset, inode);   -- incremental parse state
servers_now(key TEXT PK, port, pid, name, cmd, cwd, project_id, kind /*dev|helper*/, title,
            first_seen, last_seen);                    -- replaced on every lsof pass
gh_cache(project_id, kind /*pr|issue|run*/, number, title, state, url, updated_at, fetched_at,
         PRIMARY KEY(project_id, kind, number));

-- authored mirror (R, parsed from .drashta/)
milestones(project_id, id, title, due, state, body, file_rev, PRIMARY KEY(project_id,id));
features(project_id, id, title, milestone_id, body, file_rev, PRIMARY KEY(project_id,id));
tasks(project_id, id, title, status, feature_id, assignee, created, created_by, updated, body, log,
      file_rev, PRIMARY KEY(project_id,id));           INDEX tasks_status(project_id, status)
task_links(project_id, task_id, ref TEXT /*canonical string*/, source /*manual|auto*/, reason,
           PRIMARY KEY(project_id, task_id, ref));     INDEX tl_ref(ref)
snapshots(project_id, task_id, version, title, image_path, source, captured, author, commit_id,
          verdict, reviewed_by, reviewed_at, review_note, file_rev,
          PRIMARY KEY(project_id, task_id, version));  INDEX snap_verdict(verdict)
notes(project_id, id, ref TEXT, x REAL, y REAL, line INT, anchor, author, created, resolved INT,
      resolved_at, reply_to, body, file_rev, PRIMARY KEY(project_id,id));
      INDEX notes_ref(ref), INDEX notes_open(resolved, created DESC)

-- events (R except server/file rows, which come from the H tables)
events(id INTEGER PK, project_id, ts, day /*YYYY-MM-DD local*/, kind, ref, label, meta JSON);
      INDEX events_day(day, project_id), INDEX events_proj(project_id, ts DESC)
      -- kind: commit | session_start | session_end | server_up | server_down | file_burst
      --       | task_created | task_status | snapshot | verdict | note

-- history (H)
server_runs(id INTEGER PK, key, project_id, port, pid, name, cmd, cwd, kind, started, last_seen, ended NULL);
      INDEX sr_proj(project_id, started)
file_changes(project_id, day, path, n INT, PRIMARY KEY(project_id, day, path));   -- FSEvents counts
daily_activity(project_id, day, files INT, commits INT, lines_plus INT, lines_minus INT,
               sessions INT, session_minutes INT, snapshots INT, notes INT, server_minutes INT,
               tasks_moved INT, PRIMARY KEY(project_id, day));
sweep_log(id INTEGER PK, ts, scope, ms, ok);

-- search (R)
CREATE VIRTUAL TABLE search USING fts5(
  title, body, kind UNINDEXED, project_id UNINDEXED, ref UNINDEXED, ts UNINDEXED,
  tokenize = 'trigram');
```

- `daily_activity` is mixed. `commits`, `lines_*`, `sessions`, `snapshots` and `notes` can be recomputed from R tables. `files` comes from `file_changes`, which is H. After a DB loss it is back-filled roughly from file mtimes and `commit_files` (rows marked `source='mtime'`). `server_minutes` comes from `server_runs` (H).
- `drashta reindex [project]` drops the R tables (for one project, or all) and walks everything again. The H tables stay (conflict 15).

## 10. API

Base: `http://localhost:4455/api` (the server binds 127.0.0.1). JSON. `{p}` is a project ID (conflict 2).

### 10.1 Guard and errors

- On every `/api` route, including SSE:
  - `Host` must be `localhost:4455` or `127.0.0.1:4455`. This stops DNS rebinding.
  - `Origin`, if present, must match. In `-dev`, `http://localhost:5173` is also allowed.
  - A mutating call (POST, PATCH, PUT, DELETE) needs `Content-Type: application/json` (or `multipart/form-data` for upload routes) **and** `X-Drashta-Token`. The JSON content type forces a CORS preflight that drashta never approves.
  - Body cap 64 KB (an upload route can raise it; snapshot images allow 20 MB).
- No CORS headers are ever sent.
- The checks run in this order: Host, Origin, token, content type, size. Codes: 403 for host, origin or token; 415 for a wrong content type; 413 (code `payload_too_large`) for a big body.
- A DELETE also needs the JSON content type and the token, even with no body.
- Errors: `{error: {code, message}}` with status 400, 403, 404, 409 (rev conflict), 413, 415, 422 or 500.
- Endpoints marked **M** are mutating and need the token.

### 10.2 Endpoints

**Service**

| Method | Path | Response sketch |
|---|---|---|
| GET | `/health` | `{app:"drashta", ok:true}` (single-instance check). `GET /healthz` outside `/api` returns plain `ok`. |
| GET | `/session` | `{token}`. Dev only. In prod the token is in `index.html`. |
| GET | `/events` | SSE stream (section 10.3) |
| GET | `/state?since=<id>` | `{resync, head, events:[{id,type,data}]}`. A missing or 0 `since` means resync. |
| POST | `/presence` | req `{visible, conn?}`. `conn` comes from `hello`. Without it, all open streams are set. An unknown `conn` gives 404. |

**Home and activity**

| Method | Path | Response sketch |
|---|---|---|
| GET | `/boot` | `{now, statuses[], profiles[], author, counts:{liveServers, openTasks, awaitingReview, openNotes}, projects:[{id,name,group,path,vcs,branch,lastChangeAt,hidden,surveyed}]}`. `surveyed` = the first index of that project is done. |
| GET | `/home?range=30` (7 or 30) | `{range, days:["2026-09-02",...], lanes:[{projectId, days:[{files,commits,linesPlus,linesMinus,sessions,snapshots,notes,serverMinutes}], live:{servers:[port], sessions:[id]}, lastChangeAt}]}` |
| GET | `/day?date=2026-09-30[&project=p]` | `{date, events:[{ts, kind, ref, label, projectId}], totals:{...}}` |
| GET | `/inbox?filter=open\|resolved\|all&cursor=` | `{notes:[note + resolved target title], unfiledSnapshots:[], proposals:[{kind:'close_task', taskRef, commitRef}], feed:[event], next}` |

**Project**

| Method | Path | Response sketch |
|---|---|---|
| GET | `/projects/{p}` | `{project, overview:{readme:{path,text}, counts:{files,commits,sessions,openTasks,openNotes}, dirty:[{path,state}], servers:[...], milestones:[...]}}` |
| GET | `/projects/{p}/activity?range=30` | `{days:[{day, files, commits, ...}]}` (the project seismograph) |
| GET | `/projects/{p}/tree?dir=&depth=2` | `{dir, entries:[{path, dir, size, mtime, hasChildren}]}` (lazy) |
| GET | `/projects/{p}/file?path=&from=1&to=400` | `{path,size,mtime,kind,isDoc,lines,from,to,text,truncated,notes:[...]}` (live read) |
| GET | `/projects/{p}/raw?path=` | raw bytes with the right `Content-Type`. Only paths inside the project. Replies carry `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`. HTML, XML and JS files are sent as `text/plain`. A secret file gives 403. |
| GET | `/projects/{p}/commits?cursor=&limit=50&path=` | `{commits:[{id,vcs,author,ts,msg,plus,minus,nfiles,tasks:[id]}], next}` |
| GET | `/projects/{p}/commits/{id}` | `{commit, body, files:[{path,plus,minus}], patch}` (live `git show` or `fossil diff`) |
| GET | `/projects/{p}/dirty` | `{files:[{path,state}], diff}` (live) |
| GET | `/projects/{p}/sessions?cursor=` | `{sessions:[{id,profile,start,end,title,turns,kb,tools,edited,tasks:[id],reason}], next}` |
| GET | `/sessions/{id}?cursor=&limit=40&roles=user,claude,tool` | `{session, turns:[{i, role, ts, text, tool?}], next}` (live, paged by byte offset) |
| GET | `/projects/{p}/docs` | `{docs:[{path,title,mtime,lines}]}` |
| GET | `/projects/{p}/github` | `{prs:[...], issues:[...], runs:[...], fetchedAt}` or `{unavailable:{reason}}` (cached 5 min) |

**Servers**

| Method | Path | Response sketch |
|---|---|---|
| GET | `/servers[?project=p]` | `{active:[{key,port,pid,name,cmd,cwd,projectId,kind,title,firstSeen}], recent:[{key,...,lastSeen}]}` |
| POST **M** | `/servers/kill` | req `{pid, key, confirm:true}` gives `{ok}` (SIGTERM, then SIGKILL after 3 s) |
| POST **M** | `/servers/restart` | req `{key, confirm:true}` gives `{ok, log:"~/.drashta/logs/<port>.log"}` (409 if a restart is already pending) |

**Tracker**

| Method | Path | Response sketch |
|---|---|---|
| GET | `/tracker?project=&filter=all\|review&feature=&status=` | `{milestones[], features[], tasks:[{..., latestSnapshot:{version,verdict}, linkCount, openNotes}]}` |
| GET | `/projects/{p}/tasks/{id}` | `{task, rev, links:[{ref, source, reason, resolved:{title,subtitle,date}}], snapshots[], notes[], log[]}` |
| POST **M** | `/projects/{p}/tasks` | req `{title, featureId?, status?, assignee?, body?, links?[]}` gives `{task, rev}` (201) |
| PATCH **M** | `/projects/{p}/tasks/{id}` | `If-Match: rev`; req `{status?, title?, featureId?, assignee?, body?}` gives `{task, rev}` |
| POST **M** | `/projects/{p}/tasks/{id}/links` | req `{ref}` gives `{links}` |
| DELETE **M** | `/projects/{p}/tasks/{id}/links?ref=` | manual: removed. Auto: added to `unlinks`. |
| POST / PATCH **M** | `/projects/{p}/features[/{id}]`, `/projects/{p}/milestones[/{id}]` | same pattern as tasks |
| DELETE **M** | `/projects/{p}/{tasks\|features\|milestones}/{id}` | moves the file to `.trash/` |

**Snapshots**

| Method | Path | Response sketch |
|---|---|---|
| GET | `/projects/{p}/tasks/{id}/snapshots` | `{snapshots:[{version,title,url,captured,author,commit,verdict,reviewNote,notes:[...]}]}` |
| POST **M** | `/projects/{p}/tasks/{id}/snapshots` | multipart `image`, or JSON `{url}` or `{path}`, plus `title?`, `note?` gives `{snapshot}` |
| PUT **M** | `/projects/{p}/tasks/{id}/snapshots/{v}/verdict` | req `{verdict:'approved'\|'changes'\|'pending', note?}` gives `{snapshot, task}` (the task status may change) |
| POST **M** | `/projects/{p}/snapshots/inbox/{name}/file` | req `{taskId}` files an unfiled drop |

**Notes, refs, search, actions**

| Method | Path | Response sketch |
|---|---|---|
| GET | `/notes?ref=&project=&open=1` | `{notes[]}` |
| POST **M** | `/notes` | req `{ref, text, line?, x?, y?, replyTo?}` gives `{note}` (stored in the ref's project; `anchor` is filled from the line text) |
| PATCH **M** | `/projects/{p}/notes/{id}` | req `{resolved?}` from anyone, `{text?}` only from the author |
| GET | `/resolve?ref=` | `{ref, type, projectId, title, subtitle, icon, date, obj, nativeLabel, nativeActions:[...], notes[], tasks[]}` |
| GET | `/search?q=&project=&kind=&limit=30` | `{hits:[{kind, ref, projectId, title, snippet, ts}]}` |
| POST **M** | `/refresh` | req `{projectId?}` gives `{started:true}`. Progress arrives over SSE. |
| POST **M** | `/open` | req `{ref, action?:'default'\|'editor'\|'finder'\|'terminal'\|'safari'\|'resume'}` gives `{ok, ran:"code -g ..."}` |
| GET / PATCH **M** | `/settings` | `{hiddenFolders[], author, editorCmd}`. Hidden folders are written to `config.toml` (conflict 4). |

### 10.3 Live updates (D5)

- `GET /api/events` is `text/event-stream`. A `: ping` comment every 25 s keeps it open. The first event is `hello {now, counts, conn}`, and it is sent again after every reconnect.
- Each event has a monotonic `id` (the hub's sequence number). The hub keeps the last 1,000 events in a ring buffer.
- The server coalesces `project` events: the first one goes out at once, and later ones inside the same second are merged and sent after it. If the merged events differ, `what` becomes `all` and `parts` lists them.
- Event IDs start at the server start time in microseconds. An ID from before a restart is older than the new head, so the page gets `resync` and never a wrong replay.
- A stream that falls 256 events behind is closed. The page reconnects with `Last-Event-ID`.
- When a page connects it counts as visible until it says otherwise. After every `hello` the page sends its current visibility again.

| event | data | Page does |
|---|---|---|
| `hello` | `{now, counts, conn}` | sets the counters; remembers `conn` for presence |
| `project` | `{projectId, what:'files'\|'commits'\|'dirty'\|'sessions'\|'docs'\|'all', parts?}` | refetches that part (`all`: the whole project summary) |
| `activity` | `{projectId, day}` | refetches the lane |
| `servers` | `{projectId?, active:[port]}` | refetches servers |
| `tracker` | `{projectId, kind:'task'\|'feature'\|'milestone'\|'snapshot'\|'note', id}` | refetches the item |
| `scan` | `{projectId?, state:'running'\|'done', at}` | shows "rescanning..." or "updated 12:04" |
| `counts` | `{liveServers, openTasks, awaitingReview, openNotes}` | updates the counters |
| `resync` | `{}` | refetches everything visible |

- **Resync:** `EventSource` reconnects with `Last-Event-ID`. If that id is still in the ring, the hub replays the missed events. If not, it sends `resync`.
- **Visibility:** on `visibilitychange` to visible, the page calls `GET /api/state?since=<last id>`. The answer is the missed events or `{resync:true}`. This covers the case where WebKit suspended the page and the SSE socket died silently.
- **Plash:** Plash shows the page in a WKWebView behind all windows. WebKit may throttle timers or suspend the page while it is covered. So the page has no timers for correctness, no animation loop, and all catch-up happens on visibility and reconnect. The page reads `html.plash-is-browsing-mode` (D3) only for layout, never for data.
- lsof runs every 5 s while at least one `/events` stream is visible, and every 60 s otherwise (S8).

### 10.4 Native actions ("open out", U9, D4)

- Endpoint: `POST /api/open {ref, action?}`. The server resolves the ref to a real path from the index. The page never sends a path or a command.
- Execution: `exec.Command(name, args...)` with a fixed binary per action. Never `sh -c`, except acme's restart.
- Path rule: the resolved path is cleaned, symlinks are resolved, and it must be inside a known project root (`pathguard`). Otherwise 400.
- Every argument that comes from data goes after `--` where the tool supports it, or is checked to not start with `-`.

| Item | Command | Notes |
|---|---|---|
| file | `<editor> -g <path>:<line>` | `editor` from config, default `code`; argv template `["code","-g","{path}:{line}"]` |
| folder / project | `open -R <path>` (Finder), `open -a <terminal> <path>` | the `terminal` setting is used here only (default `Terminal`). Editor on a folder: `code <path>`. |
| image / snapshot | `open -a Preview <path>` | |
| commit (git) | terminal running `git -C <path> show <sha>` | sha must match `^[0-9a-f]{7,40}$` |
| commit (fossil) | terminal running `fossil diff --checkin <h>` | hash must match `^[0-9a-f]{6,64}$` |
| session | Terminal.app in the project dir: `env CLAUDE_CONFIG_DIR=<profile> claude --resume <id>` | id must be a lowercase UUID; the profile must be an absolute path |
| server / URL | `open -a Safari http://localhost:<port>` | only `http://localhost:<port>` or `http://127.0.0.1:<port>`. A GitHub link must be `https://github.com/...`. |
| task / note | `<editor> <path>/.drashta/tasks/<id>.md` | |

- A terminal with a command runs in **Terminal.app** (conflict 19). Nothing is written to disk. `osascript` runs a fixed script (`do script`). The command goes in as separate argv items after `--`, and each item is quoted by AppleScript `quoted form of`. No string is built into AppleScript source. The first use asks once for macOS Automation permission (R7, C4).
- The settings (`editor`, `terminal`, project roots) live in `actions.Config`. Use its `Plan` and `Do` methods. The package-level `Plan` has no roots and refuses every path target.
- The planner checks each type's allowed actions, a port against `^[0-9]{1,5}$` (1 to 65535), a relative path with no `..` element, and the URL rules in the table.
- **Destructive actions** (kill, restart): the UI shows a confirm step (the button turns into "Confirm kill :5173" for 5 s). The server also requires `{confirm:true}`. Kill only works on a PID in the current live lsof set. Restart only replays a `(cwd, cmd)` that drashta recorded itself, keeps acme's `restarting` grace map, and logs to `~/.drashta/logs/<port>.log`.
- Actions are logged to `drashta.log` as `action=<kind> ref=<ref> ok=<bool>`.

## 11. UI summary

This section sums up the behaviour of every screen. **The frozen prototype is the visual authority.** Markup, spacing, tokens and behaviour come from the design prototype (Main, Home, Project, Tracker, Inbox, ViewCode, ViewMedia) and the published artifact. This text and the prototype agree. If they plainly differ, `00-decisions.md` decides. Otherwise the prototype wins.

### 11.1 Principles and global shell

- **Preview in, open out** (U9). A click on an item opens a preview in the right-hand viewport. "Open ↗" runs the native command. drashta never loads another app's page inside itself.
- **Nothing is thrown in your face.** No pop-ups, no modal alerts, no auto-opened panels, no pulsing badges.
- **Any detail is a few clicks away.** Home, lane, day, item, Open ↗.
- **Time is the main axis.** Each event is a tremor on a lane.
- **Hierarchy without bold or italic.** Use size, colour, letter-spacing and case.
- **A live instrument.** The page updates when data changes and does not animate while it waits.
- **Plash-aware.** The same page is the wallpaper (readout mode) and the command center (browsing mode).

Layout in browsing mode:

```
┌ drashta  every tremor...    [SEARCH ____________]  last scan 14:53  [↻ refresh all]  live 2 · tasks 9 · review 1 · notes 3 ┐
│ rail │ section content (Home / Project / Tracker / Inbox)                       │ viewport (when an item is open) │
└──────┴──────────────────────────────────────────────────────────────────────────┴─────────────────────────────────┘
```

- **Top bar**, 56px high, bottom hairline. Top left: the app name **drashta**. It is the only link to Home (U2). There is no home, map, pulse or reviews button. On Home, a subtitle reads "every tremor in your projects, last 30 days" (the number follows RANGE). On other sections: a breadcrumb in spaced uppercase, for example `PROJECT › acme › COMMITS`.
- **Left rail**, 72px wide: **Project**, **Tracker**, **Inbox** (U4). Each has a stroke icon and a 10px uppercase label.
  - Project opens the last project you viewed. With none, it opens a project picker (lanes without bars).
  - Tracker badge: snapshots awaiting review. Inbox badge: open notes. At 0 the badge is not shown. A badge has no animation.
  - The active item has an amber 2px bar on the rail edge and text colour `text`. Others are `muted`.
- **Live counters** (top bar, right). One bordered strip, four buttons:

| Counter | Colour | Click goes to |
|---|---|---|
| `live n` (servers running) | teal | Home, ordered by recency, live lanes lit |
| `tasks n` (open tasks) | amber | Tracker, All tasks |
| `review n` (snapshots pending) | ivory | Tracker, Awaiting review |
| `notes n` (open notes) | coral | Inbox, Notes: Open |

### 11.2 Search, refresh, viewport, toasts, keyboard

**Global search**

- One field in the top bar: label `SEARCH`, placeholder `name, commit, doc, :port, stack`, width 300px, height 42px.
- It searches the SQLite index (S7):

| Kind | Fields matched | Opens |
|---|---|---|
| Project | name, description, stack, path | project page |
| File / doc | path, file name; text of indexed `.md`/`.txt` | file preview |
| Commit | message, body, id (git and fossil) | commit preview |
| Session | title, user and Claude turn text | session preview |
| Server | port, title, process, cmd | server preview |
| Task | id, title, assignee | task preview |
| Snapshot | title, image path, review note | snapshot preview |
| Note | text | the item the note is on |

- Plain words: case-insensitive substring match on all kinds. `:4455` matches only server ports (Q9: no other prefixes in v1).
- Results panel: a dropdown below the field, 680px wide, 60% of the viewport height at most, grouped by kind. Each group has an uppercase header in the kind's signal colour. The header line reads `12 MATCHES · 3 LANES LIT`.
- On Home, matching lanes get a faint tint (`match`) and an amber name. Other lanes dim to 22% opacity.
- Updates as you type. Nothing opens until you choose a result. `Enter` opens the first. `↑`/`↓` move. `Esc` closes the panel, and a second `Esc` clears the query. No match: "nothing registers for that."

**Refresh and scan status**

- `↻ refresh all` is the only filled amber button on the page (44px high). It asks the server to rescan all projects.
- The scan label sits at the left: `last scan 14:53` then `rescanning...` then `updated just now` then `updated 4m ago`. Relative time is recomputed only when the page re-renders or becomes visible. There is no timer.
- During a user-started rescan, one amber 2px line sweeps once across the lanes (1 s). With reduced motion there is no sweep, only the label.
- Each project page has its own `↻ refresh` (amber outline). A scan that finds nothing new shows the toast "All projects rescanned · no new changes".

**Preview viewport**

- A right-hand panel, 46% of the width, 560px minimum. It overlays the section with a single left shadow (the only shadow in the app). The section stays scrollable.
- Header row (36px controls): `←` `→` (viewport history), type chip, title, subtitle (project · date), **Open ↗** (amber outline; the tooltip shows the exact command), `✕`.
- Tasks row: linked task chips, "not linked" when none, and a `Link to task...` select.
- Notes: every type accepts notes. File notes pin to a line. Image and snapshot notes pin at x,y. Other types take whole-item notes. After a file edit, a line note is re-anchored by its stored line text, or shows "line moved?" (Q12).
- Missing item: "Nothing to show here." and one muted line that the item is not in the latest scan.

| Type | Preview body | Open ↗ runs |
|---|---|---|
| file | Code with line numbers; language, size, modified, state. `.md`/`.txt`: Rendered / Source toggle. Click a line number to pin a note. Long files: the first part plus "Open ↗ for the rest". | editor at the line |
| image | Image fit to width; size, path; pin notes; "Used in snapshots"; `Make snapshot for task...`. | Preview |
| commit | id, vcs, author, date, `+n −n`; files changed; per-file diff blocks with `Expand all` / `Collapse all`; a commit note. | terminal: `git show` or `fossil diff` |
| session | profile, start, duration, turns, tool counts, files edited; transcript with a role filter (user, claude, tool). | terminal: `claude --resume` |
| server | port, pid, process, cmd, kind, state; `Stop` / `Start`; the **live app** in a sandboxed frame with Expand, or the last capture if it refuses framing (U10). If unreachable: "This site can't be reached" plus the last capture. Not HTTP: "Not a web page". | Safari |
| snapshot | Annotated image; version list; `Compare with` a second version; verdict controls. | Preview |
| task | Status, feature, milestone, assignee, created; links; snapshot strip; notes; edit fields. | editor on the task file |

**Toasts**

- One at a time, bottom centre, 12px text, `raised` fill, 1px amber border. About 2 s, then fades (no fade with reduced motion). `role="status"`.
- They confirm what you did ("Opened in Preview", "T-12 → review", ":5173 stopped"). A failed native command gives a coral-border toast with the error that stays until dismissed. A background event never makes a toast.

**Keyboard** (shortcuts do nothing while a text field has focus, except `Esc`, and are off in readout mode. One global handler may serve them. The first build has only `Esc`, the arrows and `Enter` on their own elements; the global shortcuts are a Wave 3 task.)

| Key | Action |
|---|---|
| `/` | focus search |
| `Esc` | close search, then deselect day, then close viewport |
| `g h` / `g p` / `g t` / `g i` | Home / Project / Tracker / Inbox |
| `←` `→` on a lane | previous / next day; `↑` `↓` previous / next lane |
| `Enter` on a day | read that day; on an item: preview it |
| `o` | Open ↗ for the item in the viewport |
| `[` `]` | viewport back / forward |
| `r` | refresh (the project on a project page, else all) |

### 11.3 Home: the seismograph (U1)

Layout, top to bottom: top bar; control row (40px); date axis (16px, sticky); lanes in a scrolling area; day readout panel (128px, always on screen).

**Control row**

- Left: group chips `all 17` `personal 12` `work 2` `tools 3` and a toggle `○ show hidden (2)` / `◉ show hidden (2)`. Counts leave hidden folders out unless show hidden is on. (The numbers are examples; the app computes them.)
- Right: `ORDER` `recency | loudest` and `RANGE` `7d | 30d`, as segmented buttons with `aria-pressed`.

**A lane** (row 36px high)

| Column | Width | Content |
|---|---|---|
| Identity | 230px | name (13px, a button to the project page) · group tag · `±n` uncommitted paths (amber) · `HIDDEN` tag when shown hidden |
| Tremor | fluid, 600px minimum | one column per day; bottom hairline as the baseline |
| State | 150px, right-aligned | last touched `13m ago` (muted) · `● n` teal when servers are live, else `○` in hairline colour |

- Day column, two bars side by side, bottom-aligned: left bar grey `#6b675d` = file changes (log scale, 2px minimum above 0). Right bar amber = commits, height = lines changed, log scale, 4px minimum. A commit with no line stats (fossil) draws at the minimum height.
- **A5:** a Claude session puts a small violet tick at the top of the day column. Details appear in the readout.
- The scale is **shared across all lanes** on Home (global maximum), so lanes can be compared. The project page uses its own scale.
- The whole day column is a button with a full `aria-label` ("acme, 14 Sep: 12 file changes, 2 commits"). The selected day gets the `select` fill and a 1px amber inset line.
- States (text at bottom left, 11px): `empty directory` (no files), `flat — no tremor in 30d` (no activity in range).

**Order, filters, hidden folders, range**

- `recency` (default): by last touched, newest first. `loudest`: by file changes plus commit lines in the range, then recency. The order is stable. A lane moves only when data changes, never on hover.
- Group chips filter to one group. With 0 lanes: "no projects in this group."
- Hidden folders (S4): default set `venv`, `vendor-v1`. You hide or unhide from a project page. The list is server config, so it is the same in every window. With show hidden on, hidden lanes show at 60% opacity with a `HIDDEN` tag.
- RANGE changes the axis, lanes, flat test and subtitle. A selected day outside the new range is cleared.
- Control state (group, order, range, show hidden) is kept in `localStorage`, so the wallpaper shows the same view. Reads and writes are wrapped in try/catch.

**Day readout panel**

- No day selected: one line in 14px muted ("click any day in a lane to read its tremor here.") and the legend: `▮ file changes that day` (grey), `▮ commits (height = lines changed, log scale)` (amber), `● servers live now` (teal), `±n uncommitted paths` (amber), `name → project page`.
- Day selected, three zones: left (230px) `READOUT`, date (28px), `acme →`; middle (200px) counts: `file changes`, `commits` (amber), `lines +340 −88`, `sessions` (violet), `snapshots` (ivory), `notes` (coral); right (fluid, scrolls) the day's items in time order: time · glyph · id/label · context. Glyphs: commit `◆` amber, session `✦` violet, snapshot `▣` ivory, note `✎` coral, task created `▮` amber, then `files changed` (top 20, then "+N more").
- A click on an item opens it in the viewport. Quiet day: "files moved, nothing committed." or "a flat line — no tremor this day." `Esc`, or a click on the selected column, clears the readout.

**Many projects and live updates**

- The lane area scrolls vertically. Axis, controls and readout stay fixed. No pagination. No virtual list below 200 lanes. Above 200, only visible rows render.
- The server pushes changes over one SSE stream (section 10.3). Only the affected lanes re-render, with no flash. On `visibilitychange` to visible, and on stream reconnect, the page fetches a full snapshot and resyncs. There is no reload timer and no polling in the page.
- If the stream is down, the scan label reads `offline — last scan 14:53` in coral and the last data stays.

### 11.4 Plash-aware modes (D1 to D6)

- Plash adds the class `plash-is-browsing-mode` to `<html>` when Browsing Mode is on.
- The Plash URL is `http://localhost:4455/?desktop=1`. **Readout mode = `desktop=1` and no class** (Q7). A normal Safari tab, or the class, gives browsing mode. CSS keys off `html.desktop:not(.plash-is-browsing-mode)`. A class change switches mode at once, with no reload.

| Element | Readout mode | Browsing mode |
|---|---|---|
| App name + subtitle | shown | shown |
| Live counters | shown, read-only | shown, buttons |
| Scan label | shown | shown |
| Search, refresh all | hidden | shown |
| Rail | hidden | shown |
| Group / order / range controls | hidden (saved values apply) | shown |
| Date axis, lanes, `±n`, live dots, ago | shown | shown |
| Selected-day highlight, day readout contents | hidden; the panel shows the legend | shown |
| Viewport, search panel, toasts, hover states | hidden | shown |
| "⌥⌘D to interact" hint | shown, bottom right, 10px uppercase, muted | hidden |

- Readout layout: lanes that do not fit are not scrolled. The last row reads `+N quieter projects` (muted). Padding: 32px top, 80px bottom, for the macOS menu bar and Dock.
- **Navigation rule (D4):** the page never navigates the top frame to another origin. No `target=_blank`, no external `href`. Every Open ↗, GitHub link and Safari link is a `POST /api/open` to the local server. In-app routes use client-side routing only.
- **Performance:** no CSS animation and no `requestAnimationFrame` loop at rest. Live dots are static. Transitions are 150ms at most and only follow a user action. Readout mode has no hover handlers. Idle CPU is near 0%.
- **Fallback (D6):** the same page in Safari as an "Add to Dock" web app, without `?desktop=1`, runs in browsing mode.

### 11.5 Project page

**Header cartouche:** a box with a `3px double hairline` border and square corners. Line 1: project name (32px), group tag, `HIDDEN` tag. Line 2 (12px muted): path · stack · `git main` or `fossil trunk` · remote host. Right: RANGE `7d | 30d`, scan label, `↻ refresh`.

**Native actions** (44px outline buttons; each runs through the server)

| Button | Shown when | Runs |
|---|---|---|
| Editor | always | `code <path>` |
| Finder | always | `open -R <path>` (conflict 14) |
| Terminal | always | `open -a Terminal <path>` |
| Open in Safari | a server is live | `open -a Safari http://localhost:<port>` (teal outline) |
| Stop server(s) / Start server | servers known | kill pid / rerun cmd (coral outline for stop) |
| GitHub | a remote exists | `open -a Safari <remote>` |
| Resume Claude | a session exists | latest session, its profile, `claude --resume` |
| Hide / Unhide project | always | updates the hidden list |

**Project seismograph:** a `panel` fill panel, 150px tall, with its own scale, a `max · 0` gutter and two dashed gridlines. Values show above bars at 7d, for the selected day, and for the peak day. Header line: the peak (`peak 14 Sep · 48 file changes`) and the legend. A day click fills a one-line readout (`read the day →` opens that day's full list in Overview).

**Tabs** (40px bar; active tab has a 2px amber underline; labels carry counts such as `Commits 42`, `Servers 1/2`). Every row previews in the viewport. Nothing expands inline.

| Tab | Shows | Empty state |
|---|---|---|
| Overview | Vitals grid; **Working copy · uncommitted** list; **Last commit**; **Where you left off** (latest session, Continue); **Open notes**; **GitHub block** (A6): PRs, issues, CI from `gh`, loaded when the tab opens | "Clean — nothing uncommitted." / "not under version control." |
| Files | Tree of indexed files (skip dirs collapsed and marked); size, modified; dirty files marked `±` | "empty directory — nothing to list." |
| Commits | git and fossil check-ins, newest first: id (amber) · date · message · `+n −n` | "no commits on record." |
| Sessions | Sessions in this folder or its subfolders, then "mentioned in other projects' sessions" | "No Claude sessions ran in this folder." |
| Servers | port · pid · process · cmd · kind · state dot; Safari ↗; Stop/Start | "No localhost listeners belong to this project." |
| Docs | `.md`/`.txt` in the tree; click gives a rendered preview | "No .md or .txt files in the indexed tree." |
| Tracker | This project's milestones with progress, features, task counts by status, awaiting review; `Open board →` | "No milestones or tasks for this project yet — start them on the board." |

### 11.6 Tracker

Data lives in each project's `.drashta/` folder (S9). The footer reads "stored as .drashta/tasks/*.md in each project".

- **Controls:** `Project` select; `Show` `All tasks | Awaiting review (n)` (the top-bar `review` counter sets `?filter=review`); `View` `Board | Milestones | Timeline`; `Group` `None | Feature` (Board only); `+ Add task`.
- **Board:** five columns in this order. Each header has a square glyph in its status colour, the label and a count.

| Status | Label | Colour |
|---|---|---|
| backlog | Backlog | `#6b675d` |
| todo | To do | `#a39e92` |
| in_progress | In progress | `#ffb347` |
| review | Review | `#b69cff` |
| done | Done | `#6fd6c4` |

  - Card: id (muted) · title · feature · assignee · counts (links, open notes). A **snapshot strip** shows thumbnails oldest to newest, 40px tall, with a 1px border in the verdict colour (pending hairline, approved teal, changes coral) and `vN` under each.
  - A click opens the task preview. `←`/`→` buttons move the status one step ("T-12 → review"; at the ends: "Already first status" / "Already done"). No drag and drop in v1.
- **Milestones:** one block per milestone: title, due date, progress bar (done / total, teal on hairline), remaining count, its features with their tasks. "Outside milestones" and "Unscheduled" collect the rest.
- **Timeline:** milestones on a date axis with tasks as ticks at their created date and a done mark.
- **Create:** an inline form at the top of the board, not a modal: title (required: "Give the task a title."), project, feature, status (default `todo`). Toast: "Task T-14 created".
- **Edit** (in the task preview): title, status, feature, milestone, assignee save on blur. Links: any item can be linked from the viewport header. Notes: add, resolve, reopen.
- **Awaiting review** (U3): shows tasks whose latest snapshot is `pending`. With none: "Nothing awaiting review." and `Show all tasks`. Layout: task list on the left, the review area in the main column.
  - Versions list (`v3 · 14 Sep · alex · pending`). Frame: the image fit to the area. `Annotate` toggle: click to drop a numbered ivory pin; each pin has a note ("What should change here?"). `Compare with` shows two versions side by side at the same scale.
  - Verdict: `Approve` (teal outline), `Request changes` (coral fill; needs a review note), `Reset to pending`. Approving the latest snapshot shows a one-click "move to done" suggestion chip (A4). It never moves the task by itself.
  - `Open to annotate ↗` opens the snapshot in the viewport. `Task detail →` opens the task preview.

### 11.7 Inbox

- Heading `INBOX`, sub-line "everything addressed to you".
- **Needs you** (top block, `3px double hairline` border). Each line is a count and a link. A line with 0 is not shown: open notes; snapshots awaiting review; tasks in review; dirty working copies (project · `±n` · oldest change). With all at 0: "Nothing needs you."
- **Notes:** filter `Open | Resolved | All`. Row: text · on `file acme/main.go:42` · project · author · date · `Resolve`/`Reopen`. A click previews the item, scrolled to the line or pin.
- **Activity:** a unified feed of all projects, grouped by day. Filter `All | Commits | Sessions | Snapshots | Tasks | Notes`. It also lists proposals (for example "close T-12?") and unfiled snapshots (pick a task).

### 11.8 Visual system (U5, U6, U8)

| Token | Value | Use |
|---|---|---|
| `ground` | `#0b0b0a` | page background |
| `panel` | `#121210` | viewport, chart panels, popovers |
| `raised` | `#1a1916` | toasts, inputs, card fill |
| `hairline` | `#2c2b27` | borders, dividers, lane baseline, `○` |
| `hairline-faint` | `#1d1c19` | row separators, gridlines |
| `select` | `#221c12` | selected day column or row |
| `match` | `#15130f` | search-matched lane tint |
| `text` | `#e8e4da` | primary text |
| `muted` | `#a39e92` | labels, secondary text (the lowest colour for text) |
| `dim` | `#6b675d` | file-change bars, backlog, non-text glyphs only |
| `amber` | `#ffb347` | commits, primary action, focus, active tab, in_progress |
| `teal` | `#6fd6c4` | live servers, approved, done |
| `violet` | `#b69cff` | Claude sessions, review status |
| `ivory` | `#f3efe4` | snapshots, review pins |
| `coral` | `#ff8a66` | changes requested, notes, errors, stop actions |

- Signal colours always come with a glyph or label, never colour alone.
- **Type:** stack `'JetBrainsMono Nerd Font Mono', 'JetBrains Mono', ui-monospace, monospace`. `font-weight: 400` and `font-style: normal` on everything, including headings, `b`, `strong`, `em`, `th`, buttons and rendered markdown (bold becomes `text` colour, italics become `muted`). Line height 1.5 for body, 1.1 for display.

| Role | Size | Case / tracking | Colour |
|---|---|---|---|
| App name | 26px | lower, 0.01em | text |
| Page title (project name) | 32px | as written | text |
| Readout date | 28px | as written | text |
| Subtitle, empty-state sentence | 14px | lower, sentence | muted |
| Body, rows, buttons | 12 to 13px | as written | text |
| Secondary meta | 11px | as written | muted |
| Label (SEARCH, ORDER, READOUT) | 10px | UPPER, 0.14em | muted |
| Map-style heading (cartouche, section heads) | 10 to 11px | UPPER, 0.22em | text |
| Tag (group, HIDDEN) | 10px | UPPER, 0.10em | muted |

- **Space:** scale 4, 8, 12, 16, 24, 32, 40. Page padding 24px at every width (`02` section 8.3 says 40px from 1440px; the frozen prototype uses 24px and wins, conflict 24). Rows have 0 gap with hairline separators. Borders are 1px `hairline`. Radius 3px at most (0 for lanes, charts and cartouches).
- No shadows except the left edge of the viewport and the search panel. No gradients. No left-border accent cards.
- Buttons: 1px hairline border, transparent fill. Primary: amber fill (refresh all only) or amber outline. Danger: coral outline.
- **Map touches (U6):** the cartouche (project header and Inbox "Needs you") has a `3px double hairline` border. Section heads use spaced uppercase (0.22em). One ornament per screen at most (a scale tick row under the date axis, or a bearing mark by the scan label). No compass roses, no illustrations.

### 11.9 Accessibility

- Focus: `1px solid amber` outline with a 2px offset on `:focus-visible`. Day columns use a roving tabindex, one tab stop per lane.
- Contrast: `text` and `muted` on `ground` and `panel` pass 4.5:1. `dim` is never used for text that carries meaning.
- Targets: primary 44px, secondary 36px minimum. At 30d a day column can be narrower than 36px, so the lane row is the target and arrow keys pick the day.
- Semantics: rail is `nav`, viewport is `aside`, tabs are `tablist`, toasts are `role="status"`, segmented controls use `aria-pressed`. Every bar column has a full `aria-label`.
- Reduced motion: no sweep, no toast fade, no transitions. No colour-only meaning anywhere.

## 12. Security

- Bind `127.0.0.1:4455` only. Never `0.0.0.0`, never the IPv6 wildcard. No TLS, no auth, no remote access in any form.
- The **guard** (section 10.1) runs on every `/api` route, including SSE.
- **CSRF token:** at startup the server makes 32 random bytes. They go into `index.html` as `<meta name="drashta-token">` and must come back as `X-Drashta-Token` on every mutating call, compared with `subtle.ConstantTimeCompare`. The token changes on every restart. On 403 the page reloads once.
- Static SPA files are served without the guard (they are public code), but `index.html` is only served for an allowed Host, so the token cannot leak to a rebinding page.
- No data-driven shell. All commands use `runx` or `actions` with an argv list. The one exception is acme's `restart`, which replays a command string drashta recorded itself.
- All file reads for preview, `raw`, and all native actions go through `pathguard`: clean, resolve symlinks, must be inside a known project root.
- No secrets stored. jsonl text is redacted before indexing. `.env*`, `*.pem`, `*.key`, `id_*` and `*.p12` files are listed but their bodies are never shown or indexed.
- `gh` runs as the user with its own auth. drashta never reads or forwards the token.
- The raw file endpoint sends `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`, and serves HTML, XML and JS as `text/plain`, so a project file cannot run as a page on the drashta origin.
- The page never sends a path or a command. It sends a ref. The server resolves it.
- URL snapshots accept loopback URLs only.

## 13. Operations

### 13.1 launchd (S2)

`in.drashta.plist` is a copy of acme's with changed fields:

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
<key>StandardOutPath</key><string>~/.drashta/drashta.log</string>
<key>StandardErrorPath</key><string>~/.drashta/drashta.log</string>
```

- `PATH` must include `/opt/homebrew/bin`, because launchd does not load the shell profile (fossil, gh, node and code live there).
- `make service` installs and bootstraps. `make unservice` removes. Same flow as acme.

### 13.2 Config

`~/.drashta/config.toml` is created with defaults when missing. Changes are picked up on restart (no hot reload in v1). `DRASHTA_HOME` overrides `~/.drashta` (tests use it).

```toml
projects_dir = "~/projects"
roots   = ["personal", "work", "tools"]
hidden  = ["venv", "vendor-v1"]
skip    = []                       # extra skip dirs for the tree walk
editor  = ["code", "-g", "{path}:{line}"]
terminal = "Terminal"
claude_profiles = ["~/.claude", "~/.claude-a", "~/.claude-b", "~/.claude-c", "~/.claude-d"]
gh_enabled = true
```

- Hiding a folder from the UI writes `hidden` back to `config.toml`. That is the only config write.
- A missing Claude profile folder is skipped without an error.

### 13.3 Startup, single instance, logs

- **Startup order:** open DB, migrate, start HTTP (Home renders from cache at once), then start watchers, then the background sweep.
- **Single instance:** at start, dial `127.0.0.1:4455`. If something answers `GET /api/health` as drashta, exit 0. If another program holds the port, log and exit 1 (KeepAlive retries; the log shows why).
- **Logs:** `~/.drashta/drashta.log`, `log/slog` text. Rotate at 10 MB, keep 3, done in process because launchd does not rotate. Restart logs go to `~/.drashta/logs/<port>.log`.
- Shutdown flushes the in-memory per-day file sets.

### 13.4 Coexistence with acme

- Different port (4444 and 4455), different label (`in.acme-monitor` and `in.drashta`), different data dir (`~/.acme-monitor` and `~/.drashta`). Both run lsof. The cost is small.
- drashta does not import acme's `history.json` (conflict 16).

### 13.5 Wiping and rebuilding

- `make reindex` or `drashta -reindex` rebuilds the cache and keeps history.
- Deleting `~/.drashta/index.db` is safe. History (past file-change counts from events, server runs) is lost. Past days come back from mtime backfill with `source='mtime'`.

### 13.6 The desktop (D1 to D6)

Setup is one time, on the real Mac (Phase 5).

1. `make install`, then check `curl -s localhost:4455/healthz` returns `ok`.
2. Install Plash (free, Mac App Store, macOS 26 or newer).
3. In Plash add the website `http://localhost:4455/?desktop=1`. No `file://`. Reload interval off. Optional: deactivate on battery.
4. Hotkey for Browsing Mode `⌥⌘D` (Plash's own setting, or a Shortcuts action that opens `plash:toggle-browsing-mode`).
5. Show-desktop move: a hot corner, a trackpad spread, or Cmd+F3. Set "Click wallpaper to reveal desktop" to "Only in Stage Manager".
6. Choose toggle (default) or always-on Browsing Mode.

- Rules drashta follows to stay out of the way: readout mode has no buttons or hover; no link ever navigates the Plash window; no constant animation; resync on `visibilitychange` and on SSE reconnect.
- **Fallback (D6):** open the URL in Safari, File → Add to Dock, add it to Login Items, make it full screen and move it to Desktop 1. A native helper app is built only if both fail.
- Known limits: with Browsing Mode on, desktop icons cannot be clicked. Typing in search needs Browsing Mode. Plash shows one site on one display. To check in week 1 (C3): Plash's own hotkey, and behaviour with desktop icons.
- Installing the service and Plash changes the user's Mac. Agents do not do it without the user's confirmation.

## 14. Performance budgets and failure handling

| Budget | Target |
|---|---|
| Binary start to first HTTP response | under 300 ms (cache present) |
| Home API response (30 projects × 30 days) | under 50 ms, one SQL query per lane group |
| Home first paint in Plash | under 1 s |
| First-ever full index (no DB) | usable in under 10 s (projects, git, fossil, tree); Claude backfill under 60 s in the background |
| Change on disk to page update | under 2 s (FSEvents latency 1 s + debounce 0.75 s) |
| Idle CPU | under 1% average (lsof every 60 s, no other timers) |
| RSS | under 150 MB steady; under 300 MB during Claude backfill |
| `index.db` size | under 200 MB |
| JS bundle | under 400 KB gzip |

Failure handling:

- **Missing tool** (`fossil`, `gh`, `git`): checked with `exec.LookPath` at start. The probe reports `unavailable`. The UI greys that part out with a one-line reason. Nothing else stops.
- **Command timeout or non-zero exit:** the probe keeps the last good cached data and marks it `stale_since`. The error is logged once per project per hour.
- **Huge repos:** git log capped at 200 commits in the index; diffs 400 KB; tree 50,000 entries; file preview 1 MB.
- **Permission errors** (a root-owned dir, macOS TCC): `WalkDir` skips that dir and marks it `denied`. The service needs no Full Disk Access, because everything is under the home folder.
- **Corrupt jsonl lines:** skipped and counted. The session still shows.
- **Corrupt `index.db`:** on open error or a failed `PRAGMA quick_check`, rename it to `index.db.corrupt-<ts>`, start fresh, log it.
- **lsof failure:** keep the last server state.
- **Panics** in a probe are recovered per job and logged with the project ID.
- **Bad `.drashta/` file** (invalid YAML): that one file shows a warning. The others still load.

## 15. Testing strategy

Rules for every test: fixtures are built in `t.TempDir()` or `testdata/` and are invented. No test reads or writes the real `~/projects`, `~/.claude*`, `~/.drashta`, or the network. A missing real tool (`fossil`, `git`) means `t.Skip`.

- **Copied acme tests** run unchanged in their new packages (`parseLsof`, `Merge`, guard tests, restart grace).
- **Probes** with fixtures built in the test:
  - git: a helper creates a repo with fixed `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE`, a worktree and a dirty file.
  - fossil: `fossil new` / `open` / `commit --date-override`, skipped if fossil is missing.
  - claude: `testdata/claude/` with hand-made jsonl: subfolder cwd, worktree cwd, no-cwd file, `acme` vs `site-v2`, cross-project paths, a secret to redact, a truncated last line. Append and truncate tests prove incremental parsing.
  - tree: skip list, caps, denied dir. authored: round trip keeps unknown keys, concurrent edit gives 409. servers: recorded lsof outputs in `testdata/lsof/*.txt`.
- **Golden files:** each probe's `Result` for fixtures is JSON in `testdata/golden/*.json` (`go test -update` rewrites). API responses for home and project pages have golden files too.
- **Indexer tests** use the fake watcher and a fake clock: debounce, per-day counts, midnight rollover, no double count after restart.
- **Store tests:** migrations 0 to latest; `-reindex` keeps history; FTS finds a commit by a mid-word fragment and survives hostile input; Home query under 50 ms.
- **API integration test:** the full server on a random port, with a temp `DRASHTA_HOME` holding fixture projects and profiles. It checks the guard (bad Host, bad Origin, missing token gives 403), an SSE event after touching a fixture file, and that `/api/open` rejects paths outside roots and never runs a command (executor swapped for a recorder).
- **Frontend:** Vitest and React Testing Library for components (a lane from fixture JSON, each viewport body, the confirm-kill flow); MSW mocks `/api`. One Playwright smoke test, headless, against `drashta -dev` with a fixture home. It checks the page with and without `plash-is-browsing-mode`. A test scans the CSS for any `font-weight` other than 400 and for `italic`.
- **Visual:** each screen is rendered headless next to the prototype at 1440×900 and 1000×665 and compared (Wave 3).
- `make test` runs all Go and web tests. Each Go package passes `gofmt -l` (empty), `go vet` and `go test`. Concurrency code runs under `-race`.

## 16. Risks and open technical questions

| # | Risk or question | Handling |
|---|---|---|
| R1 | FSEvents needs cgo, while S7 asks for a pure-Go SQLite driver. | Decided (A1): accept cgo for FSEvents. SQLite stays pure Go. No file-count limit follows. |
| R2 | Does Plash's WKWebView report `visibilityState = hidden` when covered? If not, lsof always runs at 5 s. | Test in week 1 (C1). If it never goes hidden, treat "visible" as "browsing mode on or Safari tab focused". 5 s lsof costs about 2% of one core, which is acceptable. |
| R3 | Does the SSE connection survive sleep, wake and long covered periods? | Built to survive (reconnect and `/api/state?since`). Verify with a 24 h soak test (C2). |
| R4 | File-change counts from events cannot be rebuilt. Deleting `index.db` loses them. | Accept. Past days come back from mtime backfill (`source='mtime'`). Optional later: a separate `history.db`. |
| R5 | The Claude jsonl format is not a public contract. | The parser is tolerant, unknown record types are ignored, and a counter in the log shows how many were seen. Fixtures pin the format. |
| R6 | Build-tool storms inflate `files_changed`. | Skip dirs and common build outputs are excluded before counting (Q14). Debounce limits the work. |
| R7 | Opening a terminal with a command needs AppleScript, which asks once for Automation permission. | Accept. Document it in the install step (C4). |
| R8 | Concurrent edits of `.drashta/*.md` by the UI and by a Claude session. | sha check before write and 409. No other locking in v1. |
| R9 | Task IDs can clash across git branches (Q10). | Accept in v1. Add `drashta renumber` if it happens. |
| R10 | Safari capture through `safaridriver` needs a one-time macOS setting and may be fiddly. | Interface plus a spike. Until it works, a URL source gives 501 and file and upload snapshots work (Q3 detail). |

## 17. Out of scope for v1

- The chart map / portolan home (U7). Kept for later as a folder browser inside a project (the design prototype).
- Pulse and Reviews sections (U3).
- Drag and drop on the board.
- Editing files, committing or pushing from drashta. It observes and opens, and it writes only `.drashta/` and `~/.drashta/`.
- Multi-user assignees, comment threads, notifications.
- Light theme.
- A custom native helper app (D6 last resort).
- Search prefixes other than `:port` (Q9).
- A live frame of a running server inside the viewport (Q8).
- Importing acme's history (conflict 16).

## Appendix A: prototype `app` API to HTTP

The React port keeps an `app`-like client store. Routes: `/`, `/p/:project/:tab?`, `/tracker?filter=review`, `/inbox`, with `?item=<ref>` for the open viewport item. The prototype's `map` and `pulse` routes become `/` (U1). `setWallpaper` is dropped; the Plash class does this job (D3).

| Prototype call | v1 |
|---|---|
| `app.data.now`, `projects[]` | `GET /boot` |
| `project.tree` | `GET /projects/{p}/tree` (lazy per folder) |
| `project.fileContents` | `GET /projects/{p}/file` |
| `project.commits`, `fossilCommits`, `patch` | `GET /projects/{p}/commits`, `/commits/{id}` |
| `project.dirty`, `dirtyDiff` | `GET /projects/{p}/dirty` |
| `project.servers` | `GET /servers?project=p` |
| `project.sessions[].turns` | `GET /projects/{p}/sessions`, `GET /sessions/{id}` (paged) |
| `project.activity30` | `GET /projects/{p}/activity?range=30` |
| `assets[path]` | `GET /projects/{p}/raw?path=` |
| `milestones`, `features`, `tasks`, `snapshots` | `GET /tracker`, `GET /projects/{p}/tasks/{id}`, `/snapshots` |
| `notes` | `GET /notes`, `GET /inbox` |
| `app.resolve(ref)` | `GET /resolve?ref=` (cached per ref, invalidated by SSE) |
| `app.native(ref)` | `POST /open` |
| `app.notesFor`, `app.tasksFor` | the `notes` and `tasks` fields of `/resolve`, or `GET /notes?ref=` |
| `app.eventsOn(projectId, day)` | `GET /day?date=&project=` |
| `app.dayActivity(projectId)` | `GET /projects/{p}/activity` or `GET /home` |
| `app.setTaskStatus` / `addTask` | `PATCH` / `POST /projects/{p}/tasks` |
| `app.linkToTask` | `POST /projects/{p}/tasks/{id}/links` |
| `app.addNote` / `toggleNote` | `POST /notes` / `PATCH /projects/{p}/notes/{id}` |
| `app.setVerdict` | `PUT .../snapshots/{v}/verdict` |
| `app.toggleServer` | `POST /servers/kill` or `/servers/restart` (by `key`, not by port) |
| `app.refresh`, `ui.scanning`, `ui.scannedLabel` | `POST /refresh` plus SSE `scan` |
| `app.setHidden` | `PATCH /settings` |
| `app.toast` | client only |
| search palette (new) | `GET /search` |

- ID migration: `t1` becomes `T-1`, `f1` becomes `F-1`, `m1` becomes `M-1`, `n1` becomes `N-1`. Snapshot `s-*` becomes `<task>/v<version>`.

## Appendix B: facts from the Wave 1 code, for wiring

Source: the Wave 1 reports of discovery, VCS, Claude, servers and actions, the HTTP core, the store and the web shell (read 2026-10-01). Each package's `doc.go` is final. These facts are here so that Wave 2 agents do not trip on them.

**HTTP core (`internal/httpapi`, `internal/live`)**

- Route patterns are full paths that start with `/api/`, for example `Handle("GET", "/api/boot", ...)`. `Handle` panics otherwise.
- POST, PATCH, PUT and DELETE always need the token and the JSON content type. The `mutating` flag adds the token rule to GET routes. Upload routes and a bigger body cap are set per route.
- `Deps` is `{Hub, Logger, Now}`. A struct cannot gain fields from other files, so each handler file declares its own small interfaces and a `RegisterX(s *Server, ...)` function. Main calls them.
- Main creates one `live.Hub`, calls `hub.Start(ctx)` (it flushes merged events), passes it in `httpapi.Options.Hub`, and passes `Options.Counts` and `Options.Static` (`web.Client()`, only when not in `-dev`). It calls `AlreadyRunning(addr)` before `Listen`, and `Shutdown` on SIGTERM. `Listen` accepts only IPv4 loopback.
- The lsof loop reads `hub.AnyVisible()` and waits on `hub.Changed()` for the 5 s or 60 s cadence. After a watcher reports `MustScanSubDirs`, the indexer publishes `live.ResyncEvent()`.
- The token value is the hex of the seed bytes (64 characters for 32 bytes).
- SPA fallback: an unknown GET without a file extension returns `index.html`. A missing file with an extension, or anything under `/assets/`, is a plain 404 (unless the request accepts `text/html`, which keeps project routes with dots such as `/p/vendor-v1` working). Dot files are never served. If `index.html` is missing, `/` answers 503 with a hint to run `make web`.
- Suggested error mapping for handlers: `pathguard.ErrOutside` and not-exist give 404; `tree.ErrSecret` gives 403; `tree.ErrIsDir`, `ErrNotDir`, `ErrNotRegular` give 400; git `ErrBadRef` and `ErrBadPath` give 400; `ErrNotFound` gives 404; `ErrNotRepo` and fossil `ErrNotCheckout` give 404 or an empty list. Other errors are timeouts or tool failures: keep the cached data and mark it `stale_since`. A secret file read is not an error: check `Redacted`.

**Probes**

- Servers: after every successful `servers.Scan`, call `hist.Merge`, then `hist.Save` if it changed, then `manager.SetLive`. After a failed scan, do none of these. The store adapter turns `ListServerRuns` and `ReplaceServerRuns` into a `servers.HistoryStore`.
- Claude: store one `claude_file` row per file path (subagents included) from `FamilyResult.States`. `session_projects` gets reason `cwd` for the project, and `mention` for each key of `Mentions` (edited files in another project also arrive as mentions). The `/sessions/{id}` cursor is `Page.Next` as a decimal string. Run `claude.CheckPath` on any path that comes from a request.
- Docs: `docs.Extract` re-reads every doc on each call (a project with 1,200 docs took about 180 ms), so the indexer calls it only when a doc file changed.
- Seismograph: use `probe.DayCounts(commits, time.Local)` over the git commits plus the fossil commits of a project.
- The secret-file list is `.env*`, `*.pem`, `*.key`, `id_*`, `*.p12`. Later additions to consider: `.npmrc`, `.netrc`, `*.pfx`, `*.jks`, `credentials*`. One function (`tree.IsSecret`) holds the list.
- `go.mod` marks several modules `// indirect`. `go mod tidy` clears this at the end. No agent runs it.

**Store (`internal/store`)**

- Every write helper has a method on `*DB` (its own transaction) and a `...Tx(ctx, tx, ...)` twin, so the indexer can put a whole job in one commit.
- The store does not recompute daily activity. After commits, sessions, tracker changes or server-run changes, the indexer calls `RecomputeDaily(ctx, project, day, loc)` for each touched day. `AddFileChanges` takes the day as a `YYYY-MM-DD` string.
- `ReplaceCommits` upserts the given commits and deletes stored commits at least as new as the oldest given one that are gone from the log (rebase, amend). Older stored commits stay, so 30-day counts survive the 200-commit cap. It does not recompute `daily_activity`.
- `Reindex` reads its CREATE statements from `sqlite_master` and drops the cache tables, including `events`. The `server_up`, `server_down` and `file_burst` event rows are lost until rebuilt (`RebuildHistoryEvents` is a planned helper). `claude_file` is a cache table on purpose: after a reindex every jsonl is parsed again.
- Event rows are unique on `(project, kind, ref, ts)`, so `AddEvent` is safe to repeat. Give each event a stable `ref` (for example `commit:<id>`) and the real event time. There is no `DeleteEvents` helper yet.
- Search: words are split on spaces, quotes and control characters are dropped, words under 3 characters are dropped, the rest are quoted and joined with AND (8 words and 200 characters at most). No word left gives an empty list. Ranking: `bm25(search, 4.0, 1.0)` (title weight 4), then newest first.
- `Home` runs four queries in one read transaction (projects, `daily_activity`, `servers_now`, sessions). Lanes include hidden projects (`Lane.Hidden`). A live server is `kind='dev'`, by port. A session is live when its `.jsonl` was modified in the last 5 minutes.
- `daily_activity`: `files` counts distinct paths in `file_changes`; `sessions` counts sessions whose start-to-end range touches the day; `session_minutes` is the overlap (an estimate); an all-zero day deletes its row.
- A manual and an auto task link with the same ref: the manual one wins. `UpsertAuthoredTaskLinks` replaces only the rows of one source.
- Delete helpers exist so the cache can follow the disk: `DeleteProject`, `DeleteSession`, `DeleteClaudeFile`, `DeleteAuthored`, `DeleteSetting`. `gh_cache` has a table and no helper.

**Frontend shell (`web/`)**

- Readout mode is `html.desktop` without `plash-is-browsing-mode`. In readout mode the shell shows Home whatever the URL is, and renders no rail, search, refresh, drawer or toasts.
- The project picker is its own route, `/p`. The rail's Project item goes to the last viewed project, or to `/p`.
- The shell reads these optional fields: `boot.lastScanAt`, `boot.projects[].dirtyCount`, `resolve.light`, `resolve.nativeActions[0].command` (the Open tooltip). A 404 from `/api/resolve` shows "Nothing to show here." A `project` search hit carries `projectId`; a `note` hit's `ref` is the item the note is on.
- The scan label uses UTC `HH:MM`, like the prototype. Only one toast shows at a time.
- The CSRF 403 reload happens once and only for mutating calls. The event stream reopens with `?lastEventId=<id>` after a closed stream.

**Authored data and CLI (`internal/authored`, `internal/cli`)**

- `rev` is the sha256 hex of the file bytes (conflict 26).
- Unknown front-matter keys are kept by patching the file on disk at save time. `Save` re-reads the file under the lock, so even an item built from JSON keeps unknown keys. Only the changed keys are rewritten. If the front matter cannot be patched safely, the whole YAML tree is rewritten.
- `NextID` reserves an ID by creating an empty file with `O_EXCL`. `LoadProject` skips empty files silently. The ID and snapshot-version scan includes the trash, so nothing is reused.
- `Save`, `Update*`, `Delete` and `AddSnapshotImage` take an `flock` on `.drashta/.lock`. Reads do not. For appends (links, log lines, resolve) use `Update*`: it locks, loads, changes and saves, so it cannot lose a concurrent edit. Do not call another authored function inside an `Update*` callback (same lock). For `PATCH` with `If-Match`, use `Load*`, change fields, then `Save(root, &item, rev)`.
- Trash layout: `.drashta/.trash/<kind folder>/<file>`, snapshots as `.trash/snapshots/T-1/v3.md` with their images.
- Times are strings in the structs (the exact text of the file). `ParseTime` reads them. A task body is the text before the `## Log` line. A bare `## Log` line in a body is refused (`ErrInvalid`).
- `Save` validates values and fills defaults (task status `todo`, verdict `pending`, milestone state `open`). `LoadProject` is lenient: a bad value gives a warning and the item still loads.
- Refs: the project segment allows `[A-Za-z0-9._/-]`; commit IDs are 6 to 64 hex characters; a session ID is a full UUID or its first 8 characters; `project:` and `server-key:` types are accepted; `image:` canonicalises to `file:`; line fragments only on `file`, points on `file`, `image` and `snapshot`.
- Suggested error mapping for handlers: `ErrConflict` 409 (reload with `Load*` and return the current item), `ErrExists` 409, `ErrNotFound` 404, `ErrInvalid` 422, `ErrTooLarge` 413, `ErrBadExt` 415.
- The indexer routes `.drashta/**` events with `authored.Classify(root, eventPath)`, ignores `.tmp-*`, `.lock` and `.trash`, and reloads that one file.
- The status moves after a snapshot or verdict, the filing of `snapshots/inbox/*` drops, and the note `anchor` for API notes are **not** in this package (plan gap G2). The CLI fills `anchor` itself for `file:...#L` refs.
- The CLI finds the project with `-C`, else the nearest folder above the working directory that holds `.drashta` (the search stops at the home folder), else `<ProjectsDir>/<group>/<name>` that contains the working directory. The first write creates `.drashta` with `README.md`, `.gitignore` and `.lock`. `main` calls `cli.Run(os.Args[1:], os.Stdout, os.Stderr, cli.DefaultEnv())` when the first argument is `task`, `note`, `snap` or `help`.
