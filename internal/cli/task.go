package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Prath2000/drashta/internal/authored"
)

func (a *app) task(args []string) error {
	return sub(args, "task", map[string]func([]string) error{
		"new":    a.taskNew,
		"list":   a.taskList,
		"show":   a.taskShow,
		"status": a.taskStatus,
		"link":   a.taskLink,
	})
}

func validStatus(s string) bool {
	for _, v := range authored.Statuses {
		if v == s {
			return true
		}
	}
	return false
}

// checkDone keeps a Claude session inside the rules of 04-data-model.md section 2.5:
// never set a task to done unless the user said so.
func checkDone(author, status string, userSaid bool) error {
	if status == authored.StatusDone && !isHuman(author) && !userSaid {
		return fmt.Errorf("%s may not set a task to done unless the user said so; ask the user, then add --user-said-done", author)
	}
	return nil
}

func (a *app) taskNew(args []string) error {
	p, err := parseArgs(args, map[string]flagDef{
		"feature": {value: true}, "status": {value: true}, "assignee": {value: true},
		"body": {value: true}, "link": {value: true}, "quiet": {short: "q"}, "user-said-done": {},
	})
	if err != nil {
		return err
	}
	title := strings.TrimSpace(strings.Join(p.pos, " "))
	if title == "" {
		return usagef("usage: drashta task new <title> [--feature F-3] [--status todo] [--assignee name] [--body text] [--link ref]... [-q]")
	}
	who, err := a.author(p)
	if err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	status := p.str("status")
	if status == "" {
		status = authored.StatusTodo
	}
	if !validStatus(status) {
		return usagef("status %q is not one of: %s", status, strings.Join(authored.Statuses, ", "))
	}
	if err := checkDone(who, status, p.has("user-said-done")); err != nil {
		return err
	}
	feature := p.str("feature")
	if feature != "" {
		if _, err := authored.LoadFeature(root, feature); err != nil {
			return fmt.Errorf("feature %s: %w", feature, notFoundText(err))
		}
	}
	var links []string
	for _, l := range p.all("link") {
		r, err := authored.ParseRef(expandRef(l))
		if err != nil {
			return err
		}
		if err := checkTarget(root, r); err != nil {
			return err
		}
		links = append(links, r.String())
	}

	now := authored.FormatTime(a.env.Now())
	t := &authored.Task{
		Title: title, Status: status, Feature: feature, Assignee: p.str("assignee"),
		Created: now, CreatedBy: who, Updated: now, Links: links, Body: strings.TrimSpace(p.str("body")),
	}
	// Check first, so a bad request never reserves an id file (review M14).
	if err := authored.Validate(t); err != nil {
		return err
	}
	id, err := authored.NextID(root, authored.KindTask)
	if err != nil {
		return err
	}
	t.ID = id
	if _, err := authored.Save(root, t, ""); err != nil {
		_ = authored.Release(root, authored.KindTask, id)
		return err
	}
	if p.has("quiet") {
		a.printf("%s\n", id)
	} else {
		a.printf("created %s (%s): %s\n", id, status, title)
	}
	return nil
}

func (a *app) taskList(args []string) error {
	p, err := parseArgs(args, map[string]flagDef{"status": {value: true}, "feature": {value: true}, "json": {}})
	if err != nil {
		return err
	}
	if err := p.need(0, 0, "drashta task list [--status review] [--feature F-1] [--json]"); err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	if s := p.str("status"); s != "" && !validStatus(s) {
		return usagef("status %q is not one of: %s", s, strings.Join(authored.Statuses, ", "))
	}
	tr, errs := authored.LoadProject(root)
	a.warnAll(errs)

	tasks := []authored.Task{}
	for _, t := range tr.Tasks {
		if s := p.str("status"); s != "" && t.Status != s {
			continue
		}
		if f := p.str("feature"); f != "" && t.Feature != f {
			continue
		}
		tasks = append(tasks, t)
	}
	if p.has("json") {
		return a.printJSON(tasks)
	}
	if len(tasks) == 0 {
		a.printf("no tasks\n")
		return nil
	}
	tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tFEATURE\tTITLE")
	for _, t := range tasks {
		feature := t.Feature
		if feature == "" {
			feature = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.ID, t.Status, feature, t.Title)
	}
	return tw.Flush()
}

func (a *app) taskShow(args []string) error {
	p, err := parseArgs(args, map[string]flagDef{"json": {}})
	if err != nil {
		return err
	}
	if err := p.need(1, 1, "drashta task show <T-id> [--json]"); err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(p.pos[0])
	t, err := authored.LoadTask(root, id)
	if err != nil {
		return fmt.Errorf("task %s: %w", id, notFoundText(err))
	}
	tr, errs := authored.LoadProject(root)
	a.warnAll(errs)

	var snaps []authored.Snapshot
	for _, s := range tr.Snapshots {
		if s.Task == id {
			snaps = append(snaps, s)
		}
	}
	var notes []authored.Note
	for _, n := range tr.Notes {
		if r, err := authored.ParseRef(n.Ref); err == nil && r.Local() &&
			(r.Type == authored.RefTask && r.ID == id || r.Type == authored.RefSnapshot && strings.HasPrefix(r.ID, id+"/")) {
			notes = append(notes, n)
		}
	}
	if p.has("json") {
		if snaps == nil {
			snaps = []authored.Snapshot{}
		}
		if notes == nil {
			notes = []authored.Note{}
		}
		return a.printJSON(map[string]any{"task": t, "snapshots": snaps, "notes": notes})
	}

	a.printf("%s  %s\n", t.ID, t.Title)
	a.printf("status:    %s\n", t.Status)
	if t.Feature != "" {
		line := t.Feature
		for _, f := range tr.Features {
			if f.ID == t.Feature {
				line += "  " + f.Title
			}
		}
		a.printf("feature:   %s\n", line)
	}
	if t.Assignee != "" {
		a.printf("assignee:  %s\n", t.Assignee)
	}
	if t.Created != "" {
		by := ""
		if t.CreatedBy != "" {
			by = " by " + t.CreatedBy
		}
		a.printf("created:   %s%s\n", t.Created, by)
	}
	if t.Updated != "" {
		a.printf("updated:   %s\n", t.Updated)
	}
	if len(t.Links) > 0 {
		a.printf("links:\n")
		for _, l := range t.Links {
			a.printf("  %s\n", l)
		}
	}
	if len(snaps) > 0 {
		a.printf("snapshots:\n")
		for _, s := range snaps {
			a.printf("  v%d  %-8s  %s  %s\n", s.Version, s.Verdict, s.Author, s.Title)
		}
	}
	if len(notes) > 0 {
		open := 0
		for _, n := range notes {
			if !n.Resolved {
				open++
			}
		}
		a.printf("notes: %d open, %d resolved\n", open, len(notes)-open)
		for _, n := range notes {
			if !n.Resolved {
				a.printf("  %s  %s  %s  %s\n", n.ID, n.Author, n.Ref, oneLine(n.Body, 60))
			}
		}
	}
	if t.Body != "" {
		a.printf("\n%s\n", t.Body)
	}
	if len(t.Log) > 0 {
		a.printf("\nlog:\n")
		for _, e := range t.Log {
			a.printf("  %s\n", logText(e))
		}
	}
	return nil
}

func (a *app) taskStatus(args []string) error {
	p, err := parseArgs(args, map[string]flagDef{"user-said-done": {}})
	if err != nil {
		return err
	}
	if err := p.need(2, 2, "drashta task status <T-id> <backlog|todo|in_progress|review|done> [--user-said-done]"); err != nil {
		return err
	}
	id, status := strings.TrimSpace(p.pos[0]), strings.TrimSpace(p.pos[1])
	if !validStatus(status) {
		return usagef("status %q is not one of: %s", status, strings.Join(authored.Statuses, ", "))
	}
	who, err := a.author(p)
	if err != nil {
		return err
	}
	if err := checkDone(who, status, p.has("user-said-done")); err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	var from string
	changed := false
	_, err = authored.UpdateTask(root, id, func(t *authored.Task) error {
		from = t.Status
		if t.Status == status {
			return nil
		}
		now := a.env.Now()
		t.Status = status
		t.Updated = authored.FormatTime(now)
		t.AppendLog(authored.FormatLogTime(now), who, fmt.Sprintf("status %s -> %s", from, status))
		changed = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("task %s: %w", id, notFoundText(err))
	}
	if !changed {
		a.printf("%s is already %s\n", id, status)
		return nil
	}
	a.printf("%s %s -> %s\n", id, from, status)
	return nil
}

func (a *app) taskLink(args []string) error {
	p, err := parseArgs(args, nil)
	if err != nil {
		return err
	}
	if err := p.need(2, 2, "drashta task link <T-id> <ref>"); err != nil {
		return err
	}
	id := strings.TrimSpace(p.pos[0])
	ref, err := authored.ParseRef(expandRef(p.pos[1]))
	if err != nil {
		return err
	}
	who, err := a.author(p)
	if err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	if err := checkTarget(root, ref); err != nil {
		return err
	}
	if ref.Local() && ref.Type == authored.RefTask && ref.ID == id {
		return usagef("a task cannot link to itself")
	}
	text := ref.String()
	added := false
	_, err = authored.UpdateTask(root, id, func(t *authored.Task) error {
		for _, l := range t.Links {
			if l == text {
				return nil
			}
		}
		now := a.env.Now()
		t.Links = append(t.Links, text)
		t.Updated = authored.FormatTime(now)
		t.AppendLog(authored.FormatLogTime(now), who, "linked "+text)
		added = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("task %s: %w", id, notFoundText(err))
	}
	if !added {
		a.printf("%s is already linked to %s\n", id, text)
		return nil
	}
	a.printf("%s linked %s\n", id, text)
	return nil
}

// notFoundText turns ErrNotFound into a short message without the file path noise.
func notFoundText(err error) error {
	if errors.Is(err, authored.ErrNotFound) {
		return errors.New("not found")
	}
	return err
}

// logText shows one log entry on one line.
func logText(e authored.LogEntry) string {
	if e.Time == "" && e.By == "" {
		return e.Text
	}
	return strings.TrimSpace(e.Time + "  " + e.By + "  " + e.Text)
}
