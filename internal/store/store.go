package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
)

// DBFileName is the database file inside the data dir.
const DBFileName = "index.db"

// ErrClosed is returned by Write after Close.
var ErrClosed = errors.New("store: closed")

// errCorrupt marks a failure that means "this file is not a usable database".
// Only Open uses it, to decide to move the file aside.
var errCorrupt = errors.New("store: database is corrupt")

// DB is the SQLite index. One goroutine does all the writes. Reads use a pool.
type DB struct {
	dir  string // data dir, for example ~/.drashta
	path string // dir/index.db

	w *sql.DB // the one writer connection
	r *sql.DB // read pool, query_only

	jobs     chan *job
	loopDone chan struct{}

	mu     sync.RWMutex // guards closed, and sending on jobs
	closed bool

	recovered string // path of the moved-aside corrupt file, or ""

	settingsMu sync.Mutex // serialises settings.json writes
}

// Job states. A job starts queued. The writer goroutine moves it to running, or the caller
// moves it to cancelled (its context ended while it waited). Only one of them wins.
const (
	jobQueued int32 = iota
	jobRunning
	jobCancelled
)

type job struct {
	ctx   context.Context
	fn    func(*sql.Tx) error
	done  chan error // buffered, so the writer never blocks on a caller that left
	state atomic.Int32
}

// Open opens (or creates) <dataDir>/index.db, checks it, and migrates it to the latest schema.
//
// If the file is not a usable database (garbage, or PRAGMA quick_check fails), it is renamed to
// index.db.corrupt-<timestamp>, a fresh database is created, and one warning is logged.
// Recovered() then returns the new name of the old file.
func Open(dataDir string) (*DB, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("store: data dir is empty")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	path := filepath.Join(dataDir, DBFileName)
	// Private files (M-1): the folder 0700 (MkdirAll does not change an existing one), and
	// index.db 0600 before SQLite opens it. SQLite gives -wal and -shm the mode of the
	// database file; ones left from an older run are fixed here too.
	if err := securePrivate(dataDir, path); err != nil {
		return nil, err
	}

	d, err := openFile(dataDir, path)
	recovered := ""
	if errors.Is(err, errCorrupt) {
		cause := err
		recovered, err = moveAside(path)
		if err != nil {
			return nil, fmt.Errorf("store: database is corrupt (%v) and could not be moved aside: %w", cause, err)
		}
		slog.Warn("index.db was corrupt; moved it aside and started a fresh one",
			"moved_to", recovered, "cause", cause.Error())
		if err := securePrivate(dataDir, path); err != nil {
			return nil, err
		}
		d, err = openFile(dataDir, path)
	}
	if err != nil {
		return nil, err
	}
	d.recovered = recovered
	d.startWriter()

	// A fresh database has no settings. If settings.json exists, load it back.
	if err := d.restoreSettings(context.Background()); err != nil {
		slog.Warn("could not restore settings from settings.json", "err", err)
	}
	return d, nil
}

// Recovered returns the path the corrupt database was moved to during Open, or "".
func (d *DB) Recovered() string { return d.recovered }

// Path returns the database file path.
func (d *DB) Path() string { return d.path }

// Dir returns the data dir.
func (d *DB) Dir() string { return d.dir }

// Read returns the read pool. It is query_only: a write on it fails.
// Use Write for anything that changes data.
func (d *DB) Read() *sql.DB { return d.r }

// Write runs fn inside one transaction on the single writer goroutine and waits for it.
//
//   - Calls from many goroutines are serialised, one transaction at a time.
//   - Each call is its own transaction. If fn returns an error (or panics), that call is rolled
//     back and the error is returned. Other callers are not affected.
//   - If ctx ends before fn has started, fn never runs and ctx.Err() is returned at once.
//     If fn is already running, Write waits for it (the transaction uses ctx, so it stops soon).
//     So fn never runs after Write has returned, and it is safe for fn to use the caller's variables.
//   - fn must not call Write again (that would wait on itself).
func (d *DB) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	j := &job{ctx: ctx, fn: fn, done: make(chan error, 1)}

	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return ErrClosed
	}
	select {
	case d.jobs <- j:
		d.mu.RUnlock()
	case <-ctx.Done():
		d.mu.RUnlock()
		return ctx.Err()
	}
	select {
	case err := <-j.done:
		return err
	case <-ctx.Done():
		if j.state.CompareAndSwap(jobQueued, jobCancelled) {
			return ctx.Err()
		}
		return <-j.done // it is running already
	}
}

// Close finishes all queued writes, then closes both pools. It is safe to call more than once.
func (d *DB) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	close(d.jobs)
	d.mu.Unlock()

	<-d.loopDone // queue drained

	// Fold the WAL back into the main file so the data dir is tidy. Best effort.
	_, _ = d.w.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	err1 := d.r.Close()
	err2 := d.w.Close()
	return errors.Join(err1, err2)
}

func (d *DB) startWriter() {
	d.jobs = make(chan *job, 64)
	d.loopDone = make(chan struct{})
	go d.writerLoop()
}

func (d *DB) writerLoop() {
	defer close(d.loopDone)
	for j := range d.jobs {
		if !j.state.CompareAndSwap(jobQueued, jobRunning) {
			continue // the caller gave up before we started
		}
		j.done <- d.runJob(j)
	}
}

func (d *DB) runJob(j *job) (err error) {
	if err := j.ctx.Err(); err != nil {
		return err
	}
	tx, err := d.w.BeginTx(j.ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			err = fmt.Errorf("store: write panicked: %v", r)
		}
	}()
	if err := j.fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// opening, checking, recovering

// openFile opens the writer and the read pool, checks the file, and migrates it.
// A "this is not a database" failure is returned wrapped in errCorrupt.
func openFile(dataDir, path string) (*DB, error) {
	ctx := context.Background()

	w, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	fail := func(err error) (*DB, error) {
		_ = w.Close()
		return nil, classify(err)
	}

	if err := w.PingContext(ctx); err != nil {
		return fail(err)
	}
	if err := quickCheck(ctx, w); err != nil {
		return fail(err)
	}
	if err := migrate(ctx, w); err != nil {
		return fail(err)
	}

	r, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return fail(err)
	}
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	r.SetMaxOpenConns(n)
	r.SetMaxIdleConns(n)
	r.SetConnMaxLifetime(0)
	if err := r.PingContext(ctx); err != nil {
		_ = r.Close()
		return fail(err)
	}
	return &DB{dir: dataDir, path: path, w: w, r: r}, nil
}

// quickCheck runs PRAGMA quick_check. Anything but a single "ok" means corrupt.
func quickCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check(1)")
	if err != nil {
		return err
	}
	defer rows.Close()
	var first string
	if rows.Next() {
		if err := rows.Scan(&first); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if first != "ok" {
		return fmt.Errorf("%w: quick_check said %q", errCorrupt, first)
	}
	return nil
}

// classify wraps SQLITE_CORRUPT and SQLITE_NOTADB errors in errCorrupt.
// Every other error is returned as it is: a permission problem or a bad migration
// must not make us move a good database aside.
func classify(err error) error {
	if err == nil || errors.Is(err, errCorrupt) {
		return err
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 11, 26: // SQLITE_CORRUPT, SQLITE_NOTADB
			return fmt.Errorf("%w: %v", errCorrupt, err)
		}
	}
	return err
}

// moveAside renames index.db (and its -wal and -shm files) to *.corrupt-<timestamp>.
// A leftover -wal file from the old database must never be applied to the new one.
func moveAside(path string) (string, error) {
	stamp := time.Now().Format("20060102T150405")
	suffix := ".corrupt-" + stamp
	for i := 1; ; i++ { // two recoveries in one second must not overwrite each other
		if _, err := os.Lstat(path + suffix); errors.Is(err, os.ErrNotExist) {
			break
		}
		suffix = fmt.Sprintf(".corrupt-%s-%d", stamp, i)
	}
	moved := path + suffix
	if err := os.Rename(path, moved); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, side := range []string{"-wal", "-shm"} {
		if err := os.Rename(path+side, path+side+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return moved, nil
}

// dsn builds the modernc DSN. The pragmas are applied on every new connection.
func dsn(path string, writer bool) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	if writer {
		q.Add("_pragma", "journal_mode(WAL)")
		q.Set("_txlock", "immediate") // take the write lock at BEGIN, so a write never fails halfway with BUSY
	} else {
		q.Add("_pragma", "query_only(ON)") // the read pool can never write
	}
	return "file:" + escapeURIPath(path) + "?" + q.Encode()
}

// escapeURIPath escapes the three characters that have a meaning inside a SQLite file: URI.
func escapeURIPath(p string) string {
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")
	return r.Replace(p)
}

// securePrivate makes dataDir 0700 and the database files 0600 (creating an empty
// index.db so SQLite inherits the mode for -wal and -shm).
func securePrivate(dataDir, path string) error {
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return fmt.Errorf("store: chmod data dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("store: create %s: %w", path, err)
	}
	_ = f.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("store: chmod %s: %w", p, err)
		}
	}
	return nil
}
