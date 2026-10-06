package autolink

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

func ids(ms []mention) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestScanMentions(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"T-12", []string{"T-12"}},
		{"t-12", []string{"T-12"}},
		{"fix T-12.", []string{"T-12"}},
		{"(t-12)", []string{"T-12"}},
		{"[T-12]", []string{"T-12"}},
		{"T-12, T-14 and t-3", []string{"T-12", "T-14", "T-3"}},
		{"T-12/T-14", []string{"T-12", "T-14"}},
		{"foo-T-12", []string{"T-12"}},
		{"foo.T-12", []string{"T-12"}},
		{"line one\nT-5\nline three", []string{"T-5"}},
		{"ST-12", nil},
		{"AT-12 and uT-3", nil},
		{"T-12abc", nil},
		{"T-12_x", nil},
		{"_T-12", nil},
		{"T-", nil},
		{"T-x", nil},
		{"T 12", nil},
		{"T-1234567890123", nil},
		{"", nil},
		{"T-123 is not T-12", []string{"T-123", "T-12"}},
	}
	for _, c := range cases {
		got := ids(scanMentions(c.in))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("scanMentions(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The hand-written scanner must agree with the regexp from 04 section 3.
func TestScanMatchesDocRegexp(t *testing.T) {
	re := regexp.MustCompile(`\b[Tt]-\d+\b`)
	rng := rand.New(rand.NewSource(42))
	alphabet := []byte("Tt-12 _:.a/\n")
	for n := 0; n < 20000; n++ {
		b := make([]byte, rng.Intn(14))
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		s := string(b)
		var want []string
		for _, m := range re.FindAllString(s, -1) {
			want = append(want, strings.ToUpper(m))
		}
		got := ids(scanMentions(s))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("input %q: scanner %v, regexp %v", s, got, want)
		}
	}
}

func TestScanQualifiers(t *testing.T) {
	cases := []struct {
		in     string
		qual   string
		prefix bool
	}{
		{"T-12", "", false},
		{"Task: T-12", "", false},
		{"task:T-12", "", true},
		{"TASK:t-12", "", true},
		{"ledger:T-12", "ledger", false},
		{"ledger:task:T-12", "ledger", true},
		{"see work.app:T-12", "work.app", false},
		{"Refs:T-12", "Refs", false},
		{"subtask:T-12", "subtask", false},
		{"http://localhost:T-12", "localhost", false},
		{": T-12", "", false},
		{":T-12", "", false},
		{"-:T-12", "", false},
	}
	for _, c := range cases {
		ms := scanMentions(c.in)
		if len(ms) != 1 {
			t.Errorf("%q: %d mentions", c.in, len(ms))
			continue
		}
		if ms[0].Qual != c.qual || ms[0].TaskPrefix != c.prefix {
			t.Errorf("%q: qual=%q prefix=%v, want %q %v", c.in, ms[0].Qual, ms[0].TaskPrefix, c.qual, c.prefix)
		}
	}
}

func TestEditedTaskID(t *testing.T) {
	ok := map[string]string{
		".drashta/tasks/T-12.md":           "T-12",
		"./.drashta/tasks/T-7.md":          "T-7",
		".drashta/snapshots/T-12/v3.png":   "T-12",
		".drashta/snapshots/T-12/v3.md":    "T-12",
		".drashta/snapshots/T-101/v10.png": "T-101",
	}
	for in, want := range ok {
		if got, found := editedTaskID(in); !found || got != want {
			t.Errorf("editedTaskID(%q) = %q, %v", in, got, found)
		}
	}
	for _, in := range []string{
		"", "src/main.go", ".drashta/notes/N-1.md", ".drashta/features/F-1.md",
		".drashta/tasks/T-12.md.1234", ".drashta/tasks/T-12.txt", ".drashta/tasks/T-12/x.md",
		".drashta/snapshots/inbox/T-12--a.png", ".drashta/snapshots/T-12", ".drashta/tasks/",
		"~/projects/g/other/.drashta/tasks/T-12.md", "/abs/.drashta/tasks/T-12.md",
		"sub/.drashta/tasks/T-12.md", ".drashta/tasks/X-1.md",
	} {
		if got, found := editedTaskID(in); found {
			t.Errorf("editedTaskID(%q) = %q, want no match", in, got)
		}
	}
}

func TestIsDrashtaCommand(t *testing.T) {
	yes := []string{
		"drashta task set T-12 status=review",
		"/opt/homebrew/bin/drashta snap T-12 shot.png",
		"cd x && drashta note T-12 \"hi\"",
		"(drashta task link T-12 commit:abc1234)",
	}
	no := []string{"echo T-12", "go run ./cmd/drashta-old T-12", "mydrashta T-12", "cat drashta.log T-12", ""}
	for _, c := range yes {
		if !isDrashtaCommand(c) {
			t.Errorf("want true: %q", c)
		}
	}
	for _, c := range no {
		if isDrashtaCommand(c) {
			t.Errorf("want false: %q", c)
		}
	}
}

func TestClosesBefore(t *testing.T) {
	yes := []string{
		"Fixes T-12", "fixes t-12", "Closes T-12", "closed T-12", "Resolves: T-12",
		"Fixes task:T-12", "closes  T-12", "done. Fixes T-12 now", "Fixes:T-12",
		"fix\tT-12",
	}
	no := []string{
		"T-12", "Refs T-12", "prefixes T-12", "unfixes T-12", "Fixes T-14, T-12",
		"Fixes #3 and T-12", "closes the T-12 chapter", "Fixes: ledger: T-12", "see T-12 (fixes)",
		"Closes\nT-12",
	}
	for _, s := range yes {
		ms := scanMentions(s)
		found := false
		for _, m := range ms {
			if m.ID == "T-12" && closesBefore(s, m) {
				found = true
			}
		}
		if !found {
			t.Errorf("want a closing mention in %q", s)
		}
	}
	for _, s := range no {
		for _, m := range scanMentions(s) {
			if m.ID == "T-12" && closesBefore(s, m) {
				t.Errorf("did not want a closing mention in %q", s)
			}
		}
	}
}
