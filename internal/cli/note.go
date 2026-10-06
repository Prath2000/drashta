package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Prath2000/drashta/internal/authored"
)

func (a *app) note(args []string) error {
	return sub(args, "note", map[string]func([]string) error{
		"add":     a.noteAdd,
		"list":    a.noteList,
		"resolve": a.noteResolve,
	})
}

func (a *app) noteAdd(args []string) error {
	p, err := parseArgs(args, map[string]flagDef{
		"x": {value: true}, "y": {value: true}, "line": {value: true},
		"reply-to": {value: true}, "quiet": {short: "q"},
	})
	if err != nil {
		return err
	}
	const usage = "drashta note add <ref|id> <text> [--x 0.46 --y 0.2] [--line 30] [--reply-to N-1] [-q]"
	if err := p.need(2, -1, usage); err != nil {
		return err
	}
	ref, err := authored.ParseRef(expandRef(p.pos[0]))
	if err != nil {
		return err
	}
	text := strings.TrimSpace(strings.Join(p.pos[1:], " "))
	if text == "" {
		return usagef("usage: %s", usage)
	}
	x, err := p.float("x")
	if err != nil {
		return err
	}
	y, err := p.float("y")
	if err != nil {
		return err
	}
	line, err := p.int("line")
	if err != nil {
		return err
	}
	// A fragment in the ref says the same as --line or --x/--y.
	if from, _, ok := ref.Lines(); ok && line == nil {
		line = &from
	}
	if px, py, ok := ref.Point(); ok && x == nil && y == nil {
		x, y = &px, &py
	}
	if (x == nil) != (y == nil) {
		return usagef("a pin needs both --x and --y")
	}
	for _, v := range []*float64{x, y} {
		if v != nil && (*v < 0 || *v > 1) {
			return usagef("--x and --y must be between 0 and 1")
		}
	}
	if line != nil && *line < 1 {
		return usagef("--line must be 1 or more")
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
	replyTo := strings.TrimSpace(p.str("reply-to"))
	if replyTo != "" {
		if _, err := authored.LoadNote(root, replyTo); err != nil {
			return fmt.Errorf("note %s: %w", replyTo, notFoundText(err))
		}
	}
	anchor := ""
	if ref.Local() && ref.Type == authored.RefFile && line != nil {
		anchor = lineOf(root, ref.ID, *line)
	}

	n := &authored.Note{
		Ref: ref.String(), X: x, Y: y, Line: line, Anchor: anchor, Author: who,
		Created: authored.FormatTime(a.env.Now()), ReplyTo: replyTo, Body: text,
	}
	// Check first, so a bad request never reserves an id file (review M14).
	if err := authored.Validate(n); err != nil {
		return err
	}
	id, err := authored.NextID(root, authored.KindNote)
	if err != nil {
		return err
	}
	n.ID = id
	if _, err := authored.Save(root, n, ""); err != nil {
		_ = authored.Release(root, authored.KindNote, id)
		return err
	}
	if p.has("quiet") {
		a.printf("%s\n", id)
	} else {
		a.printf("created %s on %s\n", id, n.Ref)
	}
	return nil
}

func (a *app) noteList(args []string) error {
	p, err := parseArgs(args, map[string]flagDef{"ref": {value: true}, "all": {}, "json": {}})
	if err != nil {
		return err
	}
	if err := p.need(0, 0, "drashta note list [--ref ref] [--all] [--json]"); err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	want := ""
	if r := p.str("ref"); r != "" {
		ref, err := authored.ParseRef(expandRef(r))
		if err != nil {
			return err
		}
		want = ref.Canonical()
	}
	tr, errs := authored.LoadProject(root)
	a.warnAll(errs)

	notes := []authored.Note{}
	for _, n := range tr.Notes {
		if !p.has("all") && n.Resolved {
			continue
		}
		if want != "" {
			if r, err := authored.ParseRef(n.Ref); err != nil || r.Canonical() != want {
				continue
			}
		}
		notes = append(notes, n)
	}
	if p.has("json") {
		return a.printJSON(notes)
	}
	if len(notes) == 0 {
		a.printf("no notes\n")
		return nil
	}
	tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tAUTHOR\tREF\tTEXT")
	for _, n := range notes {
		state := "open"
		if n.Resolved {
			state = "resolved"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", n.ID, state, n.Author, n.Ref, oneLine(n.Body, 60))
	}
	return tw.Flush()
}

func (a *app) noteResolve(args []string) error {
	p, err := parseArgs(args, nil)
	if err != nil {
		return err
	}
	if err := p.need(1, 1, "drashta note resolve <N-id>"); err != nil {
		return err
	}
	id := strings.TrimSpace(p.pos[0])
	who, err := a.author(p)
	if err != nil {
		return err
	}
	root, err := a.project(p)
	if err != nil {
		return err
	}
	changed := false
	_, err = authored.UpdateNote(root, id, func(n *authored.Note) error {
		if n.Resolved {
			return nil
		}
		n.Resolved = true
		n.ResolvedBy = who
		n.ResolvedAt = authored.FormatTime(a.env.Now())
		changed = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("note %s: %w", id, notFoundText(err))
	}
	if !changed {
		a.printf("%s is already resolved\n", id)
		return nil
	}
	a.printf("resolved %s\n", id)
	return nil
}
