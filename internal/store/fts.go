package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The search index is one FTS5 table, "search", with the trigram tokenizer (04 section 5).
// Trigram means a word matches in the middle too: "iscove" finds "discover.go".
//
// One row in "search" belongs to one (kind, project_id, ref). The helper table "search_ref"
// remembers the FTS rowid for that key, so replacing or deleting one row is fast
// (no scan of the whole FTS table).
//
// Kinds in use: file, doc, commit, session, task, feature, milestone, note.
// ref is the local ref string, for example "file:src/x.go", "commit:abc1234", "task:T-12".

// Hit is one search result.
type Hit struct {
	Kind      string    `json:"kind"`
	Ref       string    `json:"ref"`
	ProjectID string    `json:"projectId"`
	Title     string    `json:"title"`
	Snippet   string    `json:"snippet"`
	TS        time.Time `json:"ts"`
}

// ftsWriter holds prepared statements for FTS changes inside one transaction.
type ftsWriter struct {
	ctx                 context.Context
	tx                  *sql.Tx
	sel, delRow, ins    *sql.Stmt
	delMap, putMap, all *sql.Stmt
}

func newFTSWriter(ctx context.Context, tx *sql.Tx) (*ftsWriter, error) {
	f := &ftsWriter{ctx: ctx, tx: tx}
	var err error
	prep := func(dst **sql.Stmt, q string) {
		if err != nil {
			return
		}
		*dst, err = tx.PrepareContext(ctx, q)
	}
	prep(&f.sel, `SELECT fts_rowid FROM search_ref WHERE kind = ? AND project_id = ? AND ref = ?`)
	prep(&f.delRow, `DELETE FROM search WHERE rowid = ?`)
	prep(&f.ins, `INSERT INTO search(rowid, title, body, kind, project_id, ref, ts) VALUES(?, ?, ?, ?, ?, ?, ?)`)
	prep(&f.delMap, `DELETE FROM search_ref WHERE kind = ? AND project_id = ? AND ref = ?`)
	prep(&f.putMap, `INSERT OR REPLACE INTO search_ref(kind, project_id, ref, fts_rowid) VALUES(?, ?, ?, ?)`)
	prep(&f.all, `SELECT ref, fts_rowid FROM search_ref WHERE kind = ? AND project_id = ?`)
	if err != nil {
		f.close()
		return nil, err
	}
	return f, nil
}

func (f *ftsWriter) close() {
	for _, s := range []*sql.Stmt{f.sel, f.delRow, f.ins, f.delMap, f.putMap, f.all} {
		if s != nil {
			_ = s.Close()
		}
	}
}

// put inserts or replaces the row for (kind, project, ref).
func (f *ftsWriter) put(kind, project, ref, title, body string, ts int64) error {
	body = clip(body, MaxBody)
	var rowid int64
	err := f.sel.QueryRowContext(f.ctx, kind, project, ref).Scan(&rowid)
	switch {
	case err == nil:
		if _, err := f.delRow.ExecContext(f.ctx, rowid); err != nil {
			return err
		}
		_, err := f.ins.ExecContext(f.ctx, rowid, title, body, kind, project, ref, ts)
		return err
	case errors.Is(err, sql.ErrNoRows):
		res, err := f.ins.ExecContext(f.ctx, nil, title, body, kind, project, ref, ts)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = f.putMap.ExecContext(f.ctx, kind, project, ref, id)
		return err
	default:
		return err
	}
}

// del removes the row for (kind, project, ref), if there is one.
func (f *ftsWriter) del(kind, project, ref string) error {
	var rowid int64
	err := f.sel.QueryRowContext(f.ctx, kind, project, ref).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.delRow.ExecContext(f.ctx, rowid); err != nil {
		return err
	}
	_, err = f.delMap.ExecContext(f.ctx, kind, project, ref)
	return err
}

// refs lists the refs that have a row for (kind, project).
func (f *ftsWriter) refs(kind, project string) ([]string, error) {
	rows, err := f.all.QueryContext(f.ctx, kind, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ref string
		var rowid int64
		if err := rows.Scan(&ref, &rowid); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// FTSReplaceTx adds or replaces the search row for (kind, project, ref) inside tx.
func FTSReplaceTx(ctx context.Context, tx *sql.Tx, kind, ref, project, title, body string, ts time.Time) error {
	f, err := newFTSWriter(ctx, tx)
	if err != nil {
		return err
	}
	defer f.close()
	return f.put(kind, project, ref, title, body, unixOrZero(ts))
}

// FTSDeleteTx removes the search row for (kind, project, ref) inside tx.
func FTSDeleteTx(ctx context.Context, tx *sql.Tx, kind, ref, project string) error {
	f, err := newFTSWriter(ctx, tx)
	if err != nil {
		return err
	}
	defer f.close()
	return f.del(kind, project, ref)
}

// FTSReplace adds or replaces the search row for (kind, project, ref).
// Body is cut to MaxBody bytes. The typed helpers (ReplaceCommits and so on) call this for you.
func (d *DB) FTSReplace(ctx context.Context, kind, ref, project, title, body string, ts time.Time) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		return FTSReplaceTx(ctx, tx, kind, ref, project, title, body, ts)
	})
}

// FTSDelete removes the search row for (kind, project, ref). A missing row is not an error.
func (d *DB) FTSDelete(ctx context.Context, kind, ref, project string) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		return FTSDeleteTx(ctx, tx, kind, ref, project)
	})
}

// ---------------------------------------------------------------------------
// query side

const (
	minQueryRunes = 3 // trigram cannot match anything shorter
	maxQueryRunes = 200
	maxQueryWords = 8
	defaultLimit  = 30
	maxLimit      = 200
)

// MatchExpr turns what the user typed into a safe FTS5 MATCH expression.
//
// Rules:
//   - The text is split on spaces. Double quotes and control characters are dropped.
//   - Each word is wrapped in double quotes, so FTS5 treats it as plain text.
//     "OR", "NEAR(", "*", "-x" and "col:x" are just text, never syntax.
//   - A word shorter than 3 characters cannot match with the trigram tokenizer, so it is dropped.
//   - The words are joined with a space, which means AND.
//   - If no word is left, the result is "" and the caller must return no hits.
func MatchExpr(q string) string {
	if utf8.RuneCountInString(q) > maxQueryRunes {
		q = string([]rune(q)[:maxQueryRunes])
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r == '"':
			return ' '
		case unicode.IsControl(r), r == utf8.RuneError:
			return ' '
		}
		return r
	}, q)

	seen := map[string]bool{}
	var words []string
	for _, w := range strings.Fields(clean) {
		if utf8.RuneCountInString(w) < minQueryRunes {
			continue
		}
		key := strings.ToLower(w)
		if seen[key] {
			continue
		}
		seen[key] = true
		words = append(words, `"`+w+`"`)
		if len(words) == maxQueryWords {
			break
		}
	}
	return strings.Join(words, " ")
}

// Search finds rows with MATCH, ranks them with bm25 (a title match counts 4x a body match),
// and puts the newest first when two rows have the same score.
//
// project and kind are optional filters ("" = all). limit is 30 when <= 0 and at most 200.
// Any text is safe to pass: bad or too-short queries give an empty list, never an error.
func (d *DB) Search(ctx context.Context, q, project, kind string, limit int) ([]Hit, error) {
	expr := MatchExpr(q)
	if expr == "" {
		return []Hit{}, nil
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	query := `SELECT kind, ref, project_id, title, snippet(search, -1, '', '', '...', 12), ts,
	                 bm25(search, 4.0, 1.0) AS score
	          FROM search WHERE search MATCH ?`
	args := []any{expr}
	if project != "" {
		query += ` AND project_id = ?`
		args = append(args, project)
	}
	if kind != "" {
		query += ` AND kind = ?`
		args = append(args, kind)
	}
	// bm25 is lower for a better match. Rows with exactly the same score come newest first.
	query += ` ORDER BY score, ts DESC LIMIT ?`
	args = append(args, limit)

	rows, err := d.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []Hit{}
	for rows.Next() {
		var h Hit
		var ts sql.NullInt64
		var score float64
		if err := rows.Scan(&h.Kind, &h.Ref, &h.ProjectID, &h.Title, &h.Snippet, &ts, &score); err != nil {
			return nil, err
		}
		h.TS = fromUnix(ts)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}
