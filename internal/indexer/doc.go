// Package indexer keeps the SQLite index (internal/store) in step with the disk
// (03-architecture.md section 4). It runs on change, not on a timer: file events
// from FSEvents go through a router and a debounced job queue to the probes, the
// results are written through the store helpers, and small events go to the SSE hub.
// A full sweep runs at startup (in the background) and every hour.
//
// # Exported API: scheduler and jobs (indexer.go, queue.go, route.go, sweep.go, jobs_*.go, publish.go)
//
//	type Config struct            Cfg, DB (required); Hub, Watcher, Activity, Clock, Loc, Exec (Probes),
//	                              Logger, AutoLink, NoAutoLink, GH, Workers, Debounce, StormCap,
//	                              SweepEvery, LsofVisible, LsofHidden, NoServers. Zero values = defaults.
//	type Probes struct            the probe functions (Discover, Describe, Walk, Docs, GitKey, GitScan,
//	                              FossilDetect, FossilKey, FossilScan, GitDayStats, FossilDayStats,
//	                              Servers, ClaudeDiscover, ClaudeUsers);
//	                              nil fields use the real probes. Tests swap single ones.
//	type Clock interface          Now(), After(d). RealClock() is the wall clock.
//	type GHFunc func(ctx, p, remote, branch string, force bool)
//	                              gh hook: called on Refresh(withGH), RefreshAll (projects changed in 7 days)
//	                              and when a push is seen in .git/refs/remotes. Must not block for long.
//	type PartStatus struct        {StaleSince, Error, Unavailable} for one part of a project
//	func New(cfg Config) *Indexer
//	func (*Indexer) Start(ctx) error            starts everything and returns at once (never waits for the sweep)
//	func (*Indexer) Wait()                      after ctx is cancelled: wait for all goroutines, close the watcher
//	func (*Indexer) Stop()                      cancel + Wait
//	func (*Indexer) FirstSweepDone() <-chan struct{}
//	func (*Indexer) Refresh(ctx, projectID string, withGH bool) error   rescan one project now (async)
//	func (*Indexer) RefreshAll(ctx) error       full sweep now (async), gh for recently changed projects
//	func (*Indexer) ReindexAuthored(projectID, relPath string) error
//	                                            update one .drashta file now (sync); for the tracker API
//	func (*Indexer) ScanServersNow(ctx) error   one lsof pass now (for the servers API after kill/restart)
//	func (*Indexer) Status(projectID) map[string]PartStatus   parts: tree, git, fossil, commits, authored, full
//	func (*Indexer) Counts(ctx) (live.Counts, error)          numbers for the hello event
//	func (*Indexer) ServerHistory() *servers.History          alive and recent servers (restart keys)
//	func (*Indexer) Projects() []probe.Project
//	func NewHistoryStore(db *store.DB) servers.HistoryStore   server_runs as a servers.HistoryStore
//	const DayStatsWindow (35 days of exact per-day commit numbers)
//	const DefaultDebounce (750 ms), DefaultStormCap (5 s), DefaultWorkers (4), DefaultSweepEvery (1 h),
//	      DefaultLsofVisible (5 s), DefaultLsofHidden (60 s)
//
// # Exported API: file watching and activity (watch.go, watch_fsevents.go, activity.go)
//
//	type WatchKind int                 Create | Modify | Remove | Rename | MustScan
//	                                   (also named WatchCreate, WatchModify, WatchRemove, WatchRename, WatchMustScan)
//	type WatchEvent struct{ Path string; Kind WatchKind; Time time.Time }
//	type Watcher interface{ Start(ctx, roots []string) (<-chan WatchEvent, error); Close() error }
//	type SkipConfigurer interface{ SetExtraSkip(names []string) }   call before Start
//	func NewFSEventsWatcher(latencies map[string]time.Duration) Watcher   real watcher (macOS, cgo)
//	func NewFakeWatcher() *FakeWatcher                                    tests: Send, Roots, Close, Start
//	func IsSkippedPath(rel string, extra []string) bool                   activity rule
//	func IsIgnoredWatchPath(rel string, extra []string) bool              watcher rule (.git allow-list)
//	type Activity struct; func NewActivity(db, loc, now) *Activity
//	    Record, Flush, Backfill, Run, Count, SetOnFlush, SetExtraSkip
//	See the comment block at the top of watch.go for the watcher's promises.
//
// # How it works
//
//   - Routing (route.go, 03 section 4 table): a skip dir is ignored; .git/{HEAD,index,refs/**,
//     packed-refs,FETCH_HEAD} -> vcs job; .git/worktrees/<n>/{HEAD,index} -> the worktree's vcs job; .fslckout / *.fossil / the fossil repo file -> vcs job;
//     .drashta/** -> authored job for that file; any other file -> tree job, a dirty-list refresh
//     and Activity.Record (not for folders, not for hidden projects); a group folder or its direct
//     child -> project discovery; <profile>/projects/**.jsonl -> that session's family;
//     MustScan -> a full rescan of that project, the Claude transcripts, or a full sweep.
//   - Queue (queue.go): jobs are keyed by (project, kind) and merged. A job runs 750 ms after its
//     last event, but at most 5 s after its first one, so a storm gives one run every 5 s. A full
//     job absorbs pending tree/vcs/authored jobs of its project. 4 workers. One job per project at
//     a time; git and fossil run inside one vcs job, so they are serialised. Claude jobs share one
//     bucket. Refresh and sweep jobs are "immediate" (no debounce).
//   - Jobs: projects (Discover + Describe, removed projects deleted only when discovery had no
//     error), tree (+ docs only for new or changed doc files; hidden projects never walked),
//     vcs (cache keys skip work; the dirty list is refreshed on file events even when the key is
//     unchanged; git + fossil commits are written together; commits that disappear after a
//     rebase/amend lose their rows, events and search rows), authored (mirror + FTS + events:
//     task_created, task_status, snapshot, verdict, note), claude (Discover/Group, newest first,
//     ParseFamily with per-file state in claude_file, append parses continue from memory or the
//     stored row; the same session id under two profiles: see below;
//     session_projects cwd + mention rows; session_start / session_end events), servers (lsof
//     loop, 5 s while a page is visible, else 60 s; a failed scan keeps the old state;
//     servers_now, server_runs as real intervals, server_up/down events).
//   - Exact per-day commits (daystats in jobs_vcs.go): after a vcs job whose commits changed,
//     and once per local day per project, git.DayStatsAt (+ fossil.DayStatsAt counts, fossil
//     lines from the capped commit list) is read for the last DayStatsWindow (35) days and
//     written with store.SetDailyCommitStatsTx, so the seismograph does not depend on the
//     200-commit cap. A failure keeps the old rows (logged once). A project that is no longer
//     a repo gets ClearDailyCommitStatsTx. Probes.GitDayStats / FossilDayStats swap them in tests.
//   - Servers: on a cold start (no cached projects) the lsof loop waits for the first project
//     discovery; whenever discovery adds or removes projects the loop scans again at once
//     (kickServers), so servers_now.project_id is right without waiting for the next tick.
//   - After every change the touched days get store.RecomputeDaily. Auto task links come from
//     internal/autolink after commits, sessions or .drashta files change, and once more at the
//     end of each sweep (cross-project links need the other project's tasks).
//   - Failures (03 section 10): a probe error keeps the cached data and sets PartStatus.StaleSince;
//     a missing tool sets PartStatus.Unavailable; a panic is recovered per job and logged with the
//     project id; the same error is logged once per project per hour. A panic in auto-linking
//     skips that project; a panic in a sweep ends that sweep only; the activity and lsof loops are
//     restarted after a panic (guard). None of them can stop the process.
//   - Write order (jobs_vcs.go): the new cache keys, commits, dirty list and branch go into memory
//     only after the DB write worked, so a failed write is retried by the next run. Every job
//     write checks inside its transaction that the project is still known, so a job that ends
//     after its project was removed writes nothing (no rows with project id "").
//   - Hidden flag: every job reads it again from the config hide list (Config.IsHidden), so a
//     hide or unhide made in Settings is never written back by a stale job; showing a project
//     again walks it on the next full job, also when it was walked before.
//   - Linked worktrees: a project whose .git is a "gitdir: <main>/.git/worktrees/<n>" file gets
//     the events of <main>/.git/worktrees/<n>/{HEAD,index} (read once per project).
//   - Sessions with no project are resolved again (fresh parse) when discovery adds a project.
//     The same session id in two profiles: more turns wins, then the newer .jsonl, then the
//     smaller path (a strict order, so sweeps never flip between them).
//   - Sweep requests that arrive while a sweep runs are merged into ONE extra sweep.
//   - Day loops step day keys (store.AddDays), never local midnights, so zones that skip
//     midnight on a DST day (America/Santiago) cannot repeat a day or loop.
//   - Each sweep first calls store.RebuildHistoryEvents (server_up/down and file_burst events come
//     back after a Reindex), and back-fills file activity from mtimes since the last sweep.
//
// Events published: project (files, docs, commits, dirty, sessions), activity, servers,
// tracker, scan (running/done, with projectId for a project refresh), counts, resync
// (when the project list changed).
package indexer
