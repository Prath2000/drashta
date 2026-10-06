package claude

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSynthetic writes an invented transcript of about size bytes: a mix of
// user text, assistant text with tool calls, and large tool results.
func writeSynthetic(tb testing.TB, size int) string {
	tb.Helper()
	dir := filepath.Join(tb.TempDir(), "projects", "-fx-projects-personal-alpha")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "s-bench.jsonl")
	fh, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	w := bufio.NewWriter(fh)
	output := strings.Repeat(`line of tool output with \"quotes\" and /fx/projects/personal/beta/x.go\n`, 400) // ~30 KB
	words := strings.Repeat("please change the parser so it handles more cases ", 6)
	n := 0
	for i := 0; n < size; i++ {
		ts := fmt.Sprintf("2026-09-01T10:%02d:%02d.000Z", (i/60)%60, i%60)
		lines := []string{
			fmt.Sprintf(`{"parentUuid":"p%d","isSidechain":false,"cwd":"/fx/projects/personal/alpha","sessionId":"s-bench","type":"user","message":{"role":"user","content":"%s %d"},"uuid":"u%d","timestamp":"%s"}`, i, words, i, i, ts),
			fmt.Sprintf(`{"parentUuid":"u%d","cwd":"/fx/projects/personal/alpha","sessionId":"s-bench","type":"assistant","message":{"id":"m%d","type":"message","role":"assistant","model":"x","content":[{"type":"text","text":"%s"},{"type":"tool_use","id":"t%d","name":"Bash","input":{"command":"grep -rn foo /fx/projects/personal/beta/src","description":"search"}},{"type":"tool_use","id":"e%d","name":"Edit","input":{"file_path":"/fx/projects/personal/alpha/f%d.go","old_string":"a","new_string":"b"}}]},"uuid":"a%d","timestamp":"%s"}`, i, i, words, i, i, i%50, i, ts),
			fmt.Sprintf(`{"parentUuid":"a%d","cwd":"/fx/projects/personal/alpha","sessionId":"s-bench","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t%d","content":"%s"}]},"uuid":"r%d","timestamp":"%s","toolUseResult":{"stdout":"ok"}}`, i, i, output, i, ts),
		}
		for _, l := range lines {
			w.WriteString(l)
			w.WriteByte('\n')
			n += len(l) + 1
		}
	}
	w.Flush()
	fh.Close()
	return path
}

// BenchmarkParse50MB parses a synthetic 50 MB file. Run:
//
//	go test ./internal/probe/claude/ -run '^$' -bench Parse50MB -benchmem
func BenchmarkParse50MB(b *testing.B) {
	path := writeSynthetic(b, 50<<20)
	s := newTestScanner()
	ctx := context.Background()
	f, err := Stat(path, "p")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(f.Size)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, err := s.Parse(ctx, f, nil, nil)
		if err != nil || r.Session.Turns == 0 {
			b.Fatal(err)
		}
	}
}

// BenchmarkTranscriptPage reads one 40-turn page from the middle of a large file.
func BenchmarkTranscriptPage(b *testing.B) {
	path := writeSynthetic(b, 10<<20)
	ctx := context.Background()
	first, err := Transcript(ctx, path, 0, 100, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Transcript(ctx, path, first.Next, 40, nil); err != nil {
			b.Fatal(err)
		}
	}
}
