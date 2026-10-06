package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/drawing"
	"github.com/Prath2000/drashta/internal/httpapi"
	"github.com/Prath2000/drashta/internal/indexer"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/gh"
	"github.com/Prath2000/drashta/internal/probe/git"
	"github.com/Prath2000/drashta/internal/probe/projects"
	"github.com/Prath2000/drashta/internal/probe/servers"
	"github.com/Prath2000/drashta/internal/resolve"
	"github.com/Prath2000/drashta/internal/store"
	"github.com/Prath2000/drashta/web"
)

// DefaultPort is where drashta listens (decision S2).
const DefaultPort = 4455

// ErrAlreadyRunning is returned by Run when another drashta already answers on the
// port. main exits 0 on it (03-architecture.md section 9: single instance).
var ErrAlreadyRunning = errors.New("drashta is already running")

// Options configure Run and Reindex. The zero value runs the real app on port 4455.
type Options struct {
	// Port to listen on (127.0.0.1 only). 0 means DefaultPort. Ignored when Addr is set.
	Port int
	// Addr overrides Port, for example "127.0.0.1:0" in tests (random free port).
	Addr string
	// Dev is the -dev flag: serve no page (Vite does) and allow Origin localhost:5173.
	Dev bool
	// DataDir is the folder for index.db, logs and the log file. "" means config.DataDir().
	DataDir string
	// ConfigPath is config.toml. "" means <DataDir>/config.toml.
	ConfigPath string
	// Logger replaces the log file (tests). Nil means <DataDir>/drashta.log, rotated at 10 MB.
	Logger *slog.Logger
	// LogStderr also copies log lines to stderr (useful in a terminal; launchd already
	// sends stderr to the log file, so the real service leaves it off).
	LogStderr bool
	// Static is the built web client. Nil means web.Client() (embedded). Not used in Dev.
	Static fs.FS
	// Exec runs native "open" commands. Nil means actions.ExecExecutor{} (real commands).
	// Tests pass *actions.RecorderExecutor so nothing runs.
	Exec actions.Executor
	// Probes swaps single probe functions of the indexer (tests: a fake lsof).
	Probes indexer.Probes
	// NoServers turns the lsof loop off.
	NoServers bool
	// Debounce is the indexer's quiet time before a job runs. 0 means the default (750 ms).
	Debounce time.Duration
	// Ready is called once the HTTP server is listening, with the bound address and the
	// CSRF token text. Tests use it; main does not need it.
	Ready func(addr, token string)
	// Started is called once the indexer has started (watchers running). Tests use it.
	Started func(ix *indexer.Indexer)
}

func (o *Options) addr() string {
	if o.Addr != "" {
		return o.Addr
	}
	p := o.Port
	if p == 0 {
		p = DefaultPort
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
}

func (o *Options) dirs() (dataDir, cfgPath string) {
	dataDir = o.DataDir
	if dataDir == "" {
		dataDir = config.DataDir()
	}
	cfgPath = o.ConfigPath
	if cfgPath == "" {
		cfgPath = filepath.Join(dataDir, "config.toml")
	}
	return dataDir, cfgPath
}

// logger returns the logger and a close function.
func (o *Options) logger(dataDir string) (*slog.Logger, func(), error) {
	if o.Logger != nil {
		return o.Logger, func() {}, nil
	}
	rf, err := openRotating(filepath.Join(dataDir, "drashta.log"), logMaxBytes, logKeep)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	var w io.Writer = rf
	if o.LogStderr {
		w = io.MultiWriter(rf, os.Stderr)
	}
	log := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	// Packages that log through slog.Default (actions: "action=... ok=...") go to the file too.
	slog.SetDefault(log)
	return log, func() { _ = rf.Close() }, nil
}

// Run starts drashta and blocks until ctx is cancelled (or the HTTP server fails).
//
// Startup order (03-architecture.md section 9): single-instance check, config, open the
// DB (migrations run inside store.Open), start HTTP first so the page renders from the
// cache at once, then the indexer (watchers, lsof loop) and its background sweep.
// Shutdown: stop HTTP (ends SSE streams), stop the indexer, flush the activity counts,
// close the hub and the DB.
func Run(ctx context.Context, opts Options) error {
	addr := opts.addr()
	if !strings.HasSuffix(addr, ":0") {
		running, err := httpapi.AlreadyRunning(addr)
		if err != nil {
			return err
		}
		if running {
			return ErrAlreadyRunning
		}
	}

	dataDir, cfgPath := opts.dirs()
	if err := securePrivate(dataDir); err != nil {
		return err
	}
	log, closeLog, err := opts.logger(dataDir)
	if err != nil {
		return err
	}
	defer closeLog()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Error("config", "err", err)
		return err
	}
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}
	db, err := store.Open(dataDir)
	if err != nil {
		log.Error("open index", "err", err)
		return err
	}
	defer db.Close()
	if r := db.Recovered(); r != "" {
		log.Warn("index.db was corrupt; started fresh", "moved_to", r)
	}
	logTools(log)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// ---- live hub ----
	hub := live.NewHub(live.HubOptions{})
	hub.Start(ctx)
	defer hub.Close()

	// ---- indexer (built now, started after HTTP) ----
	var mgr *actions.Manager
	probes := opts.Probes
	realScan := probes.Servers
	if realScan == nil {
		realScan = servers.NewScanner().Scan
	}
	probes.Servers = func(c context.Context, ps []probe.Project) (servers.Snapshot, error) {
		snap, err := realScan(c, ps)
		if err == nil && mgr != nil {
			at := snap.At
			if at.IsZero() {
				at = time.Now()
			}
			mgr.SetLive(snap.Servers, at)
		}
		return snap, err
	}

	var watcher indexer.Watcher
	watchLat := map[string]time.Duration{cfg.ProjectsDir: time.Second}
	for _, p := range cfg.ClaudeProfiles {
		watchLat[filepath.Join(p, "projects")] = 2 * time.Second
	}
	watcher = indexer.NewFSEventsWatcher(watchLat)
	if sc, ok := watcher.(indexer.SkipConfigurer); ok {
		sc.SetExtraSkip(cfg.Skip)
	}
	activity := indexer.NewActivity(db, time.Local, time.Now)

	ghLog := newHourlyLogger(log.With("pkg", "gh"))
	ghCache := gh.NewCache(ghStore{db: db, log: log})
	ghCache.Disabled = !cfg.GHEnabled
	ghCache.OnError = func(projectID string, err error) {
		ghLog.Log(projectID, "gh fetch failed (shown once per hour)", err)
	}

	var projAPI *httpapi.ProjectAPI // set below, before the indexer starts
	var ghWG sync.WaitGroup
	ghFunc := func(_ context.Context, p probe.Project, remote, branch string, force bool) {
		ghWG.Add(1)
		go func() {
			defer ghWG.Done()
			c, cancelGH := context.WithTimeout(ctx, 2*time.Minute)
			defer cancelGH()
			if !force {
				ghCache.Invalidate(p.ID) // a push was seen: the next Get refetches
			}
			if _, err := ghCache.Get(c, p, remote, branch, force); err != nil && c.Err() == nil {
				ghLog.Log(p.ID, "gh refresh failed (shown once per hour)", err)
			}
			if projAPI != nil {
				projAPI.InvalidateGitHub(p.ID)
			}
		}()
	}

	ix := indexer.New(indexer.Config{
		Cfg: cfg, DB: db, Hub: hub, Watcher: watcher, Activity: activity,
		Exec: probes, Logger: log, GH: ghFunc, Debounce: opts.Debounce, NoServers: opts.NoServers,
	})
	mgr = actions.NewManager(filepath.Join(dataDir, "logs"), ix.ServerHistory().Lookup)

	counts := func(c context.Context) (live.Counts, error) { return ix.Counts(c) }
	countsNoCtx := func() live.Counts {
		c, cancelC := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelC()
		n, err := counts(c)
		if err != nil {
			log.Warn("counts", "err", err)
		}
		return n
	}

	// ---- HTTP ----
	static := opts.Static
	if static == nil && !opts.Dev {
		static = web.Client()
		if !web.Built() {
			log.Warn("web client is not built; the page answers 503 until `make web` runs")
		}
	}
	srv := httpapi.New(httpapi.Options{
		Addr: addr, Dev: opts.Dev, Hub: hub, Static: static, Counts: countsNoCtx, Logger: log,
	})

	home, _ := os.UserHomeDir()
	resolver := resolve.New(resolve.Options{
		DB: db,
		Actions: actions.Config{
			Editor: cfg.EditorList(), Terminal: cfg.Terminal, Roots: []string{cfg.ProjectsDir},
			DesktopScript: cfg.DesktopScript,
		},
		Home: home,
	})
	exec := opts.Exec
	if exec == nil {
		exec = actions.ExecExecutor{}
	}

	var gitNameOnce sync.Once
	var gitName string
	author := func() string {
		c, cancelA := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelA()
		if v, ok, err := db.GetSetting(c, "author"); err == nil && ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		gitNameOnce.Do(func() { gitName = httpapi.GitUserName(c) })
		return gitName
	}

	httpapi.RegisterHome(srv, httpapi.HomeDeps{
		Store:         db,
		Config:        httpapi.NewHomeConfig(cfg, cfgPath),
		Indexer:       homeIndexer{ix: ix, log: log},
		Resolver:      resolver,
		Loc:           time.Local,
		Profiles:      cfg.ClaudeProfiles,
		DefaultAuthor: httpapi.GitUserName,
		Counts:        counts,
		EmptyNotice:   func() string { return projects.EmptyNotice(cfg, cfgPath) },
		// No OnSettings: the editor is read only from config.toml (security I-1).
	})
	projAPI = httpapi.RegisterProject(srv, httpapi.ProjectDeps{
		Store:   db,
		Roots:   []string{cfg.ProjectsDir},
		Tree:    httpapi.RealProjectTree{Skip: cfg.Skip},
		VCS:     httpapi.RealProjectVCS{},
		Claude:  httpapi.RealProjectTranscripts{Profiles: cfg.ClaudeProfiles},
		Servers: ix.ServerHistory(),
		Actions: serverActions{m: mgr, rescan: func() {
			go func() {
				c, cancelS := context.WithTimeout(ctx, 30*time.Second)
				defer cancelS()
				time.Sleep(500 * time.Millisecond) // let the process start or die first
				_ = ix.ScanServersNow(c)
			}()
		}},
		GitHub:   httpapi.ProjectGitHubFromCache{Cache: ghCache},
		Drawings: drawing.New(cfg.Chitralipi, filepath.Join(dataDir, "cache", "dwg")),
		Loc:      time.Local,
		Logger:   log,
		Home:     home,
	})
	httpapi.RegisterTracker(srv, httpapi.TrackerDeps{
		Resolver: resolver,
		Reindex:  ix,
		Exec:     exec,
		Author:   author,
		// plash-mode always writes here (not DataDir): the Option-tap keys run it too.
		DesktopState: filepath.Join(home, ".drashta", "plash-mode.state"),
	})

	ln, err := srv.Listen()
	if err != nil {
		log.Error("listen", "addr", addr, "err", err)
		return err
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	bound := ln.Addr().String()
	log.Info("drashta listening", "addr", bound, "dev", opts.Dev, "data", dataDir)
	if opts.Ready != nil {
		opts.Ready(bound, srv.Token())
	}

	// ---- indexer: watchers, lsof loop, background sweep ----
	if err := ix.Start(ctx); err != nil {
		log.Error("indexer start", "err", err)
	}
	if opts.Started != nil {
		opts.Started(ix)
	}

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-serveErr:
		if runErr != nil {
			log.Error("http server stopped", "err", runErr)
		}
	}

	// ---- shutdown ----
	log.Info("drashta stopping")
	cancel()
	shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shCancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	ix.Stop()
	ghWG.Wait()
	if err := activity.Flush(shCtx); err != nil {
		log.Warn("flush activity", "err", err)
	}
	log.Info("drashta stopped")
	return runErr
}

// logTools logs which external tools exist (03 section 10: a missing tool makes that
// probe report "unavailable"; nothing else stops).
func logTools(log *slog.Logger) {
	log.Info("tools", "git", git.Available(), "fossil", fossil.Available(), "gh", gh.Available())
	if !git.Available() {
		log.Warn("git not found on PATH: git data is unavailable")
	}
	if !fossil.Available() {
		log.Warn("fossil not found on PATH: fossil data is unavailable")
	}
}

// Reindex rebuilds the index and exits (drashta -reindex, drashta reindex [project]).
// With no project it drops every cache table (history tables stay) and runs one full
// sweep. With a project it removes that project's cached rows and runs one full sweep.
// It refuses to run while a drashta server answers on the port: stop it first, or use
// the Refresh button.
func Reindex(ctx context.Context, opts Options, project string, out io.Writer) error {
	addr := opts.addr()
	if running, err := httpapi.AlreadyRunning(addr); err == nil && running {
		return fmt.Errorf("drashta is running on %s; stop it first (make unservice) or use Refresh in the page", addr)
	}
	dataDir, cfgPath := opts.dirs()
	if err := securePrivate(dataDir); err != nil {
		return err
	}
	log, closeLog, err := opts.logger(dataDir)
	if err != nil {
		return err
	}
	defer closeLog()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}
	db, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer db.Close()

	start := time.Now()
	if project == "" {
		fmt.Fprintln(out, "dropping cache tables (history is kept)")
		if err := db.Reindex(ctx); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "dropping cached rows of %s\n", project)
		if err := db.DeleteProject(ctx, project); err != nil {
			return err
		}
	}
	ix := indexer.New(indexer.Config{Cfg: cfg, DB: db, Logger: log, Exec: opts.Probes, NoServers: true,
		Activity: indexer.NewActivity(db, time.Local, time.Now)})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := ix.Start(ctx); err != nil {
		return err
	}
	fmt.Fprintln(out, "scanning projects and Claude sessions ...")
	interrupted := false
	select {
	case <-ix.FirstSweepDone():
	case <-ctx.Done():
		interrupted = true
	}
	cancel()
	ix.Wait()
	if interrupted {
		return errors.New("reindex interrupted; run it again to finish")
	}
	fmt.Fprintf(out, "done in %s (%d projects)\n", time.Since(start).Round(time.Millisecond), len(ix.Projects()))
	return nil
}

// privateFiles are the files in the data folder that only the user may read (security M-1).
// index.db and its -wal/-shm are handled by store.Open; settings.json by the store too.
var privateFiles = []string{"config.toml", "settings.json", "launchd.log", "drashta.log"}

// securePrivate creates the data folder with mode 0700, sets 0700 on an existing one
// (MkdirAll does not change it), and sets 0600 on drashta's own files in it, including
// rotated logs (drashta.log.1, ...). Files that do not exist yet are skipped; new ones are
// created 0600 by their writers.
func securePrivate(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data folder: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return fmt.Errorf("chmod data folder: %w", err)
	}
	names := append([]string(nil), privateFiles...)
	if m, err := filepath.Glob(filepath.Join(dataDir, "drashta.log.*")); err == nil {
		for _, p := range m {
			names = append(names, filepath.Base(p))
		}
	}
	for _, n := range names {
		if err := os.Chmod(filepath.Join(dataDir, n), 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("chmod %s: %w", n, err)
		}
	}
	return nil
}
