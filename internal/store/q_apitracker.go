package store

// Queries for the tracker API and the ref resolver (agent b-api-tracker).
// Every exported name here starts with "Trk" so it never clashes with the
// q_*.go files of the other API agents.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Prath2000/drashta/internal/probe"
)

// TrkLink is one row of task_links, with the project of the task that owns it.
type TrkLink struct {
	ProjectID string // project of the task
	TaskID    string
	Ref       string // as stored: local ("commit:abc") or with a project ("ledger:commit:abc")
	Source    string // manual | auto
	Reason    string
}

// TrkAutoLinks returns the auto links of every task of one project, keyed by task id.
func (d *DB) TrkAutoLinks(ctx context.Context, project string) (map[string][]TaskLink, error) {
	rows, err := d.r.QueryContext(ctx, `
SELECT task_id, ref, source, reason FROM task_links
WHERE project_id = ? AND source = 'auto' ORDER BY task_id, ref`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]TaskLink{}
	for rows.Next() {
		var task string
		var l TaskLink
		if err := rows.Scan(&task, &l.Ref, &l.Source, &l.Reason); err != nil {
			return nil, err
		}
		out[task] = append(out[task], l)
	}
	return out, rows.Err()
}

// TrkLinksByType returns task_links rows that may point at an item of type typ in project:
// local refs of that type inside project, and "<project>:<typ>:..." refs from any project.
// The caller compares ids itself (commit ids can differ in length).
func (d *DB) TrkLinksByType(ctx context.Context, project, typ string) ([]TrkLink, error) {
	local := trkLikeEsc(typ) + ":%"
	remote := trkLikeEsc(project+":"+typ) + ":%"
	rows, err := d.r.QueryContext(ctx, `
SELECT project_id, task_id, ref, source, reason FROM task_links
WHERE (project_id = ? AND ref LIKE ? ESCAPE '\') OR ref LIKE ? ESCAPE '\'
ORDER BY project_id, task_id`, project, local, remote)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrkLink{}
	for rows.Next() {
		var l TrkLink
		if err := rows.Scan(&l.ProjectID, &l.TaskID, &l.Ref, &l.Source, &l.Reason); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// TrkCommit finds a commit of project whose id starts with prefix (lower case hex).
// When several match, the newest wins. Files come from commit_files.
func (d *DB) TrkCommit(ctx context.Context, project, prefix string) (probe.Commit, bool, error) {
	var c probe.Commit
	var ts sql.NullInt64
	err := d.r.QueryRowContext(ctx, `
SELECT id, vcs, author, ts, msg, body, plus, minus, nfiles, branch FROM commits
WHERE project_id = ? AND id LIKE ? ESCAPE '\' ORDER BY ts DESC LIMIT 1`,
		project, trkLikeEsc(strings.ToLower(prefix))+"%").
		Scan(&c.ID, &c.VCS, &c.Author, &ts, &c.Msg, &c.Body, &c.Plus, &c.Minus, &c.NFiles, &c.Branch)
	if errors.Is(err, sql.ErrNoRows) {
		return probe.Commit{}, false, nil
	}
	if err != nil {
		return probe.Commit{}, false, err
	}
	c.Time = fromUnix(ts)
	rows, err := d.r.QueryContext(ctx, `
SELECT path, plus, minus FROM commit_files WHERE project_id = ? AND commit_id = ? ORDER BY path`, project, c.ID)
	if err != nil {
		return probe.Commit{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var f probe.CommitFile
		if err := rows.Scan(&f.Path, &f.Plus, &f.Minus); err != nil {
			return probe.Commit{}, false, err
		}
		c.Files = append(c.Files, f)
	}
	return c, true, rows.Err()
}

// TrkSession finds a session whose id starts with prefix (a full UUID or its first 8 characters).
func (d *DB) TrkSession(ctx context.Context, prefix string) (Session, bool, error) {
	var s Session
	var start, end sql.NullInt64
	var tools, edited string
	var mtime int64
	err := d.r.QueryRowContext(ctx, `
SELECT id, profile, jsonl_path, cwd, project_id, "start", "end", title, turns, kb, tools, edited,
       jsonl_size, jsonl_mtime
FROM sessions WHERE id LIKE ? ESCAPE '\' ORDER BY "end" DESC LIMIT 1`,
		trkLikeEsc(strings.ToLower(prefix))+"%").
		Scan(&s.ID, &s.Profile, &s.JSONLPath, &s.CWD, &s.ProjectID, &start, &end, &s.Title, &s.Turns, &s.KB,
			&tools, &edited, &s.JSONLSize, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	s.Start, s.End = fromUnix(start), fromUnix(end)
	s.Tools = map[string]int{}
	_ = json.Unmarshal([]byte(tools), &s.Tools)
	s.Edited = []string{}
	_ = json.Unmarshal([]byte(edited), &s.Edited)
	if s.Edited == nil {
		s.Edited = []string{}
	}
	return s, true, nil
}

// TrkFile returns the files row of one path, if the index has it.
func (d *DB) TrkFile(ctx context.Context, project, path string) (File, bool, error) {
	var f File
	var mtime sql.NullInt64
	err := d.r.QueryRowContext(ctx, `
SELECT path, size, mtime, kind, lines, title FROM files WHERE project_id = ? AND path = ?`, project, path).
		Scan(&f.Path, &f.Size, &mtime, &f.Kind, &f.Lines, &f.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	f.MTime = fromUnix(mtime)
	return f, true, nil
}

// TrkServersNow returns every row of servers_now (the live servers), lowest port first.
func (d *DB) TrkServersNow(ctx context.Context) ([]ServerNow, error) {
	rows, err := d.r.QueryContext(ctx, `
SELECT key, port, pid, name, cmd, cwd, project_id, kind, title, first_seen, last_seen
FROM servers_now ORDER BY port, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerNow{}
	for rows.Next() {
		var s ServerNow
		var first, last sql.NullInt64
		if err := rows.Scan(&s.Key, &s.Port, &s.PID, &s.Name, &s.Cmd, &s.CWD, &s.ProjectID, &s.Kind, &s.Title,
			&first, &last); err != nil {
			return nil, err
		}
		s.FirstSeen, s.LastSeen = fromUnix(first), fromUnix(last)
		out = append(out, s)
	}
	return out, rows.Err()
}

// trkLikeEsc escapes the LIKE wildcards % and _ (escape character \).
func trkLikeEsc(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
