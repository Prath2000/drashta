package indexer

import (
	"context"
	"database/sql"
	"path/filepath"

	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/claude"
	"github.com/Prath2000/drashta/internal/probe/projects"
	"github.com/Prath2000/drashta/internal/store"
)

// jobProjects discovers the project folders, describes them and stores them.
// New projects and projects that were just un-hidden get a full scan.
// Projects that are gone are removed, but only when discovery had no error
// (a permission problem must not wipe the index).
func (i *Indexer) jobProjects(ctx context.Context) {
	defer i.projReadyOnce.Do(func() { close(i.projReady) })
	list, err := i.pr.Discover(i.c.Cfg)
	complete := err == nil
	if err != nil {
		i.logOnce("", "projects", err)
	}
	infos := make(map[string]projects.Info, len(list))
	for _, p := range list {
		infos[p.ID] = i.pr.Describe(p.Path)
	}

	var added, unhidden, removed []string
	var rows []store.Project
	i.mu.Lock()
	seen := map[string]bool{}
	for _, p := range list {
		seen[p.ID] = true
		ps := i.projs[p.ID]
		if ps == nil {
			ps = &projState{commits: map[string][]probe.Commit{}, dirty: map[string][]probe.DirtyFile{},
				status: map[string]PartStatus{}}
			i.projs[p.ID] = ps
			added = append(added, p.ID)
		} else if ps.p.Hidden && !p.Hidden {
			// Shown again: walk it now, also when it was walked long ago (I3).
			unhidden = append(unhidden, p.ID)
		}
		ps.p = p
		ps.info = infos[p.ID]
		rows = append(rows, i.projectRowLocked(ps))
	}
	if complete {
		for id := range i.projs {
			if !seen[id] {
				removed = append(removed, id)
				delete(i.projs, id)
			}
		}
	}
	i.rebuildRouterLocked()
	i.scanner = claude.NewScanner(i.projectListLocked())
	i.mu.Unlock()

	if werr := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
		for _, r := range rows {
			if err := store.UpsertProjectTx(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	}); werr != nil {
		i.logOnce("", "projects", werr)
		return
	}
	for _, id := range removed {
		if err := i.c.DB.DeleteProject(ctx, id); err != nil {
			i.logOnce(id, "projects", err)
		}
	}
	for _, id := range append(added, unhidden...) {
		i.enqueue(request{key: jobKey{bucket: id, kind: kindFull}, immediate: true})
	}
	if len(added) > 0 {
		// Sessions parsed before their project existed have no project yet (M13).
		i.reresolveSessions(ctx)
	}
	if len(added) > 0 || len(removed) > 0 {
		// the project list itself changed: the page refetches /boot and /home
		i.publish(live.ResyncEvent())
		// and live servers may now belong to another project: map them again at once
		// instead of waiting for the next lsof tick (up to 60 s with no page open).
		i.kickServers()
	}
}

// reresolveSessions finds sessions with no project whose cwd (or folder) now belongs to a
// known project, forgets their parse state and queues a fresh parse. The parse then stores
// the project, its events and its per-day rows like for any new session (M13).
func (i *Indexer) reresolveSessions(ctx context.Context) {
	list, err := i.c.DB.IdxUnassignedSessions(ctx)
	if err != nil {
		i.logOnce("", "claude", err)
		return
	}
	i.mu.Lock()
	sc := i.scanner
	i.mu.Unlock()
	for _, s := range list {
		pid := sc.ProjectFor(s.CWD)
		if s.CWD == "" {
			pid = sc.ProjectForFolder(filepath.Base(filepath.Dir(s.JSONLPath)))
		}
		if pid == "" || s.JSONLPath == "" {
			continue
		}
		if err := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
			return store.IdxForgetClaudeFamilyTx(ctx, tx, s.JSONLPath)
		}); err != nil {
			i.logOnce("", "claude", err)
			return
		}
		i.mu.Lock()
		delete(i.sessMem, s.JSONLPath)
		i.mu.Unlock()
		i.enqueue(request{key: jobKey{bucket: bucketClaude, kind: kindClaude, sub: s.JSONLPath + "\x00" + s.Profile},
			immediate: true, path: s.JSONLPath})
	}
}
