package indexer

import (
	"context"
	"database/sql"
	"sort"
	"strconv"
	"time"

	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe/servers"
	"github.com/Prath2000/drashta/internal/store"
)

// serverMinutesEvery is how often the per-day server minutes of projects with a live
// server are recomputed (the number grows while a server runs).
const serverMinutesEvery = 10 * time.Minute

// serversLoop runs lsof every LsofVisible while a page is visible, else every LsofHidden.
// A failed scan keeps the previous state (ledger scanOnce rule).
func (i *Indexer) serversLoop(ctx context.Context) {
	var changed <-chan struct{}
	if i.c.Hub != nil {
		changed = i.c.Hub.Changed()
	}
	var lastScan, lastMinutes time.Time
	// Cold start (no cached projects): the first scan waits for the first discovery,
	// else every server would get project_id '' until the next tick.
	i.mu.Lock()
	cold := len(i.projs) == 0
	i.mu.Unlock()
	if cold {
		select {
		case <-ctx.Done():
			return
		case <-i.projReady:
		}
	}
	for {
		now := i.clock.Now()
		interval := i.lsofInterval()
		if lastScan.IsZero() || now.Sub(lastScan) >= interval {
			select { // this scan reads the current project list, so it covers a pending kick
			case <-i.srvKick:
			default:
			}
			i.scanServers(ctx, now, &lastMinutes)
			lastScan = now
			if ctx.Err() != nil {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-i.clock.After(lastScan.Add(interval).Sub(now)):
		case <-changed:
			// visibility changed: re-check the interval at once
		case <-i.srvKick:
			lastScan = time.Time{} // the project list changed: scan now
		}
	}
}

// kickServers asks the lsof loop for one scan now. It never blocks; a kick that is
// already pending covers this one.
func (i *Indexer) kickServers() {
	select {
	case i.srvKick <- struct{}{}:
	default:
	}
}

func (i *Indexer) lsofInterval() time.Duration {
	if i.c.Hub != nil && i.c.Hub.AnyVisible() {
		return i.c.LsofVisible
	}
	return i.c.LsofHidden
}

// ScanServersNow runs one lsof pass at once (tests, and the servers API after kill/restart).
func (i *Indexer) ScanServersNow(ctx context.Context) error {
	var t time.Time
	return i.scanServers(ctx, i.clock.Now(), &t)
}

func (i *Indexer) scanServers(ctx context.Context, now time.Time, lastMinutes *time.Time) error {
	defer func() {
		if r := recover(); r != nil {
			i.log.Error("panic in lsof scan (recovered)", "panic", r)
		}
	}()
	snap, err := i.pr.Servers(ctx, i.Projects())
	if err != nil {
		if ctx.Err() == nil {
			i.logOnce("", "servers", err)
		}
		return err // keep the last state: nothing is marked dead
	}
	at := snap.At
	if at.IsZero() {
		at = now
	}
	histChanged := i.hist.Merge(snap.Servers, at)
	var ups, downs []store.ServerRun
	if histChanged {
		ups, downs, err = i.syncRuns(ctx, i.hist.Runs())
		if err != nil {
			i.logOnce("", "servers", err)
		}
	} else if err := i.c.DB.IdxTouchServerRuns(ctx, at); err != nil {
		i.logOnce("", "servers", err)
	}

	// servers_now: one row per key (lowest port wins when one process listens twice)
	byKey := map[string]store.ServerNow{}
	ports := map[string][]int{}
	var all []int
	for _, s := range snap.Servers {
		k := servers.Key(s)
		row := store.ServerNow{Key: k, Port: s.Port, PID: s.PID, Name: s.Name, Cmd: s.Cmd, CWD: s.Cwd,
			ProjectID: s.ProjectID, Kind: s.Kind, FirstSeen: at, LastSeen: at}
		if e, ok := i.hist.Lookup(k); ok && !e.FirstSeen.IsZero() {
			row.FirstSeen = e.FirstSeen
		}
		if old, ok := byKey[k]; !ok || s.Port < old.Port {
			byKey[k] = row
		}
		ports[s.ProjectID] = append(ports[s.ProjectID], s.Port)
		all = append(all, s.Port)
	}
	rows := make([]store.ServerNow, 0, len(byKey))
	titlesChanged := false
	for k, r := range byKey {
		if r.Kind == servers.KindDev || r.Kind == "" {
			var changed bool
			r.Title, changed = i.serverTitle(k, r.Port, now)
			titlesChanged = titlesChanged || changed
		}
		rows = append(rows, r)
	}
	i.mu.Lock()
	for ck := range i.titles { // forget titles of servers that are gone
		live := false
		for k, r := range byKey {
			if ck == k+"|"+strconv.Itoa(r.Port) {
				live = true
				break
			}
		}
		if !live {
			delete(i.titles, ck)
		}
	}
	i.mu.Unlock()
	sort.Slice(rows, func(a, b int) bool { return rows[a].Port < rows[b].Port })
	for p := range ports {
		sort.Ints(ports[p])
	}
	sort.Ints(all)

	// Which projects changed their live port list?
	i.mu.Lock()
	var changedProjects []string
	for p, list := range ports {
		if !sameInts(i.srvPorts[p], list) {
			changedProjects = append(changedProjects, p)
		}
	}
	for p := range i.srvPorts {
		if _, ok := ports[p]; !ok {
			changedProjects = append(changedProjects, p)
		}
	}
	prevAll := i.srvPorts["\x00all"]
	i.srvPorts = ports
	i.srvPorts["\x00all"] = all
	i.mu.Unlock()
	sort.Strings(changedProjects)
	anyChange := !sameInts(prevAll, all) || len(changedProjects) > 0 || titlesChanged

	// Days to recompute: the projects whose servers came or went, and every
	// serverMinutesEvery the projects with a live server.
	recompute := map[string]bool{}
	for _, r := range append(append([]store.ServerRun{}, ups...), downs...) {
		if r.ProjectID != "" {
			recompute[r.ProjectID] = true
		}
	}
	if now.Sub(*lastMinutes) >= serverMinutesEvery {
		*lastMinutes = now
		for p := range ports {
			if p != "" {
				recompute[p] = true
			}
		}
	}
	today := i.dayOf(at)

	if anyChange || len(recompute) > 0 || len(ups)+len(downs) > 0 {
		werr := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
			if anyChange {
				if err := store.ReplaceServersNowTx(ctx, tx, rows); err != nil {
					return err
				}
			}
			for _, r := range append(append([]store.ServerRun{}, ups...), downs...) {
				if r.ProjectID == "" {
					continue
				}
				evs := store.ServerRunEvents(r, i.loc)
				for _, e := range evs {
					// ups carry only server_up; downs carry both, server_up is already there
					if _, err := store.AddEventTx(ctx, tx, e); err != nil {
						return err
					}
				}
			}
			for p := range recompute {
				if err := store.RecomputeDailyTx(ctx, tx, p, today, i.loc); err != nil {
					return err
				}
			}
			return nil
		})
		if werr != nil {
			i.logOnce("", "servers", werr)
			return werr
		}
	}

	if anyChange {
		for _, p := range changedProjects {
			if p != "" {
				i.publish(live.ServersEvent(p, ports[p]))
			}
		}
		i.publish(live.ServersEvent("", all))
		i.publishCounts(ctx)
	}
	for p := range recompute {
		i.publish(live.ActivityEvent(p, today))
	}
	return nil
}

// pageTitle is a cached <title> of a live server page.
type pageTitle struct {
	title string
	at    time.Time
}

// titleRetry is how long an empty title is kept before the page is asked again
// (a dev server is often not ready in the first scan that sees it).
const titleRetry = time.Minute

// serverTitle returns the page title of a live dev server, fetching it once per (key, port),
// and again after titleRetry while it is empty. changed is true when the title is new.
func (i *Indexer) serverTitle(key string, port int, now time.Time) (string, bool) {
	ck := key + "|" + strconv.Itoa(port)
	i.mu.Lock()
	t, ok := i.titles[ck]
	i.mu.Unlock()
	if ok && (t.title != "" || now.Sub(t.at) < titleRetry) {
		return t.title, false
	}
	title := i.pr.PageTitle(port)
	i.mu.Lock()
	i.titles[ck] = pageTitle{title: title, at: now}
	i.mu.Unlock()
	return title, title != t.title
}

// syncRuns makes server_runs follow the history: an alive key without an open run gets
// a new run (started at its LastSeen, the scan that saw it come up); a dead key with an
// open run is ended at its LastSeen; open runs of alive keys get their live fields.
// It returns the runs that started and ended.
func (i *Indexer) syncRuns(ctx context.Context, runs []servers.Run) (ups, downs []store.ServerRun, err error) {
	err = i.c.DB.Write(ctx, func(tx *sql.Tx) error {
		ups, downs = nil, nil
		open, err := store.IdxOpenServerRunsTx(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range runs {
			o, isOpen := open[r.Key]
			if r.Ended.IsZero() {
				row := store.ServerRun{Key: r.Key, ProjectID: r.ProjectID, Port: r.Port, PID: r.PID, Name: r.Name,
					Cmd: r.Cmd, CWD: r.Cwd, Kind: r.Kind, LastSeen: r.LastSeen}
				if isOpen {
					row.ID, row.Started = o.ID, o.Started
					if err := store.IdxUpdateServerRunTx(ctx, tx, row); err != nil {
						return err
					}
					continue
				}
				row.Started = r.LastSeen
				if row.Started.IsZero() {
					row.Started = r.Started
				}
				id, err := store.IdxAppendServerRunTx(ctx, tx, row)
				if err != nil {
					return err
				}
				row.ID = id
				ups = append(ups, row)
				continue
			}
			if isOpen {
				end := r.Ended
				if end.Before(o.Started) {
					end = o.Started
				}
				if err := store.IdxEndServerRunTx(ctx, tx, o.ID, end); err != nil {
					return err
				}
				o.Ended = end
				downs = append(downs, o)
			}
		}
		return nil
	})
	return ups, downs, err
}

// historyStore is a servers.HistoryStore over the store's server_runs table.
type historyStore struct {
	db *store.DB
	ix *Indexer // optional: Save goes through the indexer's run sync
}

// NewHistoryStore returns a servers.HistoryStore backed by server_runs. Load gives the newest
// run of each key; Save starts and ends runs so server_runs stays a list of real intervals.
func NewHistoryStore(db *store.DB) servers.HistoryStore { return &historyStore{db: db} }

func (h *historyStore) Load() ([]servers.Run, error) {
	if h.db == nil {
		return nil, nil
	}
	rows, err := h.db.IdxLatestServerRuns(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]servers.Run, 0, len(rows))
	for _, r := range rows {
		last := r.LastSeen
		if last.IsZero() {
			last = r.Started
		}
		out = append(out, servers.Run{Key: r.Key, ProjectID: r.ProjectID, Port: r.Port, PID: r.PID, Name: r.Name,
			Cmd: r.Cmd, Cwd: r.CWD, Kind: r.Kind, Started: r.Started, LastSeen: last, Ended: r.Ended})
	}
	return out, nil
}

func (h *historyStore) Save(runs []servers.Run) error {
	if h.db == nil {
		return nil
	}
	ix := h.ix
	if ix == nil {
		ix = &Indexer{c: Config{DB: h.db}}
	}
	_, _, err := ix.syncRuns(context.Background(), runs)
	return err
}

func sameInts(a, b []int) bool {
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
