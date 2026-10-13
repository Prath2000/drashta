package authored

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/pathguard"
)

const lockWait = 10 * time.Second

// ensureDot returns the real path of <root>/.drashta, building the layout first when it is missing.
// A .drashta that resolves outside the project is refused before anything is created, and so is a
// .drashta symlink that points nowhere (creating through it could land outside).
func ensureDot(root string) (string, error) {
	real, err := pathguard.Resolve(root, dotName)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if fi, lerr := os.Lstat(filepath.Join(root, dotName)); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: %s is a broken symlink", pathguard.ErrOutside, dotName)
	}
	if err := EnsureLayout(root); err != nil {
		return "", err
	}
	return pathguard.Resolve(root, dotName)
}

// mkdirBelowDot creates .drashta/<rel> if needed and returns its real path, checked by pathguard.
// It walks one folder at a time from the real .drashta path. A part that is a symlink is followed
// only when its target stays inside the project; a missing part is made with os.Mkdir. So a
// hostile .drashta/snapshots symlink can never make folders outside the project (review M-4).
func mkdirBelowDot(root, rel string) (string, error) {
	cur, err := ensureDot(root)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	for _, part := range strings.Split(path.Clean("/"+rel), "/") {
		if part == "" {
			continue
		}
		next, err := stepDir(realRoot, filepath.Join(cur, part), true)
		if err != nil {
			return "", err
		}
		cur = next
	}
	return pathguard.Resolve(root, dotRel(rel))
}

// stepDir returns the real path of the folder p, making it when it is missing (and create is set).
// A symlink must resolve to a folder inside realRoot.
func stepDir(realRoot, p string, create bool) (string, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) && create {
		if err := os.Mkdir(p, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		return stepDir(realRoot, p, false) // check again: something may have raced in
	}
	if err != nil {
		return "", err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return "", fmt.Errorf("%w: %s", pathguard.ErrOutside, err)
		}
		if !pathguard.Inside(realRoot, real) {
			return "", pathguard.ErrOutside
		}
		if fi, err = os.Stat(real); err != nil {
			return "", err
		}
		p = real
	}
	if !fi.IsDir() {
		return "", invalid("%s is not a folder", filepath.Base(p))
	}
	return p, nil
}

// lockRoot takes an exclusive flock on .drashta/.lock (04-data-model.md section 2.1). The CLI,
// the server and every goroutine use the same file, so read-modify-write cycles do not mix.
// Do not call another authored function that locks while you hold it.
func lockRoot(root string) (unlock func(), err error) {
	dot, err := ensureDot(root)
	if err != nil {
		return nil, err
	}
	f, err := openLockFile(filepath.Join(dot, ".lock"))
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		locked, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, ErrLockTimeout
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() {
		unlockFile(f)
		_ = f.Close()
	}, nil
}

// writeAtomic writes data to dir/name through a temp file in the same folder: write, fsync,
// then rename. With exclusive set it fails with ErrExists when dir/name already exists.
func writeAtomic(dir, name string, data []byte, mode os.FileMode, exclusive bool) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	final := filepath.Join(dir, name)
	if exclusive {
		// A hard link fails if the target exists, which makes "create only" atomic.
		if err := os.Link(tmpName, final); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return ErrExists
			}
			if _, serr := os.Lstat(final); serr == nil {
				return ErrExists
			}
			if err := os.Rename(tmpName, final); err != nil {
				return err
			}
			cleanup = false
		}
	} else if err := os.Rename(tmpName, final); err != nil {
		return err
	} else {
		cleanup = false
	}
	syncDir(dir)
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// Save writes one item (a pointer or a value of Milestone, Feature, Task, Snapshot or Note).
//
// A new item needs ifRev == "" and fails with ErrExists when its file exists. An empty file
// made by NextID counts as free. An existing item needs the rev the caller read; if the file on
// disk has a different sha256, Save returns ErrConflict and writes nothing.
//
// Only the keys the type knows are patched. Unknown keys, their order and comments, and the
// text of unchanged parts stay as they are. The write goes through a temp file, fsync and rename.
// For a pointer item, Rev is updated. The new rev is returned.
func Save(root string, item any, ifRev string) (newRev string, err error) {
	e, err := toEntity(item)
	if err != nil {
		return "", err
	}
	if err := prepare(e); err != nil {
		return "", err
	}
	unlock, err := lockRoot(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	return saveLocked(root, e, ifRev)
}

// prepare cleans the body (no blank lines at the start, no trailing white space, which is how
// a body is read back) and checks the values.
func prepare(e entity) error {
	b := e.bodyPtr()
	*b = bodyOf(*b)
	return e.normalize()
}

func saveLocked(root string, e entity, ifRev string) (string, error) {
	rel := e.rel()
	dirReal, err := mkdirBelowDot(root, path.Dir(rel))
	if err != nil {
		return "", err
	}
	name := path.Base(rel)
	cur, err := readBelowDot(root, rel)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", err
	}
	mode := os.FileMode(0o644)
	if exists {
		if fi, err := os.Stat(filepath.Join(dirReal, name)); err == nil {
			mode = fi.Mode().Perm()
		}
	}

	var b *base
	exclusive := false
	switch {
	case ifRev == "":
		if exists && len(cur) > 0 {
			return "", fmt.Errorf("%w: %s", ErrExists, dotRel(rel))
		}
		exclusive = !exists
	case !exists:
		return "", fmt.Errorf("%w: %s", ErrNotFound, dotRel(rel))
	default:
		if got := revOf(cur); got != ifRev {
			return "", fmt.Errorf("%w: %s (rev on disk %.12s, expected %.12s)", ErrConflict, dotRel(rel), got, ifRev)
		}
		if len(cur) > 0 {
			_, b, err = parseEntity(e.kind(), cur)
			if err != nil {
				return "", fmt.Errorf("%s: cannot patch, the file does not parse: %w", dotRel(rel), err)
			}
		}
	}

	out, err := render(e, b)
	if err != nil {
		return "", err
	}
	if exists && bytes.Equal(out, cur) {
		e.setRev(revOf(cur))
		return revOf(cur), nil
	}
	if err := writeAtomic(dirReal, name, out, mode, exclusive); err != nil {
		if errors.Is(err, ErrExists) {
			return "", fmt.Errorf("%w: %s", ErrExists, dotRel(rel))
		}
		return "", err
	}
	rev := revOf(out)
	e.setRev(rev)
	return rev, nil
}

// update loads an item under the lock, lets fn change it, and saves it. Nobody can write
// in between. fn must not call other authored functions that lock.
func update[T any](root, kind, id string, fn func(*T) error) (*T, error) {
	unlock, err := lockRoot(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	e, _, err := loadOne(root, kind, id)
	if err != nil {
		return nil, err
	}
	ifRev := e.getRev()
	item := any(e).(*T)
	if err := fn(item); err != nil {
		return nil, err
	}
	if e.fileID() != id {
		return nil, invalid("an update must not change the id of %s", id)
	}
	if err := prepare(e); err != nil {
		return nil, err
	}
	if _, err := saveLocked(root, e, ifRev); err != nil {
		return nil, err
	}
	return item, nil
}

// UpdateMilestone reads, changes (with fn) and saves milestones/<id>.md as one locked step.
func UpdateMilestone(root, id string, fn func(*Milestone) error) (*Milestone, error) {
	return update(root, KindMilestone, id, fn)
}

// UpdateFeature is UpdateMilestone for features.
func UpdateFeature(root, id string, fn func(*Feature) error) (*Feature, error) {
	return update(root, KindFeature, id, fn)
}

// UpdateTask is UpdateMilestone for tasks.
func UpdateTask(root, id string, fn func(*Task) error) (*Task, error) {
	return update(root, KindTask, id, fn)
}

// UpdateSnapshot is UpdateMilestone for snapshots. The id is "T-1/v3".
func UpdateSnapshot(root, id string, fn func(*Snapshot) error) (*Snapshot, error) {
	return update(root, KindSnapshot, id, fn)
}

// UpdateNote is UpdateMilestone for notes.
func UpdateNote(root, id string, fn func(*Note) error) (*Note, error) {
	return update(root, KindNote, id, fn)
}
