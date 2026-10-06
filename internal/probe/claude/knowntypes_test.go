package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestBookkeepingRecordsAreNotUnknown: the ordinary bookkeeping record types of
// real transcripts are known (and ignored), so Unknown stays 0 for them. An
// invented type is still counted, and the session still parses.
func TestBookkeepingRecordsAreNotUnknown(t *testing.T) {
	bookkeeping := []string{
		"attachment", "system", "queue-operation", "last-prompt", "mode", "atis-latch", "ai-title",
		"permission-mode", "file-history-snapshot", "frame-link", "summary", "progress",
		// also seen in real transcripts
		"file-history-delta", "worktree-state", "relocated", "cost-state", "fork-context-ref",
		"continued-in", "artifact-autoreact-ledger", "artifact-comment-monitor", "agent-name",
		"custom-title", "pr-link", "tag",
	}
	for _, typ := range bookkeeping {
		if !knownTypes[typ] {
			t.Errorf("%q is not a known type", typ)
		}
	}

	write := func(extra ...string) File {
		t.Helper()
		prof := t.TempDir()
		dir := filepath.Join(prof, "projects", "-fx-projects-personal-alpha")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		cwd := fxRoot + "/personal/alpha"
		var b strings.Builder
		fmt.Fprintf(&b, `{"type":"user","cwd":%q,"sessionId":"s-kt","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"Hello there"}}`+"\n", cwd)
		types := append([]string{}, bookkeeping...)
		sort.Strings(types)
		for n, typ := range append(types, extra...) {
			fmt.Fprintf(&b, `{"type":%q,"sessionId":"s-kt","timestamp":"2026-09-01T10:00:%02dZ","content":"x"}`+"\n", typ, n%60)
		}
		fmt.Fprintf(&b, `{"type":"assistant","cwd":%q,"sessionId":"s-kt","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Hi."}]}}`+"\n", cwd)
		if err := os.WriteFile(filepath.Join(dir, "s-kt.jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		files, err := Discover([]string{prof})
		if err != nil {
			t.Fatal(err)
		}
		return findFile(t, files, "s-kt")
	}

	s := newTestScanner()
	r := parseOne(t, s, []File{write()}, "s-kt")
	if r.Unknown != 0 || r.Skipped != 0 {
		t.Fatalf("known types only: unknown %d skipped %d", r.Unknown, r.Skipped)
	}
	if r.Session.Turns != 2 || r.Session.ProjectID != "personal/alpha" {
		t.Fatalf("session %+v", r.Session)
	}

	r = parseOne(t, s, []File{write("brand-new-record-type")}, "s-kt")
	if r.Unknown != 1 {
		t.Fatalf("invented type: unknown %d, want 1", r.Unknown)
	}
	if r.Session.Turns != 2 {
		t.Fatalf("an unknown record broke parsing: %+v", r.Session)
	}
}
