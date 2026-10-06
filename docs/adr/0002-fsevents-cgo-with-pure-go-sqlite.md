# ADR 0002: FSEvents through cgo, SQLite in pure Go

- Status: Accepted (decision A1, 2026-10-01).
- Date: 2026-10-01

## Context

- drashta must see file changes in all of `~/projects`. Measured on 2026-10-01: 161,898 files and 24,792 folders. After skip dirs: 24,480 files and 5,581 folders, about 30 thousand watch items. The number only grows.
- The pure-Go watcher (`fsnotify`) uses kqueue on macOS. kqueue needs one open file descriptor for every watched file and folder.
- A launchd job starts with a soft limit of 256 open files, and `kern.maxfilesperproc` is 92,160. kqueue would break as the folder grows.
- S7 asks for a pure-Go SQLite driver so the database layer needs no C build.

## Decision

- Watch files with **FSEvents**, through `github.com/fsnotify/fsevents` (cgo, macOS only).
  - One recursive stream over `~/projects` (latency 1 s, file-level events).
  - One stream over the five `~/.claude*/projects` folders (latency 2 s).
- Keep SQLite on `modernc.org/sqlite` (pure Go).
- Put the watcher behind an interface (`internal/indexer/watch.go`). Tests use a fake. A kqueue fallback stays possible.

## Consequences

- Good: no file-count limit. The same cost for 30 thousand or 3 million files.
- Good: tests need no real file system events.
- Cost: the build needs a C toolchain (Xcode command line tools) and `CGO_ENABLED=1`. The binary is macOS only.
- Cost: FSEvents can coalesce or drop events. `MustScanSubDirs` triggers a full rescan of that project, and the hourly sweep is the safety net.
- Note: because the binary uses cgo anyway, a cgo SQLite driver was possible. Pure Go was kept to match S7: no second C dependency, easy tests, one file on disk.
