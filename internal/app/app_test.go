package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/git"
)

func TestRotatingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "drashta.log")
	r, err := openRotating(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(strings.Repeat("x", 39) + "\n") // 40 bytes
	for i := 0; i < 10; i++ {
		if _, err := r.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"drashta.log", "drashta.log.1", "drashta.log.2"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
		if fi.Size() > 100 {
			t.Errorf("%s is %d bytes, over the limit", name, fi.Size())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "drashta.log.3")); err == nil {
		t.Error("keep=2 must not leave a .3 file")
	}
	// Reopen appends to the existing file and keeps counting its size.
	r2, err := openRotating(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if r2.size == 0 {
		t.Error("size of the existing file not read")
	}
}

// Security M-1: the data folder becomes 0700 (also an existing 0755 one) and drashta's own
// files in it 0600, rotated logs included. A new log file is created 0600.
func TestSecurePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"config.toml", "drashta.log", "drashta.log.1", "launchd.log", "settings.json", "other.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := securePrivate(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("data dir mode %v", fi.Mode().Perm())
	}
	for _, n := range []string{"config.toml", "drashta.log", "drashta.log.1", "launchd.log", "settings.json"} {
		if fi, _ := os.Stat(filepath.Join(dir, n)); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", n, fi.Mode().Perm())
		}
	}
	if fi, _ := os.Stat(filepath.Join(dir, "other.txt")); fi.Mode().Perm() != 0o644 {
		t.Errorf("a file that is not drashta's was changed")
	}
	fresh := filepath.Join(t.TempDir(), "new", "drashta.log")
	r, err := openRotating(fresh, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o600 {
		t.Errorf("new log mode %v", fi.Mode().Perm())
	}
}

// G11: with an EMPTY PATH the git and fossil probes report Unavailable and no error.
func TestProbesEmptyPATH(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".fslckout"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	if git.Available() || fossil.Available() {
		t.Fatal("Available must be false with an empty PATH")
	}
	p := probe.Project{ID: "x", Name: "x", Group: "g", Path: dir}
	ctx := context.Background()

	gr, err := git.Scan(ctx, p)
	if err != nil {
		t.Fatalf("git.Scan error: %v", err)
	}
	if gr.Unavailable == nil || gr.Unavailable.Reason == "" {
		t.Errorf("git.Scan: want Unavailable, got %+v", gr)
	}
	fr, err := fossil.Scan(ctx, p)
	if err != nil {
		t.Fatalf("fossil.Scan error: %v", err)
	}
	if fr.Unavailable == nil || fr.Unavailable.Reason == "" {
		t.Errorf("fossil.Scan: want Unavailable, got %+v", fr)
	}
}
