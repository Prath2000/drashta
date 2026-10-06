package authored

import (
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/Prath2000/drashta/internal/pathguard"
)

const (
	dotName      = ".drashta"
	maxFileBytes = 4 << 20 // a front matter file bigger than this is not a drashta file
)

var (
	snapMDRe   = regexp.MustCompile(`^v([0-9]+)\.md$`)
	snapFileRe = regexp.MustCompile(`^v([0-9]+)\.([A-Za-z0-9]+)$`)
)

// revOf is the rev of a file: the sha256 of its bytes, in hex.
func revOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// dotRel joins a path below .drashta onto the project-relative form used with pathguard.
func dotRel(parts ...string) string {
	return path.Join(append([]string{dotName}, parts...)...)
}

func notFound(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}

// readBelowDot reads .drashta/<rel> through pathguard. rel uses forward slashes.
func readBelowDot(root, rel string) ([]byte, error) {
	full, err := pathguard.Resolve(root, dotRel(rel))
	if err != nil {
		return nil, notFound(err)
	}
	fi, err := os.Stat(full)
	if err != nil {
		return nil, notFound(err)
	}
	if fi.IsDir() {
		return nil, invalid("%s is a folder", rel)
	}
	if fi.Size() > maxFileBytes {
		return nil, invalid("%s is larger than %d MB", rel, maxFileBytes>>20)
	}
	return os.ReadFile(full)
}

// loadOne reads and parses one item file. The file name decides the id. A different id: key
// gives a warning, not a failure. An empty file is an id reserved by NextID: ErrNotFound.
func loadOne(root, kind, id string) (e entity, warn *FileError, err error) {
	if err := checkID(kind, id); err != nil {
		return nil, nil, err
	}
	rel := relFor(kind, id)
	data, err := readBelowDot(root, rel)
	if err != nil {
		return nil, nil, err
	}
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("%w: %s is an empty reserved id", ErrNotFound, rel)
	}
	e, _, err = parseEntity(kind, data)
	if err != nil {
		return nil, nil, err
	}
	was := e.fileID()
	if was != id {
		if err := e.setFileID(id); err != nil {
			return nil, nil, err
		}
		if was != "" && was != "/v0" {
			warn = &FileError{Path: dotRel(rel), Warn: true,
				Err: fmt.Errorf("front matter id %q differs from the file name; using %q", was, id)}
		}
	}
	if s, ok := e.(*Snapshot); ok {
		s.ID = s.fileID()
	}
	e.setRev(revOf(data))
	if err := normalizeLoaded(e); err != nil {
		w := &FileError{Path: dotRel(rel), Warn: true, Err: err}
		if warn == nil {
			warn = w
		} else {
			warn = &FileError{Path: dotRel(rel), Warn: true, Err: errors.Join(warn.Err, err)}
		}
	}
	return e, warn, nil
}

// normalizeLoaded makes sure lists are never nil (so JSON shows [] and not null), then checks
// the values. A bad value is returned for a warning, and the item is still shown.
func normalizeLoaded(e entity) error {
	switch v := e.(type) {
	case *Milestone:
		if v.Features == nil {
			v.Features = []string{}
		}
	case *Task:
		if v.Links == nil {
			v.Links = []string{}
		}
		if v.Unlinks == nil {
			v.Unlinks = []string{}
		}
		if v.Log == nil {
			v.Log = []LogEntry{}
		}
	}
	return e.normalize()
}

func loadEntity(root, kind, id string) (entity, error) {
	kind, err := normKind(kind)
	if err != nil {
		return nil, err
	}
	e, _, err := loadOne(root, kind, id)
	return e, err
}

func loadAs[T any](root, kind, id string) (*T, error) {
	e, err := loadEntity(root, kind, id)
	if err != nil {
		return nil, err
	}
	return any(e).(*T), nil
}

// LoadItem loads one item by kind and id. The result is a pointer: *Milestone, *Feature,
// *Task, *Snapshot (id "T-1/v3") or *Note.
func LoadItem(root, kind, id string) (any, error) {
	e, err := loadEntity(root, kind, id)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// LoadMilestone loads milestones/<id>.md.
func LoadMilestone(root, id string) (*Milestone, error) {
	return loadAs[Milestone](root, KindMilestone, id)
}

// LoadFeature loads features/<id>.md.
func LoadFeature(root, id string) (*Feature, error) { return loadAs[Feature](root, KindFeature, id) }

// LoadTask loads tasks/<id>.md.
func LoadTask(root, id string) (*Task, error) { return loadAs[Task](root, KindTask, id) }

// LoadSnapshot loads snapshots/<task>/v<N>.md. The id is "T-1/v3".
func LoadSnapshot(root, id string) (*Snapshot, error) {
	return loadAs[Snapshot](root, KindSnapshot, id)
}

// LoadNote loads notes/<id>.md.
func LoadNote(root, id string) (*Note, error) { return loadAs[Note](root, KindNote, id) }

// idNum is the number in an id such as T-12 (0 when there is none).
func idNum(id string) int {
	if i := strings.IndexByte(id, '-'); i >= 0 {
		rest := id[i+1:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[:j]
		}
		n, _ := strconv.Atoi(rest)
		return n
	}
	return 0
}

// readDirBelowDot lists a folder below .drashta through pathguard. A missing folder gives no entries.
func readDirBelowDot(root, rel string) ([]os.DirEntry, error) {
	full, err := pathguard.Resolve(root, dotRel(rel))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return os.ReadDir(full)
}

// LoadProject reads every .drashta/{milestones,features,tasks,snapshots,notes}/*.md file of the
// project at root (root is the project folder, not the .drashta folder).
//
// One bad file never stops the others: its problem is added to the error list as a *FileError
// and the file is skipped. A *FileError with Warn set means the item was loaded anyway.
// A project without a .drashta folder gives an empty Tracker and no errors.
func LoadProject(root string) (Tracker, []error) {
	tr := Tracker{
		Milestones: []Milestone{}, Features: []Feature{}, Tasks: []Task{},
		Snapshots: []Snapshot{}, Notes: []Note{},
	}
	var errs []error
	if _, err := pathguard.Resolve(root, dotName); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return tr, nil
		}
		return tr, []error{err}
	}

	add := func(kind, id string, rel string) {
		e, warn, err := loadOne(root, kind, id)
		if warn != nil {
			errs = append(errs, warn)
		}
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return // an empty reserved id is not a problem
			}
			errs = append(errs, &FileError{Path: dotRel(rel), Err: err})
			return
		}
		switch v := e.(type) {
		case *Milestone:
			tr.Milestones = append(tr.Milestones, *v)
		case *Feature:
			tr.Features = append(tr.Features, *v)
		case *Task:
			tr.Tasks = append(tr.Tasks, *v)
		case *Snapshot:
			tr.Snapshots = append(tr.Snapshots, *v)
		case *Note:
			tr.Notes = append(tr.Notes, *v)
		}
	}

	for _, kind := range []string{KindMilestone, KindFeature, KindTask, KindNote} {
		dir := kindDir[kind]
		entries, err := readDirBelowDot(root, dir)
		if err != nil {
			errs = append(errs, &FileError{Path: dotRel(dir), Err: err})
			continue
		}
		for _, ent := range entries {
			name := ent.Name()
			if ent.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
				continue
			}
			id := strings.TrimSuffix(name, ".md")
			rel := path.Join(dir, name)
			if !idRe[kind].MatchString(id) {
				errs = append(errs, &FileError{Path: dotRel(rel), Warn: true,
					Err: fmt.Errorf("ignored: the file name is not a %s id", kind)})
				continue
			}
			add(kind, id, rel)
		}
	}

	// snapshots/<task>/v<N>.md; snapshots/inbox is the drop folder and is skipped here.
	taskDirs, err := readDirBelowDot(root, "snapshots")
	if err != nil {
		errs = append(errs, &FileError{Path: dotRel("snapshots"), Err: err})
	}
	for _, td := range taskDirs {
		if !td.IsDir() || !taskIDRe.MatchString(td.Name()) {
			continue
		}
		dirRel := path.Join("snapshots", td.Name())
		files, err := readDirBelowDot(root, dirRel)
		if err != nil {
			errs = append(errs, &FileError{Path: dotRel(dirRel), Err: err})
			continue
		}
		first := len(tr.Snapshots)
		for _, f := range files {
			m := snapMDRe.FindStringSubmatch(f.Name())
			if m == nil || f.IsDir() {
				continue
			}
			n, _ := strconv.Atoi(m[1])
			add(KindSnapshot, SnapshotID(td.Name(), n), path.Join(dirRel, f.Name()))
		}
		for i := first; i < len(tr.Snapshots); i++ {
			s := &tr.Snapshots[i]
			if s.Image == "" {
				s.Image = findImage(files, s.Version)
			}
		}
	}

	sort.SliceStable(tr.Milestones, func(i, j int) bool { return idNum(tr.Milestones[i].ID) < idNum(tr.Milestones[j].ID) })
	sort.SliceStable(tr.Features, func(i, j int) bool { return idNum(tr.Features[i].ID) < idNum(tr.Features[j].ID) })
	sort.SliceStable(tr.Tasks, func(i, j int) bool { return idNum(tr.Tasks[i].ID) < idNum(tr.Tasks[j].ID) })
	sort.SliceStable(tr.Notes, func(i, j int) bool { return idNum(tr.Notes[i].ID) < idNum(tr.Notes[j].ID) })
	sort.SliceStable(tr.Snapshots, func(i, j int) bool {
		a, b := tr.Snapshots[i], tr.Snapshots[j]
		if idNum(a.Task) != idNum(b.Task) {
			return idNum(a.Task) < idNum(b.Task)
		}
		return a.Version < b.Version
	})
	return tr, errs
}

// findImage looks for vN.<ext> with an allowed image extension next to the metadata file.
func findImage(files []os.DirEntry, version int) string {
	for _, f := range files {
		m := snapFileRe.FindStringSubmatch(f.Name())
		if m == nil || f.IsDir() || !imageExts[strings.ToLower(m[2])] {
			continue
		}
		if n, _ := strconv.Atoi(m[1]); n == version {
			return f.Name()
		}
	}
	return ""
}

// Classify tells which item a changed path belongs to. p is absolute or relative to root.
// It is a text check only (it does not touch the disk), so the indexer can use it on events
// for files that no longer exist.
//
// kind is one of the Kind* names, or "inbox" for a file in snapshots/inbox. For a snapshot,
// both vN.md and vN.<image> give kind "snapshot" and id "T-1/vN".
func Classify(root, p string) (kind, id string, ok bool) {
	rel := p
	if filepath.IsAbs(p) {
		r, err := filepath.Rel(root, p)
		if err != nil {
			return "", "", false
		}
		rel = r
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) < 3 || parts[0] != dotName {
		return "", "", false
	}
	parts = parts[1:]
	for _, k := range []string{KindMilestone, KindFeature, KindTask, KindNote} {
		if parts[0] == kindDir[k] && len(parts) == 2 && strings.HasSuffix(parts[1], ".md") {
			id := strings.TrimSuffix(parts[1], ".md")
			if idRe[k].MatchString(id) {
				return k, id, true
			}
			return "", "", false
		}
	}
	if parts[0] == "snapshots" && len(parts) == 3 {
		if parts[1] == "inbox" {
			return "inbox", parts[2], true
		}
		if taskIDRe.MatchString(parts[1]) {
			if m := snapFileRe.FindStringSubmatch(parts[2]); m != nil {
				if n, _ := strconv.Atoi(m[1]); n > 0 {
					if m[2] == "md" || imageExts[strings.ToLower(m[2])] {
						return KindSnapshot, SnapshotID(parts[1], n), true
					}
				}
			}
		}
	}
	return "", "", false
}
