package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMigrateZeroToLatest(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var v int
	if err := db.Read().QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != LatestSchemaVersion() || v < 1 {
		t.Fatalf("user_version = %d, want latest %d", v, LatestSchemaVersion())
	}
	if got, ok, _ := db.GetMeta(bg(), "schema_version"); !ok || got != strconv.Itoa(LatestSchemaVersion()) {
		t.Fatalf("meta schema_version = %q, %v", got, ok)
	}
	// Every table of the doc exists.
	have := tableNames(t, db.Read())
	for _, want := range []string{"projects", "files", "commits", "commit_files", "sessions", "events",
		"server_runs", "file_changes", "daily_activity", "claude_file", "sweep_log", "search"} {
		if !have[want] {
			t.Errorf("table %s is missing", want)
		}
	}

	// Opening again does nothing and keeps the data.
	if err := db.SetMeta(bg(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got, ok, _ := db2.GetMeta(bg(), "k"); !ok || got != "v" {
		t.Fatalf("meta k after reopen = %q, %v", got, ok)
	}
}

func TestMigrationFilesAreNumberedWithoutGaps(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range ms {
		if m.version != i+1 {
			t.Fatalf("migration %s at position %d", m.name, i+1)
		}
	}
}

func TestNewerSchemaIsRefusedAndFileIsKept(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write(bg(), func(tx *sql.Tx) error {
		_, err := tx.Exec("PRAGMA user_version = 999")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(dir)
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("Open error = %v, want ErrNewerSchema", err)
	}
	if _, err := os.Stat(dbPathIn(dir)); err != nil {
		t.Fatalf("the database file must stay in place: %v", err)
	}
	matches, _ := filepath.Glob(dbPathIn(dir) + ".corrupt-*")
	if len(matches) != 0 {
		t.Fatalf("a newer database must not be moved aside: %v", matches)
	}
}

func TestPragmasAreApplied(t *testing.T) {
	db := openTest(t)

	check := func(name string, q *sql.DB, wants map[string]string) {
		t.Helper()
		// Take several connections at once, so we see more than one.
		var conns []*sql.Conn
		for i := 0; i < 3; i++ {
			c, err := q.Conn(bg())
			if err != nil {
				if name == "writer" {
					break // the writer pool has one connection only
				}
				t.Fatal(err)
			}
			conns = append(conns, c)
			if name == "writer" {
				break
			}
		}
		defer func() {
			for _, c := range conns {
				c.Close()
			}
		}()
		if len(conns) == 0 {
			t.Fatalf("%s: no connection", name)
		}
		for _, c := range conns {
			for pragma, want := range wants {
				var got string
				if err := c.QueryRowContext(bg(), "PRAGMA "+pragma).Scan(&got); err != nil {
					t.Fatalf("%s PRAGMA %s: %v", name, pragma, err)
				}
				if strings.ToLower(got) != want {
					t.Errorf("%s PRAGMA %s = %q, want %q", name, pragma, got, want)
				}
			}
		}
	}

	check("writer", db.w, map[string]string{
		"journal_mode": "wal", "synchronous": "1", "foreign_keys": "1", "busy_timeout": "5000",
	})
	check("reader", db.r, map[string]string{
		"journal_mode": "wal", "synchronous": "1", "foreign_keys": "1", "busy_timeout": "5000", "query_only": "1",
	})

	// The read pool cannot write.
	if _, err := db.Read().Exec(`INSERT INTO meta(key, value) VALUES('x', 'y')`); err == nil {
		t.Fatal("a write on the read pool must fail")
	}
}

func TestCorruptFileIsMovedAsideAndRecreated(t *testing.T) {
	dir := t.TempDir()
	path := dbPathIn(dir)
	garbage := []byte(strings.Repeat("this is not a sqlite database\n", 400))
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	// A stale WAL from the old file must not be applied to the new database.
	if err := os.WriteFile(path+"-wal", []byte("stale wal bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open must recover, got: %v", err)
	}
	defer db.Close()

	moved := db.Recovered()
	if moved == "" || !strings.Contains(moved, "index.db.corrupt-") {
		t.Fatalf("Recovered() = %q", moved)
	}
	got, err := os.ReadFile(moved)
	if err != nil || string(got) != string(garbage) {
		t.Fatalf("the corrupt file must be kept intact at %s (err %v)", moved, err)
	}
	// The stale -wal must never be applied to the new database. It is moved along, or SQLite removed it.
	if wal, err := os.ReadFile(path + "-wal"); err == nil && strings.Contains(string(wal), "stale wal bytes") {
		t.Errorf("the stale -wal file is still next to the new database")
	}
	// The new database works.
	if err := db.SetMeta(bg(), "a", "b"); err != nil {
		t.Fatal(err)
	}
	if db.Recovered() == "" {
		t.Fatal("Recovered must stay set")
	}
}

func TestDamagedPagesFailQuickCheck(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Put enough data in so there are real table pages.
	for i := 0; i < 50; i++ {
		seedProject(t, db, "p"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Overwrite pages 2..6 with 0xFF. The first page (header) stays valid.
	f, err := os.OpenFile(dbPathIn(dir), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	junk := make([]byte, 4096*5)
	for i := range junk {
		junk[i] = 0xFF
	}
	if _, err := f.WriteAt(junk, 4096); err != nil {
		t.Fatal(err)
	}
	f.Close()

	db2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open must recover: %v", err)
	}
	defer db2.Close()
	if db2.Recovered() == "" {
		t.Fatal("damaged pages should have been detected")
	}
	if n := count(t, db2, "SELECT COUNT(*) FROM projects"); n != 0 {
		t.Fatalf("fresh database should be empty, has %d projects", n)
	}
}

func TestRecoveredIsEmptyOnNormalOpen(t *testing.T) {
	db := openTest(t)
	if db.Recovered() != "" {
		t.Fatalf("Recovered() = %q on a good database", db.Recovered())
	}
}

func TestOpenRejectsEmptyDir(t *testing.T) {
	if _, err := Open(" "); err == nil {
		t.Fatal("empty data dir must be an error")
	}
}

// ---------------------------------------------------------------------------
// writer

func TestWritesAreSerialisedAcrossGoroutines(t *testing.T) {
	db := openTest(t)
	mustWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE tcount(id INTEGER PRIMARY KEY, n INTEGER); INSERT INTO tcount VALUES(1, 0)`)
		return err
	})

	const workers, each = 16, 40
	var inFlight, maxInFlight atomic.Int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				err := db.Write(bg(), func(tx *sql.Tx) error {
					cur := inFlight.Add(1)
					defer inFlight.Add(-1)
					for {
						m := maxInFlight.Load()
						if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
							break
						}
					}
					// read, wait a little, write: loses updates if two run together
					var n int
					if err := tx.QueryRow(`SELECT n FROM tcount WHERE id = 1`).Scan(&n); err != nil {
						return err
					}
					time.Sleep(50 * time.Microsecond)
					_, err := tx.Exec(`UPDATE tcount SET n = ? WHERE id = 1`, n+1)
					return err
				})
				if err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := count(t, db, `SELECT n FROM tcount WHERE id = 1`); got != workers*each {
		t.Fatalf("counter = %d, want %d (lost updates)", got, workers*each)
	}
	if m := maxInFlight.Load(); m != 1 {
		t.Fatalf("%d write functions ran at the same time, want 1", m)
	}
}

func TestEachWriteIsItsOwnTransaction(t *testing.T) {
	db := openTest(t)
	mustWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE tt(v TEXT)`)
		return err
	})
	boom := errors.New("boom")

	// A failing call is rolled back and its error comes back to that caller only.
	err := db.Write(bg(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO tt VALUES('lost')`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	// A panic is turned into an error and rolled back.
	err = db.Write(bg(), func(tx *sql.Tx) error {
		_, _ = tx.Exec(`INSERT INTO tt VALUES('lost2')`)
		panic("oops")
	})
	if err == nil || !strings.Contains(err.Error(), "oops") {
		t.Fatalf("panic should become an error, got %v", err)
	}
	// The next call works.
	mustWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO tt VALUES('kept')`)
		return err
	})
	if n := count(t, db, `SELECT COUNT(*) FROM tt`); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestWriteWithCancelledContextDoesNotRun(t *testing.T) {
	db := openTest(t)
	release := make(chan struct{})
	started := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- db.Write(bg(), func(tx *sql.Tx) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	ctx, cancel := context.WithCancel(bg())
	var ran atomic.Bool
	second := make(chan error, 1)
	go func() {
		second <- db.Write(ctx, func(tx *sql.Tx) error { ran.Store(true); return nil })
	}()
	time.Sleep(20 * time.Millisecond) // let it queue behind the first
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled caller must not wait for the writer")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	// Run one more job so the queue is certainly past the cancelled one.
	mustWrite(t, db, func(tx *sql.Tx) error { return nil })
	if ran.Load() {
		t.Fatal("the function of a cancelled call must not run")
	}
}

func TestCloseFlushesQueuedWritesAndRejectsNewOnes(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE tq(n INTEGER)`)
		return err
	})

	const n = 40
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := db.Write(bg(), func(tx *sql.Tx) error {
				_, err := tx.Exec(`INSERT INTO tq VALUES(?)`, i)
				return err
			})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrClosed):
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if err := db.Write(bg(), func(tx *sql.Tx) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close = %v, want ErrClosed", err)
	}
	if err := db.Close(); err != nil { // second Close is fine
		t.Fatal(err)
	}

	// Every write that reported success is on disk.
	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got := count(t, db2, `SELECT COUNT(*) FROM tq`); got != int(ok.Load()) {
		t.Fatalf("rows on disk = %d, successful writes = %d", got, ok.Load())
	}
}

// ---------------------------------------------------------------------------
// table lists

func TestTableListsMatchTheMigration(t *testing.T) {
	db := openTest(t)
	have := tableNames(t, db.Read())

	seen := map[string]string{}
	for _, name := range rTables {
		if prev, dup := seen[name]; dup {
			t.Errorf("%s is listed twice (%s and R)", name, prev)
		}
		seen[name] = "R"
	}
	for _, name := range hTables {
		if prev, dup := seen[name]; dup {
			t.Errorf("%s is listed twice (%s and H)", name, prev)
		}
		seen[name] = "H"
	}
	for name := range have {
		if seen[name] == "" {
			t.Errorf("table %s is in the migration but in neither the R nor the H list", name)
		}
	}
	for name, kind := range seen {
		if !have[name] {
			t.Errorf("table %s is in the %s list but not in the migration", name, kind)
		}
	}
	// The history tables named in the brief and in 04 section 5.
	for _, want := range []string{"server_runs", "file_changes", "daily_activity", "sweep_log", "settings"} {
		if seen[want] != "H" {
			t.Errorf("%s must be an H table", want)
		}
	}
}

// ---------------------------------------------------------------------------
// reindex

func TestReindexKeepsHistoryAndRebuildsCache(t *testing.T) {
	db := openTest(t)
	ctx := bg()

	// Cache data (R).
	seedProject(t, db, "alphalpha")
	if err := db.ReplaceFiles(ctx, "alphalpha", []File{{Path: "main.go", Kind: "code", MTime: time.Unix(100, 0)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertAuthoredTask(ctx, Task{ProjectID: "alphalpha", ID: "T-1", Title: "Write the store"}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutClaudeFile(ctx, ClaudeFile{Path: "/x.jsonl", Size: 5, MTime: time.Unix(1, 0), Offset: 5, Inode: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddEvent(ctx, Event{ProjectID: "alphalpha", TS: time.Unix(500, 0), Kind: "commit", Ref: "commit:abc"}); err != nil {
		t.Fatal(err)
	}
	// History data (H).
	if err := db.SetSetting(ctx, "author", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendServerRun(ctx, ServerRun{Key: "k", ProjectID: "alphalpha", Port: 5173, Started: time.Unix(1000, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFileChanges(ctx, "alphalpha", "2026-09-30", []string{"a.go", "b.go"}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecomputeDaily(ctx, "alphalpha", "2026-09-30", testLoc); err != nil {
		t.Fatal(err)
	}
	if err := db.AddSweepLog(ctx, SweepLog{Scope: "all", MS: 12, OK: true}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search`); n == 0 {
		t.Fatal("the test needs search rows before Reindex")
	}

	if err := db.Reindex(ctx); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	// All R tables are empty (meta holds only schema_version again).
	for _, name := range rTables {
		want := 0
		if name == "meta" {
			want = 1
		}
		if n := count(t, db, `SELECT COUNT(*) FROM "`+name+`"`); n != want {
			t.Errorf("R table %s has %d rows after Reindex, want %d", name, n, want)
		}
	}
	// All H tables kept their rows.
	for name, want := range map[string]int{"settings": 1, "server_runs": 1, "file_changes": 2, "daily_activity": 1, "sweep_log": 1} {
		if n := count(t, db, `SELECT COUNT(*) FROM "`+name+`"`); n != want {
			t.Errorf("H table %s has %d rows after Reindex, want %d", name, n, want)
		}
	}
	// Same table set and same indexes as before: Reindex rebuilt the schema, not a copy of it.
	have := tableNames(t, db.Read())
	for _, name := range append(append([]string{}, rTables...), hTables...) {
		if !have[name] {
			t.Errorf("table %s is gone after Reindex", name)
		}
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'commits_ts'`); n != 1 {
		t.Error("indexes must be recreated")
	}
	if v, ok, _ := db.GetMeta(ctx, "schema_version"); !ok || v != strconv.Itoa(LatestSchemaVersion()) {
		t.Errorf("schema_version after Reindex = %q, %v", v, ok)
	}
	var uv int
	if err := db.Read().QueryRow("PRAGMA user_version").Scan(&uv); err != nil || uv != LatestSchemaVersion() {
		t.Errorf("user_version after Reindex = %d (%v)", uv, err)
	}

	// The cache and the search index work again.
	seedProject(t, db, "alphalpha")
	if err := db.UpsertAuthoredTask(ctx, Task{ProjectID: "alphalpha", ID: "T-2", Title: "Rebuilt after reindex"}); err != nil {
		t.Fatal(err)
	}
	hits, err := db.Search(ctx, "rebuilt", "", "", 10)
	if err != nil || len(hits) != 1 || hits[0].Ref != "task:T-2" {
		t.Fatalf("search after Reindex = %+v, %v", hits, err)
	}
	// And the old search rows are really gone.
	if hits, _ := db.Search(ctx, "main.go", "", "", 10); len(hits) != 0 {
		t.Fatalf("old search rows survived: %+v", hits)
	}
}

func TestReindexIsAllOrNothing(t *testing.T) {
	db := openTest(t)
	seedProject(t, db, "alphalpha")
	// Break the schema so Reindex fails halfway: remove a cache table it expects to find.
	mustWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DROP TABLE gh_cache`)
		return err
	})
	if err := db.Reindex(bg()); err == nil {
		t.Fatal("Reindex must fail when a cache table is missing")
	}
	// Nothing was dropped.
	if n := count(t, db, `SELECT COUNT(*) FROM projects`); n != 1 {
		t.Fatalf("projects = %d, the failed Reindex must roll back", n)
	}
}
