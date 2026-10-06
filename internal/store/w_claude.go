package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ClaudeFile is the parse state of one .jsonl file (table claude_file, 03 section 3).
//
//   - same Size and MTime as on disk: skip the file;
//   - bigger, same Inode: read from Offset;
//   - smaller, or a new Inode: read from 0.
type ClaudeFile struct {
	Path   string
	Size   int64
	MTime  time.Time // saved with nanosecond precision
	Offset int64
	Inode  uint64
}

// GetClaudeFile returns the saved state of a file. ok is false when there is none.
func (d *DB) GetClaudeFile(ctx context.Context, path string) (cf ClaudeFile, ok bool, err error) {
	var mtime, inode int64
	err = d.r.QueryRowContext(ctx,
		`SELECT path, size, mtime, "offset", inode FROM claude_file WHERE path = ?`, path).
		Scan(&cf.Path, &cf.Size, &mtime, &cf.Offset, &inode)
	if errors.Is(err, sql.ErrNoRows) {
		return ClaudeFile{}, false, nil
	}
	if err != nil {
		return ClaudeFile{}, false, err
	}
	cf.MTime = time.Unix(0, mtime)
	cf.Inode = uint64(inode)
	return cf, true, nil
}

// PutClaudeFile saves the state of a file (insert or update).
func (d *DB) PutClaudeFile(ctx context.Context, cf ClaudeFile) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return PutClaudeFileTx(ctx, tx, cf) })
}

// PutClaudeFileTx is PutClaudeFile inside a transaction you already have.
// Do it in the same transaction as UpsertSessions, so the offset never runs ahead of the data.
func PutClaudeFileTx(ctx context.Context, tx *sql.Tx, cf ClaudeFile) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO claude_file(path, size, mtime, "offset", inode) VALUES(?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
  size = excluded.size, mtime = excluded.mtime, "offset" = excluded."offset", inode = excluded.inode`,
		cf.Path, cf.Size, cf.MTime.UnixNano(), cf.Offset, int64(cf.Inode))
	return err
}

// DeleteClaudeFile forgets a file (it was deleted on disk).
func (d *DB) DeleteClaudeFile(ctx context.Context, path string) error {
	return d.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM claude_file WHERE path = ?`, path)
		return err
	})
}
