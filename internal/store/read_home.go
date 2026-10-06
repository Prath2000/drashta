package store

import (
	"context"
	"database/sql"
	"time"
)

// DayCell is one day of activity for one project. JSON names follow 04 section 6 (GET /home).
type DayCell struct {
	Files          int `json:"files"`
	Commits        int `json:"commits"`
	LinesPlus      int `json:"linesPlus"`
	LinesMinus     int `json:"linesMinus"`
	Sessions       int `json:"sessions"`
	SessionMinutes int `json:"sessionMinutes"`
	Snapshots      int `json:"snapshots"`
	Notes          int `json:"notes"`
	ServerMinutes  int `json:"serverMinutes"`
	TasksMoved     int `json:"tasksMoved"`
}

// Live is what is running right now in one project.
type Live struct {
	Servers  []int    `json:"servers"`  // ports of dev servers
	Sessions []string `json:"sessions"` // ids of Claude sessions written to in the last LiveSessionWindow
}

// Lane is one row of the seismograph.
type Lane struct {
	ProjectID    string    `json:"projectId"`
	Name         string    `json:"name"`
	Group        string    `json:"group"`
	Hidden       bool      `json:"hidden"`
	Dirty        int       `json:"dirty"` // number of uncommitted paths
	Days         []DayCell `json:"days"`  // same length and order as HomeData.Days
	Live         Live      `json:"live"`
	LastChangeAt time.Time `json:"lastChangeAt"`
}

// HomeData is the answer for GET /home.
type HomeData struct {
	Range int      `json:"range"`
	Days  []string `json:"days"` // oldest first, the last one is today (YYYY-MM-DD, local)
	Lanes []Lane   `json:"lanes"`
}

// LiveSessionWindow: a Claude session counts as live when its .jsonl was written this recently.
const LiveSessionWindow = 5 * time.Minute

// maxHomeRange is the longest range Home accepts.
const maxHomeRange = 366

// Home builds the seismograph data: every project (hidden ones too, with Hidden set), one cell per day.
//
// It runs 4 queries in one read transaction, one per kind of data and not one per project:
// projects (with the dirty count), daily_activity, servers_now and sessions.
// rangeDays is 30 when <= 0. A nil loc means time.Local. Days without a row are zero cells.
// Lanes are ordered by last change, newest first, then by id.
func (d *DB) Home(ctx context.Context, rangeDays int, loc *time.Location) (HomeData, error) {
	return d.HomeAt(ctx, rangeDays, loc, now())
}

// HomeAt is Home with an explicit "now": the last cell is the day of at in loc, and live
// sessions are those changed within LiveSessionWindow before at.
func (d *DB) HomeAt(ctx context.Context, rangeDays int, loc *time.Location, at time.Time) (HomeData, error) {
	if rangeDays <= 0 {
		rangeDays = 30
	}
	if rangeDays > maxHomeRange {
		rangeDays = maxHomeRange
	}
	if loc == nil {
		loc = time.Local
	}
	today := DayOf(at, loc)

	days := make([]string, rangeDays)
	index := make(map[string]int, rangeDays)
	for i := 0; i < rangeDays; i++ {
		// Calendar math on the day key: safe in zones that skip midnight (I1).
		day := mustAddDays(today, -(rangeDays - 1 - i))
		days[i] = day
		index[day] = i
	}
	out := HomeData{Range: rangeDays, Days: days, Lanes: []Lane{}}

	tx, err := d.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck // read only

	// 1. lanes
	laneAt := map[string]int{}
	rows, err := tx.QueryContext(ctx, `
SELECT p.id, p.name, p.grp, p.hidden, p.last_change_at,
       (SELECT COUNT(*) FROM dirty d WHERE d.project_id = p.id)
FROM projects p
ORDER BY COALESCE(p.last_change_at, 0) DESC, p.id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var l Lane
		var hidden int
		var last sql.NullInt64
		if err := rows.Scan(&l.ProjectID, &l.Name, &l.Group, &hidden, &last, &l.Dirty); err != nil {
			rows.Close()
			return out, err
		}
		l.Hidden = hidden != 0
		l.LastChangeAt = fromUnix(last)
		l.Days = make([]DayCell, rangeDays)
		l.Live = Live{Servers: []int{}, Sessions: []string{}}
		laneAt[l.ProjectID] = len(out.Lanes)
		out.Lanes = append(out.Lanes, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()

	// 2. activity for all lanes at once
	rows, err = tx.QueryContext(ctx, `
SELECT project_id, day, files, commits, lines_plus, lines_minus, sessions, session_minutes,
       snapshots, notes, server_minutes, tasks_moved
FROM daily_activity WHERE day >= ? AND day <= ?`, days[0], days[rangeDays-1])
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var pid, day string
		var c DayCell
		if err := rows.Scan(&pid, &day, &c.Files, &c.Commits, &c.LinesPlus, &c.LinesMinus, &c.Sessions,
			&c.SessionMinutes, &c.Snapshots, &c.Notes, &c.ServerMinutes, &c.TasksMoved); err != nil {
			rows.Close()
			return out, err
		}
		li, ok := laneAt[pid]
		di, ok2 := index[day]
		if ok && ok2 {
			out.Lanes[li].Days[di] = c
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()

	// 3. live dev servers
	rows, err = tx.QueryContext(ctx,
		`SELECT project_id, port FROM servers_now WHERE project_id != '' AND kind = 'dev' ORDER BY port`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var pid string
		var port int
		if err := rows.Scan(&pid, &port); err != nil {
			rows.Close()
			return out, err
		}
		if li, ok := laneAt[pid]; ok {
			out.Lanes[li].Live.Servers = append(out.Lanes[li].Live.Servers, port)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()

	// 4. live sessions
	rows, err = tx.QueryContext(ctx,
		`SELECT project_id, id FROM sessions WHERE project_id != '' AND jsonl_mtime >= ? ORDER BY jsonl_mtime DESC`,
		at.Add(-LiveSessionWindow).Unix())
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var pid, id string
		if err := rows.Scan(&pid, &id); err != nil {
			rows.Close()
			return out, err
		}
		if li, ok := laneAt[pid]; ok {
			out.Lanes[li].Live.Sessions = append(out.Lanes[li].Live.Sessions, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()

	return out, tx.Commit()
}
