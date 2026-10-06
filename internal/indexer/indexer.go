package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/autolink"
	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/claude"
	"github.com/Prath2000/drashta/internal/probe/docs"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/git"
	"github.com/Prath2000/drashta/internal/probe/projects"
	"github.com/Prath2000/drashta/internal/probe/servers"
	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

// Default timings (03-architecture.md sections 4 and 6).
const (
	DefaultDebounce    = 750 * time.Millisecond
	DefaultStormCap    = 5 * time.Second
	DefaultWorkers     = 4
	DefaultSweepEvery  = time.Hour
	DefaultLsofVisible = 5 * time.Second
	DefaultLsofHidden  = 60 * time.Second
	errLogEvery        = time.Hour
)

// Clock is the time source. Tests pass a fake one so nothing sleeps.
type Clock interface {
	Now() time.Time
	// After returns a channel that gets the time once d has passed. d <= 0 fires at once.
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// RealClock is the wall clock.
func RealClock() Clock { return realClock{} }

// Probes are the probe functions the indexer calls. A zero field means the real probe.
// Tests swap single fields (a failing git, a panicking walk, a fake lsof).
type Probes struct {
	Discover     func(cfg *config.Config) ([]probe.Project, error)
	Describe     func(path string) projects.Info
	Walk         func(ctx context.Context, root string, extraSkip []string) (tree.Result, error)
	Docs         func(entries []tree.Entry, root string) []docs.Doc
	GitKey       func(p probe.Project) string
	GitScan      func(ctx context.Context, p probe.Project) (git.Result, error)
	FossilDetect func(path string) bool
	FossilKey    func(p probe.Project, repo string) string
	FossilScan   func(ctx context.Context, p probe.Project) (fossil.Result, error)
	// GitDayStats / FossilDayStats give the exact per-day commit numbers of the last
	// `days` local days ending at now. Defaults git.DayStatsAt / fossil.DayStatsAt.
	GitDayStats    func(ctx context.Context, p probe.Project, now time.Time, days int, loc *time.Location) (map[string]probe.DayCount, error)
	FossilDayStats func(ctx context.Context, p probe.Project, now time.Time, days int, loc *time.Location) (map[string]probe.DayCount, error)
	Servers        func(ctx context.Context, projects []probe.Project) (servers.Snapshot, error)
	ClaudeDiscover func(profiles []string) ([]claude.File, error)
	ClaudeUsers    func(ctx context.Context, path string) []string
	// PageTitle reads the <title> of http://127.0.0.1:<port>/ ("" on failure). Default actions.PageTitle.
	PageTitle func(port int) string
}

func (p *Probes) fill() {
	if p.Discover == nil {
		p.Discover = projects.Discover
	}
	if p.Describe == nil {
		p.Describe = projects.Describe
	}
	if p.Walk == nil {
		p.Walk = tree.Walk
	}
	if p.Docs == nil {
		p.Docs = docs.Extract
	}
	if p.GitKey == nil {
		p.GitKey = git.CacheKey
	}
	if p.GitScan == nil {
		p.GitScan = git.Scan
	}
	if p.FossilDetect == nil {
		p.FossilDetect = fossil.Detect
	}
	if p.FossilKey == nil {
		p.FossilKey = fossil.CacheKey
	}
	if p.FossilScan == nil {
		p.FossilScan = fossil.Scan
	}
	if p.GitDayStats == nil {
		p.GitDayStats = git.DayStatsAt
	}
	if p.FossilDayStats == nil {
		p.FossilDayStats = fossil.DayStatsAt
	}
	if p.Servers == nil {
		sc := servers.NewScanner()
		p.Servers = sc.Scan
	}
	if p.ClaudeDiscover == nil {
		p.ClaudeDiscover = claude.Discover
	}
	if p.ClaudeUsers == nil {
		p.ClaudeUsers = claude.Users
	}
	if p.PageTitle == nil {
		p.PageTitle = actions.PageTitle
	}
}

// GHFunc asks for a gh refresh of one project. remote and branch come from the git probe.
// force is true for a user refresh. It must not block for long (run the fetch in a goroutine).
type GHFunc func(ctx context.Context, p probe.Project, remote, branch string, force bool)

// Config is everything the indexer needs. Cfg and DB are required.
type Config struct {
	Cfg      *config.Config
	DB       *store.DB
	Hub      *live.Hub      // nil: events are not published
	Watcher  Watcher        // nil: no file watching (sweeps only)
	Activity *Activity      // nil: no per-day file counts
	Clock    Clock          // nil: the wall clock
	Loc      *time.Location // nil: time.Local
	Exec     Probes         // probe functions; zero fields use the real probes
	Logger   *slog.Logger
	// AutoLink computes auto task links. nil: autolink.Links (internal/autolink).
	AutoLink func(in autolink.Input) []autolink.Link
	// NoAutoLink turns auto-linking off.
	NoAutoLink bool
	GH         GHFunc

	Workers     int           // 0: DefaultWorkers
	Debounce    time.Duration // 0: DefaultDebounce
	StormCap    time.Duration // 0: DefaultStormCap
	SweepEvery  time.Duration // 0: DefaultSweepEvery
	LsofVisible time.Duration // 0: DefaultLsofVisible
	LsofHidden  time.Duration // 0: DefaultLsofHidden
	// NoServers turns the lsof loop off (tests that do not need it).
	NoServers bool
}

// PartStatus is the health of one part (tree, git, fossil, claude, servers, authored) of a project.
type PartStatus struct {
	StaleSince  time.Time `json:"staleSince,omitempty"`  // last probe run failed; cached data is kept
	Error       string    `json:"error,omitempty"`       // the last error text
	Unavailable string    `json:"unavailable,omitempty"` // the tool is missing (one-line reason)
}

// projState is what the indexer remembers about one project between jobs.
type projState struct {
	p    probe.Project
	info projects.Info

	gitRepo, fossilRepo bool
	gitKey, fossilKey   string
	fossilRepoFile      string
	branch, remote      string
	commits             map[string][]probe.Commit // by vcs; a missing key means "not known yet"
	dayStatsDay         string                    // local day of the last exact per-day write ("" = none yet)
	dirty               map[string][]probe.DirtyFile

	// gitLink is the gitdir of a linked worktree (its .git is a file "gitdir: <main>/.git/worktrees/<n>"),
	// read once per project ("" = not a linked worktree). The router maps it to this project (I6).
	gitLink     string
	gitLinkRead bool

	docs      map[string]docs.Doc // by path
	docsKnown bool
	walked    bool

	lastFiles, lastCommit, lastSession, indexedAt time.Time
	status                                        map[string]PartStatus
}

func (ps *projState) lastChange() time.Time {
	t := ps.lastFiles
	for _, o := range []time.Time{ps.lastCommit, ps.lastSession} {
		if o.After(t) {
			t = o
		}
	}
	return t
}

func (ps *projState) vcsName() string {
	switch {
	case ps.gitRepo && ps.fossilRepo:
		return "git,fossil"
	case ps.gitRepo:
		return "git"
	case ps.fossilRepo:
		return "fossil"
	}
	return ""
}

// Indexer keeps the SQLite index in step with the disk (03-architecture.md section 4).
type Indexer struct {
	c     Config
	pr    Probes
	log   *slog.Logger
	clock Clock
	loc   *time.Location

	mu       sync.Mutex
	projs    map[string]*projState
	rt       *router
	scanner  *claude.Scanner
	sessMem  map[string]*sessMem // by main .jsonl path
	errSeen  map[string]time.Time
	counts   *live.Counts
	links    map[string]string // project -> signature of its last auto-link result
	linkFn   func(in autolink.Input) []autolink.Link
	lastSwp  time.Time
	hist     *servers.History
	srvPorts map[string][]int     // project -> live ports (last published)
	titles   map[string]pageTitle // "<key>|<port>" -> page title of a live dev server

	reqCh   chan request
	doneCh  chan string
	workCh  chan *job
	sweepCh chan sweepReq

	evCount   atomic.Int64 // watch events handled by the scheduler (tests wait on it)
	started   atomic.Bool
	sweeping  atomic.Bool
	wg        sync.WaitGroup
	cancel    context.CancelFunc
	ctx       context.Context
	firstDone chan struct{}
	firstOnce sync.Once

	// projReady is closed when the first projects discovery has finished (with or
	// without an error). The lsof loop waits for it on a cold start, so the first
	// scan can map servers to projects. srvKick asks the lsof loop for a scan now
	// (sent when the project list changed).
	projReady     chan struct{}
	projReadyOnce sync.Once
	srvKick       chan struct{}
}

// New makes an indexer. It does no work until Start.
func New(cfg Config) *Indexer {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Loc == nil {
		cfg.Loc = time.Local
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultWorkers
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = DefaultDebounce
	}
	if cfg.StormCap <= 0 {
		cfg.StormCap = DefaultStormCap
	}
	if cfg.SweepEvery <= 0 {
		cfg.SweepEvery = DefaultSweepEvery
	}
	if cfg.LsofVisible <= 0 {
		cfg.LsofVisible = DefaultLsofVisible
	}
	if cfg.LsofHidden <= 0 {
		cfg.LsofHidden = DefaultLsofHidden
	}
	pr := cfg.Exec
	pr.fill()
	i := &Indexer{
		c:         cfg,
		pr:        pr,
		log:       cfg.Logger.With("pkg", "indexer"),
		clock:     cfg.Clock,
		loc:       cfg.Loc,
		projs:     map[string]*projState{},
		sessMem:   map[string]*sessMem{},
		errSeen:   map[string]time.Time{},
		links:     map[string]string{},
		srvPorts:  map[string][]int{},
		titles:    map[string]pageTitle{},
		reqCh:     make(chan request, 1024),
		doneCh:    make(chan string, cfg.Workers),
		workCh:    make(chan *job, cfg.Workers),
		sweepCh:   make(chan sweepReq, 1), // a burst of requests runs at most one extra sweep (M12)
		firstDone: make(chan struct{}),
		projReady: make(chan struct{}),
		srvKick:   make(chan struct{}, 1),
	}
	i.linkFn = cfg.AutoLink
	if i.linkFn == nil {
		i.linkFn = autolink.Links
	}
	i.rt = newRouter(cfg.Cfg.ProjectsDir, cfg.Cfg.Roots, cfg.Cfg.Skip, cfg.Cfg.ClaudeProfiles)
	i.scanner = claude.NewScanner(nil)
	i.hist = servers.LoadHistory(NewHistoryStore(cfg.DB))
	return i
}

// Start runs the indexer in the background and returns at once (it never waits for the sweep):
// it loads the cached project list, starts the job scheduler and workers, the file watcher,
// the activity tracker, the lsof loop and the hourly sweep, then queues the first full sweep.
// Stop it by cancelling ctx, then call Wait.
func (i *Indexer) Start(ctx context.Context) error {
	if !i.started.CompareAndSwap(false, true) {
		return errors.New("indexer: already started")
	}
	ctx, cancel := context.WithCancel(ctx)
	i.ctx, i.cancel = ctx, cancel
	i.seedFromStore(ctx)

	var events <-chan WatchEvent
	if i.c.Watcher != nil {
		roots := append([]string{i.c.Cfg.ProjectsDir}, profileProjectDirs(i.c.Cfg.ClaudeProfiles)...)
		ch, err := i.c.Watcher.Start(ctx, roots)
		if err != nil {
			i.log.Warn("file watcher did not start; only sweeps will update the index", "err", err)
		} else {
			events = ch
		}
	}

	i.wg.Add(1)
	go func() { defer i.wg.Done(); i.schedule(ctx, events) }()
	for n := 0; n < i.c.Workers; n++ {
		i.wg.Add(1)
		go func() { defer i.wg.Done(); i.worker(ctx) }()
	}
	i.wg.Add(1)
	go func() { defer i.wg.Done(); i.sweeper(ctx) }()
	if i.c.Activity != nil {
		i.c.Activity.SetExtraSkip(i.c.Cfg.Skip)
		i.c.Activity.SetOnFlush(func(projectID, day string) { i.publish(live.ActivityEvent(projectID, day)) })
		i.wg.Add(1)
		go func() { defer i.wg.Done(); i.guard(ctx, "activity", i.c.Activity.Run) }()
	}
	if !i.c.NoServers {
		i.wg.Add(1)
		go func() { defer i.wg.Done(); i.guard(ctx, "servers", i.serversLoop) }()
	}
	return nil
}

// Wait blocks until every goroutine of the indexer has stopped (after ctx was cancelled).
func (i *Indexer) Wait() {
	i.wg.Wait()
	if i.c.Watcher != nil {
		_ = i.c.Watcher.Close()
	}
}

// Stop cancels the indexer and waits for it.
func (i *Indexer) Stop() {
	if i.cancel != nil {
		i.cancel()
	}
	i.Wait()
}

// FirstSweepDone is closed when the first full sweep after Start has finished.
func (i *Indexer) FirstSweepDone() <-chan struct{} { return i.firstDone }

func profileProjectDirs(profiles []string) []string {
	out := make([]string, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, filepath.Join(p, "projects"))
	}
	return out
}

// seedFromStore loads the cached projects so the first jobs know branches and times.
func (i *Indexer) seedFromStore(ctx context.Context) {
	list, err := i.c.DB.ProjectList(ctx)
	if err != nil {
		i.log.Warn("read cached projects", "err", err)
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, sp := range list {
		ps := &projState{
			p:       probe.Project{ID: sp.ID, Name: sp.Name, Group: sp.Group, Path: sp.Path, Hidden: sp.Hidden},
			info:    projects.Info{Stack: sp.Stack, Description: sp.Description, ReadmePath: sp.ReadmePath},
			branch:  sp.Branch,
			remote:  sp.Remote,
			commits: map[string][]probe.Commit{},
			dirty:   map[string][]probe.DirtyFile{},
			status:  map[string]PartStatus{},
		}
		ps.gitRepo = sp.VCS == "git" || sp.VCS == "git,fossil"
		ps.fossilRepo = sp.VCS == "fossil" || sp.VCS == "git,fossil"
		ps.lastFiles = sp.LastChangeAt
		ps.indexedAt = sp.IndexedAt
		i.projs[sp.ID] = ps
	}
	i.rebuildRouterLocked()
	if v, ok, _ := i.c.DB.GetMeta(ctx, "last_sweep_at"); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			i.lastSwp = t
		}
	}
}

// ---- scheduler ----

func (i *Indexer) schedule(ctx context.Context, events <-chan WatchEvent) {
	q := newQueue(i.c.Debounce, i.c.StormCap)
	running := 0
	for {
		now := i.clock.Now()
		if running < i.c.Workers {
			for _, j := range q.pop(now, i.c.Workers-running) {
				running++
				i.workCh <- j // buffered to Workers, never blocks
			}
		}
		var timer <-chan time.Time
		if running < i.c.Workers {
			if d, ok := q.next(); ok {
				timer = i.clock.After(d.Sub(now))
			}
		}
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			i.onEvent(ev, q)
		case r := <-i.reqCh:
			q.add(r, i.clock.Now())
		case b := <-i.doneCh:
			q.release(b)
			running--
		case <-timer:
		}
	}
}

func (i *Indexer) onEvent(ev WatchEvent, q *queue) {
	i.mu.Lock()
	rt := i.rt
	i.mu.Unlock()
	out := rt.route(ev)
	now := i.clock.Now()
	defer i.evCount.Add(1)
	for _, r := range out.reqs {
		q.add(r, now)
	}
	if out.activity && i.c.Activity != nil && !isDirEvent(ev) {
		t := ev.Time
		if t.IsZero() {
			t = now
		}
		i.c.Activity.Record(out.project, out.rel, t)
	}
	if out.sweep {
		i.requestSweep(sweepReq{reason: "must-scan"})
	}
}

// isDirEvent reports whether the event path is an existing folder. Folders do not count
// as file changes on the seismograph. A removed path cannot be checked and counts.
func isDirEvent(ev WatchEvent) bool {
	if ev.Kind == Remove {
		return false
	}
	fi, err := os.Lstat(ev.Path)
	return err == nil && fi.IsDir()
}

func (i *Indexer) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-i.workCh:
			i.runJob(ctx, j)
			for _, w := range j.waiters {
				close(w)
			}
			select {
			case i.doneCh <- j.key.bucket:
			case <-ctx.Done():
				return
			}
		}
	}
}

// enqueue adds a request. It never blocks the caller for long.
func (i *Indexer) enqueue(r request) {
	select {
	case i.reqCh <- r:
	case <-i.doneCtx():
		if r.done != nil {
			close(r.done)
		}
	}
}

func (i *Indexer) doneCtx() <-chan struct{} {
	if i.ctx == nil {
		return nil
	}
	return i.ctx.Done()
}

// runJob runs one job with panic recovery (03 section 10).
func (i *Indexer) runJob(ctx context.Context, j *job) {
	defer func() {
		if r := recover(); r != nil {
			i.log.Error("panic in job (recovered)", "project", j.key.bucket, "job", j.key.kind.String(),
				"panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			i.setStatus(j.key.bucket, j.key.kind.String(), fmt.Errorf("panic: %v", r))
		}
	}()
	if ctx.Err() != nil {
		return
	}
	switch j.key.kind {
	case kindProjects:
		i.jobProjects(ctx)
	case kindTree:
		i.jobTree(ctx, j.key.bucket, sortedPaths(j.paths), time.Time{})
	case kindVCS:
		i.jobVCS(ctx, j.key.bucket, vcsOpts{force: j.force, dirty: j.dirty, push: j.push})
	case kindAuthored:
		i.jobAuthored(ctx, j.key.bucket, sortedPaths(j.paths))
	case kindFull:
		i.jobFull(ctx, j)
	case kindClaude:
		i.jobClaude(ctx, j.key.sub)
	case kindClaudeAll:
		i.jobClaudeAll(ctx)
	}
}

func sortedPaths(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// jobFull runs every probe except gh (unless asked) for one project.
func (i *Indexer) jobFull(ctx context.Context, j *job) {
	id := j.key.bucket
	if j.scanEvent {
		i.publish(live.ScanEvent(id, "running", i.clock.Now()))
		defer func() { i.publish(live.ScanEvent(id, "done", i.clock.Now())) }()
	}
	i.jobVCS(ctx, id, vcsOpts{force: j.force, dirty: true, push: j.push, withGH: j.withGH})
	i.jobTree(ctx, id, nil, j.backfill)
	_ = i.jobAuthored(ctx, id, nil)
	if ctx.Err() != nil {
		return
	}
	// indexed_at says "the first full index has finished" (the API's "surveyed").
	i.mu.Lock()
	ps := i.projs[id]
	var row store.Project
	if ps != nil {
		ps.indexedAt = i.clock.Now()
		row = i.projectRowLocked(ps)
	}
	i.mu.Unlock()
	if ps != nil {
		err := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
			if !i.known(id) {
				return errProjectGone // removed meanwhile (M11)
			}
			return store.UpsertProjectTx(ctx, tx, row)
		})
		if err != nil && !errors.Is(err, errProjectGone) {
			i.logOnce(id, "projects", err)
		}
	}
}

// ---- public API ----

// Refresh rescans one project now (all probes; gh too when withGH). It returns at once;
// progress arrives as "scan" events. An unknown project gives an error.
func (i *Indexer) Refresh(ctx context.Context, projectID string, withGH bool) error {
	if !i.known(projectID) {
		return fmt.Errorf("indexer: unknown project %q", projectID)
	}
	i.enqueue(request{key: jobKey{bucket: projectID, kind: kindFull}, immediate: true, force: true,
		withGH: withGH, scanEvent: true})
	return nil
}

// RefreshAll starts a full sweep now, plus gh for projects that changed in the last 7 days.
// It returns at once.
func (i *Indexer) RefreshAll(ctx context.Context) error {
	i.requestSweep(sweepReq{reason: "refresh", withGH: true})
	return nil
}

// ReindexAuthored mirrors exactly one .drashta file into the index right away (the tracker API
// calls it after each write): the item's table row, task_links, FTS, events, the per-day row of
// the touched days and a "tracker" event. When the file is gone, its row, search row and events
// are removed. relPath is relative to the project folder, for example ".drashta/tasks/T-3.md".
// It runs in the caller's goroutine.
func (i *Indexer) ReindexAuthored(projectID, relPath string) error {
	ps := i.state(projectID)
	if ps == nil {
		return fmt.Errorf("indexer: unknown project %q", projectID)
	}
	ctx := i.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	abs := filepath.Join(ps.p.Path, filepath.FromSlash(relPath))
	return i.jobAuthored(ctx, projectID, []string{abs})
}

// Status returns the health of each part of a project (stale data, missing tools).
func (i *Indexer) Status(projectID string) map[string]PartStatus {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := map[string]PartStatus{}
	if ps := i.projs[projectID]; ps != nil {
		for k, v := range ps.status {
			out[k] = v
		}
	}
	return out
}

// Counts returns the numbers for the hello event.
func (i *Indexer) Counts(ctx context.Context) (live.Counts, error) {
	c, err := i.c.DB.IdxCounts(ctx)
	if err != nil {
		return live.Counts{}, err
	}
	return live.Counts{LiveServers: c.LiveServers, OpenTasks: c.OpenTasks,
		AwaitingReview: c.AwaitingReview, OpenNotes: c.OpenNotes}, nil
}

// ServerHistory is the in-memory server history (alive and recent servers, restart keys).
// The actions and servers API can read it. It is kept in step with server_runs.
func (i *Indexer) ServerHistory() *servers.History { return i.hist }

// Projects returns the known projects (hidden ones too), sorted by id.
func (i *Indexer) Projects() []probe.Project {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.projectListLocked()
}

func (i *Indexer) projectListLocked() []probe.Project {
	out := make([]probe.Project, 0, len(i.projs))
	for _, ps := range i.projs {
		out = append(out, ps.p)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

func (i *Indexer) known(id string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	_, ok := i.projs[id]
	return ok
}

// errProjectGone stops a write whose project was removed while the job ran (M11).
var errProjectGone = errors.New("indexer: project was removed")

// state returns a copy of the project's identity fields (or nil).
// The hidden flag is read again from the config first (I3), so a hide or unhide
// made in Settings is seen by the very next job, not only after the next discovery.
func (i *Indexer) state(id string) *projState {
	i.mu.Lock()
	defer i.mu.Unlock()
	ps := i.projs[id]
	if ps == nil {
		return nil
	}
	i.syncHiddenLocked(ps)
	cp := *ps
	return &cp
}

// syncHiddenLocked sets ps.p.Hidden from the config's hide list (the same rule
// projects.Discover uses) and rebuilds the router when it changed. Call with i.mu held.
func (i *Indexer) syncHiddenLocked(ps *projState) {
	if i.c.Cfg == nil {
		return
	}
	h := i.c.Cfg.IsHidden(ps.p.Group, filepath.Base(ps.p.Path))
	if h == ps.p.Hidden {
		return
	}
	ps.p.Hidden = h
	i.rebuildRouterLocked()
}

// projectRow builds the store row of a project from memory. Call with i.mu held.
// The hidden flag is synced with the config first, so a stale flag is never written back (I3).
func (i *Indexer) projectRowLocked(ps *projState) store.Project {
	i.syncHiddenLocked(ps)
	return store.Project{
		ID: ps.p.ID, Name: ps.p.Name, Group: ps.p.Group, Path: ps.p.Path, Hidden: ps.p.Hidden,
		VCS: ps.vcsName(), Branch: ps.branch, Remote: ps.remote,
		Description: ps.info.Description, Stack: ps.info.Stack, ReadmePath: ps.info.ReadmePath,
		LastChangeAt: ps.lastChange(), IndexedAt: ps.indexedAt,
	}
}

func (i *Indexer) projectRow(id string) (store.Project, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	ps := i.projs[id]
	if ps == nil {
		return store.Project{}, false
	}
	return i.projectRowLocked(ps), true
}

// rebuildRouterLocked makes a new router from the project list. Call with i.mu held.
func (i *Indexer) rebuildRouterLocked() {
	rt := newRouter(i.c.Cfg.ProjectsDir, i.c.Cfg.Roots, i.c.Cfg.Skip, i.c.Cfg.ClaudeProfiles)
	for id, ps := range i.projs {
		rt.byRel[ps.p.Group+"/"+filepath.Base(ps.p.Path)] = id
		rt.hidden[id] = ps.p.Hidden
		if !ps.gitLinkRead {
			ps.gitLink, ps.gitLinkRead = readGitLink(ps.p.Path), true
		}
		if ps.gitLink != "" {
			for _, f := range pathForms(ps.gitLink) {
				rt.worktrees[f] = id
			}
		}
		if ps.fossilRepoFile != "" {
			for _, f := range pathForms(ps.fossilRepoFile) {
				if _, inside := relUnder(filepath.Clean(ps.p.Path), f); !inside {
					rt.fossilRepos[f] = id
				}
			}
		}
	}
	i.rt = rt
}

// readGitLink returns the gitdir a linked worktree points at: the folder named in the
// "gitdir: ..." line of <project>/.git when .git is a small file. "" otherwise.
func readGitLink(projectPath string) string {
	p := filepath.Join(projectPath, ".git")
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 4096 {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
		if !ok {
			continue
		}
		dir := strings.TrimSpace(rest)
		if dir == "" {
			return ""
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(projectPath, dir)
		}
		return filepath.Clean(dir)
	}
	return ""
}
