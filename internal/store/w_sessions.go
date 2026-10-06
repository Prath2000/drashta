package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Session is one Claude Code session (one .jsonl file).
type Session struct {
	ID         string
	Profile    string
	JSONLPath  string
	CWD        string
	ProjectID  string // "" = unassigned
	Start, End time.Time
	Title      string
	Turns      int
	KB         int
	Tools      map[string]int // tool name -> count
	Edited     []string       // edited file paths
	JSONLSize  int64
	JSONLMTime time.Time
	// Text is for search only. Give already-redacted user text (not tool output).
	// It is not saved in the sessions table. It is cut to MaxBody bytes.
	Text string
}

// SessionProject says that a session belongs to, or touches, a project.
// Reason is "cwd", "mention" or "edit".
type SessionProject struct {
	ProjectID string
	Reason    string
}

// UpsertSessions inserts or updates sessions. Search rows (kind "session") change with them.
func (d *DB) UpsertSessions(ctx context.Context, ss []Session) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return UpsertSessionsTx(ctx, tx, ss) })
}

// UpsertSessionsTx is UpsertSessions inside a transaction you already have.
func UpsertSessionsTx(ctx context.Context, tx *sql.Tx, ss []Session) error {
	f, err := newFTSWriter(ctx, tx)
	if err != nil {
		return err
	}
	defer f.close()

	prevProject, err := tx.PrepareContext(ctx, `SELECT project_id FROM sessions WHERE id = ?`)
	if err != nil {
		return err
	}
	defer prevProject.Close()
	upsert, err := tx.PrepareContext(ctx, `
INSERT INTO sessions(id, profile, jsonl_path, cwd, project_id, "start", "end", title, turns, kb,
                     tools, edited, jsonl_size, jsonl_mtime)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  profile = excluded.profile, jsonl_path = excluded.jsonl_path, cwd = excluded.cwd,
  project_id = excluded.project_id, "start" = excluded."start", "end" = excluded."end",
  title = excluded.title, turns = excluded.turns, kb = excluded.kb, tools = excluded.tools,
  edited = excluded.edited, jsonl_size = excluded.jsonl_size, jsonl_mtime = excluded.jsonl_mtime`)
	if err != nil {
		return err
	}
	defer upsert.Close()

	for _, s := range ss {
		// A session can move to another project. Then its old search row must go.
		var old string
		err := prevProject.QueryRowContext(ctx, s.ID).Scan(&old)
		switch {
		case err == nil && old != s.ProjectID:
			if err := f.del("session", old, "session:"+s.ID); err != nil {
				return err
			}
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return err
		}

		if _, err := upsert.ExecContext(ctx, s.ID, s.Profile, s.JSONLPath, s.CWD, s.ProjectID,
			unixOrNil(s.Start), unixOrNil(s.End), s.Title, s.Turns, s.KB,
			jsonText(s.Tools, "{}"), jsonText(s.Edited, "[]"), s.JSONLSize, unixOrZero(s.JSONLMTime)); err != nil {
			return err
		}
		ts := s.End
		if ts.IsZero() {
			ts = s.Start
		}
		if err := f.put("session", s.ProjectID, "session:"+s.ID, s.Title, s.Text, unixOrZero(ts)); err != nil {
			return err
		}
	}
	return nil
}

// SetSessionProjects makes the session_projects rows of one session equal to links.
func (d *DB) SetSessionProjects(ctx context.Context, sessionID string, links []SessionProject) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return SetSessionProjectsTx(ctx, tx, sessionID, links) })
}

// SetSessionProjectsTx is SetSessionProjects inside a transaction you already have.
func SetSessionProjectsTx(ctx context.Context, tx *sql.Tx, sessionID string, links []SessionProject) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_projects WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO session_projects(session_id, project_id, reason) VALUES(?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, l := range links {
		reason := l.Reason
		if reason == "" {
			reason = "cwd"
		}
		if _, err := ins.ExecContext(ctx, sessionID, l.ProjectID, reason); err != nil {
			return err
		}
	}
	return nil
}

// DeleteSession removes a session, its project links and its search row.
// Use it when the .jsonl file is gone. (The claude_file row is separate: see DeleteClaudeFile.)
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		var project string
		err := tx.QueryRowContext(ctx, `SELECT project_id FROM sessions WHERE id = ?`, id).Scan(&project)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := FTSDeleteTx(ctx, tx, "session", "session:"+id, project); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM session_projects WHERE session_id = ?`, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
		return err
	})
}
