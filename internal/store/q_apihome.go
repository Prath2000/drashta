package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Queries for the HTTP handlers of the home area: GET /boot, /inbox, /search and PATCH /settings.
// Every exported name here starts with "Home" so it cannot clash with other q_*.go files.

// ---------------------------------------------------------------------------
// boot

// HomeBootProject is a project row plus the two numbers GET /boot adds to it.
type HomeBootProject struct {
	Project
	DirtyCount int  // number of uncommitted paths (rows in dirty)
	Surveyed   bool // false until the first index of the project has finished (indexed_at is set)
	// FileCount is the number of rows in files. HasFileCount is false when the number means nothing:
	// a project that was never walked (not surveyed, or hidden with no files) has 0 rows but is not empty.
	FileCount    int
	HasFileCount bool
}

// HomeBootProjects returns every project (hidden ones too), ordered by group then name.
func (d *DB) HomeBootProjects(ctx context.Context) ([]HomeBootProject, error) {
	list, err := d.ProjectList(ctx)
	if err != nil {
		return nil, err
	}
	dirty := map[string]int{}
	rows, err := d.r.QueryContext(ctx, `SELECT project_id, COUNT(*) FROM dirty GROUP BY project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		dirty[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	files := map[string]int{}
	frows, err := d.r.QueryContext(ctx, `SELECT project_id, COUNT(*) FROM files GROUP BY project_id`)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var id string
		var n int
		if err := frows.Scan(&id, &n); err != nil {
			return nil, err
		}
		files[id] = n
	}
	if err := frows.Err(); err != nil {
		return nil, err
	}
	out := make([]HomeBootProject, 0, len(list))
	for _, p := range list {
		bp := HomeBootProject{Project: p, DirtyCount: dirty[p.ID], Surveyed: !p.IndexedAt.IsZero(), FileCount: files[p.ID]}
		// 0 files is a real answer only for a visible project whose first index has finished.
		bp.HasFileCount = bp.FileCount > 0 || (bp.Surveyed && !p.Hidden)
		out = append(out, bp)
	}
	return out, nil
}

// HomeCounts are the four numbers in the top bar.
type HomeCounts struct {
	LiveServers    int `json:"liveServers"`
	OpenTasks      int `json:"openTasks"`
	AwaitingReview int `json:"awaitingReview"`
	OpenNotes      int `json:"openNotes"`
}

// HomeBootCounts counts, in one statement:
//   - liveServers: listening dev servers (kind dev)
//   - openTasks: tasks that are not done, in projects that are not hidden
//     (the tasks GET /api/tracker lists, minus the done ones)
//   - awaitingReview: the tasks GET /api/tracker?filter=review returns: tasks of projects
//     that are not hidden whose status is review, or that are not done and whose newest
//     snapshot is still pending (httpapi trkAwaitingReview, the same rule) (I10)
//   - openNotes: notes that are not resolved
//
// GET /api/boot, the SSE hello and counts events (indexer.Counts -> IdxCounts) all use this,
// so the numbers always agree with each other and with the Tracker.
func (d *DB) HomeBootCounts(ctx context.Context) (HomeCounts, error) {
	var c HomeCounts
	err := d.r.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*) FROM servers_now WHERE kind = 'dev'),
  (SELECT COUNT(*) FROM tasks t JOIN projects p ON p.id = t.project_id AND p.hidden = 0
     WHERE t.status <> 'done'),
  (SELECT COUNT(*) FROM tasks t JOIN projects p ON p.id = t.project_id AND p.hidden = 0
     WHERE t.status = 'review'
        OR (t.status <> 'done' AND EXISTS (
              SELECT 1 FROM snapshots s
              WHERE s.project_id = t.project_id AND s.task_id = t.id AND s.verdict = 'pending'
                AND s.version = (SELECT MAX(s2.version) FROM snapshots s2
                                 WHERE s2.project_id = t.project_id AND s2.task_id = t.id)))),
  (SELECT COUNT(*) FROM notes WHERE resolved = 0)`).
		Scan(&c.LiveServers, &c.OpenTasks, &c.AwaitingReview, &c.OpenNotes)
	return c, err
}

// HomeLastScan returns the time of the last finished sweep: the newest ok row of sweep_log, or
// meta.last_sweep_at (unix seconds or RFC 3339), whichever is later. Zero time before the first sweep.
func (d *DB) HomeLastScan(ctx context.Context) (time.Time, error) {
	var ts sql.NullInt64
	if err := d.r.QueryRowContext(ctx, `SELECT MAX(ts) FROM sweep_log WHERE ok = 1`).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	best := fromUnix(ts)
	if v, ok, err := d.GetMeta(ctx, "last_sweep_at"); err == nil && ok {
		var t time.Time
		if n, perr := strconv.ParseInt(strings.TrimSpace(v), 10, 64); perr == nil && n > 0 {
			t = time.Unix(n, 0)
		} else if pt, perr := time.Parse(time.RFC3339Nano, strings.TrimSpace(v)); perr == nil {
			t = pt
		}
		if t.After(best) {
			best = t
		}
	}
	return best, nil
}

// HomeProjectExists reports whether a project id is known.
func (d *DB) HomeProjectExists(ctx context.Context, id string) (bool, error) {
	var one int
	err := d.r.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// HomeHiddenIDs returns the ids of hidden projects.
func (d *DB) HomeHiddenIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := d.r.QueryContext(ctx, `SELECT id FROM projects WHERE hidden <> 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// HomeHiddenChange is a project whose hidden flag changed.
type HomeHiddenChange struct {
	ID     string
	Hidden bool // the new value
}

// HomeApplyHidden sets projects.hidden for every project from the rule (true = hidden) and returns
// the projects that changed. It writes only the rows that differ.
func (d *DB) HomeApplyHidden(ctx context.Context, hide func(group, name string) bool) ([]HomeHiddenChange, error) {
	var changed []HomeHiddenChange
	err := d.Write(ctx, func(tx *sql.Tx) error {
		changed = nil
		rows, err := tx.QueryContext(ctx, `SELECT id, grp, name, hidden FROM projects`)
		if err != nil {
			return err
		}
		var todo []HomeHiddenChange
		for rows.Next() {
			var id, grp, name string
			var cur int
			if err := rows.Scan(&id, &grp, &name, &cur); err != nil {
				rows.Close()
				return err
			}
			want := hide(grp, name)
			if want != (cur != 0) {
				todo = append(todo, HomeHiddenChange{ID: id, Hidden: want})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, c := range todo {
			if _, err := tx.ExecContext(ctx, `UPDATE projects SET hidden = ? WHERE id = ?`, b2i(c.Hidden), c.ID); err != nil {
				return err
			}
		}
		changed = todo
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].ID < changed[j].ID })
	return changed, nil
}

// ---------------------------------------------------------------------------
// inbox

// HomeInboxNotes returns notes for the inbox, newest first (by the created time, then by the
// number in the id). filter is open, resolved or all. offset and limit page the result.
func (d *DB) HomeInboxNotes(ctx context.Context, filter string, offset, limit int) ([]Note, error) {
	where := ""
	switch filter {
	case "open":
		where = "WHERE resolved = 0"
	case "resolved":
		where = "WHERE resolved <> 0"
	case "all":
	default:
		return nil, fmt.Errorf("store: bad inbox filter %q", filter)
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.r.QueryContext(ctx, `
SELECT project_id, id, ref, x, y, line, anchor, author, created, resolved, resolved_at, reply_to, body, file_rev
FROM notes `+where+`
ORDER BY unixepoch(created) DESC, CAST(substr(id, 3) AS INTEGER) DESC, project_id, id
LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		var n Note
		var x, y sql.NullFloat64
		var line sql.NullInt64
		var resolved int
		if err := rows.Scan(&n.ProjectID, &n.ID, &n.Ref, &x, &y, &line, &n.Anchor, &n.Author, &n.Created,
			&resolved, &n.ResolvedAt, &n.ReplyTo, &n.Body, &n.FileRev); err != nil {
			return nil, err
		}
		if x.Valid {
			v := x.Float64
			n.X = &v
		}
		if y.Valid {
			v := y.Float64
			n.Y = &v
		}
		if line.Valid {
			v := int(line.Int64)
			n.Line = &v
		}
		n.Resolved = resolved != 0
		out = append(out, n)
	}
	return out, rows.Err()
}

// homeFeedKinds are the event kinds that belong in the inbox activity feed.
// Server and file-burst events are left out: they are noise in a feed. A session is shown once, by its start.
const homeFeedKinds = `'commit','session_start','snapshot','verdict','task_created','task_status','note'`

// HomeInboxFeed returns the activity feed of all projects that are not hidden, newest first.
// The cursor is the (ts, id) of the last event of the previous page. A zero beforeTS means the first page.
func (d *DB) HomeInboxFeed(ctx context.Context, beforeTS, beforeID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.r.QueryContext(ctx, `
SELECT e.id, e.project_id, e.ts, e.day, e.kind, e.ref, e.label, e.meta
FROM events e JOIN projects p ON p.id = e.project_id AND p.hidden = 0
WHERE e.kind IN (`+homeFeedKinds+`) AND e.ts > 0
  AND (? = 0 OR e.ts < ? OR (e.ts = ? AND e.id < ?))
ORDER BY e.ts DESC, e.id DESC
LIMIT ?`, beforeTS, beforeTS, beforeTS, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var ts int64
		var meta string
		if err := rows.Scan(&e.ID, &e.ProjectID, &ts, &e.Day, &e.Kind, &e.Ref, &e.Label, &meta); err != nil {
			return nil, err
		}
		e.TS = time.Unix(ts, 0)
		if meta != "" && meta != "{}" {
			e.Meta = []byte(meta)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HomeSnapshotVerdict returns the current verdict (pending, approved, changes) of one snapshot,
// or "" when there is no such snapshot.
func (d *DB) HomeSnapshotVerdict(ctx context.Context, project, task string, version int) (string, error) {
	var v string
	err := d.r.QueryRowContext(ctx,
		`SELECT verdict FROM snapshots WHERE project_id = ? AND task_id = ? AND version = ?`, project, task, version).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// HomeHasTask reports whether a task exists in the tracker mirror.
func (d *DB) HomeHasTask(ctx context.Context, project, id string) (bool, error) {
	var one int
	err := d.r.QueryRowContext(ctx, `SELECT 1 FROM tasks WHERE project_id = ? AND id = ?`, project, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// HomeProposal is a suggestion to move a task to done because a commit says it fixes or closes it.
type HomeProposal struct {
	ProjectID string
	TaskID    string
	CommitID  string
	CommitTS  time.Time
}

var (
	// "Fixes T-12", "closed: T-12", "Resolves task:T-12, T-14 and T-15". 04 section 3.
	homeCloseRe = regexp.MustCompile(`(?i)\b(?:fix(?:es|ed)?|clos(?:e|es|ed)|resolv(?:e|es|ed))\b[:\s]+` +
		`((?:task:)?t-\d+(?:\s*(?:,|and|&)\s*(?:task:)?t-\d+)*)`)
	homeTaskIDRe = regexp.MustCompile(`(?i)\bt-(\d+)\b`)
)

// homeMaxProposalScan is how many recent commits HomeInboxProposals looks at.
const homeMaxProposalScan = 2000

// HomeInboxProposals finds commits since "since" whose message or body says "Fixes T-n" or "Closes T-n"
// for a task that exists and is not done. One proposal per task, from its newest closing commit.
// If the task changed after the commit, the user has already decided, so nothing is proposed.
// Commits of hidden projects are skipped. The result is newest first and at most max long.
func (d *DB) HomeInboxProposals(ctx context.Context, since time.Time, max int) ([]HomeProposal, error) {
	if max <= 0 {
		max = 50
	}
	rows, err := d.r.QueryContext(ctx, `
SELECT c.project_id, c.id, c.msg, c.body, c.ts
FROM commits c JOIN projects p ON p.id = c.project_id AND p.hidden = 0
WHERE c.ts >= ? AND (c.msg LIKE '%t-%' OR c.body LIKE '%t-%')
ORDER BY c.ts DESC LIMIT ?`, unixOrZero(since), homeMaxProposalScan)
	if err != nil {
		return nil, err
	}
	type cand struct {
		project, commit string
		ts              int64
		tasks           []string
	}
	var cands []cand
	for rows.Next() {
		var project, id, msg, body string
		var ts int64
		if err := rows.Scan(&project, &id, &msg, &body, &ts); err != nil {
			rows.Close()
			return nil, err
		}
		var ids []string
		for _, m := range homeCloseRe.FindAllStringSubmatch(msg+"\n"+body, -1) {
			for _, t := range homeTaskIDRe.FindAllStringSubmatch(m[1], -1) {
				ids = append(ids, "T-"+t[1])
			}
		}
		if len(ids) > 0 {
			cands = append(cands, cand{project: project, commit: id, ts: ts, tasks: ids})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := []HomeProposal{}
	seen := map[string]bool{}
	for _, c := range cands { // newest commit first
		for _, tid := range c.tasks {
			key := c.project + "\x00" + tid
			if seen[key] {
				continue
			}
			seen[key] = true
			var status, updated, created string
			err := d.r.QueryRowContext(ctx,
				`SELECT status, updated, created FROM tasks WHERE project_id = ? AND id = ?`, c.project, tid).
				Scan(&status, &updated, &created)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if status == "done" {
				continue
			}
			last := parseTime(updated)
			if last.IsZero() {
				last = parseTime(created)
			}
			if !last.IsZero() && last.Unix() > c.ts {
				continue
			}
			out = append(out, HomeProposal{ProjectID: c.project, TaskID: tid, CommitID: c.commit, CommitTS: time.Unix(c.ts, 0)})
			if len(out) >= max {
				return out, nil
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// search extras
//
// store.Search covers the FTS kinds (file, doc, commit, session, task, feature, milestone, note).
// The search palette (02-ui-spec section 2.2) also lists projects, snapshots and servers. They are
// not in the FTS table, so HomeSearchExtras finds them with LIKE.

const homeMinSearchRunes = 3

// homeLikeEsc escapes the LIKE wildcards of s. Use it with ESCAPE '\'.
func homeLikeEsc(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// HomeSearchExtras finds projects, snapshots and servers for the search palette. q is what the user typed.
// A leading colon (":4455") searches server ports only. kind and project are optional filters; a kind
// other than project, snapshot or server gives no hits. Refs are local ("project:ledger", "server:4455",
// "snapshot:T-1/v3"). Texts shorter than 3 characters give no hits.
func (d *DB) HomeSearchExtras(ctx context.Context, q, project, kind string, limit int) ([]Hit, error) {
	out := []Hit{}
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) < homeMinSearchRunes {
		return out, nil
	}
	if utf8.RuneCountInString(q) > maxQueryRunes {
		q = string([]rune(q)[:maxQueryRunes])
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	// Port search: only digits after the colon, only servers.
	if strings.HasPrefix(q, ":") {
		digits := q[1:]
		if digits == "" || strings.Trim(digits, "0123456789") != "" {
			return out, nil
		}
		if kind != "" && kind != "server" {
			return out, nil
		}
		return d.homeServerHits(ctx, "CAST(port AS TEXT) LIKE ?", []any{"%" + digits + "%"}, project, limit)
	}

	pat := "%" + homeLikeEsc(q) + "%"
	if kind == "" || kind == "project" {
		query := `SELECT id, name, description, path, last_change_at FROM projects
WHERE (name LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\' OR stack LIKE ? ESCAPE '\')`
		args := []any{pat, pat, pat, pat}
		if project != "" {
			query += ` AND id = ?`
			args = append(args, project)
		}
		query += ` ORDER BY COALESCE(last_change_at, 0) DESC, id LIMIT ?`
		args = append(args, limit)
		rows, err := d.r.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, name, desc, path string
			var last sql.NullInt64
			if err := rows.Scan(&id, &name, &desc, &path, &last); err != nil {
				rows.Close()
				return nil, err
			}
			snippet := desc
			if snippet == "" {
				snippet = path
			}
			out = append(out, Hit{Kind: "project", Ref: "project:" + id, ProjectID: id, Title: name,
				Snippet: snippet, TS: fromUnix(last)})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if kind == "" || kind == "snapshot" {
		query := `SELECT project_id, task_id, version, title, image_path, verdict, captured FROM snapshots
WHERE (title LIKE ? ESCAPE '\' OR image_path LIKE ? ESCAPE '\' OR review_note LIKE ? ESCAPE '\')`
		args := []any{pat, pat, pat}
		if project != "" {
			query += ` AND project_id = ?`
			args = append(args, project)
		}
		query += ` ORDER BY project_id, task_id, version DESC LIMIT ?`
		args = append(args, limit)
		rows, err := d.r.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var pid, task, title, img, verdict, captured string
			var version int
			if err := rows.Scan(&pid, &task, &version, &title, &img, &verdict, &captured); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, Hit{Kind: "snapshot", Ref: "snapshot:" + task + "/v" + strconv.Itoa(version), ProjectID: pid,
				Title: title + " · v" + strconv.Itoa(version), Snippet: verdict + " · " + img, TS: parseTime(captured)})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if kind == "" || kind == "server" {
		hits, err := d.homeServerHits(ctx,
			`(CAST(port AS TEXT) LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\' OR cmd LIKE ? ESCAPE '\')`,
			[]any{pat, pat, pat, pat}, project, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	return out, nil
}

// homeServerHits reads live servers that belong to a project and match the given WHERE part.
func (d *DB) homeServerHits(ctx context.Context, where string, args []any, project string, limit int) ([]Hit, error) {
	query := `SELECT project_id, port, name, cmd, title, last_seen FROM servers_now WHERE project_id <> '' AND ` + where
	if project != "" {
		query += ` AND project_id = ?`
		args = append(args, project)
	}
	query += ` ORDER BY port LIMIT ?`
	args = append(args, limit)
	rows, err := d.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Hit{}
	for rows.Next() {
		var pid, name, cmd, title string
		var port int
		var seen sql.NullInt64
		if err := rows.Scan(&pid, &port, &name, &cmd, &title, &seen); err != nil {
			return nil, err
		}
		label := title
		if label == "" {
			label = name
		}
		out = append(out, Hit{Kind: "server", Ref: "server:" + strconv.Itoa(port), ProjectID: pid,
			Title: ":" + strconv.Itoa(port) + " · " + label, Snippet: cmd, TS: fromUnix(seen)})
	}
	return out, rows.Err()
}
