package tree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// Limits. They are variables so tests can make them small.
var (
	maxEntries = 50000 // entries in one Walk
	maxDepth   = 12    // folder depth below the root (a child of the root has depth 1)
)

// Kind values for Entry.Kind and FileText.Kind.
const (
	KindCode  = "code"
	KindDoc   = "doc"
	KindImage = "image"
	KindOther = "other"
)

// defaultSkip is the built-in skip list (03-architecture.md section 3, tree).
var defaultSkip = []string{
	"node_modules", ".git", "dist", "build", "target", "venv", ".venv",
	"__pycache__", ".next", ".cache", ".DS_Store", ".fslckout",
}

// DefaultSkipNames returns a copy of the built-in skip list.
func DefaultSkipNames() []string {
	return append([]string(nil), defaultSkip...)
}

// Entry is one file or folder. Path is relative to the walk root and uses "/".
type Entry struct {
	Path  string    `json:"path"`
	Dir   bool      `json:"dir"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
	// Kind is "code", "doc", "image" or "other" for files. It is empty for folders.
	Kind string `json:"kind,omitempty"`
	// Collapsed marks a folder that is shown but never walked (skip list, or a symlink).
	Collapsed bool `json:"collapsed,omitempty"`
	// HasChildren is set by ListDir only. It is true when a folder shows something inside.
	HasChildren bool `json:"hasChildren,omitempty"`
}

// Result is what Walk returns.
type Result struct {
	Entries   []Entry  `json:"entries"`
	Truncated bool     `json:"truncated"`        // a cap (entries or depth) cut the walk short
	Denied    []string `json:"denied,omitempty"` // folders that could not be read (relative paths)
}

// skipper decides which names are not walked.
type skipper struct {
	names map[string]bool // match the base name
	paths map[string]bool // match the whole relative path (entries with a "/")
}

func newSkipper(extra []string) *skipper {
	s := &skipper{names: map[string]bool{}, paths: map[string]bool{}}
	for _, n := range defaultSkip {
		s.names[n] = true
	}
	for _, n := range extra {
		n = strings.Trim(strings.TrimSpace(filepath.ToSlash(n)), "/")
		if n == "" {
			continue
		}
		if strings.Contains(n, "/") {
			s.paths[n] = true
		} else {
			s.names[n] = true
		}
	}
	return s
}

func (s *skipper) skip(name, rel string) bool {
	return s.names[name] || s.paths[rel]
}

// Walk lists the files and folders below root.
//
// Folders on the skip list (default list plus extraSkip) appear as Collapsed
// entries and are never opened. Skipped files (.DS_Store, .fslckout) are left
// out. Symlinks are never followed: a link to a folder is a Collapsed folder,
// any other link is a plain file entry. A folder that cannot be read stays in
// Entries and is also listed in Denied. When a cap is hit (50,000 entries or
// depth 12) the walk stops there and Truncated is true.
//
// If ctx ends, Walk returns what it has so far together with ctx.Err().
func Walk(ctx context.Context, root string, extraSkip []string) (Result, error) {
	res := Result{Entries: []Entry{}}
	st, err := os.Stat(root)
	if err != nil {
		return res, err
	}
	if !st.IsDir() {
		return res, fmt.Errorf("tree: %s is not a folder", root)
	}
	w := &walker{ctx: ctx, skip: newSkipper(extraSkip), res: &res}
	if err := w.dir(root, "", 1); err != nil {
		return res, err
	}
	return res, nil
}

type walker struct {
	ctx  context.Context
	skip *skipper
	res  *Result
	full bool // the entry cap is reached; stop everything
}

// dir lists one folder. depth is the depth of its children.
func (w *walker) dir(abs, rel string, depth int) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	des, err := os.ReadDir(abs)
	if err != nil && len(des) == 0 {
		w.res.Denied = append(w.res.Denied, relOrDot(rel))
		return nil
	}
	// A partial list with an error is kept: the entries we got are real.
	for _, de := range des {
		if w.full {
			return nil
		}
		name := de.Name()
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		e, ok := buildEntry(abs, childRel, de, w.skip)
		if !ok {
			continue
		}
		if len(w.res.Entries) >= maxEntries {
			w.res.Truncated = true
			w.full = true
			return nil
		}
		w.res.Entries = append(w.res.Entries, e)
		if !e.Dir || e.Collapsed {
			continue
		}
		if depth >= maxDepth {
			// Too deep. Only call it truncated if something is inside.
			switch has, derr := firstName(filepath.Join(abs, name)); {
			case derr != nil:
				w.res.Denied = append(w.res.Denied, childRel)
			case has:
				w.res.Truncated = true
			}
			continue
		}
		if err := w.dir(filepath.Join(abs, name), childRel, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// buildEntry makes the Entry for one directory item. ok is false for items that
// are left out (skipped files, and items that vanished). Walk and ListDir share it.
func buildEntry(parentAbs, rel string, de fs.DirEntry, sk *skipper) (Entry, bool) {
	name := de.Name()
	info, err := de.Info()
	if err != nil {
		return Entry{}, false // vanished between ReadDir and Info
	}
	e := Entry{Path: rel, MTime: info.ModTime()}

	isDir := de.IsDir()
	isLink := de.Type()&fs.ModeSymlink != 0
	if isLink {
		// What does the link point at? Only used to decide dir or file.
		if target, err := os.Stat(filepath.Join(parentAbs, name)); err == nil && target.IsDir() {
			isDir = true
		}
	}

	if isDir {
		e.Dir = true
		if isLink || sk.skip(name, rel) {
			e.Collapsed = true
		}
		return e, true
	}
	if sk.skip(name, rel) {
		return Entry{}, false
	}
	e.Size = info.Size()
	e.Kind = Classify(name)
	return e, true
}

// firstName reports whether a folder has at least one item.
func firstName(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if err != nil && len(names) == 0 {
		// io.EOF means empty. Anything else is a real error.
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return len(names) > 0, nil
}

func relOrDot(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

// ListDir lists the children of dir (relative to root; "" is the root itself),
// down to depth levels. depth 1 gives direct children only. Values below 1 mean
// 1, and the depth is capped at 12.
//
// The path goes through pathguard.Resolve, so a link that leaves root is an
// error (errors.Is(err, pathguard.ErrOutside)). Entry paths are relative to
// root. Folders come before files; each group is sorted by name without regard
// to case. Skipped folders are returned Collapsed and are not opened. Folders
// at the last level get HasChildren, so the page can show an expand arrow.
// Extra names to skip (config.skip) can be passed as extraSkip.
//
// Asking for a skipped folder directly (for example "node_modules") works: you
// get its children. The skip list applies to what is inside.
func ListDir(root, dir string, depth int, extraSkip ...string) ([]Entry, error) {
	abs, err := pathguard.Resolve(root, dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrNotDir
	}
	if depth < 1 {
		depth = 1
	}
	if depth > maxDepth {
		depth = maxDepth
	}
	base := cleanRel(dir)
	out := []Entry{}
	sk := newSkipper(extraSkip)
	if _, err := listInto(&out, abs, base, depth, 1, sk); err != nil {
		return nil, err
	}
	return out, nil
}

// ErrNotDir is returned by ListDir when the path is a file.
var ErrNotDir = errors.New("tree: not a folder")

// listInto appends the children of abs to out and returns how many entries it
// added (children and, below them, deeper levels).
func listInto(out *[]Entry, abs, rel string, depth, level int, sk *skipper) (int, error) {
	des, err := os.ReadDir(abs)
	if err != nil && len(des) == 0 {
		return 0, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		if e, ok := buildEntry(abs, childRel, de, sk); ok {
			entries = append(entries, e)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		a, b := strings.ToLower(path.Base(entries[i].Path)), strings.ToLower(path.Base(entries[j].Path))
		if a != b {
			return a < b
		}
		return entries[i].Path < entries[j].Path
	})
	added := 0
	for _, e := range entries {
		if len(*out) >= maxEntries {
			break
		}
		childAbs := filepath.Join(abs, path.Base(e.Path))
		idx := len(*out)
		*out = append(*out, e)
		added++
		if !e.Dir || e.Collapsed {
			continue
		}
		if level >= depth {
			(*out)[idx].HasChildren, _ = firstName(childAbs)
			continue
		}
		n, err := listInto(out, childAbs, e.Path, depth, level+1, sk)
		if err != nil {
			continue // an unreadable subfolder is skipped; the rest still counts
		}
		(*out)[idx].HasChildren = n > 0
		added += n
	}
	return added, nil
}

// cleanRel turns a user-given relative path into a clean slash path with no
// leading slash. "" and "." give "". ".." parts are dropped like pathguard does.
func cleanRel(rel string) string {
	c := path.Clean("/" + filepath.ToSlash(rel))
	return strings.TrimPrefix(c, "/")
}

// Classify returns the Kind of a file from its name.
func Classify(name string) string {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "readme") {
		return KindDoc
	}
	switch lower {
	case "makefile", "dockerfile", "justfile", "rakefile", "gemfile", "procfile":
		return KindCode
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	switch {
	case docExt[ext]:
		return KindDoc
	case imageExt[ext]:
		return KindImage
	case codeExt[ext]:
		return KindCode
	}
	return KindOther
}

var docExt = toSet("md", "markdown", "mdx", "txt", "rst", "adoc")

var imageExt = toSet("png", "jpg", "jpeg", "gif", "webp", "svg", "ico", "bmp", "tif", "tiff", "avif", "heic")

var codeExt = toSet(
	"go", "mod", "sum", "js", "jsx", "mjs", "cjs", "ts", "tsx", "py", "rs", "c", "h", "cc", "cpp", "hpp",
	"m", "mm", "java", "kt", "kts", "swift", "rb", "php", "lua", "pl", "r", "scala", "cs", "dart", "zig",
	"sh", "bash", "zsh", "fish", "ps1", "bat",
	"html", "htm", "css", "scss", "sass", "less", "vue", "svelte",
	"json", "jsonc", "yaml", "yml", "toml", "ini", "cfg", "conf", "xml", "plist", "sql", "graphql", "proto",
	"tf", "gradle", "lock",
)

func toSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}
