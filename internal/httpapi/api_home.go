package httpapi

// Home area of the API (04-data-model.md section 6, "Home and activity"):
//
//	GET   /api/boot       projects, counts, statuses, profiles, author
//	GET   /api/home       the seismograph (lanes x days)
//	GET   /api/day        one day: events, totals, top changed files
//	GET   /api/inbox      see api_inbox.go
//	GET   /api/search     see api_search.go
//	GET   /api/settings, PATCH /api/settings     see api_settings.go
//	POST  /api/refresh    see api_refresh.go
//
// RegisterHome wires all of them. Every other service comes in through the small interfaces below, so
// tests use fakes and main plugs in the real packages. Unexported names in the api_home/inbox/search/
// settings/refresh files start with "ah" or "home" so they cannot clash with other api_*.go files.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/store"
)

// HomeIndexer starts a rescan. Both calls may take a while. The indexer publishes the "scan" events itself.
type HomeIndexer interface {
	// Refresh rescans one project. withGH also refreshes its GitHub data.
	Refresh(ctx context.Context, projectID string, withGH bool)
	// RefreshAll rescans every project.
	RefreshAll(ctx context.Context)
}

// HomeNotifier publishes live events. *live.Hub is one (Server.Hub()).
type HomeNotifier interface {
	Publish(ev live.Event) uint64
}

// HomeResolver gives the title of the thing a ref points at. The ref is project-qualified
// ("ledger:file:main.go#L42"). An unknown ref gives an empty title.
type HomeResolver interface {
	Title(ctx context.Context, ref string) (title, subtitle string)
}

// HomeDeps is everything RegisterHome needs. Only Store is required.
type HomeDeps struct {
	// Store is the SQLite index (required).
	Store *store.DB
	// Config holds the hidden-folder list and the editor command (config.toml). Nil: only the store settings are used.
	Config HomeConfig
	// Indexer is used by POST /api/refresh. Nil: that route answers 503.
	Indexer HomeIndexer
	// Notifier receives the events this file publishes. Nil: Server.Hub().
	Notifier HomeNotifier
	// Resolver gives note target titles for the inbox. Nil: the title is the ref itself.
	Resolver HomeResolver
	// Loc is the zone for "today", day keys and printed times. Nil: time.Local.
	Loc *time.Location
	// Profiles are the Claude profile folders. Only the last element of each path is sent in /boot.
	Profiles []string
	// DefaultAuthor gives the author name when none is saved, for example GitUserName. Nil: "".
	DefaultAuthor func(ctx context.Context) string
	// Counts gives the numbers for /boot. Nil: the store query HomeBootCounts. Pass the same function that
	// feeds Options.Counts and the "counts" events, so the top bar never flickers between two definitions.
	Counts func(ctx context.Context) (live.Counts, error)
	// EmptyNotice explains an empty project list (first run). It is called by /boot only
	// when there are no projects. Nil: no notice.
	EmptyNotice func() string
	// OnSettings is called after a successful PATCH /api/settings with the new values. Optional.
	OnSettings func(HomeSettings)
}

// homeAPI holds the wired dependencies for the handlers.
type homeAPI struct {
	d    HomeDeps
	s    *Server
	loc  *time.Location
	now  func() time.Time
	log  *slog.Logger
	note HomeNotifier

	mu      sync.Mutex
	running map[string]bool // refreshes in flight; "" = refresh all
}

// RegisterHome registers the boot, home, day, inbox, search, settings and refresh routes.
// It panics when d.Store is nil (a wiring mistake).
func RegisterHome(s *Server, d HomeDeps) {
	if d.Store == nil {
		panic("httpapi: RegisterHome needs a Store")
	}
	deps := s.Deps()
	h := &homeAPI{d: d, s: s, loc: d.Loc, now: deps.Now, log: deps.Logger, note: d.Notifier, running: map[string]bool{}}
	if h.loc == nil {
		h.loc = time.Local
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	if h.note == nil {
		h.note = s.Hub()
	}
	s.Handle(http.MethodGet, "/api/boot", h.handleBoot, false)
	s.Handle(http.MethodGet, "/api/home", h.handleHome, false)
	s.Handle(http.MethodGet, "/api/day", h.handleDay, false)
	h.registerInbox()
	h.registerSearch()
	h.registerSettings()
	h.registerRefresh()
}

// ---------------------------------------------------------------------------
// shared helpers

// ahTimeLayout always prints a numeric offset ("+05:30", "+00:00"), never "Z".
const ahTimeLayout = "2006-01-02T15:04:05-07:00"

// ahTime formats t in loc. The zero time gives "".
func ahTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format(ahTimeLayout)
}

// ahTimePtr is ahTime that gives nil (JSON null) for the zero time.
func ahTimePtr(t time.Time, loc *time.Location) *string {
	if t.IsZero() {
		return nil
	}
	s := ahTime(t, loc)
	return &s
}

// ahAuthoredTime turns a time text from front matter into the API format. A text it cannot read is kept.
func ahAuthoredTime(s string, loc *time.Location) string {
	if t, ok := authored.ParseTime(s); ok {
		return ahTime(t, loc)
	}
	return s
}

// ahLocalRefTypes are the first segments that mean "this ref has no project part" (04 section 2.4).
var ahLocalRefTypes = map[string]bool{
	"project": true, "file": true, "image": true, "doc": true, "commit": true, "session": true,
	"server": true, "server-key": true, "task": true, "feature": true, "milestone": true,
	"snapshot": true, "note": true,
}

// ahQualify puts the project in front of a local ref ("commit:abc" becomes "ledger:commit:abc").
// A ref that already has a project part, or an empty ref, is returned as it is.
func ahQualify(project, ref string) string {
	if ref == "" || project == "" {
		return ref
	}
	if first, _, ok := strings.Cut(ref, ":"); ok && ahLocalRefTypes[first] {
		return project + ":" + ref
	}
	return ref
}

// ahPlain reports whether s has no control characters.
func ahPlain(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ahProfileNames reduces profile paths to their folder names, without duplicates.
func ahProfileNames(paths []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range paths {
		n := filepath.Base(strings.TrimRight(p, "/"+string(filepath.Separator)))
		if n == "" || n == "." || n == "/" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// ahSplitVCS turns "git,fossil" into ["git","fossil"]. No VCS gives an empty list.
func ahSplitVCS(v string) []string {
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ahStr gives nil (JSON null) for an empty string.
func ahStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---------------------------------------------------------------------------
// GET /api/boot

type ahBootProject struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Group        string   `json:"group"`
	Path         string   `json:"path"`
	VCS          []string `json:"vcs"`
	Branch       *string  `json:"branch"`
	LastChangeAt *string  `json:"lastChangeAt"`
	Hidden       bool     `json:"hidden"`
	Surveyed     bool     `json:"surveyed"`
	DirtyCount   int      `json:"dirtyCount"`
	FileCount    *int     `json:"fileCount,omitempty"` // omitted when the project was never walked
	Description  string   `json:"description"`
	Stack        []string `json:"stack"`
}

type ahBoot struct {
	Now        string           `json:"now"`
	Statuses   []string         `json:"statuses"`
	Profiles   []string         `json:"profiles"`
	Author     string           `json:"author"`
	Counts     store.HomeCounts `json:"counts"`
	LastScanAt *string          `json:"lastScanAt"`
	Projects   []ahBootProject  `json:"projects"`
	// Notice is set only when there are no projects: why, and what to change.
	Notice string `json:"notice,omitempty"`
}

func (h *homeAPI) handleBoot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projects, err := h.d.Store.HomeBootProjects(ctx)
	if err != nil {
		h.fail(w, "boot projects", err)
		return
	}
	counts, err := h.counts(ctx)
	if err != nil {
		h.fail(w, "boot counts", err)
		return
	}
	last, err := h.d.Store.HomeLastScan(ctx)
	if err != nil {
		h.fail(w, "boot last scan", err)
		return
	}
	out := ahBoot{
		Now:        ahTime(h.now(), h.loc),
		Statuses:   append([]string{}, authored.Statuses...),
		Profiles:   ahProfileNames(h.d.Profiles),
		Author:     h.author(ctx),
		Counts:     counts,
		LastScanAt: ahTimePtr(last, h.loc),
		Projects:   make([]ahBootProject, 0, len(projects)),
	}
	for _, p := range projects {
		stack := p.Stack
		if stack == nil {
			stack = []string{}
		}
		bp := ahBootProject{
			ID: p.ID, Name: p.Name, Group: p.Group, Path: p.Path,
			VCS: ahSplitVCS(p.VCS), Branch: ahStr(p.Branch), LastChangeAt: ahTimePtr(p.LastChangeAt, h.loc),
			Hidden: p.Hidden, Surveyed: p.Surveyed, DirtyCount: p.DirtyCount,
			Description: p.Description, Stack: stack,
		}
		if p.HasFileCount {
			n := p.FileCount
			bp.FileCount = &n
		}
		out.Projects = append(out.Projects, bp)
	}
	if len(out.Projects) == 0 && h.d.EmptyNotice != nil {
		out.Notice = h.d.EmptyNotice()
	}
	WriteJSON(w, http.StatusOK, out)
}

// counts reads the four top-bar numbers from the injected function or from the store.
func (h *homeAPI) counts(ctx context.Context) (store.HomeCounts, error) {
	if h.d.Counts != nil {
		c, err := h.d.Counts(ctx)
		return store.HomeCounts{LiveServers: c.LiveServers, OpenTasks: c.OpenTasks,
			AwaitingReview: c.AwaitingReview, OpenNotes: c.OpenNotes}, err
	}
	return h.d.Store.HomeBootCounts(ctx)
}

// author is the saved author name, else the default one.
func (h *homeAPI) author(ctx context.Context) string {
	if v, ok, err := h.d.Store.GetSetting(ctx, settingAuthor); err == nil && ok && strings.TrimSpace(v) != "" {
		return v
	}
	if h.d.DefaultAuthor != nil {
		return h.d.DefaultAuthor(ctx)
	}
	return ""
}

// fail logs an internal error and answers 500 without details.
func (h *homeAPI) fail(w http.ResponseWriter, what string, err error) {
	h.log.Error("api error", "what", what, "err", err)
	Internal(w, "could not read "+what)
}

// ---------------------------------------------------------------------------
// GET /api/home

type ahLane struct {
	ProjectID    string          `json:"projectId"`
	Hidden       bool            `json:"hidden"`
	Dirty        int             `json:"dirty"`
	Days         []store.DayCell `json:"days"`
	Live         store.Live      `json:"live"`
	LastChangeAt *string         `json:"lastChangeAt"`
}

type ahHome struct {
	Range int      `json:"range"`
	Days  []string `json:"days"`
	Lanes []ahLane `json:"lanes"`
}

func (h *homeAPI) handleHome(w http.ResponseWriter, r *http.Request) {
	rng := 30
	if q := r.URL.Query(); q.Has("range") {
		switch q.Get("range") {
		case "7":
			rng = 7
		case "30":
			rng = 30
		default:
			BadRequest(w, "range must be 7 or 30")
			return
		}
	}
	data, err := h.d.Store.HomeAt(r.Context(), rng, h.loc, h.now())
	if err != nil {
		h.fail(w, "home", err)
		return
	}
	out := ahHome{Range: data.Range, Days: data.Days, Lanes: make([]ahLane, 0, len(data.Lanes))}
	for _, l := range data.Lanes {
		out.Lanes = append(out.Lanes, ahLane{
			ProjectID: l.ProjectID, Hidden: l.Hidden, Dirty: l.Dirty, Days: l.Days, Live: l.Live,
			LastChangeAt: ahTimePtr(l.LastChangeAt, h.loc),
		})
	}
	WriteJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// GET /api/day

// ahEvent is one dated item in /day and in the inbox feed.
//
// Kind is the coarse kind the page draws a glyph for: commit, session, snapshot, note, task, file, server.
// Event is the fine kind stored by the indexer (session_start, task_status, verdict, ...).
type ahEvent struct {
	TS        string `json:"ts"`
	Kind      string `json:"kind"`
	Event     string `json:"event"`
	Ref       string `json:"ref"`
	Label     string `json:"label"`
	ProjectID string `json:"projectId"`
	Context   string `json:"context,omitempty"`
}

// ahCoarseKind maps the stored event kind to the page's kind.
func ahCoarseKind(kind string) string {
	switch kind {
	case "session_start", "session_end":
		return "session"
	case "server_up", "server_down":
		return "server"
	case "file_burst":
		return "file"
	case "task_created", "task_status":
		return "task"
	case "snapshot", "verdict":
		return "snapshot"
	}
	return kind
}

// ahEventContext gives the muted second line of an item. The indexer may write meta.context directly;
// otherwise it is built from meta.plus/minus (commit) or meta.profile (session).
func ahEventContext(kind string, meta json.RawMessage) string {
	if len(meta) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(meta, &m) != nil {
		return ""
	}
	if s, ok := m["context"].(string); ok {
		return s
	}
	switch kind {
	case "commit":
		plus, ok1 := m["plus"].(float64)
		minus, ok2 := m["minus"].(float64)
		if ok1 || ok2 {
			return fmt.Sprintf("+%d −%d", int(plus), int(minus))
		}
	case "session_start", "session_end":
		if s, ok := m["profile"].(string); ok {
			return s
		}
	}
	return ""
}

// ahSnapshotRefRe finds the task and version in a snapshot ref such as "snapshot:T-4/v2".
var ahSnapshotRefRe = regexp.MustCompile(`(?:^|:)snapshot:([^/:]+)/v([0-9]+)$`)

var ahVerdicts = map[string]bool{"pending": true, "approved": true, "changes": true}

// apiEvents turns stored events into page items. Items of the snapshot kind carry the verdict word
// (pending, approved or changes) in Context: the current verdict of that snapshot, else the one the
// indexer wrote into meta.verdict or the label, else "pending" for a new snapshot.
func (h *homeAPI) apiEvents(ctx context.Context, evs []store.Event) []ahEvent {
	out := make([]ahEvent, 0, len(evs))
	verdicts := map[string]string{} // "project|ref" -> verdict, so one snapshot is looked up once
	for _, e := range evs {
		item := ahEvent{
			TS: ahTime(e.TS, h.loc), Kind: ahCoarseKind(e.Kind), Event: e.Kind,
			Ref: ahQualify(e.ProjectID, e.Ref), Label: e.Label, ProjectID: e.ProjectID,
			Context: ahEventContext(e.Kind, e.Meta),
		}
		if item.Kind == "snapshot" {
			item.Context = h.snapshotVerdict(ctx, e, verdicts)
		}
		out = append(out, item)
	}
	return out
}

func (h *homeAPI) snapshotVerdict(ctx context.Context, e store.Event, cache map[string]string) string {
	if m := ahSnapshotRefRe.FindStringSubmatch(e.Ref); m != nil {
		key := e.ProjectID + "|" + e.Ref
		v, ok := cache[key]
		if !ok {
			version, _ := strconv.Atoi(m[2])
			if got, err := h.d.Store.HomeSnapshotVerdict(ctx, e.ProjectID, m[1], version); err == nil {
				v = got
			}
			cache[key] = v
		}
		if ahVerdicts[v] {
			return v
		}
	}
	var meta map[string]any
	if len(e.Meta) > 0 && json.Unmarshal(e.Meta, &meta) == nil {
		if v, ok := meta["verdict"].(string); ok && ahVerdicts[v] {
			return v
		}
	}
	if ahVerdicts[e.Label] {
		return e.Label
	}
	if e.Kind == "snapshot" {
		return "pending"
	}
	return ""
}

type ahDay struct {
	Date       string        `json:"date"`
	Events     []ahEvent     `json:"events"`
	Totals     store.DayCell `json:"totals"`
	Files      []string      `json:"files"`
	MoreFiles  int           `json:"moreFiles"`
	FilesTotal int           `json:"filesTotal"`
}

func (h *homeAPI) handleDay(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	date := q.Get("date")
	if len(date) != len(store.DayLayout) {
		BadRequest(w, "date must be YYYY-MM-DD")
		return
	}
	if _, err := time.Parse(store.DayLayout, date); err != nil {
		BadRequest(w, "date must be a real day in the form YYYY-MM-DD")
		return
	}
	project := q.Get("project")
	if len(project) > 200 || !ahPlain(project) {
		BadRequest(w, "bad project id")
		return
	}
	data, err := h.d.Store.Day(r.Context(), date, project)
	if err != nil {
		h.fail(w, "day", err)
		return
	}

	// A session that starts and ends on the same day is one item: keep the start.
	started := map[string]bool{}
	for _, e := range data.Events {
		if e.Kind == "session_start" {
			started[e.ProjectID+"\x00"+e.Ref] = true
		}
	}
	out := ahDay{Date: data.Date, Totals: data.Totals, Events: make([]ahEvent, 0, len(data.Events)),
		Files: make([]string, 0, len(data.Files)), FilesTotal: data.FilesTotal}
	kept := make([]store.Event, 0, len(data.Events))
	for _, e := range data.Events {
		if e.Kind == "session_end" && started[e.ProjectID+"\x00"+e.Ref] {
			continue
		}
		kept = append(kept, e)
	}
	out.Events = h.apiEvents(r.Context(), kept)
	for _, f := range data.Files {
		out.Files = append(out.Files, f.Path)
	}
	if project != "" && data.FilesTotal > len(out.Files) {
		out.MoreFiles = data.FilesTotal - len(out.Files)
	}
	WriteJSON(w, http.StatusOK, out)
}
