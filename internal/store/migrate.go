package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNewerSchema is returned by Open when the database was written by a newer drashta.
// The file is not touched. Run the newer drashta, or delete the database.
var ErrNewerSchema = errors.New("store: database schema is newer than this drashta")

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded files migrations/NNN_name.sql.
// The numbers must start at 1 and have no gaps.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		numPart, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("store: migration %q must be named NNN_name.sql", name)
		}
		n, err := strconv.Atoi(numPart)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("store: migration %q has a bad number", name)
		}
		body, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: n, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migrations must be numbered 1..N without gaps (saw %s at position %d)", m.name, i+1)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("store: no migrations found")
	}
	return out, nil
}

// LatestSchemaVersion is the number of the last embedded migration.
func LatestSchemaVersion() int {
	ms, err := loadMigrations()
	if err != nil {
		return 0
	}
	return ms[len(ms)-1].version
}

// migrate applies every migration newer than PRAGMA user_version.
// Each migration runs in its own transaction, together with the user_version bump.
// db must be the single writer connection.
func migrate(ctx context.Context, db *sql.DB) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	latest := ms[len(ms)-1].version
	if current > latest {
		return fmt.Errorf("%w (file has version %d, this build knows %d)", ErrNewerSchema, current, latest)
	}
	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	// PRAGMA does not take bound parameters. The number is an int we built, so this is safe.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(m.version)); err != nil {
		return err
	}
	return tx.Commit()
}
