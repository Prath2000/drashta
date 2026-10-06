package store

import (
	"context"
	"database/sql"
	"time"
)

// File is one row of the files table. File bodies are never stored.
type File struct {
	Path  string
	Size  int64
	MTime time.Time
	Kind  string // code | doc | image | other
	Lines int
	Title string // docs: the first H1
	// Body is optional. It is only for Kind "doc": the first part of the text, used for search.
	// It is not saved in the files table. It is cut to MaxBody bytes.
	Body string
}

// ReplaceFiles makes the files of a project equal to the given list.
//
// Search rows follow in the same transaction:
//   - every file gets a "file" row (title = path), or a "doc" row (title = path, body = title + text)
//     when Kind is "doc" and Body is not empty;
//   - rows are only rewritten for files that are new, whose mtime changed, or whose row kind changed.
//     A big tree with a few changed files is therefore cheap;
//   - rows of files that are gone are deleted.
func (d *DB) ReplaceFiles(ctx context.Context, project string, files []File) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return ReplaceFilesTx(ctx, tx, project, files) })
}

// ReplaceFilesTx is ReplaceFiles inside a transaction you already have.
func ReplaceFilesTx(ctx context.Context, tx *sql.Tx, project string, files []File) error {
	// What we had before: mtime per path.
	old := map[string]int64{}
	rows, err := tx.QueryContext(ctx, `SELECT path, mtime FROM files WHERE project_id = ?`, project)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p string
		var m int64
		if err := rows.Scan(&p, &m); err != nil {
			rows.Close()
			return err
		}
		old[p] = m
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	f, err := newFTSWriter(ctx, tx)
	if err != nil {
		return err
	}
	defer f.close()

	// What search rows exist: ref -> kind. A path can only have one of "file" or "doc".
	haveKind := map[string]string{}
	for _, kind := range []string{"file", "doc"} {
		refs, err := f.refs(kind, project)
		if err != nil {
			return err
		}
		for _, r := range refs {
			haveKind[r] = kind
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE project_id = ?`, project); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO files(project_id, path, size, mtime, kind, lines, title) VALUES(?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	keep := make(map[string]bool, len(files))
	for _, fl := range files {
		mtime := unixOrZero(fl.MTime)
		kind := fl.Kind
		if kind == "" {
			kind = "other"
		}
		if _, err := ins.ExecContext(ctx, project, fl.Path, fl.Size, mtime, kind, fl.Lines, fl.Title); err != nil {
			return err
		}

		ref := "file:" + fl.Path
		keep[ref] = true
		wantKind := "file"
		if kind == "doc" && fl.Body != "" {
			wantKind = "doc"
		}
		prevMTime, existed := old[fl.Path]
		if haveKind[ref] == wantKind && existed && prevMTime == mtime {
			continue // nothing changed for search
		}
		if prev := haveKind[ref]; prev != "" && prev != wantKind {
			if err := f.del(prev, project, ref); err != nil {
				return err
			}
		}
		title, body := fl.Path, ""
		if wantKind == "doc" {
			body = fl.Title + "\n" + fl.Body
		}
		if err := f.put(wantKind, project, ref, title, body, mtime); err != nil {
			return err
		}
	}

	// Search rows of files that are gone.
	for ref, kind := range haveKind {
		if !keep[ref] {
			if err := f.del(kind, project, ref); err != nil {
				return err
			}
		}
	}
	return nil
}
