package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// DayFile is a path changed on a day, with how many changes were seen.
type DayFile struct {
	Path string `json:"path"`
	N    int    `json:"n"`
}

// DayData is the answer for GET /day.
type DayData struct {
	Date   string  `json:"date"`
	Events []Event `json:"events"` // oldest first
	Totals DayCell `json:"totals"` // from daily_activity, for the project or summed over all projects
	// Files is the top 20 changed paths of the day. Only filled when a project is given.
	Files []DayFile `json:"files"`
	// FilesTotal is the number of distinct changed paths (for the project, or over all projects).
	FilesTotal int `json:"filesTotal"`
}

// maxDayEvents is the most events Day returns for one day.
const maxDayEvents = 2000

// topDayFiles is how many paths Day returns in Files.
const topDayFiles = 20

// Day returns what happened on one local day (date is YYYY-MM-DD). project is optional ("" = all).
func (d *DB) Day(ctx context.Context, date, project string) (DayData, error) {
	out := DayData{Date: date, Events: []Event{}, Files: []DayFile{}}
	if _, err := time.Parse(DayLayout, date); err != nil {
		return out, fmt.Errorf("store: bad date %q (want YYYY-MM-DD)", date)
	}

	tx, err := d.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck // read only

	// events
	q := `SELECT id, project_id, ts, day, kind, ref, label, meta FROM events WHERE day = ?`
	args := []any{date}
	if project != "" {
		q += ` AND project_id = ?`
		args = append(args, project)
	}
	q += ` ORDER BY ts, id LIMIT ?`
	args = append(args, maxDayEvents)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var e Event
		var ts int64
		var meta string
		if err := rows.Scan(&e.ID, &e.ProjectID, &ts, &e.Day, &e.Kind, &e.Ref, &e.Label, &meta); err != nil {
			rows.Close()
			return out, err
		}
		e.TS = time.Unix(ts, 0)
		if meta != "" && meta != "{}" {
			e.Meta = json.RawMessage(meta)
		}
		out.Events = append(out.Events, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()

	// totals
	tq := `SELECT COALESCE(SUM(files),0), COALESCE(SUM(commits),0), COALESCE(SUM(lines_plus),0),
	              COALESCE(SUM(lines_minus),0), COALESCE(SUM(sessions),0), COALESCE(SUM(session_minutes),0),
	              COALESCE(SUM(snapshots),0), COALESCE(SUM(notes),0), COALESCE(SUM(server_minutes),0),
	              COALESCE(SUM(tasks_moved),0)
	       FROM daily_activity WHERE day = ?`
	targs := []any{date}
	if project != "" {
		tq += ` AND project_id = ?`
		targs = append(targs, project)
	}
	c := &out.Totals
	if err := tx.QueryRowContext(ctx, tq, targs...).Scan(&c.Files, &c.Commits, &c.LinesPlus, &c.LinesMinus,
		&c.Sessions, &c.SessionMinutes, &c.Snapshots, &c.Notes, &c.ServerMinutes, &c.TasksMoved); err != nil {
		return out, err
	}

	// files
	if project != "" {
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM file_changes WHERE project_id = ? AND day = ?`, project, date).Scan(&out.FilesTotal); err != nil {
			return out, err
		}
		rows, err = tx.QueryContext(ctx,
			`SELECT path, n FROM file_changes WHERE project_id = ? AND day = ? ORDER BY n DESC, path LIMIT ?`,
			project, date, topDayFiles)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var f DayFile
			if err := rows.Scan(&f.Path, &f.N); err != nil {
				rows.Close()
				return out, err
			}
			out.Files = append(out.Files, f)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return out, err
		}
		rows.Close()
	} else {
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM file_changes WHERE day = ?`, date).Scan(&out.FilesTotal); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}
