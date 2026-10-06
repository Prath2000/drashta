package docs

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/probe/tree"
)

func mk(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func extract(t *testing.T, root string) []Doc {
	t.Helper()
	res, err := tree.Walk(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Extract(res.Entries, root)
}

func byPath(docs []Doc) map[string]Doc {
	m := map[string]Doc{}
	for _, d := range docs {
		m[d.Path] = d
	}
	return m
}

func paths(docs []Doc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.Path
	}
	return out
}

func TestExtractPicksAndTitles(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "README.md", "# Project Alpha\n\nIntro text.\n")
	mk(t, root, "CLAUDE.md", "Rules without a heading.\n## Sub only\n")
	mk(t, root, "AGENTS.md", "# Agents\n")
	mk(t, root, "docs/guide.markdown", "intro\n\n# The Guide #\n\nbody\n")
	mk(t, root, "docs/deep/notes.txt", "plain notes\nline two\n")
	mk(t, root, "readme.rst", "Title\n=====\n")
	mk(t, root, "README", "bare readme\n")
	mk(t, root, "plan/lang.md", "# C#\n")
	mk(t, root, "fenced.md", "```sh\n# not a title\n```\n\n# Real Title\n")
	mk(t, root, "indented.md", "    # code block line\n\n# After\n")
	mk(t, root, "src/main.go", "package main\n")
	mk(t, root, "data.json", "{}")
	// Not documents:
	mk(t, root, "node_modules/pkg/README.md", "# Dep\n")
	mk(t, root, "dist/out.md", "# Built\n")
	mk(t, root, ".git/info.txt", "x")
	// Secret files, whatever the extension:
	mk(t, root, ".env", "A=1\n")
	mk(t, root, ".env.txt", "A=1\n")
	mk(t, root, "id_rsa.txt", "key\n")
	mk(t, root, "server.pem", "-----BEGIN\n")
	// A folder that looks like a doc:
	mk(t, root, "folder.md/inside.go", "package x\n")

	docs := extract(t, root)
	got := byPath(docs)

	wantPaths := []string{
		"AGENTS.md", "CLAUDE.md", "README", "README.md", "docs/deep/notes.txt", "docs/guide.markdown",
		"fenced.md", "indented.md", "plan/lang.md", "readme.rst",
	}
	var all []string
	for _, d := range docs {
		all = append(all, d.Path)
	}
	if !reflect.DeepEqual(all, wantPaths) {
		t.Fatalf("docs = %v\nwant %v", all, wantPaths)
	}

	titles := map[string]string{
		"README.md":           "Project Alpha",
		"CLAUDE.md":           "CLAUDE.md", // no "# " line: file name
		"AGENTS.md":           "Agents",
		"docs/guide.markdown": "The Guide",
		"docs/deep/notes.txt": "notes.txt",
		"readme.rst":          "readme.rst",
		"README":              "README",
		"plan/lang.md":        "C#",
		"fenced.md":           "Real Title",
		"indented.md":         "After",
	}
	for p, want := range titles {
		if got[p].Title != want {
			t.Errorf("%s: title = %q, want %q", p, got[p].Title, want)
		}
	}

	if d := got["README.md"]; d.Lines != 3 || d.Head != "# Project Alpha\n\nIntro text." || d.MTime.IsZero() {
		t.Errorf("README.md = %+v", d)
	}
	if d := got["docs/deep/notes.txt"]; d.Lines != 2 {
		t.Errorf("notes.txt lines = %d, want 2", d.Lines)
	}
}

func TestExtractKeepsEntryOrderAndMTime(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "b.md", "# B\n")
	mk(t, root, "a.md", "# A\n")
	when := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	entries := []tree.Entry{
		{Path: "b.md", MTime: when, Size: 4},
		{Path: "a.md", MTime: when.Add(time.Hour), Size: 4},
	}
	docs := Extract(entries, root)
	if !reflect.DeepEqual(paths(docs), []string{"b.md", "a.md"}) {
		t.Errorf("order = %v", paths(docs))
	}
	if !docs[0].MTime.Equal(when) || !docs[1].MTime.Equal(when.Add(time.Hour)) {
		t.Errorf("MTime must come from the entry: %v %v", docs[0].MTime, docs[1].MTime)
	}
}

func TestExtractDropsEntriesInsideSkipDirs(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "node_modules/x/README.md", "# X\n")
	mk(t, root, "ok/README.md", "# OK\n")
	// A caller hands in entries that already contain a skip folder child.
	entries := []tree.Entry{
		{Path: "node_modules/x/README.md", Size: 4},
		{Path: "ok/README.md", Size: 5},
		{Path: "ok", Dir: true},
	}
	docs := Extract(entries, root)
	if !reflect.DeepEqual(paths(docs), []string{"ok/README.md"}) {
		t.Errorf("docs = %v", paths(docs))
	}
}

func TestExtractNeverReadsSecrets(t *testing.T) {
	root := t.TempDir()
	mk(t, root, ".env.md", "TOKEN=abc\n")
	mk(t, root, "id_ed25519.txt", "PRIVATE\n")
	mk(t, root, "ok.md", "# Fine\n")
	// Entries that name secret files must not be returned, even when passed in by hand.
	entries := []tree.Entry{
		{Path: ".env.md", Size: 10},
		{Path: "id_ed25519.txt", Size: 8},
		{Path: "ok.md", Size: 7},
	}
	docs := Extract(entries, root)
	if !reflect.DeepEqual(paths(docs), []string{"ok.md"}) {
		t.Errorf("docs = %v", paths(docs))
	}
	for _, d := range docs {
		if strings.Contains(d.Head, "TOKEN") || strings.Contains(d.Head, "PRIVATE") {
			t.Errorf("secret text leaked into Head: %q", d.Head)
		}
	}
}

func TestExtractHeadIsCappedAtFourKB(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "long.md", "# Long\n"+strings.Repeat("word ", 3000)) // about 15 KB
	// Multi-byte text around the cut: 2 bytes per character, odd offset.
	mk(t, root, "utf.md", "x"+strings.Repeat("é", 3000))

	got := byPath(extract(t, root))
	if n := len(got["long.md"].Head); n != 4096 {
		t.Errorf("long.md head = %d bytes, want 4096", n)
	}
	if got["long.md"].Lines != 1+1 {
		t.Errorf("long.md lines = %d, want 2", got["long.md"].Lines)
	}
	h := got["utf.md"].Head
	if !utf8.ValidString(h) || len(h) > 4096 || len(h) < 4094 {
		t.Errorf("utf.md head: valid=%v len=%d", utf8.ValidString(h), len(h))
	}
}

func TestExtractBigAndBinaryFiles(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "huge.md", "# Huge Title\n"+strings.Repeat("a\n", 600<<10)) // over 1 MB
	mk(t, root, "bin.txt", "\x00\x01\x02 not text")
	mk(t, root, "small.md", "# Small\n")

	got := byPath(extract(t, root))
	h, ok := got["huge.md"]
	if !ok {
		t.Fatal("a big doc is still listed")
	}
	if h.Head != "" || h.Lines != 0 || h.Title != "huge.md" {
		t.Errorf("big file must not be read: %+v", h)
	}
	b, ok := got["bin.txt"]
	if !ok || b.Head != "" || b.Lines != 0 || b.Title != "bin.txt" {
		t.Errorf("binary file: %+v (found=%v)", b, ok)
	}
	if got["small.md"].Title != "Small" {
		t.Errorf("small.md = %+v", got["small.md"])
	}
}

func TestExtractSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mk(t, outside, "private.md", "# Private\n")
	mk(t, root, "real.md", "# Real\n")
	if err := os.Symlink(filepath.Join(outside, "private.md"), filepath.Join(root, "escape.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gone.md"), filepath.Join(root, "broken.md")); err != nil {
		t.Fatal(err)
	}

	docs := extract(t, root)
	got := byPath(docs)
	if _, ok := got["escape.md"]; ok {
		t.Error("a doc link that leaves the project must be dropped")
	}
	if _, ok := got["broken.md"]; ok {
		t.Error("a broken link must be dropped")
	}
	if got["alias.md"].Title != "Real" {
		t.Errorf("link inside the project is fine: %+v", got["alias.md"])
	}
	for _, d := range docs {
		if strings.Contains(d.Head, "Private") {
			t.Errorf("leaked outside text: %+v", d)
		}
	}
}

func TestExtractEmpty(t *testing.T) {
	docs := Extract(nil, t.TempDir())
	if docs == nil || len(docs) != 0 {
		t.Errorf("want an empty, non-nil slice, got %#v", docs)
	}
}

func TestIsDocPath(t *testing.T) {
	yes := []string{
		"a.md", "docs/B.MD", "x/y/notes.markdown", "notes.txt", "README", "readme.rst", "ReadMe.adoc",
		"CLAUDE.md", "sub/AGENTS.md", "claude.MD",
	}
	for _, p := range yes {
		if !IsDocPath(p) {
			t.Errorf("IsDocPath(%q) = false, want true", p)
		}
	}
	no := []string{"main.go", "data.json", "image.png", ".env", ".env.txt", "id_rsa.md", "cert.pem", "docs/", "notes.md.bak"}
	for _, p := range no {
		if IsDocPath(p) {
			t.Errorf("IsDocPath(%q) = true, want false", p)
		}
	}
}
