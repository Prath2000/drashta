package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ProjectList returns every project row, hidden ones too, ordered by group then name.
func (d *DB) ProjectList(ctx context.Context) ([]Project, error) {
	rows, err := d.r.QueryContext(ctx, `
SELECT id, name, grp, path, hidden, vcs, branch, remote, description, stack, readme_path,
       last_change_at, indexed_at
FROM projects ORDER BY grp, name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		var hidden int
		var stack string
		var last, idx sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &p.Group, &p.Path, &hidden, &p.VCS, &p.Branch, &p.Remote,
			&p.Description, &stack, &p.ReadmePath, &last, &idx); err != nil {
			return nil, err
		}
		p.Hidden = hidden != 0
		p.Stack = []string{}
		if stack != "" {
			_ = json.Unmarshal([]byte(stack), &p.Stack) // a bad value just leaves the empty list
			if p.Stack == nil {
				p.Stack = []string{}
			}
		}
		p.LastChangeAt = fromUnix(last)
		p.IndexedAt = fromUnix(idx)
		out = append(out, p)
	}
	return out, rows.Err()
}
