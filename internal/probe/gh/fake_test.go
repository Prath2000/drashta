package gh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeScript is a stand-in for the real gh. It logs every call and prints canned
// files that sit next to it. A file called auth_fail, pr_fail, issue_fail or
// run_fail makes that call exit 1 and print the file's text on stderr.
const fakeScript = `#!/bin/sh
d=$(dirname "$0")
echo "$@" >> "$d/calls.log"
env | sort > "$d/env.$$.log"
fail() {
  if [ -f "$d/$1_fail" ]; then cat "$d/$1_fail" >&2; exit 1; fi
}
case "$1 $2" in
  "auth status") fail auth; exit 0 ;;
  "pr list") fail pr; cat "$d/pr.json" ;;
  "issue list") fail issue; cat "$d/issue.json" ;;
  "run list") fail run; cat "$d/run.json" ;;
  *) echo "fake gh: unexpected call: $*" >&2; exit 2 ;;
esac
`

// fakeGH builds a fake gh in a temp dir and returns a Client for it plus the dir.
// files are written next to the script (pr.json, issue.json, run.json, *_fail).
func fakeGH(t *testing.T, files map[string]string) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	if err := os.WriteFile(bin, []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// Defaults: the canned PR and run files, an empty issue list.
	defaults := map[string]string{
		"pr.json":    readTestdata(t, "pr.json"),
		"run.json":   readTestdata(t, "run.json"),
		"issue.json": "[]",
	}
	for k, v := range files {
		defaults[k] = v
	}
	for name, body := range defaults {
		if body == "-" { // "-" removes a default file
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Client{Bin: bin}, dir
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// calls returns the logged calls of the fake, one string per call.
func calls(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// countCalls counts logged calls that start with prefix.
func countCalls(t *testing.T, dir, prefix string) int {
	n := 0
	for _, c := range calls(t, dir) {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// issuesJSON builds n open issues, issue 1 the oldest.
func issuesJSON(n int) string {
	var parts []string
	for i := 1; i <= n; i++ {
		parts = append(parts, fmt.Sprintf(
			`{"number":%d,"title":"Issue %d","state":"OPEN","updatedAt":"2026-09-01T%02d:%02d:00Z","url":"https://github.com/acme/widgets/issues/%d"}`,
			i, i, i/60, i%60, i))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
