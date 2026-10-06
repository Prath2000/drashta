package authored

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// maxIn finds the highest number used by files such as T-12.md in dir (also T-12.md.<suffix>,
// which is how the trash names a second copy).
func maxIn(dir, prefix string) int {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `-([0-9]+)\.md(\..*)?$`)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	max := 0
	for _, e := range entries {
		if m := re.FindStringSubmatch(e.Name()); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > max {
				max = n
			}
		}
	}
	return max
}

// trashDirIfAny returns the real path of .drashta/.trash/<rel> or "" when there is none.
func trashDirIfAny(root, rel string) string {
	real, err := pathguard.Resolve(root, dotRel(".trash", rel))
	if err != nil {
		return ""
	}
	return real
}

// NextID reserves the next free id for kind ("task", "feature", "milestone" or "note") and
// returns it: T-1, F-1, M-1, N-1, and so on.
//
// The number is max(existing, including the trash)+1, and the id is reserved by creating the
// empty file <dir>/<id>.md with O_CREAT|O_EXCL. If another writer took it first, the next
// number is tried. So two writers (two goroutines, the server and the CLI) never get the same
// id, and an id is never reused, even after Delete. The empty file is filled by the first
// Save(root, item, "") for that id.
func NextID(root, kind string) (string, error) {
	kind, err := normKind(kind)
	if err != nil {
		return "", err
	}
	prefix, ok := kindPrefix[kind]
	if !ok {
		return "", invalid("%s ids are made by AddSnapshotImage", kind)
	}
	dirRel := kindDir[kind]
	dir, err := mkdirBelowDot(root, dirRel)
	if err != nil {
		return "", err
	}
	n := maxIn(dir, prefix)
	if t := trashDirIfAny(root, dirRel); t != "" {
		if m := maxIn(t, prefix); m > n {
			n = m
		}
	}
	for tries := 0; tries < 100000; tries++ {
		n++
		id := prefix + "-" + strconv.Itoa(n)
		f, err := os.OpenFile(filepath.Join(dir, id+".md"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_ = f.Close()
			return id, nil
		}
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("authored: no free %s id found", kind)
}

// uniqueIn returns name, or name.<unix nano> when name is taken.
func uniqueIn(dir, name string) string {
	if _, err := os.Lstat(filepath.Join(dir, name)); errors.Is(err, fs.ErrNotExist) {
		return name
	}
	return name + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// Validate checks an item the way Save would (title, status, ids, body rules) without
// writing anything and without changing item. A missing id is filled with a placeholder
// for the check only. Call it before NextID, so a request that would fail never reserves
// an id file (review M14).
func Validate(item any) error {
	var e entity
	switch v := item.(type) {
	case *Milestone:
		c := *v
		e = &c
	case Milestone:
		e = &v
	case *Feature:
		c := *v
		e = &c
	case Feature:
		e = &v
	case *Task:
		c := *v
		e = &c
	case Task:
		e = &v
	case *Note:
		c := *v
		e = &c
	case Note:
		e = &v
	case *Snapshot:
		c := *v
		e = &c
	case Snapshot:
		e = &v
	default:
		return invalid("cannot check a %T", item)
	}
	if prefix, ok := kindPrefix[e.kind()]; ok && e.fileID() == "" {
		_ = e.setFileID(prefix + "-1")
	}
	if s, ok := e.(*Snapshot); ok {
		if s.Task == "" {
			s.Task = "T-1"
		}
		if s.Version == 0 {
			s.Version = 1
		}
	}
	// e is a copy: prepare fills defaults (nil lists, status) on the copy only.
	return prepare(e)
}

// Release gives back an id that NextID reserved but that was never filled (Save failed):
// it removes <dir>/<id>.md only when the file is still empty. Anything else is left alone.
func Release(root, kind, id string) error {
	kind, err := normKind(kind)
	if err != nil {
		return err
	}
	if err := checkID(kind, id); err != nil {
		return err
	}
	if kind == KindSnapshot {
		return invalid("snapshot ids are not reserved by NextID")
	}
	unlock, err := lockRoot(root)
	if err != nil {
		return err
	}
	defer unlock()
	p, err := pathguard.Resolve(root, dotRel(relFor(kind, id)))
	if err != nil {
		return nil // nothing there
	}
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() || fi.Size() != 0 {
		return nil
	}
	return os.Remove(p)
}

// Delete moves an item to .drashta/.trash/<kind folder>/. Nothing is deleted for good, and the
// id is never reused. For a snapshot (id "T-1/v3") the .md file and its image files all move.
// For a task, its snapshots folder and the notes on the task (or on its snapshots) move with
// it, so a pending snapshot of a deleted task cannot keep "awaiting review" up (review I11).
// It returns ErrNotFound when there is nothing to move.
func Delete(root, kind, id string) error {
	_, err := DeleteMoved(root, kind, id)
	return err
}

// DeleteMoved is Delete, and it also returns every item file that moved, as a path relative
// to the project root (".drashta/tasks/T-3.md", ".drashta/snapshots/T-3/v1.md", ...), so the
// caller can mirror each one into the index at once.
func DeleteMoved(root, kind, id string) ([]string, error) {
	kind, err := normKind(kind)
	if err != nil {
		return nil, err
	}
	if err := checkID(kind, id); err != nil {
		return nil, err
	}
	unlock, err := lockRoot(root)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if kind != KindSnapshot {
		rel := relFor(kind, id)
		src, err := pathguard.Resolve(root, dotRel(rel))
		if err != nil {
			return nil, notFound(err)
		}
		dstDir, err := mkdirBelowDot(root, path.Join(".trash", kindDir[kind]))
		if err != nil {
			return nil, err
		}
		if err := os.Rename(src, filepath.Join(dstDir, uniqueIn(dstDir, path.Base(rel)))); err != nil {
			return nil, err
		}
		moved := []string{dotRel(rel)}
		if kind == KindTask {
			more, err := trashTaskExtras(root, id)
			moved = append(moved, more...)
			if err != nil {
				return moved, err
			}
		}
		return moved, nil
	}

	task, v, _ := strings.Cut(id, "/")
	srcRel := path.Join("snapshots", task)
	srcDir, err := pathguard.Resolve(root, dotRel(srcRel))
	if err != nil {
		return nil, notFound(err)
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, notFound(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), v+".") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: snapshot %s", ErrNotFound, id)
	}
	dstDir, err := mkdirBelowDot(root, path.Join(".trash", srcRel))
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, n := range names {
		src, err := pathguard.Resolve(root, dotRel(srcRel, n))
		if err != nil {
			return moved, err
		}
		if err := os.Rename(src, filepath.Join(dstDir, uniqueIn(dstDir, n))); err != nil {
			return moved, err
		}
		if strings.HasSuffix(n, ".md") {
			moved = append(moved, dotRel(srcRel, n))
		}
	}
	return moved, nil
}

// trashTaskExtras moves what belongs to a deleted task: every file of snapshots/<task>/
// into .trash/snapshots/<task>/, and every note whose ref is the task or one of its
// snapshots (plus replies to those notes) into .trash/notes/. Call it with the lock held.
// It returns the moved item files (.md), relative to the project root.
func trashTaskExtras(root, task string) ([]string, error) {
	var moved []string
	srcRel := path.Join("snapshots", task)
	if srcDir, err := pathguard.Resolve(root, dotRel(srcRel)); err == nil {
		entries, err := os.ReadDir(srcDir)
		if err != nil {
			return moved, err
		}
		if len(entries) > 0 {
			dstDir, err := mkdirBelowDot(root, path.Join(".trash", srcRel))
			if err != nil {
				return moved, err
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				src, err := pathguard.Resolve(root, dotRel(srcRel, e.Name()))
				if err != nil {
					return moved, err
				}
				if err := os.Rename(src, filepath.Join(dstDir, uniqueIn(dstDir, e.Name()))); err != nil {
					return moved, err
				}
				if strings.HasSuffix(e.Name(), ".md") {
					moved = append(moved, dotRel(srcRel, e.Name()))
				}
			}
		}
		_ = os.Remove(srcDir) // only when empty now
	}

	// Notes on the task or on its snapshots, then replies to those, until nothing changes.
	notesDir, err := pathguard.Resolve(root, dotRel(kindDir[KindNote]))
	if err != nil {
		return moved, nil // no notes folder
	}
	entries, err := os.ReadDir(notesDir)
	if err != nil {
		return moved, nil
	}
	type noteInfo struct{ ref, replyTo string }
	notes := map[string]noteInfo{}
	for _, e := range entries {
		nid, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || e.IsDir() || checkID(KindNote, nid) != nil {
			continue
		}
		ent, _, err := loadOne(root, KindNote, nid)
		if err != nil {
			continue
		}
		if n, ok := ent.(*Note); ok {
			notes[nid] = noteInfo{ref: n.Ref, replyTo: n.ReplyTo}
		}
	}
	gone := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for nid, n := range notes {
			if gone[nid] {
				continue
			}
			if noteOnTask(n.ref, task) || (n.replyTo != "" && gone[n.replyTo]) {
				gone[nid] = true
				changed = true
			}
		}
	}
	if len(gone) == 0 {
		return moved, nil
	}
	dstDir, err := mkdirBelowDot(root, path.Join(".trash", kindDir[KindNote]))
	if err != nil {
		return moved, err
	}
	ids := make([]string, 0, len(gone))
	for nid := range gone {
		ids = append(ids, nid)
	}
	sort.Strings(ids)
	for _, nid := range ids {
		rel := relFor(KindNote, nid)
		src, err := pathguard.Resolve(root, dotRel(rel))
		if err != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(dstDir, uniqueIn(dstDir, path.Base(rel)))); err != nil {
			return moved, err
		}
		moved = append(moved, dotRel(rel))
	}
	return moved, nil
}

// noteOnTask reports whether a note ref (as stored, local to the project) points at the
// task or at one of its snapshots.
func noteOnTask(ref, task string) bool {
	r, err := ParseRef(ref)
	if err != nil || r.Project != "" {
		return false
	}
	switch r.Type {
	case RefTask:
		return r.ID == task
	case RefSnapshot:
		return r.ID == task || strings.HasPrefix(r.ID, task+"/")
	}
	return false
}
