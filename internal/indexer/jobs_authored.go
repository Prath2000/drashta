package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/autolink"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/store"
)

// authoredChange is one item to write (item != nil) or delete (item == nil).
type authoredChange struct {
	kind, id string
	item     any // *authored.Milestone, *Feature, *Task, *Snapshot, *Note
}

// jobAuthored mirrors .drashta/ files into the tracker tables (FTS in the same transaction),
// writes the day-readout events and per-day rows, and publishes "tracker" events.
// paths nil = the whole project (sweep); else only the items those absolute paths belong to.
func (i *Indexer) jobAuthored(ctx context.Context, id string, paths []string) error {
	ps := i.state(id)
	if ps == nil {
		return nil
	}
	root := ps.p.Path
	var changes []authoredChange
	var inbox []string

	if paths == nil {
		tr, errs := authored.LoadProject(root)
		for _, e := range errs {
			i.logOnce(id, "authored", e)
		}
		stored, err := i.c.DB.IdxAuthoredIDs(ctx, id)
		if err != nil {
			i.setStatus(id, "authored", err)
			return err
		}
		have := map[string]bool{}
		for n := range tr.Milestones {
			changes = append(changes, authoredChange{authored.KindMilestone, tr.Milestones[n].ID, &tr.Milestones[n]})
		}
		for n := range tr.Features {
			changes = append(changes, authoredChange{authored.KindFeature, tr.Features[n].ID, &tr.Features[n]})
		}
		for n := range tr.Tasks {
			changes = append(changes, authoredChange{authored.KindTask, tr.Tasks[n].ID, &tr.Tasks[n]})
		}
		for n := range tr.Snapshots {
			changes = append(changes, authoredChange{authored.KindSnapshot, tr.Snapshots[n].ID, &tr.Snapshots[n]})
		}
		for n := range tr.Notes {
			changes = append(changes, authoredChange{authored.KindNote, tr.Notes[n].ID, &tr.Notes[n]})
		}
		for _, c := range changes {
			have[c.kind+"\x00"+c.id] = true
		}
		for kind, ids := range stored {
			for _, sid := range ids {
				if !have[kind+"\x00"+sid] {
					changes = append(changes, authoredChange{kind: kind, id: sid})
				}
			}
		}
	} else {
		seen := map[string]bool{}
		for _, p := range paths {
			kind, iid, ok := authored.Classify(root, p)
			if !ok {
				continue
			}
			if kind == "inbox" {
				inbox = append(inbox, iid)
				continue
			}
			if seen[kind+"\x00"+iid] {
				continue
			}
			seen[kind+"\x00"+iid] = true
			item, err := authored.LoadItem(root, kind, iid)
			switch {
			case err == nil:
				changes = append(changes, authoredChange{kind, iid, item})
			case errors.Is(err, authored.ErrNotFound) || errors.Is(err, fs.ErrNotExist):
				changes = append(changes, authoredChange{kind: kind, id: iid})
			default:
				i.logOnce(id, "authored", err)
			}
		}
	}

	if len(changes) > 0 {
		if err := i.writeAuthored(ctx, id, changes); errors.Is(err, errProjectGone) {
			return nil
		} else if err != nil {
			if ctx.Err() == nil {
				i.setStatus(id, "authored", err)
			}
			return err
		}
		i.setStatus(id, "authored", nil)
		for _, c := range changes {
			i.publish(live.TrackerEvent(id, c.kind, c.id))
		}
		i.publishCounts(ctx)
		i.autolink(ctx, id)
	}
	// Images dropped in snapshots/inbox/: a name like T-12--x.png is filed into task T-12 at once
	// (04-data-model.md section 4). Others stay for the user to pick a task (tracker API).
	var filed []string
	for _, name := range inbox {
		paths, err := i.fileInboxDrop(root, name)
		if err != nil {
			i.logOnce(id, "inbox", err)
		}
		filed = append(filed, paths...)
		i.publish(live.TrackerEvent(id, authored.KindSnapshot, "inbox/"+name))
	}
	if len(filed) > 0 {
		return i.jobAuthored(ctx, id, filed)
	}
	return nil
}

func (i *Indexer) writeAuthored(ctx context.Context, id string, changes []authoredChange) error {
	// Old task statuses, for task_status events.
	oldStatus := map[string]string{}
	for _, c := range changes {
		if c.kind == authored.KindTask && c.item != nil {
			s, err := i.c.DB.IdxTaskStatus(ctx, id, c.id)
			if err != nil {
				return err
			}
			oldStatus[c.id] = s
		}
	}
	touched := map[string]bool{}
	err := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
		if !i.known(id) {
			return errProjectGone // removed while the job ran: no orphan rows (M11)
		}
		for _, c := range changes {
			ref := c.kind + ":" + c.id
			if c.item == nil {
				if err := store.DeleteAuthoredTx(ctx, tx, id, c.kind, c.id); err != nil {
					return err
				}
				if err := store.IdxDeleteEventsTx(ctx, tx, id, "", ref); err != nil {
					return err
				}
				touched[i.dayOf(i.clock.Now())] = true
				continue
			}
			var events []store.Event
			switch v := c.item.(type) {
			case *authored.Milestone:
				if err := store.UpsertAuthoredMilestoneTx(ctx, tx, store.Milestone{ProjectID: id, ID: v.ID,
					Title: v.Title, Due: v.Due, State: v.State, Body: v.Body, FileRev: v.Rev}); err != nil {
					return err
				}
			case *authored.Feature:
				if err := store.UpsertAuthoredFeatureTx(ctx, tx, store.Feature{ProjectID: id, ID: v.ID,
					Title: v.Title, MilestoneID: v.Milestone, Body: v.Body, FileRev: v.Rev}); err != nil {
					return err
				}
			case *authored.Task:
				if err := store.UpsertAuthoredTaskTx(ctx, tx, store.Task{ProjectID: id, ID: v.ID, Title: v.Title,
					Status: v.Status, FeatureID: v.Feature, Assignee: v.Assignee, Created: v.Created,
					CreatedBy: v.CreatedBy, Updated: v.Updated, Body: v.Body, Log: logText(v.Log),
					FileRev: v.Rev}); err != nil {
					return err
				}
				links := make([]store.TaskLink, 0, len(v.Links))
				for _, l := range v.Links {
					links = append(links, store.TaskLink{Ref: canonicalRef(l), Source: "manual"})
				}
				if err := store.UpsertAuthoredTaskLinksTx(ctx, tx, id, v.ID, "manual", links); err != nil {
					return err
				}
				if t, ok := authored.ParseTime(v.Created); ok {
					events = append(events, store.Event{TS: t, Kind: "task_created", Ref: ref, Label: v.Title})
				}
				if old := oldStatus[v.ID]; old != "" && old != v.Status {
					t, ok := authored.ParseTime(v.Updated)
					if !ok {
						t = i.clock.Now()
					}
					events = append(events, store.Event{TS: t, Kind: "task_status", Ref: ref,
						Label: v.ID + " " + old + " -> " + v.Status,
						Meta:  metaJSON(map[string]any{"from": old, "to": v.Status})})
				}
				if t, ok := authored.ParseTime(v.Updated); ok {
					touched[i.dayOf(t)] = true
				}
			case *authored.Snapshot:
				ver := v.Version
				if err := store.UpsertAuthoredSnapshotTx(ctx, tx, store.Snapshot{ProjectID: id, TaskID: v.Task,
					Version: ver, Title: v.Title, ImagePath: v.ImageRel(), Source: v.Source, Captured: v.Captured,
					Author: v.Author, CommitID: v.Commit, Verdict: v.Verdict, ReviewedBy: v.ReviewedBy,
					ReviewedAt: v.ReviewedAt, ReviewNote: v.ReviewNote, FileRev: v.Rev}); err != nil {
					return err
				}
				if t, ok := authored.ParseTime(v.Captured); ok {
					events = append(events, store.Event{TS: t, Kind: "snapshot", Ref: ref, Label: v.Title,
						Meta: metaJSON(map[string]any{"task": v.Task, "version": ver, "verdict": v.Verdict, "context": v.Verdict})})
				}
				if err := store.IdxDeleteEventsTx(ctx, tx, id, "verdict", ref); err != nil {
					return err
				}
				if v.Verdict != "" && v.Verdict != authored.VerdictPending {
					if t, ok := authored.ParseTime(v.ReviewedAt); ok {
						events = append(events, store.Event{TS: t, Kind: "verdict", Ref: ref,
							Label: v.ID + " " + v.Verdict, Meta: metaJSON(map[string]any{"verdict": v.Verdict, "context": v.Verdict, "note": v.ReviewNote})})
					}
				}
			case *authored.Note:
				if err := store.UpsertAuthoredNoteTx(ctx, tx, store.Note{ProjectID: id, ID: v.ID, Ref: v.Ref,
					X: v.X, Y: v.Y, Line: v.Line, Anchor: v.Anchor, Author: v.Author, Created: v.Created,
					Resolved: v.Resolved, ResolvedAt: v.ResolvedAt, ReplyTo: v.ReplyTo, Body: v.Body,
					FileRev: v.Rev}); err != nil {
					return err
				}
				if t, ok := authored.ParseTime(v.Created); ok {
					events = append(events, store.Event{TS: t, Kind: "note", Ref: ref, Label: firstLine(v.Body, 120),
						Meta: metaJSON(map[string]any{"target": v.Ref})})
				}
			}
			for _, e := range events {
				e.ProjectID = id
				e.Day = i.dayOf(e.TS)
				touched[e.Day] = true
				if _, err := store.AddEventTx(ctx, tx, e); err != nil {
					return err
				}
			}
		}
		for d := range touched {
			if err := store.RecomputeDailyTx(ctx, tx, id, d, i.loc); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		i.publishDays(id, touched)
	}
	return err
}

// logText turns the task log back into text, one "- " line per entry.
func logText(entries []authored.LogEntry) string {
	var b strings.Builder
	for _, e := range entries {
		line := e.Raw
		if line == "" {
			line = strings.TrimSpace("- " + e.Time + " " + e.By + " " + e.Text)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// canonicalRef normalises a ref string; an unparsable one is kept as written.
func canonicalRef(s string) string {
	r, err := authored.ParseRef(s)
	if err != nil {
		return strings.TrimSpace(s)
	}
	return r.Canonical()
}

func firstLine(s string, max int) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > max {
				cut := max
				for cut > 0 && line[cut]&0xC0 == 0x80 {
					cut--
				}
				line = line[:cut]
			}
			return line
		}
	}
	return ""
}

// autolink refreshes the auto task_links of a project (04-data-model.md section 3) with
// internal/autolink: its own tasks' unqualified auto links, and the cross-project links it
// makes in other projects' tasks ("<project>:commit:<id>"). Manual links and auto links
// written by other projects' runs are never touched. Nothing is written when the result
// did not change since the last run.
//
// A panic in the link rules is caught here and logged once per hour for the project; the
// project is skipped and every other project still gets its links (I5).
func (i *Indexer) autolink(ctx context.Context, id string) {
	if i.c.NoAutoLink {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			i.logOnce(id, "autolink", fmt.Errorf("panic (recovered): %v", r))
			i.log.Debug("panic stack", "stack", string(debug.Stack()))
		}
	}()
	ps := i.state(id)
	if ps == nil {
		return
	}
	in, err := i.autolinkInput(ctx, ps)
	if err != nil {
		i.logOnce(id, "autolink", err)
		return
	}
	links := i.linkFn(in)

	var own, cross []store.IdxAutoLink
	var b strings.Builder
	for _, l := range links {
		row := store.IdxAutoLink{ProjectID: l.ProjectID, TaskID: l.TaskID, Ref: l.Ref, Reason: l.Reason}
		if l.Cross() {
			cross = append(cross, row)
		} else if l.ProjectID == id {
			own = append(own, row)
		} else {
			continue
		}
		b.WriteString(l.ProjectID + "|" + l.TaskID + "|" + l.Ref + "|" + l.Reason + "\n")
	}
	sig := b.String()
	i.mu.Lock()
	prev, seen := i.links[id]
	i.mu.Unlock()
	if seen && prev == sig {
		return
	}
	if err := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
		return store.IdxReplaceAutoLinksTx(ctx, tx, id, own, cross)
	}); err != nil {
		i.logOnce(id, "autolink", err)
		return
	}
	i.mu.Lock()
	i.links[id] = sig
	i.mu.Unlock()

	// Tell the pages which tasks may have new or lost links.
	changed := map[string]bool{}
	for _, l := range append(own, cross...) {
		changed[l.ProjectID+"\x00"+l.TaskID] = true
	}
	for _, t := range in.Tasks {
		changed[id+"\x00"+t.ID] = true
	}
	keys := make([]string, 0, len(changed))
	for k := range changed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p, tid, _ := strings.Cut(k, "\x00")
		i.publish(live.TrackerEvent(p, authored.KindTask, tid))
	}
}

// autolinkInput builds the autolink input of one project from the task files and the store.
// Branches and session shell commands are not filled (optional rules, see the report).
func (i *Indexer) autolinkInput(ctx context.Context, ps *projState) (autolink.Input, error) {
	id := ps.p.ID
	in := autolink.Input{ProjectID: id}
	tr, _ := authored.LoadProject(ps.p.Path)
	for _, t := range tr.Tasks {
		in.Tasks = append(in.Tasks, autolink.TaskInfo{ID: t.ID, Title: t.Title, Status: t.Status,
			Manual: t.Links, Unlinks: t.Unlinks})
	}
	for _, s := range tr.Snapshots {
		in.Snapshots = append(in.Snapshots, autolink.SnapshotInfo{TaskID: s.Task, Version: s.Version, Commit: s.Commit})
	}
	commits, err := i.c.DB.IdxCommitsWithFiles(ctx, id, 500)
	if err != nil {
		return in, err
	}
	in.Commits = commits
	sessions, err := i.c.DB.IdxSessionsForProject(ctx, id)
	if err != nil {
		return in, err
	}
	i.mu.Lock()
	for _, s := range sessions {
		si := autolink.SessionInfo{ID: s.ID, ProjectID: id, Start: s.Start, End: s.End, Title: s.Title, Edited: s.Edited}
		if m := i.sessMem[s.JSONLPath]; m != nil && m.text != "" {
			si.UserTexts = strings.Split(m.text, "\n")
		}
		in.Sessions = append(in.Sessions, si)
	}
	i.mu.Unlock()
	others, err := i.c.DB.IdxTasksByProject(ctx, id)
	if err != nil {
		return in, err
	}
	pids := make([]string, 0, len(others))
	for p := range others {
		pids = append(pids, p)
	}
	sort.Strings(pids)
	for _, p := range pids {
		pt := autolink.ProjectTasks{ProjectID: p}
		for _, t := range others[p] {
			pt.Tasks = append(pt.Tasks, autolink.TaskInfo{ID: t.ID, Title: t.Title, Status: t.Status, Manual: t.Manual})
		}
		in.Others = append(in.Others, pt)
	}
	return in, nil
}
