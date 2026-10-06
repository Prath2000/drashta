package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "drashta ") {
		t.Errorf("-version: %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Errorf("unknown command: exit %d, want 2", code)
	}
	errb.Reset()
	if code := run([]string{"-nope"}, &out, &errb); code != 2 {
		t.Errorf("unknown flag: exit %d, want 2", code)
	}
	errb.Reset()
	if code := run([]string{"reindex", "a", "b"}, &out, &errb); code != 2 {
		t.Errorf("reindex with two args: exit %d, want 2", code)
	}
}

func TestRunDispatchesTrackerCommands(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"task", "-h"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "task") {
		t.Errorf("task -h: exit %d out %q err %q", code, out.String(), errb.String())
	}
}
