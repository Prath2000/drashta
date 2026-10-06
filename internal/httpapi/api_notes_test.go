package httpapi

import (
	"net/url"
	"strings"
	"testing"
)

func TestTrkNotes(t *testing.T) {
	f := newTrkFix(t)
	head := f.srv.Hub().Head()

	// A line note: the anchor is the line text, read live.
	m := trkJSON(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:file:src/x.go#L3", "text": "Rename this."}), 201)
	n := m["note"].(map[string]any)
	if n["id"] != "N-2" || n["line"] != 3.0 || n["anchor"] != "func Hello() {}" || n["ref"] != "alpha:file:src/x.go#L3" ||
		n["author"] != "alice" || n["projectId"] != "alpha" {
		t.Fatalf("line note: %v", n)
	}
	stored := trkRead(t, f.root+"/.drashta/notes/N-2.md")
	if !strings.Contains(stored, "ref: file:src/x.go#L3") {
		t.Fatalf("the note should store the local ref:\n%s", stored)
	}
	if evs := f.trackerEvents(head); len(evs) != 1 || evs[0].Kind != "note" || evs[0].ID != "N-2" {
		t.Fatalf("events: %v", evs)
	}
	// A note on a secret file never copies its text.
	m = trkJSON(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:file:.env", "line": 1, "text": "Rotate this."}), 201)
	if a, ok := trkGet(m, "note", "anchor").(string); ok && a != "" {
		t.Fatalf("secret leaked into the anchor: %q", a)
	}
	if strings.Contains(trkRead(t, f.root+"/.drashta/notes/N-3.md"), "very-secret") {
		t.Fatal("secret text in the note file")
	}
	// A pin on an image and a reply.
	m = trkJSON(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:image:shots/a.png#x=0.5,y=0.25", "text": "Too dark."}), 201)
	if trkGet(m, "note", "x") != 0.5 || trkGet(m, "note", "y") != 0.25 {
		t.Fatalf("pin: %v", m)
	}
	m = trkJSON(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:task:T-1", "text": "Yes, 100 items.", "replyTo": "N-1"}), 201)
	if trkGet(m, "note", "replyTo") != "N-1" {
		t.Fatalf("reply: %v", m)
	}

	// Bad input.
	trkCode(t, f.do("POST", "/api/notes", map[string]any{"ref": "file:src/x.go", "text": "x"}), 400, "")
	trkCode(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:task:T-99", "text": "x"}), 422, "")
	trkCode(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:task:T-1", "text": " "}), 422, "")
	trkCode(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:task:T-1", "text": "x", "x": 0.4}), 422, "")
	trkCode(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:task:T-1", "text": "x", "line": 2}), 422, "")
	trkCode(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:task:T-1", "text": "x", "replyTo": "N-99"}), 422, "")
	trkCode(t, f.do("POST", "/api/notes", map[string]any{"ref": "nope:task:T-1", "text": "x"}), 404, "")
	trkCode(t, f.do("POST", "/api/notes", map[string]any{"ref": "alpha:file:../x", "text": "x"}), 400, "")

	// Lists.
	m = trkJSON(t, f.do("GET", "/api/notes?ref="+url.QueryEscape("alpha:task:T-1"), nil), 200)
	if len(m["notes"].([]any)) != 2 {
		t.Fatalf("notes on T-1: %v", m)
	}
	m = trkJSON(t, f.do("GET", "/api/notes?ref="+url.QueryEscape("file:src/x.go")+"&project=alpha", nil), 200)
	if len(m["notes"].([]any)) != 1 {
		t.Fatalf("notes on the file (any line): %v", m)
	}
	m = trkJSON(t, f.do("GET", "/api/notes?project=alpha", nil), 200)
	if len(m["notes"].([]any)) != 5 {
		t.Fatalf("project notes: %v", m)
	}

	// Resolving is allowed for anyone; the text only for the author.
	f.setAuthor("someone-else")
	m = trkJSON(t, f.do("PATCH", "/api/projects/alpha/notes/N-2", map[string]any{"resolved": true}), 200)
	if trkGet(m, "note", "resolved") != true || trkGet(m, "note", "resolvedBy") != "someone-else" {
		t.Fatalf("resolve: %v", m)
	}
	trkCode(t, f.do("PATCH", "/api/projects/alpha/notes/N-2", map[string]any{"text": "changed"}), 403, "forbidden")
	if strings.Contains(trkRead(t, f.root+"/.drashta/notes/N-2.md"), "changed") {
		t.Fatal("text changed by a non-author")
	}
	f.setAuthor("alice")
	m = trkJSON(t, f.do("PATCH", "/api/projects/alpha/notes/N-2", map[string]any{"text": "Rename it to Greet."}), 200)
	if trkGet(m, "note", "text") != "Rename it to Greet." {
		t.Fatalf("author edit: %v", m)
	}
	m = trkJSON(t, f.do("GET", "/api/notes?project=alpha&open=1", nil), 200)
	if len(m["notes"].([]any)) != 4 {
		t.Fatalf("open notes: %v", m)
	}
	trkCode(t, f.do("PATCH", "/api/projects/alpha/notes/N-2", map[string]any{"resolved": false, "text": " "}), 422, "")
	trkCode(t, f.do("PATCH", "/api/projects/alpha/notes/N-99", map[string]any{"resolved": true}), 404, "")
	trkCode(t, f.do("PATCH", "/api/projects/alpha/notes/..%2FN-1", map[string]any{"resolved": true}), 400, "")
	trkCode(t, f.do("PATCH", "/api/projects/alpha/notes/N-2", map[string]any{}), 400, "")
}
