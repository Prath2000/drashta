# drashta v1: data model and API

Status: draft for review. `00-decisions.md` wins where this file differs.
Sources: the design prototype, `CHANGES-v2.md`, `tracker.json`, `data.json`, acme `discover.go` and `history.go`.

Main rules:

- **Observed** data comes from disk and tools (git, fossil, lsof, ps, Claude `.jsonl`, `gh`). drashta never writes it.
- **Authored** data lives per project in `<project>/.drashta/` as markdown with YAML front matter (S9). Those files are the source of truth.
- SQLite (`~/.drashta/index.db`) is a cache plus a history log (S7). You can delete it. Only the history tables are lost.

---

## 1. Glossary

| Entity | Kind | What it is | Ref (how you point at it) |
|---|---|---|---|
| **Project** | observed | One folder `~/projects/<group>/<name>` (S4). ID = `<name>`. On a name clash across groups the ID becomes `<group>.<name>`. | `project:acme` |
| **Group** | observed | The parent folder: `personal`, `work`, `tools`. | (none) |
| **Item / Ref** | (none) | A pointer to one thing that can be previewed and opened. Syntax is in section 2.4. | (none) |
| file | observed | A file in the project tree. Can take a line range. | `file:discover.go#L30` |
| image | observed | A file with an image extension. It is `file:` with a different viewer. `image:` is accepted as an alias. | `image:f0_gate.png` |
| doc | observed | A file shown as a document: `*.md`, `*.txt`, `README*`, `docs/**`, `plan/**`, `notes/**`. It is `file:` too. | `file:plan/roadmap.md` |
| commit | observed | A git or fossil check-in. The ID is the short hash, 7 characters or longer. | `commit:bdcb99c` |
| session | observed | One Claude Code session: one `.jsonl` in one of the 5 profiles (S6). The ID is the UUID. | `session:121c3b19-ada3-...` |
| server | observed | A listening localhost process (from acme). Live ref = port. Stable key = `cwd\|cmd` (acme `serverKey`). | `server:3000` |
| **Activity event** | observed + authored | One dated tremor: a file change, commit, session start or end, server up or down, snapshot, note, or task change. It feeds the seismograph and the Inbox feed. | (none) |
| **Feature** | authored | A named part of a project that groups tasks. | `feature:F-3` |
| **Milestone** | authored | A dated target that holds one or more features. | `milestone:M-1` |
| **Task** | authored | One unit of work with a status: `backlog, todo, in_progress, review, done`. | `task:T-12` |
| **Snapshot** | authored | One numbered version of a task's visible deliverable: an image plus a verdict. | `snapshot:T-12/v3` |
| **Note** | authored | A comment on any Ref. It can pin to a line (`#L30`) or to a point `x,y` (0..1) on an image or snapshot. It can be open or resolved. | `note:N-40` |

---

## 2. `.drashta/` on disk

### 2.1 Layout

```
<project>/.drashta/
  README.md                 # one paragraph that tells humans and Claude the rules below
  milestones/M-1.md
  features/F-3.md
  tasks/T-12.md
  snapshots/T-12/v3.png     # image (png|jpg|webp)
  snapshots/T-12/v3.md      # metadata + verdict
  snapshots/inbox/          # drop folder (see section 4)
  notes/N-40.md             # one file per note
  .lock                     # empty; flock target for the CLI and server
```

- One entity per file. Two writers therefore rarely touch the same file.
- The indexer watches `.drashta/**` with fsnotify. Any change updates SQLite within about 1 s and sends an SSE event.
- Unknown front-matter keys are kept on rewrite. Keys keep their order.

### 2.2 IDs

- Format: prefix + integer, per project: `M-1`, `F-3`, `T-12`, `N-40`. IDs are short, easy to type, and easy to grep in commit messages.
- Snapshot IDs are scoped to the task: `T-12/v3`. They need no global counter.
- **Allocation is collision-safe through exclusive create.** The writer computes `max(existing)+1` and creates the file with `O_CREAT|O_EXCL`. If the file already exists, the writer tries `+1` again. Two Claude sessions can never get the same ID on one machine.
  - The drashta server and the `drashta` CLI do this for you.
  - A Claude session without the CLI can do the same thing in shell: `set -o noclobber; echo ... > tasks/T-13.md`. If that fails, try T-14.
- An ID is never reused, including after a delete. Deleted entities are moved to `.drashta/.trash/`.
- The file name is the ID. The `id:` key in front matter must match it. If they differ, the indexer uses the file name and shows a warning.

### 2.3 Front matter per entity

**Milestone** `milestones/M-1.md`

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

**Feature** `features/F-1.md`

```markdown
---
id: F-1
title: Relic chambers
milestone: M-1         # optional; mirrors milestone.features (milestone file wins on conflict)
created: 2026-09-29T13:00:00+05:30
---
Each relic opens into its own chamber: philosophy, diary, voyage.
```

**Task** `tasks/T-1.md`

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
links:                 # manual links only; auto links live in SQLite (section 3)
  - commit:b7dc920bef
  - file:notes/HANDOFF.md#L3
  - server:3000
unlinks: []            # auto links the user rejected
---
The dome should feel like standing under the night sky. The compass rose is the floor.

## Log
- 2026-09-30T12:02+05:30 session:121c3b19 posted snapshot v3
- 2026-09-30T13:05+05:30 alex status review -> in_progress
```

**Snapshot** `snapshots/T-1/v3.md` (next to `v3.png`)

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

**Note** `notes/N-40.md`

```markdown
---
id: N-40
ref: snapshot:T-1/v3   # any Ref, with an optional fragment
x: 0.46                # optional, image/snapshot pins (0..1)
y: 0.20
line: null             # optional, for file refs (same as #L30 in ref)
anchor: ""             # optional: text of the pinned line, used to re-find it after edits
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

### 2.4 Ref syntax

```
[<project>:]<type>:<id>[#<fragment>]
```

| Example | Meaning |
|---|---|
| `file:src/x.go#L40` / `#L40-L52` | a line or a line range |
| `image:shots/a.png#x=0.46,y=0.20` | a point on an image |
| `commit:abc1234` | a commit in the same project (git or fossil; the index knows which) |
| `session:121c3b19-ada3-495e-a518-7fe5acf0a71e` | a session (the UUID is unique across profiles) |
| `server:3000` | a server, live by port. drashta stores the stable key `server-key:<sha1(cwd\|cmd)[:10]>` next to it |
| `task:T-12`, `snapshot:T-12/v3`, `note:N-40`, `feature:F-3`, `milestone:M-1` | authored items |
| `acme:commit:bdcb99c` | the same, in another project |

- Parse rule: if the first segment is a known type, the ref is local. Otherwise the first segment is a project ID.
- In JSON the API uses the object form `{type, projectId, id, frag?}` (as in CONTRACT.md). `"ref"` string fields accept either form.

### 2.5 Safe writes (drashta server, CLI and Claude sessions)

1. **Patch fields, never rewrite the file.** Read the file, change only the keys you own for this action, keep everything else byte-for-byte, then write a temp file and `rename` it. The CLI takes `flock(.drashta/.lock)` around read-modify-write.
2. **The server uses optimistic concurrency.** Every entity has a `rev` (sha1 of the file bytes). `PATCH` sends `If-Match: <rev>`. On a mismatch the server returns `409` with the current entity, and the UI merges or asks.
3. **Append-only zones.** Other writers only append to these:
   - The task `## Log` section and the `links:` list. Append a line and never edit an old one.
   - Notes: to reply you add a new note with `reply_to`. You never change someone else's note text. Resolving is allowed and changes only `resolved*`.
   - Snapshot versions: a new version is a new file. You never overwrite `vN.png`.
4. **Ownership rules for Claude sessions** (written into `.drashta/README.md`):
   - A session can create tasks, notes and snapshots. It can set `status` on a task (never to `done` unless the user said so), append links and log lines.
   - It must not change `title`, the body, `assignee`, `verdict` or `review_note` of items the user wrote, and it must not delete files.
   - The easy path is the CLI, which applies these rules for you:
     `drashta task new "title" --feature F-3`, `drashta task set T-12 status=review`, `drashta task link T-12 commit:abc1234`, `drashta note T-12 "text"`, `drashta snap T-12 <file|url>`.

---

## 3. Auto-linking tasks to commits and sessions

Auto links are **derived**. They live only in SQLite `task_links` with `source='auto'` and a `reason`, and a rebuild recreates them. Manual links live in the task file.

| Signal | Rule | Link |
|---|---|---|
| Commit message | Word-boundary match of `\b[Tt]-\d+\b` against tasks that exist in the project. Also trailers `Task: T-12`, `Refs: T-12, T-14`. `Fixes T-12` / `Closes T-12` also **proposes** a move to `done` in the Inbox, which the user confirms. | task ↔ commit |
| Branch name | `t-12-...`, `T-12/...`, `feat/T-12-...`. Every commit on that branch that is not on the default branch links to the task. | task ↔ commit |
| Session text | A user turn mentions `T-12` or `task:T-12`. | task ↔ session |
| Session edits | A session writes `.drashta/tasks/T-12.md` or runs `drashta ... T-12`. | task ↔ session |
| Session output | A session edited files in commits made within 2 h of its turns, in the same project. | session ↔ commit (shown on the task through the session) |
| Snapshot | `commit:` in the snapshot front matter. | task ↔ commit |
| Cross-project | `acme:T-12` anywhere above links to the other project's task. | task ↔ commit/session |

The user can link manually from any preview with "Link to task…". This appends to `links:`. It works for every Ref type: file with lines, image, commit, session, server, snapshot, another task or a note. "Unlink" on an auto link appends it to `unlinks:` so the indexer stops showing it. The badge on each link shows `auto: commit message` or `manual`.

---

## 4. Snapshot capture

**Recommended for v1: one code path, `snap(task, source)`, with three ways in.**

1. **CLI** `drashta snap T-12 <file|url> [--title ..] [--note ..]`. Claude sessions and the user can run it.
   - A file path is copied to `snapshots/T-12/v<N>.<ext>`.
   - A `http://localhost:*` URL is captured with headless Chrome (`--headless=new --screenshot --window-size=1440,900 --hide-scrollbars`), found in `/Applications`. The URL must be loopback.  
     **Update 2026-10-01 (A3): use Safari through `safaridriver` instead of Chrome; details decided later.**
   - The CLI calls `POST /api/.../snapshots` when the server is up, and writes the files directly (with `.lock`) when it is not.
2. **Viewport "Snap ⊙" button** on a server or page preview. The user picks a task and the server runs the same URL capture.
3. **Drop folder.** Put an image in `.drashta/snapshots/inbox/`, or name it `T-12--anything.png` in `inbox/`. The indexer files named images at once. Unnamed ones appear in the Inbox as "unfiled snapshot: pick a task".

Every new version:

- gets `verdict: pending`,
- moves the task from `in_progress` to `review`, and adds a log line.

Verdict `changes` moves the task back to `in_progress`. Verdict `approved` leaves the status alone (see open question 4).

Alternatives considered:

- `screencapture -i` region grab. Good for native apps. You can add it later as `drashta snap T-12 --region`.
- Pasting from the clipboard into the task panel. This is easy to add to the UI and uses the same endpoint (multipart upload).
- Picking up Claude's Playwright screenshots on their own. Too magic. Use the drop folder instead.

---

## 5. SQLite schema (`~/.drashta/index.db`, modernc.org/sqlite, WAL)

Legend: **R** = rebuildable from disk and tools. **H** = history-only, lost if the DB is deleted.

```sql
-- meta
meta(key TEXT PRIMARY KEY, value TEXT);                -- schema_version, last_sweep_at, token_secret   R
settings(key TEXT PRIMARY KEY, value TEXT);            -- hidden folders, author name, range default    H*

-- observed cache (R)
projects(id TEXT PK, name, grp, path UNIQUE, hidden INT, vcs TEXT /*git,fossil*/, branch, remote,
         description, stack JSON, readme_path, last_change_at, indexed_at);
files(project_id, path, size, mtime, kind /*code|doc|image|other*/, lines, title /*docs: first H1*/,
      PRIMARY KEY(project_id, path));                  INDEX files_mtime(project_id, mtime DESC)
dirty(project_id, path, state, PRIMARY KEY(project_id, path));
commits(project_id, id, vcs, author, ts, msg, body, plus, minus, nfiles, branch,
        PRIMARY KEY(project_id, id));                  INDEX commits_ts(project_id, ts DESC)
commit_files(project_id, commit_id, path, plus, minus); INDEX cf_path(project_id, path)
sessions(id TEXT PK, profile, jsonl_path, cwd, project_id, start, end, title, turns INT, kb INT,
         tools JSON, edited JSON, jsonl_size, jsonl_mtime);   INDEX sessions_end(project_id, end DESC)
session_projects(session_id, project_id, reason /*cwd|mention|edit*/, PRIMARY KEY(session_id, project_id));
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
           PRIMARY KEY(project_id, task_id, ref));     INDEX tl_ref(ref)   -- tasksFor(ref)
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
server_runs(id INTEGER PK, key, project_id, port, pid, name, cmd, cwd, started, ended NULL);
      INDEX sr_proj(project_id, started)               -- seeded once from acme's history JSON
file_changes(project_id, day, path, n INT, PRIMARY KEY(project_id, day, path));  -- fsnotify counts
daily_activity(project_id, day, files INT, commits INT, lines_plus INT, lines_minus INT,
               sessions INT, session_minutes INT, snapshots INT, notes INT, server_minutes INT,
               tasks_moved INT, PRIMARY KEY(project_id, day));

-- exact per-day commit numbers (R, migration 002)
daily_commits(project_id, day, commits INT, lines_plus INT, lines_minus INT,
              PRIMARY KEY(project_id, day));          -- last 35 local days, from git/fossil DayStats

-- search (R)
CREATE VIRTUAL TABLE search USING fts5(
  title, body, kind UNINDEXED, project_id UNINDEXED, ref UNINDEXED, ts UNINDEXED,
  tokenize = 'trigram');                               -- trigram: paths and code fragments match mid-word
```

- `daily_activity` is **history (H)**. It is kept whole by a Reindex. Its columns are computed from other tables, but some of those are caches that can be incomplete for an old day, so `RecomputeDaily` follows these rules:
  - `files` comes from fsnotify `file_changes` (H). After a DB loss it is back-filled roughly from file mtimes. `server_minutes` comes from `server_runs` (H). `snapshots`, `notes` and `tasks_moved` come from the authored mirror. These are taken as computed.
  - `commits` and `lines_*` take the `daily_commits` row of that day when there is one (exact). Without one they are counted from the `commits` rows, but they never go below the stored value: the `commits` table holds only the newest 200 commits per VCS, so an old day can be partly or not at all in it (for example after a Reindex).
  - `sessions` and `session_minutes` never go below the stored value: Claude deletes old `.jsonl` files, and after a Reindex the sessions are parsed again later.
  - A row is deleted only when every column is zero after these rules.
- `daily_commits` is a **cache (R)** with the exact commit count and line totals of each of the last 35 local days (`git log` / `fossil timeline` per day, so the 200-commit cap does not apply). A day in the window with no commit is stored as 0. The indexer writes it after a VCS job whose commits changed and once per local day. A Reindex drops it; the next VCS job fills it again, and the rules above keep older days of `daily_activity` intact.
- `settings` is marked H* because it is also mirrored to `~/.drashta/settings.json`, so it survives a DB delete.
- FTS content: task, feature and milestone titles and bodies, note text, commit subject and body, session title and user turns (not tool output), doc files (first 64 KB), and all file paths.
- Rebuild: `drashta reindex [project]` drops the R tables and walks everything again. The H tables stay.

---

## 6. HTTP API (`http://127.0.0.1:4455/api`, JSON)

**Guard (copied from acme `server.go`):**

- Host must be `127.0.0.1:4455` or `localhost:4455`.
- Any `Origin` must be the same.
- Mutating calls need `Content-Type: application/json` (or multipart for uploads) **and** `X-Drashta-Token`. The token is injected into `index.html` as `<meta name="drashta-token">` and rotated on every server start.

Endpoints marked **M** are mutating and need the token. `{p}` = project ID.

Errors: `{error: {code, message}}` with status 400, 404, 409 (rev conflict) or 422.

### Home and activity

| Method | Path | Response sketch |
|---|---|---|
| GET | `/boot` | `{now, statuses[], profiles[], author, counts:{liveServers, openTasks, awaitingReview, openNotes}, projects:[{id,name,group,path,vcs,branch,lastChangeAt,hidden,surveyed}]}` |
| GET | `/home?range=30` (7 or 30) | `{range, days:["2026-09-02",…], lanes:[{projectId, days:[{files,commits,linesPlus,linesMinus,sessions,snapshots,notes,serverMinutes}], live:{servers:[port], sessions:[id]}, lastChangeAt}]}` |
| GET | `/day?date=2026-09-30[&project=p]` | `{date, events:[{ts, kind, ref, label, projectId}], totals:{...}}` |
| GET | `/inbox?filter=open\|resolved\|all&cursor=` | `{notes:[note+resolved target title], unfiledSnapshots:[], proposals:[{kind:'close_task', taskRef, commitRef}], feed:[event], next}` |

### Project

| Method | Path | Response sketch |
|---|---|---|
| GET | `/projects/{p}` | `{project, overview:{readme:{path,text}, counts:{files,commits,sessions,openTasks,openNotes}, dirty:[{path,state}], servers:[…], milestones:[…]}}` |
| GET | `/projects/{p}/activity?range=30` | `{days:[{day, files, commits, …}]}` (the project seismograph) |
| GET | `/projects/{p}/tree?dir=&depth=2` | `{dir, entries:[{path, dir, size, mtime, hasChildren}]}` (lazy) |
| GET | `/projects/{p}/file?path=&from=1&to=400` | `{path,size,mtime,kind,isDoc,lines,from,to,text,truncated,notes:[…]}` (live read) |
| GET | `/projects/{p}/raw?path=` | raw bytes with the right `Content-Type` (images, snapshots). Only paths inside the project |
| GET | `/projects/{p}/commits?cursor=&limit=50&path=` | `{commits:[{id,vcs,author,ts,msg,plus,minus,nfiles,tasks:[id]}], next}` |
| GET | `/projects/{p}/commits/{id}` | `{commit, body, files:[{path,plus,minus}], patch}` (live `git show` / `fossil diff`) |
| GET | `/projects/{p}/dirty` | `{files:[{path,state}], diff}` (live) |
| GET | `/projects/{p}/sessions?cursor=` | `{sessions:[{id,profile,start,end,title,turns,kb,tools,edited,tasks:[id], reason}], next}` |
| GET | `/sessions/{id}?cursor=&limit=40&roles=user,claude,tool` | `{session, turns:[{i, role, ts, text, tool?}], next}` (live, paged from `.jsonl` by byte offset) |
| GET | `/projects/{p}/docs` | `{docs:[{path,title,mtime,lines}]}` |
| GET | `/projects/{p}/github` | `{prs:[…], issues:[…], runs:[…], fetchedAt}` (`gh`, cached 5 min) |

### Servers

| Method | Path | Response sketch |
|---|---|---|
| GET | `/servers[?project=p]` | `{active:[{key,port,pid,name,cmd,cwd,projectId,kind,title,firstSeen}], recent:[{key,…,lastSeen}]}` |
| POST **M** | `/servers/kill` | req `{pid, key}` → `{ok}` (SIGTERM, then SIGKILL after 3 s, as in acme) |
| POST **M** | `/servers/restart` | req `{key}` → `{ok, log:"~/.drashta/logs/<port>.log"}` (409 if a restart is already pending) |

### Tracker

| Method | Path | Response sketch |
|---|---|---|
| GET | `/tracker?project=&filter=all\|review&feature=&status=` | `{milestones[], features[], tasks:[{…, latestSnapshot:{version,verdict}, linkCount, openNotes}]}` |
| GET | `/projects/{p}/tasks/{id}` | `{task, rev, links:[{ref, source, reason, resolved:{title,subtitle,date}}], snapshots[], notes[], log[]}` |
| POST **M** | `/projects/{p}/tasks` | req `{title, featureId?, status?, assignee?, body?, links?[]}` → `{task, rev}` (201) |
| PATCH **M** | `/projects/{p}/tasks/{id}` | `If-Match: rev`; req `{status?, title?, featureId?, assignee?, body?}` → `{task, rev}` |
| POST **M** | `/projects/{p}/tasks/{id}/links` | req `{ref}` → `{links}` |
| DELETE **M** | `/projects/{p}/tasks/{id}/links?ref=` | manual: removed; auto: added to `unlinks` |
| POST/PATCH **M** | `/projects/{p}/features[/{id}]`, `/projects/{p}/milestones[/{id}]` | same pattern as tasks |
| DELETE **M** | `/projects/{p}/{tasks\|features\|milestones}/{id}` | moves the file to `.trash/` |

### Snapshots

| Method | Path | Response sketch |
|---|---|---|
| GET | `/projects/{p}/tasks/{id}/snapshots` | `{snapshots:[{version,title,url,captured,author,commit,verdict,reviewNote,notes:[…]}]}` |
| POST **M** | `/projects/{p}/tasks/{id}/snapshots` | multipart `image` **or** JSON `{url}` / `{path}`, plus `title?`, `note?` → `{snapshot}` |
| PUT **M** | `/projects/{p}/tasks/{id}/snapshots/{v}/verdict` | req `{verdict:'approved'\|'changes'\|'pending', note?}` → `{snapshot, task}` (task status may change) |
| POST **M** | `/projects/{p}/snapshots/inbox/{name}/file` | req `{taskId}` → files an unfiled drop |

### Notes, refs, search, actions

| Method | Path | Response sketch |
|---|---|---|
| GET | `/notes?ref=&project=&open=1` | `{notes[]}` |
| POST **M** | `/notes` | req `{ref, text, line?, x?, y?, replyTo?}` → `{note}` (the note is stored in the ref's project; `anchor` is filled from the line text) |
| PATCH **M** | `/projects/{p}/notes/{id}` | req `{resolved?}` from anyone; `{text?}` only from the author → `{note}` |
| GET | `/resolve?ref=` | `{ref, type, projectId, title, subtitle, icon, date, obj, nativeLabel, nativeActions:[…], notes[], tasks[]}` |
| GET | `/search?q=&project=&kind=&limit=30` | `{hits:[{kind, ref, projectId, title, snippet, ts}]}` (FTS5 `bm25`, then by recency) |
| POST **M** | `/refresh` | req `{projectId?}` → `{started:true}`; progress arrives over SSE |
| POST **M** | `/open` | req `{ref, action?:'default'\|'editor'\|'finder'\|'terminal'\|'safari'\|'resume'}` → `{ok, ran:"code -g …"}`. The server builds the command from the ref and never accepts a raw command (D4) |
| GET/PATCH **M** | `/settings` | `{hiddenFolders[], author, editorCmd}` |

### Server-Sent Events: `GET /events`

- One stream per tab.
- The server sends a `hello {now, counts}` event at the start and after every reconnect.
- The client refetches what it shows when an event arrives and when the page becomes visible again (D5).

| event | data |
|---|---|
| `project` | `{projectId, what:'files'\|'commits'\|'dirty'\|'sessions'\|'docs'}` |
| `servers` | `{projectId?, active:[port]}` |
| `tracker` | `{projectId, kind:'task'\|'feature'\|'milestone'\|'snapshot'\|'note', id}` |
| `activity` | `{projectId, day}` (a seismograph lane changed) |
| `scan` | `{projectId?, state:'running'\|'done', at}` |
| `counts` | `{liveServers, openTasks, awaitingReview, openNotes}` |

lsof runs every 5 s while at least one `/events` stream is open, and every 60 s otherwise (S8).

---

## 7. Prototype `app` API → HTTP

The React port keeps an `app`-like store. The table shows what each call uses.

| Prototype (`CONTRACT.md` + `CHANGES-v2.md`) | v1 |
|---|---|
| `app.data.now`, `projects[]` (summary fields) | `GET /boot` |
| `project.tree` | `GET /projects/{p}/tree` (lazy per folder) |
| `project.fileContents` | `GET /projects/{p}/file` |
| `project.commits` / `fossilCommits` / `patch` | `GET /projects/{p}/commits`, `/commits/{id}` (git and fossil merged, `vcs` field) |
| `project.dirty`, `dirtyDiff` | `GET /projects/{p}/dirty` |
| `project.servers` | `GET /servers?project=p` |
| `project.sessions[].turns` | `GET /projects/{p}/sessions` + `GET /sessions/{id}` (paged) |
| `project.activity30` | `GET /projects/{p}/activity?range=30` |
| `assets[path]` → `/_blob/…` | `/api/projects/{p}/raw?path=…` |
| `milestones`, `features`, `tasks`, `snapshots` | `GET /tracker`, `GET /projects/{p}/tasks/{id}`, `/snapshots` |
| `notes` | `GET /notes`, `GET /inbox` |
| `app.ui.*`, `go`, `openProject`, `setProjectTab`, `setRange`, `openItem`, `closeItem`, `setTrackerFilter` | client state + URL routes: `/`, `/p/:project/:tab?`, `/tracker?filter=review`, `/inbox`, with `?item=<ref>` for the viewport. Route `map`/`pulse` → `/` (the seismograph, U1). `setWallpaper` is dropped; the Plash class does this job (D3). |
| `app.resolve(ref)` | `GET /resolve?ref=` (cached per ref and invalidated by SSE) |
| `app.native(ref)` | `POST /open` **M** |
| `app.notesFor(ref)` / `app.tasksFor(ref)` | the `notes` and `tasks` fields of `/resolve`, or `GET /notes?ref=` |
| `app.eventsOn(projectId, day)` | `GET /day?date=&project=` |
| `app.dayActivity(projectId)` | `GET /projects/{p}/activity` (one project) or `GET /home` (all lanes) |
| `app.setTaskStatus(id, s)` | `PATCH /projects/{p}/tasks/{id}` **M** |
| `app.addTask({…})` | `POST /projects/{p}/tasks` **M** |
| `app.linkToTask(id, ref)` | `POST /projects/{p}/tasks/{id}/links` **M** |
| `app.addNote({…})` | `POST /notes` **M** |
| `app.toggleNote(id)` | `PATCH /projects/{p}/notes/{id}` `{resolved:!r}` **M** |
| `app.setVerdict(snapId, v, note)` | `PUT …/snapshots/{v}/verdict` **M**. The prototype's `s-ph3` becomes `T-1/v3` |
| `app.toggleServer(p, port)` | `POST /servers/kill` or `/servers/restart` **M** (by `key`, not by port) |
| `app.refresh(p)`, `ui.scanning`, `ui.scannedLabel` | `POST /refresh` **M** + SSE `scan` |
| `app.toast(msg)` | client only |
| (new) search palette | `GET /search` |

Prototype ID migration: `t1` → `T-1`, `f1` → `F-1`, `m1` → `M-1`, `n1` → `N-1`. Snapshot `s-*` → `<task>/v<version>`. The two prototype snapshots that share `v3` (`s-vo3a`, `s-vo3b` = v3, v4) are fine because the versions are already distinct.

---

## 8. Open questions

1. **Commit `.drashta/` to git?** Text files yes. Snapshot images can grow the repo. Should we add `snapshots/**/*.png` to `.gitignore` by default, use LFS, or commit them?
2. **URL snaps: answered (A3).** Use Safari via `safaridriver` (built into macOS; enable "Allow Remote Automation" once). The fallback is a small WKWebView helper. Details come later.
3. **ID clash across git branches or worktrees.** Two branches can both create `T-13`. Git reports an add/add conflict on merge. Is that acceptable for v1, or do we want a `drashta renumber` fix-up?
4. **Verdict → status rules.** Should `approved` on the latest snapshot move the task to `done`, or only suggest it? This draft only suggests it.
5. **How does the CLI know the calling Claude session ID?** Options: a Claude Code hook that exports it, or matching cwd plus the most recently active `.jsonl`. Without the ID, the author is just `Claude`.
6. **Line notes after edits.** Re-anchor through `anchor` text matching in v1, or show "line moved?" and let the user fix it?
7. **Server identity.** `server:3000` is readable, but ports get reused. Should manual task links store the stable `cwd|cmd` key instead, and show the port only as a label?
