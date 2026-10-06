package store

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// RecomputeDaily recalculates one row of daily_activity (one project, one local day).
// day is YYYY-MM-DD in loc (nil means time.Local).
//
// Each column is derived from other tables:
//
//	files           distinct paths in file_changes for that day            (history)
//	commits, lines  daily_commits row of that day when there is one (exact, see q_daystats.go),
//	                else commits whose time is in the day                  (commits table)
//	sessions        sessions of the project whose [start, end] touches the day
//	session_minutes the overlap of those sessions with the day, in minutes (an estimate)
//	snapshots       snapshots captured in the day
//	notes           notes created in the day
//	tasks_moved     tasks whose "updated" time is in the day
//	server_minutes  minutes in the day with at least one server of the project up (server_runs)
//
// daily_activity is a history (H) table: it must survive a Reindex and the loss of
// old .jsonl files. Some sources above are caches that can be incomplete for an old
// day: after a Reindex the commits table holds only the newest 200 commits and
// daily_commits only the last DayStatsWindow days, and Claude deletes old transcripts.
// So (C1):
//
//   - commits and lines take the exact daily_commits row when there is one (complete
//     by definition); without it they never go below the stored value (the commit
//     rows may be a capped, partial list for that day);
//   - sessions and session_minutes never go below the stored value (transcripts
//     can be deleted from disk, and after a Reindex they are parsed again later);
//   - the other columns come from complete sources and are taken as computed.
//
// A row is deleted only when every column, after these rules, is zero.
// A server run that has no end time is treated as running until now.
func (d *DB) RecomputeDaily(ctx context.Context, project, day string, loc *time.Location) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return RecomputeDailyTx(ctx, tx, project, day, loc) })
}

// RecomputeDailyTx is RecomputeDaily inside a transaction you already have.
func RecomputeDailyTx(ctx context.Context, tx *sql.Tx, project, day string, loc *time.Location) error {
	start, end, err := dayBounds(day, loc)
	if err != nil {
		return err
	}
	var c DayCell

	// The stored row: history that a partial cache must never lower (C1).
	var old DayCell
	switch err := tx.QueryRowContext(ctx, `
SELECT commits, lines_plus, lines_minus, sessions, session_minutes
FROM daily_activity WHERE project_id = ? AND day = ?`, project, day).
		Scan(&old.Commits, &old.LinesPlus, &old.LinesMinus, &old.Sessions, &old.SessionMinutes); {
	case err == sql.ErrNoRows:
	case err != nil:
		return err
	}

	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM file_changes WHERE project_id = ? AND day = ?`, project, day).Scan(&c.Files); err != nil {
		return err
	}
	// Exact numbers from daily_commits win (q_daystats.go); the commit rows are capped at 200.
	if err := tx.QueryRowContext(ctx,
		`SELECT commits, lines_plus, lines_minus FROM daily_commits WHERE project_id = ? AND day = ?`, project, day).
		Scan(&c.Commits, &c.LinesPlus, &c.LinesMinus); err == sql.ErrNoRows {
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(plus), 0), COALESCE(SUM(minus), 0)
FROM commits WHERE project_id = ? AND ts >= ? AND ts < ?`, project, start, end).
			Scan(&c.Commits, &c.LinesPlus, &c.LinesMinus); err != nil {
			return err
		}
		// The commit rows may be a partial list for this day: keep the history.
		c.Commits = max(c.Commits, old.Commits)
		c.LinesPlus = max(c.LinesPlus, old.LinesPlus)
		c.LinesMinus = max(c.LinesMinus, old.LinesMinus)
	} else if err != nil {
		return err
	}
	var sessSeconds int64
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(MIN(COALESCE("end", "start"), ?) - MAX("start", ?)), 0)
FROM sessions
WHERE project_id = ? AND "start" IS NOT NULL AND "start" < ? AND COALESCE("end", "start") >= ?`,
		end, start, project, end, start).Scan(&c.Sessions, &sessSeconds); err != nil {
		return err
	}
	c.SessionMinutes = int(sessSeconds / 60)
	// Transcripts can be gone from disk (or not parsed again yet after a Reindex).
	c.Sessions = max(c.Sessions, old.Sessions)
	c.SessionMinutes = max(c.SessionMinutes, old.SessionMinutes)

	// Authored times are text such as 2026-09-30T12:02:00+05:30. strftime('%s') turns them into UTC seconds.
	// A text that SQLite cannot read gives NULL, and NULL never matches the range.
	for _, q := range []struct {
		dst   *int
		query string
	}{
		{&c.Snapshots, `SELECT COUNT(*) FROM snapshots WHERE project_id = ?
		                AND CAST(strftime('%s', captured) AS INTEGER) >= ? AND CAST(strftime('%s', captured) AS INTEGER) < ?`},
		{&c.Notes, `SELECT COUNT(*) FROM notes WHERE project_id = ?
		            AND CAST(strftime('%s', created) AS INTEGER) >= ? AND CAST(strftime('%s', created) AS INTEGER) < ?`},
		{&c.TasksMoved, `SELECT COUNT(*) FROM tasks WHERE project_id = ?
		                 AND CAST(strftime('%s', updated) AS INTEGER) >= ? AND CAST(strftime('%s', updated) AS INTEGER) < ?`},
	} {
		if err := tx.QueryRowContext(ctx, q.query, project, start, end).Scan(q.dst); err != nil {
			return err
		}
	}

	mins, err := serverMinutes(ctx, tx, project, start, end)
	if err != nil {
		return err
	}
	c.ServerMinutes = mins

	if c == (DayCell{}) {
		_, err := tx.ExecContext(ctx, `DELETE FROM daily_activity WHERE project_id = ? AND day = ?`, project, day)
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO daily_activity(project_id, day, files, commits, lines_plus, lines_minus, sessions,
                           session_minutes, snapshots, notes, server_minutes, tasks_moved)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, day) DO UPDATE SET
  files = excluded.files, commits = excluded.commits, lines_plus = excluded.lines_plus,
  lines_minus = excluded.lines_minus, sessions = excluded.sessions,
  session_minutes = excluded.session_minutes, snapshots = excluded.snapshots, notes = excluded.notes,
  server_minutes = excluded.server_minutes, tasks_moved = excluded.tasks_moved`,
		project, day, c.Files, c.Commits, c.LinesPlus, c.LinesMinus, c.Sessions, c.SessionMinutes,
		c.Snapshots, c.Notes, c.ServerMinutes, c.TasksMoved)
	return err
}

// serverMinutes returns the whole minutes in [start, end) in which at least one run of the
// project was up. Overlapping runs are merged, so two servers at once count once.
func serverMinutes(ctx context.Context, tx *sql.Tx, project string, start, end int64) (int, error) {
	nowSec := now().Unix()
	rows, err := tx.QueryContext(ctx, `
SELECT started, COALESCE(ended, ?) FROM server_runs
WHERE project_id = ? AND started < ? AND COALESCE(ended, ?) > ?`, nowSec, project, end, nowSec, start)
	if err != nil {
		return 0, err
	}
	type span struct{ a, b int64 }
	var spans []span
	for rows.Next() {
		var s span
		if err := rows.Scan(&s.a, &s.b); err != nil {
			rows.Close()
			return 0, err
		}
		if s.a < start {
			s.a = start
		}
		if s.b > end {
			s.b = end
		}
		if s.b > s.a {
			spans = append(spans, s)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	sort.Slice(spans, func(i, j int) bool { return spans[i].a < spans[j].a })
	var total int64
	var cur span
	for i, s := range spans {
		switch {
		case i == 0:
			cur = s
		case s.a <= cur.b:
			if s.b > cur.b {
				cur.b = s.b
			}
		default:
			total += cur.b - cur.a
			cur = s
		}
	}
	if len(spans) > 0 {
		total += cur.b - cur.a
	}
	return int(total / 60), nil
}
