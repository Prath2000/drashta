package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func toolLine(name, ts string) string {
	return `{"cwd":"/fx/projects/personal/alpha","type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"` +
		name + `","input":{}}]},"timestamp":"` + ts + `"}` + "\n"
}

// TestAppendKeepsLastRole (review I7): a run of tool calls split across two
// passes is one turn, so first pass + append equals a full parse.
func TestAppendKeepsLastRole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "-fx-projects-personal-alpha")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "s-run.jsonl")
	first := userLine("fix the parser", "2026-09-01T10:00:00Z") + toolLine("Read", "2026-09-01T10:00:01Z")
	os.WriteFile(path, []byte(first), 0o644)
	s := newTestScanner()
	ctx := context.Background()

	r1, err := s.Parse(ctx, mustStat(t, path), nil, nil)
	if err != nil || r1.Session.Turns != 2 {
		t.Fatalf("first pass: turns=%d err=%v", r1.Session.Turns, err)
	}
	appendLine(t, path, toolLine("Grep", "2026-09-01T10:00:02Z")+userLine("thanks", "2026-09-01T10:00:03Z"))
	f := mustStat(t, path)
	r2, err := s.Parse(ctx, f, &r1.Session, &r1.State)
	if err != nil || r2.Mode != Append {
		t.Fatalf("append: mode=%v err=%v", r2.Mode, err)
	}
	full, err := s.Parse(ctx, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if full.Session.Turns != 3 || r2.Session.Turns != full.Session.Turns {
		t.Fatalf("turns: append=%d full=%d, want 3 and equal", r2.Session.Turns, full.Session.Turns)
	}
}

// TestSubagentFirstSeenEmpty (review M3): a subagent file that was empty on
// the first pass is counted once, not again when it grows.
func TestSubagentFirstSeenEmpty(t *testing.T) {
	prof := filepath.Join(t.TempDir(), "profile")
	dir := filepath.Join(prof, "projects", "-fx-projects-personal-alpha")
	sub := filepath.Join(dir, "s-fam", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "s-fam.jsonl"), []byte(userLine("hello", "2026-09-01T10:00:00Z")), 0o644)
	subPath := filepath.Join(sub, "agent-z.jsonl")
	os.WriteFile(subPath, nil, 0o644)

	s := newTestScanner()
	ctx := context.Background()
	family := func() Family {
		files, _ := Discover([]string{prof})
		for _, f := range Group(files) {
			if f.ID == "s-fam" {
				return f
			}
		}
		t.Fatal("family not found")
		return Family{}
	}
	fr, err := s.ParseFamily(ctx, family(), nil, nil)
	if err != nil || fr.Session.Subagents != 1 {
		t.Fatalf("first pass: subagents=%d err=%v", fr.Session.Subagents, err)
	}
	appendLine(t, subPath, toolLine("Grep", "2026-09-01T10:00:05Z"))
	prev := fr.Session
	fr2, err := s.ParseFamily(ctx, family(), &prev, fr.States)
	if err != nil || fr2.Rebuilt {
		t.Fatalf("append pass: rebuilt=%v err=%v", fr2.Rebuilt, err)
	}
	if fr2.Session.Subagents != 1 || fr2.Session.Tools["Grep"] != 1 {
		t.Fatalf("append pass: subagents=%d tools=%v, want 1", fr2.Session.Subagents, fr2.Session.Tools)
	}
}
