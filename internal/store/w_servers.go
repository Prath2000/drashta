package store

import (
	"context"
	"database/sql"
	"time"
)

// ServerNow is one listening server right now (table servers_now).
type ServerNow struct {
	Key       string // stable key (ledger serverKey)
	Port      int
	PID       int
	Name      string
	Cmd       string
	CWD       string
	ProjectID string // "" = not in a project
	Kind      string // dev | helper
	Title     string
	FirstSeen time.Time
	LastSeen  time.Time
}

// ReplaceServersNow replaces the whole servers_now table. The indexer calls it after every lsof pass.
func (d *DB) ReplaceServersNow(ctx context.Context, rows []ServerNow) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return ReplaceServersNowTx(ctx, tx, rows) })
}

// ReplaceServersNowTx is ReplaceServersNow inside a transaction you already have.
func ReplaceServersNowTx(ctx context.Context, tx *sql.Tx, rows []ServerNow) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM servers_now`); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx, `
INSERT OR REPLACE INTO servers_now(key, port, pid, name, cmd, cwd, project_id, kind, title, first_seen, last_seen)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, r := range rows {
		kind := r.Kind
		if kind == "" {
			kind = "dev"
		}
		if _, err := ins.ExecContext(ctx, r.Key, r.Port, r.PID, r.Name, r.Cmd, r.CWD, r.ProjectID, kind,
			r.Title, unixOrNil(r.FirstSeen), unixOrNil(r.LastSeen)); err != nil {
			return err
		}
	}
	return nil
}

// ServerRun is one row of the history table server_runs: a server that ran for a while.
// The wiring code converts between this and the servers package type.
type ServerRun struct {
	ID        int64 // 0 on insert; the store fills it
	Key       string
	ProjectID string
	Port      int
	PID       int
	Name      string
	Cmd       string
	CWD       string
	Kind      string    // dev | helper
	Started   time.Time // first seen
	Ended     time.Time // zero = still running
	LastSeen  time.Time // last scan that saw it
}

// AppendServerRun adds a run and returns its new id.
func (d *DB) AppendServerRun(ctx context.Context, r ServerRun) (int64, error) {
	var id int64
	err := d.Write(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = insertServerRun(ctx, tx, r, false)
		return err
	})
	return id, err
}

// EndServerRun sets the end time of a run that is still open. An already ended run is left alone.
func (d *DB) EndServerRun(ctx context.Context, id int64, ended time.Time) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE server_runs SET ended = ?, last_seen = COALESCE(last_seen, ?) WHERE id = ? AND ended IS NULL`,
			ended.Unix(), ended.Unix(), id)
		return err
	})
}

// ListServerRuns returns all runs, oldest first (by start, then id).
func (d *DB) ListServerRuns(ctx context.Context) ([]ServerRun, error) {
	rows, err := d.r.QueryContext(ctx, `
SELECT id, key, project_id, port, pid, name, cmd, cwd, kind, started, ended, last_seen
FROM server_runs ORDER BY started, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerRun{}
	for rows.Next() {
		var r ServerRun
		var started int64
		var ended, last sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Key, &r.ProjectID, &r.Port, &r.PID, &r.Name, &r.Cmd, &r.CWD,
			&r.Kind, &started, &ended, &last); err != nil {
			return nil, err
		}
		r.Started = fromUnix(sql.NullInt64{Int64: started, Valid: true})
		r.Ended = fromUnix(ended)
		r.LastSeen = fromUnix(last)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceServerRuns replaces the whole history with rows. A row with ID > 0 keeps its id.
func (d *DB) ReplaceServerRuns(ctx context.Context, rows []ServerRun) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM server_runs`); err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := insertServerRun(ctx, tx, r, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// insertServerRun inserts one run. With keepID and r.ID > 0 the given id is used.
func insertServerRun(ctx context.Context, tx *sql.Tx, r ServerRun, keepID bool) (int64, error) {
	var id any
	if keepID && r.ID > 0 {
		id = r.ID
	}
	kind := r.Kind
	if kind == "" {
		kind = "dev"
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO server_runs(id, key, project_id, port, pid, name, cmd, cwd, kind, started, ended, last_seen)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, r.Key, r.ProjectID, r.Port, r.PID, r.Name, r.Cmd, r.CWD, kind,
		unixOrZero(r.Started), unixOrNil(r.Ended), unixOrNil(r.LastSeen))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
