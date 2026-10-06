package httpapi

// Snapshot routes (04-data-model.md section 6 "Snapshots", spec 9.6).
//
// Status rules:
//   (a) a NEW version on a task that is in_progress moves the task to review (+ log line);
//   (b) verdict "changes" on the LATEST version moves the task to in_progress (+ log line);
//   (c) verdict "approved" on the latest version only SUGGESTS done (decision A4): the reply
//       carries suggestion:{status:"done"} and the task is not changed.
// Every move is Load + authored.Save with the rev that was read; a conflict is retried a few
// times (the move is a rule, so it is computed again on the fresh file) and then gives 409.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/resolve"
	"github.com/Prath2000/drashta/internal/runx"
)

func (t *trackerAPI) registerSnapshots() {
	s := t.s
	s.Handle("GET", "/api/projects/{p}/tasks/{id}/snapshots", t.listSnapshots, false)
	s.HandleOpts("POST", "/api/projects/{p}/tasks/{id}/snapshots", t.createSnapshot,
		RouteOptions{Mutating: true, Upload: true, MaxBody: snapshotMaxBody})
	s.Handle("PUT", "/api/projects/{p}/tasks/{id}/snapshots/{v}/verdict", t.putVerdict, true)
	s.Handle("POST", "/api/projects/{p}/snapshots/inbox/{name}/file", t.fileDrop, true)
}

func (t *trackerAPI) listSnapshots(w http.ResponseWriter, r *http.Request) {
	id, ok := trkID(w, r, "id", trkTaskIDRe)
	if !ok {
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	if _, found := b.Task(id); !found {
		NotFound(w, "no task "+id)
		return
	}
	out := []resolve.Snapshot{}
	for _, s := range b.SnapshotsOf(id) {
		out = append(out, b.SnapshotView(s))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"snapshots": out})
}

// ---- status moves ----

// moveTask applies change to the task with Load + Save(rev). change returns false when
// there is nothing to do. A conflict is retried 3 times. It returns the task as saved
// (or as loaded when nothing changed) and whether it changed.
func (t *trackerAPI) moveTask(root, id string, change func(*authored.Task) bool) (*authored.Task, bool, error) {
	var last error
	for try := 0; try < 3; try++ {
		task, err := authored.LoadTask(root, id)
		if err != nil {
			return nil, false, err
		}
		if !change(task) {
			return task, false, nil
		}
		_, err = authored.Save(root, task, task.Rev)
		if err == nil {
			return task, true, nil
		}
		if !errors.Is(err, authored.ErrConflict) {
			return nil, false, err
		}
		last = err
	}
	return nil, false, last
}

// ---- creating a version ----

type snapInput struct {
	src    io.Reader
	ext    string
	source string
	title  string
	note   string
}

// addSnapshot stores the image, writes vN.md and applies rule (a). It answers itself.
func (t *trackerAPI) addSnapshot(w http.ResponseWriter, ctx context.Context, p, root, taskID string, in snapInput) {
	task, err := authored.LoadTask(root, taskID)
	if err != nil {
		trkFail(w, err)
		return
	}
	version, rel, err := authored.AddSnapshotImage(root, taskID, in.src, in.ext)
	if err != nil {
		trkFail(w, err)
		return
	}
	t.writeVersion(w, ctx, p, root, task, version, rel, in)
}

func (t *trackerAPI) createSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	taskID, ok := trkID(w, r, "id", trkTaskIDRe)
	if !ok {
		return
	}
	root, err := t.d.Resolver.Root(ctx, p)
	if err != nil {
		trkFail(w, err)
		return
	}
	if _, err := authored.LoadTask(root, taskID); err != nil {
		trkFail(w, err)
		return
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		t.createFromMultipart(w, r, p, root, taskID)
		return
	}

	// JSON: {url} or {path}, plus title? and note?. JSON bodies keep the small cap.
	r.Body = http.MaxBytesReader(w, r.Body, DefaultMaxBody)
	var req struct {
		URL   string `json:"url"`
		Path  string `json:"path"`
		Title string `json:"title"`
		Note  string `json:"note"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	switch {
	case req.URL != "" && req.Path != "":
		BadRequest(w, "send url or path, not both")
		return
	case req.URL != "":
		WriteError(w, http.StatusNotImplemented, "not_implemented",
			"URL snapshots arrive later (Safari capture, decision A3)")
		return
	case req.Path == "":
		BadRequest(w, "send a multipart image, or JSON {path}")
		return
	}
	rel, full, err := trkProjectFile(root, req.Path)
	if err != nil {
		trkFail(w, err)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		trkFail(w, err)
		return
	}
	defer f.Close()
	t.addSnapshot(w, ctx, p, root, taskID, snapInput{
		src: f, ext: filepath.Ext(full), source: "file:" + rel, title: req.Title, note: req.Note,
	})
}

// trkProjectFile checks a project-relative path from a request: relative, no "..", inside
// the project after symlinks (pathguard), and a regular file.
func trkProjectFile(root, p string) (rel, full string, err error) {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.ContainsAny(p, "\\\x00") {
		return "", "", fmt.Errorf("%w: path must be relative to the project", resolve.ErrForbidden)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", "", fmt.Errorf("%w: path must not contain ..", resolve.ErrForbidden)
		}
	}
	full, err = pathguard.Resolve(root, p)
	if err != nil {
		return "", "", err
	}
	fi, err := os.Stat(full)
	if err != nil {
		return "", "", err
	}
	if !fi.Mode().IsRegular() {
		return "", "", fmt.Errorf("%w: %s is not a file", resolve.ErrForbidden, p)
	}
	return path.Clean(p), full, nil
}

// createFromMultipart streams the "image" part into the store (no temp copy of the whole
// body). Text parts "title" and "note" may come before or after it.
func (t *trackerAPI) createFromMultipart(w http.ResponseWriter, r *http.Request, p, root, taskID string) {
	mr, err := r.MultipartReader()
	if err != nil {
		BadRequest(w, "bad multipart body: "+err.Error())
		return
	}
	var title, note string
	var version int
	var rel string
	got := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.dropVersion(root, rel)
			trkFail(w, trkMultipartErr(err))
			return
		}
		switch part.FormName() {
		case "title", "note":
			b, err := io.ReadAll(io.LimitReader(part, 16<<10))
			if err != nil {
				t.dropVersion(root, rel)
				trkFail(w, trkMultipartErr(err))
				return
			}
			if part.FormName() == "title" {
				title = string(b)
			} else {
				note = string(b)
			}
		case "image":
			if got {
				t.dropVersion(root, rel)
				BadRequest(w, "send one image only")
				return
			}
			ext := filepath.Ext(part.FileName())
			if ext == "" {
				if exts, _ := mime.ExtensionsByType(part.Header.Get("Content-Type")); len(exts) > 0 {
					ext = exts[0]
				}
			}
			version, rel, err = authored.AddSnapshotImage(root, taskID, part, ext)
			if err != nil {
				trkFail(w, trkMultipartErr(err))
				return
			}
			got = true
		}
		part.Close()
	}
	if !got {
		BadRequest(w, `the multipart body needs an "image" part`)
		return
	}
	t.finishVersion(w, r.Context(), p, root, taskID, version, rel, title, note)
}

// trkMultipartErr keeps a body-cap error visible through the multipart reader.
func trkMultipartErr(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return err
	}
	if strings.Contains(err.Error(), "request body too large") {
		return &http.MaxBytesError{Limit: snapshotMaxBody}
	}
	return err
}

// dropVersion removes an image stored by a request that then failed.
func (t *trackerAPI) dropVersion(root, rel string) {
	if rel != "" {
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
	}
}

// finishVersion writes vN.md for an image that is already stored and applies rule (a).
func (t *trackerAPI) finishVersion(w http.ResponseWriter, ctx context.Context, p, root, taskID string,
	version int, rel, title, note string) {
	task, err := authored.LoadTask(root, taskID)
	if err != nil {
		t.dropVersion(root, rel)
		trkFail(w, err)
		return
	}
	in := snapInput{source: "upload", title: title, note: note}
	t.writeVersion(w, ctx, p, root, task, version, rel, in)
}

func (t *trackerAPI) writeVersion(w http.ResponseWriter, ctx context.Context, p, root string, task *authored.Task,
	version int, rel string, in snapInput) {
	now := t.now()
	title := strings.TrimSpace(in.title)
	if title == "" {
		title = task.Title
	}
	if r := []rune(title); len(r) > 200 {
		title = string(r[:200])
	}
	snap := &authored.Snapshot{
		Task: task.ID, Version: version, Title: title, Image: path.Base(rel), Source: in.source,
		Captured: authored.FormatTime(now), Author: t.author(), Commit: t.d.HeadCommit(ctx, root),
		Verdict: authored.VerdictPending, Body: strings.TrimSpace(in.note),
	}
	if _, err := authored.Save(root, snap, ""); err != nil {
		t.dropVersion(root, rel)
		trkFail(w, err)
		return
	}
	_, _, err := t.moveTask(root, task.ID, func(tk *authored.Task) bool {
		msg := fmt.Sprintf("posted snapshot v%d", version)
		if tk.Status == authored.StatusInProgress {
			tk.Status = authored.StatusReview
			msg += "; status in_progress -> review"
		}
		tk.Updated = authored.FormatTime(now)
		tk.AppendLog(authored.FormatLogTime(now), t.author(), msg)
		return true
	})
	if err != nil {
		t.log().Warn("snapshot saved, task not updated", "task", task.ID, "err", err)
	}
	id := authored.SnapshotID(task.ID, version)
	t.changed(p, "snapshot", id, ".drashta/snapshots/"+id+".md", rel)
	t.changed(p, "task", task.ID, ".drashta/tasks/"+task.ID+".md")
	if b := t.reload(ctx, p); b != nil {
		if s, ok := b.Snapshot(id); ok {
			WriteJSON(w, http.StatusCreated, map[string]any{"snapshot": b.SnapshotView(s)})
			return
		}
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"snapshot": snap})
}

// ---- verdict ----

func (t *trackerAPI) putVerdict(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	taskID, ok := trkID(w, r, "id", trkTaskIDRe)
	if !ok {
		return
	}
	vs := strings.TrimPrefix(r.PathValue("v"), "v")
	version, err := strconv.Atoi(vs)
	if err != nil || version < 1 || len(vs) > 9 {
		BadRequest(w, "bad version "+trkQuote(r.PathValue("v")))
		return
	}
	var req struct {
		Verdict string  `json:"verdict"`
		Note    *string `json:"note"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	switch req.Verdict {
	case authored.VerdictPending, authored.VerdictApproved, authored.VerdictChanges:
	default:
		Unprocessable(w, "verdict must be pending, approved or changes")
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	if _, found := b.Task(taskID); !found {
		NotFound(w, "no task "+taskID)
		return
	}
	id := authored.SnapshotID(taskID, version)
	now := t.now()
	snap, err := authored.UpdateSnapshot(b.Root, id, func(s *authored.Snapshot) error {
		s.Verdict = req.Verdict
		if req.Verdict == authored.VerdictPending {
			s.ReviewedBy, s.ReviewedAt, s.ReviewNote = "", "", ""
			return nil
		}
		s.ReviewedBy = t.author()
		s.ReviewedAt = authored.FormatTime(now)
		if req.Note != nil {
			s.ReviewNote = strings.TrimSpace(*req.Note)
		}
		return nil
	})
	if err != nil {
		trkFail(w, err)
		return
	}
	t.changed(p, "snapshot", id, ".drashta/snapshots/"+id+".md")

	latest := true
	if l, ok := b.Latest(taskID); ok && l.Version > version {
		latest = false
	}
	reply := map[string]any{}
	if latest && req.Verdict == authored.VerdictChanges {
		// Rule (b).
		_, moved, err := t.moveTask(b.Root, taskID, func(tk *authored.Task) bool {
			if tk.Status == authored.StatusInProgress {
				return false
			}
			old := tk.Status
			tk.Status = authored.StatusInProgress
			tk.Updated = authored.FormatTime(now)
			tk.AppendLog(authored.FormatLogTime(now), t.author(),
				fmt.Sprintf("changes requested on snapshot v%d; status %s -> in_progress", version, old))
			return true
		})
		if err != nil {
			if errors.Is(err, authored.ErrConflict) {
				if cur, lerr := authored.LoadTask(b.Root, taskID); lerr == nil {
					trkConflict(w, "the verdict is saved, but the task changed on disk", cur.Rev, b.TaskView(*cur))
					return
				}
			}
			trkFail(w, err)
			return
		}
		if moved {
			t.changed(p, "task", taskID, ".drashta/tasks/"+taskID+".md")
		}
	}
	fresh := t.reload(ctx, p)
	if fresh == nil {
		fresh = b
	}
	task, _ := fresh.Task(taskID)
	if latest && req.Verdict == authored.VerdictApproved && task.Status != authored.StatusDone {
		// Rule (c), decision A4: only a suggestion; the task is not changed.
		reply["suggestion"] = map[string]string{"status": authored.StatusDone}
	}
	if s, ok := fresh.Snapshot(id); ok {
		reply["snapshot"] = fresh.SnapshotView(s)
	} else {
		reply["snapshot"] = snap
	}
	reply["task"] = fresh.TaskView(task)
	WriteJSON(w, http.StatusOK, reply)
}

// ---- filing a drop from .drashta/snapshots/inbox/ ----

func (t *trackerAPI) fileDrop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.PathValue("p")
	name := r.PathValue("name")
	if err := resolve.CheckDropName(name); err != nil {
		if errors.Is(err, authored.ErrBadExt) {
			UnsupportedMediaType(w, "the drop is not an allowed image (png, jpg, jpeg, webp, gif)")
			return
		}
		BadRequest(w, "bad drop name "+trkQuote(name))
		return
	}
	var req struct {
		TaskID string `json:"taskId"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	if !trkTaskIDRe.MatchString(req.TaskID) {
		Unprocessable(w, "taskId must look like T-12")
		return
	}
	root, err := t.d.Resolver.Root(ctx, p)
	if err != nil {
		trkFail(w, err)
		return
	}
	task, err := authored.LoadTask(root, req.TaskID)
	if err != nil {
		trkFail(w, err)
		return
	}
	full, err := resolve.DropPath(root, name)
	if err != nil {
		trkFail(w, err)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		trkFail(w, err)
		return
	}
	version, rel, err := authored.AddSnapshotImage(root, req.TaskID, f, filepath.Ext(name))
	f.Close()
	if err != nil {
		trkFail(w, err)
		return
	}
	if err := os.Remove(full); err != nil {
		t.log().Warn("drop filed but not removed", "path", full, "err", err)
	}
	t.writeVersion(w, ctx, p, root, task, version, rel, snapInput{source: "upload"})
}

// ---- git HEAD for a new snapshot ----

// trkGitHead returns the first 10 characters of HEAD when the project has a .git folder.
func trkGitHead(ctx context.Context, root string) string {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := runx.Run(ctx, runx.Options{Dir: root, Timeout: 3 * time.Second, MaxOut: 4096}, "git", "rev-parse", "HEAD")
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	h := strings.TrimSpace(string(res.Stdout))
	if len(h) > 10 {
		h = h[:10]
	}
	return h
}
