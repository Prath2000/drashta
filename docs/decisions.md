# drashta: decisions log

drashta (द्रष्टा, "the one who sees") is a local command center for every project in `~/projects/<group>/<name>`.
It **observes**: commits, files, localhost servers, Claude Code sessions and activity.
It also **tracks**: features, milestones, tasks, snapshots under review, and notes on any item. The tracking side follows the style of ShotGrid and Autodesk Construction Cloud.
The main rule: nothing is pushed at you, and any detail is a few clicks away.

All items below are confirmed by the user. The newest decisions are at the top.

## UI

| # | Decision | Date |
|---|---|---|
| U11 | **The app name is shown in Devanagari, द्रष्टा**, in the top bar, the window title and the error page. Only the visible name changes: the binary, service, URLs, CLI and code stay `drashta`. Exception to U5: the name uses a macOS Devanagari font (Kohinoor Devanagari, then Devanagari Sangam MN), because JetBrains Mono has no Devanagari glyphs. | 2026-10-01 |
| U10 | **The server preview runs the live app.** Every server that answers HTTP is embedded live in the viewport in a sandboxed frame (no top navigation). An Expand button fills the drashta window with it, and Esc shrinks it back. If the app refuses framing (`X-Frame-Options`, CSP `frame-ancestors`), the preview shows the last capture and the reason. In the Plash readout (no Browsing Mode) it always shows the capture. This replaces spec Q8 and the "never loads another app's page" rule in `02` section 1. | 2026-10-01 |
| U1 | **The seismograph is the home screen.** One lane per project across the last 30 days: file-change bars, commit bars and live-server dots. It was chosen over the chart map. | 2026-10-01 |
| U2 | **No separate home or map button.** The app name (द्रष्टा, U11) at the top left is the link to home. | 2026-10-01 |
| U3 | **No Pulse section and no Reviews section.** A snapshot is one step in a task's life. You review it inside the Tracker, where "Awaiting review" is a filter. | 2026-10-01 |
| U4 | Sections: **Home (the app name) · Project · Tracker · Inbox**, plus the preview viewport on the right. | 2026-10-01 |
| U5 | **Font: `JetBrainsMono Nerd Font Mono`, regular (400) only. No italics and no bold anywhere.** Hierarchy comes from size, colour, letter-spacing and case. | 2026-10-01 |
| U6 | Some map-style detail is welcome in labels and headers: spaced uppercase and cartouche boxes with a double hairline border. The instrument look stays the base. | 2026-10-01 |
| U7 | The **chart map is kept for later**, for example as a folder browser inside a project. It is not in v1. Source: the design prototype. | 2026-10-01 |
| U8 | Visual system: dark instrument panel. Ground `#0b0b0a`, panel `#121210`, hairline `#2c2b27`, text `#e8e4da`, muted `#a39e92`. Signals: commits amber `#ffb347`, live/servers teal `#6fd6c4`, Claude violet `#b69cff`, snapshots ivory `#f3efe4`, changes-requested coral `#ff8a66`. | 2026-09-30 |
| U9 | **Click behaviour: "preview in, open out".** Every item previews in the right-hand viewport. "Open ↗" runs the native command: editor, Finder, Terminal, Preview, `claude --resume`, or Safari. | 2026-09-30 |

## Desktop: drashta as the opening screen of the Mac

| # | Decision | Date |
|---|---|---|
| D1 | The **full, working** home screen is the Mac desktop wallpaper, shown through **Plash**. It is not a calm "view only" picture. | 2026-10-01 |
| D2 | You interact through Plash Browsing Mode on a hotkey (`plash:toggle-browsing-mode`), or leave it on all the time. | 2026-10-01 |
| D3 | The page is **Plash-aware**. Without the `plash-is-browsing-mode` class it is a clean readout with a small "⌥⌘D to interact" hint. With the class it is the full command center. | 2026-10-01 |
| D4 | "Open ↗" and links never navigate inside Plash. The drashta server runs every native command, including `open -a Safari <url>`. | 2026-10-01 |
| D5 | The page updates itself: the server pushes changes, and the page resyncs when it becomes visible again. It has no reload timer and no constant animation. | 2026-10-01 |
| D6 | Fallback if Plash does not work well: a Safari web app (Add to Dock) in its own full-screen Space. The page is the same. A custom native helper app only if both fail. | 2026-10-01 |

## System

| # | Decision | Date |
|---|---|---|
| S1 | A local web app. **Go backend and React Router frontend**, shipped as one binary, listening on 127.0.0.1 only. | 2026-09-30 |
| S2 | **Port 4455.** It starts at login through a launchd plist, like acme. | 2026-10-01 |
| S3 | **acme is copied into drashta; acme itself stays.** acme is the lsof-based localhost server discovery with kill/restart and history, at `~/projects/personal/acme`. | 2026-09-30 |
| S4 | **Discovery by folder convention.** Every `~/projects/<group>/<name>` is a project. Groups: personal, work, tools. You can hide folders from the UI (for example `venv`, `vendor-v1`). | 2026-09-30 |
| S5 | **v1 scope is everything:** history and files, running servers and their actions, docs, Claude sessions, GitHub through `gh`, activity, and the tracker. | 2026-09-30 |
| S6 | Claude sessions come from 5 profiles: `~/.claude`, `~/.claude-a`, `~/.claude-b`, `~/.claude-c`, `~/.claude-d`. Each has `projects/<encoded-cwd>/*.jsonl`. A session started in a subfolder counts toward its parent project. A session can also be about a project other than its cwd. | 2026-09-30 |
| S7 | **Data model B:** a SQLite summary index for home, activity and search, plus live fetch on detail pages. SQLite is a **cache and history log, never the source of truth**. It is one file, `~/.drashta/index.db`, pure-Go driver, and safe to delete. | 2026-09-30, reconfirmed 2026-10-01 |
| S8 | **The indexer runs on change, not on a timer.** It uses fsnotify on project roots and VCS metadata, plus an hourly sweep. lsof runs every 5 s while a tab is open and every 60 s otherwise. The tree walk skips node_modules, .git, dist, target and venv. "Refresh all" and per-project refresh buttons are available. | 2026-09-30 |
| S9 | **Authored data lives per project in `.drashta/`** as markdown with YAML front matter, indexed into SQLite. It can be versioned, and Claude sessions can read and edit it. | 2026-09-30 |

## Answers to the open questions (2026-10-01)

| # | Decision |
|---|---|
| A1 | cgo is accepted for FSEvents file watching; SQLite stays pure Go. |
| A2 | Commit `.drashta/` text to each project's git; snapshot images are git-ignored by default. |
| A3 | URL snapshots use **Safari** (`safaridriver`), not Chrome. The details are decided later. |
| A4 | Approving the latest snapshot only **suggests** moving the task to done. |
| A5 | Claude sessions put a small violet tick on the seismograph lane. |
| A6 | GitHub (`gh`) data is a block in the project Overview, not its own tab. |

## Process

- The order is: discuss, brainstorm, build an artifact, **freeze it**, then implement. Nothing replaces the real app until the user gives a **green light**.
- Write explanations in plain, simple English (ASD-STE100 style) with bullet points.
