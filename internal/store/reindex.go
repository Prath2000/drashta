package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// Reindex drops and recreates every cache (R) table and the search table, in ONE transaction.
// History (H) tables are not touched. See tables.go for the two lists.
//
// The CREATE statements are read from the database itself (sqlite_master), so the tables come back
// exactly as the migrations made them, with no second copy of the schema in Go.
//
// After Reindex the cache is empty. The caller then runs a full sweep to fill it again.
// Because claude_file is a cache table, every .jsonl file is parsed again from the start.
// Events of kind server_up, server_down and file_burst were in the dropped events table.
// They can be derived again from the history tables if the UI needs them.
func (d *DB) Reindex(ctx context.Context) error {
	return d.Write(ctx, func(tx *sql.Tx) error { return reindexTx(ctx, tx) })
}

func reindexTx(ctx context.Context, tx *sql.Tx) error {
	isR := make(map[string]bool, len(rTables))
	for _, t := range rTables {
		isR[t] = true
	}

	// Collect the CREATE statements first. Rows are closed before any DROP runs.
	rows, err := tx.QueryContext(ctx,
		`SELECT type, name, tbl_name, sql FROM sqlite_master
		 WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		 ORDER BY rowid`)
	if err != nil {
		return err
	}
	var tableSQL, indexSQL []string
	found := map[string]bool{}
	for rows.Next() {
		var typ, name, tbl, stmt string
		if err := rows.Scan(&typ, &name, &tbl, &stmt); err != nil {
			rows.Close()
			return err
		}
		if !isR[tbl] {
			continue // a history table, or an FTS shadow table (they go with their FTS table)
		}
		switch typ {
		case "table":
			if isR[name] {
				found[name] = true
				tableSQL = append(tableSQL, stmt)
			}
		case "index", "trigger":
			indexSQL = append(indexSQL, stmt)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, t := range rTables {
		if !found[t] {
			return fmt.Errorf("store: reindex: cache table %q is missing from the database", t)
		}
	}

	// Drop. FTS first: dropping it also removes its shadow tables.
	drop := append([]string{"search"}, rTables...)
	done := map[string]bool{}
	for _, t := range drop {
		if done[t] {
			continue
		}
		done[t] = true
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS "`+t+`"`); err != nil {
			return fmt.Errorf("store: reindex: drop %s: %w", t, err)
		}
	}
	// Create again: tables, then indexes.
	for _, stmt := range tableSQL {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: reindex: %w", err)
		}
	}
	for _, stmt := range indexSQL {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: reindex: %w", err)
		}
	}

	// meta is a cache table. Put the schema version back.
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES('schema_version', ?)`, strconv.Itoa(version))
	return err
}
