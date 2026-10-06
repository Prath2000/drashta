package httpapi

// Tracker API: tasks, features, milestones and links (04-data-model.md section 6 "Tracker").
// The .drashta files are the source of truth. Every write goes through internal/authored,
// then asks the Reindexer to mirror the file into the index and publishes a "tracker" event.
// Snapshots, notes, resolve and open live in api_snapshots.go, api_notes.go, api_resolve.go
// and api_open.go. RegisterTracker registers all of them.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/actions"
	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/resolve"
)

// Reindexer mirrors one changed .drashta file into the index (mirror tables + search) at once.
// relPath is relative to the project root, for example ".drashta/tasks/T-3.md". A path that
// no longer exists means "delete the row". The wiring adapts the indexer to this.
type Reindexer interface {
	ReindexAuthored(project, relPath string) error
}

// TrackerDeps is what RegisterTracker needs. Main builds it.
type TrackerDeps struct {
	// Resolver reads projects, boards, refs and targets (required). Its actions.Config
	// (editor, terminal, roots) is also used by POST /api/open.
	Resolver *resolve.Resolver
	// Reindex is called after every write. Nil means no reindex (the file watcher catches up).
	Reindex Reindexer
	// Exec runs native commands for POST /api/open. Nil means actions.ExecExecutor{}.
	// Tests pass *actions.RecorderExecutor.
	Exec actions.Executor
	// Author is the name written as author, reviewer and log writer (settings "author").
	// Nil or empty means "user".
	Author func() string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// HeadCommit gives the current commit of a project for a new snapshot ("" for none).
	// Nil means: ask git when the project has a .git folder.
	HeadCommit func(ctx context.Context, root string) string
	// DesktopState is the plash-mode state file (one word: the last mode set) read by
	// GET /api/desktop. "" means the mode is always unknown.
	DesktopState string
}

type trackerAPI struct {
	s *Server
	d TrackerDeps
}

// snapshotMaxBody is the body cap of the snapshot upload route: the 20 MB image plus room
// for the multipart framing and the small text fields.
const snapshotMaxBody = authored.MaxImageBytes + 1<<20

// RegisterTracker registers the tracker, snapshot, note, resolve and open routes.
func RegisterTracker(s *Server, d TrackerDeps) {
	if d.Resolver == nil {
		panic("httpapi: RegisterTracker needs a Resolver")
	}
	if d.Exec == nil {
		d.Exec = actions.ExecExecutor{}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.HeadCommit == nil {
		d.HeadCommit = trkGitHead
	}
	t := &trackerAPI{s: s, d: d}

	t.registerDesktop()
	s.Handle("GET", "/api/tracker", t.getTracker, false)
	s.Handle("GET", "/api/projects/{p}/tasks/{id}", t.getTask, false)
	s.Handle("POST", "/api/projects/{p}/tasks", t.createTask, true)
	s.Handle("PATCH", "/api/projects/{p}/tasks/{id}", t.patchTask, true)
	s.Handle("POST", "/api/projects/{p}/tasks/{id}/links", t.addLink, true)
	s.Handle("DELETE", "/api/projects/{p}/tasks/{id}/links", t.removeLink, true)
	s.Handle("POST", "/api/projects/{p}/features", t.createFeature, true)
	s.Handle("PATCH", "/api/projects/{p}/features/{id}", t.patchFeature, true)
	s.Handle("POST", "/api/projects/{p}/milestones", t.createMilestone, true)
	s.Handle("PATCH", "/api/projects/{p}/milestones/{id}", t.patchMilestone, true)
	for _, kind := range []string{"tasks", "features", "milestones"} {
		s.Handle("DELETE", "/api/projects/{p}/"+kind+"/{id}", t.deleteItem(kind), true)
	}

	t.registerSnapshots()
	t.registerNotes()
	t.registerResolve()
	t.registerOpen()
}

// ---- small helpers ----

func (t *trackerAPI) now() time.Time { return t.d.Now() }

func (t *trackerAPI) author() string {
	if t.d.Author != nil {
		if a := strings.TrimSpace(t.d.Author()); a != "" {
			return a
		}
	}
	return "user"
}

func (t *trackerAPI) log() *slog.Logger { return t.s.Deps().Logger }

// changed mirrors the written files into the index and publishes one tracker event.
func (t *trackerAPI) changed(project, kind, id string, rels ...string) {
	if t.d.Reindex != nil {
		for _, rel := range rels {
			if err := t.d.Reindex.ReindexAuthored(project, rel); err != nil {
				t.log().Warn("reindex after write failed", "project", project, "path", rel, "err", err)
			}
		}
	}
	t.s.Hub().Publish(live.TrackerEvent(project, kind, id))
}

// board loads the tracker of the project in the {p} path value.
func (t *trackerAPI) board(w http.ResponseWriter, r *http.Request) (*resolve.Board, bool) {
	b, err := t.d.Resolver.Board(r.Context(), r.PathValue("p"))
	if err != nil {
		trkFail(w, err)
		return nil, false
	}
	return b, true
}

// reload loads the board again after a write (for fresh views). Errors give nil.
func (t *trackerAPI) reload(ctx context.Context, project string) *resolve.Board {
	b, err := t.d.Resolver.Board(ctx, project)
	if err != nil {
		return nil
	}
	return b
}

var (
	trkTaskIDRe      = regexp.MustCompile(`^T-[0-9]{1,9}$`)
	trkFeatureIDRe   = regexp.MustCompile(`^F-[0-9]{1,9}$`)
	trkMilestoneIDRe = regexp.MustCompile(`^M-[0-9]{1,9}$`)
	trkNoteIDRe      = regexp.MustCompile(`^N-[0-9]{1,9}$`)
)

// trkID checks an id path value against re. It answers 400 itself when it does not match,
// so "../x" and other tricks never reach the file system.
func trkID(w http.ResponseWriter, r *http.Request, name string, re *regexp.Regexp) (string, bool) {
	id := r.PathValue(name)
	if !re.MatchString(id) {
		BadRequest(w, "bad id "+trkQuote(id))
		return "", false
	}
	return id, true
}

func trkQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// trkIfMatch reads the If-Match header (quotes and W/ removed). It answers 428 when missing.
func trkIfMatch(w http.ResponseWriter, r *http.Request) (string, bool) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	v = strings.TrimPrefix(v, "W/")
	v = strings.Trim(v, `"`)
	if v == "" {
		WriteError(w, http.StatusPreconditionRequired, "precondition_required", "send If-Match: <rev> (the rev you read)")
		return "", false
	}
	return v, true
}

// trkConflict answers 409 with the current item and its rev, so the page can merge or reload.
func trkConflict(w http.ResponseWriter, msg, rev string, current any) {
	WriteJSON(w, http.StatusConflict, map[string]any{
		"error":   errorDetail{Code: "conflict", Message: msg},
		"rev":     rev,
		"current": current,
	})
}

// trkFail maps an error to the reply (authored, resolve, actions and pathguard errors).
func trkFail(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		PayloadTooLarge(w, tooBig.Limit)
	case errors.Is(err, authored.ErrTooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", err.Error())
	case errors.Is(err, authored.ErrBadExt):
		UnsupportedMediaType(w, err.Error())
	case errors.Is(err, resolve.ErrNotFound), errors.Is(err, authored.ErrNotFound),
		errors.Is(err, actions.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		NotFound(w, err.Error())
	case errors.Is(err, authored.ErrConflict), errors.Is(err, authored.ErrExists):
		Conflict(w, err.Error())
	case errors.Is(err, authored.ErrInvalid):
		Unprocessable(w, err.Error())
	case errors.Is(err, authored.ErrLockTimeout):
		WriteError(w, http.StatusServiceUnavailable, "busy", err.Error())
	case errors.Is(err, resolve.ErrInvalid), errors.Is(err, resolve.ErrForbidden),
		errors.Is(err, resolve.ErrBadDropName), errors.Is(err, pathguard.ErrOutside),
		errors.Is(err, actions.ErrForbidden), errors.Is(err, actions.ErrInvalid),
		errors.Is(err, actions.ErrAction), errors.Is(err, actions.ErrConfirm), errors.Is(err, actions.ErrNotLive):
		BadRequest(w, err.Error())
	default:
		Internal(w, err.Error())
	}
}

// trkLocalRef parses a ref for storage in project p: the project segment is dropped when it
// is p itself. The ref must be valid.
func (t *trackerAPI) trkLocalRef(ctx context.Context, p, s string) (string, error) {
	ref, err := t.d.Resolver.Qualify(ctx, s, p)
	if err != nil {
		return "", err
	}
	if ref.Project == p && ref.Type != authored.RefProject {
		ref.Project = ""
	}
	return ref.String(), nil
}

// ---- GET /api/tracker ----

type trkTrackerReply struct {
	Milestones []resolve.Milestone `json:"milestones"`
	Features   []resolve.Feature   `json:"features"`
	Tasks      []resolve.Task      `json:"tasks"`
}

func (t *trackerAPI) getTracker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	filter := q.Get("filter")
	if filter != "" && filter != "all" && filter != "review" {
		BadRequest(w, "filter must be all or review")
		return
	}
	status := q.Get("status")
	if status != "" && !trkValidStatus(status) {
		BadRequest(w, "unknown status "+trkQuote(status))
		return
	}
	feature := q.Get("feature")

	var ids []string
	if p := q.Get("project"); p != "" {
		if _, err := t.d.Resolver.Project(ctx, p); err != nil {
			trkFail(w, err)
			return
		}
		ids = []string{p}
	} else {
		projects, err := t.d.Resolver.Projects(ctx)
		if err != nil {
			trkFail(w, err)
			return
		}
		for _, p := range projects {
			if p.Hidden {
				continue
			}
			if _, err := pathguard.Resolve(p.Path, ".drashta"); err != nil {
				continue // no tracker in this project
			}
			ids = append(ids, p.ID)
		}
	}

	out := trkTrackerReply{Milestones: []resolve.Milestone{}, Features: []resolve.Feature{}, Tasks: []resolve.Task{}}
	for _, id := range ids {
		b, err := t.d.Resolver.Board(ctx, id)
		if err != nil {
			trkFail(w, err)
			return
		}
		for _, m := range b.Tracker.Milestones {
			out.Milestones = append(out.Milestones, b.MilestoneView(m))
		}
		for _, f := range b.Tracker.Features {
			out.Features = append(out.Features, b.FeatureView(f))
		}
		for _, task := range b.Tracker.Tasks {
			if feature != "" && task.Feature != feature {
				continue
			}
			if status != "" && task.Status != status {
				continue
			}
			v := b.TaskView(task)
			if filter == "review" && !trkAwaitingReview(v) {
				continue
			}
			out.Tasks = append(out.Tasks, v)
		}
	}
	WriteJSON(w, http.StatusOK, out)
}

// trkAwaitingReview: the task is in review, or its latest snapshot waits for a verdict.
func trkAwaitingReview(v resolve.Task) bool {
	if v.Status == authored.StatusReview {
		return true
	}
	return v.LatestSnapshot != nil && v.LatestSnapshot.Verdict == authored.VerdictPending &&
		v.Status != authored.StatusDone
}

func trkValidStatus(s string) bool {
	for _, x := range authored.Statuses {
		if x == s {
			return true
		}
	}
	return false
}

// ---- GET /api/projects/{p}/tasks/{id} ----

type trkLinkView struct {
	Ref      string           `json:"ref"`
	Source   string           `json:"source"`
	Reason   string           `json:"reason,omitempty"`
	Resolved *resolve.Summary `json:"resolved"`
}

type trkTaskDetail struct {
	Task      resolve.Task       `json:"task"`
	Rev       string             `json:"rev"`
	Links     []trkLinkView      `json:"links"`
	Snapshots []resolve.Snapshot `json:"snapshots"`
	Notes     []resolve.Note     `json:"notes"`
	Log       []resolve.LogEntry `json:"log"`
}

func (t *trackerAPI) linkViews(ctx context.Context, b *resolve.Board, task authored.Task) []trkLinkView {
	out := []trkLinkView{}
	for _, l := range b.Links(task) {
		v := trkLinkView{Ref: b.FullRef(l.Ref), Source: l.Source, Reason: l.Reason}
		if s, err := t.d.Resolver.Summary(ctx, l.Ref, b.ProjectID); err == nil {
			v.Resolved = &s
		}
		out = append(out, v)
	}
	return out
}

func (t *trackerAPI) taskDetail(ctx context.Context, b *resolve.Board, task authored.Task) trkTaskDetail {
	d := trkTaskDetail{
		Task: b.TaskView(task), Rev: task.Rev, Links: t.linkViews(ctx, b, task),
		Snapshots: []resolve.Snapshot{}, Notes: []resolve.Note{}, Log: resolve.LogView(task),
	}
	for _, s := range b.SnapshotsOf(task.ID) {
		d.Snapshots = append(d.Snapshots, b.SnapshotView(s))
	}
	for _, n := range b.NotesOn(authored.Ref{Project: b.ProjectID, Type: authored.RefTask, ID: task.ID}) {
		d.Notes = append(d.Notes, b.NoteView(n))
	}
	return d
}

func (t *trackerAPI) getTask(w http.ResponseWriter, r *http.Request) {
	id, ok := trkID(w, r, "id", trkTaskIDRe)
	if !ok {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	task, found := b.Task(id)
	if !found {
		NotFound(w, "no task "+id)
		return
	}
	WriteJSON(w, http.StatusOK, t.taskDetail(r.Context(), b, task))
}

// ---- POST /api/projects/{p}/tasks ----

type trkTaskWrite struct {
	Task resolve.Task `json:"task"`
	Rev  string       `json:"rev"`
}

func (t *trackerAPI) taskReply(ctx context.Context, project string, task *authored.Task) trkTaskWrite {
	if b := t.reload(ctx, project); b != nil {
		if fresh, ok := b.Task(task.ID); ok {
			return trkTaskWrite{Task: b.TaskView(fresh), Rev: fresh.Rev}
		}
	}
	b := resolve.NewBoard(project, "", authored.Tracker{Tasks: []authored.Task{*task}}, nil)
	return trkTaskWrite{Task: b.TaskView(*task), Rev: task.Rev}
}

func (t *trackerAPI) createTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	var req struct {
		Title     string   `json:"title"`
		FeatureID *string  `json:"featureId"`
		Status    string   `json:"status"`
		Assignee  string   `json:"assignee"`
		Body      string   `json:"body"`
		Links     []string `json:"links"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		Unprocessable(w, "title is required")
		return
	}
	if req.Status == "" {
		req.Status = authored.StatusTodo
	}
	if !trkValidStatus(req.Status) {
		Unprocessable(w, "unknown status "+trkQuote(req.Status))
		return
	}
	feature := ""
	if req.FeatureID != nil && *req.FeatureID != "" {
		if _, ok := b.Feature(*req.FeatureID); !ok {
			Unprocessable(w, "unknown feature "+trkQuote(*req.FeatureID))
			return
		}
		feature = *req.FeatureID
	}
	links := []string{}
	for _, l := range req.Links {
		lr, err := t.trkLocalRef(ctx, p, l)
		if err != nil {
			Unprocessable(w, "bad link: "+err.Error())
			return
		}
		links = append(links, lr)
	}
	root := b.Root
	now := t.now()
	task := &authored.Task{
		Title: req.Title, Status: req.Status, Feature: feature, Assignee: req.Assignee,
		Created: authored.FormatTime(now), CreatedBy: t.author(), Updated: authored.FormatTime(now),
		Links: links, Unlinks: []string{}, Body: req.Body,
	}
	task.AppendLog(authored.FormatLogTime(now), t.author(), "created")
	// Check everything BEFORE an id is reserved: a 422 must leave no empty T-N.md (M14).
	if err := authored.Validate(task); err != nil {
		trkFail(w, err)
		return
	}
	if err := authored.EnsureLayout(root); err != nil {
		trkFail(w, err)
		return
	}
	_ = authored.EnsureGitignore(root)
	id, err := authored.NextID(root, authored.KindTask)
	if err != nil {
		trkFail(w, err)
		return
	}
	task.ID = id
	if _, err := authored.Save(root, task, ""); err != nil {
		_ = authored.Release(root, authored.KindTask, id)
		trkFail(w, err)
		return
	}
	t.changed(p, "task", id, ".drashta/tasks/"+id+".md")
	WriteJSON(w, http.StatusCreated, t.taskReply(ctx, p, task))
}

// ---- PATCH /api/projects/{p}/tasks/{id} ----

// trkOptString reads an optional string field: absent -> (nil, false); null -> ("", true).
func trkOptString(m map[string]json.RawMessage, key string) (*string, bool, error) {
	raw, ok := m[key]
	if !ok {
		return nil, false, nil
	}
	if string(raw) == "null" {
		s := ""
		return &s, true, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, true, errors.New(key + " must be a string")
	}
	return &s, true, nil
}

func (t *trackerAPI) patchTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	id, ok := trkID(w, r, "id", trkTaskIDRe)
	if !ok {
		return
	}
	rev, ok := trkIfMatch(w, r)
	if !ok {
		return
	}
	var m map[string]json.RawMessage
	if !ReadJSON(w, r, &m) {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	task, err := authored.LoadTask(b.Root, id)
	if err != nil {
		trkFail(w, err)
		return
	}
	if task.Rev != rev {
		trkConflict(w, "the task changed since you read it", task.Rev, b.TaskView(*task))
		return
	}
	old := *task
	now := t.now()
	for _, key := range []string{"status", "title", "featureId", "assignee", "body"} {
		v, present, err := trkOptString(m, key)
		if err != nil {
			BadRequest(w, err.Error())
			return
		}
		if !present {
			continue
		}
		switch key {
		case "status":
			if !trkValidStatus(*v) {
				Unprocessable(w, "unknown status "+trkQuote(*v))
				return
			}
			task.Status = *v
		case "title":
			if strings.TrimSpace(*v) == "" {
				Unprocessable(w, "title must not be empty")
				return
			}
			task.Title = strings.TrimSpace(*v)
		case "featureId":
			if *v != "" {
				if _, ok := b.Feature(*v); !ok {
					Unprocessable(w, "unknown feature "+trkQuote(*v))
					return
				}
			}
			task.Feature = *v
		case "assignee":
			task.Assignee = *v
		case "body":
			task.Body = *v
		}
	}
	if task.Status != old.Status {
		task.AppendLog(authored.FormatLogTime(now), t.author(), "status "+old.Status+" -> "+task.Status)
	}
	if task.Title != old.Title || task.Status != old.Status || task.Feature != old.Feature ||
		task.Assignee != old.Assignee || task.Body != old.Body {
		task.Updated = authored.FormatTime(now)
	}
	if _, err := authored.Save(b.Root, task, rev); err != nil {
		if errors.Is(err, authored.ErrConflict) {
			cur, lerr := authored.LoadTask(b.Root, id)
			if lerr == nil {
				trkConflict(w, "the task changed on disk", cur.Rev, b.TaskView(*cur))
				return
			}
		}
		trkFail(w, err)
		return
	}
	t.changed(p, "task", id, ".drashta/tasks/"+id+".md")
	WriteJSON(w, http.StatusOK, t.taskReply(ctx, p, task))
}

// ---- links ----

func (t *trackerAPI) linksReply(w http.ResponseWriter, ctx context.Context, project string, task *authored.Task) {
	b := t.reload(ctx, project)
	if b == nil {
		WriteJSON(w, http.StatusOK, map[string]any{"links": []trkLinkView{}})
		return
	}
	fresh, ok := b.Task(task.ID)
	if !ok {
		fresh = *task
	}
	WriteJSON(w, http.StatusOK, map[string]any{"links": t.linkViews(ctx, b, fresh), "rev": fresh.Rev})
}

func (t *trackerAPI) addLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	id, ok := trkID(w, r, "id", trkTaskIDRe)
	if !ok {
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	ref, err := t.trkLocalRef(ctx, p, req.Ref)
	if err != nil {
		Unprocessable(w, "bad ref: "+err.Error())
		return
	}
	if ref == "task:"+id {
		Unprocessable(w, "a task cannot link to itself")
		return
	}
	now := t.now()
	task, err := authored.UpdateTask(b.Root, id, func(task *authored.Task) error {
		// Linking again what the user once removed brings it back.
		kept := task.Unlinks[:0:0]
		for _, u := range task.Unlinks {
			if !b.SameLink(u, ref) {
				kept = append(kept, u)
			}
		}
		task.Unlinks = kept
		for _, l := range task.Links {
			if b.SameLink(l, ref) {
				return nil
			}
		}
		task.Links = append(task.Links, ref)
		task.Updated = authored.FormatTime(now)
		task.AppendLog(authored.FormatLogTime(now), t.author(), "linked "+ref)
		return nil
	})
	if err != nil {
		trkFail(w, err)
		return
	}
	t.changed(p, "task", id, ".drashta/tasks/"+id+".md")
	t.linksReply(w, ctx, p, task)
}

func (t *trackerAPI) removeLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	id, ok := trkID(w, r, "id", trkTaskIDRe)
	if !ok {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	raw := r.URL.Query().Get("ref")
	if raw == "" {
		BadRequest(w, "ref is required")
		return
	}
	ref, err := t.trkLocalRef(ctx, p, raw)
	if err != nil {
		BadRequest(w, "bad ref: "+err.Error())
		return
	}
	isAuto := b.HasAuto(id, ref)
	found := false
	now := t.now()
	task, err := authored.UpdateTask(b.Root, id, func(task *authored.Task) error {
		kept := task.Links[:0:0]
		for _, l := range task.Links {
			if b.SameLink(l, ref) {
				found = true
				continue
			}
			kept = append(kept, l)
		}
		task.Links = kept
		if isAuto {
			found = true
			already := false
			for _, u := range task.Unlinks {
				if b.SameLink(u, ref) {
					already = true
				}
			}
			if !already {
				task.Unlinks = append(task.Unlinks, ref)
			}
		}
		if !found {
			return errTrkNoLink
		}
		task.Updated = authored.FormatTime(now)
		task.AppendLog(authored.FormatLogTime(now), t.author(), "unlinked "+ref)
		return nil
	})
	if errors.Is(err, errTrkNoLink) {
		NotFound(w, "task "+id+" has no link "+ref)
		return
	}
	if err != nil {
		trkFail(w, err)
		return
	}
	t.changed(p, "task", id, ".drashta/tasks/"+id+".md")
	t.linksReply(w, ctx, p, task)
}

var errTrkNoLink = errors.New("no such link")

// ---- features ----

func (t *trackerAPI) featureReply(ctx context.Context, project string, f *authored.Feature) map[string]any {
	if b := t.reload(ctx, project); b != nil {
		if fresh, ok := b.Feature(f.ID); ok {
			return map[string]any{"feature": b.FeatureView(fresh), "rev": fresh.Rev}
		}
	}
	return map[string]any{"feature": f, "rev": f.Rev}
}

// syncMilestone keeps milestone.features in step with a feature's milestone key
// (the milestone file wins on conflict, so it must list the feature).
func (t *trackerAPI) syncMilestone(b *resolve.Board, featureID, oldM, newM string) ([]string, error) {
	var rels []string
	if oldM != "" && oldM != newM {
		if _, ok := b.Milestone(oldM); ok {
			_, err := authored.UpdateMilestone(b.Root, oldM, func(m *authored.Milestone) error {
				kept := m.Features[:0:0]
				for _, f := range m.Features {
					if f != featureID {
						kept = append(kept, f)
					}
				}
				m.Features = kept
				return nil
			})
			if err != nil {
				return rels, err
			}
			rels = append(rels, ".drashta/milestones/"+oldM+".md")
		}
	}
	if newM != "" {
		_, err := authored.UpdateMilestone(b.Root, newM, func(m *authored.Milestone) error {
			for _, f := range m.Features {
				if f == featureID {
					return nil
				}
			}
			m.Features = append(m.Features, featureID)
			return nil
		})
		if err != nil {
			return rels, err
		}
		rels = append(rels, ".drashta/milestones/"+newM+".md")
	}
	return rels, nil
}

func (t *trackerAPI) createFeature(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	var req struct {
		Title       string  `json:"title"`
		Summary     string  `json:"summary"`
		Body        string  `json:"body"`
		MilestoneID *string `json:"milestoneId"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		Unprocessable(w, "title is required")
		return
	}
	ms := ""
	if req.MilestoneID != nil && *req.MilestoneID != "" {
		if _, ok := b.Milestone(*req.MilestoneID); !ok {
			Unprocessable(w, "unknown milestone "+trkQuote(*req.MilestoneID))
			return
		}
		ms = *req.MilestoneID
	}
	body := req.Body
	if body == "" {
		body = req.Summary
	}
	f := &authored.Feature{Title: req.Title, Milestone: ms, Created: authored.FormatTime(t.now()), Body: body}
	if err := authored.Validate(f); err != nil { // before an id is reserved (M14)
		trkFail(w, err)
		return
	}
	if err := authored.EnsureLayout(b.Root); err != nil {
		trkFail(w, err)
		return
	}
	_ = authored.EnsureGitignore(b.Root)
	id, err := authored.NextID(b.Root, authored.KindFeature)
	if err != nil {
		trkFail(w, err)
		return
	}
	f.ID = id
	if _, err := authored.Save(b.Root, f, ""); err != nil {
		_ = authored.Release(b.Root, authored.KindFeature, id)
		trkFail(w, err)
		return
	}
	rels, err := t.syncMilestone(b, id, "", ms)
	if err != nil {
		t.log().Warn("feature saved, milestone not updated", "feature", id, "err", err)
	}
	t.changed(p, "feature", id, append([]string{".drashta/features/" + id + ".md"}, rels...)...)
	WriteJSON(w, http.StatusCreated, t.featureReply(ctx, p, f))
}

func (t *trackerAPI) patchFeature(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	id, ok := trkID(w, r, "id", trkFeatureIDRe)
	if !ok {
		return
	}
	rev, ok := trkIfMatch(w, r)
	if !ok {
		return
	}
	var m map[string]json.RawMessage
	if !ReadJSON(w, r, &m) {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	f, err := authored.LoadFeature(b.Root, id)
	if err != nil {
		trkFail(w, err)
		return
	}
	if f.Rev != rev {
		trkConflict(w, "the feature changed since you read it", f.Rev, b.FeatureView(*f))
		return
	}
	oldM := b.FeatureView(*f).MilestoneID
	for _, key := range []string{"title", "summary", "body", "milestoneId"} {
		v, present, err := trkOptString(m, key)
		if err != nil {
			BadRequest(w, err.Error())
			return
		}
		if !present {
			continue
		}
		switch key {
		case "title":
			if strings.TrimSpace(*v) == "" {
				Unprocessable(w, "title must not be empty")
				return
			}
			f.Title = strings.TrimSpace(*v)
		case "summary", "body":
			f.Body = *v
		case "milestoneId":
			if *v != "" {
				if _, ok := b.Milestone(*v); !ok {
					Unprocessable(w, "unknown milestone "+trkQuote(*v))
					return
				}
			}
			f.Milestone = *v
		}
	}
	if _, err := authored.Save(b.Root, f, rev); err != nil {
		if errors.Is(err, authored.ErrConflict) {
			if cur, lerr := authored.LoadFeature(b.Root, id); lerr == nil {
				trkConflict(w, "the feature changed on disk", cur.Rev, b.FeatureView(*cur))
				return
			}
		}
		trkFail(w, err)
		return
	}
	rels := []string{".drashta/features/" + id + ".md"}
	if _, present := m["milestoneId"]; present {
		old := ""
		if oldM != nil {
			old = *oldM
		}
		more, err := t.syncMilestone(b, id, old, f.Milestone)
		if err != nil {
			t.log().Warn("feature saved, milestone not updated", "feature", id, "err", err)
		}
		rels = append(rels, more...)
	}
	t.changed(p, "feature", id, rels...)
	WriteJSON(w, http.StatusOK, t.featureReply(ctx, p, f))
}

// ---- milestones ----

func (t *trackerAPI) milestoneReply(ctx context.Context, project string, m *authored.Milestone) map[string]any {
	if b := t.reload(ctx, project); b != nil {
		if fresh, ok := b.Milestone(m.ID); ok {
			return map[string]any{"milestone": b.MilestoneView(fresh), "rev": fresh.Rev}
		}
	}
	return map[string]any{"milestone": m, "rev": m.Rev}
}

func trkFeatureList(b *resolve.Board, ids []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := b.Feature(id); !ok {
			return nil, errors.New("unknown feature " + trkQuote(id))
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (t *trackerAPI) createMilestone(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	var req struct {
		Title      string   `json:"title"`
		Due        *string  `json:"due"`
		State      string   `json:"state"`
		Body       string   `json:"body"`
		FeatureIDs []string `json:"featureIds"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		Unprocessable(w, "title is required")
		return
	}
	feats, err := trkFeatureList(b, req.FeatureIDs)
	if err != nil {
		Unprocessable(w, err.Error())
		return
	}
	m := &authored.Milestone{Title: req.Title, State: req.State, Features: feats,
		Created: authored.FormatTime(t.now()), Body: req.Body}
	if req.Due != nil {
		m.Due = *req.Due
	}
	if err := authored.Validate(m); err != nil { // before an id is reserved (M14)
		trkFail(w, err)
		return
	}
	if err := authored.EnsureLayout(b.Root); err != nil {
		trkFail(w, err)
		return
	}
	_ = authored.EnsureGitignore(b.Root)
	id, err := authored.NextID(b.Root, authored.KindMilestone)
	if err != nil {
		trkFail(w, err)
		return
	}
	m.ID = id
	if _, err := authored.Save(b.Root, m, ""); err != nil {
		_ = authored.Release(b.Root, authored.KindMilestone, id)
		trkFail(w, err)
		return
	}
	rels := t.syncFeatures(b, id, nil, feats)
	t.changed(p, "milestone", id, append([]string{".drashta/milestones/" + id + ".md"}, rels...)...)
	WriteJSON(w, http.StatusCreated, t.milestoneReply(ctx, p, m))
}

func (t *trackerAPI) patchMilestone(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	id, ok := trkID(w, r, "id", trkMilestoneIDRe)
	if !ok {
		return
	}
	rev, ok := trkIfMatch(w, r)
	if !ok {
		return
	}
	var m map[string]json.RawMessage
	if !ReadJSON(w, r, &m) {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	ms, err := authored.LoadMilestone(b.Root, id)
	if err != nil {
		trkFail(w, err)
		return
	}
	if ms.Rev != rev {
		trkConflict(w, "the milestone changed since you read it", ms.Rev, b.MilestoneView(*ms))
		return
	}
	for _, key := range []string{"title", "due", "state", "body"} {
		v, present, err := trkOptString(m, key)
		if err != nil {
			BadRequest(w, err.Error())
			return
		}
		if !present {
			continue
		}
		switch key {
		case "title":
			if strings.TrimSpace(*v) == "" {
				Unprocessable(w, "title must not be empty")
				return
			}
			ms.Title = strings.TrimSpace(*v)
		case "due":
			ms.Due = *v
		case "state":
			ms.State = *v
		case "body":
			ms.Body = *v
		}
	}
	var oldFeats, newFeats []string
	if raw, ok := m["featureIds"]; ok {
		var ids []string
		if err := json.Unmarshal(raw, &ids); err != nil {
			BadRequest(w, "featureIds must be a list of strings")
			return
		}
		feats, err := trkFeatureList(b, ids)
		if err != nil {
			Unprocessable(w, err.Error())
			return
		}
		oldFeats = append([]string{}, ms.Features...)
		newFeats = feats
		ms.Features = feats
	}
	if _, err := authored.Save(b.Root, ms, rev); err != nil {
		if errors.Is(err, authored.ErrConflict) {
			if cur, lerr := authored.LoadMilestone(b.Root, id); lerr == nil {
				trkConflict(w, "the milestone changed on disk", cur.Rev, b.MilestoneView(*cur))
				return
			}
		}
		trkFail(w, err)
		return
	}
	rels := []string{".drashta/milestones/" + id + ".md"}
	if newFeats != nil {
		rels = append(rels, t.syncFeatures(b, id, oldFeats, newFeats)...)
	}
	t.changed(p, "milestone", id, rels...)
	WriteJSON(w, http.StatusOK, t.milestoneReply(ctx, p, ms))
}

// syncFeatures keeps features in step after milestone msID got the feature list now
// (it had old before): each listed feature gets "milestone: msID" and leaves any other
// milestone's list; a feature dropped from the list loses its milestone key when it pointed
// here. Failures are logged; the milestone file wins on conflict anyway.
func (t *trackerAPI) syncFeatures(b *resolve.Board, msID string, old, now []string) []string {
	var rels []string
	in := map[string]bool{}
	for _, f := range now {
		in[f] = true
		if _, err := authored.UpdateFeature(b.Root, f, func(ft *authored.Feature) error {
			ft.Milestone = msID
			return nil
		}); err != nil {
			t.log().Warn("milestone saved, feature not updated", "feature", f, "err", err)
			continue
		}
		rels = append(rels, ".drashta/features/"+f+".md")
		for _, other := range b.Tracker.Milestones {
			if other.ID == msID {
				continue
			}
			for _, x := range other.Features {
				if x != f {
					continue
				}
				if _, err := authored.UpdateMilestone(b.Root, other.ID, func(m *authored.Milestone) error {
					kept := m.Features[:0:0]
					for _, y := range m.Features {
						if y != f {
							kept = append(kept, y)
						}
					}
					m.Features = kept
					return nil
				}); err == nil {
					rels = append(rels, ".drashta/milestones/"+other.ID+".md")
				}
			}
		}
	}
	for _, f := range old {
		if in[f] {
			continue
		}
		if _, err := authored.UpdateFeature(b.Root, f, func(ft *authored.Feature) error {
			if ft.Milestone == msID {
				ft.Milestone = ""
			}
			return nil
		}); err == nil {
			rels = append(rels, ".drashta/features/"+f+".md")
		}
	}
	return rels
}

// ---- DELETE /api/projects/{p}/{tasks|features|milestones}/{id} ----

func (t *trackerAPI) deleteItem(kind string) http.HandlerFunc {
	re := map[string]*regexp.Regexp{"tasks": trkTaskIDRe, "features": trkFeatureIDRe, "milestones": trkMilestoneIDRe}[kind]
	single := strings.TrimSuffix(kind, "s")
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.PathValue("p")
		id, ok := trkID(w, r, "id", re)
		if !ok {
			return
		}
		root, err := t.d.Resolver.Root(r.Context(), p)
		if err != nil {
			trkFail(w, err)
			return
		}
		// A task takes its snapshots and notes along to the trash (I11); every moved
		// file is mirrored at once, so counts and the inbox drop them now.
		moved, err := authored.DeleteMoved(root, single, id)
		if err != nil && len(moved) == 0 {
			trkFail(w, err)
			return
		}
		if err != nil {
			t.log().Warn("delete: some files did not move", "project", p, "id", id, "err", err)
		}
		if len(moved) == 0 {
			moved = []string{path.Join(".drashta", kind, id+".md")}
		}
		t.changed(p, single, id, moved...)
		WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
