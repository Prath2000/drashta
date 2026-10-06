package store

// Queries for the project pages of the HTTP API (internal/httpapi/api_project.go and friends).
//
// Every name here starts with "Proj" so it cannot clash with the queries other API
// agents add in their own q_*.go files. These are read-only helpers on the read pool.
//
//	(*DB).ProjGet(ctx, id) (Project, bool, error)                       one project row by id
//	(*DB).ProjCountsFor(ctx, id) (ProjCounts, error)                    the numbers on the Overview
//	(*DB).ProjDirtyFiles(ctx, id) ([]probe.DirtyFile, error)            uncommitted paths, A-Z
//	(*DB).ProjDirtyStates(ctx, id) (map[string]string, error)           path -> state
//	(*DB).ProjMilestones(ctx, id) ([]ProjMilestone, error)              milestones with feature ids and task counts
//	(*DB).ProjActivity(ctx, id, rangeDays, loc) ([]ProjDay, error)      one cell per day, oldest first, zero filled
//	(*DB).ProjActivityAt(ctx, id, rangeDays, loc, at) ([]ProjDay, error) same, with an explicit "now"
//	(*DB).ProjCommitsBefore(ctx, id, cur, limit) ([]ProjCommit, error)  keyset page, newest first
//	(*DB).ProjCommitCounts(ctx, id) (map[string]int, error)             cached commits per vcs
//	(*DB).ProjCommitByPrefix(ctx, id, prefix) (ProjCommit, bool, error) one cached commit by hash prefix
//	(*DB).ProjTaskRefs(ctx, id, typ) ([]ProjTaskRef, error)            task links of one ref type (commit, session)
//	(*DB).ProjSessionsPage(ctx, id, cur, limit) ([]ProjSession, error)  keyset page, newest first, with reason
//	(*DB).ProjSessionByID(ctx, sessionID) (ProjSession, bool, error)    one session, with its .jsonl path
//	(*DB).ProjDocs(ctx, id) ([]ProjDoc, error)                          files of kind "doc"
//	(*DB).ProjFileNotes(ctx, id, path) ([]ProjNote, error)              notes pinned on a file
//	(*DB).ProjServerTitles(ctx) ([]ProjServerTitle, error)              page titles of the servers that run now

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// ProjGet returns one project by id. ok is false when there is no such project.
func (d *DB) ProjGet(ctx context.Context, id string) (p Project, ok bool, err error) {
	var hidden int
	var stack string
	var last, idx sql.NullInt64
	err = d.r.QueryRowContext(ctx, `
SELECT id, name, grp, path, hidden, vcs, branch, remote, description, stack, readme_path,
       last_change_at, indexed_at
FROM projects WHERE id = ?`, id).Scan(&p.ID, &p.Name, &p.Group, &p.Path, &hidden, &p.VCS, &p.Branch,
		&p.Remote, &p.Description, &stack, &p.ReadmePath, &last, &idx)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, err
	}
	p.Hidden = hidden != 0
	p.Stack = []string{}
	if stack != "" {
		_ = json.Unmarshal([]byte(stack), &p.Stack) // a bad value leaves the empty list
		if p.Stack == nil {
			p.Stack = []string{}
		}
	}
	p.LastChangeAt = fromUnix(last)
	p.IndexedAt = fromUnix(idx)
	return p, true, nil
}

// ProjCounts are the numbers shown on the project Overview.
type ProjCounts struct {
	Files     int `json:"files"`
	Commits   int `json:"commits"`
	Sessions  int `json:"sessions"`
	OpenTasks int `json:"openTasks"`
	OpenNotes int `json:"openNotes"`
	Dirty     int `json:"dirty"`
}

// ProjCountsFor counts the cached rows of one project. Commits is the number of cached
// commits (the index keeps the newest ones), not the full history.
func (d *DB) ProjCountsFor(ctx context.Context, id string) (ProjCounts, error) {
	var c ProjCounts
	err := d.r.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*) FROM files   WHERE project_id = ?),
  (SELECT COUNT(*) FROM commits WHERE project_id = ?),
  (SELECT COUNT(*) FROM sessions s WHERE s.project_id = ?
      OR EXISTS (SELECT 1 FROM session_projects sp WHERE sp.session_id = s.id AND sp.project_id = ?)),
  (SELECT COUNT(*) FROM tasks   WHERE project_id = ? AND status != 'done'),
  (SELECT COUNT(*) FROM notes   WHERE project_id = ? AND resolved = 0),
  (SELECT COUNT(*) FROM dirty   WHERE project_id = ?)`,
		id, id, id, id, id, id, id).Scan(&c.Files, &c.Commits, &c.Sessions, &c.OpenTasks, &c.OpenNotes, &c.Dirty)
	return c, err
}

// ProjDirtyFiles returns the uncommitted paths of a project, ordered by path.
func (d *DB) ProjDirtyFiles(ctx context.Context, id string) ([]probe.DirtyFile, error) {
	rows, err := d.r.QueryContext(ctx, `SELECT path, state FROM dirty WHERE project_id = ? ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []probe.DirtyFile{}
	for rows.Next() {
		var f probe.DirtyFile
		if err := rows.Scan(&f.Path, &f.State); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ProjDirtyStates returns path -> state for the uncommitted paths of a project.
func (d *DB) ProjDirtyStates(ctx context.Context, id string) (map[string]string, error) {
	files, err := d.ProjDirtyFiles(ctx, id)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(files))
	for _, f := range files {
		m[f.Path] = f.State
	}
	return m, nil
}

// ProjMilestone is one milestone with the features that belong to it and a task count.
type ProjMilestone struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"projectId"`
	Title      string   `json:"title"`
	Due        *string  `json:"due"` // nil when the milestone has no due date
	State      string   `json:"state"`
	FeatureIDs []string `json:"featureIds"`
	Tasks      int      `json:"tasks"` // tasks in the features of this milestone
	Done       int      `json:"done"`  // of those, how many are done
	Rev        string   `json:"rev,omitempty"`
}

// ProjMilestones returns the milestones of a project: dated ones first (by date), then the rest,
// then by id (M-2 before M-10).
func (d *DB) ProjMilestones(ctx context.Context, id string) ([]ProjMilestone, error) {
	rows, err := d.r.QueryContext(ctx,
		`SELECT id, title, due, state, file_rev FROM milestones WHERE project_id = ?`, id)
	if err != nil {
		return nil, err
	}
	out := []ProjMilestone{}
	for rows.Next() {
		m := ProjMilestone{ProjectID: id, FeatureIDs: []string{}}
		var due string
		if err := rows.Scan(&m.ID, &m.Title, &due, &m.State, &m.Rev); err != nil {
			rows.Close()
			return nil, err
		}
		if due != "" {
			m.Due = &due
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}

	at := make(map[string]int, len(out))
	for i, m := range out {
		at[m.ID] = i
	}
	featureMilestone := map[string]string{}
	frows, err := d.r.QueryContext(ctx,
		`SELECT id, milestone_id FROM features WHERE project_id = ? AND milestone_id != '' ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var fid, mid string
		if err := frows.Scan(&fid, &mid); err != nil {
			frows.Close()
			return nil, err
		}
		if i, ok := at[mid]; ok {
			out[i].FeatureIDs = append(out[i].FeatureIDs, fid)
			featureMilestone[fid] = mid
		}
	}
	if err := frows.Err(); err != nil {
		frows.Close()
		return nil, err
	}
	frows.Close()
	for i := range out {
		sort.SliceStable(out[i].FeatureIDs, func(a, b int) bool { return projNatLess(out[i].FeatureIDs[a], out[i].FeatureIDs[b]) })
	}

	trows, err := d.r.QueryContext(ctx,
		`SELECT feature_id, status, COUNT(*) FROM tasks WHERE project_id = ? AND feature_id != '' GROUP BY feature_id, status`, id)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var fid, status string
		var n int
		if err := trows.Scan(&fid, &status, &n); err != nil {
			trows.Close()
			return nil, err
		}
		if mid, ok := featureMilestone[fid]; ok {
			i := at[mid]
			out[i].Tasks += n
			if status == "done" {
				out[i].Done += n
			}
		}
	}
	if err := trows.Err(); err != nil {
		trows.Close()
		return nil, err
	}
	trows.Close()

	sort.SliceStable(out, func(a, b int) bool {
		da, db := "", ""
		if out[a].Due != nil {
			da = *out[a].Due
		}
		if out[b].Due != nil {
			db = *out[b].Due
		}
		if (da == "") != (db == "") {
			return da != "" // dated first
		}
		if da != db {
			return da < db
		}
		return projNatLess(out[a].ID, out[b].ID)
	})
	return out, nil
}

var projDigitsRe = regexp.MustCompile(`^(.*?)(\d+)$`)

// projNatLess orders ids such as "M-2" before "M-10".
func projNatLess(a, b string) bool {
	ma, mb := projDigitsRe.FindStringSubmatch(a), projDigitsRe.FindStringSubmatch(b)
	if ma != nil && mb != nil && ma[1] == mb[1] {
		na, _ := strconv.Atoi(ma[2])
		nb, _ := strconv.Atoi(mb[2])
		if na != nb {
			return na < nb
		}
	}
	return a < b
}

// ProjDay is one day of the project seismograph. The cell fields are flattened in JSON.
type ProjDay struct {
	Day string `json:"day"`
	DayCell
}

// ProjActivity returns one cell per day for the last rangeDays days, oldest first, the last
// one is today. Days without a row are zero cells. rangeDays is 30 when <= 0 (max 366).
func (d *DB) ProjActivity(ctx context.Context, id string, rangeDays int, loc *time.Location) ([]ProjDay, error) {
	return d.ProjActivityAt(ctx, id, rangeDays, loc, now())
}

// ProjActivityAt is ProjActivity with an explicit "now": the last cell is the day of at in loc.
// The HTTP layer passes its injected clock here.
func (d *DB) ProjActivityAt(ctx context.Context, id string, rangeDays int, loc *time.Location, at time.Time) ([]ProjDay, error) {
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
	days := make([]ProjDay, rangeDays)
	index := make(map[string]int, rangeDays)
	for i := range days {
		day := mustAddDays(today, -(rangeDays - 1 - i)) // calendar math, DST safe (I1)
		days[i].Day = day
		index[day] = i
	}
	rows, err := d.r.QueryContext(ctx, `
SELECT day, files, commits, lines_plus, lines_minus, sessions, session_minutes,
       snapshots, notes, server_minutes, tasks_moved
FROM daily_activity WHERE project_id = ? AND day >= ? AND day <= ?`,
		id, days[0].Day, days[rangeDays-1].Day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var c DayCell
		if err := rows.Scan(&day, &c.Files, &c.Commits, &c.LinesPlus, &c.LinesMinus, &c.Sessions,
			&c.SessionMinutes, &c.Snapshots, &c.Notes, &c.ServerMinutes, &c.TasksMoved); err != nil {
			return nil, err
		}
		if i, ok := index[day]; ok {
			days[i].DayCell = c
		}
	}
	return days, rows.Err()
}

// ProjCommit is one cached commit row.
type ProjCommit struct {
	ID     string
	VCS    string
	Author string
	Time   time.Time
	Msg    string
	Body   string
	Plus   int
	Minus  int
	NFiles int
}

// ProjCursor is a keyset position in a list that is ordered newest first.
// Unix is the sort time (seconds), ID breaks ties. The zero value means "from the start".
type ProjCursor struct {
	Set  bool
	Unix int64
	ID   string
}

// ProjCommitsBefore returns up to limit cached commits older than cur, newest first.
func (d *DB) ProjCommitsBefore(ctx context.Context, id string, cur ProjCursor, limit int) ([]ProjCommit, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, vcs, author, ts, msg, body, plus, minus, nfiles FROM commits WHERE project_id = ?`
	args := []any{id}
	if cur.Set {
		q += ` AND (ts < ? OR (ts = ? AND id < ?))`
		args = append(args, cur.Unix, cur.Unix, cur.ID)
	}
	q += ` ORDER BY ts DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjCommit{}
	for rows.Next() {
		c, err := projScanCommit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func projScanCommit(rows *sql.Rows) (ProjCommit, error) {
	var c ProjCommit
	var ts int64
	if err := rows.Scan(&c.ID, &c.VCS, &c.Author, &ts, &c.Msg, &c.Body, &c.Plus, &c.Minus, &c.NFiles); err != nil {
		return c, err
	}
	if ts != 0 {
		c.Time = time.Unix(ts, 0)
	}
	return c, nil
}

// ProjCommitCounts returns how many commits are cached per vcs ("git", "fossil").
func (d *DB) ProjCommitCounts(ctx context.Context, id string) (map[string]int, error) {
	rows, err := d.r.QueryContext(ctx, `SELECT vcs, COUNT(*) FROM commits WHERE project_id = ? GROUP BY vcs`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var v string
		var n int
		if err := rows.Scan(&v, &n); err != nil {
			return nil, err
		}
		out[v] = n
	}
	return out, rows.Err()
}

// ProjCommitByPrefix finds a cached commit whose hash starts with prefix (lower case hex).
// When several match, the newest one is returned.
func (d *DB) ProjCommitByPrefix(ctx context.Context, id, prefix string) (ProjCommit, bool, error) {
	if prefix == "" {
		return ProjCommit{}, false, nil
	}
	rows, err := d.r.QueryContext(ctx, `
SELECT id, vcs, author, ts, msg, body, plus, minus, nfiles FROM commits
WHERE project_id = ? AND substr(id, 1, ?) = ? ORDER BY ts DESC, id DESC LIMIT 1`,
		id, len(prefix), prefix)
	if err != nil {
		return ProjCommit{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return ProjCommit{}, false, rows.Err()
	}
	c, err := projScanCommit(rows)
	return c, err == nil, err
}

// ProjTaskRef is one task link: the task id and the ref text it points at.
type ProjTaskRef struct {
	TaskID string
	Ref    string
}

// projLikeEscape escapes % _ and \ for a LIKE pattern that uses ESCAPE '\'.
func projLikeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ProjTaskRefs returns the task links of a project whose ref has the given type, for example
// "commit" or "session". Both forms are returned: "commit:abc1234" and "<project>:commit:abc1234".
func (d *DB) ProjTaskRefs(ctx context.Context, id, typ string) ([]ProjTaskRef, error) {
	rows, err := d.r.QueryContext(ctx, `
SELECT task_id, ref FROM task_links
WHERE project_id = ? AND (ref LIKE ? ESCAPE '\' OR ref LIKE ? ESCAPE '\')
ORDER BY task_id, ref`,
		id, projLikeEscape(typ)+`:%`, projLikeEscape(id)+`:`+projLikeEscape(typ)+`:%`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjTaskRef{}
	for rows.Next() {
		var r ProjTaskRef
		if err := rows.Scan(&r.TaskID, &r.Ref); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProjSession is one session row with the reason it belongs to a project.
type ProjSession struct {
	ID        string
	Profile   string
	JSONLPath string
	CWD       string
	ProjectID string // the project the session ran in ("" = unassigned)
	Start     time.Time
	End       time.Time
	Title     string
	Turns     int
	KB        int
	Tools     map[string]int
	Edited    []string
	Reason    string // cwd | mention | edit
}

// SortKey is the time a session is ordered by: its end, else its start.
func (s ProjSession) SortKey() int64 {
	if !s.End.IsZero() {
		return s.End.Unix()
	}
	if !s.Start.IsZero() {
		return s.Start.Unix()
	}
	return 0
}

const projSessionColumns = `s.id, s.profile, s.jsonl_path, s.cwd, s.project_id, s."start", s."end", s.title,
  s.turns, s.kb, s.tools, s.edited`

func projScanSession(rows *sql.Rows, withReason bool) (ProjSession, error) {
	var s ProjSession
	var start, end sql.NullInt64
	var tools, edited string
	dest := []any{&s.ID, &s.Profile, &s.JSONLPath, &s.CWD, &s.ProjectID, &start, &end, &s.Title,
		&s.Turns, &s.KB, &tools, &edited}
	if withReason {
		dest = append(dest, &s.Reason)
	}
	if err := rows.Scan(dest...); err != nil {
		return s, err
	}
	s.Start, s.End = fromUnix(start), fromUnix(end)
	s.Tools = map[string]int{}
	if tools != "" {
		_ = json.Unmarshal([]byte(tools), &s.Tools)
		if s.Tools == nil {
			s.Tools = map[string]int{}
		}
	}
	s.Edited = []string{}
	if edited != "" {
		_ = json.Unmarshal([]byte(edited), &s.Edited)
		if s.Edited == nil {
			s.Edited = []string{}
		}
	}
	return s, nil
}

// ProjSessionsPage returns up to limit sessions of a project, newest first, older than cur.
// A session belongs to the project when it ran there (project_id) or has a session_projects row.
func (d *DB) ProjSessionsPage(ctx context.Context, id string, cur ProjCursor, limit int) ([]ProjSession, error) {
	if limit <= 0 {
		limit = 50
	}
	const key = `COALESCE(s."end", s."start", 0)`
	q := `SELECT ` + projSessionColumns + `, COALESCE(sp.reason, 'cwd')
FROM sessions s
LEFT JOIN session_projects sp ON sp.session_id = s.id AND sp.project_id = ?
WHERE (s.project_id = ? OR sp.project_id IS NOT NULL)`
	args := []any{id, id}
	if cur.Set {
		q += ` AND (` + key + ` < ? OR (` + key + ` = ? AND s.id < ?))`
		args = append(args, cur.Unix, cur.Unix, cur.ID)
	}
	q += ` ORDER BY ` + key + ` DESC, s.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjSession{}
	for rows.Next() {
		s, err := projScanSession(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ProjSessionByID returns one session by its exact id. Reason is "cwd" when the session is
// assigned to a project and "" when it is not.
func (d *DB) ProjSessionByID(ctx context.Context, sessionID string) (ProjSession, bool, error) {
	rows, err := d.r.QueryContext(ctx, `SELECT `+projSessionColumns+` FROM sessions s WHERE s.id = ?`, sessionID)
	if err != nil {
		return ProjSession{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return ProjSession{}, false, rows.Err()
	}
	s, err := projScanSession(rows, false)
	if err != nil {
		return ProjSession{}, false, err
	}
	if s.ProjectID != "" {
		s.Reason = "cwd"
	}
	return s, true, nil
}

// ProjDoc is one document file of a project.
type ProjDoc struct {
	Path  string    `json:"path"`
	Title string    `json:"title"`
	MTime time.Time `json:"-"`
	Lines int       `json:"lines"`
}

// ProjDocs returns the files of kind "doc", ordered by path.
func (d *DB) ProjDocs(ctx context.Context, id string) ([]ProjDoc, error) {
	rows, err := d.r.QueryContext(ctx,
		`SELECT path, title, mtime, lines FROM files WHERE project_id = ? AND kind = 'doc' ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjDoc{}
	for rows.Next() {
		var f ProjDoc
		var mtime int64
		if err := rows.Scan(&f.Path, &f.Title, &mtime, &f.Lines); err != nil {
			return nil, err
		}
		if mtime != 0 {
			f.MTime = time.Unix(mtime, 0)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ProjNote is a note as the project pages show it. JSON names follow web/app/lib/types.ts.
type ProjNote struct {
	ID        string   `json:"id"`
	Ref       string   `json:"ref"`
	ProjectID string   `json:"projectId"`
	Text      string   `json:"text"`
	Author    string   `json:"author"`
	Date      string   `json:"date"` // as written in the note file
	Resolved  bool     `json:"resolved"`
	Line      *int     `json:"line,omitempty"`
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
	Anchor    string   `json:"anchor,omitempty"`
	ReplyTo   string   `json:"replyTo,omitempty"`
}

var projLineFragRe = regexp.MustCompile(`#L(\d+)`)

// ProjFileNotes returns the notes pinned on a file (ref "file:<path>" or "image:<path>", with or
// without a project prefix and a #fragment). Oldest first.
func (d *DB) ProjFileNotes(ctx context.Context, id, path string) ([]ProjNote, error) {
	var conds []string
	var args []any
	args = append(args, id)
	for _, prefix := range []string{"file:" + path, "image:" + path, id + ":file:" + path, id + ":image:" + path} {
		conds = append(conds, `ref = ?`, `ref LIKE ? ESCAPE '\'`)
		args = append(args, prefix, projLikeEscape(prefix)+`#%`)
	}
	rows, err := d.r.QueryContext(ctx, `
SELECT id, ref, author, created, resolved, line, x, y, anchor, reply_to, body
FROM notes WHERE project_id = ? AND (`+strings.Join(conds, " OR ")+`)
ORDER BY created, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjNote{}
	for rows.Next() {
		n := ProjNote{ProjectID: id}
		var resolved int
		var line sql.NullInt64
		var x, y sql.NullFloat64
		if err := rows.Scan(&n.ID, &n.Ref, &n.Author, &n.Date, &resolved, &line, &x, &y, &n.Anchor, &n.ReplyTo, &n.Text); err != nil {
			return nil, err
		}
		n.Resolved = resolved != 0
		if line.Valid {
			v := int(line.Int64)
			n.Line = &v
		} else if m := projLineFragRe.FindStringSubmatch(n.Ref); m != nil {
			v, _ := strconv.Atoi(m[1])
			n.Line = &v
		}
		if x.Valid {
			v := x.Float64
			n.X = &v
		}
		if y.Valid {
			v := y.Float64
			n.Y = &v
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ProjServerTitle is the page title of one server that runs now (servers_now).
type ProjServerTitle struct {
	Key   string
	Port  int
	Title string
}

// ProjServerTitles returns the titles of the servers that run now. Servers without a title are left out.
func (d *DB) ProjServerTitles(ctx context.Context) ([]ProjServerTitle, error) {
	rows, err := d.r.QueryContext(ctx, `SELECT key, port, title FROM servers_now WHERE title != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjServerTitle{}
	for rows.Next() {
		var t ProjServerTitle
		if err := rows.Scan(&t.Key, &t.Port, &t.Title); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
