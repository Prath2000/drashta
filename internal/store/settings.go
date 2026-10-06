package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// SettingsFileName is the mirror of the settings table, inside the data dir.
// It lets settings survive a deleted index.db (04 section 5, note on H*).
const SettingsFileName = "settings.json"

// SettingsPath returns the full path of settings.json.
func (d *DB) SettingsPath() string { return filepath.Join(d.dir, SettingsFileName) }

// GetSetting returns one setting. ok is false when the key is not set.
func (d *DB) GetSetting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = d.r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// Settings returns all settings.
func (d *DB) Settings(ctx context.Context) (map[string]string, error) {
	return readSettings(ctx, d.r)
}

func readSettings(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSetting saves one setting, then rewrites settings.json.
// If the file cannot be written, the setting is still saved and the error is returned.
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	err := d.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO settings(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		return err
	})
	if err != nil {
		return err
	}
	return d.mirrorSettings(ctx)
}

// DeleteSetting removes one setting, then rewrites settings.json.
func (d *DB) DeleteSetting(ctx context.Context, key string) error {
	err := d.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
		return err
	})
	if err != nil {
		return err
	}
	return d.mirrorSettings(ctx)
}

// mirrorSettings writes all settings to settings.json (temp file, then rename).
func (d *DB) mirrorSettings(ctx context.Context) error {
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	all, err := readSettings(ctx, d.r)
	if err != nil {
		return err
	}
	return writeSettingsFile(d.SettingsPath(), all)
}

func writeSettingsFile(path string, all map[string]string) error {
	data, err := json.MarshalIndent(all, "", "  ") // map keys are sorted, so the file is stable
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// restoreSettings loads settings.json into an EMPTY settings table.
// It runs once in Open. If the table already has rows, the database wins.
func (d *DB) restoreSettings(ctx context.Context) error {
	var n int
	if err := d.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	data, err := os.ReadFile(d.SettingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved map[string]string
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	if len(saved) == 0 {
		return nil
	}
	return d.Write(ctx, func(tx *sql.Tx) error {
		for k, v := range saved {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO settings(key, value) VALUES(?, ?)`, k, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// meta (cache table: schema_version, last_sweep_at, ...)

// GetMeta returns one meta value. ok is false when the key is not set.
func (d *DB) GetMeta(ctx context.Context, key string) (value string, ok bool, err error) {
	err = d.r.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetMeta saves one meta value.
func (d *DB) SetMeta(ctx context.Context, key, value string) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO meta(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		return err
	})
}

// ---------------------------------------------------------------------------
// sweep_log (history)

// maxSweepLog is how many sweep_log rows are kept. Older rows are deleted on insert.
const maxSweepLog = 5000

// SweepLog is one row of the sweep history.
type SweepLog struct {
	ID    int64     `json:"id"`
	TS    time.Time `json:"ts"`
	Scope string    `json:"scope"` // "all", a project id, "claude", ...
	MS    int64     `json:"ms"`
	OK    bool      `json:"ok"`
}

// AddSweepLog appends one row. A zero TS means now.
func (d *DB) AddSweepLog(ctx context.Context, s SweepLog) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		ts := s.TS
		if ts.IsZero() {
			ts = now()
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO sweep_log(ts, scope, ms, ok) VALUES(?, ?, ?, ?)`, ts.Unix(), s.Scope, s.MS, b2i(s.OK))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sweep_log WHERE id <= ?`, id-maxSweepLog)
		return err
	})
}

// RecentSweeps returns the newest sweep rows, newest first. limit <= 0 means 20.
func (d *DB) RecentSweeps(ctx context.Context, limit int) ([]SweepLog, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.r.QueryContext(ctx, `SELECT id, ts, scope, ms, ok FROM sweep_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SweepLog{}
	for rows.Next() {
		var s SweepLog
		var ts int64
		var ok int
		if err := rows.Scan(&s.ID, &ts, &s.Scope, &s.MS, &ok); err != nil {
			return nil, err
		}
		s.TS = time.Unix(ts, 0)
		s.OK = ok != 0
		out = append(out, s)
	}
	return out, rows.Err()
}
