package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Every test works in t.TempDir(). Nothing here reads or writes ~/.drashta or ~/projects.

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func bg() context.Context { return context.Background() }

// count runs SELECT COUNT(*) with a query and args on the read pool.
func count(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// tableNames returns user tables in the database (no sqlite_ tables, no FTS shadow tables).
func tableNames(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	shadow := map[string]bool{}
	for _, s := range ftsShadowTables {
		shadow[s] = true
	}
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if !shadow[n] {
			out[n] = true
		}
	}
	return out
}

// fixed local zone for day maths, so tests do not depend on the machine's zone.
var testLoc = time.FixedZone("IST", 5*3600+1800)

func at(day string, hour, min int) time.Time {
	d, err := time.ParseInLocation(DayLayout, day, testLoc)
	if err != nil {
		panic(err)
	}
	return d.Add(time.Duration(hour)*time.Hour + time.Duration(min)*time.Minute)
}

// useClock sets the package clock for one test.
func useClock(t *testing.T, tm time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return tm }
	t.Cleanup(func() { now = old })
}

func mustWrite(t *testing.T, db *DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	if err := db.Write(bg(), fn); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func dbPathIn(dir string) string { return filepath.Join(dir, DBFileName) }

func seedProject(t *testing.T, db *DB, id string) {
	t.Helper()
	err := db.UpsertProject(bg(), Project{
		ID: id, Name: filepath.Base(id), Group: "g", Path: "/p/" + id, Stack: []string{"go"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fmtDay(i int) string { return fmt.Sprintf("2026-09-%02d", i) }
