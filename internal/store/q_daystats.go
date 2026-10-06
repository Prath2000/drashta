package store

// Exact per-day commit numbers (table daily_commits, migration 002).
//
// The commits table keeps only the newest 200 commits of a project, so counting
// its rows is wrong for a busy project. The indexer reads the real numbers per
// local day with git.DayStats / fossil.DayStats and writes them here. Then
// RecomputeDaily takes commits, lines_plus and lines_minus of daily_activity
// from daily_commits for every day that has a row, and from the commit rows
// only for days that have none (older than any window we counted).

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// SetDailyCommitStats stores the exact commit numbers of one project for every
// day from..to (YYYY-MM-DD, both included). A day that is missing in stats is
// stored as zero ("counted, no commit"). Days outside from..to are not touched.
// Every day whose numbers changed (or had no row before) gets RecomputeDaily, so
// daily_activity is right when it returns. It returns those days, sorted.
func (d *DB) SetDailyCommitStats(ctx context.Context, project, from, to string,
	stats map[string]probe.DayCount, loc *time.Location) ([]string, error) {
	var changed []string
	err := d.Write(ctx, func(tx *sql.Tx) error {
		var err error
		changed, err = SetDailyCommitStatsTx(ctx, tx, project, from, to, stats, loc)
		return err
	})
	return changed, err
}

// SetDailyCommitStatsTx is SetDailyCommitStats inside a transaction you already have.
func SetDailyCommitStatsTx(ctx context.Context, tx *sql.Tx, project, from, to string,
	stats map[string]probe.DayCount, loc *time.Location) ([]string, error) {
	if loc == nil {
		loc = time.Local
	}
	if _, err := AddDays(from, 0); err != nil {
		return nil, err
	}
	if _, err := AddDays(to, 0); err != nil {
		return nil, err
	}
	old, err := dailyCommitRows(ctx, tx, project, from, to)
	if err != nil {
		return nil, err
	}
	var changed []string
	// Step by calendar day key (pure date math), never by a local midnight instant:
	// in zones where midnight is skipped that would loop for ever (I1).
	for day := from; day <= to; day = mustAddDays(day, 1) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		want := stats[day]
		if have, ok := old[day]; ok && have == want {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO daily_commits(project_id, day, commits, lines_plus, lines_minus) VALUES(?, ?, ?, ?, ?)
ON CONFLICT(project_id, day) DO UPDATE SET
  commits = excluded.commits, lines_plus = excluded.lines_plus, lines_minus = excluded.lines_minus`,
			project, day, want.Commits, want.Plus, want.Minus); err != nil {
			return nil, err
		}
		if err := RecomputeDailyTx(ctx, tx, project, day, loc); err != nil {
			return nil, err
		}
		changed = append(changed, day)
	}
	return changed, nil
}

// ClearDailyCommitStatsTx removes every daily_commits row of a project (for
// example when its folder is no longer a repository) and recomputes those days
// from the commit rows. It returns the days that were cleared, sorted.
func ClearDailyCommitStatsTx(ctx context.Context, tx *sql.Tx, project string, loc *time.Location) ([]string, error) {
	old, err := dailyCommitRows(ctx, tx, project, "", "")
	if err != nil {
		return nil, err
	}
	if len(old) == 0 {
		return nil, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM daily_commits WHERE project_id = ?`, project); err != nil {
		return nil, err
	}
	days := make([]string, 0, len(old))
	for day := range old {
		days = append(days, day)
	}
	sort.Strings(days)
	for _, day := range days {
		if err := RecomputeDailyTx(ctx, tx, project, day, loc); err != nil {
			return nil, err
		}
	}
	return days, nil
}

// DailyCommitStats reads the stored exact numbers of one project, day -> numbers.
// Empty from and to mean every day.
func (d *DB) DailyCommitStats(ctx context.Context, project, from, to string) (map[string]probe.DayCount, error) {
	tx, err := d.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // read only
	return dailyCommitRows(ctx, tx, project, from, to)
}

func dailyCommitRows(ctx context.Context, tx *sql.Tx, project, from, to string) (map[string]probe.DayCount, error) {
	q := `SELECT day, commits, lines_plus, lines_minus FROM daily_commits WHERE project_id = ?`
	args := []any{project}
	if from != "" {
		q += ` AND day >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND day <= ?`
		args = append(args, to)
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]probe.DayCount{}
	for rows.Next() {
		var day string
		var c probe.DayCount
		if err := rows.Scan(&day, &c.Commits, &c.Plus, &c.Minus); err != nil {
			return nil, err
		}
		out[day] = c
	}
	return out, rows.Err()
}
