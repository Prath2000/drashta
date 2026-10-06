# drashta: open questions

These need a user answer before or during the spec (phase 2). Each has a proposed default, so work can continue if the answer is "use the default".
Details are in the linked docs.

## Answered 2026-10-01

- Q1 yes: cgo is OK for FSEvents. Measured: `~/projects` has 161,898 files and 24,792 folders. After skipping node_modules, .git, dist, target and venv it has 24,480 files and 5,581 folders, about 30k watch items today. That is a count, not a cap. FSEvents has no per-file limit, so growth is fine.
- Q2 **yes** (use the default).
- Q3 **use Safari instead of Chrome** for URL snapshots, through `safaridriver`, which is built into macOS and needs "Allow Remote Automation" turned on once. A WKWebView helper is the fallback. To be solved later; not blocking.
- Q4 **yes** (only suggest done). Q5 **yes** (violet session tick on lanes). Q6 **yes** (GitHub as a block in Overview).

## Needed an answer before the spec

| # | Question | Proposed default | Source |
|---|---|---|---|
| Q1 | File watching needs **cgo** (macOS FSEvents). kqueue would need one open file per watched item (about 30,000 today, and growing). Is cgo in the build OK? | Yes: accept cgo for FSEvents, keep SQLite pure Go | 03 R1 |
| Q2 | Commit `.drashta/` to each project's git? | Text yes; snapshot images git-ignored by default | 04 §8.1 |
| Q3 | Snapshots from a URL need a headless browser. | **Answered: Safari through `safaridriver`; decide details later** | 04 §8.2 |
| Q4 | Does approving the latest snapshot move the task to **done**? | Only suggest it; the user moves it | 04 §8.4 |
| Q5 | Should Claude sessions also mark the seismograph lane (a small violet tick)? | Yes, a small tick on the day; details in the readout | 02 §9.1 |
| Q6 | GitHub (`gh`): a block in the project Overview, or its own tab? | A block in Overview | 02 §9.4 |

## Can be settled during the build

| # | Question | Proposed default | Source |
|---|---|---|---|
| Q7 | Is `?desktop=1` in the Plash URL OK as the "running as wallpaper" marker? | Yes | 02 §9.2 |
| Q8 | Server preview: a live frame of `localhost:<port>`, or the last capture plus Open ↗? | Last capture plus Open ↗ | 02 §9.3 |
| Q9 | Search prefixes beyond `:port` (`group:`, `kind:`)? | `:port` only in v1 | 02 §9.5 |
| Q10 | Task IDs can clash across git branches (both create `T-13`). | Accept for v1; add `drashta renumber` if it happens | 04 §8.3 |
| Q11 | How does the `drashta` CLI know which Claude session is calling it? | A Claude Code hook exports the session id; fallback: cwd plus the newest `.jsonl` | 04 §8.5 |
| Q12 | Line notes after the file is edited. | Re-anchor by matching text; show "line moved?" if that fails | 04 §8.6 |
| Q13 | Link servers by port or by a stable `cwd|cmd` key? | Store the stable key, show the port | 04 §8.7 |
| Q14 | Build tools (`npm install`, `vite build`) create thousands of file events and inflate "file changes". | Exclude skip-dirs and common build outputs from the counts | 03 R6 |

## To check on the real Mac (phase 5)

| # | Check | Source |
|---|---|---|
| C1 | Does Plash report the page as hidden when windows cover it? (This decides whether lsof can slow down.) | 03 R2 |
| C2 | Does the live connection survive sleep and long covered periods? | 03 R3 |
| C3 | Does Plash have its own Browsing Mode hotkey, and do desktop icons still work? | 05 |
| C4 | Opening a terminal with a command asks once for macOS Automation permission. | 03 R7 |
