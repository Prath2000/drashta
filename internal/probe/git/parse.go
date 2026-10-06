package git

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/probe"
)

// logFormat is the fixed --format used for every log call.
//
//	0x1e starts a record. 0x1f separates the fields.
//
// Fields: full hash, author name, committer date (strict ISO), subject, body.
// With -z, git ends the format with a NUL byte and then a newline, and prints
// the numstat rows after that, each one ended by NUL.
const logFormat = "--format=%x1e%H%x1f%an%x1f%cI%x1f%s%x1f%b%x1f"

var hexRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// credRe finds "user:password@" inside a URL like https://user:pw@host/x.
var credRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.\-]*://)[^/@]+@`)

// stripCreds removes any user[:password]@ part from a remote URL, so a token in
// the URL never reaches the index or the page. scp-style remotes
// (git@github.com:me/x.git) have no password and stay as they are.
func stripCreds(u string) string {
	return credRe.ReplaceAllString(strings.TrimSpace(u), "$1")
}

// cleanText makes git output safe to store: valid UTF-8 only.
func cleanText(s string) string {
	return strings.ToValidUTF8(s, "�")
}

// cutBytes shortens s to at most n bytes without cutting a rune in half.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// parseLog turns the output of `git log -z --numstat <logFormat>` into commits.
//
// truncated says the output hit the size cap, so the last record may be cut in
// half and is dropped. bodyCap and filesCap limit the stored body and file list;
// NFiles, Plus and Minus always cover every file.
func parseLog(out []byte, truncated bool, bodyCap, filesCap int) []probe.Commit {
	chunks := strings.Split(string(out), "\x1e")
	// chunks[0] is the empty text before the first record start.
	if truncated && len(chunks) > 2 {
		chunks = chunks[:len(chunks)-1]
	}
	var commits []probe.Commit
	for _, ch := range chunks {
		if ch == "" {
			continue
		}
		nul := strings.IndexByte(ch, 0)
		if nul < 0 {
			continue // header not finished
		}
		head, rest := ch[:nul], ch[nul+1:]
		f := strings.SplitN(head, "\x1f", 5)
		if len(f) < 5 || !hexRe.MatchString(f[0]) {
			continue
		}
		t, _ := time.Parse(time.RFC3339, f[2]) // zero time on a parse error
		c := probe.Commit{
			ID:     f[0],
			VCS:    "git",
			Author: cleanText(f[1]),
			Time:   t,
			Msg:    cutBytes(cleanText(f[3]), maxMsgBytes),
			Body:   cutBytes(strings.TrimSpace(cleanText(strings.TrimSuffix(f[4], "\x1f"))), bodyCap),
		}
		parseNumstat(&c, strings.TrimPrefix(rest, "\n"), filesCap)
		commits = append(commits, c)
	}
	return commits
}

// parseNumstat reads NUL separated numstat rows into c.
//
//	normal row:  "<plus>\t<minus>\t<path>"
//	rename row:  "<plus>\t<minus>\t" then the old path, then the new path
//	binary file: "-\t-\t<path>"  (counts as 0 and 0)
func parseNumstat(c *probe.Commit, rest string, filesCap int) {
	toks := strings.Split(rest, "\x00")
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if tok == "" {
			continue
		}
		parts := strings.SplitN(tok, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		if path == "" { // rename: the next two tokens are old path and new path
			if i+2 >= len(toks) {
				break
			}
			path = toks[i+2]
			i += 2
		}
		plus, _ := strconv.Atoi(parts[0]) // "-" (binary) gives 0
		minus, _ := strconv.Atoi(parts[1])
		c.Plus += plus
		c.Minus += minus
		c.NFiles++
		if filesCap <= 0 || len(c.Files) < filesCap {
			c.Files = append(c.Files, probe.CommitFile{Path: cleanText(path), Plus: plus, Minus: minus})
		}
	}
}

// statusInfo is what `git status --porcelain=v2 --branch -z` told us.
type statusInfo struct {
	oid       string // full HEAD hash, "" for a repo with no commit
	head      string // branch name, "HEAD" when detached
	detached  bool
	files     []probe.DirtyFile
	truncated bool // more than maxDirty files
}

// parseStatus reads the NUL separated output of
// `git status --porcelain=v2 --branch --untracked-files=normal -z`.
func parseStatus(out []byte, outTruncated bool) statusInfo {
	var si statusInfo
	toks := strings.Split(string(out), "\x00")
	if outTruncated && len(toks) > 0 {
		toks = toks[:len(toks)-1] // the last token may be cut
	}
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if tok == "" {
			continue
		}
		switch tok[0] {
		case '#':
			switch {
			case strings.HasPrefix(tok, "# branch.oid "):
				v := strings.TrimPrefix(tok, "# branch.oid ")
				if v != "(initial)" {
					si.oid = v
				}
			case strings.HasPrefix(tok, "# branch.head "):
				v := strings.TrimPrefix(tok, "# branch.head ")
				if v == "(detached)" {
					si.head, si.detached = "HEAD", true
				} else {
					si.head = v
				}
			}
		case '1': // ordinary change: 8 fixed fields, then the path
			f := strings.SplitN(tok, " ", 9)
			if len(f) == 9 {
				si.add(f[8], stateOf(f[1]))
			}
		case '2': // rename or copy: 9 fixed fields, then the new path; old path is the next token
			f := strings.SplitN(tok, " ", 10)
			if len(f) == 10 {
				si.add(f[9], stateOf(f[1]))
			}
			i++
		case 'u': // unmerged: 10 fixed fields, then the path
			f := strings.SplitN(tok, " ", 11)
			if len(f) == 11 {
				si.add(f[10], "U")
			}
		case '?':
			if len(tok) > 2 {
				si.add(tok[2:], "?")
			}
		}
	}
	return si
}

func (si *statusInfo) add(path, state string) {
	if len(si.files) >= maxDirty {
		si.truncated = true
		return
	}
	si.files = append(si.files, probe.DirtyFile{Path: cleanText(path), State: state})
}

// stateOf turns the two porcelain letters (index, work tree) into one code.
// Priority: a file gone from the work tree is D, then renames, then adds.
func stateOf(xy string) string {
	if len(xy) < 2 {
		return "M"
	}
	x, y := xy[0], xy[1]
	switch {
	case x == 'D' || y == 'D':
		return "D"
	case x == 'R' || y == 'R':
		return "R"
	case x == 'A' || x == 'C' || y == 'A' || y == 'C':
		return "A"
	}
	return "M"
}
