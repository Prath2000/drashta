package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The authored tables mirror the markdown files in <project>/.drashta/ (04 section 2).
// The files are the source of truth. These rows are a cache, rebuilt from the files.
// Time fields are kept as the text from the front matter, for example 2026-09-30T12:02:00+05:30.

// Milestone is a row of milestones.
type Milestone struct {
	ProjectID string
	ID        string
	Title     string
	Due       string
	State     string // open | closed
	Body      string
	FileRev   string
}

// Feature is a row of features.
type Feature struct {
	ProjectID   string
	ID          string
	Title       string
	MilestoneID string
	Body        string
	FileRev     string
}

// Task is a row of tasks.
type Task struct {
	ProjectID string
	ID        string
	Title     string
	Status    string // backlog | todo | in_progress | review | done
	FeatureID string
	Assignee  string
	Created   string
	CreatedBy string
	Updated   string
	Body      string
	Log       string
	FileRev   string
}

// TaskLink is a row of task_links. Source is "manual" or "auto".
type TaskLink struct {
	Ref    string // canonical ref string, for example "commit:abc1234"
	Source string // manual | auto
	Reason string // for auto links: why, for example "commit message"
}

// Snapshot is a row of snapshots.
type Snapshot struct {
	ProjectID  string
	TaskID     string
	Version    int
	Title      string
	ImagePath  string
	Source     string
	Captured   string
	Author     string
	CommitID   string
	Verdict    string // pending | approved | changes
	ReviewedBy string
	ReviewedAt string
	ReviewNote string
	FileRev    string
}

// Note is a row of notes. X, Y and Line are nil when the note has no pin.
type Note struct {
	ProjectID  string
	ID         string
	Ref        string
	X, Y       *float64
	Line       *int
	Anchor     string
	Author     string
	Created    string
	Resolved   bool
	ResolvedAt string
	ReplyTo    string
	Body       string
	FileRev    string
}

// UpsertAuthoredMilestone inserts or updates a milestone. Its search row follows.
func (d *DB) UpsertAuthoredMilestone(ctx context.Context, m Milestone) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return UpsertAuthoredMilestoneTx(ctx, tx, m) })
}

// UpsertAuthoredMilestoneTx is UpsertAuthoredMilestone inside a transaction you already have.
func UpsertAuthoredMilestoneTx(ctx context.Context, tx *sql.Tx, m Milestone) error {
	state := m.State
	if state == "" {
		state = "open"
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO milestones(project_id, id, title, due, state, body, file_rev) VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, id) DO UPDATE SET
  title = excluded.title, due = excluded.due, state = excluded.state, body = excluded.body,
  file_rev = excluded.file_rev`,
		m.ProjectID, m.ID, m.Title, m.Due, state, m.Body, m.FileRev); err != nil {
		return err
	}
	return FTSReplaceTx(ctx, tx, "milestone", "milestone:"+m.ID, m.ProjectID, m.Title, m.Body, parseTime(m.Due))
}

// UpsertAuthoredFeature inserts or updates a feature. Its search row follows.
func (d *DB) UpsertAuthoredFeature(ctx context.Context, ft Feature) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return UpsertAuthoredFeatureTx(ctx, tx, ft) })
}

// UpsertAuthoredFeatureTx is UpsertAuthoredFeature inside a transaction you already have.
func UpsertAuthoredFeatureTx(ctx context.Context, tx *sql.Tx, ft Feature) error {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO features(project_id, id, title, milestone_id, body, file_rev) VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, id) DO UPDATE SET
  title = excluded.title, milestone_id = excluded.milestone_id, body = excluded.body,
  file_rev = excluded.file_rev`,
		ft.ProjectID, ft.ID, ft.Title, ft.MilestoneID, ft.Body, ft.FileRev); err != nil {
		return err
	}
	return FTSReplaceTx(ctx, tx, "feature", "feature:"+ft.ID, ft.ProjectID, ft.Title, ft.Body, time.Time{})
}

// UpsertAuthoredTask inserts or updates a task. Its search row follows.
func (d *DB) UpsertAuthoredTask(ctx context.Context, t Task) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return UpsertAuthoredTaskTx(ctx, tx, t) })
}

// UpsertAuthoredTaskTx is UpsertAuthoredTask inside a transaction you already have.
func UpsertAuthoredTaskTx(ctx context.Context, tx *sql.Tx, t Task) error {
	status := t.Status
	if status == "" {
		status = "backlog"
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO tasks(project_id, id, title, status, feature_id, assignee, created, created_by, updated,
                  body, log, file_rev)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, id) DO UPDATE SET
  title = excluded.title, status = excluded.status, feature_id = excluded.feature_id,
  assignee = excluded.assignee, created = excluded.created, created_by = excluded.created_by,
  updated = excluded.updated, body = excluded.body, log = excluded.log, file_rev = excluded.file_rev`,
		t.ProjectID, t.ID, t.Title, status, t.FeatureID, t.Assignee, t.Created, t.CreatedBy, t.Updated,
		t.Body, t.Log, t.FileRev); err != nil {
		return err
	}
	ts := parseTime(t.Updated)
	if ts.IsZero() {
		ts = parseTime(t.Created)
	}
	return FTSReplaceTx(ctx, tx, "task", "task:"+t.ID, t.ProjectID, t.Title, t.Body, ts)
}

// UpsertAuthoredTaskLinks makes the links of one task and one source equal to links.
// Links with another source are not touched. If a manual and an auto link have the same ref,
// the manual one wins. Each link's Source is set to source.
func (d *DB) UpsertAuthoredTaskLinks(ctx context.Context, project, taskID, source string, links []TaskLink) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		return UpsertAuthoredTaskLinksTx(ctx, tx, project, taskID, source, links)
	})
}

// UpsertAuthoredTaskLinksTx is UpsertAuthoredTaskLinks inside a transaction you already have.
func UpsertAuthoredTaskLinksTx(ctx context.Context, tx *sql.Tx, project, taskID, source string, links []TaskLink) error {
	if source != "manual" && source != "auto" {
		return fmt.Errorf("store: task link source must be manual or auto, got %q", source)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_links WHERE project_id = ? AND task_id = ? AND source = ?`, project, taskID, source); err != nil {
		return err
	}
	verb := "INSERT OR REPLACE" // manual replaces an auto link with the same ref
	if source == "auto" {
		verb = "INSERT OR IGNORE" // auto never replaces a manual link
	}
	ins, err := tx.PrepareContext(ctx,
		verb+` INTO task_links(project_id, task_id, ref, source, reason) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, l := range links {
		if _, err := ins.ExecContext(ctx, project, taskID, l.Ref, source, l.Reason); err != nil {
			return err
		}
	}
	return nil
}

// UpsertAuthoredSnapshot inserts or updates a snapshot version. Snapshots are not in the search index.
func (d *DB) UpsertAuthoredSnapshot(ctx context.Context, s Snapshot) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return UpsertAuthoredSnapshotTx(ctx, tx, s) })
}

// UpsertAuthoredSnapshotTx is UpsertAuthoredSnapshot inside a transaction you already have.
func UpsertAuthoredSnapshotTx(ctx context.Context, tx *sql.Tx, s Snapshot) error {
	verdict := s.Verdict
	if verdict == "" {
		verdict = "pending"
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO snapshots(project_id, task_id, version, title, image_path, source, captured, author,
                      commit_id, verdict, reviewed_by, reviewed_at, review_note, file_rev)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, task_id, version) DO UPDATE SET
  title = excluded.title, image_path = excluded.image_path, source = excluded.source,
  captured = excluded.captured, author = excluded.author, commit_id = excluded.commit_id,
  verdict = excluded.verdict, reviewed_by = excluded.reviewed_by, reviewed_at = excluded.reviewed_at,
  review_note = excluded.review_note, file_rev = excluded.file_rev`,
		s.ProjectID, s.TaskID, s.Version, s.Title, s.ImagePath, s.Source, s.Captured, s.Author,
		s.CommitID, verdict, s.ReviewedBy, s.ReviewedAt, s.ReviewNote, s.FileRev)
	return err
}

// UpsertAuthoredNote inserts or updates a note. Its search row follows.
func (d *DB) UpsertAuthoredNote(ctx context.Context, n Note) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return UpsertAuthoredNoteTx(ctx, tx, n) })
}

// UpsertAuthoredNoteTx is UpsertAuthoredNote inside a transaction you already have.
func UpsertAuthoredNoteTx(ctx context.Context, tx *sql.Tx, n Note) error {
	var x, y, line any
	if n.X != nil {
		x = *n.X
	}
	if n.Y != nil {
		y = *n.Y
	}
	if n.Line != nil {
		line = *n.Line
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO notes(project_id, id, ref, x, y, line, anchor, author, created, resolved, resolved_at,
                  reply_to, body, file_rev)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, id) DO UPDATE SET
  ref = excluded.ref, x = excluded.x, y = excluded.y, line = excluded.line, anchor = excluded.anchor,
  author = excluded.author, created = excluded.created, resolved = excluded.resolved,
  resolved_at = excluded.resolved_at, reply_to = excluded.reply_to, body = excluded.body,
  file_rev = excluded.file_rev`,
		n.ProjectID, n.ID, n.Ref, x, y, line, n.Anchor, n.Author, n.Created, b2i(n.Resolved),
		n.ResolvedAt, n.ReplyTo, n.Body, n.FileRev); err != nil {
		return err
	}
	return FTSReplaceTx(ctx, tx, "note", "note:"+n.ID, n.ProjectID, firstLine(n.Body, 80), n.Body, parseTime(n.Created))
}

// DeleteAuthored removes one authored row (use it when the file is gone or in .trash/).
// kind is milestone, feature, task, snapshot or note. For a snapshot, id is "T-12/v3".
// Deleting a task also deletes its task_links. Search rows are removed too.
func (d *DB) DeleteAuthored(ctx context.Context, project, kind, id string) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return DeleteAuthoredTx(ctx, tx, project, kind, id) })
}

// DeleteAuthoredTx is DeleteAuthored inside a transaction you already have.
func DeleteAuthoredTx(ctx context.Context, tx *sql.Tx, project, kind, id string) error {
	switch kind {
	case "milestone", "feature", "note":
		table := map[string]string{"milestone": "milestones", "feature": "features", "note": "notes"}[kind]
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE project_id = ? AND id = ?", project, id); err != nil {
			return err
		}
		return FTSDeleteTx(ctx, tx, kind, kind+":"+id, project)
	case "task":
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE project_id = ? AND id = ?`, project, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_links WHERE project_id = ? AND task_id = ?`, project, id); err != nil {
			return err
		}
		return FTSDeleteTx(ctx, tx, "task", "task:"+id, project)
	case "snapshot":
		task, ver, ok := strings.Cut(id, "/v")
		n, err := strconv.Atoi(ver)
		if !ok || err != nil {
			return fmt.Errorf("store: snapshot id must look like T-12/v3, got %q", id)
		}
		_, err = tx.ExecContext(ctx,
			`DELETE FROM snapshots WHERE project_id = ? AND task_id = ? AND version = ?`, project, task, n)
		return err
	default:
		return fmt.Errorf("store: unknown authored kind %q", kind)
	}
}

// firstLine returns the first non-empty line of s, cut to max bytes.
func firstLine(s string, max int) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return clip(line, max)
		}
	}
	return ""
}
