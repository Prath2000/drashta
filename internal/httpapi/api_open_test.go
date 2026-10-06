package httpapi

import (
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Prath2000/drashta/internal/actions"
)

func TestTrkResolveEveryType(t *testing.T) {
	f := newTrkFix(t)
	// A snapshot and a note so those types exist.
	trkJSON(t, f.upload("T-1", "a.png", []byte(trkPNG), nil), 201)
	cases := []struct {
		ref, typ, title, action string
	}{
		{"alpha:project:alpha", "project", "alpha", "finder"},
		{"project:alpha", "project", "alpha", "finder"},
		{"alpha:file:src/x.go#L3", "file", "x.go", "editor"},
		{"alpha:doc:README.md", "doc", "Alpha", "editor"},
		{"alpha:image:shots/a.png", "image", "a.png", "default"},
		{"alpha:file:shots", "folder", "shots", "app"},
		{"alpha:commit:b7dc920", "commit", "T-1: cache layer", "terminal"},
		{"beta:commit:cccc0000dd", "commit", "fossil checkin", "terminal"},
		{"session:" + trkUUID, "session", "Build the cache", "resume"},
		{"session:121c3b19", "session", "Build the cache", "resume"},
		{"server:5173", "server", "Alpha dev", "safari"},
		{"alpha:task:T-1", "task", "Cache layer", "editor"},
		{"alpha:feature:F-1", "feature", "Offline sync", "editor"},
		{"alpha:milestone:M-1", "milestone", "First release", "editor"},
		{"alpha:snapshot:T-1/v1", "snapshot", "Cache layer", "default"},
		{"alpha:note:N-1", "note", "Is the cache bounded?", "editor"},
	}
	for _, c := range cases {
		m := trkJSON(t, f.do("GET", "/api/resolve?ref="+url.QueryEscape(c.ref), nil), 200)
		if m["type"] != c.typ || m["title"] != c.title {
			t.Errorf("%s: type %v title %v", c.ref, m["type"], m["title"])
			continue
		}
		acts, _ := m["nativeActions"].([]any)
		if len(acts) == 0 {
			t.Errorf("%s: no native actions", c.ref)
			continue
		}
		a0 := acts[0].(map[string]any)
		if a0["action"] != c.action || a0["command"] == "" || m["nativeLabel"] != a0["label"] {
			t.Errorf("%s: first action %v, label %v", c.ref, a0, m["nativeLabel"])
		}
		if m["light"] != false || m["notes"] == nil || m["tasks"] == nil {
			t.Errorf("%s: shape %v", c.ref, m)
		}
	}
	// Server key form.
	var key string
	m := trkJSON(t, f.do("GET", "/api/resolve?ref=server:5173", nil), 200)
	key = trkGet(m, "obj", "refKey").(string)
	trkJSON(t, f.do("GET", "/api/resolve?ref="+url.QueryEscape(key), nil), 200)

	// Notes and tasks on items.
	m = trkJSON(t, f.do("GET", "/api/resolve?ref="+url.QueryEscape("alpha:task:T-1"), nil), 200)
	if len(m["notes"].([]any)) != 1 {
		t.Fatalf("notes on T-1: %v", m["notes"])
	}
	m = trkJSON(t, f.do("GET", "/api/resolve?ref="+url.QueryEscape("alpha:commit:b7dc920bef0123"), nil), 200)
	if ts := m["tasks"].([]any); len(ts) != 1 || ts[0].(map[string]any)["id"] != "T-1" {
		t.Fatalf("auto-linked task: %v", m["tasks"])
	}
	m = trkJSON(t, f.do("GET", "/api/resolve?ref="+url.QueryEscape("alpha:file:src/x.go"), nil), 200)
	if ts := m["tasks"].([]any); len(ts) != 1 {
		t.Fatalf("manual-linked task: %v", m["tasks"])
	}
	// The project lists editor and GitHub; the command text is readable.
	m = trkJSON(t, f.do("GET", "/api/resolve?ref=project:alpha", nil), 200)
	got := map[string]string{}
	for _, a := range m["nativeActions"].([]any) {
		got[a.(map[string]any)["action"].(string)] = a.(map[string]any)["command"].(string)
	}
	if got["editor"] != "code -g "+f.root || got["safari"] != "open -a Safari https://github.com/example/alpha" {
		t.Fatalf("project actions: %v", got)
	}
	m = trkJSON(t, f.do("GET", "/api/resolve?ref="+url.QueryEscape("alpha:commit:b7dc920"), nil), 200)
	cmd := m["nativeActions"].([]any)[0].(map[string]any)["command"].(string)
	if !strings.HasPrefix(cmd, "Terminal: cd "+f.root+" && git -c core.pager=less -c core.fsmonitor=false -C "+f.root+" show --no-ext-diff --no-textconv b7dc920bef") {
		t.Fatalf("readable command: %q", cmd)
	}
	// beta has a non-GitHub remote: no safari action.
	m = trkJSON(t, f.do("GET", "/api/resolve?ref=project:beta", nil), 200)
	for _, a := range m["nativeActions"].([]any) {
		if a.(map[string]any)["action"] == "safari" {
			t.Fatal("safari offered for a non-GitHub remote")
		}
	}

	// Unknown and bad refs.
	for ref, code := range map[string]int{
		"alpha:task:T-99": 404, "alpha:commit:abcdef9": 404, "alpha:file:nope.go": 404, "nope:task:T-1": 404,
		"server:9999": 404, "session:00000000-0000-0000-0000-000000000000": 404, "task:T-1": 400, "garbage": 400,
		"alpha:file:../etc/passwd": 400, "alpha:file:src/escape.go": 400,
	} {
		w := f.do("GET", "/api/resolve?ref="+url.QueryEscape(ref), nil)
		if w.Code != code {
			t.Errorf("%s: %d, want %d (%s)", ref, w.Code, code, w.Body.String())
		}
	}
	trkCode(t, f.do("GET", "/api/resolve", nil), 400, "")
}

func TestTrkOpen(t *testing.T) {
	f := newTrkFix(t)
	trkJSON(t, f.upload("T-1", "a.png", []byte(trkPNG), nil), 201)
	osa := func(dir string, argv ...string) []string {
		cmd, _ := actions.Config{Roots: []string{f.projects}}.Plan(actions.Target{Type: actions.TypeCommit,
			ProjectRoot: f.root, SHA: "b7dc920bef0123456789abcdef0123456789abcd", VCS: "git"}, "")
		// Same fixed script; swap the argv after "--".
		for i, a := range cmd.Args {
			if a == "--" {
				return append(append(append([]string{}, cmd.Args[:i+1]...), dir), argv...)
			}
		}
		return nil
	}
	cases := []struct {
		ref, action, name string
		args              []string
	}{
		{"alpha:file:src/x.go#L3", "", "code", []string{"-g", f.root + "/src/x.go:3"}},
		{"alpha:file:src/x.go", "finder", "open", []string{"-R", f.root + "/src/x.go"}},
		{"alpha:image:shots/a.png", "", "open", []string{"-a", "Preview", f.root + "/shots/a.png"}},
		{"alpha:commit:b7dc920", "", "osascript", osa(f.root, "git", "-c", "core.pager=less", "-c", "core.fsmonitor=false", "-C", f.root, "show", "--no-ext-diff", "--no-textconv", "b7dc920bef0123456789abcdef0123456789abcd", "--")},
		{"session:121c3b19", "resume", "osascript", osa(f.root, "env",
			"CLAUDE_CONFIG_DIR="+filepath.Join(f.home, ".claude-work"), "claude", "--resume", trkUUID)},
		{"server:5173", "", "open", []string{"-a", "Safari", "http://localhost:5173"}},
		{"alpha:task:T-1", "", "code", []string{"-g", f.root + "/.drashta/tasks/T-1.md:1"}},
		{"alpha:note:N-1", "editor", "code", []string{"-g", f.root + "/.drashta/notes/N-1.md:1"}},
		{"alpha:snapshot:T-1/v1", "", "open", []string{"-a", "Preview", f.root + "/.drashta/snapshots/T-1/v1.png"}},
		{"project:alpha", "", "open", []string{"-R", f.root}},
		{"project:alpha", "terminal", "open", []string{"-a", "Terminal", f.root}},
		{"project:alpha", "editor", "code", []string{"-g", f.root}},
		{"project:alpha", "safari", "open", []string{"-a", "Safari", "https://github.com/example/alpha"}},
	}
	for _, c := range cases {
		n := len(f.rec.Cmds)
		m := trkJSON(t, f.do("POST", "/api/open", map[string]any{"ref": c.ref, "action": c.action}), 200)
		if m["ok"] != true || len(f.rec.Cmds) != n+1 {
			t.Errorf("%s: %v", c.ref, m)
			continue
		}
		got := f.rec.Cmds[n]
		if got.Name != c.name || !reflect.DeepEqual(got.Args, c.args) {
			t.Errorf("%s %s: ran %v %v, want %v %v", c.ref, c.action, got.Name, got.Args, c.name, c.args)
		}
		if m["ran"] != got.String() {
			t.Errorf("%s: ran %q, recorded %q", c.ref, m["ran"], got.String())
		}
	}

	// Refused: nothing runs.
	n := len(f.rec.Cmds)
	for _, c := range []struct {
		body map[string]any
		code int
	}{
		{map[string]any{"ref": "project:gamma"}, 400},                            // project folder outside the roots
		{map[string]any{"ref": "gamma:file:README.md"}, 400},                     // file in that folder
		{map[string]any{"ref": "alpha:file:src/escape.go"}, 400},                 // link that leaves the project
		{map[string]any{"ref": "alpha:file:../outside/secret.txt"}, 400},         // traversal
		{map[string]any{"ref": "project:beta", "action": "safari"}, 400},         // not a GitHub remote
		{map[string]any{"ref": "alpha:commit:b7dc920", "action": "safari"}, 400}, // wrong action for the type
		{map[string]any{"ref": "alpha:file:src/x.go", "action": "rm -rf"}, 400},
		{map[string]any{"ref": "alpha:task:T-99"}, 404},
		{map[string]any{"ref": ""}, 400},
		{map[string]any{"ref": "file:src/x.go"}, 400}, // no project
		{map[string]any{"ref": "alpha:file:src/x.go", "path": "/etc/passwd", "command": "rm"}, 200},
	} {
		w := f.do("POST", "/api/open", c.body)
		if w.Code != c.code {
			t.Errorf("%v: %d, want %d (%s)", c.body, w.Code, c.code, w.Body.String())
		}
	}
	// Only the last case ran, and it opened the ref, not the path or command it was sent.
	if len(f.rec.Cmds) != n+1 || !strings.HasSuffix(f.rec.Cmds[n].Args[1], "/src/x.go:1") {
		t.Fatalf("refused cases ran something: %v", f.rec.Cmds[n:])
	}

	// A failing program gives 500 action_failed.
	f.rec.Err = errTrkTest
	trkCode(t, f.do("POST", "/api/open", map[string]any{"ref": "server:5173"}), 500, "action_failed")
}

var errTrkTest = errTrkString("boom")

type errTrkString string

func (e errTrkString) Error() string { return string(e) }
