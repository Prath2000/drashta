package store

// Queries and small write helpers used by the indexer (internal/indexer).
// All names start with "Idx" so they never clash with the API agents' q_*.go files.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// IdxCommitTimes returns id -> commit time (unix seconds) for every stored commit of a project.
func (d *DB) IdxCommitTimes(ctx context.Context, project string) (map[string]int64, error) {
	rows, err := d.r.QueryContext(ctx, `SELECT id, ts FROM commits WHERE project_id = ?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var ts int64
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, err
		}
		out[id] = ts
	}
	return out, rows.Err()
}

// IdxCommits returns the newest stored commits of a project (no files), newest first.
// The indexer feeds them to auto-linking.
func (d *DB) IdxCommits(ctx context.Context, project string, limit int) ([]probe.Commit, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := d.r.QueryContext(ctx, `
SELECT id, vcs, author, ts, msg, body, plus, minus, nfiles, branch
FROM commits WHERE project_id = ? ORDER BY ts DESC, id LIMIT ?`, project, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []probe.Commit{}
	for rows.Next() {
		var c probe.Commit
		var ts int64
		if err := rows.Scan(&c.ID, &c.VCS, &c.Author, &ts, &c.Msg, &c.Body, &c.Plus, &c.Minus, &c.NFiles, &c.Branch); err != nil {
			return nil, err
		}
		c.Time = fromUnix(sql.NullInt64{Int64: ts, Valid: true})
		out = append(out, c)
	}
	return out, rows.Err()
}

// IdxDeleteEventsTx removes the events of one (project, kind, ref). An empty kind matches every kind.
func IdxDeleteEventsTx(ctx context.Context, tx *sql.Tx, project, kind, ref string) error {
	if kind == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM events WHERE project_id = ? AND ref = ?`, project, ref)
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM events WHERE project_id = ? AND kind = ? AND ref = ?`, project, kind, ref)
	return err
}

// IdxSession reads one stored session row, with the projects it mentions (reason "mention").
// ok is false when there is no row.
func (d *DB) IdxSession(ctx context.Context, id string) (s Session, mentions []string, ok bool, err error) {
	var start, end sql.NullInt64
	var tools, edited string
	var mtime int64
	err = d.r.QueryRowContext(ctx, `
SELECT id, profile, jsonl_path, cwd, project_id, "start", "end", title, turns, kb, tools, edited, jsonl_size, jsonl_mtime
FROM sessions WHERE id = ?`, id).Scan(&s.ID, &s.Profile, &s.JSONLPath, &s.CWD, &s.ProjectID, &start, &end,
		&s.Title, &s.Turns, &s.KB, &tools, &edited, &s.JSONLSize, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, nil, false, nil
	}
	if err != nil {
		return Session{}, nil, false, err
	}
	s.Start, s.End = fromUnix(start), fromUnix(end)
	s.JSONLMTime = fromUnix(sql.NullInt64{Int64: mtime, Valid: true})
	_ = json.Unmarshal([]byte(tools), &s.Tools)
	_ = json.Unmarshal([]byte(edited), &s.Edited)
	rows, err := d.r.QueryContext(ctx,
		`SELECT project_id FROM session_projects WHERE session_id = ? AND reason <> 'cwd' ORDER BY project_id`, id)
	if err != nil {
		return s, nil, true, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return s, nil, true, err
		}
		mentions = append(mentions, p)
	}
	return s, mentions, true, rows.Err()
}

// IdxSessionPaths returns session id -> jsonl_path for every stored session.
func (d *DB) IdxSessionPaths(ctx context.Context) (map[string]string, error) {
	rows, err := d.r.QueryContext(ctx, `SELECT id, jsonl_path FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, p string
		if err := rows.Scan(&id, &p); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}

// IdxSessionsForProject returns the sessions that belong to a project (by project_id), newest first.
// Text is empty. Used for auto-linking.
func (d *DB) IdxSessionsForProject(ctx context.Context, project string) ([]Session, error) {
	rows, err := d.r.QueryContext(ctx, `
SELECT id, profile, jsonl_path, cwd, "start", "end", title, turns, edited
FROM sessions WHERE project_id = ? ORDER BY "end" DESC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var s Session
		var start, end sql.NullInt64
		var edited string
		if err := rows.Scan(&s.ID, &s.Profile, &s.JSONLPath, &s.CWD, &start, &end, &s.Title, &s.Turns, &edited); err != nil {
			return nil, err
		}
		s.ProjectID = project
		s.Start, s.End = fromUnix(start), fromUnix(end)
		_ = json.Unmarshal([]byte(edited), &s.Edited)
		out = append(out, s)
	}
	return out, rows.Err()
}

// IdxDeleteSessionTx removes a session row, its project links, its search row and its events.
func IdxDeleteSessionTx(ctx context.Context, tx *sql.Tx, id string) error {
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
	for _, q := range []string{
		`DELETE FROM session_projects WHERE session_id = ?`,
		`DELETE FROM sessions WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM events WHERE ref = ? AND kind IN ('session_start','session_end')`, "session:"+id)
	return err
}

// IdxClaudeFiles returns every stored claude_file row by path.
func (d *DB) IdxClaudeFiles(ctx context.Context) (map[string]ClaudeFile, error) {
	rows, err := d.r.QueryContext(ctx, `SELECT path, size, mtime, "offset", inode FROM claude_file`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ClaudeFile{}
	for rows.Next() {
		var cf ClaudeFile
		var mtime int64
		var inode int64
		if err := rows.Scan(&cf.Path, &cf.Size, &mtime, &cf.Offset, &inode); err != nil {
			return nil, err
		}
		if mtime != 0 {
			cf.MTime = time.Unix(0, mtime)
		}
		cf.Inode = uint64(inode)
		out[cf.Path] = cf
	}
	return out, rows.Err()
}

// IdxDeleteClaudeFileTx removes one claude_file row.
func IdxDeleteClaudeFileTx(ctx context.Context, tx *sql.Tx, path string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM claude_file WHERE path = ?`, path)
	return err
}

// IdxAuthoredIDs returns the stored authored ids of a project by kind
// (milestone, feature, task, snapshot "T-1/v3", note).
func (d *DB) IdxAuthoredIDs(ctx context.Context, project string) (map[string][]string, error) {
	out := map[string][]string{}
	qs := map[string]string{
		"milestone": `SELECT id FROM milestones WHERE project_id = ?`,
		"feature":   `SELECT id FROM features WHERE project_id = ?`,
		"task":      `SELECT id FROM tasks WHERE project_id = ?`,
		"note":      `SELECT id FROM notes WHERE project_id = ?`,
		"snapshot":  `SELECT task_id || '/v' || version FROM snapshots WHERE project_id = ?`,
	}
	for kind, q := range qs {
		rows, err := d.r.QueryContext(ctx, q, project)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[kind] = append(out[kind], id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// IdxTaskStatus returns the stored status of one task, or "" when there is no row.
func (d *DB) IdxTaskStatus(ctx context.Context, project, id string) (string, error) {
	var s string
	err := d.r.QueryRowContext(ctx, `SELECT status FROM tasks WHERE project_id = ? AND id = ?`, project, id).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return s, err
}

// IdxCounts are the numbers of the hello and counts events.
type IdxCounts struct {
	LiveServers    int
	OpenTasks      int
	AwaitingReview int
	OpenNotes      int
}

// IdxCounts returns the four top-bar numbers. It uses HomeBootCounts, so the SSE "counts"
// event and GET /api/boot always agree: live dev servers, tasks not done, tasks whose newest
// snapshot verdict is pending (awaiting review), unresolved notes.
func (d *DB) IdxCounts(ctx context.Context) (IdxCounts, error) {
	c, err := d.HomeBootCounts(ctx)
	if err != nil {
		return IdxCounts{}, err
	}
	return IdxCounts{LiveServers: c.LiveServers, OpenTasks: c.OpenTasks, AwaitingReview: c.AwaitingReview,
		OpenNotes: c.OpenNotes}, nil
}

// IdxOpenServerRunsTx returns the runs that have no end time, by key (the newest one per key).
func IdxOpenServerRunsTx(ctx context.Context, tx *sql.Tx) (map[string]ServerRun, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id, key, project_id, port, pid, name, cmd, cwd, kind, started, last_seen
FROM server_runs WHERE ended IS NULL ORDER BY started, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ServerRun{}
	for rows.Next() {
		var r ServerRun
		var started int64
		var last sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Key, &r.ProjectID, &r.Port, &r.PID, &r.Name, &r.Cmd, &r.CWD, &r.Kind, &started, &last); err != nil {
			return nil, err
		}
		r.Started = fromUnix(sql.NullInt64{Int64: started, Valid: true})
		r.LastSeen = fromUnix(last)
		out[r.Key] = r
	}
	return out, rows.Err()
}

// IdxAppendServerRunTx adds a run inside your transaction and returns its id.
func IdxAppendServerRunTx(ctx context.Context, tx *sql.Tx, r ServerRun) (int64, error) {
	return insertServerRun(ctx, tx, r, false)
}

// IdxUpdateServerRunTx updates the live fields of an open run (port, pid, name, project, last_seen).
func IdxUpdateServerRunTx(ctx context.Context, tx *sql.Tx, r ServerRun) error {
	_, err := tx.ExecContext(ctx, `
UPDATE server_runs SET port = ?, pid = ?, name = ?, project_id = ?, kind = ?, last_seen = ?
WHERE id = ? AND ended IS NULL`, r.Port, r.PID, r.Name, r.ProjectID, idxNonEmpty(r.Kind, "dev"), unixOrNil(r.LastSeen), r.ID)
	return err
}

// IdxEndServerRunTx ends an open run. last_seen is set to ended when it is empty.
func IdxEndServerRunTx(ctx context.Context, tx *sql.Tx, id int64, ended time.Time) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE server_runs SET ended = ?, last_seen = COALESCE(last_seen, ?) WHERE id = ? AND ended IS NULL`,
		ended.Unix(), ended.Unix(), id)
	return err
}

// IdxTouchServerRuns sets last_seen on every open run. Cheap; called after each good lsof pass.
func (d *DB) IdxTouchServerRuns(ctx context.Context, seen time.Time) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE server_runs SET last_seen = ? WHERE ended IS NULL`, seen.Unix())
		return err
	})
}

// IdxLatestServerRuns returns the newest run of every key (open runs win), oldest key first.
// The servers.History is loaded from this list.
func (d *DB) IdxLatestServerRuns(ctx context.Context) ([]ServerRun, error) {
	all, err := d.ListServerRuns(ctx)
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	var out []ServerRun
	for _, r := range all { // oldest first, so a later row replaces an earlier one
		if i, ok := idx[r.Key]; ok {
			if out[i].Ended.IsZero() && !r.Ended.IsZero() {
				continue // keep the open run
			}
			out[i] = r
			continue
		}
		idx[r.Key] = len(out)
		out = append(out, r)
	}
	return out, nil
}

// RebuildHistoryEvents writes the events that come from history tables:
// server_up and server_down from server_runs, and one file_burst per project and day
// from file_changes (ref "files:<day>", ts = start of the day, meta {"files": n}).
// It is safe to run many times. Use it after a Reindex, which drops the events table.
func (d *DB) RebuildHistoryEvents(ctx context.Context, loc *time.Location) error {
	if loc == nil {
		loc = time.Local
	}
	runs, err := d.ListServerRuns(ctx)
	if err != nil {
		return err
	}
	type burst struct {
		project, day string
		n            int
	}
	var bursts []burst
	rows, err := d.r.QueryContext(ctx,
		`SELECT project_id, day, COUNT(DISTINCT path) FROM file_changes GROUP BY project_id, day`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b burst
		if err := rows.Scan(&b.project, &b.day, &b.n); err != nil {
			rows.Close()
			return err
		}
		bursts = append(bursts, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return d.Write(ctx, func(tx *sql.Tx) error {
		for _, r := range runs {
			if r.ProjectID == "" {
				continue
			}
			for _, e := range ServerRunEvents(r, loc) {
				if _, err := AddEventTx(ctx, tx, e); err != nil {
					return err
				}
			}
		}
		for _, b := range bursts {
			if b.project == "" || b.n == 0 {
				continue
			}
			start, err := time.ParseInLocation(DayLayout, b.day, loc)
			if err != nil {
				continue
			}
			ref := "files:" + b.day
			if err := IdxDeleteEventsTx(ctx, tx, b.project, "file_burst", ref); err != nil {
				return err
			}
			meta, _ := json.Marshal(map[string]int{"files": b.n})
			label := strconv.Itoa(b.n) + " files changed"
			if b.n == 1 {
				label = "1 file changed"
			}
			if _, err := AddEventTx(ctx, tx, Event{ProjectID: b.project, TS: start, Day: b.day,
				Kind: "file_burst", Ref: ref, Label: label, Meta: meta}); err != nil {
				return err
			}
		}
		return nil
	})
}

// IdxDropCommitsTx removes everything that still points at commits that no longer exist
// (after a rebase, amend or force-push): their commits and commit_files rows, their search
// rows and their "commit" events. ReplaceCommits already drops most of them; this also covers
// the ones it keeps on purpose (older than the oldest commit given to it) when the caller
// knows they are gone.
func IdxDropCommitsTx(ctx context.Context, tx *sql.Tx, project string, ids []string) error {
	for _, id := range ids {
		for _, q := range []string{
			`DELETE FROM commits WHERE project_id = ? AND id = ?`,
			`DELETE FROM commit_files WHERE project_id = ? AND commit_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, project, id); err != nil {
				return err
			}
		}
		if err := FTSDeleteTx(ctx, tx, "commit", "commit:"+id, project); err != nil {
			return err
		}
		if err := IdxDeleteEventsTx(ctx, tx, project, "commit", "commit:"+id); err != nil {
			return err
		}
	}
	return nil
}

// ServerRunEvents gives the server_up event of a run, and server_down when it has ended.
func ServerRunEvents(r ServerRun, loc *time.Location) []Event {
	ref := "server:" + strconv.Itoa(r.Port)
	meta, _ := json.Marshal(map[string]any{"key": r.Key, "port": r.Port, "pid": r.PID})
	label := strings.TrimSpace(r.Name + " :" + strconv.Itoa(r.Port))
	out := []Event{}
	if !r.Started.IsZero() {
		out = append(out, Event{ProjectID: r.ProjectID, TS: r.Started, Day: DayOf(r.Started, loc),
			Kind: "server_up", Ref: ref, Label: label, Meta: meta})
	}
	if !r.Ended.IsZero() {
		out = append(out, Event{ProjectID: r.ProjectID, TS: r.Ended, Day: DayOf(r.Ended, loc),
			Kind: "server_down", Ref: ref, Label: label, Meta: meta})
	}
	return out
}

func idxNonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// IdxAutoLink is one auto task link written by the indexer.
// ProjectID owns the task. Ref is "commit:<id>" / "session:<id>" for a link inside one
// project, or "<from project>:commit:<id>" for a cross-project link.
type IdxAutoLink struct {
	ProjectID string
	TaskID    string
	Ref       string
	Reason    string
}

// IdxReplaceAutoLinksTx replaces the auto links that one project's run produced, and only those:
//   - in the project's own tasks: auto rows whose ref is unqualified (starts with "commit:" or
//     "session:") are replaced by own;
//   - in other projects' tasks: auto rows whose ref starts with "<project>:" are replaced by cross.
//
// Auto rows written by another project's run (ref "<other>:...") and all manual rows stay.
// When a manual row has the same ref, the manual row wins (the auto one is not inserted).
func IdxReplaceAutoLinksTx(ctx context.Context, tx *sql.Tx, project string, own, cross []IdxAutoLink) error {
	if _, err := tx.ExecContext(ctx, `
DELETE FROM task_links WHERE project_id = ? AND source = 'auto'
  AND (substr(ref, 1, 7) = 'commit:' OR substr(ref, 1, 8) = 'session:')`, project); err != nil {
		return err
	}
	prefix := project + ":"
	if _, err := tx.ExecContext(ctx, `
DELETE FROM task_links WHERE project_id <> ? AND source = 'auto' AND substr(ref, 1, ?) = ?`,
		project, len(prefix), prefix); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx, `
INSERT INTO task_links(project_id, task_id, ref, source, reason) VALUES(?, ?, ?, 'auto', ?)
ON CONFLICT(project_id, task_id, ref) DO NOTHING`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, l := range own {
		if _, err := ins.ExecContext(ctx, project, l.TaskID, l.Ref, l.Reason); err != nil {
			return err
		}
	}
	for _, l := range cross {
		if l.ProjectID == project || !strings.HasPrefix(l.Ref, prefix) {
			continue // not a cross link of this project: refuse to write it here
		}
		if _, err := ins.ExecContext(ctx, l.ProjectID, l.TaskID, l.Ref, l.Reason); err != nil {
			return err
		}
	}
	return nil
}

// IdxCommitsWithFiles is IdxCommits plus each commit's files (commit_files).
func (d *DB) IdxCommitsWithFiles(ctx context.Context, project string, limit int) ([]probe.Commit, error) {
	commits, err := d.IdxCommits(ctx, project, limit)
	if err != nil || len(commits) == 0 {
		return commits, err
	}
	idx := make(map[string]int, len(commits))
	for n, c := range commits {
		idx[c.ID] = n
	}
	rows, err := d.r.QueryContext(ctx,
		`SELECT commit_id, path, plus, minus FROM commit_files WHERE project_id = ? ORDER BY commit_id, path`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var f probe.CommitFile
		if err := rows.Scan(&id, &f.Path, &f.Plus, &f.Minus); err != nil {
			return nil, err
		}
		if n, ok := idx[id]; ok {
			commits[n].Files = append(commits[n].Files, f)
		}
	}
	return commits, rows.Err()
}

// IdxTask is a task of the mirror with its manual link refs.
type IdxTask struct {
	ID, Title, Status string
	Manual            []string
}

// IdxTasksByProject returns the mirrored tasks of every project except exclude,
// with their manual links. Auto-linking uses it for cross-project mentions.
func (d *DB) IdxTasksByProject(ctx context.Context, exclude string) (map[string][]IdxTask, error) {
	rows, err := d.r.QueryContext(ctx,
		`SELECT project_id, id, title, status FROM tasks WHERE project_id <> ? ORDER BY project_id, id`, exclude)
	if err != nil {
		return nil, err
	}
	out := map[string][]IdxTask{}
	pos := map[string]int{}
	for rows.Next() {
		var p string
		var t IdxTask
		if err := rows.Scan(&p, &t.ID, &t.Title, &t.Status); err != nil {
			rows.Close()
			return nil, err
		}
		pos[p+"\x00"+t.ID] = len(out[p])
		out[p] = append(out[p], t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	lrows, err := d.r.QueryContext(ctx,
		`SELECT project_id, task_id, ref FROM task_links WHERE source = 'manual' AND project_id <> ? ORDER BY ref`, exclude)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var p, tid, ref string
		if err := lrows.Scan(&p, &tid, &ref); err != nil {
			return nil, err
		}
		if n, ok := pos[p+"\x00"+tid]; ok {
			out[p][n].Manual = append(out[p][n].Manual, ref)
		}
	}
	return out, lrows.Err()
}
