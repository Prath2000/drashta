package indexer

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe/docs"
	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

// jobTree walks one project's files and stores them, with the docs.
// Hidden projects are never walked (a hidden venv can hold 31k entries).
// Documents are read again only when a doc file is new or its mtime changed.
// changed lists the project-relative paths that triggered the job (nil in a sweep).
// When backfill is not zero, the activity tracker back-fills file counts from mtimes.
func (i *Indexer) jobTree(ctx context.Context, id string, changed []string, backfill time.Time) {
	ps := i.state(id)
	if ps == nil || ps.p.Hidden {
		return
	}
	res, err := i.pr.Walk(ctx, ps.p.Path, i.c.Cfg.Skip)
	if err != nil {
		if ctx.Err() == nil {
			i.setStatus(id, "tree", err)
		}
		return
	}
	i.setStatus(id, "tree", nil)
	if res.Truncated {
		i.logOnce(id, "tree", errors.New("tree walk cut at the entry or depth cap"))
	}

	// Docs: reuse the ones whose mtime did not change.
	prev := ps.docs
	var toRead []tree.Entry
	newDocs := map[string]docs.Doc{}
	for _, e := range res.Entries {
		if e.Dir || e.Collapsed || !docs.IsDocPath(e.Path) {
			continue
		}
		if d, ok := prev[e.Path]; ok && d.MTime.Equal(e.MTime) {
			newDocs[e.Path] = d
			continue
		}
		toRead = append(toRead, e)
	}
	docsChanged := len(toRead) > 0 || len(prev) != len(newDocs)+len(toRead)
	if len(toRead) > 0 {
		for _, d := range i.pr.Docs(toRead, ps.p.Path) {
			newDocs[d.Path] = d
		}
	}

	files := make([]store.File, 0, len(res.Entries))
	var newest time.Time
	for _, e := range res.Entries {
		if e.Dir {
			continue
		}
		f := store.File{Path: e.Path, Size: e.Size, MTime: e.MTime, Kind: e.Kind}
		if d, ok := newDocs[e.Path]; ok {
			f.Kind = tree.KindDoc
			f.Title, f.Lines, f.Body = d.Title, d.Lines, d.Head
		}
		if f.Kind == "" {
			f.Kind = tree.KindOther
		}
		if e.MTime.After(newest) {
			newest = e.MTime
		}
		files = append(files, f)
	}

	i.mu.Lock()
	cur := i.projs[id]
	if cur == nil {
		i.mu.Unlock()
		return // removed while the walk ran (M11)
	}
	cur.docs = newDocs
	cur.docsKnown = true
	cur.walked = true
	if newest.After(cur.lastFiles) {
		cur.lastFiles = newest
	}
	row := i.projectRowLocked(cur)
	i.mu.Unlock()

	if err := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
		if !i.known(id) {
			return errProjectGone // removed meanwhile: no orphan rows (M11)
		}
		if err := store.ReplaceFilesTx(ctx, tx, id, files); err != nil {
			return err
		}
		return store.UpsertProjectTx(ctx, tx, row)
	}); err != nil {
		if ctx.Err() == nil && !errors.Is(err, errProjectGone) {
			i.setStatus(id, "tree", err)
		}
		return
	}

	if !backfill.IsZero() && i.c.Activity != nil {
		if err := i.c.Activity.Backfill(ctx, id, res.Entries, backfill); err != nil && ctx.Err() == nil {
			i.logOnce(id, "activity", err)
		}
	}

	i.publish(live.ProjectEvent(id, "files"))
	if docsChanged {
		i.publish(live.ProjectEvent(id, "docs"))
	}
	if len(changed) > 0 || !backfill.IsZero() {
		i.publish(live.ActivityEvent(id, i.dayOf(i.clock.Now())))
	}
}
