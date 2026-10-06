package autolink

import "strings"

// mention is one task id found in free text.
type mention struct {
	Start      int    // index of the 't' or 'T'
	Lead       int    // index where `task:` or `proj:` in front of it begins (Start when none)
	ID         string // "T-12" (as written, upper case T)
	Upper      bool   // the letter was written as upper case
	TaskPrefix bool   // written as task:T-12
	Qual       string // project qualifier as in ledger:T-12; "" when none
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAlnumByte(c byte) bool { return c != '_' && isWordByte(c) }

func isIdentByte(c byte) bool { return isAlnumByte(c) || c == '.' || c == '-' || c == '_' }

// scanMentions finds every `\b[Tt]-\d+\b` in s (the rule of 04 section 3). The match
// is exact: T-123 never counts as T-12, and ST-12 or T-12abc never match.
// For each hit it also reads an optional `task:` and `<project>:` before it.
func scanMentions(s string) []mention {
	var out []mention
	for i := 0; i+2 < len(s); i++ {
		c := s[i]
		if (c != 'T' && c != 't') || s[i+1] != '-' {
			continue
		}
		if i > 0 && isWordByte(s[i-1]) {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i+2 || j-(i+2) > 9 {
			continue
		}
		if j < len(s) && isWordByte(s[j]) {
			continue
		}
		m := mention{Start: i, ID: "T-" + s[i+2:j], Upper: c == 'T'}
		m.Qual, m.TaskPrefix, m.Lead = qualifierBefore(s, i)
		out = append(out, m)
		i = j - 1
	}
	return out
}

// qualifierBefore reads what stands just before s[i]: `task:`, `proj:`, `proj:task:`.
// lead is where that text begins (i when there is none).
func qualifierBefore(s string, i int) (qual string, taskPrefix bool, lead int) {
	ident, start := identBefore(s, i)
	if ident == "" {
		return "", false, i
	}
	if strings.EqualFold(ident, "task") {
		q, qstart := identBefore(s, start)
		if q == "" {
			return "", true, start
		}
		return q, true, qstart
	}
	return ident, false, start
}

// identBefore wants s[:end] to end with ':' and returns the name in front of it
// (letters, digits, '.', '_' and '-', starting with a letter or digit) and where it starts.
func identBefore(s string, end int) (string, int) {
	if end < 1 || s[end-1] != ':' {
		return "", end
	}
	k := end - 1
	j := k
	for j > 0 && isIdentByte(s[j-1]) {
		j--
	}
	for j < k && !isAlnumByte(s[j]) {
		j++
	}
	if j == k {
		return "", end
	}
	return s[j:k], j
}

// trailerWords may stand in front of a colon without being a project name: `Refs:T-12`.
var trailerWords = map[string]bool{
	"task": true, "tasks": true, "ref": true, "refs": true, "related": true,
	"fix": true, "fixes": true, "fixed": true,
	"close": true, "closes": true, "closed": true,
	"resolve": true, "resolves": true, "resolved": true,
}

func isTrailerWord(q string) bool { return trailerWords[strings.ToLower(q)] }

// closeWords make a commit propose to close a task (04 section 3: Fixes / Closes).
// The longer GitHub family is accepted too.
var closeWords = map[string]bool{
	"fix": true, "fixes": true, "fixed": true,
	"close": true, "closes": true, "closed": true,
	"resolve": true, "resolves": true, "resolved": true,
}

// closesBefore reports whether the text just before the mention is a closing
// word: `Fixes T-12`, `closes: T-12`, `Fixes task:T-12`, `Fixes drashta:T-12`.
func closesBefore(s string, m mention) bool {
	if m.Qual != "" && closeWords[strings.ToLower(m.Qual)] {
		return true // Fixes:T-12 (no space): the closing word was read as the qualifier
	}
	b := strings.TrimRight(s[:m.Lead], " \t")
	b = strings.TrimSuffix(b, ":")
	b = strings.TrimRight(b, " \t")
	k := len(b)
	for k > 0 && ((b[k-1] >= 'a' && b[k-1] <= 'z') || (b[k-1] >= 'A' && b[k-1] <= 'Z')) {
		k--
	}
	if !closeWords[strings.ToLower(b[k:])] {
		return false
	}
	return k == 0 || !isWordByte(b[k-1])
}

// branchMentions finds task ids in a branch name: t-12-x, T-12/x, feat/T-12-x.
// '_' counts as a separator here, so fix_t-12 matches.
func branchMentions(name string) []mention {
	return scanMentions(strings.ReplaceAll(name, "_", "/"))
}

// editedTaskID reads a session's edited path (project-relative) and returns the
// task it belongs to: .drashta/tasks/T-12.md or .drashta/snapshots/T-12/...
func editedTaskID(p string) (string, bool) {
	p = strings.TrimPrefix(p, "./")
	var rest string
	switch {
	case strings.HasPrefix(p, ".drashta/tasks/"):
		rest = strings.TrimPrefix(p, ".drashta/tasks/")
		rest = strings.TrimSuffix(rest, ".md")
		if strings.Contains(rest, "/") || strings.Contains(rest, ".") {
			return "", false
		}
	case strings.HasPrefix(p, ".drashta/snapshots/"):
		rest = strings.TrimPrefix(p, ".drashta/snapshots/")
		first, _, ok := strings.Cut(rest, "/")
		if !ok {
			return "", false
		}
		rest = first
	default:
		return "", false
	}
	return normTaskID(rest)
}

// normTaskID accepts "T-12" or "t-12" and returns "T-12".
func normTaskID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if len(id) < 3 || (id[0] != 'T' && id[0] != 't') || id[1] != '-' || len(id) > 11 {
		return "", false
	}
	for i := 2; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return "", false
		}
	}
	return "T-" + id[2:], true
}

// isDrashtaCommand reports whether a shell command runs the drashta program.
func isDrashtaCommand(cmd string) bool {
	for _, f := range strings.Fields(cmd) {
		f = strings.Trim(f, "\"'`;&|()")
		if f == "drashta" || strings.HasSuffix(f, "/drashta") {
			return true
		}
	}
	return false
}

// BranchTaskIDs returns the task ids named in a branch name (t-12-x, T-12/x, feat/T-12-x),
// in order, without repeats. The indexer uses it to run `git log <default>..<branch>`
// only for branches that name a task.
func BranchTaskIDs(name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range branchMentions(name) {
		if !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, m.ID)
		}
	}
	return out
}
