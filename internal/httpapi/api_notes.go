package httpapi

// Note routes (04-data-model.md section 6 "Notes, refs, search, actions").
// A note is stored in the project of the item it is on. The anchor text of a line note is
// read live from the file through the tree probe, which never reads secret files.

import (
	"errors"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/resolve"
)

func (t *trackerAPI) registerNotes() {
	t.s.Handle("GET", "/api/notes", t.listNotes, false)
	t.s.Handle("POST", "/api/notes", t.createNote, true)
	t.s.Handle("PATCH", "/api/projects/{p}/notes/{id}", t.patchNote, true)
}

func (t *trackerAPI) listNotes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	onlyOpen := q.Get("open") == "1" || q.Get("open") == "true"
	project := q.Get("project")
	out := []resolve.Note{}
	keep := func(n resolve.Note) {
		if onlyOpen && n.Resolved {
			return
		}
		out = append(out, n)
	}

	switch {
	case q.Get("ref") != "":
		ref, err := t.d.Resolver.Qualify(ctx, q.Get("ref"), project)
		if err != nil {
			trkFail(w, err)
			return
		}
		notes, err := t.d.Resolver.NotesFor(ctx, ref)
		if err != nil {
			trkFail(w, err)
			return
		}
		for _, n := range notes {
			keep(n)
		}
	default:
		var ids []string
		if project != "" {
			if _, err := t.d.Resolver.Project(ctx, project); err != nil {
				trkFail(w, err)
				return
			}
			ids = []string{project}
		} else {
			ps, err := t.d.Resolver.Projects(ctx)
			if err != nil {
				trkFail(w, err)
				return
			}
			for _, p := range ps {
				if !p.Hidden {
					ids = append(ids, p.ID)
				}
			}
		}
		for _, id := range ids {
			b, err := t.d.Resolver.Board(ctx, id)
			if err != nil {
				continue
			}
			for _, n := range b.Tracker.Notes {
				keep(b.NoteView(n))
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	WriteJSON(w, http.StatusOK, map[string]any{"notes": out})
}

func (t *trackerAPI) createNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Ref     string   `json:"ref"`
		Text    string   `json:"text"`
		Line    *int     `json:"line"`
		X       *float64 `json:"x"`
		Y       *float64 `json:"y"`
		ReplyTo string   `json:"replyTo"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		Unprocessable(w, "text is required")
		return
	}
	if strings.TrimSpace(req.Ref) == "" {
		BadRequest(w, "ref is required")
		return
	}
	ref, err := t.d.Resolver.Qualify(ctx, req.Ref, "")
	if err != nil {
		trkFail(w, err)
		return
	}
	if ref.Project == "" {
		Unprocessable(w, "the item has no project, so the note has no place to live")
		return
	}
	// A fragment says the same as line or x/y.
	if from, _, ok := ref.Lines(); ok && req.Line == nil {
		req.Line = &from
	}
	if px, py, ok := ref.Point(); ok && req.X == nil && req.Y == nil {
		req.X, req.Y = &px, &py
	}
	if (req.X == nil) != (req.Y == nil) {
		Unprocessable(w, "a pin needs both x and y")
		return
	}
	for _, v := range []*float64{req.X, req.Y} {
		if v != nil && (*v < 0 || *v > 1) {
			Unprocessable(w, "x and y must be between 0 and 1")
			return
		}
	}
	if req.Line != nil && *req.Line < 1 {
		Unprocessable(w, "line must be 1 or more")
		return
	}
	if req.Line != nil && canonFile(ref.Type) != authored.RefFile {
		Unprocessable(w, "a line only works on a file ref")
		return
	}
	// Tracker items must exist (observed items may not be indexed yet).
	if ref.IsAuthored() {
		if _, err := t.d.Resolver.Summary(ctx, ref.String(), ""); err != nil {
			if errors.Is(err, resolve.ErrNotFound) {
				Unprocessable(w, "no such item: "+ref.String())
				return
			}
			trkFail(w, err)
			return
		}
	}
	b, err := t.d.Resolver.Board(ctx, ref.Project)
	if err != nil {
		trkFail(w, err)
		return
	}
	replyTo := strings.TrimSpace(req.ReplyTo)
	if replyTo != "" {
		if !trkNoteIDRe.MatchString(replyTo) {
			Unprocessable(w, "replyTo must look like N-12")
			return
		}
		if _, ok := b.Note(replyTo); !ok {
			Unprocessable(w, "no note "+replyTo+" to reply to")
			return
		}
	}
	anchor := ""
	if canonFile(ref.Type) == authored.RefFile && req.Line != nil {
		anchor = trkLineText(b.Root, ref.ID, *req.Line)
	}
	stored := ref
	stored.Project = "" // the note lives in the ref's project
	n := &authored.Note{
		Ref: stored.String(), X: req.X, Y: req.Y, Line: req.Line, Anchor: anchor, Author: t.author(),
		Created: authored.FormatTime(t.now()), ReplyTo: replyTo, Body: text,
	}
	if err := authored.Validate(n); err != nil { // before an id is reserved (M14)
		trkFail(w, err)
		return
	}
	if err := authored.EnsureLayout(b.Root); err != nil {
		trkFail(w, err)
		return
	}
	_ = authored.EnsureGitignore(b.Root)
	id, err := authored.NextID(b.Root, authored.KindNote)
	if err != nil {
		trkFail(w, err)
		return
	}
	n.ID = id
	if _, err := authored.Save(b.Root, n, ""); err != nil {
		_ = authored.Release(b.Root, authored.KindNote, id)
		trkFail(w, err)
		return
	}
	t.changed(ref.Project, "note", id, ".drashta/notes/"+id+".md")
	WriteJSON(w, http.StatusCreated, map[string]any{"note": b.NoteView(*n)})
}

func canonFile(typ string) string {
	if typ == authored.RefImage {
		return authored.RefFile
	}
	return typ
}

// trkLineText returns the trimmed text of one line (at most 200 characters), read live and
// secret-safe: secret and binary files give "".
func trkLineText(root, rel string, line int) string {
	ft, err := tree.ReadFile(root, path.Clean(rel), line, line)
	if err != nil || ft.Redacted || ft.Binary {
		return ""
	}
	s := strings.TrimSpace(strings.SplitN(ft.Text, "\n", 2)[0])
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

func (t *trackerAPI) patchNote(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("p")
	id, ok := trkID(w, r, "id", trkNoteIDRe)
	if !ok {
		return
	}
	var req struct {
		Resolved *bool   `json:"resolved"`
		Text     *string `json:"text"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Resolved == nil && req.Text == nil {
		BadRequest(w, "send resolved and/or text")
		return
	}
	if req.Text != nil && strings.TrimSpace(*req.Text) == "" {
		Unprocessable(w, "text must not be empty")
		return
	}
	b, ok := t.board(w, r)
	if !ok {
		return
	}
	who := t.author()
	now := t.now()
	note, err := authored.UpdateNote(b.Root, id, func(n *authored.Note) error {
		if req.Text != nil && strings.TrimSpace(*req.Text) != n.Body {
			if n.Author != who {
				return errTrkNotAuthor
			}
			n.Body = strings.TrimSpace(*req.Text)
		}
		if req.Resolved != nil && *req.Resolved != n.Resolved {
			n.Resolved = *req.Resolved
			if n.Resolved {
				n.ResolvedBy, n.ResolvedAt = who, authored.FormatTime(now)
			} else {
				n.ResolvedBy, n.ResolvedAt = "", ""
			}
		}
		return nil
	})
	if errors.Is(err, errTrkNotAuthor) {
		Forbidden(w, "only the author of a note can change its text")
		return
	}
	if err != nil {
		trkFail(w, err)
		return
	}
	t.changed(p, "note", id, ".drashta/notes/"+id+".md")
	WriteJSON(w, http.StatusOK, map[string]any{"note": b.NoteView(*note)})
}

var errTrkNotAuthor = errors.New("not the author")
