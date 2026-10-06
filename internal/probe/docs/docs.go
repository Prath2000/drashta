package docs

import (
	"math"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/probe/tree"
)

const (
	// headBytes is how much text each doc keeps for the full-text index.
	headBytes = 4 << 10
	// maxReadBytes: a file bigger than this is listed, but not read.
	maxReadBytes = 1 << 20
)

// Doc is one document found in a project.
type Doc struct {
	Path  string    `json:"path"`  // relative to the project root, "/" separators
	Title string    `json:"title"` // first "# " heading, else the file name
	MTime time.Time `json:"mtime"`
	Lines int       `json:"lines"` // 0 when the file was not read (too big, binary)
	// Head is the first 4 KB of text, for the full-text index. Empty when the file was not read.
	Head string `json:"head,omitempty"`
}

// IsDocPath reports whether a relative path is a document by name:
// *.md, *.markdown, *.txt, README*, CLAUDE.md or AGENTS.md (any letter case).
// Secret files are never documents. The folder part of the path is not checked.
func IsDocPath(rel string) bool {
	name := path.Base(strings.ReplaceAll(rel, `\`, "/"))
	if tree.IsSecret(name) {
		return false
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "readme"):
		return true
	case lower == "claude.md", lower == "agents.md":
		return true
	}
	switch path.Ext(lower) {
	case ".md", ".markdown", ".txt":
		return true
	}
	return false
}

// Extract picks the documents out of a tree listing and reads each one.
//
// entries is the Entries list from tree.Walk, root is the project folder.
// Files in skip folders are left out (Walk never lists them, and entries that
// still pass through a default skip folder are dropped here too). Secret files
// are never read and never returned.
//
// A document that cannot be read safely (a link that leaves the project, a
// file that vanished) is dropped. A file bigger than 1 MB, or one with binary
// content, is returned with Title = file name, Lines = 0 and an empty Head.
// The order of entries is kept.
func Extract(entries []tree.Entry, root string) []Doc {
	skip := map[string]bool{}
	for _, n := range tree.DefaultSkipNames() {
		skip[n] = true
	}

	docs := make([]Doc, 0)
	for _, e := range entries {
		if e.Dir || !IsDocPath(e.Path) || insideSkipDir(e.Path, skip) {
			continue
		}
		name := path.Base(e.Path)
		d := Doc{Path: e.Path, Title: name, MTime: e.MTime}

		if e.Size > maxReadBytes {
			docs = append(docs, d)
			continue
		}
		// Whole file in one go: line count and title need all of it.
		ft, err := tree.ReadFile(root, e.Path, 1, math.MaxInt32)
		if err != nil || ft.Redacted {
			continue
		}
		if !ft.Binary {
			d.Lines = ft.Lines
			d.Head = head(ft.Text)
			if t := firstTitle(ft.Text); t != "" {
				d.Title = t
			}
		}
		docs = append(docs, d)
	}
	return docs
}

// insideSkipDir reports whether any folder part of rel is a skip folder.
func insideSkipDir(rel string, skip map[string]bool) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if skip[p] {
			return true
		}
	}
	return false
}

// head returns the first 4 KB of text, cut on a character boundary.
func head(s string) string {
	if len(s) <= headBytes {
		return s
	}
	cut := headBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// firstTitle returns the text of the first "# " heading, ignoring code blocks.
func firstTitle(text string) string {
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimLeft(line, " ")
		if len(line)-len(t) <= 3 && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if len(line)-len(t) <= 3 && strings.HasPrefix(t, "# ") {
			title := strings.TrimSpace(t[2:])
			// A closing run of # needs a space before it ("# Title ##"), so "# C#" keeps its #.
			if i := strings.LastIndex(title, " #"); i >= 0 && strings.Trim(title[i+1:], "#") == "" {
				title = strings.TrimSpace(title[:i])
			}
			if title != "" {
				return title
			}
		}
	}
	return ""
}
