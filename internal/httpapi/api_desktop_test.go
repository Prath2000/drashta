package httpapi

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Prath2000/drashta/internal/actions"
)

func TestDesktopModes(t *testing.T) {
	f := newTrkFix(t)
	script := filepath.Join(f.root, "desktop", "plash-mode")

	// No script yet: not available, mode unknown, and a switch is refused.
	m := trkJSON(t, f.do("GET", "/api/desktop", nil), 200)
	if m["available"] != false || m["mode"] != "" {
		t.Fatalf("no script: %v", m)
	}
	trkCode(t, f.do("POST", "/api/desktop", map[string]any{"mode": "browse"}), 404, "not_found")

	trkWrite(t, script, "#!/bin/sh\n")
	trkWrite(t, filepath.Join(f.home, "plash-mode.state"), "wallpaper\n")
	m = trkJSON(t, f.do("GET", "/api/desktop", nil), 200)
	if m["available"] != true || m["mode"] != "wallpaper" {
		t.Fatalf("with script: %v", m)
	}

	// A known mode runs "<script> <mode>" and nothing else.
	trkJSON(t, f.do("POST", "/api/desktop", map[string]any{"mode": "blank"}), 200)
	want := []actions.Command{{Name: script, Args: []string{"blank"}}}
	if !reflect.DeepEqual(f.rec.Cmds, want) {
		t.Fatalf("ran %v, want %v", f.rec.Cmds, want)
	}

	// Anything else is refused before it runs.
	for _, body := range []map[string]any{{"mode": "rm -rf /"}, {"mode": ""}, {}} {
		trkCode(t, f.do("POST", "/api/desktop", body), 400, "bad_request")
	}
	if len(f.rec.Cmds) != 1 {
		t.Fatalf("refused modes ran something: %v", f.rec.Cmds)
	}

	// A failing script gives 500 action_failed.
	f.rec.Err = errTrkTest
	trkCode(t, f.do("POST", "/api/desktop", map[string]any{"mode": "browse"}), 500, "action_failed")
}
