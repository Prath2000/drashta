package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/store"
)

type vcsOpts struct {
	force  bool // scan even when the cache key did not change
	dirty  bool // a file changed: refresh the dirty list even when the key did not change
	push   bool // a push was seen in .git/refs/remotes
	withGH bool // user refresh: ask gh too
}

// jobVCS runs git and fossil for one project, one after the other (they are serialised:
// one job per project at a time). It uses the cache keys to skip work, keeps the last good
// data when a command fails (stale), and writes commits, dirty files, commit events and the
// per-day rows in one transaction.
func (i *Indexer) jobVCS(ctx context.Context, id string, o vcsOpts) {
	ps := i.state(id)
	if ps == nil {
		return
	}
	p := ps.p

	commitsChanged := false
	dirtyChanged := false
	metaChanged := false
	newCommits := map[string][]probe.Commit{}
	newDirty := map[string][]probe.DirtyFile{}
	for k, v := range ps.commits {
		newCommits[k] = v
	}
	for k, v := range ps.dirty {
		newDirty[k] = v
	}
	gitRepo, fossilRepo := ps.gitRepo, ps.fossilRepo
	gitKey, fossilKey, fossilRepoFile := ps.gitKey, ps.fossilKey, ps.fossilRepoFile
	branch, remote := ps.branch, ps.remote

	// ---- git ----
	gk := i.pr.GitKey(p)
	switch {
	case gk == "":
		if gitRepo || newCommits["git"] != nil {
			gitRepo = false
			metaChanged = true
			if len(newCommits["git"]) > 0 {
				commitsChanged = true
			}
			newCommits["git"] = []probe.Commit{}
			if len(newDirty["git"]) > 0 {
				dirtyChanged = true
			}
			delete(newDirty, "git")
		}
	case o.force || o.dirty || gk != gitKey || newCommits["git"] == nil:
		res, err := i.pr.GitScan(ctx, p)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			i.setStatus(id, "git", err)
		case res.Unavailable != nil:
			i.setUnavailable(id, "git", res.Unavailable.Reason)
		case !res.IsRepo:
			i.setStatus(id, "git", nil)
			if gitRepo {
				gitRepo, metaChanged = false, true
			}
		default:
			i.setStatus(id, "git", nil)
			i.setUnavailable(id, "git", "")
			if !gitRepo {
				gitRepo, metaChanged = true, true
			}
			if gk != gitKey || newCommits["git"] == nil || o.force {
				if !sameCommits(newCommits["git"], res.Commits) {
					commitsChanged = true
				}
				newCommits["git"] = nonNilCommits(res.Commits)
			}
			if !sameDirty(newDirty["git"], res.Dirty) {
				dirtyChanged = true
			}
			newDirty["git"] = res.Dirty
			if res.Branch != branch || res.Remote != remote {
				branch, remote, metaChanged = res.Branch, res.Remote, true
			}
			gitKey = gk
		}
	}

	// ---- fossil ----
	if i.pr.FossilDetect(p.Path) {
		fk := i.pr.FossilKey(p, fossilRepoFile)
		if o.force || o.dirty || fk != fossilKey || newCommits["fossil"] == nil {
			res, err := i.pr.FossilScan(ctx, p)
			switch {
			case err != nil:
				if ctx.Err() != nil {
					return
				}
				i.setStatus(id, "fossil", err)
			case res.Unavailable != nil:
				i.setUnavailable(id, "fossil", res.Unavailable.Reason)
			case !res.IsRepo:
				i.setStatus(id, "fossil", nil)
			default:
				i.setStatus(id, "fossil", nil)
				i.setUnavailable(id, "fossil", "")
				if !fossilRepo {
					fossilRepo, metaChanged = true, true
				}
				if !sameCommits(newCommits["fossil"], res.Commits) {
					commitsChanged = true
				}
				newCommits["fossil"] = nonNilCommits(res.Commits)
				if !sameDirty(newDirty["fossil"], res.Dirty) {
					dirtyChanged = true
				}
				newDirty["fossil"] = res.Dirty
				if !gitRepo && (res.Branch != branch || res.Remote != remote) {
					branch, remote, metaChanged = res.Branch, res.Remote, true
				}
				if res.Repo != fossilRepoFile {
					fossilRepoFile = res.Repo
					metaChanged = true
				}
				fossilKey = i.pr.FossilKey(p, res.Repo)
			}
		}
	} else if fossilRepo || newCommits["fossil"] != nil {
		fossilRepo, metaChanged = false, true
		if len(newCommits["fossil"]) > 0 {
			commitsChanged = true
		}
		newCommits["fossil"] = []probe.Commit{}
		delete(newDirty, "fossil")
		dirtyChanged = true
	}

	// The store keeps one commit list per project. Give it git and fossil together, but only
	// when every VCS of the project has a known list (else a failed fossil scan would drop
	// fossil commits). Without that, keep the stored commits (stale).
	canWriteCommits := (!gitRepo || newCommits["git"] != nil) && (!fossilRepo || newCommits["fossil"] != nil)
	if !canWriteCommits {
		commitsChanged = false
	}

	// Exact per-day commit numbers (the commit list above is capped at 200). Read again
	// when the commits changed, or once per local day (the window moves at midnight).
	ds := i.vcsDayStats(ctx, id, p, ps.dayStatsDay, gitRepo, fossilRepo, canWriteCommits,
		commitsChanged, newCommits["fossil"])

	var all []probe.Commit
	if commitsChanged {
		all = append(all, newCommits["git"]...)
		all = append(all, newCommits["fossil"]...)
		sort.SliceStable(all, func(a, b int) bool { return all[a].Time.After(all[b].Time) })
	}
	var dirtyAll []probe.DirtyFile
	if dirtyChanged {
		seen := map[string]bool{}
		for _, v := range []string{"git", "fossil"} {
			for _, d := range newDirty[v] {
				if !seen[d.Path] {
					seen[d.Path] = true
					dirtyAll = append(dirtyAll, d)
				}
			}
		}
	}

	// The new state goes into memory only AFTER the DB write worked (I4): when the write
	// fails, the old keys stay, so the next run sees a change and tries again.
	apply := func(cur *projState) {
		cur.gitRepo, cur.fossilRepo = gitRepo, fossilRepo
		cur.gitKey, cur.fossilKey, cur.branch, cur.remote = gitKey, fossilKey, branch, remote
		cur.commits, cur.dirty = newCommits, newDirty
		for _, c := range all {
			if c.Time.After(cur.lastCommit) {
				cur.lastCommit = c.Time
			}
		}
	}

	touched := map[string]bool{}
	if commitsChanged || dirtyChanged || metaChanged || ds.write || ds.clear {
		// The project row as it will be after this job (built from a copy of the state).
		i.mu.Lock()
		cur := i.projs[id]
		if cur == nil {
			i.mu.Unlock()
			return // removed while the probes ran (M11)
		}
		next := *cur
		apply(&next)
		row := i.projectRowLocked(&next)
		i.mu.Unlock()

		err := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
			if !i.known(id) {
				return errProjectGone // removed meanwhile: write nothing (M11)
			}
			if commitsChanged {
				prevTimes, err := store.IdxCommitTimesVCSTx(ctx, tx, id)
				if err != nil {
					return err
				}
				if err := i.writeCommitsTx(ctx, tx, id, all, prevTimes, touched); err != nil {
					return err
				}
			}
			if dirtyChanged {
				if err := store.ReplaceDirtyTx(ctx, tx, id, dirtyAll); err != nil {
					return err
				}
			}
			for d := range touched {
				if err := store.RecomputeDailyTx(ctx, tx, id, d, i.loc); err != nil {
					return err
				}
			}
			var changed []string
			var err error
			switch {
			case ds.write:
				changed, err = store.SetDailyCommitStatsTx(ctx, tx, id, ds.from, ds.to, ds.stats, i.loc)
			case ds.clear:
				changed, err = store.ClearDailyCommitStatsTx(ctx, tx, id, i.loc)
			}
			if err != nil {
				return err
			}
			for _, d := range changed {
				touched[d] = true
			}
			return store.UpsertProjectTx(ctx, tx, row)
		})
		if errors.Is(err, errProjectGone) {
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				i.setStatus(id, "commits", err)
			}
			return // memory keeps the old state: the next run retries
		}
		i.setStatus(id, "commits", nil)
	}

	// Remember the new state.
	i.mu.Lock()
	cur := i.projs[id]
	if cur == nil {
		i.mu.Unlock()
		return
	}
	apply(cur)
	if cur.fossilRepoFile != fossilRepoFile {
		cur.fossilRepoFile = fossilRepoFile
		i.rebuildRouterLocked()
	}
	if ds.write || ds.clear {
		cur.dayStatsDay = ds.today
	}
	i.mu.Unlock()

	if commitsChanged {
		i.publish(live.ProjectEvent(id, "commits"))
		i.autolink(ctx, id)
	}
	if len(touched) > 0 {
		i.publishDays(id, touched)
	}
	if dirtyChanged {
		i.publish(live.ProjectEvent(id, "dirty"))
	}
	if i.c.GH != nil && (o.withGH || o.push) && remote != "" {
		i.c.GH(ctx, p, remote, branch, o.withGH)
	}
}

// writeCommitsTx stores the commit list, the commit events, and drops what points at
// commits that are gone (rebase, amend, force-push). touched gets every day that changed.
func (i *Indexer) writeCommitsTx(ctx context.Context, tx *sql.Tx, id string, all []probe.Commit,
	prevTimes map[string]store.IdxCommitTime, touched map[string]bool) error {
	given := make(map[string]bool, len(all))
	for _, c := range all {
		given[c.ID] = true
	}
	oldest := store.OldestPerVCS(all)
	// Commits that are gone. ReplaceCommits drops the ones newer than the oldest given
	// commit of the same VCS (store.CommitGone, the same rule); IdxDropCommitsTx also removes
	// their events (the store keeps older ones on purpose, so the counts survive the cap).
	var gone []string
	for cid, pt := range prevTimes {
		if !given[cid] && (len(all) == 0 || store.CommitGone(oldest, pt.VCS, pt.TS)) {
			gone = append(gone, cid)
			touched[i.dayOf(unixTime(pt.TS))] = true
		}
	}
	sort.Strings(gone)
	if err := store.ReplaceCommitsTx(ctx, tx, id, all); err != nil {
		return err
	}
	if err := store.IdxDropCommitsTx(ctx, tx, id, gone); err != nil {
		return err
	}
	for _, c := range all {
		if pt, ok := prevTimes[c.ID]; ok && pt.TS == c.Time.Unix() {
			continue // known, unchanged
		}
		if pt, ok := prevTimes[c.ID]; ok {
			touched[i.dayOf(unixTime(pt.TS))] = true
		}
		day := i.dayOf(c.Time)
		touched[day] = true
		meta, _ := json.Marshal(map[string]any{"vcs": c.VCS, "plus": c.Plus, "minus": c.Minus, "author": c.Author})
		if err := store.IdxDeleteEventsTx(ctx, tx, id, "commit", "commit:"+c.ID); err != nil {
			return err
		}
		if _, err := store.AddEventTx(ctx, tx, store.Event{ProjectID: id, TS: c.Time, Day: day,
			Kind: "commit", Ref: "commit:" + c.ID, Label: c.Msg, Meta: meta}); err != nil {
			return err
		}
	}
	return nil
}

func sameCommits(a, b []probe.Commit) bool {
	if a == nil || len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n].ID != b[n].ID || !a[n].Time.Equal(b[n].Time) || a[n].Plus != b[n].Plus ||
			a[n].Minus != b[n].Minus || a[n].NFiles != b[n].NFiles || a[n].Msg != b[n].Msg {
			return false
		}
	}
	return true
}

func sameDirty(a, b []probe.DirtyFile) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

func nonNilCommits(c []probe.Commit) []probe.Commit {
	if c == nil {
		return []probe.Commit{}
	}
	return c
}

// DayStatsWindow is how many local days (today included) the indexer counts exactly
// with git/fossil DayStats. Home shows at most 30 days; 35 leaves a margin.
const DayStatsWindow = 35

// dayStatsPlan is what jobVCS writes into daily_commits.
type dayStatsPlan struct {
	write    bool // store stats for from..to
	clear    bool // the project is no longer a repo: drop its rows
	from, to string
	today    string // "" after a clear
	stats    map[string]probe.DayCount
}

// vcsDayStats reads the exact per-day commit numbers of one project when they are due.
// git gives commits and lines. fossil gives commit counts; its lines come from the
// per-commit cache (fossilCommits, the capped list), as before. A project with both
// adds the two. Any failure skips the write: the stored rows stay (stale) and the
// error is logged once.
func (i *Indexer) vcsDayStats(ctx context.Context, id string, p probe.Project, lastDay string,
	gitRepo, fossilRepo, known, commitsChanged bool, fossilCommits []probe.Commit) dayStatsPlan {
	now := i.clock.Now()
	today := i.dayOf(now)
	if !gitRepo && !fossilRepo {
		return dayStatsPlan{clear: lastDay != ""}
	}
	if !known || (!commitsChanged && lastDay == today) {
		return dayStatsPlan{}
	}
	from, err := store.AddDays(today, -(DayStatsWindow - 1)) // calendar math, DST safe (I1)
	if err != nil {
		return dayStatsPlan{}
	}
	plan := dayStatsPlan{write: true, from: from, to: today, today: today,
		stats: map[string]probe.DayCount{}}
	add := func(m map[string]probe.DayCount, lines bool) {
		for d, c := range m {
			if d < plan.from || d > plan.to {
				continue
			}
			cur := plan.stats[d]
			cur.Commits += c.Commits
			if lines {
				cur.Plus += c.Plus
				cur.Minus += c.Minus
			}
			plan.stats[d] = cur
		}
	}
	if gitRepo {
		m, err := i.pr.GitDayStats(ctx, p, now, DayStatsWindow, i.loc)
		if err != nil {
			if ctx.Err() == nil {
				i.logOnce(id, "daystats", err)
			}
			return dayStatsPlan{}
		}
		add(m, true)
	}
	if fossilRepo {
		m, err := i.pr.FossilDayStats(ctx, p, now, DayStatsWindow, i.loc)
		if err != nil {
			if ctx.Err() == nil {
				i.logOnce(id, "daystats", err)
			}
			return dayStatsPlan{}
		}
		add(m, false)
		for d, c := range probe.DayCounts(fossilCommits, i.loc) {
			if d < plan.from || d > plan.to {
				continue
			}
			cur := plan.stats[d]
			cur.Plus += c.Plus
			cur.Minus += c.Minus
			plan.stats[d] = cur
		}
	}
	return plan
}
