package resolve

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/pathguard"
)

// InboxDir is the snapshot drop folder, relative to the project root.
const InboxDir = ".drashta/snapshots/inbox"

// Drop is one image in the snapshot drop folder that is not filed to a task yet.
type Drop struct {
	Name  string    `json:"name"`  // file name, for example shot.png
	Path  string    `json:"path"`  // relative to the project root
	Size  int64     `json:"size"`  // bytes
	MTime time.Time `json:"mtime"` // last change
}

// ErrBadDropName means a drop name is not one plain, safe file name.
var ErrBadDropName = errors.New("resolve: bad drop file name")

// CheckDropName accepts one plain file name: no separators, not "." or "..", no leading
// dot, no control characters, and an image extension from authored.ImageExts.
func CheckDropName(name string) error {
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, ".") ||
		name != strings.TrimSpace(name) {
		return ErrBadDropName
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ErrBadDropName
		}
	}
	if !isImageExt(path.Ext(name)) {
		return authored.ErrBadExt
	}
	return nil
}

func isImageExt(ext string) bool {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	for _, e := range authored.ImageExts {
		if e == ext {
			return true
		}
	}
	return false
}

// DropPath resolves a drop name to its absolute path inside <root>/.drashta/snapshots/inbox,
// through pathguard. The file must exist and be a regular file.
func DropPath(root, name string) (string, error) {
	if err := CheckDropName(name); err != nil {
		return "", err
	}
	dir, err := pathguard.Resolve(root, InboxDir)
	if err != nil {
		return "", err
	}
	full, err := pathguard.Resolve(root, InboxDir+"/"+name)
	if err != nil {
		return "", err
	}
	if filepath.Dir(full) != dir {
		return "", pathguard.ErrOutside
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", ErrBadDropName
	}
	return full, nil
}

// UnfiledSnapshots lists the images in <root>/.drashta/snapshots/inbox that wait to be filed,
// newest first. Missing folder means none. Links, folders, hidden files and non-image files
// are skipped.
func UnfiledSnapshots(root string) []Drop {
	out := []Drop{}
	dir, err := pathguard.Resolve(root, InboxDir)
	if err != nil {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if CheckDropName(e.Name()) != nil || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Drop{Name: e.Name(), Path: InboxDir + "/" + e.Name(), Size: fi.Size(), MTime: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].MTime.Equal(out[j].MTime) {
			return out[i].MTime.After(out[j].MTime)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
