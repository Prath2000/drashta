# ADR 0004: `.drashta/` markdown with YAML front matter is the authored source of truth

- Status: Accepted (decisions S9 and A2).
- Date: 2026-10-01

## Context

- Tracker data (milestones, features, tasks, snapshots, notes) is written by the user and by Claude sessions.
- It must be easy to read, diff, grep and version. It must survive a deleted cache. Claude must be able to read and edit it.

## Decision

- Each project keeps `.drashta/{milestones,features,tasks,snapshots,notes}/<id>.md`. One entity per file. YAML front matter plus a markdown body.
- IDs are a prefix and a number per project (`T-12`). A writer creates the file with `O_CREAT|O_EXCL`, so two writers never get the same ID. IDs are never reused. Deleted files move to `.drashta/.trash/`.
- Writes patch fields and keep unknown keys. They write a temp file in the same folder, then rename. The server sends and checks a `rev` (hash of the file) and answers 409 on a conflict.
- Some zones are append-only: the task log, the `links:` list, notes (reply with a new note), and snapshot versions.
- The CLI (`drashta task|note|snap`) applies the ownership rules for Claude sessions.
- A2: commit the text files to the project's git. Git-ignore snapshot images by default through `.drashta/.gitignore`.
- SQLite holds a mirror for fast queries. Auto links (derived from commits and sessions) live only in SQLite.

## Consequences

- Good: plain files that humans, git and Claude can all use. The data survives a cache wipe.
- Good: the CLI works with the server stopped.
- Cost: two branches can both create `T-13` (accepted for v1, Q10).
- Cost: concurrent edits need the `rev` check (risk R8).
- Cost: drashta writes inside project folders. This is the only place it writes there. `.drashta/README.md` explains the rules to anyone who opens the folder.
- Cost: snapshot images can grow a repo if the ignore rule is removed.
