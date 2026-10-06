package indexer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/pathguard"
)

// inboxDropRe matches a drop that names its task: T-12--anything.png (04-data-model.md section 4).
var inboxDropRe = regexp.MustCompile(`^(T-[0-9]+)--(.+)\.([A-Za-z]+)$`)

// fileInboxDrop files one image from .drashta/snapshots/inbox/ into its task, when the name
// says which task (T-12--x.png) and that task exists:
//   - the image becomes snapshots/T-12/v<N>.<ext> (authored.AddSnapshotImage, never overwrites),
//   - a metadata file v<N>.md is written with verdict "pending",
//   - the drop is deleted,
//   - the task moves from in_progress to review and gets a log line (spec 9.6); in another
//     status it only gets the log line.
//
// It returns the absolute paths of the files it wrote (to mirror them at once). A drop without
// a task name, or for an unknown task, is left alone: the Inbox lists it as unfiled.
func (i *Indexer) fileInboxDrop(root, name string) ([]string, error) {
	m := inboxDropRe.FindStringSubmatch(name)
	if m == nil {
		return nil, nil
	}
	taskID, title, ext := m[1], m[2], strings.ToLower(m[3])
	rel := path.Join(".drashta", "snapshots", "inbox", name)
	drop, err := pathguard.Resolve(root, rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // already filed (this event is the delete)
		}
		return nil, err
	}
	fi, err := os.Stat(drop)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, nil
	}
	if _, err := authored.LoadTask(root, taskID); err != nil {
		if errors.Is(err, authored.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	f, err := os.Open(drop)
	if err != nil {
		return nil, err
	}
	version, imgRel, err := authored.AddSnapshotImage(root, taskID, f, ext)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("file inbox drop %s: %w", name, err)
	}
	now := i.clock.Now().In(i.loc)
	snap := &authored.Snapshot{
		Task: taskID, Version: version, Title: strings.TrimSpace(strings.ReplaceAll(title, "-", " ")),
		Image: path.Base(imgRel), Source: "file:" + rel, Captured: authored.FormatTime(now),
		Author: "drashta", Verdict: authored.VerdictPending,
	}
	if _, err := authored.Save(root, snap, ""); err != nil {
		return nil, fmt.Errorf("file inbox drop %s: %w", name, err)
	}
	if err := os.Remove(drop); err != nil {
		i.logOnce("", "inbox", err)
	}
	if _, err := authored.UpdateTask(root, taskID, func(t *authored.Task) error {
		if t.Status == authored.StatusInProgress {
			t.Status = authored.StatusReview
			t.Updated = authored.FormatTime(now)
			t.AppendLog(authored.FormatLogTime(now), "drashta",
				fmt.Sprintf("posted snapshot v%d; status in_progress -> review", version))
			return nil
		}
		t.AppendLog(authored.FormatLogTime(now), "drashta", fmt.Sprintf("posted snapshot v%d", version))
		return nil
	}); err != nil {
		return nil, fmt.Errorf("file inbox drop %s: %w", name, err)
	}
	return []string{
		filepath.Join(root, ".drashta", "snapshots", taskID, fmt.Sprintf("v%d.md", version)),
		filepath.Join(root, ".drashta", "tasks", taskID+".md"),
	}, nil
}
