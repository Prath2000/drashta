package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/pathguard"
)

func (a *app) snap(args []string) error {
	return sub(args, "snap", map[string]func([]string) error{
		"add":  a.snapAdd,
		"list": a.snapList,
	})
}

func (a *app) snapAdd(args []string) error {
	p, err := parseArgs(args, map[string]flagDef{
		"task": {value: true}, "image": {value: true}, "path": {value: true}, "url": {value: true},
		"title": {value: true}, "note": {value: true}, "quiet": {short: "q"},
	})
	if err != nil {
		return err
	}
	const usage = "drashta snap add --task T-12 (--image file | --path file) [--title text] [--note text] [-q]"
	if err := p.need(0, 0, usage); err != nil {
		return err
	}
	if p.has("url") {
		return fmt.Errorf("capturing a snapshot from a URL is not built yet (decision A3); take a screenshot and use --image or --path")
	}
	taskID := strings.TrimSpace(p.str("task"))
	image, path := p.str("image"), p.str("path")
	if taskID == "" || (image == "") == (path == "") {
		return usagef("usage: %s", usage)
	}
	who, err := a.author(p)
	if err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	task, err := authored.LoadTask(root, taskID)
	if err != nil {
		return fmt.Errorf("task %s: %w", taskID, notFoundText(err))
	}

	// Find the image file.
	srcArg := image
	if srcArg == "" {
		srcArg = path
	}
	abs := srcArg
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(a.env.Cwd, abs)
	}
	var real string
	if path != "" {
		real, err = pathguard.ResolveAny([]string{root}, abs)
		if err != nil {
			return fmt.Errorf("--path %s: %w (it must be a file inside the project)", srcArg, err)
		}
	} else if real, err = filepath.EvalSymlinks(abs); err != nil {
		return fmt.Errorf("--image %s: %w", srcArg, err)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a file", srcArg)
	}
	if fi.Size() > authored.MaxImageBytes {
		return authored.ErrTooLarge
	}
	f, err := os.Open(real)
	if err != nil {
		return err
	}
	defer f.Close()

	version, rel, err := authored.AddSnapshotImage(root, taskID, f, filepath.Ext(real))
	if err != nil {
		return err
	}

	source := "upload"
	if pathguard.Inside(root, real) {
		if r, err := filepath.Rel(root, real); err == nil {
			cand := "file:" + filepath.ToSlash(r)
			if _, err := authored.ParseRef(cand); err == nil {
				source = cand
			}
		}
	}
	title := strings.TrimSpace(p.str("title"))
	if title == "" {
		title = task.Title
	}
	snap := &authored.Snapshot{
		Task: taskID, Version: version, Title: oneLine(title, 200), Image: filepath.Base(rel), Source: source,
		Captured: authored.FormatTime(a.env.Now()), Author: who, Commit: headCommit(root),
		Verdict: authored.VerdictPending, Body: strings.TrimSpace(p.str("note")),
	}
	if _, err := authored.Save(root, snap, ""); err != nil {
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
		return err
	}

	// A new version moves the task from in_progress to review and adds a log line (04 section 4).
	moved := false
	_, err = authored.UpdateTask(root, taskID, func(t *authored.Task) error {
		now := a.env.Now()
		if t.Status == authored.StatusInProgress {
			t.Status = authored.StatusReview
			moved = true
		}
		t.Updated = authored.FormatTime(now)
		t.AppendLog(authored.FormatLogTime(now), who, fmt.Sprintf("posted snapshot v%d", version))
		return nil
	})
	if err != nil {
		return fmt.Errorf("the snapshot was saved, but the task %s could not be updated: %w", taskID, err)
	}
	id := authored.SnapshotID(taskID, version)
	if p.has("quiet") {
		a.printf("%s\n", id)
		return nil
	}
	a.printf("%s added: %s\n", id, rel)
	if moved {
		a.printf("%s in_progress -> review\n", taskID)
	}
	return nil
}

func (a *app) snapList(args []string) error {
	p, err := parseArgs(args, map[string]flagDef{"task": {value: true}, "json": {}})
	if err != nil {
		return err
	}
	if err := p.need(0, 0, "drashta snap list [--task T-12] [--json]"); err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	tr, errs := authored.LoadProject(root)
	a.warnAll(errs)
	snaps := []authored.Snapshot{}
	for _, s := range tr.Snapshots {
		if t := strings.TrimSpace(p.str("task")); t != "" && s.Task != t {
			continue
		}
		snaps = append(snaps, s)
	}
	if p.has("json") {
		return a.printJSON(snaps)
	}
	if len(snaps) == 0 {
		a.printf("no snapshots\n")
		return nil
	}
	tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tVERDICT\tAUTHOR\tCAPTURED\tTITLE")
	for _, s := range snaps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.ID, s.Verdict, s.Author, s.Captured, s.Title)
	}
	return tw.Flush()
}
