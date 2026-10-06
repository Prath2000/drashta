package store

// Queries for the wiring (internal/app). Names start with "Wiring".
//
// gh_cache holds ONE row per project for the GitHub probe's cache (internal/probe/gh):
// project_id = project, kind = gh.StoreKind ("result"), number = 0, title = the JSON
// of gh.Result, fetched_at = unix seconds. The table's pr|issue|run columns are not used.

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// WiringGHGet reads the cached blob of one project and kind. ok is false when there is no row.
func (d *DB) WiringGHGet(ctx context.Context, project, kind string) (data []byte, at time.Time, ok bool, err error) {
	var title string
	var fetched sql.NullInt64
	err = d.Read().QueryRowContext(ctx,
		`SELECT title, fetched_at FROM gh_cache WHERE project_id = ? AND kind = ? AND number = 0`,
		project, kind).Scan(&title, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	if fetched.Valid && fetched.Int64 > 0 {
		at = time.Unix(fetched.Int64, 0)
	}
	return []byte(title), at, true, nil
}

// WiringGHPut stores (replaces) the cached blob of one project and kind.
func (d *DB) WiringGHPut(ctx context.Context, project, kind string, data []byte, at time.Time) error {
	var fetched any
	if !at.IsZero() {
		fetched = at.Unix()
	}
	return d.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO gh_cache(project_id, kind, number, title, fetched_at) VALUES(?, ?, 0, ?, ?)`,
			project, kind, string(data), fetched)
		return err
	})
}
