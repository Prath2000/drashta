package autolink

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A large project: 200 commits, 60 tasks, 3000 sessions with 50 user turns of 500 characters.
func BenchmarkLinksLargeProject(b *testing.B) {
	in := Input{ProjectID: "p"}
	for i := 1; i <= 60; i++ {
		in.Tasks = append(in.Tasks, TaskInfo{ID: fmt.Sprintf("T-%d", i)})
	}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("%040x", i+1)
		in.Commits = append(in.Commits, commit(id, fmt.Sprintf("change %d for T-%d", i, i%60+1), t0.Add(time.Duration(i)*time.Minute), "a.go"))
	}
	text := strings.Repeat("some ordinary words about the work and nothing else ", 10)[:490] + " T-5"
	for i := 0; i < 3000; i++ {
		s := SessionInfo{
			ID: fmt.Sprintf("%08x-1111-4222-8333-444455556666", i+1), ProjectID: "p",
			Start: t0, End: t0.Add(time.Hour), Edited: []string{"a.go"},
		}
		for j := 0; j < 50; j++ {
			s.UserTexts = append(s.UserTexts, text)
		}
		in.Sessions = append(in.Sessions, s)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Links(in)
	}
}
