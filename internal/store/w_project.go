package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Project is one row of the projects table.
// ID is an opaque string for the store. By the project rule it is the folder name, for example "ledger",
// or "<group>.<name>" when two groups hold the same name. It never contains a slash.
type Project struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Group        string    `json:"group"`
	Path         string    `json:"path"`
	Hidden       bool      `json:"hidden"`
	VCS          string    `json:"vcs"` // "git", "fossil", "git,fossil" or ""
	Branch       string    `json:"branch"`
	Remote       string    `json:"remote"`
	Description  string    `json:"description"`
	Stack        []string  `json:"stack"`
	ReadmePath   string    `json:"readmePath"`
	LastChangeAt time.Time `json:"lastChangeAt"`
	IndexedAt    time.Time `json:"indexedAt"`
}

// UpsertProject inserts the project or updates every column of an existing one.
func (d *DB) UpsertProject(ctx context.Context, p Project) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return UpsertProjectTx(ctx, tx, p) })
}

// UpsertProjectTx is UpsertProject inside a transaction you already have.
func UpsertProjectTx(ctx context.Context, tx *sql.Tx, p Project) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO projects(id, name, grp, path, hidden, vcs, branch, remote, description, stack,
                     readme_path, last_change_at, indexed_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name, grp = excluded.grp, path = excluded.path, hidden = excluded.hidden,
  vcs = excluded.vcs, branch = excluded.branch, remote = excluded.remote,
  description = excluded.description, stack = excluded.stack, readme_path = excluded.readme_path,
  last_change_at = excluded.last_change_at, indexed_at = excluded.indexed_at`,
		p.ID, p.Name, p.Group, p.Path, b2i(p.Hidden), p.VCS, p.Branch, p.Remote, p.Description,
		jsonText(p.Stack, "[]"), p.ReadmePath, unixOrNil(p.LastChangeAt), unixOrNil(p.IndexedAt))
	return err
}

// projectTables are the cache tables that have a project_id column.
// DeleteProject clears them. (sessions and servers_now are handled on their own.)
var projectTables = []string{
	"files", "dirty", "commits", "commit_files", "gh_cache",
	"milestones", "features", "tasks", "task_links", "snapshots", "notes", "events",
	"session_projects", "search_ref", "daily_commits",
}

// DeleteProject removes a project and all its cache rows, including its search rows.
// Sessions of the project are kept but become unassigned (project_id = ”). History tables are kept.
func (d *DB) DeleteProject(ctx context.Context, id string) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		// Search rows first: search_ref tells us which FTS rows belong to the project.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM search WHERE rowid IN (SELECT fts_rowid FROM search_ref WHERE project_id = ?)`, id); err != nil {
			return err
		}
		for _, t := range projectTables {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+t+" WHERE project_id = ?", id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET project_id = '' WHERE project_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE servers_now SET project_id = '' WHERE project_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
		return err
	})
}

// placeholders returns "?,?,?" for n values.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
