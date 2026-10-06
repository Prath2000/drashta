package store

// Queries added by the Wave 3 fix wave (fix-core).

import (
	"context"
	"database/sql"
	"strings"
)

// IdxCommitTime is the stored time and VCS of one commit.
type IdxCommitTime struct {
	VCS string // "git" or "fossil"
	TS  int64  // unix seconds
}

// IdxCommitTimesVCSTx returns id -> (vcs, time) for every stored commit of a project,
// read inside your transaction (so it matches what ReplaceCommitsTx will see).
func IdxCommitTimesVCSTx(ctx context.Context, tx *sql.Tx, project string) (map[string]IdxCommitTime, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, vcs, ts FROM commits WHERE project_id = ?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]IdxCommitTime{}
	for rows.Next() {
		var id string
		var t IdxCommitTime
		if err := rows.Scan(&id, &t.VCS, &t.TS); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// IdxUnassignedSession is a session that belongs to no project yet.
type IdxUnassignedSession struct {
	ID        string
	Profile   string
	JSONLPath string
	CWD       string
}

// IdxUnassignedSessions returns the sessions whose project_id is empty. The indexer
// resolves them again when a new project appears (M13).
func (d *DB) IdxUnassignedSessions(ctx context.Context) ([]IdxUnassignedSession, error) {
	rows, err := d.r.QueryContext(ctx,
		`SELECT id, profile, jsonl_path, cwd FROM sessions WHERE project_id = '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdxUnassignedSession
	for rows.Next() {
		var s IdxUnassignedSession
		if err := rows.Scan(&s.ID, &s.Profile, &s.JSONLPath, &s.CWD); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// IdxForgetClaudeFamilyTx removes the claude_file rows of one session family: the main
// .jsonl file and every file below <main without .jsonl>/ (its subagent files). The next
// parse of that session then starts from zero.
func IdxForgetClaudeFamilyTx(ctx context.Context, tx *sql.Tx, mainPath string) error {
	dir := strings.TrimSuffix(mainPath, ".jsonl") + "/"
	_, err := tx.ExecContext(ctx,
		`DELETE FROM claude_file WHERE path = ? OR substr(path, 1, ?) = ?`, mainPath, len(dir), dir)
	return err
}
