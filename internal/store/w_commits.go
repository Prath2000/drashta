package store

import (
	"context"
	"database/sql"

	"github.com/Prath2000/drashta/internal/probe"
)

// ReplaceCommits brings the commit rows of a project up to date with a fresh log.
//
// The log is only the newest part of the history (git log is capped at 200). So:
//   - every given commit is inserted or updated, with its files (commit_files);
//   - commits that are newer than the oldest given commit OF THE SAME VCS, but are not in
//     the list any more (a rebase, an amend, a reset), are deleted (see CommitGone);
//   - older commits (and ones with the very same time as that oldest one) are kept;
//   - a VCS that has no commit in the list loses all its rows;
//   - an empty list deletes all commits of the project.
//
// Search rows (kind "commit", title = subject, body = hash + body) change in the same transaction.
// Days are not recomputed here. Call RecomputeDaily for the days you touched.
func (d *DB) ReplaceCommits(ctx context.Context, project string, commits []probe.Commit) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return ReplaceCommitsTx(ctx, tx, project, commits) })
}

// OldestPerVCS returns, per VCS ("git", "fossil"; "" counts as "git"), the unix time of
// the oldest commit in the list. Each VCS has its own capped log, so the cut-off of one
// must never be used for the other (I2).
func OldestPerVCS(commits []probe.Commit) map[string]int64 {
	out := map[string]int64{}
	for _, c := range commits {
		v := c.VCS
		if v == "" {
			v = "git"
		}
		ts := unixOrZero(c.Time)
		if o, ok := out[v]; !ok || ts < o {
			out[v] = ts
		}
	}
	return out
}

// CommitGone reports whether a stored commit (vcs, ts) that is NOT in the new list is gone
// (rewritten history) rather than just older than the capped log. It is gone only when it
// is strictly newer than the oldest given commit of the same VCS: a commit with the same
// time as that oldest one may simply have fallen off the cap (M1). A VCS with no commit in
// the list at all means all its commits are gone.
func CommitGone(oldest map[string]int64, vcs string, ts int64) bool {
	if vcs == "" {
		vcs = "git"
	}
	o, ok := oldest[vcs]
	if !ok {
		return true
	}
	return ts > o
}

// ReplaceCommitsTx is ReplaceCommits inside a transaction you already have.
func ReplaceCommitsTx(ctx context.Context, tx *sql.Tx, project string, commits []probe.Commit) error {
	f, err := newFTSWriter(ctx, tx)
	if err != nil {
		return err
	}
	defer f.close()

	given := make(map[string]bool, len(commits))
	oldest := OldestPerVCS(commits)
	for _, c := range commits {
		given[c.ID] = true
	}

	// Find commits to drop.
	var drop []string
	rows, err := tx.QueryContext(ctx, `SELECT id, vcs, ts FROM commits WHERE project_id = ?`, project)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, vcs string
		var ts int64
		if err := rows.Scan(&id, &vcs, &ts); err != nil {
			rows.Close()
			return err
		}
		if len(commits) == 0 || (!given[id] && CommitGone(oldest, vcs, ts)) {
			drop = append(drop, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, id := range drop {
		if _, err := tx.ExecContext(ctx, `DELETE FROM commits WHERE project_id = ? AND id = ?`, project, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM commit_files WHERE project_id = ? AND commit_id = ?`, project, id); err != nil {
			return err
		}
		if err := f.del("commit", project, "commit:"+id); err != nil {
			return err
		}
	}

	upsert, err := tx.PrepareContext(ctx, `
INSERT INTO commits(project_id, id, vcs, author, ts, msg, body, plus, minus, nfiles, branch)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, id) DO UPDATE SET
  vcs = excluded.vcs, author = excluded.author, ts = excluded.ts, msg = excluded.msg,
  body = excluded.body, plus = excluded.plus, minus = excluded.minus, nfiles = excluded.nfiles,
  branch = excluded.branch`)
	if err != nil {
		return err
	}
	defer upsert.Close()
	delFiles, err := tx.PrepareContext(ctx, `DELETE FROM commit_files WHERE project_id = ? AND commit_id = ?`)
	if err != nil {
		return err
	}
	defer delFiles.Close()
	insFile, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO commit_files(project_id, commit_id, path, plus, minus) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insFile.Close()

	for _, c := range commits {
		vcs := c.VCS
		if vcs == "" {
			vcs = "git"
		}
		nfiles := c.NFiles
		if nfiles == 0 {
			nfiles = len(c.Files)
		}
		body := clip(c.Body, MaxBody)
		if _, err := upsert.ExecContext(ctx, project, c.ID, vcs, c.Author, unixOrZero(c.Time),
			c.Msg, body, c.Plus, c.Minus, nfiles, c.Branch); err != nil {
			return err
		}
		if _, err := delFiles.ExecContext(ctx, project, c.ID); err != nil {
			return err
		}
		for _, cf := range c.Files {
			if _, err := insFile.ExecContext(ctx, project, c.ID, cf.Path, cf.Plus, cf.Minus); err != nil {
				return err
			}
		}
		// The hash goes into the body so a pasted hash (or a piece of it) finds the commit.
		if err := f.put("commit", project, "commit:"+c.ID, c.Msg, c.ID+"\n"+body, unixOrZero(c.Time)); err != nil {
			return err
		}
	}
	return nil
}
