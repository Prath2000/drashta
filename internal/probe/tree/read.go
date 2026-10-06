package tree

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// Read limits. Variables so tests can make them small.
var (
	maxFileBytes   = 1 << 20 // a live read never takes more than 1 MB of a file
	defaultLines   = 2000    // lines returned when the caller gives no end line
	binarySniffLen = 8 << 10 // the first 8 KB are checked for a NUL byte
)

// Errors from ReadFile and RawPath. Errors from pathguard (ErrOutside) pass through.
var (
	ErrIsDir      = errors.New("tree: path is a folder")
	ErrNotRegular = errors.New("tree: not a regular file")
	ErrSecret     = errors.New("tree: secret file, content is hidden")
)

// FileText is the live read of one file (the /file API).
//
// Lines is the number of lines in the part of the file that was read. For a file
// bigger than 1 MB that is only the first 1 MB, and Truncated is true.
type FileText struct {
	Path      string    `json:"path"` // relative to root, "/" separators
	Size      int64     `json:"size"`
	MTime     time.Time `json:"mtime"`
	Kind      string    `json:"kind"`
	IsDoc     bool      `json:"isDoc"`
	Lines     int       `json:"lines"`
	From      int       `json:"from"` // 1-based first line in Text; 0 when there is no text
	To        int       `json:"to"`   // 1-based last line in Text
	Text      string    `json:"text"`
	Truncated bool      `json:"truncated"` // more of the file exists after To, or after the 1 MB cap
	Binary    bool      `json:"binary"`
	Redacted  bool      `json:"redacted"`
}

// secretNames are file names (lower case) that hold credentials.
var secretNames = map[string]bool{
	".npmrc": true, ".netrc": true, "_netrc": true, ".pgpass": true, ".git-credentials": true,
	".dev.vars": true, "credentials": true, "credentials.json": true,
}

// secretSuffixes are endings (lower case) of key stores and secret files.
var secretSuffixes = []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".ppk",
	".tfvars", ".tfstate", ".tfstate.backup"}

// secretInDir are "<folder>/<name>" pairs (lower case) that hold credentials.
var secretInDir = map[string]bool{
	".aws/credentials": true, ".docker/config.json": true, ".git/config": true,
}

// IsSecret reports whether a file belongs to a secret file whose body must
// never be shown or indexed (any letter case): .env*, id_*, *.pem, *.key,
// *.p12, *.pfx, *.jks, *.keystore, *.ppk, *.tfvars, *.tfstate, secrets.*,
// service-account*.json, .npmrc, .netrc, _netrc, .pgpass, .git-credentials,
// .dev.vars, credentials, credentials.json, .aws/credentials,
// .docker/config.json and .git/config (which can hold a token in a remote URL).
// name may be a bare file name, a relative path or an absolute path; the
// folder pairs need the folder part.
func IsSecret(name string) bool {
	slash := strings.ToLower(filepath.ToSlash(name))
	n := path.Base(slash)
	switch {
	case strings.HasPrefix(n, ".env"), strings.HasPrefix(n, "id_"), strings.HasPrefix(n, "secrets."):
		return true
	case strings.HasPrefix(n, "service-account") && strings.HasSuffix(n, ".json"):
		return true
	case secretNames[n]:
		return true
	}
	for _, suf := range secretSuffixes {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	if dir := path.Dir(slash); dir != "." && dir != "/" {
		return secretInDir[path.Base(dir)+"/"+n]
	}
	return false
}

// ReadFile reads a file live from disk (nothing is cached or stored).
//
// rel goes through pathguard.Resolve, so ".." and links that leave root are
// refused. Folders and special files (pipes, devices) are errors.
//
// Secret files (see IsSecret) are not opened: you get Redacted:true and no
// text. The name is checked on the path you asked for and on the real target
// of a link.
//
// from and to are a 1-based line window, both included. from < 1 means 1.
// to < 1 means "2000 lines from from". At most 1 MB of the file is read. A file
// with a NUL byte in its first 8 KB is binary: Binary:true and no text.
func ReadFile(root, rel string, from, to int) (FileText, error) {
	abs, err := pathguard.Resolve(root, rel)
	if err != nil {
		return FileText{}, err
	}
	clean := cleanRel(rel)
	ft := FileText{Path: clean, Kind: Classify(path.Base(clean))}
	ft.IsDoc = ft.Kind == KindDoc

	// Open without following a link at the last step and without blocking on
	// a pipe. The path is already link-free, so this only guards against a swap.
	f, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ft, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ft, err
	}
	if st.IsDir() {
		return ft, ErrIsDir
	}
	if !st.Mode().IsRegular() {
		return ft, ErrNotRegular
	}
	ft.Size = st.Size()
	ft.MTime = st.ModTime()

	if IsSecret(clean) || IsSecret(abs) {
		ft.Redacted = true
		return ft, nil
	}

	// Look at the first 8 KB before reading more, so a big binary is not read.
	head := make([]byte, binarySniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return ft, err
	}
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		ft.Binary = true
		return ft, nil
	}
	rest, err := io.ReadAll(io.LimitReader(f, int64(maxFileBytes)+1-int64(len(head))))
	if err != nil {
		return ft, err
	}
	data := append(head, rest...)
	cut := false
	if len(data) > maxFileBytes {
		data = data[:maxFileBytes]
		cut = true
	}
	if cut {
		data = trimPartialRune(data)
	}

	ft.Lines = countLines(data)
	ft.Text, ft.From, ft.To = window(data, from, to)
	ft.Truncated = cut || (ft.To < ft.Lines)
	return ft, nil
}

// countLines counts lines. "a\nb" and "a\nb\n" are both 2 lines; empty is 0.
func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// window returns the text of lines from..to (1-based, included), the final line
// break removed, and the real from and to. No lines gives "", 0, 0.
func window(b []byte, from, to int) (string, int, int) {
	total := countLines(b)
	if total == 0 {
		return "", 0, 0
	}
	if from < 1 {
		from = 1
	}
	if to < 1 {
		to = from + defaultLines - 1
	}
	if to < from {
		to = from
	}
	if from > total {
		return "", 0, 0
	}
	if to > total {
		to = total
	}

	// Find the byte offset where line `from` starts.
	start := 0
	for line := 1; line < from; line++ {
		i := bytes.IndexByte(b[start:], '\n')
		start += i + 1
	}
	// Find where line `to` ends (before its line break).
	end := start
	for line := from; line <= to; line++ {
		i := bytes.IndexByte(b[end:], '\n')
		if i < 0 {
			end = len(b)
			break
		}
		end += i
		if line < to {
			end++
		}
	}
	return string(b[start:end]), from, to
}

// trimPartialRune removes an unfinished UTF-8 character from the end of b.
func trimPartialRune(b []byte) []byte {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		c := b[len(b)-i]
		if utf8.RuneStart(c) {
			if c >= 0x80 && !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			break
		}
	}
	return b
}

// RawPath checks that rel is a plain file inside root and returns its real
// (link-free) absolute path and the content type to serve it with.
//
// Secret files (see IsSecret) are refused with ErrSecret, because the raw
// endpoint would otherwise give their bytes away.
//
// The content type is chosen here, not from the system MIME tables. Anything
// that a browser could run as a page or script (html, xml, js) is returned as
// text/plain. The HTTP layer must still send "X-Content-Type-Options: nosniff"
// and "Content-Security-Policy: sandbox" with the bytes, because SVG keeps
// image/svg+xml so that it shows in an <img>.
func RawPath(root, rel string) (abs string, mime string, err error) {
	abs, err = pathguard.Resolve(root, rel)
	if err != nil {
		return "", "", err
	}
	if IsSecret(cleanRel(rel)) || IsSecret(abs) {
		return "", "", ErrSecret
	}
	f, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", "", err
	}
	if st.IsDir() {
		return "", "", ErrIsDir
	}
	if !st.Mode().IsRegular() {
		return "", "", ErrNotRegular
	}

	ext := strings.ToLower(filepath.Ext(abs))
	if m, ok := rawTypes[ext]; ok {
		return abs, m, nil
	}
	if textLikeExt[ext] || Classify(path.Base(abs)) != KindOther {
		return abs, textPlain, nil
	}
	// Unknown extension: look at the first bytes.
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	return abs, sniffType(buf[:n]), nil
}

const textPlain = "text/plain; charset=utf-8"

// rawTypes are fixed content types for files the page shows as media.
var rawTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
	".bmp":  "image/bmp",
	".avif": "image/avif",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".heic": "image/heic",
	".pdf":  "application/pdf",
	".json": "application/json",
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".m4v":  "video/x-m4v",
	".ogv":  "video/ogg",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".aif":  "audio/aiff",
	".aiff": "audio/aiff",
	// Can run in the page: serve as text.
	".html":  textPlain,
	".htm":   textPlain,
	".xhtml": textPlain,
	".xml":   textPlain,
	".js":    textPlain,
	".mjs":   textPlain,
}

// textLikeExt lists plain text extensions that Classify may call "other".
var textLikeExt = toSet(".log", ".csv", ".tsv", ".diff", ".patch", ".gitignore", ".gitattributes")

// sniffType picks a safe content type from the first bytes of a file.
func sniffType(b []byte) string {
	t := http.DetectContentType(b)
	switch {
	case strings.HasPrefix(t, "text/"):
		return textPlain
	case strings.HasPrefix(t, "image/"), strings.HasPrefix(t, "video/"), strings.HasPrefix(t, "audio/"):
		return t
	case t == "application/pdf":
		return t
	}
	return "application/octet-stream"
}
