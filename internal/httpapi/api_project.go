package httpapi

// Project pages of the HTTP API (docs/spec/04-data-model.md section 6, "Project" and "Servers").
//
// This file holds the shared part: the dependencies, RegisterProject, the small interfaces
// for the probes, and the first three routes. The other routes live next to it:
//
//	api_files.go     GET /api/projects/{p}/tree, /file, /raw, /files/{path...}
//	api_dwg.go       GET /api/projects/{p}/dwg/layers, /dwg/entities
//	api_commits.go   GET /api/projects/{p}/commits, /commits/{id}, /dirty
//	api_sessions.go  GET /api/projects/{p}/sessions, GET /api/sessions/{id}
//	api_servers.go   GET /api/servers, GET /api/servers/embed, POST /api/servers/kill, POST /api/servers/restart
//	api_github.go    GET /api/projects/{p}/github
//
// # Routes in this file
//
//	GET /api/projects/{p}                 {project, overview:{readme, counts, dirty, servers, milestones}}
//	GET /api/projects/{p}/activity?range  {range, days:[{day, files, commits, ...}]}   range 7 or 30 (default 30)
//	GET /api/projects/{p}/docs            {docs:[{path, title, mtime, lines}]}
//
// {p} is the project id (the folder name, or "<group>.<name>" on a clash). It is looked up in
// the index. It is never a path. An unknown id is a 404.
//
// # How the wiring code uses it
//
//	api := httpapi.RegisterProject(srv, httpapi.ProjectDeps{
//	    Store:   db,                                    // *store.DB
//	    Roots:   []string{projectsDir},                 // every project folder must sit inside one
//	    Tree:    httpapi.RealProjectTree{Skip: cfg.Skip},
//	    VCS:     httpapi.RealProjectVCS{},
//	    Claude:  httpapi.RealProjectTranscripts{Profiles: cfg.ClaudeProfiles},
//	    Servers: history,                               // *servers.History (has Snapshot())
//	    Actions: manager,                               // *actions.Manager (has Kill and Restart)
//	    GitHub:  httpapi.ProjectGitHubFromCache{Cache: ghCache}, // ghCache is a *gh.Cache
//	    Drawings: drawing.New(cfg.Chitralipi, cacheDir),         // reads .dwg files (internal/drawing)
//	    // ProbePage is optional: default actions.ProbePage (used by GET /api/servers/embed).
//	})
//	// after a forced gh refresh: api.InvalidateGitHub(projectID)
//
// Every dependency may be nil. The route then answers with an empty result (or 503 for the two
// POST routes), so a missing tool never breaks the rest of the page.

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/claude"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/git"
	"github.com/Prath2000/drashta/internal/probe/servers"
	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

// ---- what the project routes need from other packages ----

// ProjectTree is the file tree probe (internal/probe/tree). RealProjectTree implements it.
type ProjectTree interface {
	// ListDir lists the children of dir down to depth levels (guarded, lazy).
	ListDir(root, dir string, depth int) ([]tree.Entry, error)
	// ReadFile reads a file live (1-based line window, 1 MB cap, secret files are redacted).
	ReadFile(root, rel string, from, to int) (tree.FileText, error)
	// RawPath checks a path for the raw endpoint and returns the real path and a safe content type.
	RawPath(root, rel string) (abs, mime string, err error)
}

// ProjectVCS reads live history from git or fossil. vcs is "git" or "fossil".
// RealProjectVCS implements it.
type ProjectVCS interface {
	Commits(ctx context.Context, vcs string, p probe.Project, skip, limit int, path string) ([]probe.Commit, error)
	Detail(ctx context.Context, vcs string, p probe.Project, id string) (probe.CommitDetail, error)
	DirtyDiff(ctx context.Context, vcs string, p probe.Project) (diff string, truncated bool, err error)
}

// ProjectTranscripts reads one page of a Claude transcript. RealProjectTranscripts implements it.
type ProjectTranscripts interface {
	Transcript(ctx context.Context, path string, offset int64, limit int, roles []string) (claude.Page, error)
}

// ProjectServers gives the servers that run now and the recent ones. *servers.History implements it.
type ProjectServers interface {
	Snapshot() (active, recent []servers.Server)
}

// ProjectServerActions kills and restarts servers. *actions.Manager implements it.
type ProjectServerActions interface {
	Kill(pid int, key string, confirm bool) error
	Restart(key string, confirm bool) (logPath string, err error)
}

// ProjectDeps is everything RegisterProject needs. Only Store is required.
type ProjectDeps struct {
	Store *store.DB
	// Roots are the allowed project roots (the projects dir). When set, a request for a project
	// whose folder is outside every root is refused with 403, and a missing folder is a 404.
	Roots []string

	Tree    ProjectTree
	VCS     ProjectVCS
	Claude  ProjectTranscripts
	Servers ProjectServers
	Actions ProjectServerActions
	GitHub  ProjectGitHub
	// Drawings reads DWG files for the drawing preview. Nil answers 503 on the /dwg routes.
	Drawings ProjectDrawings

	// ProbePage tells whether a live server's page can be embedded in an iframe on parentOrigin.
	// Default actions.ProbePage (a real GET on loopback); tests replace it.
	ProbePage func(port int, parentOrigin string) actions.PageInfo

	Now    func() time.Time // default time.Now
	Loc    *time.Location   // zone for time strings and day keys; default time.Local
	Logger *slog.Logger     // default slog.Default()
	// Home is shown as "~" in the restart log path. Default os.UserHomeDir().
	Home string
}

// ProjectAPI is the handle RegisterProject returns.
type ProjectAPI struct {
	d    ProjectDeps
	log  *slog.Logger
	now  func() time.Time
	loc  *time.Location
	home string

	ghMu    sync.Mutex
	ghCache map[string]pjGitHubEntry
}

// RegisterProject adds all the project routes to s. See the file comment for the list.
func RegisterProject(s *Server, d ProjectDeps) *ProjectAPI {
	a := &ProjectAPI{d: d, log: d.Logger, now: d.Now, loc: d.Loc, home: d.Home, ghCache: map[string]pjGitHubEntry{}}
	if a.log == nil {
		a.log = s.Deps().Logger
	}
	if a.log == nil {
		a.log = slog.Default()
	}
	if a.now == nil {
		a.now = time.Now
	}
	if a.loc == nil {
		a.loc = time.Local
	}
	if a.home == "" {
		a.home, _ = os.UserHomeDir()
	}

	s.Handle(http.MethodGet, "/api/projects/{p}", a.handleOverview, false)
	s.Handle(http.MethodGet, "/api/projects/{p}/activity", a.handleActivity, false)
	s.Handle(http.MethodGet, "/api/projects/{p}/docs", a.handleDocs, false)
	a.registerFiles(s)
	a.registerCommits(s)
	a.registerSessions(s)
	a.registerServers(s)
	a.registerGitHub(s)
	a.registerDrawings(s)
	return a
}

// ---- shared helpers ----

// pjProject is one project, found by the {p} part of the URL.
type pjProject struct {
	Row   store.Project
	Probe probe.Project
	Root  string // absolute project folder
}

// lookup finds the project of the URL. It writes 404 (unknown id) or 500 itself.
func (a *ProjectAPI) lookup(w http.ResponseWriter, r *http.Request) (pjProject, bool) {
	var pr pjProject
	if a.d.Store == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "the index is not open")
		return pr, false
	}
	id := r.PathValue("p")
	row, ok, err := a.d.Store.ProjGet(r.Context(), id)
	if err != nil {
		a.fail(w, r, "project lookup", err)
		return pr, false
	}
	if !ok {
		NotFound(w, "no such project")
		return pr, false
	}
	pr.Row = row
	pr.Root = row.Path
	pr.Probe = probe.Project{ID: row.ID, Name: row.Name, Group: row.Group, Path: row.Path, Hidden: row.Hidden}
	return pr, true
}

// rootCheck says whether the project folder may be used: it must be inside one of Roots (when
// Roots is set) and it must exist. It returns pathguard.ErrOutside or a not-exist error.
func (a *ProjectAPI) rootCheck(pr pjProject) error {
	if len(a.d.Roots) == 0 {
		return nil
	}
	_, err := pathguard.ResolveAny(a.d.Roots, pr.Root)
	return err
}

// diskGuard writes the 403 or 404 for a project folder that cannot be used. It returns true when
// the folder is fine.
func (a *ProjectAPI) diskGuard(w http.ResponseWriter, pr pjProject) bool {
	err := a.rootCheck(pr)
	switch {
	case err == nil:
		return true
	case errors.Is(err, pathguard.ErrOutside):
		Forbidden(w, "the project folder is outside the allowed roots")
	default:
		NotFound(w, "the project folder is not on disk")
	}
	return false
}

// onDisk is lookup plus the root check, for routes that touch the project folder.
func (a *ProjectAPI) onDisk(w http.ResponseWriter, r *http.Request) (pjProject, bool) {
	pr, ok := a.lookup(w, r)
	if !ok {
		return pr, false
	}
	return pr, a.diskGuard(w, pr)
}

// fail logs an unexpected error and answers 500 without the detail.
func (a *ProjectAPI) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	if r.Context().Err() != nil {
		// The client left. Nothing to tell it.
		WriteError(w, http.StatusServiceUnavailable, "canceled", "request canceled")
		return
	}
	a.log.Error("project api", "what", what, "path", r.URL.Path, "err", err)
	Internal(w, what+" failed")
}

// pathError maps an error from the tree or pathguard to a reply.
func (a *ProjectAPI) pathError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pathguard.ErrOutside):
		Forbidden(w, "the path is outside the project")
	case errors.Is(err, tree.ErrSecret):
		Forbidden(w, "secret file: its content is never served")
	case errors.Is(err, tree.ErrIsDir), errors.Is(err, tree.ErrNotDir), errors.Is(err, tree.ErrNotRegular):
		BadRequest(w, "the path is not of the right kind: "+pjShortErr(err))
	case errors.Is(err, fs.ErrNotExist):
		NotFound(w, "no such file or folder")
	case errors.Is(err, fs.ErrPermission):
		Forbidden(w, "permission denied")
	default:
		a.fail(w, r, "file access", err)
	}
}

func pjShortErr(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// pjCleanRel checks a project-relative path from a query string. It rejects absolute paths,
// ".." elements and NUL, and returns the cleaned path ("" for the root when allowEmpty).
func pjCleanRel(raw string, allowEmpty bool) (string, error) {
	if raw == "" {
		if allowEmpty {
			return "", nil
		}
		return "", errors.New("path is required")
	}
	if len(raw) > 4096 {
		return "", errors.New("path is too long")
	}
	if strings.ContainsRune(raw, 0) {
		return "", errors.New("path has a forbidden character")
	}
	if strings.HasPrefix(raw, "/") {
		return "", errors.New("path must be relative to the project")
	}
	for _, seg := range strings.Split(raw, "/") {
		if seg == ".." {
			return "", errors.New("path must not contain ..")
		}
	}
	cleaned := path.Clean(raw)
	if cleaned == "." {
		if allowEmpty {
			return "", nil
		}
		return "", errors.New("path is required")
	}
	return cleaned, nil
}

// pjClipText cuts s to at most n bytes at a character boundary.
func pjClipText(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// iso formats a time in the configured zone. The zero time gives "".
func (a *ProjectAPI) iso(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(a.loc).Format(time.RFC3339)
}

// isoPtr is iso, but nil for the zero time (JSON null).
func (a *ProjectAPI) isoPtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := a.iso(t)
	return &s
}

// pjVCSList turns the project vcs text ("git", "fossil", "git,fossil", "") into a list.
func pjVCSList(v string) []string {
	out := []string{}
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p == "git" || p == "fossil" {
			out = append(out, p)
		}
	}
	return out
}

// ---- the real adapters ----

// RealProjectTree runs the tree probe. Skip is config.skip (extra folders that are not listed deeply).
type RealProjectTree struct{ Skip []string }

// ListDir implements ProjectTree.
func (t RealProjectTree) ListDir(root, dir string, depth int) ([]tree.Entry, error) {
	return tree.ListDir(root, dir, depth, t.Skip...)
}

// ReadFile implements ProjectTree.
func (RealProjectTree) ReadFile(root, rel string, from, to int) (tree.FileText, error) {
	return tree.ReadFile(root, rel, from, to)
}

// RawPath implements ProjectTree.
func (RealProjectTree) RawPath(root, rel string) (string, string, error) {
	return tree.RawPath(root, rel)
}

// RealProjectVCS runs the git and fossil probes.
type RealProjectVCS struct{}

// Commits implements ProjectVCS.
func (RealProjectVCS) Commits(ctx context.Context, vcs string, p probe.Project, skip, limit int, path string) ([]probe.Commit, error) {
	switch vcs {
	case "git":
		return git.Commits(ctx, p, skip, limit, path)
	case "fossil":
		return fossil.Commits(ctx, p, skip, limit, path)
	}
	return nil, errors.New("unknown vcs " + vcs)
}

// Detail implements ProjectVCS.
func (RealProjectVCS) Detail(ctx context.Context, vcs string, p probe.Project, id string) (probe.CommitDetail, error) {
	switch vcs {
	case "git":
		return git.Detail(ctx, p, id)
	case "fossil":
		return fossil.Detail(ctx, p, id)
	}
	return probe.CommitDetail{}, errors.New("unknown vcs " + vcs)
}

// DirtyDiff implements ProjectVCS.
func (RealProjectVCS) DirtyDiff(ctx context.Context, vcs string, p probe.Project) (string, bool, error) {
	switch vcs {
	case "git":
		return git.DirtyDiff(ctx, p)
	case "fossil":
		return fossil.DirtyDiff(ctx, p)
	}
	return "", false, errors.New("unknown vcs " + vcs)
}

// RealProjectTranscripts reads transcripts with the claude probe. When Profiles is set, the
// file must also sit inside <profile>/projects (claude.CheckPath) before it is opened.
type RealProjectTranscripts struct{ Profiles []string }

// Transcript implements ProjectTranscripts.
func (t RealProjectTranscripts) Transcript(ctx context.Context, path string, offset int64, limit int, roles []string) (claude.Page, error) {
	if len(t.Profiles) > 0 {
		checked, err := claude.CheckPath(t.Profiles, path)
		if err != nil {
			return claude.Page{}, err
		}
		path = checked
	}
	return claude.Transcript(ctx, path, offset, limit, roles)
}

// ---- GET /api/projects/{p} ----

// pjSummary is the "project" object. Field names follow web/app/lib/types.ts (ProjectSummary).
type pjSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Group        string   `json:"group"`
	Path         string   `json:"path"`
	VCS          []string `json:"vcs"`
	Branch       string   `json:"branch"`
	LastChangeAt *string  `json:"lastChangeAt"`
	Hidden       bool     `json:"hidden"`
	Surveyed     bool     `json:"surveyed"`
	DirtyCount   int      `json:"dirtyCount"`
	Description  string   `json:"description"`
	Stack        []string `json:"stack"`
	Remote       string   `json:"remote"`
}

func (a *ProjectAPI) summary(row store.Project, dirty int) pjSummary {
	stack := row.Stack
	if stack == nil {
		stack = []string{}
	}
	return pjSummary{
		ID: row.ID, Name: row.Name, Group: row.Group, Path: row.Path,
		VCS: pjVCSList(row.VCS), Branch: row.Branch, LastChangeAt: a.isoPtr(row.LastChangeAt),
		Hidden: row.Hidden, Surveyed: !row.IndexedAt.IsZero(), DirtyCount: dirty,
		Description: row.Description, Stack: stack, Remote: row.Remote,
	}
}

type pjReadme struct {
	Path      string `json:"path"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

type pjOverview struct {
	Readme         *pjReadme             `json:"readme"`
	Counts         store.ProjCounts      `json:"counts"`
	Dirty          []probe.DirtyFile     `json:"dirty"`
	DirtyTruncated bool                  `json:"dirtyTruncated,omitempty"`
	Servers        []pjServer            `json:"servers"`
	Milestones     []store.ProjMilestone `json:"milestones"`
}

type pjOverviewReply struct {
	Project  pjSummary  `json:"project"`
	Overview pjOverview `json:"overview"`
}

const (
	pjReadmeMax      = 64 << 10 // bytes of README text in the overview
	pjOverviewDirtyN = 500      // dirty paths listed in the overview
)

func (a *ProjectAPI) handleOverview(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.lookup(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	st := a.d.Store
	counts, err := st.ProjCountsFor(ctx, pr.Row.ID)
	if err != nil {
		a.fail(w, r, "counts", err)
		return
	}
	dirty, err := st.ProjDirtyFiles(ctx, pr.Row.ID)
	if err != nil {
		a.fail(w, r, "dirty list", err)
		return
	}
	milestones, err := st.ProjMilestones(ctx, pr.Row.ID)
	if err != nil {
		a.fail(w, r, "milestones", err)
		return
	}
	ov := pjOverview{Counts: counts, Dirty: dirty, Milestones: milestones}
	if len(ov.Dirty) > pjOverviewDirtyN {
		ov.Dirty = ov.Dirty[:pjOverviewDirtyN]
		ov.DirtyTruncated = true
	}
	ov.Readme = a.readme(pr)
	ov.Servers = a.serversOf(ctx, pr.Row.ID)
	WriteJSON(w, http.StatusOK, pjOverviewReply{Project: a.summary(pr.Row, counts.Dirty), Overview: ov})
}

// readme reads the README live. It gives nil when there is none or it cannot be shown.
func (a *ProjectAPI) readme(pr pjProject) *pjReadme {
	if a.d.Tree == nil || pr.Row.ReadmePath == "" || a.rootCheck(pr) != nil {
		return nil
	}
	ft, err := a.d.Tree.ReadFile(pr.Root, pr.Row.ReadmePath, 1, 0)
	if err != nil || ft.Redacted || ft.Binary {
		return nil
	}
	text, cut := pjClipText(ft.Text, pjReadmeMax)
	return &pjReadme{Path: ft.Path, Text: text, Truncated: cut || ft.Truncated}
}

// ---- GET /api/projects/{p}/activity ----

func (a *ProjectAPI) handleActivity(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.lookup(w, r)
	if !ok {
		return
	}
	rng := 30
	if v := r.URL.Query().Get("range"); v != "" {
		switch v {
		case "7":
			rng = 7
		case "30":
			rng = 30
		default:
			BadRequest(w, "range must be 7 or 30")
			return
		}
	}
	days, err := a.d.Store.ProjActivityAt(r.Context(), pr.Row.ID, rng, a.loc, a.now())
	if err != nil {
		a.fail(w, r, "activity", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"range": rng, "days": days})
}

// ---- GET /api/projects/{p}/docs ----

type pjDoc struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	MTime string `json:"mtime"`
	Lines int    `json:"lines"`
}

func (a *ProjectAPI) handleDocs(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.lookup(w, r)
	if !ok {
		return
	}
	rows, err := a.d.Store.ProjDocs(r.Context(), pr.Row.ID)
	if err != nil {
		a.fail(w, r, "docs", err)
		return
	}
	docs := make([]pjDoc, 0, len(rows))
	for _, d := range rows {
		title := d.Title
		if title == "" {
			title = path.Base(d.Path)
		}
		docs = append(docs, pjDoc{Path: d.Path, Title: title, MTime: a.iso(d.MTime), Lines: d.Lines})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"docs": docs})
}

// pjNatLess orders ids such as "T-2" before "T-10".
func pjNatLess(x, y string) bool {
	i := strings.LastIndexAny(x, "-")
	j := strings.LastIndexAny(y, "-")
	if i >= 0 && j >= 0 && x[:i] == y[:j] {
		nx, ex := pjAtoi(x[i+1:])
		ny, ey := pjAtoi(y[j+1:])
		if ex == nil && ey == nil && nx != ny {
			return nx < ny
		}
	}
	return x < y
}

func pjAtoi(s string) (int, error) {
	if s == "" || len(s) > 9 {
		return 0, errors.New("not a number")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
