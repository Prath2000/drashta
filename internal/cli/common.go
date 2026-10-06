package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/runx"
)

var (
	bareTaskRe     = regexp.MustCompile(`^T-[0-9]+$`)
	bareFeatureRe  = regexp.MustCompile(`^F-[0-9]+$`)
	bareMileRe     = regexp.MustCompile(`^M-[0-9]+$`)
	bareNoteRe     = regexp.MustCompile(`^N-[0-9]+$`)
	bareSnapshotRe = regexp.MustCompile(`^T-[0-9]+/v[0-9]+$`)
	hexRe          = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

// expandRef lets people write T-12 instead of task:T-12.
func expandRef(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case bareTaskRe.MatchString(s):
		return "task:" + s
	case bareFeatureRe.MatchString(s):
		return "feature:" + s
	case bareMileRe.MatchString(s):
		return "milestone:" + s
	case bareNoteRe.MatchString(s):
		return "note:" + s
	case bareSnapshotRe.MatchString(s):
		return "snapshot:" + s
	}
	return s
}

// checkTarget makes sure a ref to a tracker item of this project points at something real.
// Other kinds of refs (files, commits, sessions, servers) and refs to other projects are
// not checked here.
func checkTarget(root string, r authored.Ref) error {
	if !r.Local() || !r.IsAuthored() {
		return nil
	}
	var err error
	switch r.Type {
	case authored.RefTask:
		_, err = authored.LoadTask(root, r.ID)
	case authored.RefFeature:
		_, err = authored.LoadFeature(root, r.ID)
	case authored.RefMilestone:
		_, err = authored.LoadMilestone(root, r.ID)
	case authored.RefSnapshot:
		_, err = authored.LoadSnapshot(root, r.ID)
	case authored.RefNote:
		_, err = authored.LoadNote(root, r.ID)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", r.Type, r.ID, notFoundText(err))
	}
	return nil
}

// headCommit returns the first 10 characters of HEAD for a git project, or "" (04 section 4:
// "HEAD at capture time, if any"). It never fails the command.
func headCommit(root string) string {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return ""
	}
	res, err := runx.Run(context.Background(), runx.Options{Dir: root, Timeout: 3 * time.Second},
		"git", "--no-optional-locks", "rev-parse", "HEAD")
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	h := strings.TrimSpace(string(res.Stdout))
	if !hexRe.MatchString(h) {
		return ""
	}
	return h[:10]
}

// lineOf returns the text of line n of a project file, for the note anchor. It reads through
// pathguard, takes at most 200 characters, and returns "" on any problem.
func lineOf(root, rel string, n int) string {
	full, err := pathguard.Resolve(root, rel)
	if err != nil {
		return ""
	}
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() || fi.Size() > 4<<20 {
		return ""
	}
	f, err := os.Open(full)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for i := 1; sc.Scan(); i++ {
		if i == n {
			line := strings.TrimSpace(sc.Text())
			if r := []rune(line); len(r) > 200 {
				line = string(r[:200])
			}
			return line
		}
	}
	return ""
}
