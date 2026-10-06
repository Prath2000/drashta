// Package runx runs external commands for the probes.
//
// Rules (03-architecture.md section 3): exec.CommandContext, never a shell,
// a default 10 s timeout, stdout capped at 8 MB, LC_ALL=C.
package runx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const (
	// DefaultTimeout applies when the caller's context has no earlier deadline.
	DefaultTimeout = 10 * time.Second
	// MaxStdout is the stdout cap in bytes. Output past it is dropped and Truncated is set.
	MaxStdout = 8 << 20
	maxStderr = 64 << 10
)

// Options tune one call. The zero value is valid.
type Options struct {
	Dir     string        // working directory
	Env     []string      // extra KEY=VALUE entries added after the inherited environment
	Timeout time.Duration // 0 means DefaultTimeout
	MaxOut  int           // 0 means MaxStdout
}

// Result is what a finished command left behind.
type Result struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int  // -1 when the process did not start or was killed
	Truncated bool // stdout hit the cap
}

// ErrNotFound is returned when the binary is not on PATH.
var ErrNotFound = errors.New("runx: binary not found")

// capWriter keeps at most max bytes and remembers that it dropped some.
type capWriter struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	room := w.max - w.buf.Len()
	if room <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		w.buf.Write(p[:room])
		w.truncated = true
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

// Run executes name with args. A non-zero exit code is not an error: the
// caller reads Result.ExitCode. An error means the command could not run
// (not found, timeout, context cancelled).
func Run(ctx context.Context, o Options, name string, args ...string) (Result, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	to := o.Timeout
	if to <= 0 {
		to = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	max := o.MaxOut
	if max <= 0 {
		max = MaxStdout
	}
	out := &capWriter{max: max}
	errw := &capWriter{max: maxStderr}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = o.Dir
	cmd.Env = append(append(os.Environ(), "LC_ALL=C"), o.Env...)
	cmd.Stdout = out
	cmd.Stderr = errw

	runErr := cmd.Run()
	res := Result{Stdout: out.buf.Bytes(), Stderr: errw.buf.Bytes(), Truncated: out.truncated}
	if runErr == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) && ctx.Err() == nil {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	res.ExitCode = -1
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	return res, runErr
}

// Available reports whether a binary is on PATH (used at start to mark probes unavailable).
func Available(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
