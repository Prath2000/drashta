package store

import (
	"context"
	"database/sql"

	"github.com/Prath2000/drashta/internal/probe"
)

// ReplaceDirty makes the dirty (uncommitted) paths of a project equal to the given list.
func (d *DB) ReplaceDirty(ctx context.Context, project string, files []probe.DirtyFile) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return ReplaceDirtyTx(ctx, tx, project, files) })
}

// ReplaceDirtyTx is ReplaceDirty inside a transaction you already have.
func ReplaceDirtyTx(ctx context.Context, tx *sql.Tx, project string, files []probe.DirtyFile) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM dirty WHERE project_id = ?`, project); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO dirty(project_id, path, state) VALUES(?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, f := range files {
		if _, err := ins.ExecContext(ctx, project, f.Path, f.State); err != nil {
			return err
		}
	}
	return nil
}
