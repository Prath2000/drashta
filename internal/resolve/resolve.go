package resolve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/servers"
	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

// Errors. The HTTP layer maps ErrNotFound to 404 and ErrInvalid / ErrForbidden to 400.
var (
	ErrNotFound  = errors.New("resolve: not found")
	ErrInvalid   = errors.New("resolve: invalid ref")
	ErrForbidden = errors.New("resolve: path is outside the project")
)

// Options configure a Resolver.
type Options struct {
	DB      *store.DB      // the index (required)
	Actions actions.Config // editor, terminal and allowed roots, for nativeActions and /open
	Home    string         // home folder; used to find a Claude profile folder from its name
}

// Resolver turns refs into previews, titles and native-action targets.
type Resolver struct {
	db   *store.DB
	mu   sync.RWMutex // guards cfg (SetEditor may change the editor at run time)
	cfg  actions.Config
	home string
}

// New makes a Resolver.
func New(o Options) *Resolver {
	return &Resolver{db: o.DB, cfg: o.Actions, home: o.Home}
}

// Actions returns the current actions config (a copy).
func (r *Resolver) Actions() actions.Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c := r.cfg
	c.Editor = append([]string{}, r.cfg.Editor...)
	c.Roots = append([]string{}, r.cfg.Roots...)
	return c
}

// SetEditor replaces the editor argv template used for nativeActions and /open
// (PATCH /api/settings {editorCmd}). The caller validates argv.
func (r *Resolver) SetEditor(argv []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg.Editor = append([]string{}, argv...)
}

// NativeAction is one "Open" choice of an item. Command is the command line that would run;
// it is only text for a tooltip and is never executed by resolve.
type NativeAction struct {
	Action  string `json:"action"`
	Label   string `json:"label"`
	Command string `json:"command"`
}

// Resolved is the reply of GET /api/resolve.
type Resolved struct {
	Ref           string         `json:"ref"`
	Type          string         `json:"type"`
	ProjectID     string         `json:"projectId"`
	Title         string         `json:"title"`
	Subtitle      string         `json:"subtitle"`
	Icon          string         `json:"icon"`
	Date          *string        `json:"date"`
	Obj           any            `json:"obj"`
	NativeLabel   string         `json:"nativeLabel"`
	NativeActions []NativeAction `json:"nativeActions"`
	Notes         []Note         `json:"notes"`
	Tasks         []Task         `json:"tasks"`
	Light         bool           `json:"light"`
}

// Summary is the short form used for task links: title, subtitle and date.
type Summary struct {
	Title    string  `json:"title"`
	Subtitle string  `json:"subtitle"`
	Date     *string `json:"date"`
}

type actSpec struct{ action, label string }

// item is what lookup finds for one ref.
type item struct {
	ref      authored.Ref // qualified
	typ      string       // type for the reply (file, image, doc, ...)
	title    string
	subtitle string
	icon     string
	date     *string
	obj      any
	target   actions.Target
	acts     []actSpec
}

// ---- projects ----

func (r *Resolver) projects(ctx context.Context) (map[string]store.Project, error) {
	list, err := r.db.ProjectList(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.Project, len(list))
	for _, p := range list {
		out[p.ID] = p
	}
	return out, nil
}

// Project returns one project row.
func (r *Resolver) Project(ctx context.Context, id string) (store.Project, error) {
	ps, err := r.projects(ctx)
	if err != nil {
		return store.Project{}, err
	}
	p, ok := ps[id]
	if !ok || id == "" {
		return store.Project{}, fmt.Errorf("%w: project %q", ErrNotFound, id)
	}
	return p, nil
}

// Projects returns every project row (hidden ones too).
func (r *Resolver) Projects(ctx context.Context) ([]store.Project, error) {
	return r.db.ProjectList(ctx)
}

// Root returns the folder of a project.
func (r *Resolver) Root(ctx context.Context, id string) (string, error) {
	p, err := r.Project(ctx, id)
	if err != nil {
		return "", err
	}
	return p.Path, nil
}

// Board loads one project's tracker from its .drashta files, plus the auto links of the index.
// A bad file never stops the others (see authored.LoadProject).
func (r *Resolver) Board(ctx context.Context, project string) (*Board, error) {
	root, err := r.Root(ctx, project)
	if err != nil {
		return nil, err
	}
	tr, _ := authored.LoadProject(root)
	auto, err := r.db.TrkAutoLinks(ctx, project)
	if err != nil {
		return nil, err
	}
	return NewBoard(project, root, tr, auto), nil
}

// ---- refs ----

// Qualify parses s and fills in its project. A local ref takes defaultProject. When that
// is empty too, a session ref takes the project of the session and a server ref the project
// of the server; any other local ref is invalid. The project must exist.
func (r *Resolver) Qualify(ctx context.Context, s, defaultProject string) (authored.Ref, error) {
	ref, err := ParseRef(s)
	if err != nil {
		return authored.Ref{}, err
	}
	if ref.Type == authored.RefProject {
		ref.Project = ref.ID
	}
	if ref.Project == "" {
		ref.Project = defaultProject
	}
	if ref.Project == "" {
		switch ref.Type {
		case authored.RefSession:
			sess, ok, err := r.db.TrkSession(ctx, ref.ID)
			if err != nil {
				return authored.Ref{}, err
			}
			if !ok {
				return authored.Ref{}, fmt.Errorf("%w: session %s", ErrNotFound, ref.ID)
			}
			ref.Project = sess.ProjectID
		case authored.RefServer, authored.RefServerKey:
			srv, ok, err := r.findServer(ctx, ref)
			if err != nil {
				return authored.Ref{}, err
			}
			if !ok {
				return authored.Ref{}, fmt.Errorf("%w: server %s", ErrNotFound, ref.ID)
			}
			ref.Project = srv.ProjectID
		default:
			return authored.Ref{}, fmt.Errorf("%w: ref %q needs a project (use <project>:%s)", ErrInvalid, s, s)
		}
		return ref, nil // a session or server may have no project
	}
	if _, err := r.Project(ctx, ref.Project); err != nil {
		return authored.Ref{}, err
	}
	return ref, nil
}

// ---- public API ----

// Resolve turns a ref into its preview: title, subtitle, icon, date, the object itself,
// the native actions (with the command text), and the notes and tasks on it.
func (r *Resolver) Resolve(ctx context.Context, ref string) (Resolved, error) {
	q, err := r.Qualify(ctx, ref, "")
	if err != nil {
		return Resolved{}, err
	}
	it, err := r.lookup(ctx, q)
	if err != nil {
		return Resolved{}, err
	}
	out := Resolved{
		Ref: q.String(), Type: it.typ, ProjectID: q.Project, Title: it.title, Subtitle: it.subtitle,
		Icon: it.icon, Date: it.date, Obj: it.obj, NativeActions: []NativeAction{}, Notes: []Note{}, Tasks: []Task{},
	}
	cfg := r.Actions()
	for _, a := range it.acts {
		cmd, err := cfg.Plan(it.target, a.action)
		if err != nil {
			continue
		}
		out.NativeActions = append(out.NativeActions, NativeAction{Action: a.action, Label: a.label, Command: readable(cmd)})
	}
	if len(out.NativeActions) > 0 {
		out.NativeLabel = out.NativeActions[0].Label
	}
	if notes, err := r.NotesFor(ctx, q); err == nil {
		out.Notes = notes
	}
	if tasks, err := r.TasksFor(ctx, q); err == nil {
		out.Tasks = tasks
	}
	return out, nil
}

// Summary gives title, subtitle and date of a ref. defaultProject qualifies a local ref.
func (r *Resolver) Summary(ctx context.Context, ref, defaultProject string) (Summary, error) {
	q, err := r.Qualify(ctx, ref, defaultProject)
	if err != nil {
		return Summary{}, err
	}
	it, err := r.lookup(ctx, q)
	if err != nil {
		return Summary{}, err
	}
	return Summary{Title: it.title, Subtitle: it.subtitle, Date: it.date}, nil
}

// Title gives the title and subtitle of a ref (for the inbox). The ref must carry its project
// (or be a session or server ref). When the item cannot be found, the title is the ref itself.
func (r *Resolver) Title(ctx context.Context, ref string) (title, subtitle string) {
	s, err := r.Summary(ctx, ref, "")
	if err != nil {
		return ref, ""
	}
	return s.Title, s.Subtitle
}

// Target builds the actions.Target for POST /api/open: project root plus a RELATIVE path,
// line, sha, vcs, session id, profile folder or port. It runs nothing.
func (r *Resolver) Target(ctx context.Context, ref string) (actions.Target, error) {
	q, err := r.Qualify(ctx, ref, "")
	if err != nil {
		return actions.Target{}, err
	}
	it, err := r.lookup(ctx, q)
	if err != nil {
		return actions.Target{}, err
	}
	return it.target, nil
}

// NotesFor returns the notes on ref (a qualified ref). Notes live in the ref's project.
func (r *Resolver) NotesFor(ctx context.Context, ref authored.Ref) ([]Note, error) {
	out := []Note{}
	if ref.Project == "" {
		return out, nil
	}
	b, err := r.Board(ctx, ref.Project)
	if err != nil {
		return out, err
	}
	for _, n := range b.NotesOn(ref) {
		out = append(out, b.NoteView(n))
	}
	return out, nil
}

// TasksFor returns the tasks that link to ref (a qualified ref): manual links in the task
// files and auto links in the index, without the links the user removed.
func (r *Resolver) TasksFor(ctx context.Context, ref authored.Ref) ([]Task, error) {
	out := []Task{}
	if ref.Project == "" {
		return out, nil
	}
	boards := map[string]*Board{}
	board := func(p string) *Board {
		if b, ok := boards[p]; ok {
			return b
		}
		b, err := r.Board(ctx, p)
		if err != nil {
			b = nil
		}
		boards[p] = b
		return b
	}
	type key struct{ p, t string }
	cands := []key{}
	seen := map[key]bool{}
	add := func(k key) {
		if !seen[k] {
			seen[k] = true
			cands = append(cands, k)
		}
	}
	// Live manual links of the ref's own project (fresh even before a reindex).
	if b := board(ref.Project); b != nil {
		for _, t := range b.Tracker.Tasks {
			if linksTo(b, t.Links, ref) {
				add(key{ref.Project, t.ID})
			}
		}
	}
	rows, err := r.db.TrkLinksByType(ctx, ref.Project, canonType(ref.Type))
	if err != nil {
		return out, err
	}
	if canonType(ref.Type) == authored.RefFile {
		more, err := r.db.TrkLinksByType(ctx, ref.Project, authored.RefImage)
		if err != nil {
			return out, err
		}
		rows = append(rows, more...)
	}
	for _, l := range rows {
		lr, err := ParseRef(l.Ref)
		if err != nil {
			continue
		}
		if lr.Project == "" {
			lr.Project = l.ProjectID
		}
		if SameItem(lr, ref) {
			add(key{l.ProjectID, l.TaskID})
		}
	}
	for _, k := range cands {
		b := board(k.p)
		if b == nil {
			continue
		}
		t, ok := b.Task(k.t)
		if !ok {
			continue
		}
		manual := linksTo(b, t.Links, ref)
		auto := false
		for _, a := range b.Auto[t.ID] {
			if refMatches(b, a.Ref, ref) {
				auto = true
			}
		}
		if !manual && (!auto || linksTo(b, t.Unlinks, ref)) {
			continue
		}
		out = append(out, b.TaskView(t))
	}
	return out, nil
}

func refMatches(b *Board, stored string, ref authored.Ref) bool {
	lr, err := ParseRef(stored)
	if err != nil {
		return false
	}
	if lr.Project == "" {
		lr.Project = b.ProjectID
	}
	return SameItem(lr, ref)
}

func linksTo(b *Board, list []string, ref authored.Ref) bool {
	for _, s := range list {
		if refMatches(b, s, ref) {
			return true
		}
	}
	return false
}

// ---- lookup per type ----

func (r *Resolver) lookup(ctx context.Context, q authored.Ref) (item, error) {
	it := item{ref: q, typ: q.Type, icon: q.Type}
	var root string
	if q.Project != "" {
		p, err := r.Project(ctx, q.Project)
		if err != nil {
			return item{}, err
		}
		root = p.Path
		it.target.ProjectRoot = root
	}
	it.target.Ref = q.String()
	switch q.Type {
	case authored.RefProject:
		return r.lookupProject(ctx, it)
	case authored.RefFile, authored.RefImage:
		return r.lookupFile(ctx, it, root)
	case authored.RefCommit:
		return r.lookupCommit(ctx, it)
	case authored.RefSession:
		return r.lookupSession(ctx, it)
	case authored.RefServer, authored.RefServerKey:
		return r.lookupServer(ctx, it)
	case authored.RefTask, authored.RefFeature, authored.RefMilestone, authored.RefSnapshot, authored.RefNote:
		return r.lookupAuthored(ctx, it)
	}
	return item{}, fmt.Errorf("%w: type %q", ErrInvalid, q.Type)
}

func (r *Resolver) lookupProject(ctx context.Context, it item) (item, error) {
	p, err := r.Project(ctx, it.ref.Project)
	if err != nil {
		return item{}, err
	}
	it.title = p.Name
	if it.title == "" {
		it.title = p.ID
	}
	it.subtitle = strings.Trim(p.Group+" · "+p.Description, " ·")
	it.date = isoOf(p.LastChangeAt)
	it.obj = p
	it.target.Type = actions.TypeProject
	it.target.URL = p.Remote // only used by "safari", and only for a GitHub remote
	it.acts = []actSpec{{actions.ActionFinder, revealLabel()}, {actions.ActionEditor, "Open in editor"},
		{actions.ActionTerminal, "Open in Terminal"}, {actions.ActionSafari, "Open on GitHub"}}
	return it, nil
}

// FileObj is the obj of a resolved file, image or doc.
type FileObj struct {
	Path   string   `json:"path"`
	Dir    bool     `json:"dir"`
	Size   int64    `json:"size"`
	MTime  *string  `json:"mtime"`
	Kind   string   `json:"kind"`
	Lines  int      `json:"lines,omitempty"`
	Title  string   `json:"title,omitempty"`
	Line   int      `json:"line,omitempty"`
	LineTo int      `json:"lineTo,omitempty"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	URL    string   `json:"url,omitempty"`
	Exists bool     `json:"exists"`
}

// appLabel names the "open <path>" action: whatever app macOS has for the file.
const appLabel = "Open in default app"

func (r *Resolver) lookupFile(ctx context.Context, it item, root string) (item, error) {
	rel := path.Clean(it.ref.ID)
	obj := FileObj{Path: rel, Kind: tree.Classify(path.Base(rel))}
	row, indexed, err := r.db.TrkFile(ctx, it.ref.Project, rel)
	if err != nil {
		return item{}, err
	}
	full, gerr := pathguard.Resolve(root, rel)
	switch {
	case gerr == nil:
		fi, err := os.Stat(full)
		if err != nil {
			return item{}, fmt.Errorf("%w: %s", ErrNotFound, rel)
		}
		obj.Exists, obj.Dir, obj.Size, obj.MTime = true, fi.IsDir(), fi.Size(), isoOf(fi.ModTime())
		if fi.IsDir() {
			obj.Kind = "folder"
		}
	case errors.Is(gerr, pathguard.ErrOutside):
		return item{}, fmt.Errorf("%w: %s", ErrForbidden, rel)
	case !indexed:
		return item{}, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	if indexed {
		obj.Lines, obj.Title = row.Lines, row.Title
		if row.Kind != "" && !obj.Dir {
			obj.Kind = row.Kind
		}
		if obj.MTime == nil {
			obj.MTime = isoOf(row.MTime)
			obj.Size = row.Size
		}
	}
	if from, to, ok := it.ref.Lines(); ok {
		obj.Line, obj.LineTo = from, to
		it.target.Line = from
	}
	if x, y, ok := it.ref.Point(); ok {
		obj.X, obj.Y = &x, &y
	}
	it.title = path.Base(rel)
	if obj.Title != "" {
		it.title = obj.Title
	}
	it.subtitle = strings.TrimSpace(it.ref.Project + " / " + rel)
	if obj.Line > 0 {
		it.subtitle += " · L" + strconv.Itoa(obj.Line)
		if obj.LineTo > obj.Line {
			it.subtitle += "-L" + strconv.Itoa(obj.LineTo)
		}
	}
	it.date = obj.MTime
	it.target.Path = rel
	switch {
	case obj.Dir:
		it.typ, it.icon = "folder", "folder"
		it.target.Type = actions.TypeFolder
		it.acts = []actSpec{{actions.ActionApp, "Open in " + fileManager()}, {actions.ActionTerminal, "Open in Terminal"}}
	case obj.Kind == tree.KindImage || it.ref.Type == authored.RefImage:
		it.typ, it.icon = "image", "image"
		obj.URL = RawURL(it.ref.Project, rel)
		it.target.Type = actions.TypeImage
		it.acts = []actSpec{{actions.ActionDefault, "Open in Preview"}, {actions.ActionApp, appLabel}, {actions.ActionFinder, revealLabel()}}
	default:
		it.typ, it.icon = "file", "file"
		if obj.Kind == tree.KindDoc {
			it.typ, it.icon = "doc", "doc"
		}
		it.target.Type = actions.TypeFile
		if obj.Kind == tree.KindCode || obj.Kind == tree.KindDoc {
			it.acts = []actSpec{{actions.ActionEditor, "Open in editor"}, {actions.ActionApp, appLabel}, {actions.ActionFinder, revealLabel()}}
		} else {
			// Media, PDF, office files, archives: the app macOS picks comes first.
			it.acts = []actSpec{{actions.ActionApp, appLabel}, {actions.ActionEditor, "Open in editor"}, {actions.ActionFinder, revealLabel()}}
		}
	}
	it.obj = obj
	return it, nil
}

func (r *Resolver) lookupCommit(ctx context.Context, it item) (item, error) {
	c, ok, err := r.db.TrkCommit(ctx, it.ref.Project, it.ref.ID)
	if err != nil {
		return item{}, err
	}
	if !ok {
		return item{}, fmt.Errorf("%w: commit %s", ErrNotFound, it.ref.ID)
	}
	if c.Files == nil {
		c.Files = []probe.CommitFile{}
	}
	it.title = c.Msg
	it.subtitle = short(c.ID, 7) + " · " + c.Author
	if c.Plus+c.Minus > 0 {
		it.subtitle += fmt.Sprintf(" · +%d -%d", c.Plus, c.Minus)
	}
	it.date = isoOf(c.Time)
	it.obj = c
	it.target.Type, it.target.SHA, it.target.VCS = actions.TypeCommit, c.ID, c.VCS
	it.acts = []actSpec{{actions.ActionTerminal, "Show in Terminal"}}
	return it, nil
}

func (r *Resolver) lookupSession(ctx context.Context, it item) (item, error) {
	s, ok, err := r.db.TrkSession(ctx, it.ref.ID)
	if err != nil {
		return item{}, err
	}
	if !ok {
		return item{}, fmt.Errorf("%w: session %s", ErrNotFound, it.ref.ID)
	}
	it.title = s.Title
	if it.title == "" {
		it.title = "Session " + short(s.ID, 8)
	}
	it.subtitle = fmt.Sprintf("%s · %d turns", strings.TrimPrefix(s.Profile, "."), s.Turns)
	it.date = isoOf(s.End)
	if it.date == nil {
		it.date = isoOf(s.Start)
	}
	it.obj = map[string]any{
		"id": s.ID, "profile": s.Profile, "cwd": s.CWD, "projectId": s.ProjectID, "start": isoOf(s.Start),
		"end": isoOf(s.End), "title": s.Title, "turns": s.Turns, "kb": s.KB, "tools": s.Tools, "edited": s.Edited,
	}
	it.target.Type, it.target.SessionID, it.target.Profile = actions.TypeSession, s.ID, r.profileDir(s)
	if it.target.ProjectRoot == "" {
		it.target.ProjectRoot = s.CWD
	}
	it.acts = []actSpec{{actions.ActionResume, "Resume in Terminal"}}
	return it, nil
}

// profileDir finds the Claude profile folder (CLAUDE_CONFIG_DIR) of a session.
func (r *Resolver) profileDir(s store.Session) string {
	if filepath.IsAbs(s.Profile) {
		return filepath.Clean(s.Profile)
	}
	if i := strings.Index(s.JSONLPath, string(filepath.Separator)+"projects"+string(filepath.Separator)); i > 0 {
		return s.JSONLPath[:i]
	}
	if r.home != "" && s.Profile != "" {
		return filepath.Join(r.home, s.Profile)
	}
	return ""
}

type serverRow struct {
	Key       string  `json:"key"`
	RefKey    string  `json:"refKey"`
	Port      int     `json:"port"`
	PID       int     `json:"pid"`
	Name      string  `json:"name"`
	Cmd       string  `json:"cmd"`
	CWD       string  `json:"cwd"`
	ProjectID string  `json:"projectId"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	Live      bool    `json:"live"`
	Since     *string `json:"since"`
	LastSeen  *string `json:"lastSeen"`
}

func (r *Resolver) findServer(ctx context.Context, ref authored.Ref) (serverRow, bool, error) {
	match := func(key string, port int) bool {
		if ref.Type == authored.RefServerKey {
			return strings.EqualFold(servers.RefKey(key), "server-key:"+ref.ID)
		}
		return strconv.Itoa(port) == ref.ID
	}
	live, err := r.db.TrkServersNow(ctx)
	if err != nil {
		return serverRow{}, false, err
	}
	for _, s := range live {
		if match(s.Key, s.Port) && (ref.Project == "" || s.ProjectID == "" || s.ProjectID == ref.Project) {
			return serverRow{Key: s.Key, RefKey: servers.RefKey(s.Key), Port: s.Port, PID: s.PID, Name: s.Name,
				Cmd: s.Cmd, CWD: s.CWD, ProjectID: s.ProjectID, Kind: s.Kind, Title: s.Title, Live: true,
				Since: isoOf(s.FirstSeen), LastSeen: isoOf(s.LastSeen)}, true, nil
		}
	}
	runs, err := r.db.ListServerRuns(ctx)
	if err != nil {
		return serverRow{}, false, err
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Started.After(runs[j].Started) })
	for _, s := range runs {
		if match(s.Key, s.Port) && (ref.Project == "" || s.ProjectID == "" || s.ProjectID == ref.Project) {
			return serverRow{Key: s.Key, RefKey: servers.RefKey(s.Key), Port: s.Port, PID: s.PID, Name: s.Name,
				Cmd: s.Cmd, CWD: s.CWD, ProjectID: s.ProjectID, Kind: s.Kind, Live: false,
				Since: isoOf(s.Started), LastSeen: isoOf(s.LastSeen)}, true, nil
		}
	}
	return serverRow{}, false, nil
}

func (r *Resolver) lookupServer(ctx context.Context, it item) (item, error) {
	s, ok, err := r.findServer(ctx, it.ref)
	if err != nil {
		return item{}, err
	}
	if !ok {
		return item{}, fmt.Errorf("%w: server %s", ErrNotFound, it.ref.ID)
	}
	it.typ, it.icon = authored.RefServer, authored.RefServer
	it.title = s.Title
	if it.title == "" {
		it.title = s.Name
	}
	it.subtitle = ":" + strconv.Itoa(s.Port) + " · " + s.Cmd
	if !s.Live {
		it.subtitle += " · stopped"
	}
	it.date = s.Since
	it.obj = s
	it.target.Type, it.target.Port = actions.TypeServer, strconv.Itoa(s.Port)
	it.acts = []actSpec{{actions.ActionSafari, "Open in Safari"}}
	return it, nil
}

func (r *Resolver) lookupAuthored(ctx context.Context, it item) (item, error) {
	b, err := r.Board(ctx, it.ref.Project)
	if err != nil {
		return item{}, err
	}
	nf := fmt.Errorf("%w: %s %s", ErrNotFound, it.ref.Type, it.ref.ID)
	editor := []actSpec{{actions.ActionEditor, "Open in editor"}}
	switch it.ref.Type {
	case authored.RefTask:
		t, ok := b.Task(it.ref.ID)
		if !ok {
			return item{}, nf
		}
		it.title = t.Title
		it.subtitle = t.ID + " · " + t.Status
		if f, ok := b.Feature(t.Feature); ok {
			it.subtitle += " · " + f.Title
		}
		it.date = isoTime(firstNonEmpty(t.Updated, t.Created))
		it.obj = b.TaskView(t)
		it.target.Type, it.target.Path = actions.TypeTask, ".drashta/tasks/"+t.ID+".md"
		it.acts = editor
	case authored.RefFeature:
		f, ok := b.Feature(it.ref.ID)
		if !ok {
			return item{}, nf
		}
		it.title, it.subtitle = f.Title, f.ID
		it.date = isoTime(f.Created)
		it.obj = b.FeatureView(f)
		it.target.Type, it.target.Path = actions.TypeTask, ".drashta/features/"+f.ID+".md"
		it.acts = editor
	case authored.RefMilestone:
		m, ok := b.Milestone(it.ref.ID)
		if !ok {
			return item{}, nf
		}
		it.title, it.subtitle = m.Title, m.ID+" · "+m.State
		if m.Due != "" {
			it.subtitle += " · due " + m.Due
		}
		it.date = isoTime(firstNonEmpty(m.Due, m.Created))
		it.obj = b.MilestoneView(m)
		it.target.Type, it.target.Path = actions.TypeTask, ".drashta/milestones/"+m.ID+".md"
		it.acts = editor
	case authored.RefSnapshot:
		s, ok := b.Snapshot(it.ref.ID)
		if !ok {
			return item{}, nf
		}
		it.title = s.Title
		if it.title == "" {
			it.title = s.ID
		}
		it.subtitle = fmt.Sprintf("%s v%d · %s", s.Task, s.Version, s.Verdict)
		it.date = isoTime(s.Captured)
		it.obj = b.SnapshotView(s)
		if s.Image != "" {
			it.target.Type, it.target.Path = actions.TypeSnapshot, s.ImageRel()
			it.acts = []actSpec{{actions.ActionDefault, "Open in Preview"}, {actions.ActionFinder, revealLabel()}}
		} else {
			it.target.Type, it.target.Path = actions.TypeNote, ".drashta/snapshots/"+s.ID+".md"
			it.acts = editor
		}
	case authored.RefNote:
		n, ok := b.Note(it.ref.ID)
		if !ok {
			return item{}, nf
		}
		it.title = firstLine(n.Body, 80)
		if it.title == "" {
			it.title = n.ID
		}
		it.subtitle = n.ID + " · " + n.Author + " · on " + n.Ref
		it.date = isoTime(n.Created)
		it.obj = b.NoteView(n)
		it.target.Type, it.target.Path = actions.TypeNote, ".drashta/notes/"+n.ID+".md"
		it.acts = editor
	}
	return it, nil
}

// readable turns a planned command into text for a tooltip. A Terminal command (osascript
// with the fixed script) is shown as "Terminal: cd <dir> && <argv>".
func readable(c actions.Command) string {
	if c.Name == "osascript" {
		for i, a := range c.Args {
			if a == "--" && i+1 < len(c.Args) {
				rest := strings.Join(c.Args[i+2:], " ")
				return "Terminal: cd " + c.Args[i+1] + " && " + rest
			}
		}
	}
	return c.String()
}

func short(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string, max int) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if r := []rune(line); len(r) > max {
				return string(r[:max])
			}
			return line
		}
	}
	return ""
}

// fileManager names the OS file manager for tooltips.
func fileManager() string {
	if runtime.GOOS == "windows" {
		return "Explorer"
	}
	return "Finder"
}

func revealLabel() string { return "Reveal in " + fileManager() }
