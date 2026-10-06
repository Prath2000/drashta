# Architecture: package map

Data flows one way: **disk and tools → probes → indexer → SQLite store → HTTP API → React app**. Detail (a diff, a file body, a transcript) skips the store and is read live by a probe. The store is a cache plus a history log, never the source of truth (ADR 0003).

```
~/projects (FSEvents)  git / fossil / gh / lsof  ~/.claude*/projects/**/*.jsonl  <project>/.drashta/*.md
            \                    |                        /                              /
             `-------- internal/probe/*  (typed Scan / Detail functions) --------------'
                                   |
                           internal/indexer  (watch, debounce, queue, sweeps, activity)
                                   |
                           internal/store  (SQLite ~/.drashta/index.db, WAL, FTS5)
                                   |
   internal/httpapi (guard, JSON, SSE)  +  internal/live (event hub)  +  internal/resolve / actions
                                   |
                          web/  (React Router v7 SPA, embedded in the binary)
```

Every package has a `doc.go` that lists its exported API. Start there.

## Go (`cmd/`, `internal/`)

| Package | Owns |
|---|---|
| `cmd/drashta` | The binary: flags `-port -dev -reindex -version`, subcommands `reindex`, `task|note|snap` | `b-wiring` |
| `internal/app` | Wiring: config, DB, hub, indexer, API, logging with rotation, single-instance check, adapters |
| `internal/config` | `~/.drashta/config.toml`, data dir (`DRASHTA_HOME`), hide list, editor validation, one lock |
| `internal/runx` | Run an external command: argv only, 10 s timeout, 8 MB output cap, `LC_ALL=C` |
| `internal/pathguard` | Clean + `EvalSymlinks` + must be inside a root. Used by every file read and every action |
| `internal/probe` | Shared types: `Project`, `Commit`, `CommitDetail`, `DirtyFile`, `DayCount` |
| `internal/probe/projects` | Find projects (`~/projects/<group>/<name>`), ids, stack, description |
| `internal/probe/tree` | File walk (skip list, caps), lazy `ListDir`, live `ReadFile` (secret files redacted), `RawPath` |
| `internal/probe/docs` | Markdown/text docs list with titles and heads for search |
| `internal/probe/git`, `fossil` | Commits, numstat, dirty files, diffs, per-day stats (`DayStats`), cache keys. Shell out through `runx` |
| `internal/probe/gh` | PRs, issues, CI via the `gh` CLI. Never touches a token. Cache with TTL |
| `internal/probe/servers` | `lsof` scan, project mapping by `cwd`, server history |
| `internal/probe/claude` | Claude transcripts: discover, incremental parse (`ParseFamily`), roll-up to projects, mentions, redaction, live paged transcript |
| `internal/authored` | `.drashta/` read/write: front matter, `rev` (sha256) + 409, ids, trash, snapshot images, refs |
| `internal/cli` | `drashta task|note|snap ...` (talks to `authored` directly) | `e-authored-cli` |
| `internal/autolink` | Pure rules: which commits/sessions belong to which task |
| `internal/store` | SQLite: migrations (`001_init`, `002_daily_commits`), single writer, FTS5 trigram search, typed queries (`q_*.go`), reindex, history safety |
| `internal/indexer` | Scheduler (4 workers, one job per project, debounce 750 ms), routing, sweeps, FSEvents watcher, per-day activity, auto-link, SSE events |
| `internal/live` | In-process event hub: ring of 1000 events, ids, resync, coalescing, presence |
| `internal/httpapi` | Guard (Host/Origin/CSRF/Sec-Fetch-Site), JSON helpers, SSE, SPA serving, and the API handlers `api_*.go` |
| `internal/resolve` | Ref → title, preview, native-action target (`/api/resolve`, `/api/open`) |
| `internal/actions` | Native "open out" commands (editor, Finder, Terminal, Safari), kill, restart. Pure `Plan`, injectable executor. Also `ProbePage` / `PageInfo`: can a live server's page be framed (an enforced CSP `frame-ancestors` beats `X-Frame-Options`) |
| `internal/drawing` | DWG preview reader: runs the external `chitralipi` (argv via `runx`, 60 s, model space only), caches the entity JSONL and the layer JSON by file+program size/mtime (200 files max, both kinds together), one run per key and kind; a run finishes even if the caller leaves. Behind `/api/projects/{p}/dwg/layers` and `/dwg/entities` |
| `web` (Go) | `//go:embed` of `web/build/client` |

Tests live beside the code. `testdata/` holds fixture repos, jsonl and golden files. No test touches the real `~/projects`, `~/.claude*` or `~/.drashta`.

## HTTP API (all under `/api`, JSON; guard on every route)

| Group | Files | Endpoints |
|---|---|
| Core | `server.go`, `spa.go`, `guard.go` | `health`, `session`, `events` (SSE), `state`, `presence`, `/healthz`, `/blank` (empty transparent page), the SPA |
| Home | `api_home.go`, `api_inbox.go`, `api_search.go`, `api_settings.go`, `api_refresh.go` | `boot`, `home`, `day`, `inbox`, `search`, `settings`, `refresh` |
| Project | `api_project.go`, `api_files.go`, `api_commits.go`, `api_sessions.go`, `api_servers.go`, `api_github.go` | `projects/{p}` and `/activity /tree /file /raw /commits /dirty /sessions /docs /github`, `sessions/{id}`, `servers`, `servers/embed` (can the page be framed), `servers/kill`, `servers/restart` |
| Tracker | `api_tracker.go`, `api_snapshots.go`, `api_notes.go`, `api_resolve.go`, `api_open.go`, `api_desktop.go` | `tracker`, tasks/features/milestones CRUD, links, snapshots + verdict, `notes`, `resolve`, `open`, `desktop` (desktop mode buttons) |

The shapes are in [spec/04-data-model.md](spec/04-data-model.md) section 6, with the deviations listed in section 5 of the [design spec](superpowers/specs/2026-10-01-drashta-design.md).

## Web (`web/app/`)

| Folder | Owns |
|---|---|
| `routes/` | `/` Home, `/p` picker, `/p/:project/:tab?`, `/tracker`, `/inbox`, not-found. `?item=<ref>` opens the viewport on any route |
| `lib/` | `api.ts` (typed client, CSRF token, retry), `types.ts` (API types), `events.ts` (SSE, visibility catch-up, resync), `query.ts` (tiny cache + `invalidate`), `store.ts` (mode readout/browsing, range, search, toasts), `shortcuts.ts` (the one global key listener; Esc layers in order: `app`, `search`, `day`, `viewport`), `url.ts`, `refs.ts`, `format.ts`, `boot.ts` |
| `components/shell/` | Top bar, rail, counters, search palette, viewport drawer frame, toasts, scan label |
| `components/home/` | Seismograph lanes, date axis, day readout |
| `components/project/` | Header, seismograph, tabs (Overview, Files, Commits, Sessions, Servers, Docs, Tracker) |
| `components/tracker/` | Board, milestones, task body (editable, also used by the viewport), review flow |
| `components/inbox/` | Notes, unfiled snapshots, proposals, activity feed |
| `components/viewport/` | Bodies by ref type: file/doc, commit, image, snapshot, session, server, generic (`registry.ts`). `ServerBody.tsx` runs the live app in a sandboxed iframe (Live/Capture, reload, Expand), or the last capture |
| `mocks/` | MSW handlers and fixtures for tests and `npm run dev:mock` |
| `styles/app.css` | Design tokens (U8) and shell classes. Font weight 400 only, no italics (a test scans every CSS file) |

Layout rule: a route or drawer container is `display: grid; grid-template: minmax(0,1fr)/minmax(0,1fr)` so its child fills it.
