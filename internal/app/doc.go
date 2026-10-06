// Package app wires drashta together: config, the SQLite index, the live hub, the
// indexer (FSEvents watcher, activity counts, lsof loop, sweeps), the GitHub cache, the
// resolver, the actions manager and every HTTP route. cmd/drashta calls it.
// Rules come from 03-architecture.md sections 2, 6 and 9.
//
// # Exported API
//
//	const DefaultPort = 4455
//	var ErrAlreadyRunning                     Run found our own drashta on the port (main exits 0)
//	type Options struct                       Port, Addr, Dev, DataDir, ConfigPath, Logger, LogStderr,
//	                                          Static, Exec, Probes, NoServers, Debounce, Ready, Started
//	func Run(ctx, Options) error              start everything, block until ctx ends, shut down cleanly
//	func Reindex(ctx, Options, project string, out io.Writer) error
//	                                          drop the cache (all, or one project's rows), one full sweep, return
//
// # What Run does, in order
//
//  1. Single instance: httpapi.AlreadyRunning on the address (skipped for port 0).
//  2. Data folder 0700 and drashta's files in it 0600 (fixed at start for an older install).
//     Log file <DataDir>/drashta.log (slog text, 0600), rotated in-process at 10 MB, 3 old files kept.
//     launchd's own stdout/stderr go to <DataDir>/launchd.log (in.drashta.plist), never drashta.log.
//  3. config.Load (creates config.toml with defaults), store.Open (migrations, corrupt-file recovery).
//  4. Tool check (git, fossil, gh): a missing tool is logged; its probe reports "unavailable".
//  5. live.Hub; indexer.New with the FSEvents watcher (projects dir 1 s, each <profile>/projects 2 s,
//     config skip names), Activity, the gh hook, and a wrapped lsof probe that also feeds
//     actions.Manager.SetLive after every good scan.
//  6. gh.Cache backed by the gh_cache table (store.WiringGHGet/Put), Disabled = !gh_enabled,
//     errors logged once per project per hour.
//  7. httpapi.New + RegisterHome, RegisterProject, RegisterTracker. One counts function
//     (Indexer.Counts = store.IdxCounts = HomeBootCounts) feeds /api/boot, hello and counts events.
//     The editor is read only from config.toml (config.ValidateEditor at load; an invalid one
//     falls back to the default with one warning). PATCH /api/settings {editorCmd} is refused.
//  8. Listen + Serve (the page renders from the cache at once), then Indexer.Start
//     (watchers, lsof loop, background sweep).
//  9. On ctx end: HTTP Shutdown (ends SSE), Indexer.Stop, wait for gh fetches,
//     Activity.Flush, hub close, DB close.
package app
