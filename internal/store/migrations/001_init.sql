-- 001_init: the first schema of ~/.drashta/index.db.
--
-- Source: docs/spec/04-data-model.md section 5. Column names and keys follow it.
-- The doc gives no column types, so types are chosen here (see store/doc.go).
--
-- Added on top of the doc:
--   * claude_file   named in 03-architecture.md section 3 (incremental jsonl parse state)
--   * sweep_log     named in 03-architecture.md section 5 (history of sweeps)
--   * search_ref    store internal: maps one FTS row to its (kind, project_id, ref),
--                   so the store can replace or delete one FTS row without a table scan
--   * a few extra indexes and the events_uniq index (makes AddEvent safe to repeat)
--   * a primary key on commit_files
--   * server_runs.kind and server_runs.last_seen (needed by the servers package, which keeps both)
--
-- Legend: R = cache, can be rebuilt from disk and tools. H = history, kept by Reindex.
-- The same split is written in tables.go. A test checks the two agree.
--
-- Time columns: INTEGER unix seconds (UTC) for observed data.
-- Authored data (milestones, tasks, snapshots, notes) keeps the text from the
-- front matter as written, for example 2026-09-30T12:02:00+05:30.
-- "end", "start" and "offset" are quoted because they are SQL words.

-- meta -------------------------------------------------------------- R
CREATE TABLE meta(
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

-- settings ---------------------------------------------------------- H* (also mirrored to settings.json)
CREATE TABLE settings(
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

-- observed cache ---------------------------------------------------- R
CREATE TABLE projects(
  id             TEXT PRIMARY KEY,
  name           TEXT NOT NULL DEFAULT '',
  grp            TEXT NOT NULL DEFAULT '',
  path           TEXT NOT NULL UNIQUE,
  hidden         INTEGER NOT NULL DEFAULT 0,
  vcs            TEXT NOT NULL DEFAULT '',      -- git | fossil | git,fossil | ''
  branch         TEXT NOT NULL DEFAULT '',
  remote         TEXT NOT NULL DEFAULT '',
  description    TEXT NOT NULL DEFAULT '',
  stack          TEXT NOT NULL DEFAULT '[]',    -- JSON array
  readme_path    TEXT NOT NULL DEFAULT '',
  last_change_at INTEGER,
  indexed_at     INTEGER
);

CREATE TABLE files(
  project_id TEXT NOT NULL,
  path       TEXT NOT NULL,
  size       INTEGER NOT NULL DEFAULT 0,
  mtime      INTEGER NOT NULL DEFAULT 0,
  kind       TEXT NOT NULL DEFAULT 'other',     -- code | doc | image | other
  lines      INTEGER NOT NULL DEFAULT 0,
  title      TEXT NOT NULL DEFAULT '',          -- docs: first H1
  PRIMARY KEY(project_id, path)
);
CREATE INDEX files_mtime ON files(project_id, mtime DESC);

CREATE TABLE dirty(
  project_id TEXT NOT NULL,
  path       TEXT NOT NULL,
  state      TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(project_id, path)
);

CREATE TABLE commits(
  project_id TEXT NOT NULL,
  id         TEXT NOT NULL,
  vcs        TEXT NOT NULL DEFAULT 'git',
  author     TEXT NOT NULL DEFAULT '',
  ts         INTEGER NOT NULL DEFAULT 0,
  msg        TEXT NOT NULL DEFAULT '',
  body       TEXT NOT NULL DEFAULT '',
  plus       INTEGER NOT NULL DEFAULT 0,
  minus      INTEGER NOT NULL DEFAULT 0,
  nfiles     INTEGER NOT NULL DEFAULT 0,
  branch     TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(project_id, id)
);
CREATE INDEX commits_ts ON commits(project_id, ts DESC);

CREATE TABLE commit_files(
  project_id TEXT NOT NULL,
  commit_id  TEXT NOT NULL,
  path       TEXT NOT NULL,
  plus       INTEGER NOT NULL DEFAULT 0,
  minus      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(project_id, commit_id, path)
);
CREATE INDEX cf_path ON commit_files(project_id, path);

CREATE TABLE sessions(
  id          TEXT PRIMARY KEY,
  profile     TEXT NOT NULL DEFAULT '',
  jsonl_path  TEXT NOT NULL DEFAULT '',
  cwd         TEXT NOT NULL DEFAULT '',
  project_id  TEXT NOT NULL DEFAULT '',          -- '' = unassigned
  "start"     INTEGER,
  "end"       INTEGER,
  title       TEXT NOT NULL DEFAULT '',
  turns       INTEGER NOT NULL DEFAULT 0,
  kb          INTEGER NOT NULL DEFAULT 0,
  tools       TEXT NOT NULL DEFAULT '{}',        -- JSON object: tool name -> count
  edited      TEXT NOT NULL DEFAULT '[]',        -- JSON array of paths
  jsonl_size  INTEGER NOT NULL DEFAULT 0,
  jsonl_mtime INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX sessions_end ON sessions(project_id, "end" DESC);

CREATE TABLE session_projects(
  session_id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT 'cwd',        -- cwd | mention | edit
  PRIMARY KEY(session_id, project_id)
);
CREATE INDEX sp_project ON session_projects(project_id);

CREATE TABLE servers_now(
  key        TEXT PRIMARY KEY,
  port       INTEGER NOT NULL DEFAULT 0,
  pid        INTEGER NOT NULL DEFAULT 0,
  name       TEXT NOT NULL DEFAULT '',
  cmd        TEXT NOT NULL DEFAULT '',
  cwd        TEXT NOT NULL DEFAULT '',
  project_id TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL DEFAULT 'dev',        -- dev | helper
  title      TEXT NOT NULL DEFAULT '',
  first_seen INTEGER,
  last_seen  INTEGER
);
CREATE INDEX servers_now_proj ON servers_now(project_id);

CREATE TABLE gh_cache(
  project_id TEXT NOT NULL,
  kind       TEXT NOT NULL,                      -- pr | issue | run
  number     INTEGER NOT NULL,
  title      TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT '',
  url        TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT '',
  fetched_at INTEGER,
  PRIMARY KEY(project_id, kind, number)
);

CREATE TABLE claude_file(
  path     TEXT PRIMARY KEY,
  size     INTEGER NOT NULL DEFAULT 0,
  mtime    INTEGER NOT NULL DEFAULT 0,           -- unix nanoseconds
  "offset" INTEGER NOT NULL DEFAULT 0,
  inode    INTEGER NOT NULL DEFAULT 0
);

-- authored mirror (parsed from .drashta/) --------------------------- R
CREATE TABLE milestones(
  project_id TEXT NOT NULL,
  id         TEXT NOT NULL,
  title      TEXT NOT NULL DEFAULT '',
  due        TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT 'open',
  body       TEXT NOT NULL DEFAULT '',
  file_rev   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(project_id, id)
);

CREATE TABLE features(
  project_id   TEXT NOT NULL,
  id           TEXT NOT NULL,
  title        TEXT NOT NULL DEFAULT '',
  milestone_id TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL DEFAULT '',
  file_rev     TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(project_id, id)
);

CREATE TABLE tasks(
  project_id TEXT NOT NULL,
  id         TEXT NOT NULL,
  title      TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'backlog',    -- backlog | todo | in_progress | review | done
  feature_id TEXT NOT NULL DEFAULT '',
  assignee   TEXT NOT NULL DEFAULT '',
  created    TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  updated    TEXT NOT NULL DEFAULT '',
  body       TEXT NOT NULL DEFAULT '',
  log        TEXT NOT NULL DEFAULT '',
  file_rev   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(project_id, id)
);
CREATE INDEX tasks_status ON tasks(project_id, status);

CREATE TABLE task_links(
  project_id TEXT NOT NULL,
  task_id    TEXT NOT NULL,
  ref        TEXT NOT NULL,                      -- canonical ref string
  source     TEXT NOT NULL DEFAULT 'manual',     -- manual | auto
  reason     TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(project_id, task_id, ref)
);
CREATE INDEX tl_ref ON task_links(ref);

CREATE TABLE snapshots(
  project_id  TEXT NOT NULL,
  task_id     TEXT NOT NULL,
  version     INTEGER NOT NULL,
  title       TEXT NOT NULL DEFAULT '',
  image_path  TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT '',
  captured    TEXT NOT NULL DEFAULT '',
  author      TEXT NOT NULL DEFAULT '',
  commit_id   TEXT NOT NULL DEFAULT '',
  verdict     TEXT NOT NULL DEFAULT 'pending',   -- pending | approved | changes
  reviewed_by TEXT NOT NULL DEFAULT '',
  reviewed_at TEXT NOT NULL DEFAULT '',
  review_note TEXT NOT NULL DEFAULT '',
  file_rev    TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(project_id, task_id, version)
);
CREATE INDEX snap_verdict ON snapshots(verdict);

CREATE TABLE notes(
  project_id  TEXT NOT NULL,
  id          TEXT NOT NULL,
  ref         TEXT NOT NULL DEFAULT '',
  x           REAL,
  y           REAL,
  line        INTEGER,
  anchor      TEXT NOT NULL DEFAULT '',
  author      TEXT NOT NULL DEFAULT '',
  created     TEXT NOT NULL DEFAULT '',
  resolved    INTEGER NOT NULL DEFAULT 0,
  resolved_at TEXT NOT NULL DEFAULT '',
  reply_to    TEXT NOT NULL DEFAULT '',
  body        TEXT NOT NULL DEFAULT '',
  file_rev    TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(project_id, id)
);
CREATE INDEX notes_ref  ON notes(ref);
CREATE INDEX notes_open ON notes(resolved, created DESC);

-- events ------------------------------------------------------------ R
-- kind: commit | session_start | session_end | server_up | server_down | file_burst
--       | task_created | task_status | snapshot | verdict | note
CREATE TABLE events(
  id         INTEGER PRIMARY KEY,
  project_id TEXT NOT NULL,
  ts         INTEGER NOT NULL,
  day        TEXT NOT NULL,                      -- YYYY-MM-DD, local
  kind       TEXT NOT NULL,
  ref        TEXT NOT NULL DEFAULT '',
  label      TEXT NOT NULL DEFAULT '',
  meta       TEXT NOT NULL DEFAULT '{}'          -- JSON
);
CREATE INDEX events_day  ON events(day, project_id);
CREATE INDEX events_proj ON events(project_id, ts DESC);
CREATE UNIQUE INDEX events_uniq ON events(project_id, kind, ref, ts);

-- history ----------------------------------------------------------- H
CREATE TABLE server_runs(
  id         INTEGER PRIMARY KEY,
  key        TEXT NOT NULL DEFAULT '',
  project_id TEXT NOT NULL DEFAULT '',
  port       INTEGER NOT NULL DEFAULT 0,
  pid        INTEGER NOT NULL DEFAULT 0,
  name       TEXT NOT NULL DEFAULT '',
  cmd        TEXT NOT NULL DEFAULT '',
  cwd        TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL DEFAULT 'dev',        -- dev | helper (added at the lead's request, not in 04)
  started    INTEGER NOT NULL DEFAULT 0,
  ended      INTEGER,                            -- NULL = still running
  last_seen  INTEGER                             -- last scan that saw it (added at the lead's request, not in 04)
);
CREATE INDEX sr_proj ON server_runs(project_id, started);
CREATE INDEX sr_key  ON server_runs(key);

CREATE TABLE file_changes(
  project_id TEXT NOT NULL,
  day        TEXT NOT NULL,
  path       TEXT NOT NULL,
  n          INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(project_id, day, path)
);

-- daily_activity is mixed (see 04 section 5). Reindex keeps it whole;
-- RecomputeDaily rebuilds the derivable columns from the R tables.
CREATE TABLE daily_activity(
  project_id      TEXT NOT NULL,
  day             TEXT NOT NULL,
  files           INTEGER NOT NULL DEFAULT 0,
  commits         INTEGER NOT NULL DEFAULT 0,
  lines_plus      INTEGER NOT NULL DEFAULT 0,
  lines_minus     INTEGER NOT NULL DEFAULT 0,
  sessions        INTEGER NOT NULL DEFAULT 0,
  session_minutes INTEGER NOT NULL DEFAULT 0,
  snapshots       INTEGER NOT NULL DEFAULT 0,
  notes           INTEGER NOT NULL DEFAULT 0,
  server_minutes  INTEGER NOT NULL DEFAULT 0,
  tasks_moved     INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(project_id, day)
);
CREATE INDEX da_day ON daily_activity(day);

CREATE TABLE sweep_log(
  id    INTEGER PRIMARY KEY,
  ts    INTEGER NOT NULL,
  scope TEXT NOT NULL DEFAULT '',                -- all | project id | claude | ...
  ms    INTEGER NOT NULL DEFAULT 0,
  ok    INTEGER NOT NULL DEFAULT 1
);

-- search ------------------------------------------------------------ R
-- Trigram: paths and code fragments match in the middle of a word.
CREATE VIRTUAL TABLE search USING fts5(
  title, body, kind UNINDEXED, project_id UNINDEXED, ref UNINDEXED, ts UNINDEXED,
  tokenize = 'trigram'
);

CREATE TABLE search_ref(
  kind       TEXT NOT NULL,
  project_id TEXT NOT NULL,
  ref        TEXT NOT NULL,
  fts_rowid  INTEGER NOT NULL,
  PRIMARY KEY(kind, project_id, ref)
);
