# ADR 0003: SQLite is a cache and a history log, never the source of truth

- Status: Accepted (decision S7, 2026-09-30, reconfirmed 2026-10-01).
- Date: 2026-10-01

## Context

- Home, activity and search need fast answers across all projects. Asking git, fossil and the file system on every request is too slow.
- The real sources are already on disk: files, VCS history, Claude `.jsonl` files, and `.drashta/` markdown.
- A few facts cannot be rebuilt: file-change counts seen live from FSEvents, and server runs.

## Decision

- Keep one file, `~/.drashta/index.db`. Pure-Go driver, WAL mode, one writer goroutine and a read pool.
- Two kinds of tables:
  - **Cache (R):** projects, files, commits, sessions, tracker mirror, search. Rebuildable from the sources.
  - **History (H):** `server_runs`, `file_changes`, `daily_activity` (event columns), `sweep_log`, `settings`. Not rebuildable.
- Summary data comes from SQLite. Detail data (a diff, a file body, a transcript) is fetched live.
- `drashta -reindex` drops and recreates the cache tables and keeps history. Deleting the file is safe.
- Never store secrets or file bodies. Redact jsonl text before it is indexed.
- Mirror `settings` to `~/.drashta/settings.json`, so they survive a deleted database.

## Consequences

- Good: the app is safe to wipe. Home answers in under 50 ms.
- Good: full-text search (FTS5, trigram) comes with the same file.
- Cost: history is lost if the file is deleted (risk R4). Past days then come back from file modified times and are marked `source='mtime'`.
- Cost: cache invalidation. Each probe has a cache key and a re-run rule.
- Cost: a change to a history table needs a real migration. A change to a cache table can drop and rebuild.
