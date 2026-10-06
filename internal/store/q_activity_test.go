package store

import (
	"context"
	"testing"
)

// Tests for q_activity.go. Everything runs in t.TempDir().

func qaOpen(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func qaN(t *testing.T, db *DB, project, day, path string) int {
	t.Helper()
	var n int
	err := db.Read().QueryRow(`SELECT n FROM file_changes WHERE project_id = ? AND day = ? AND path = ?`,
		project, day, path).Scan(&n)
	if err != nil {
		t.Fatalf("read n of %s: %v", path, err)
	}
	return n
}

func TestApplyFileEventsCountsAndUpgrade(t *testing.T) {
	ctx := context.Background()
	db := qaOpen(t)

	if err := db.ApplyFileEvents(ctx, "p", "2026-10-01", map[string]int{"a.go": 2, "b.go": 0}); err != nil {
		t.Fatal(err)
	}
	if got := qaN(t, db, "p", "2026-10-01", "a.go"); got != 2 {
		t.Fatalf("a.go n = %d, want 2", got)
	}
	if got := qaN(t, db, "p", "2026-10-01", "b.go"); got != 1 {
		t.Fatalf("b.go n = %d, want 1 (a count below 1 is treated as 1)", got)
	}
	// A second call adds to the counter. The path stays one row.
	if err := db.ApplyFileEvents(ctx, "p", "2026-10-01", map[string]int{"a.go": 3}); err != nil {
		t.Fatal(err)
	}
	if got := qaN(t, db, "p", "2026-10-01", "a.go"); got != 5 {
		t.Fatalf("a.go n = %d, want 5", got)
	}
	tally, err := db.FileChangeCount(ctx, "p", "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	if tally.Events != 2 || tally.Mtime != 0 || tally.Total() != 2 {
		t.Fatalf("tally = %+v", tally)
	}

	// An mtime row is upgraded to an events row by a live event.
	if added, err := db.ApplyFileMtimes(ctx, "p", "2026-10-01", []string{"m.go"}); err != nil || added != 1 {
		t.Fatalf("ApplyFileMtimes added=%d err=%v", added, err)
	}
	if got := qaN(t, db, "p", "2026-10-01", "m.go"); got != 0 {
		t.Fatalf("m.go n = %d, want 0", got)
	}
	if err := db.ApplyFileEvents(ctx, "p", "2026-10-01", map[string]int{"m.go": 1}); err != nil {
		t.Fatal(err)
	}
	if got := qaN(t, db, "p", "2026-10-01", "m.go"); got != 1 {
		t.Fatalf("m.go n = %d after upgrade, want 1", got)
	}
}

func TestApplyFileMtimesNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	db := qaOpen(t)

	if err := db.ApplyFileEvents(ctx, "p", "2026-10-01", map[string]int{"a.go": 4}); err != nil {
		t.Fatal(err)
	}
	added, err := db.ApplyFileMtimes(ctx, "p", "2026-10-01", []string{"a.go", "b.go", "b.go", ""})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1 (only b.go is new)", added)
	}
	if got := qaN(t, db, "p", "2026-10-01", "a.go"); got != 4 {
		t.Fatalf("a.go n = %d, an events row must not be overwritten (want 4)", got)
	}
	// Running it again changes nothing.
	added, err = db.ApplyFileMtimes(ctx, "p", "2026-10-01", []string{"a.go", "b.go"})
	if err != nil || added != 0 {
		t.Fatalf("second run added=%d err=%v, want 0", added, err)
	}
	tally, _ := db.FileChangeCount(ctx, "p", "2026-10-01")
	if tally.Events != 1 || tally.Mtime != 1 {
		t.Fatalf("tally = %+v, want 1 events + 1 mtime", tally)
	}
}

func TestFileChangePathsAndDayCheck(t *testing.T) {
	ctx := context.Background()
	db := qaOpen(t)

	paths, err := db.FileChangePaths(ctx, "p", "2026-10-01")
	if err != nil || paths == nil || len(paths) != 0 {
		t.Fatalf("empty day: paths=%v err=%v (want a non-nil empty list)", paths, err)
	}
	_ = db.ApplyFileEvents(ctx, "p", "2026-10-01", map[string]int{"z.go": 1, "a.go": 1})
	_, _ = db.ApplyFileMtimes(ctx, "p", "2026-10-01", []string{"m.go"})
	_ = db.ApplyFileEvents(ctx, "p", "2026-10-02", map[string]int{"other.go": 1})
	_ = db.ApplyFileEvents(ctx, "q", "2026-10-01", map[string]int{"qq.go": 1})
	paths, err = db.FileChangePaths(ctx, "p", "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || paths[0] != "a.go" || paths[1] != "m.go" || paths[2] != "z.go" {
		t.Fatalf("paths = %v", paths)
	}

	if err := db.ApplyFileEvents(ctx, "p", "10/01/2026", map[string]int{"a": 1}); err == nil {
		t.Fatal("a bad day must fail")
	}
	if _, err := db.ApplyFileMtimes(ctx, "p", "nope", []string{"a"}); err == nil {
		t.Fatal("a bad day must fail")
	}
}
