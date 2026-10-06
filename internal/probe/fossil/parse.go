package fossil

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/probe"
)

// timelineFormat is the -F value of `fossil timeline`. The separator is the
// real byte 0x1f (fossil has no %x escape). One check-in per line: fossil
// already turns line breaks and tabs inside a comment into spaces (%c).
const timelineFormat = "%H\x1f%a\x1f%d\x1f%c"

var (
	hashRe    = regexp.MustCompile(`^[0-9a-f]{6,64}$`)
	credRe    = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.\-]*://)[^/@]+@`)
	numstatRe = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(.+)$`)
	totalRe   = regexp.MustCompile(`^TOTAL over \d+ changed files?$`)
)

// stripCreds removes any user[:password]@ part from a URL.
func stripCreds(u string) string {
	return credRe.ReplaceAllString(strings.TrimSpace(u), "$1")
}

func cleanText(s string) string { return strings.ToValidUTF8(s, "�") }

func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// parseTimeline reads the output of `fossil timeline --utc -F timelineFormat`.
// fossil prints dates as "YYYY-MM-DD HH:MM:SS"; with --utc they are UTC.
func parseTimeline(out []byte) []probe.Commit {
	var commits []probe.Commit
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "\x1f", 4)
		if len(f) < 4 || !hashRe.MatchString(f[0]) {
			continue
		}
		t, _ := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(f[2]), time.UTC)
		msg := strings.Join(strings.Fields(cleanText(f[3])), " ")
		commits = append(commits, probe.Commit{
			ID:     f[0],
			VCS:    "fossil",
			Author: cleanText(f[1]),
			Time:   t,
			Msg:    cutBytes(msg, maxMsgBytes),
		})
	}
	return commits
}

// infoValues returns every value of a "key:   value" line in `fossil info`
// output, in order. (A merge check-in has several "parent:" lines.)
func infoValues(out []byte, key string) []string {
	var vals []string
	prefix := key + ":"
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, prefix); ok {
			vals = append(vals, strings.TrimSpace(v))
		}
	}
	return vals
}

// firstWord returns the first space separated word of s ("" when empty).
func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// parseNumstat reads `fossil diff --numstat`:
//
//	INSERTED    DELETED
//	       1          0 a.txt
//	       2          0 TOTAL over 2 changed files
//
// It returns the per-file rows. The TOTAL row is dropped.
func parseNumstat(out []byte) []probe.CommitFile {
	var files []probe.CommitFile
	for _, line := range strings.Split(string(out), "\n") {
		m := numstatRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil || totalRe.MatchString(strings.TrimSpace(m[3])) {
			continue
		}
		plus, _ := strconv.Atoi(m[1])
		minus, _ := strconv.Atoi(m[2])
		files = append(files, probe.CommitFile{Path: cleanText(m[3]), Plus: plus, Minus: minus})
	}
	return files
}

// changeState maps the keyword of `fossil changes` to a DirtyFile state.
var changeState = map[string]string{
	"EDITED":               "M",
	"ADDED":                "A",
	"DELETED":              "D",
	"MISSING":              "D", // tracked file is gone from the disk
	"RENAMED":              "R",
	"CONFLICT":             "U",
	"UPDATED_BY_MERGE":     "M",
	"UPDATED_BY_INTEGRATE": "M",
	"ADDED_BY_MERGE":       "A",
	"ADDED_BY_INTEGRATE":   "A",
	"EXECUTABLE":           "M",
	"UNEXEC":               "M",
	"SYMLINK":              "M",
	"UNLINK":               "M",
	"NOT_A_FILE":           "M",
}

// parseChanges reads `fossil changes --rel-paths`. Lines look like
// "EDITED     src/a.go" or "RENAMED    old.txt  ->  new.txt". Other lines (for
// example "MERGED_WITH <hash>") are ignored. The second result says the list was cut at maxDirty.
func parseChanges(out []byte) ([]probe.DirtyFile, bool) {
	var files []probe.DirtyFile
	for _, line := range strings.Split(string(out), "\n") {
		code, rest, ok := strings.Cut(strings.TrimRight(line, "\r"), " ")
		if !ok {
			continue
		}
		state, known := changeState[code]
		if !known {
			continue
		}
		path := strings.TrimLeft(rest, " ")
		if code == "RENAMED" {
			if i := strings.LastIndex(path, "->"); i >= 0 {
				path = strings.TrimSpace(path[i+2:])
			}
		}
		if path == "" {
			continue
		}
		if len(files) >= maxDirty {
			return files, true
		}
		files = append(files, probe.DirtyFile{Path: cleanText(path), State: state})
	}
	return files, false
}
