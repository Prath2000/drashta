package authored

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Prath2000/drashta/internal/pathguard"
)

var imageExts = map[string]bool{"png": true, "jpg": true, "jpeg": true, "webp": true, "gif": true}

// ImageExts lists the snapshot image extensions that are allowed.
var ImageExts = []string{"png", "jpg", "jpeg", "webp", "gif"}

// gitignoreText is written once by EnsureGitignore (decision A2: text is committed, snapshot
// images are not). Delete the image lines to commit images.
const gitignoreText = `# Written by drashta. Snapshot images are not committed by default.
# Delete the lines below if you want to commit them.
snapshots/**/*.png
snapshots/**/*.jpg
snapshots/**/*.jpeg
snapshots/**/*.webp
snapshots/**/*.gif
# Local files that never belong in git.
.lock
.trash/
.tmp-*
`

// ReadmeText is written once to .drashta/README.md. It tells humans and Claude the rules.
const ReadmeText = `# .drashta

This folder holds the tracker data for this project: milestones, features, tasks, snapshots and notes. Each item is one markdown file with YAML front matter, and these files are the source of truth (drashta only keeps a cache of them). Do not rename files: the file name is the id (T-12, F-3, M-1, N-40, and snapshots/T-12/v3). Keep keys you do not know, because other tools may use them. Change only the keys you own for your action, and append to a task's links list and its "## Log" section instead of editing old lines. A Claude session may create tasks, notes and snapshots, set a task status (never "done" unless the user said so), append links and log lines. It must not change the title, body, assignee, verdict or review note of items the user wrote, and it must not delete files.

The easy path is the command line, which follows these rules for you:

- drashta task new "title" [--feature F-3]
- drashta task status T-12 review
- drashta task link T-12 commit:abc1234
- drashta task list, drashta task show T-12
- drashta note add T-12 "text", drashta note resolve N-40
- drashta snap add --task T-12 --image shot.png
`

// EnsureLayout makes the .drashta folder with its sub folders, an empty .lock file, README.md
// and .gitignore. Files that already exist are never changed. It is safe to call again.
func EnsureLayout(root string) error {
	fi, err := os.Stat(root)
	if err != nil {
		return notFound(err)
	}
	if !fi.IsDir() {
		return invalid("%s is not a folder", root)
	}
	if err := os.MkdirAll(filepath.Join(root, dotName), 0o755); err != nil {
		return err
	}
	dot, err := pathguard.Resolve(root, dotName)
	if err != nil {
		return err
	}
	for _, d := range []string{"milestones", "features", "tasks", "snapshots", "notes"} {
		if err := os.MkdirAll(filepath.Join(dot, d), 0o755); err != nil {
			return err
		}
	}
	for _, f := range []struct{ name, text string }{
		{".lock", ""}, {"README.md", ReadmeText}, {".gitignore", gitignoreText},
	} {
		if err := createIfMissing(filepath.Join(dot, f.name), f.text); err != nil {
			return err
		}
	}
	return nil
}

// EnsureGitignore writes .drashta/.gitignore (snapshot images ignored, decision A2) when the
// file does not exist. An existing file is left alone, so a user who removed the lines to
// commit images is not overruled.
func EnsureGitignore(root string) error {
	dot, err := ensureDot(root)
	if err != nil {
		return err
	}
	return createIfMissing(filepath.Join(dot, ".gitignore"), gitignoreText)
}

func createIfMissing(p, text string) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

var versionFileRe = regexp.MustCompile(`^v([0-9]+)\.`)

// maxVersion is the highest vN used by any file in dir (md or image).
func maxVersion(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	max := 0
	for _, e := range entries {
		if m := versionFileRe.FindStringSubmatch(e.Name()); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > max {
				max = n
			}
		}
	}
	return max
}

// AddSnapshotImage stores a new snapshot image as .drashta/snapshots/<task>/v<N>.<ext> and
// returns N and the path relative to the project root (for example
// .drashta/snapshots/T-12/v3.png).
//
// N is one more than the highest version ever used for the task (the trash counts). An
// existing image is never overwritten. ext must be png, jpg, jpeg, webp or gif (with or
// without a dot, any case). The image must not be empty or bigger than MaxImageBytes (20 MB).
// taskID must look like T-12, so a value such as "../x" is rejected, and the destination
// folder must resolve inside the project (pathguard). The bytes are not inspected.
//
// This only stores the image. Write the metadata with Save(root, &Snapshot{...}, "").
func AddSnapshotImage(root, taskID string, src io.Reader, ext string) (version int, relPath string, err error) {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	if !imageExts[ext] {
		return 0, "", fmt.Errorf("%w: %q (allowed: %s)", ErrBadExt, ext, strings.Join(ImageExts, ", "))
	}
	if !taskIDRe.MatchString(taskID) {
		return 0, "", invalid("task id %q is not in the form T-12", taskID)
	}
	if src == nil {
		return 0, "", invalid("no image data")
	}
	dirRel := path.Join("snapshots", taskID)
	dir, err := mkdirBelowDot(root, dirRel)
	if err != nil {
		return 0, "", err
	}

	// Copy to a temp file first (outside the lock), with the size cap.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return 0, "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // a no-op after a successful link; removes the temp name either way
	n, err := io.Copy(tmp, io.LimitReader(src, MaxImageBytes+1))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, "", err
	}
	if n > MaxImageBytes {
		return 0, "", ErrTooLarge
	}
	if n == 0 {
		return 0, "", invalid("the image is empty")
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return 0, "", err
	}

	unlock, err := lockRoot(root)
	if err != nil {
		return 0, "", err
	}
	defer unlock()
	v := maxVersion(dir)
	if t := trashDirIfAny(root, path.Join("snapshots", taskID)); t != "" {
		if m := maxVersion(t); m > v {
			v = m
		}
	}
	for tries := 0; tries < 10000; tries++ {
		v++
		name := "v" + strconv.Itoa(v) + "." + ext
		final := filepath.Join(dir, name)
		if err := os.Link(tmpName, final); err != nil {
			if _, serr := os.Lstat(final); serr == nil {
				continue // taken by a writer that does not use the lock
			}
			// No hard links on this file system: rename instead (we hold the lock).
			if err := os.Rename(tmpName, final); err != nil {
				return 0, "", err
			}
		}
		syncDir(dir)
		return v, dotRel(dirRel, name), nil
	}
	return 0, "", fmt.Errorf("authored: no free snapshot version for %s", taskID)
}
