package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// This file holds the queries for the per-day file activity (the seismograph file bars).
// It belongs to the indexer's activity tracker (internal/indexer/activity.go).
//
// The table file_changes has one row per (project, day, path). It has no "source" column,
// so the source of a row is kept in the counter n:
//
//	n >= 1   the row comes from live FSEvents events ("events"). n counts the events that were flushed.
//	n == 0   the row was made by the mtime backfill ("mtime"). We only know the file changed on that day.
//
// Rules:
//   - A live event on a path that has an "mtime" row upgrades it to "events" (n becomes the event count).
//   - A backfill never touches an existing row. It never overwrites an "events" row.
//   - daily_activity.files counts distinct rows of file_changes, so both sources count once.

// Sources of a file_changes row. See the note at the top of this file.
const (
	FileChangeSourceEvents = "events"
	FileChangeSourceMtime  = "mtime"
)

// FileChangeTally is the number of file_changes rows of one (project, day), split by source.
type FileChangeTally struct {
	Events int `json:"events"`
	Mtime  int `json:"mtime"`
}

// Total is the number of distinct paths for the day.
func (t FileChangeTally) Total() int { return t.Events + t.Mtime }

// ApplyFileEvents adds live file events for one project and local day (YYYY-MM-DD).
// counts maps a path (relative to the project) to the number of events seen since the last call.
// A count below 1 is treated as 1. An existing "mtime" row becomes an "events" row.
// It does not recompute daily_activity. Call RecomputeDaily (or RecomputeDailyTx) afterwards.
func (d *DB) ApplyFileEvents(ctx context.Context, project, day string, counts map[string]int) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return ApplyFileEventsTx(ctx, tx, project, day, counts) })
}

// ApplyFileEventsTx is ApplyFileEvents inside a transaction you already have.
func ApplyFileEventsTx(ctx context.Context, tx *sql.Tx, project, day string, counts map[string]int) error {
	if _, err := time.Parse(DayLayout, day); err != nil {
		return errors.New("store: ApplyFileEvents day must be YYYY-MM-DD")
	}
	if len(counts) == 0 {
		return nil
	}
	st, err := tx.PrepareContext(ctx, `
INSERT INTO file_changes(project_id, day, path, n) VALUES(?, ?, ?, ?)
ON CONFLICT(project_id, day, path) DO UPDATE SET n = n + excluded.n`)
	if err != nil {
		return err
	}
	defer st.Close()
	for p, n := range counts {
		if p == "" {
			continue
		}
		if n < 1 {
			n = 1
		}
		if _, err := st.ExecContext(ctx, project, day, p, n); err != nil {
			return err
		}
	}
	return nil
}

// ApplyFileMtimes records paths whose modification time falls on a local day (YYYY-MM-DD).
// The rows get n = 0 (source "mtime"). A path that already has a row for that day, from any
// source, is left alone. It returns how many new rows were added.
// It does not recompute daily_activity.
func (d *DB) ApplyFileMtimes(ctx context.Context, project, day string, paths []string) (added int, err error) {
	err = d.Write(ctx, func(tx *sql.Tx) error {
		var err error
		added, err = ApplyFileMtimesTx(ctx, tx, project, day, paths)
		return err
	})
	return added, err
}

// ApplyFileMtimesTx is ApplyFileMtimes inside a transaction you already have.
func ApplyFileMtimesTx(ctx context.Context, tx *sql.Tx, project, day string, paths []string) (int, error) {
	if _, err := time.Parse(DayLayout, day); err != nil {
		return 0, errors.New("store: ApplyFileMtimes day must be YYYY-MM-DD")
	}
	if len(paths) == 0 {
		return 0, nil
	}
	st, err := tx.PrepareContext(ctx, `
INSERT INTO file_changes(project_id, day, path, n) VALUES(?, ?, ?, 0)
ON CONFLICT(project_id, day, path) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	added := 0
	for _, p := range paths {
		if p == "" {
			continue
		}
		res, err := st.ExecContext(ctx, project, day, p)
		if err != nil {
			return added, err
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			added++
		}
	}
	return added, nil
}

// FileChangePaths lists the distinct paths stored for one project and local day (any source).
func (d *DB) FileChangePaths(ctx context.Context, project, day string) ([]string, error) {
	rows, err := d.Read().QueryContext(ctx,
		`SELECT path FROM file_changes WHERE project_id = ? AND day = ? ORDER BY path`, project, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FileChangeCount returns how many rows of file_changes a project and local day have, by source.
func (d *DB) FileChangeCount(ctx context.Context, project, day string) (FileChangeTally, error) {
	var t FileChangeTally
	err := d.Read().QueryRowContext(ctx, `
SELECT COALESCE(SUM(CASE WHEN n > 0 THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN n = 0 THEN 1 ELSE 0 END), 0)
FROM file_changes WHERE project_id = ? AND day = ?`, project, day).Scan(&t.Events, &t.Mtime)
	return t, err
}
