package tree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// numbered makes "line 1\nline 2\n...\nline n\n".
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestReadFileBasic(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "src/main.go", "package main\n\nfunc main() {}\n")

	ft, err := ReadFile(root, "src/main.go", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ft.Path != "src/main.go" || ft.Kind != "code" || ft.IsDoc {
		t.Errorf("meta wrong: %+v", ft)
	}
	if ft.Text != "package main\n\nfunc main() {}" {
		t.Errorf("text = %q", ft.Text)
	}
	if ft.Lines != 3 || ft.From != 1 || ft.To != 3 || ft.Truncated || ft.Binary || ft.Redacted {
		t.Errorf("window wrong: %+v", ft)
	}
	if ft.Size != int64(len("package main\n\nfunc main() {}\n")) || ft.MTime.IsZero() {
		t.Errorf("size/mtime wrong: %+v", ft)
	}

	mk(t, root, "docs/guide.md", "# Guide\n")
	ft, err = ReadFile(root, "docs/guide.md", 1, 0)
	if err != nil || !ft.IsDoc || ft.Kind != "doc" {
		t.Errorf("doc flag wrong: %+v %v", ft, err)
	}
}

func TestReadFileDefaultFirst2000Lines(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "big.txt", numbered(2500))

	ft, err := ReadFile(root, "big.txt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ft.From != 1 || ft.To != 2000 || ft.Lines != 2500 || !ft.Truncated {
		t.Errorf("default window wrong: from=%d to=%d lines=%d trunc=%v", ft.From, ft.To, ft.Lines, ft.Truncated)
	}
	if !strings.HasPrefix(ft.Text, "line 1\n") || !strings.HasSuffix(ft.Text, "line 2000") {
		t.Errorf("text edges wrong: %q ... %q", ft.Text[:10], ft.Text[len(ft.Text)-12:])
	}
	if got := strings.Count(ft.Text, "\n") + 1; got != 2000 {
		t.Errorf("text has %d lines, want 2000", got)
	}
}

func TestReadFileLineWindow(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "f.txt", numbered(10))

	cases := []struct {
		name         string
		from, to     int
		text         string
		wFrom, wTo   int
		wantTruncate bool
	}{
		{"middle", 3, 5, "line 3\nline 4\nline 5", 3, 5, true},
		{"single line", 4, 4, "line 4", 4, 4, true},
		{"to past the end", 8, 99, "line 8\nline 9\nline 10", 8, 10, false},
		{"from only", 9, 0, "line 9\nline 10", 9, 10, false},
		{"to before from", 6, 2, "line 6", 6, 6, true},
		{"from past the end", 50, 60, "", 0, 0, false},
		{"whole file", 1, 10, numbered(10)[:len(numbered(10))-1], 1, 10, false},
	}
	for _, tc := range cases {
		ft, err := ReadFile(root, "f.txt", tc.from, tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if ft.Text != tc.text || ft.From != tc.wFrom || ft.To != tc.wTo {
			t.Errorf("%s: text=%q from=%d to=%d, want text=%q from=%d to=%d",
				tc.name, ft.Text, ft.From, ft.To, tc.text, tc.wFrom, tc.wTo)
		}
		if ft.Lines != 10 {
			t.Errorf("%s: Lines = %d, want 10", tc.name, ft.Lines)
		}
		if tc.name != "from past the end" && ft.Truncated != tc.wantTruncate {
			t.Errorf("%s: Truncated = %v, want %v", tc.name, ft.Truncated, tc.wantTruncate)
		}
	}
}

func TestReadFileLineEndings(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "nonl.txt", "a\nb")
	mk(t, root, "empty.txt", "")
	mk(t, root, "crlf.txt", "a\r\nb\r\nc\r\n")

	ft, _ := ReadFile(root, "nonl.txt", 0, 0)
	if ft.Lines != 2 || ft.Text != "a\nb" || ft.To != 2 {
		t.Errorf("no trailing newline: %+v", ft)
	}
	ft, _ = ReadFile(root, "empty.txt", 0, 0)
	if ft.Lines != 0 || ft.Text != "" || ft.From != 0 || ft.To != 0 || ft.Truncated {
		t.Errorf("empty file: %+v", ft)
	}
	ft, _ = ReadFile(root, "crlf.txt", 2, 3)
	if ft.Lines != 3 || ft.Text != "b\r\nc\r" {
		t.Errorf("crlf: lines=%d text=%q", ft.Lines, ft.Text)
	}
}

func TestReadFileSizeCap(t *testing.T) {
	old := maxFileBytes
	maxFileBytes = 100
	t.Cleanup(func() { maxFileBytes = old })

	root := t.TempDir()
	mk(t, root, "long.txt", numbered(100)) // far more than 100 bytes
	ft, err := ReadFile(root, "long.txt", 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !ft.Truncated {
		t.Error("a cut file must be Truncated")
	}
	if len(ft.Text) > 100 {
		t.Errorf("text is %d bytes, cap is 100", len(ft.Text))
	}
	if ft.Size <= 100 {
		t.Errorf("Size should be the real size, got %d", ft.Size)
	}

	// A multi-byte character at the cut is dropped, not broken.
	mk(t, root, "utf.txt", strings.Repeat("a", 99)+"é"+"tail")
	ft, err = ReadFile(root, "utf.txt", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ft.Text != strings.Repeat("a", 99) || !ft.Truncated {
		t.Errorf("partial rune not trimmed: %q trunc=%v", ft.Text, ft.Truncated)
	}
}

func TestReadFileBinary(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "pic.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	mk(t, root, "late.txt", strings.Repeat("x", 9000)+"\x00") // NUL after the 8 KB sniff window
	mk(t, root, "edge.bin", strings.Repeat("x", 8191)+"\x00") // NUL in the last sniffed byte

	ft, err := ReadFile(root, "pic.png", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ft.Binary || ft.Text != "" || ft.Kind != "image" || ft.Size == 0 {
		t.Errorf("png: %+v", ft)
	}
	ft, _ = ReadFile(root, "edge.bin", 0, 0)
	if !ft.Binary || ft.Text != "" {
		t.Errorf("NUL inside first 8 KB must be binary: binary=%v", ft.Binary)
	}
	ft, _ = ReadFile(root, "late.txt", 0, 0)
	if ft.Binary {
		t.Error("NUL after the first 8 KB is not sniffed")
	}
}

func TestReadFileSecretsRedacted(t *testing.T) {
	root := t.TempDir()
	names := []string{".env", ".env.local", "tls/server.pem", "tls/server.key", "ssh/id_rsa", "ssh/id_ed25519.pub", "cert.p12", "A/.ENV"}
	for _, n := range names {
		mk(t, root, n, "SUPER_SECRET=1\n")
	}
	for _, n := range names {
		ft, err := ReadFile(root, n, 0, 0)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if !ft.Redacted || ft.Text != "" || ft.Lines != 0 || ft.Binary {
			t.Errorf("%s not redacted: %+v", n, ft)
		}
		if ft.Size != int64(len("SUPER_SECRET=1\n")) || ft.Path != n {
			t.Errorf("%s: size/path should still be reported: %+v", n, ft)
		}
	}

	// A harmless name that links to a secret file is redacted too.
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	ft, err := ReadFile(root, "notes.txt", 0, 0)
	if err != nil || !ft.Redacted || ft.Text != "" {
		t.Errorf("link to a secret: %+v %v", ft, err)
	}

	// Secrets are listable.
	res, err := Walk(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := find(res.Entries, ".env"); !ok {
		t.Error(".env must be listed in the tree")
	}
}

func TestReadFileSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mk(t, outside, "private.txt", "do not read")
	mk(t, root, "ok.txt", "fine")
	if err := os.Symlink(filepath.Join(outside, "private.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	// A sibling of root that ".." would reach.
	sibling := filepath.Join(filepath.Dir(root), "drashta-sibling-"+filepath.Base(root)+".txt")
	if err := os.WriteFile(sibling, []byte("sibling"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(sibling) })

	for _, rel := range []string{"link.txt", "dirlink/private.txt"} {
		ft, err := ReadFile(root, rel, 0, 0)
		if !errors.Is(err, pathguard.ErrOutside) {
			t.Errorf("%s: want ErrOutside, got %v", rel, err)
		}
		if ft.Text != "" {
			t.Errorf("%s: leaked text %q", rel, ft.Text)
		}
	}
	for _, rel := range []string{"../" + filepath.Base(sibling), "../../etc/hosts", "a/../../" + filepath.Base(sibling), "/etc/hosts"} {
		ft, err := ReadFile(root, rel, 0, 0)
		if err == nil || ft.Text != "" {
			t.Errorf("%s: climbing out must fail, got err=%v text=%q", rel, err, ft.Text)
		}
	}
	// A link that stays inside root is fine.
	if err := os.Symlink(filepath.Join(root, "ok.txt"), filepath.Join(root, "inner-link.txt")); err != nil {
		t.Fatal(err)
	}
	ft, err := ReadFile(root, "inner-link.txt", 0, 0)
	if err != nil || ft.Text != "fine" {
		t.Errorf("inside link: %+v %v", ft, err)
	}
}

func TestReadFileErrors(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "d/f.txt", "x")

	if _, err := ReadFile(root, "d", 0, 0); !errors.Is(err, ErrIsDir) {
		t.Errorf("folder: want ErrIsDir, got %v", err)
	}
	if _, err := ReadFile(root, "", 0, 0); !errors.Is(err, ErrIsDir) {
		t.Errorf("root: want ErrIsDir, got %v", err)
	}
	if _, err := ReadFile(root, "nope.txt", 0, 0); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: want ErrNotExist, got %v", err)
	}
}

func TestRawPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mk(t, outside, "x.png", "png")
	mk(t, root, "img/a.png", "\x89PNG")
	mk(t, root, "img/b.SVG", "<svg/>")
	mk(t, root, "page.html", "<script>alert(1)</script>")
	mk(t, root, "run.js", "x")
	mk(t, root, "notes.md", "# x")
	mk(t, root, "data.json", "{}")
	mk(t, root, "doc.pdf", "%PDF-1.4")
	mk(t, root, "noext", "just some text\n")
	mk(t, root, "mystery.zzz", "<html><body>hi</body></html>")
	mk(t, root, "blob.zzz2", "\x00\x01\x02\x03\x04")
	mk(t, root, ".env", "SECRET=1")
	mk(t, root, "keys/server.pem", "-----BEGIN")
	if err := os.Symlink(filepath.Join(outside, "x.png"), filepath.Join(root, "evil.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "innocent.png")); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"img/a.png":   "image/png",
		"img/b.SVG":   "image/svg+xml",
		"page.html":   "text/plain; charset=utf-8",
		"run.js":      "text/plain; charset=utf-8",
		"notes.md":    "text/plain; charset=utf-8",
		"data.json":   "application/json",
		"doc.pdf":     "application/pdf",
		"noext":       "text/plain; charset=utf-8",
		"mystery.zzz": "text/plain; charset=utf-8", // sniffed as html, forced to text
		"blob.zzz2":   "application/octet-stream",
	}
	for rel, wantMime := range cases {
		abs, mime, err := RawPath(root, rel)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if mime != wantMime {
			t.Errorf("%s: mime = %q, want %q", rel, mime, wantMime)
		}
		if !filepath.IsAbs(abs) || filepath.Base(abs) != filepath.Base(rel) {
			t.Errorf("%s: abs = %q", rel, abs)
		}
	}

	for _, rel := range []string{".env", "keys/server.pem", "innocent.png"} {
		if _, _, err := RawPath(root, rel); !errors.Is(err, ErrSecret) {
			t.Errorf("%s: want ErrSecret, got %v", rel, err)
		}
	}
	if _, _, err := RawPath(root, "evil.png"); !errors.Is(err, pathguard.ErrOutside) {
		t.Errorf("evil.png: want ErrOutside, got %v", err)
	}
	if _, _, err := RawPath(root, "../x.png"); err == nil {
		t.Error("../x.png must fail")
	}
	if _, _, err := RawPath(root, "img"); !errors.Is(err, ErrIsDir) {
		t.Errorf("folder: want ErrIsDir, got %v", err)
	}
	if _, _, err := RawPath(root, "missing.png"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: want ErrNotExist, got %v", err)
	}
}
