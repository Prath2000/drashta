package actions

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanDesktop(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	script := filepath.Join(root, "plash-mode")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Roots: []string{root}, DesktopScript: script}

	for _, m := range DesktopModes {
		got, err := cfg.PlanDesktop(m)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if want := (Command{Name: script, Args: []string{m}}); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	for _, tc := range []struct {
		name string
		cfg  Config
		mode string
		want error
	}{
		{"unknown mode", cfg, "-rf", ErrInvalid},
		{"empty mode", cfg, "", ErrInvalid},
		{"no script", Config{Roots: []string{root}}, "browse", ErrAction},
		{"outside roots", Config{Roots: []string{filepath.Join(root, "sub")}, DesktopScript: script}, "browse", ErrForbidden},
		{"missing script", Config{Roots: []string{root}, DesktopScript: filepath.Join(root, "nope")}, "browse", ErrNotFound},
	} {
		if _, err := tc.cfg.PlanDesktop(tc.mode); !errors.Is(err, tc.want) {
			t.Errorf("%s: err %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestReadDesktopMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state")
	if got := ReadDesktopMode(p); got != "" {
		t.Fatalf("missing file: %q", got)
	}
	for text, want := range map[string]string{"browse\n": "browse", "blank": "blank", "rm -rf\n": ""} {
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ReadDesktopMode(p); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
}
