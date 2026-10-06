package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Event is one dated tremor (table events). Kinds: commit, session_start, session_end, server_up,
// server_down, file_burst, task_created, task_status, snapshot, verdict, note.
type Event struct {
	ID        int64           `json:"id,omitempty"`
	ProjectID string          `json:"projectId"`
	TS        time.Time       `json:"ts"`
	Day       string          `json:"day"` // YYYY-MM-DD, local. If empty on insert, it is taken from TS in time.Local.
	Kind      string          `json:"kind"`
	Ref       string          `json:"ref"`
	Label     string          `json:"label"`
	Meta      json.RawMessage `json:"meta,omitempty"` // JSON object. Empty means {}.
}

// AddEvent inserts an event. It is safe to call twice with the same event: a second event with the
// same (project, kind, ref, ts) is ignored. added tells you which happened.
func (d *DB) AddEvent(ctx context.Context, e Event) (added bool, err error) {
	err = d.Write(ctx, func(tx *sql.Tx) error {
		var err error
		added, err = AddEventTx(ctx, tx, e)
		return err
	})
	return added, err
}

// AddEventTx is AddEvent inside a transaction you already have.
func AddEventTx(ctx context.Context, tx *sql.Tx, e Event) (bool, error) {
	day := e.Day
	if day == "" {
		day = DayOf(e.TS, time.Local)
	}
	meta := "{}"
	if len(e.Meta) > 0 {
		if !json.Valid(e.Meta) {
			return false, errors.New("store: event meta is not valid JSON")
		}
		meta = string(e.Meta)
	}
	res, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO events(project_id, ts, day, kind, ref, label, meta) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		e.ProjectID, unixOrZero(e.TS), day, e.Kind, e.Ref, e.Label, meta)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AddFileChanges records that these paths changed on a local day (table file_changes, history).
// Each path in the list adds 1 to its counter n. Pass a path twice to count two changes.
// The number of distinct paths for a day is what daily_activity.files counts.
func (d *DB) AddFileChanges(ctx context.Context, project, day string, paths []string) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return AddFileChangesTx(ctx, tx, project, day, paths) })
}

// AddFileChangesTx is AddFileChanges inside a transaction you already have.
func AddFileChangesTx(ctx context.Context, tx *sql.Tx, project, day string, paths []string) error {
	if _, err := time.Parse(DayLayout, day); err != nil {
		return errors.New("store: AddFileChanges day must be YYYY-MM-DD")
	}
	st, err := tx.PrepareContext(ctx, `
INSERT INTO file_changes(project_id, day, path, n) VALUES(?, ?, ?, 1)
ON CONFLICT(project_id, day, path) DO UPDATE SET n = n + 1`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := st.ExecContext(ctx, project, day, p); err != nil {
			return err
		}
	}
	return nil
}
