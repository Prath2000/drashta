// Package store is the SQLite index of drashta: ~/.drashta/index.db (modernc.org/sqlite, pure Go).
//
// The database is a cache plus a history log (decision S7). It is never the source of truth.
// Deleting it is safe. Only the history tables are lost.
//
// # How it works
//
//   - Open(dataDir) opens <dataDir>/index.db, runs PRAGMA quick_check, and applies the embedded
//     migrations (migrations/NNN_name.sql, tracked by PRAGMA user_version).
//   - If the file is not a database, it is renamed to index.db.corrupt-<timestamp> (with its -wal and -shm),
//     a fresh one is made, and one warning is logged. Recovered() returns the new name of the old file.
//   - Pragmas on every connection: journal_mode=WAL, synchronous=NORMAL, foreign_keys=ON, busy_timeout=5000.
//   - ONE writer goroutine runs all writes, one transaction per Write call. Reads use a pool (Read()).
//     The pool is query_only, so a write through Read() fails.
//   - Every typed write helper has two forms: a method on *DB (own transaction) and a function with a
//     "Tx" suffix that runs inside a transaction you already have (use it inside db.Write to group
//     several changes into one commit, as the indexer does per job).
//   - Anything that changes searchable text also changes the FTS5 table "search" in the same transaction.
//
// # Conventions
//
//   - Project id is an opaque string for the store. By the project rule it is the folder name
//     ("ledger"), or "<group>.<name>" on a clash. It never has a slash.
//   - Observed times are unix seconds (UTC) in INTEGER columns. claude_file.mtime is unix nanoseconds.
//     The Go API uses time.Time. The zero time means "unknown" (NULL or 0).
//   - Authored times (tasks, notes, snapshots, milestones) are kept as the text from the front matter.
//   - A "day" is a local date string YYYY-MM-DD (DayLayout). Anything that needs the zone takes a *time.Location
//     (nil = time.Local).
//   - Refs in the search table are local ref strings: "file:src/x.go", "commit:abc1234", "session:<uuid>",
//     "task:T-12", "feature:F-3", "milestone:M-1", "note:N-40". Search kinds: file, doc, commit, session,
//     task, feature, milestone, note.
//   - The tables are listed twice in tables.go: R (cache, dropped by Reindex) and H (history, kept).
//     A test checks the lists against the migration.
//
// # Exported API
//
// Opening and writing:
//
//	Open(dataDir string) (*DB, error)                       open, check, migrate; recover from a corrupt file;
//	                                                         dataDir 0700, index.db (+ -wal/-shm) 0600
//	(*DB).Close() error                                      finish queued writes, checkpoint, close (safe twice)
//	(*DB).Read() *sql.DB                                     read pool (query_only)
//	(*DB).Write(ctx, fn func(*sql.Tx) error) error           run fn in one transaction on the writer goroutine
//	(*DB).Recovered() string                                 path of the moved-aside corrupt file, or ""
//	(*DB).Path() string / Dir() string                       index.db path / data dir
//	(*DB).Reindex(ctx) error                                 drop and recreate all R tables + FTS, keep H tables
//	LatestSchemaVersion() int                                number of the last migration
//	RTables() []string / HTables() []string                  the cache list / the history list
//	ErrClosed, ErrNewerSchema                                sentinel errors
//
// Search:
//
//	(*DB).Search(ctx, q, project, kind string, limit int) ([]Hit, error)   MATCH + bm25, then newest first; safe for any input
//	MatchExpr(q string) string                               the sanitiser: quoted words, 3 characters or more, AND
//	(*DB).FTSReplace(ctx, kind, ref, project, title, body string, ts time.Time) error
//	(*DB).FTSDelete(ctx, kind, ref, project string) error
//	FTSReplaceTx / FTSDeleteTx                               same, inside your transaction
//
// Observed data (cache):
//
//	(*DB).UpsertProject(ctx, Project) error                  insert or update a project row
//	(*DB).DeleteProject(ctx, id) error                       remove a project and all its cache and search rows
//	(*DB).ReplaceFiles(ctx, project, []File) error           files + search rows; only changed files touch FTS
//	(*DB).ReplaceCommits(ctx, project, []probe.Commit) error commits + commit_files + FTS; keeps older commits
//	OldestPerVCS(commits) map[vcs]int64                      the cut-off of each VCS's capped log
//	CommitGone(oldest, vcs, ts) bool                         stored commit not in the list is gone (ts > oldest[vcs])
//	(*DB).ReplaceDirty(ctx, project, []probe.DirtyFile) error
//	(*DB).UpsertSessions(ctx, []Session) error              sessions + FTS (Session.Text is search-only)
//	(*DB).SetSessionProjects(ctx, sessionID, []SessionProject) error   cwd / mention / edit links
//	(*DB).DeleteSession(ctx, id) error
//	(*DB).ReplaceServersNow(ctx, []ServerNow) error          replaced on every lsof pass
//	(*DB).GetClaudeFile(ctx, path) (ClaudeFile, bool, error) incremental parse state
//	(*DB).PutClaudeFile(ctx, ClaudeFile) error
//	(*DB).DeleteClaudeFile(ctx, path) error
//
// Authored mirror (cache of .drashta/ files):
//
//	(*DB).UpsertAuthoredMilestone / UpsertAuthoredFeature / UpsertAuthoredTask / UpsertAuthoredSnapshot / UpsertAuthoredNote
//	(*DB).UpsertAuthoredTaskLinks(ctx, project, taskID, source, []TaskLink) error   replaces the links of one source
//	(*DB).DeleteAuthored(ctx, project, kind, id) error       kind: milestone|feature|task|snapshot|note
//
// History and activity:
//
//	(*DB).AddEvent(ctx, Event) (added bool, err error)       idempotent on (project, kind, ref, ts)
//	(*DB).AddFileChanges(ctx, project, day, paths []string) error
//	(*DB).RecomputeDaily(ctx, project, day string, loc *time.Location) error   one daily_activity row
//	    daily_activity is history: commits/lines (without a daily_commits row) and sessions never
//	    go below the stored value, so a Reindex or deleted .jsonl files cannot lower an old day.
//	(*DB).SetDailyCommitStats(ctx, project, from, to string, map[day]probe.DayCount, loc) (changed []string, err error)
//	SetDailyCommitStatsTx(ctx, tx, ...)                       same inside a transaction (q_daystats.go)
//	ClearDailyCommitStatsTx(ctx, tx, project, loc) ([]string, error)   drop a project's exact rows
//	(*DB).DailyCommitStats(ctx, project, from, to string) (map[string]probe.DayCount, error)
//	    Exact per-day commit numbers (table daily_commits, R, migration 002). RecomputeDaily takes
//	    commits/lines_plus/lines_minus from daily_commits when the day has a row there, else from
//	    the (capped) commit rows, never lower than stored. Missing days in from..to are stored as zero.
//	AddDays(day string, n int) (string, error)               calendar math on a day key (DST safe)
//	DayStart(day string, loc) (time.Time, error)             first instant of a local day (also where
//	                                                         midnight is skipped by a DST change)
//	IdxCommitTimesVCSTx(ctx, tx, project) (map[id]IdxCommitTime, error)   stored (vcs, ts) per commit
//	(*DB).IdxUnassignedSessions(ctx) ([]IdxUnassignedSession, error)      sessions with project_id ''
//	IdxForgetClaudeFamilyTx(ctx, tx, mainPath) error         drop claude_file rows of one session family
//	(*DB).HomeBootCounts / IdxCounts                         top-bar counts; awaitingReview and openTasks
//	                                                         use the Tracker's rule, hidden projects left out
//	(*DB).AppendServerRun(ctx, ServerRun) (id int64, err error)
//	(*DB).EndServerRun(ctx, id int64, ended time.Time) error
//	(*DB).ListServerRuns(ctx) ([]ServerRun, error)           oldest first
//	(*DB).ReplaceServerRuns(ctx, []ServerRun) error          whole history; ServerRun has Kind and LastSeen
//	(*DB).AddSweepLog(ctx, SweepLog) error
//	(*DB).RecentSweeps(ctx, limit) ([]SweepLog, error)
//
// Settings and meta:
//
//	(*DB).GetSetting / SetSetting / DeleteSetting / Settings  settings table, mirrored to <dataDir>/settings.json
//	(*DB).SettingsPath() string
//	(*DB).GetMeta / SetMeta                                  meta table (cache)
//
// Typed reads for the API (more are added by the API agents in internal/store/q_*.go):
//
//	(*DB).Home(ctx, rangeDays int, loc *time.Location) (HomeData, error)   seismograph lanes x days
//	(*DB).HomeAt(ctx, rangeDays int, loc *time.Location, at time.Time) (HomeData, error)   same, with an explicit "now"
//	(*DB).Day(ctx, date, project string) (DayData, error)                  events, totals, top changed files
//	(*DB).ProjectList(ctx) ([]Project, error)
//
// Types: DB, Project, File, Session, SessionProject, ServerNow, ServerRun, ClaudeFile, Milestone, Feature,
// Task, TaskLink, Snapshot, Note, Event, Hit, HomeData, Lane, Live, DayCell, DayData, DayFile, SweepLog.
// Constants: DBFileName, SettingsFileName, DayLayout, MaxBody (64 KB cap for one search body),
// LiveSessionWindow (5 minutes: a session is "live" when its .jsonl was written that recently).
//
// # Notes for the wiring and indexer code
//
//   - Write blocks until the transaction is done. fn must not call Write (it would wait on itself).
//     If ctx ends before fn starts, fn never runs. Use the Tx functions inside fn.
//   - Reindex empties the cache. The caller then runs a full sweep. claude_file is a cache table,
//     so every .jsonl is parsed again from the start. Events of kind server_*/file_burst lived in the
//     dropped events table and must be derived again from server_runs / file_changes if the UI shows them.
//   - ReplaceCommits does NOT recompute daily_activity. Call RecomputeDaily for each day you touched.
//   - Home returns hidden projects too (Lane.Hidden). Lane order is last change first.
//     Live servers are the "dev" kind only.
//   - Other code may read through Read(), but must write only through Write.
package store
