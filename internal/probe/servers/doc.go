// Package servers finds local TCP servers with lsof and remembers them.
//
// The code is copied from ledger (discover.go, history.go). Changes:
// each server gets a ProjectID (longest path prefix of its cwd), a Kind
// (dev or helper), and the history is kept through a HistoryStore instead of
// history.json.
//
// Rule from ledger scanOnce: when a scan fails, Scan returns an error. The
// caller keeps its previous snapshot and does not call History.Merge, so a
// failed lsof never marks every server dead.
//
// Exported API:
//
//	KindDev, KindHelper                 Kind values.
//	type Server                          One listening process (port, pid, name, cmd, cwd, projectId, kind, firstSeen, lastSeen, alive).
//	KindOf(name string) string           KindDev for dev runtimes (node, python*, go, ...), else KindHelper.
//	MatchProject(cwd, projects) string   ID of the project with the longest path prefix of cwd, "" if none ("other").
//	type Snapshot                        Result of one lsof pass: At and Servers.
//	type Scanner                         Runs passes and keeps the PID cache. Fields Listen, Enrich, Self, Now can be swapped in tests.
//	NewScanner() *Scanner                Scanner using the real lsof and ps (through runx), excluding os.Getpid().
//	(*Scanner).Scan(ctx, projects) (Snapshot, error)
//	Scan(ctx, projects) (Snapshot, error) One pass with a shared default Scanner.
//	Key(s Server) string                 History key "<cwd>|<cmd>" (or "pid:<pid>|<port>"). Used by restart.
//	RefKey(key string) string            Stable ref form "server-key:<sha1(key)[:10]>".
//	type Run                             One history row for the store (server_runs). Ended is zero while alive.
//	type HistoryStore                    Interface { Load() ([]Run, error); Save([]Run) error }.
//	type MemStore                        In-memory HistoryStore (tests; LoadErr simulates a bad store).
//	type History                         All servers ever seen, by key (max 50, oldest dead dropped first).
//	LoadHistory(st HistoryStore) *History  Loads from st; a failing store gives an empty history. st may be nil.
//	(*History).Merge(live, now) bool     Folds a live scan in; true when something meaningful changed.
//	(*History).Save() error              Writes all rows to the store (call when Merge returned true).
//	(*History).Runs() []Run              The rows Save would write, sorted by key.
//	(*History).Snapshot() (active, recent []Server)  Alive by port, dead newest first.
//	(*History).Lookup(key) (Server, bool)
//
// Typical loop (indexer):
//
//	snap, err := scanner.Scan(ctx, projects)
//	if err != nil { log; return }            // keep the old state
//	if hist.Merge(snap.Servers, snap.At) { hist.Save() }
//	actionsManager.SetLive(snap.Servers, snap.At)
package servers
