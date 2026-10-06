package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Log rotation (03-architecture.md section 9): launchd does not rotate, so drashta
// rotates its own log file. When drashta.log would grow past maxBytes it becomes
// drashta.log.1, the old .1 becomes .2, and so on; at most keep old files stay.
const (
	logMaxBytes = 10 << 20
	logKeep     = 3
)

// rotatingFile is an io.Writer over one log file that rotates by size.
// It is safe for concurrent use (slog handlers already serialise writes, but
// the lock keeps rotation and writes apart).
type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

// openRotating opens (or creates) path for appending.
func openRotating(path string, max int64, keep int) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &rotatingFile{path: path, max: max, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // private (security M-1)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, fi.Size()
	return nil
}

// Write appends p, rotating first when p would push the file past the limit.
func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.max {
		if err := r.rotate(); err != nil {
			// Keep logging into the old file rather than losing lines.
			fmt.Fprintf(os.Stderr, "drashta: log rotation failed: %v\n", err)
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate shifts drashta.log.N up by one, drops the oldest, and starts a new file.
func (r *rotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	r.f = nil
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, r.keep))
	for n := r.keep - 1; n >= 1; n-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, n), fmt.Sprintf("%s.%d", r.path, n+1))
	}
	if r.keep >= 1 {
		if err := os.Rename(r.path, r.path+".1"); err != nil && !os.IsNotExist(err) {
			_ = r.open() // reopen the old file so writes continue
			return err
		}
	} else {
		_ = os.Remove(r.path)
	}
	return r.open()
}

// Close closes the file.
func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
