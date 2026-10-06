# drashta v1: UI specification

This file tells you what each screen shows and how it behaves.
`00-decisions.md` wins if the two files disagree.
The references are the design prototype (home, "C · Seismograph") and the design prototype (the other screens).
The chart map (of the design prototype) is **not** part of v1.

---

## 1. Principles

- **Preview in, open out.** A click on an item opens a preview in the right-hand viewport. "Open ↗" runs the native command. The one exception is a live localhost server, which runs inside the viewport in a sandboxed frame (decision U10).
- **Nothing is thrown in your face.** No pop-ups, no modal alerts, no auto-opened panels, no badges that pulse. Counters are there for you to read, and they do not ask for your attention.
- **Any detail is a few clicks away.** Home → lane → day → item → Open ↗. Three clicks at most to reach any native tool.
- **Time is the main axis.** Each event is a tremor on a lane.
- **Hierarchy without bold or italic.** One font, one weight (400). Use size, colour, letter-spacing and case to rank text.
- **The page is a live instrument.** It updates when the data changes, and it does not animate while it waits.
- **Plash-aware.** The same page is the desktop wallpaper (readout mode) and the command center (browsing mode).

---

## 2. Global shell

### 2.1 Layout (browsing mode)

```
┌ drashta  every tremor…      [SEARCH ______________]  last scan 14:53  [↻ refresh all]  live 2 · tasks 9 · review 1 · notes 3 ┐
│ rail │ section content (Home / Project / Tracker / Inbox)                    │ viewport (when an item is open) │
└──────┴───────────────────────────────────────────────────────────────────────┴─────────────────────────────────┘
```

- **Top bar**, 56px high, bottom hairline.
  - Top left: the app name **drashta**. It is the only link to Home (U2). There is no home, map, pulse or reviews button.
  - Next to the name, on Home only: the subtitle "every tremor in your projects, last 30 days" (the number follows RANGE).
  - On other sections: a breadcrumb in spaced uppercase, for example `PROJECT › acme › COMMITS`. The project name keeps its own case.
- **Left rail**, 72px wide. It has three items: **Project**, **Tracker**, **Inbox**. Each item has a stroke icon and a 10px uppercase label.
  - Project opens the last project you viewed. If there is none, it opens a project picker (a list of lanes without bars).
  - Tracker badge: the number of snapshots awaiting review. Inbox badge: the number of open notes. When a count is 0, the badge is not shown. A badge has no animation.
  - The active item has an amber 2px bar on the rail edge and text colour `text`. The other items are `muted`.
- **Live counters** (top bar, right side). One bordered strip with four cells. Each cell is a button.

| Counter | Colour | Click goes to |
|---|---|---|
| `live n` (servers running) | teal | Home, ordered by recency, with live lanes lit |
| `tasks n` (open tasks) | amber | Tracker, All tasks |
| `review n` (snapshots pending) | ivory | Tracker, Awaiting review |
| `notes n` (open notes) | coral | Inbox, Notes: Open |

### 2.2 Global search

- There is one field in the top bar: label `SEARCH`, placeholder `name, commit, doc, :port, stack`, width 300px, height 42px.
- **What it indexes** (from the SQLite index, S7):

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

- **Syntax**
  - Plain words: case-insensitive substring match on all kinds.
  - `:4455`: matches only server ports (a leading colon means "port").
- **Results panel**: a dropdown below the field, 680px wide, max 60% of the viewport height. Results are grouped by kind. Each group has an uppercase header in the kind's signal colour. A row shows kind, name (ellipsis) and context (muted). The header line reads `12 MATCHES · 3 LANES LIT`.
- **On Home**, when a query is active: the lanes that match get a faint row tint (`match`) and an amber name. The other lanes dim to 22% opacity. A cleared query restores them.
- Search updates as you type. Nothing opens until you choose a result. `Enter` opens the first result. `↑`/`↓` move through the results. `Esc` closes the panel, and a second `Esc` clears the query.
- No match: "nothing registers for that." (muted, 14px).

### 2.3 Refresh and scan status

- `↻ refresh all` is the only filled amber button on the page (44px high, ground-coloured text). It asks the server to rescan all projects.
- The scan label is at the left of the button: `last scan 14:53 UTC` → `rescanning…` → `updated just now` → `updated 4m ago` (relative time, recomputed only when the page re-renders or becomes visible again; no timer).
- While a user-started rescan runs, one amber 2px line sweeps once across the lanes (1s, then it is removed). If reduced motion is on, there is no sweep, only the label changes.
- Each project page has its own `↻ refresh` (amber outline).
- A scan that finds nothing new shows the toast "All projects rescanned · no new changes".

### 2.4 Preview viewport

- A right-hand panel, 46% of the width, min 560px. It overlays the section with a single left shadow (the only shadow in the app). The section stays scrollable.
- **Header row** (36px controls): `←` `→` (viewport history), type chip (glyph + kind in signal colour), title, subtitle (project · date), **Open ↗** (amber outline; the tooltip shows the exact command), `✕` close.
- **Tasks row** under the header: linked task chips (click → task preview), "not linked" when there are none, and a `Link to task…` select.
- **Notes**: every type accepts notes. File notes can be on a line, image and snapshot notes are pinned at x,y, and the other types take whole-item notes.
- **Missing item**: "Nothing to show here." and one muted line: "This item isn't in the latest scan — it may have moved, been deleted, or sits in a folder drashta keeps collapsed."

| Type | Preview body | Open ↗ runs |
|---|---|---|
| file | Code with line numbers; language, size, modified, state (clean/dirty). `.md`/`.txt`: `Rendered` / `Source` toggle. Click a line number to pin a line note. Long files: first part plus "Open ↗ for the rest". | `code -g <path>:<line>` |
| image | Image fit to width; size, path; pin notes; "Used in snapshots"; `Make snapshot for task…`. | `open -a Preview <path>` |
| commit | id, vcs, author, date, `+n −n`; files changed; per-file diff blocks with `Expand all` / `Collapse all`; a commit note. Fossil: "Diff loads live via fossil" (fetched when opened). | git: `git -C <path> show <id>` · fossil: `fossil diff --checkin <id>` (in Terminal) |
| session | profile, start, duration, turns, tool counts, files edited; transcript with a role filter (user / claude / tool). | `cd <path> && CLAUDE_CONFIG_DIR=<profile> claude --resume <id>` in Terminal |
| server | port, pid, process, cmd, kind (dev/helper), state; `Stop` / `Start`; the page in a sandboxed frame if the server speaks HTTP, else "Not a web page"; if unreachable: "This site can't be reached" plus the last capture. | `open -a Safari http://localhost:<port>` |
| snapshot | Annotated image (see §6.4); version list; `Compare with` a second version side by side; verdict controls. | `open -a Preview <path>` |
| task | Status, feature, milestone, assignee, created; links; snapshot strip; notes; edit fields (see §6.3). | `open <path>/.drashta/tasks/<id>.md` |

- Other native actions, same rule (the server runs them; D4): Finder `open <path>`, Terminal `open -a Terminal <path>`, editor on a folder `code <path>`, GitHub `open -a Safari <remote-url>`.

### 2.5 Toasts

- One toast at a time, bottom centre, 12px text, `raised` fill, 1px amber border. It stays about 2s, then fades (no fade if reduced motion is on). It has `role="status"`.
- Toasts confirm what you did: "Opened in Preview", "T-12 → in review", "Note pinned", ":5173 stopped". A failed native command gives a coral-border toast with the error message, and it stays until you dismiss it.
- A background event never makes a toast.

### 2.6 Keyboard

| Key | Action |
|---|---|
| `/` | focus search |
| `Esc` | close search → deselect day → close viewport (in that order) |
| `g h` / `g p` / `g t` / `g i` | Home / Project / Tracker / Inbox |
| `←` `→` on a lane | previous / next day; `↑` `↓` previous / next lane |
| `Enter` on a day | read that day; on an item: preview it |
| `o` | Open ↗ for the item in the viewport |
| `[` `]` | viewport back / forward |
| `r` | refresh (the project on a project page, else all) |

- Shortcuts do nothing while a text field has focus, except `Esc`.

---

## 3. Home: the seismograph

### 3.1 Layout (top to bottom)

1. Top bar (§2.1).
2. **Control row** (40px, top hairline):
   - Left: group chips `all 17` `personal 12` `work 2` `tools 3` and a toggle `○ show hidden (2)` / `◉ show hidden (2)`. The counts leave hidden folders out unless show hidden is on.
   - Right: `ORDER` `recency | loudest` and `RANGE` `7d | 30d`. These are segmented buttons, 34px high (36px target with padding).
3. **Date axis** (16px): muted 10px uppercase ticks over the lane column, `02 SEP … 16 SEP … TODAY`. It is sticky when the lanes scroll.
4. **Lanes**: one row per project in a scrolling area.
5. **Day readout panel** (128px, top hairline), always on screen.

### 3.2 A lane

| Column | Width | Content |
|---|---|---|
| Identity | 230px | name (13px, ellipsis at 150px, a button → project page) · group tag (`PERSONAL`, 10px uppercase, muted) · `±n` uncommitted paths (amber, 11px) · `HIDDEN` tag (bordered, 10px) when shown hidden |
| Tremor | fluid (min 600px) | one column per day; bottom hairline as the baseline |
| State | 150px, right-aligned | last touched `13m ago` (muted, 11px) · `● n` teal when servers are live, else `○` in hairline colour |

- The row is 36px high. The name is `text` colour when active and `muted` when flat.
- **Day column encodings** (two bars per day, side by side, bottom-aligned):
  - Left bar, grey `#6b675d`: file changes that day. Height = log scale, min 2px when above 0.
  - Right bar, amber: commits that day. Height = lines changed (plus + minus), log scale, min 4px. A commit with no line stats (fossil) draws at min height.
  - The scale is **shared across all lanes** on Home (global max), so lanes can be compared. The project page uses its own scale.
  - At 7d, day columns get a faint separator hairline.
  - The whole day column is a button (`aria-label`: "acme, 14 Sep: 12 file changes, 2 commits"). The selected day gets the `select` fill and a 1px amber inset line. Hovering a column (browsing mode only) makes the grey bar brighter.
- **Empty and flat states** (text in the tremor column, bottom left, 11px, `dim-text`):
  - The folder has no files: `empty directory`.
  - No activity in range: `flat — no tremor in 30d` (or `7d`).

### 3.3 Ordering, filters, hidden folders, range

- **recency** (default): by last touched, newest first.
- **loudest**: by the sum of file changes and commit lines in the range, then by recency.
- The order is stable. A lane moves only when the data changes, never on hover.
- **Group chips** filter to one group. With 0 lanes: "no projects in this group."
- **Hidden folders** (S4): the default hidden set is `venv`, `vendor-v1`. You hide or unhide a project from its project page. The hidden list is server config, so it is the same in every window. With show hidden on, hidden lanes show at 60% opacity with a `HIDDEN` tag.
- **Range** 7d | 30d changes the axis, the lanes, the flat test and the subtitle. If the selected day falls outside the new range, it is cleared.
- Control state (group, order, range, show hidden) is kept in `localStorage`, so the wallpaper shows the same view.

### 3.4 Day readout panel

- **With no day selected**: one line in 14px muted, "click any day in a lane to read its tremor here.", and the legend:
  `▮ file changes that day` (grey) · `▮ commits (height = lines changed, log scale)` (amber) · `● servers live now` (teal) · `±n uncommitted paths` (amber) · `name → project page`.
- **With a day selected** (three zones):

| Zone | Content |
|---|---|
| Left (230px) | `READOUT` label · date (28px, e.g. `14 Sep`) · `acme →` (amber, opens the project page) |
| Middle (200px) | counts, label muted and value text: `file changes 12` · `commits 2` (amber) · `lines +340 −88` · `sessions 1` (violet) · `snapshots 1` (ivory) · `notes 2` (coral) |
| Right (fluid, scrolls) | that day's items in time order: time · kind glyph in signal colour · id/label · context. Commits `◆` amber (`a1b2c3 msg +40 −3`), sessions `✦` violet (title, profile), snapshots `▣` ivory (title vN, verdict), notes `✎` coral (text, on what), tasks created `▮` amber, then `files changed` (paths touched that day, top 20, then "+N more") |

- A click on any item opens it in the **viewport**. It does not change the page.
- Quiet day: "files moved, nothing committed." (files only) or "a flat line — no tremor this day." (nothing).
- `Esc` or a click on the selected column again clears the readout.

### 3.5 Many projects

- The lane area scrolls vertically. The axis, control row and readout panel stay fixed.
- There is no pagination and no virtual list below 200 lanes (currently 17). Above 200, only the visible rows are rendered.
- The search dim (§2.2) is the way to find one lane among many.

### 3.6 Live updates (D5)

- The server pushes changes over one event stream (SSE): lane data, `±n`, live servers, scan status, counters, and tracker and note changes.
- On a push, only the affected lanes re-render. There is no flash or highlight. The scan label moves to "updated just now".
- On `visibilitychange` to visible (and on a stream reconnect), the page fetches a full snapshot and resyncs.
- There is no reload timer and no polling in the page. lsof cadence is a server matter (S8).
- If the stream is down, the scan label reads `offline — last scan 14:53` in coral, and the page keeps showing the last data.

---

## 4. Plash-aware modes (D1–D6)

- Plash adds the class `plash-is-browsing-mode` to `<html>` when Browsing Mode is on.
- The Plash URL is `http://127.0.0.1:4455/?desktop=1`. **Readout mode = `desktop=1` and no class.** In a normal Safari tab, or with the class, you get browsing mode.
- The CSS keys off `html.desktop:not(.plash-is-browsing-mode)`. A class change switches the mode at once, with no reload.

| Element | Readout mode | Browsing mode |
|---|---|---|
| App name + subtitle | shown | shown |
| Live counters | shown (read-only) | shown (buttons) |
| Scan label | shown | shown |
| Search, refresh all | hidden | shown |
| Rail | hidden | shown |
| Group / order / range controls | hidden (the saved values apply) | shown |
| Date axis, lanes, `±n`, live dots, ago | shown | shown |
| Selected-day highlight, day readout contents | hidden; the panel shows the legend | shown |
| Viewport, search panel, toasts, hover states | hidden | shown |
| "⌥⌘D to interact" hint | shown, bottom right, 10px uppercase, muted | hidden |

- **Readout layout**: lanes that do not fit are not scrolled. The last row reads `+N quieter projects` (muted). Lanes are sorted by the saved order.
- **Leave space for the macOS menu bar and Dock**: 32px top padding and 80px bottom padding in readout mode.
- **Navigation rule (D4)**: the page never navigates the top frame to another origin. No `target=_blank`, and no external `href`. Every Open ↗, GitHub link and Safari link is a `POST /api/native` to the local server, which runs `open …`. In-app routes use client-side routing only.
- **Performance**: no CSS animation or `requestAnimationFrame` loop runs at rest. Live dots are static (no pulse ring). Transitions are ≤150ms and only follow a user action. Readout mode has no hover handlers. The page stays at 0% CPU when idle.
- **Fallback (D6)**: the same page, in Safari as an "Add to Dock" web app without `?desktop=1`, runs in browsing mode.

---

## 5. Project page

### 5.1 Header cartouche

- A box with a double hairline border (`3px double hairline`) and square corners:
  - Line 1: project name (32px) · group tag · `HIDDEN` tag when hidden.
  - Line 2 (12px muted): path · stack · `git main` / `fossil trunk` · remote host.
  - Right side: RANGE `7d | 30d` · scan label (`touched 2h ago` / `rescanning…`) · `↻ refresh`.

### 5.2 Native actions

- A row of 44px outline buttons. Each one runs through the server:

| Button | Shown when | Runs |
|---|---|---|
| Editor | always | `code <path>` |
| Finder | always | `open <path>` |
| Terminal | always | `open -a Terminal <path>` |
| Open in Safari | a server is live | `open -a Safari http://localhost:<port>` (teal outline) |
| Stop server(s) / Start server | servers known | kill pid / rerun cmd (coral outline for stop) |
| GitHub | a remote exists | `open -a Safari <remote>` |
| Resume Claude | a session exists | latest session, its profile, `claude --resume` |
| Hide / Unhide project | always | updates the hidden list |

### 5.3 Project seismograph

- A panel (`panel` fill, faint hairline), 150px tall, full width. It has its own scale, a `max · 0` gutter and two dashed gridlines.
- The value is shown above a bar when 7d is set, for the selected day, and for the peak day.
- Header line: the peak (for example `peak 14 Sep · 48 file changes`) on the left, and the legend `▮ file changes · ▮ commits · log scale · click a day` on the right.
- A day click fills a one-line readout under the chart: date · file changes · commits · lines · `read the day →`. `read the day →` opens the full list of that day's items (the same list as §3.4) in the Overview tab. With no day selected: "click a day column above to read it."

### 5.4 Tabs

- The tab bar is 40px high. The active tab has a 2px amber underline. Tab labels carry counts: `Commits 42`, `Servers 1/2`.

| Tab | Shows | Empty state |
|---|---|---|
| Overview | Vitals grid (last commit, uncommitted, live servers, sessions in 30d, open tasks, awaiting review, open notes, scan); **Working copy · uncommitted** list (`±` path, click → file preview); **Last commit**; **Where you left off** (latest session, Continue); **Open notes**; **Remote**: PRs, issues and CI runs from `gh`, loaded live when the tab opens | "Clean — nothing uncommitted." / "not under version control." |
| Files | Tree of the indexed files (node_modules, .git, dist, target and venv collapsed and marked); size, modified; dirty files marked `±`; click → preview | "empty directory — nothing to list." |
| Commits | git and fossil check-ins, newest first: id (amber) · date · message · `+n −n`; click → commit preview | "no commits on record." / "not under version control — no commits to read." |
| Sessions | Sessions that ran in this folder or its subfolders: profile · subfolder · started · duration · turns · title; Resume. Then "mentioned in other projects' sessions" (S6) | "No Claude sessions ran in this folder." |
| Servers | port · pid · process · cmd · kind · state dot; Safari ↗; Stop/Start | "No localhost listeners belong to this project." |
| Docs | `.md`/`.txt` in the tree; click → rendered preview | "No .md or .txt files in the indexed tree." |
| Tracker | This project's milestones with progress, features, task counts by status, awaiting review; `Open board →`, `Awaiting review →` | "No milestones or tasks for this project yet — start them on the board." |

- Every row in every tab previews in the viewport. Nothing expands inline, except the commit meta line when you use the keyboard.

---

## 6. Tracker

- Data lives in each project's `.drashta/` folder (S9). The footer shows "stored as .drashta/tasks/*.md in each project".

### 6.1 Controls

- `Project` select (All or one project; it opens on the current project when you come from a project page).
- `Show`: `All tasks` | `Awaiting review (n)`. The top-bar `review` counter sets this filter (controlled by the URL, `?filter=review`).
- `View`: `Board` | `Milestones` | `Timeline`.
- `Group`: `None` | `Feature` (Board only; groups become swimlanes).
- `+ Add task`.

### 6.2 Views

- **Board**: five columns in this order. Each header has its status colour (a square glyph), the label and a count.

| Status | Label | Colour |
|---|---|---|
| backlog | Backlog | `#6b675d` |
| todo | To do | `#a39e92` |
| in_progress | In progress | `#ffb347` |
| review | Review | `#b69cff` |
| done | Done | `#6fd6c4` |

  - **Card**: id (muted) · title · feature · assignee · counts (links, open notes). A **snapshot strip** shows thumbnails of the snapshot versions, oldest to newest, 40px tall, with a 1px border in the verdict colour (pending hairline, approved teal, changes coral) and `vN` under each.
  - A card click opens the task preview. `←`/`→` buttons on the card (and in the preview) move the status one step; the toast reads "T-12 → review". Move beyond the ends: "Already first status" / "Already done". There is no drag and drop in v1.
  - Empty column: "no tasks" (muted).
- **Milestones**: one block per milestone: title, `milestone due 12 Oct`, progress bar (done / total, teal on hairline), remaining count, and its features with their tasks. "Outside milestones" and "Unscheduled" collect the rest.
- **Timeline**: milestones on a date axis (the same axis style as Home), with tasks as ticks at their created date and a done mark when finished.

### 6.3 Task create and edit

- **Create** (an inline form at the top of the board, not a modal): title (`What needs doing?`, required: "Give the task a title."), project, feature (`No feature` allowed; "Pick a feature (this project has none yet)."), status (default `todo`). `Add task` / `Cancel`. Toast: "Task T-14 created".
- **Edit** (in the task preview): title, status, feature, milestone, assignee are inline fields that save on blur. Links: any item can be linked from the viewport header (`Link to task…`). Linked items list here and preview when clicked. Notes: add, resolve, reopen.

### 6.4 Awaiting review

- The filter shows tasks whose latest snapshot is `pending`. With none: "Nothing awaiting review." and `Show all tasks`.
- Layout: the task list on the left, and the review area in the main column (it is not the viewport, so the image gets the space).
  - **Versions** list: `v3 · 14 Sep · alex · pending`, and so on. Choose one to view it.
  - **Frame**: the image fit to the area. `Annotate` toggle: "Annotate mode — click anywhere on the frame to drop a pin." A pin is a numbered ivory circle at (x, y). Each pin has a note beside it: "What should change here?". Notes list under the frame, and hovering or focusing a pin highlights its note. "No notes pinned on v3."
  - `Compare with` (select a version): the two versions side by side at the same scale.
  - **Verdict**: `Approve` (teal outline) · `Request changes` (coral fill; it needs a review note, "What needs to change?") · `Reset to pending`. Review note textarea. Toast: "Gate art v3 · changes requested".
  - `Open to annotate ↗` opens the snapshot in the viewport. `Task detail →` opens the task preview.
- Snapshots are made from an image or server preview (`Make snapshot for task…`). A new snapshot becomes the next version on that task, with verdict `pending`.

---

## 7. Inbox

- Heading `INBOX`, sub-line "everything addressed to you".
- **Needs you** (top block, spaced uppercase label). Each line is a count and a link, and a line with 0 is not shown:
  - `3 open notes` → Notes: Open
  - `1 snapshots awaiting review` → Tracker, Awaiting review
  - `2 tasks in review` → Tracker board, Review column
  - `2 dirty working copies` → **Uncommitted** list (project · `±n` · oldest change), click → project Overview
  - With all at 0: "Nothing needs you." (muted).
- **Notes**: filter `Open` | `Resolved` | `All`. Row: note text · on `file acme/main.go:42` (type and id) · project · author · date · `Resolve`/`Reopen`. A click previews the item it is on, scrolled to the line or pin.
  - Empty: "No open notes. All clear." / "Nothing resolved yet." / "No notes yet." Help line: "Notes pinned to files, commits, sessions and snapshots collect here."
- **Activity**: a unified feed of all projects, grouped by day (newest first). Filter: `All | Commits | Sessions | Snapshots | Tasks | Notes`. Row: time · glyph in signal colour · project · label. A click previews the item. Empty: "No activity of this kind yet."

---

## 8. Visual system

### 8.1 Tokens

| Token | Value | Use |
|---|---|---|
| `ground` | `#0b0b0a` | page background |
| `panel` | `#121210` | viewport, chart panels, popovers |
| `raised` | `#1a1916` | toasts, inputs, card fill |
| `hairline` | `#2c2b27` | borders, dividers, lane baseline, `○` |
| `hairline-faint` | `#1d1c19` | row separators, gridlines |
| `select` | `#221c12` | selected day column / row |
| `match` | `#15130f` | search-matched lane tint |
| `text` | `#e8e4da` | primary text |
| `muted` | `#a39e92` | labels, secondary text (min colour for text) |
| `dim` | `#6b675d` | file-change bars, backlog, non-text glyphs only |
| `amber` | `#ffb347` | commits, primary action, focus, active tab, in_progress |
| `teal` | `#6fd6c4` | live servers, approved, done |
| `violet` | `#b69cff` | Claude sessions, review status |
| `ivory` | `#f3efe4` | snapshots, review pins |
| `coral` | `#ff8a66` | changes requested, notes, errors, stop actions |

- The reference file uses `#9a958a` for muted. v1 uses `#a39e92` everywhere.
- Signal colours are always paired with a glyph or label, never colour alone.

### 8.2 Type (JetBrains Mono, weight 400 only)

- Stack: `'JetBrainsMono Nerd Font Mono', 'JetBrains Mono', ui-monospace, monospace`.
- `font-weight: 400` and `font-style: normal` on everything, including `h1–h6`, `b`, `strong`, `em`, `th`, buttons, and rendered markdown (markdown bold → `text` colour, italics → `muted` colour).
- Line height 1.5 for body and 1.1 for display.

| Role | Size | Case / tracking | Colour | Was (reference) |
|---|---|---|---|---|
| App name | 26px | lower, 0.01em | text | serif 50px |
| Page title (project name) | 32px | as written | text | serif 42px |
| Readout date | 28px | as written | text | serif 34px |
| Subtitle, empty-state sentence | 14px | lower, sentence | muted | serif italic 18–24px |
| Body, rows, buttons | 12–13px | as written | text | mono 12px |
| Secondary meta | 11px | as written | muted | mono 11px |
| Label (SEARCH, ORDER, READOUT) | 10px | UPPER, 0.14em | muted | mono 10px |
| Map-style heading (cartouche, section heads) | 10–11px | UPPER, 0.22em | text | none |
| Tag (group, HIDDEN) | 10px | UPPER, 0.10em | muted | 9px dim |

- Empty-state sentences keep their lower-case, calm wording. The italics are dropped. Hierarchy comes from `muted` colour and 14px size.

### 8.3 Space, borders, shape

- The spacing scale is 4, 8, 12, 16, 24, 32, 40. Page padding is 24px (40px on wide screens ≥1440). Gap between rows is 0, with hairline separators.
- Borders are 1px `hairline`. Radius ≤ 3px (0 for lanes, charts and cartouches).
- There are no shadows, except the left edge of the viewport and the search panel. There are no gradients and no left-border accent cards.
- Buttons: 1px hairline border with a transparent fill. Primary: amber fill (refresh all only) or amber outline. Danger: coral outline.

### 8.4 Map-style touches (U6)

- Cartouche: the project header and the Inbox "Needs you" box get a `3px double hairline` border.
- Section heads in spaced uppercase (0.22em).
- One ornament per screen at most: a scale tick row under the date axis (the day ticks), or a small bearing mark by the scan label. No compass roses and no illustrations.

### 8.5 Accessibility

- **Focus**: every control shows a `1px solid amber` outline with a 2px offset on `:focus-visible`. Day columns are focusable as a roving tabindex, one tab stop per lane.
- **Contrast**: `text` and `muted` on `ground` and `panel` pass 4.5:1. `dim` is never used for text that carries meaning.
- **Targets**: primary 44px. Secondary 36px min. A day column is at least 36px high, and at 30d its width can go below 36px, so the lane row is the target and arrow keys pick the day.
- **Semantics**: rail = `nav`, viewport = `aside`, tabs = `tablist`, toasts = `role="status"`, segmented controls use `aria-pressed`. Every bar column has a full `aria-label`.
- **Reduced motion**: no sweep, no toast fade, no transitions.
- **No colour-only meaning**: verdicts, statuses and live state all have a glyph or word.

---

## 9. Out of scope for v1

- The chart map / portolan home (U7). It is kept for later as a possible folder browser inside a project.
- Pulse and Reviews sections (U3).
- Drag and drop on the board.
- Editing files, committing or pushing from drashta. It observes and opens, and it writes only `.drashta/`.
- Multi-user assignees, comments threads, notifications.
- Light theme.
- A custom native helper app (D6 last resort).

### Open UI questions

1. **Sessions on the lane.** Should a Claude session also mark the lane (for example a small violet tick at the top of the day column)? Or should sessions stay only in the readout (U1 names only files, commits and servers)?
2. **Readout-mode URL.** Is `?desktop=1` acceptable as the marker for "running in Plash"? Plash only exposes the browsing-mode class, so a plain Safari tab and the wallpaper cannot be told apart without it.
3. **Server preview.** Should it be a live sandboxed frame of `localhost:<port>`, or only a last capture plus Open ↗ (cheaper, and no risk of the page navigating)?
4. **GitHub.** Is a block inside Overview enough, or does `gh` data (PRs, issues, CI) need its own tab?
5. **Search prefixes.** Is `:port` enough for v1, or are `group:` / `kind:` filters needed as well?
