# drashta: domain glossary

This file defines the words used in the drashta docs, code and UI.
When a word here and a word in another doc differ, `docs/decisions.md` wins, then `docs/superpowers/specs/2026-10-01-drashta-design.md`.
Each entry is one sentence. IDs such as S7 or U1 point to `docs/decisions.md`.

## The app

- **drashta**: A local command center for every project in `~/projects`, and the opening screen of the Mac.
- **observe**: To read facts from disk and tools (commits, files, servers, sessions) without writing them.
- **track**: To keep authored work items (tasks, snapshots, notes) in `.drashta/` folders.
- **preview in, open out (U9)**: A click previews an item inside drashta, and "Open ↗" runs the real app.
- **Open ↗**: The button that asks the server to run a native command for the item in view.
- **native action**: A command the server runs for you, such as opening an editor, Finder, Terminal, Preview or Safari.
- **ref**: A text pointer to one item, written `[<project>:]<type>:<id>[#<fragment>]`, for example `file:main.go#L30`.
- **item**: Any one thing that has a ref and can be previewed, opened and noted.
- **fragment**: The part of a ref after `#`, such as a line range or an image point.

## Projects and what drashta observes

- **project**: One folder `~/projects/<group>/<name>`.
- **group**: The parent folder of a project: `personal`, `work` or `tools`.
- **project ID**: The name of a project in URLs, refs and the API: the folder name, or `<group>.<name>` when two groups share a folder name.
- **hidden project**: A folder the user hid from the UI; it is still discovered and can be shown again.
- **skip dir**: A folder the tree walk never enters, such as `node_modules`, `.git`, `dist`, `target` or `venv`.
- **collapsed node**: A skip dir that shows in the tree with no children.
- **VCS**: Version control, which is git or fossil here.
- **commit**: One git commit or fossil check-in.
- **dirty file**: A path with uncommitted changes; the lane shows their count as `±n`.
- **server**: A process that listens on a localhost TCP port, found with `lsof`.
- **live server**: A server that is listening now.
- **server key**: The stable name of a server, made from its working folder and command (`cwd|cmd`), because ports get reused.
- **Claude session**: One Claude Code run, stored as one `.jsonl` file.
- **profile**: One Claude config folder (`~/.claude` or `~/.claude-<name>`).
- **roll-up**: The rule that a session counts toward the project whose path is the longest prefix of the session's `cwd`.
- **mention**: A session that touched files of a project other than its own, shown in that project's Sessions tab.
- **doc**: A text file shown as a document, such as `*.md`, `*.txt`, `README*`, `CLAUDE.md` or `AGENTS.md`.
- **GitHub block**: The part of a project's Overview that shows pull requests, issues and CI from `gh` (A6).
- **unavailable**: The state of a probe whose tool is missing or not logged in; the UI greys it out with a reason.

## Home: the seismograph

- **seismograph (U1)**: The home screen: one lane per project across the last 7 or 30 days.
- **lane**: The row of one project on the seismograph.
- **tremor**: One recorded event on a lane, such as file changes, a commit or a session.
- **day column**: The vertical slot of one day in a lane; it is a button.
- **file bar**: The grey bar in a day column; its height is the number of file changes that day (log scale).
- **commit bar**: The amber bar in a day column; its height is the lines changed by commits that day (log scale).
- **session tick (A5)**: The small violet mark at the top of a day column when a Claude session ran that day.
- **shared scale**: The rule that every lane on Home uses the same maximum, so lanes can be compared.
- **day readout**: The panel under the lanes that lists everything that happened on the selected day.
- **range**: The number of days shown, 7 or 30.
- **order**: How lanes are sorted: `recency` (last touched) or `loudest` (most activity in range).
- **flat lane**: A lane with no activity in the range.
- **quieter projects**: The last row in readout mode (`+N quieter projects`) for lanes that do not fit.
- **scan label**: The text that tells when drashta last scanned (`last scan 14:53`, `updated 4m ago`).
- **live counters**: The four top-bar counts: live servers, open tasks, snapshots awaiting review, open notes.

## Plash and desktop modes

- **Plash**: The Mac app that shows a web page as the desktop wallpaper.
- **readout mode**: The calm, view-only look of the page when it runs as the wallpaper (`?desktop=1` and no `plash-is-browsing-mode` class).
- **browsing mode**: The full command center, used in a Safari tab or when Plash Browsing Mode is on.
- **desktop marker**: The `?desktop=1` part of the Plash URL that tells the page it runs as the wallpaper (Q7).
- **interact hint**: The small `⌥⌘D to interact` text shown in readout mode.
- **light vs full detail**: A prototype-only split where most projects had a summary and three had full data; the real app loads full detail for every project.

## Screens

- **Project page**: The page for one project, with a header, native actions, a project seismograph and tabs.
- **cartouche**: A box with a double hairline border, used for the project header and the Inbox "Needs you" block (U6).
- **tab**: One section of the project page: Overview, Files, Commits, Sessions, Servers, Docs or Tracker.
- **Tracker**: The section that shows milestones, features and tasks as a board, milestone list or timeline.
- **board**: The Tracker view with five status columns.
- **Awaiting review**: The Tracker filter that shows tasks whose latest snapshot is pending (U3).
- **Inbox**: The section that collects open notes, things that need you, and the activity feed.
- **viewport**: The right-hand panel that previews one item.
- **rail**: The left strip with Project, Tracker and Inbox.
- **toast**: The short message at the bottom that confirms an action.
- **signal colour**: A colour tied to a kind of thing: amber commits, teal live, violet Claude, ivory snapshots, coral changes and notes (U8).

## Tracking (authored data)

- **authored data (S9)**: Data the user or Claude writes, kept in the project's `.drashta/` folder.
- **`.drashta/`**: The folder in each project that holds milestones, features, tasks, snapshots and notes as markdown files.
- **front matter**: The YAML block at the top of a `.drashta/` file that holds its fields.
- **task**: One unit of work with a status of backlog, todo, in_progress, review or done.
- **feature**: A named part of a project that groups tasks.
- **milestone**: A dated target that holds one or more features.
- **status**: The stage of a task; it is one of five values.
- **snapshot**: One numbered version of a task's visible result, an image plus a verdict.
- **version**: The number of a snapshot within its task (`T-12/v3`).
- **verdict**: The review result of a snapshot: `pending`, `approved` or `changes`.
- **suggestion chip (A4)**: The one-click offer to move a task to done after its latest snapshot is approved; it never moves the task by itself.
- **note**: A comment on any item, open or resolved.
- **pin**: A note fixed to a line of a file or to a point `x,y` on an image.
- **anchor**: The saved text of a pinned line, used to find the line again after the file changes.
- **manual link**: A link from a task to an item that a person added; it is stored in the task file.
- **auto link**: A link from a task to a commit or session that drashta derived; it lives only in SQLite.
- **unlink**: A rejected auto link, stored in the task so it stays hidden.
- **proposal**: A suggested change, such as closing a task after a commit says `Fixes T-12`; the user confirms it.
- **drop folder**: `.drashta/snapshots/inbox/`, where a dropped image becomes a snapshot.
- **unfiled snapshot**: An image in the drop folder with no task name; the Inbox asks which task it belongs to.
- **trash**: `.drashta/.trash/`, where deleted items move.
- **rev**: The hash of an authored file's bytes, sent as `If-Match` so edits can be checked.
- **409**: The error the server returns when a file changed on disk since the client read it.

## Engine

- **probe**: A package that reads one kind of source (projects, tree, git, fossil, gh, servers, claude, docs, authored).
- **indexer**: The part that watches for changes, runs probes and writes results to the store.
- **watcher**: The part that reports file events; it uses macOS FSEvents (A1).
- **FSEvents**: The macOS system that reports file changes for a whole folder tree without one open file per file.
- **debounce**: Waiting a short time (750 ms) after the last event before running a job.
- **sweep**: A full scan of all projects, at startup and every hour.
- **store**: The SQLite database `~/.drashta/index.db` (S7).
- **cache table**: A store table that can be rebuilt from disk and tools.
- **history table**: A store table that cannot be rebuilt, such as file-change counts from events and server runs.
- **reindex**: Dropping the cache tables and scanning everything again while keeping history.
- **FTS**: The full-text search table, which uses SQLite FTS5 with the trigram tokenizer.
- **daily activity**: The per-project, per-day counts that feed the seismograph.
- **backfill**: Filling past days from file modified times; those rows are marked `source='mtime'`.
- **stale**: Cached data kept after its probe failed.
- **hub**: The part that keeps the event sequence and the replay buffer for SSE.
- **SSE**: Server-Sent Events, the one-way stream from server to page.
- **event**: One message on the SSE stream, such as `project`, `activity`, `servers`, `tracker`, `scan`, `counts`, `hello` or `resync`.
- **ring buffer**: The last 1,000 events the hub keeps so a reconnecting page can catch up.
- **resync**: The signal that tells the page to refetch everything it shows.
- **presence**: The page telling the server whether it is visible, which sets how often lsof runs.
- **guard**: The check on every `/api` call for Host, Origin, content type and token.
- **token**: The random value injected into `index.html` that mutating calls must send back as `X-Drashta-Token`.
- **runx**: The helper that runs external commands with no shell, a timeout and an output cap.
- **pathguard**: The helper that proves a path is inside a project root before any read or action.
- **Plan (actions)**: The pure function that turns a resolved item and an action into a command.

## Build process

- **frozen prototype**: The original design mockup (Prototype v3), kept as the visual contract for the UI.
- **green light**: The user's approval that lets work move to the next phase.
- **wave**: A group of build agents that run in parallel on separate files.
- **brief**: The instruction file one build agent reads, in `.build/briefs/`.
- **report**: The file one build agent writes when it finishes, in `.build/reports/`.
- **ADR**: An architecture decision record in `docs/adr/`.
