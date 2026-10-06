package claude

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// File is one transcript file on disk.
type File struct {
	Path       string    `json:"path"`      // absolute path of the .jsonl
	Profile    string    `json:"profile"`   // profile folder name, for example ".claude-work"
	Size       int64     `json:"size"`      // bytes
	MTime      time.Time `json:"mtime"`     // modification time
	Inode      uint64    `json:"inode"`     // 0 if unknown
	SessionID  string    `json:"sessionId"` // file name without ".jsonl" (for a subagent: "agent-...")
	IsSubagent bool      `json:"isSubagent"`
	ParentID   string    `json:"parentId,omitempty"` // subagent only: the session it counts into
}

// Owner is the session this file counts into: ParentID for a subagent, else SessionID.
func (f File) Owner() string {
	if f.IsSubagent {
		return f.ParentID
	}
	return f.SessionID
}

// ParentPath is the path of the session's main .jsonl. For a subagent file at
// <dir>/<parent>/subagents/x.jsonl it is <dir>/<parent>.jsonl.
func (f File) ParentPath() string {
	if !f.IsSubagent {
		return f.Path
	}
	sessDir := filepath.Dir(filepath.Dir(f.Path))
	return filepath.Join(filepath.Dir(sessDir), f.ParentID+".jsonl")
}

// folder is the encoded-cwd folder name the file sits in.
func (f File) folder() string {
	return filepath.Base(filepath.Dir(f.ParentPath()))
}

// State is what the store keeps per file (table claude_file).
type State struct {
	Size   int64     `json:"size"`
	MTime  time.Time `json:"mtime"`
	Offset int64     `json:"offset"` // first byte not parsed yet (always the start of a line)
	Inode  uint64    `json:"inode"`
}

// Mode says how much of a file must be parsed.
type Mode int

const (
	Skip   Mode = iota // nothing changed
	Append             // the file grew: parse from State.Offset
	Full               // new, shrunk or replaced: parse from 0
)

func (m Mode) String() string {
	switch m {
	case Skip:
		return "skip"
	case Append:
		return "append"
	}
	return "full"
}

// NeedsParse compares the stored state with the file now.
// Same size and mtime: Skip. Grown with the same inode: Append. Shrunk, new
// inode or no state: Full.
func NeedsParse(prev *State, cur File) Mode {
	if prev == nil {
		return Full
	}
	if prev.Inode != 0 && cur.Inode != 0 && prev.Inode != cur.Inode {
		return Full
	}
	if cur.Size < prev.Size || cur.Size < prev.Offset {
		return Full
	}
	if cur.Size == prev.Size && cur.MTime.Equal(prev.MTime) {
		return Skip
	}
	return Append
}

// Discover lists every transcript under <profile>/projects/<encoded-cwd>/*.jsonl,
// plus subagent files under <encoded-cwd>/<session-id>/subagents/*.jsonl, newest
// first. Missing profiles are not an error. Symlinks are ignored, so every path
// stays inside its profile folder. Other read errors are joined and returned
// together with the files that could be listed.
func Discover(profiles []string) ([]File, error) {
	var out []File
	var errs []error
	note := func(err error) {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, prof := range profiles {
		name := filepath.Base(prof)
		base := filepath.Join(prof, "projects")
		dirs, err := os.ReadDir(base)
		if err != nil {
			note(err)
			continue
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			dir := filepath.Join(base, d.Name())
			ents, err := os.ReadDir(dir)
			if err != nil {
				note(err)
				continue
			}
			for _, e := range ents {
				switch {
				case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl"):
					if f, ok := statFile(filepath.Join(dir, e.Name()), name); ok {
						out = append(out, f)
					}
				case e.IsDir():
					subDir := filepath.Join(dir, e.Name(), "subagents")
					subs, err := os.ReadDir(subDir)
					if err != nil {
						note(err)
						continue
					}
					for _, s := range subs {
						if !s.Type().IsRegular() || !strings.HasSuffix(s.Name(), ".jsonl") {
							continue
						}
						if f, ok := statFile(filepath.Join(subDir, s.Name()), name); ok {
							f.IsSubagent = true
							f.ParentID = e.Name()
							out = append(out, f)
						}
					}
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].MTime.After(out[j].MTime) })
	return out, errors.Join(errs...)
}

func statFile(path, profile string) (File, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return File{}, false
	}
	return fileFromInfo(path, profile, fi), true
}

func fileFromInfo(path, profile string, fi os.FileInfo) File {
	f := File{Path: path, Profile: profile, Size: fi.Size(), MTime: fi.ModTime(),
		SessionID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		f.Inode = uint64(st.Ino)
	}
	return f
}

// Stat builds a File for one path (for example after an FSEvents event).
// profile is the profile folder name. Subagent paths are recognised.
func Stat(path, profile string) (File, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	f := fileFromInfo(path, profile, fi)
	if dir := filepath.Dir(path); filepath.Base(dir) == "subagents" {
		f.IsSubagent = true
		f.ParentID = filepath.Base(filepath.Dir(dir))
	}
	return f, nil
}

// Family is one session: its main file (if it exists) plus its subagent files.
type Family struct {
	ID         string    `json:"id"`         // session id
	Profile    string    `json:"profile"`    // profile folder name
	ParentPath string    `json:"parentPath"` // the main .jsonl (may not exist)
	Files      []File    `json:"files"`      // main file first, then subagents by path
	MTime      time.Time `json:"mtime"`      // newest mtime of all files
}

// Group puts Discover's files into sessions, newest first.
func Group(files []File) []Family {
	idx := map[string]int{}
	var fams []Family
	for _, f := range files {
		key := f.ParentPath()
		i, ok := idx[key]
		if !ok {
			i = len(fams)
			idx[key] = i
			fams = append(fams, Family{ID: f.Owner(), Profile: f.Profile, ParentPath: key})
		}
		fams[i].Files = append(fams[i].Files, f)
		if f.MTime.After(fams[i].MTime) {
			fams[i].MTime = f.MTime
		}
	}
	for i := range fams {
		fs := fams[i].Files
		sort.SliceStable(fs, func(a, b int) bool {
			if fs[a].IsSubagent != fs[b].IsSubagent {
				return !fs[a].IsSubagent
			}
			return fs[a].Path < fs[b].Path
		})
	}
	sort.SliceStable(fams, func(i, j int) bool { return fams[i].MTime.After(fams[j].MTime) })
	return fams
}

// ErrNotTranscript means a path is not a .jsonl inside a profile's projects folder.
var ErrNotTranscript = errors.New("claude: not a transcript inside a profile")

// CheckPath makes sure path is a .jsonl file inside <profile>/projects of one of
// the profiles (symlinks resolved, via pathguard). Use it before Transcript or
// Users when the path did not come straight from Discover.
func CheckPath(profiles []string, path string) (string, error) {
	if !strings.HasSuffix(path, ".jsonl") {
		return "", ErrNotTranscript
	}
	roots := make([]string, 0, len(profiles))
	for _, p := range profiles {
		roots = append(roots, filepath.Join(p, "projects"))
	}
	real, err := pathguard.ResolveAny(roots, path)
	if err != nil {
		if errors.Is(err, pathguard.ErrOutside) {
			return "", ErrNotTranscript
		}
		return "", err
	}
	return real, nil
}
