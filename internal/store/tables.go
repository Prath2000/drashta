package store

// Table kinds. This is the one list that says which table is cache and which is history.
// The migration (migrations/*.sql) creates the tables. A test checks that these two lists
// together match the tables in the migrated database, exactly.
//
//	R = cache. Can be rebuilt from disk and tools. Reindex drops and recreates it.
//	H = history. Cannot be rebuilt. Reindex keeps it.

// rTables are the cache tables. Reindex drops and recreates every one of them.
// "search" is the FTS5 table. "search_ref" is its helper (see fts.go).
var rTables = []string{
	"meta",
	"projects",
	"files",
	"dirty",
	"commits",
	"commit_files",
	"sessions",
	"session_projects",
	"servers_now",
	"gh_cache",
	"claude_file", // R on purpose: after Reindex the jsonl files must be parsed again from zero.
	"milestones",
	"features",
	"tasks",
	"task_links",
	"snapshots",
	"notes",
	"events",
	"daily_commits", // exact per-day commit numbers (migration 002); the indexer writes them again
	"search",
	"search_ref",
}

// hTables are the history tables. Reindex never touches them.
//
// daily_activity is mixed (04 section 5): some columns can be recomputed, "files" cannot.
// It is kept whole, and RecomputeDaily refreshes the derivable columns after a rebuild.
// settings is also mirrored to settings.json, so it survives a deleted database.
var hTables = []string{
	"settings",
	"server_runs",
	"file_changes",
	"daily_activity",
	"sweep_log",
}

// ftsShadowTables are the hidden tables SQLite creates for the FTS5 table "search".
// They belong to "search". They are not listed in rTables, and Reindex does not touch them
// by name (dropping "search" removes them).
var ftsShadowTables = []string{
	"search_data",
	"search_idx",
	"search_content",
	"search_docsize",
	"search_config",
}

// RTables returns a copy of the list of cache tables.
func RTables() []string { return append([]string(nil), rTables...) }

// HTables returns a copy of the list of history tables.
func HTables() []string { return append([]string(nil), hTables...) }
