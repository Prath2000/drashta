package httpapi

// DWG routes of the project pages (the drawing preview).
//
//	GET /api/projects/{p}/dwg/layers?path=     the layers of a .dwg as JSON (application/json)
//	GET /api/projects/{p}/dwg/entities?path=   the model-space entities of a .dwg, one JSON object per line
//	                                           (application/x-ndjson, served from the reader's cache file)
//
// Rules:
//   - path is relative to the project and must end in .dwg (any letter case), else 400. Absolute
//     paths and ".." are a 400 (pjCleanRel). The path is read once from the query string, so a
//     name with spaces, "#", "%" or non-ASCII letters needs no special care.
//   - The file goes through pathguard (a link out of the project is 403), a missing file is 404,
//     a secret file (or a link to one) is 403, a folder is 400.
//   - chitralipi missing is 503 "unavailable" and a file it cannot read is 422 "bad_drawing"; both
//     carry the reader's message. Any other failure is a plain 500 without detail.
//   - ProjectDeps.Drawings is nil: 503 "unavailable".

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/drawing"
	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/probe/tree"
)

// ProjectDrawings reads DWG files through chitralipi. *drawing.Reader implements it.
type ProjectDrawings interface {
	// Layers returns the stdout of `layers --as json` for the file at abs.
	Layers(ctx context.Context, abs string) ([]byte, error)
	// Entities returns the path of a cached JSONL file of the model-space entities of abs.
	Entities(ctx context.Context, abs string) (string, error)
}

func (a *ProjectAPI) registerDrawings(s *Server) {
	s.Handle(http.MethodGet, "/api/projects/{p}/dwg/layers", a.handleDwgLayers, false)
	s.Handle(http.MethodGet, "/api/projects/{p}/dwg/entities", a.handleDwgEntities, false)
}

func (a *ProjectAPI) handleDwgLayers(w http.ResponseWriter, r *http.Request) {
	abs, ok := a.dwgPath(w, r)
	if !ok {
		return
	}
	out, err := a.d.Drawings.Layers(r.Context(), abs)
	if err != nil {
		a.drawingError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func (a *ProjectAPI) handleDwgEntities(w http.ResponseWriter, r *http.Request) {
	abs, ok := a.dwgPath(w, r)
	if !ok {
		return
	}
	cached, err := a.d.Drawings.Entities(r.Context(), abs)
	if err != nil {
		a.drawingError(w, r, err)
		return
	}
	f, err := os.Open(cached)
	if err != nil {
		a.fail(w, r, "open cached entities", err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		a.fail(w, r, "stat cached entities", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "entities.jsonl", st.ModTime().Truncate(time.Second), f)
}

// dwgPath checks the request and returns the absolute path of the .dwg file. On any problem it
// has already written the reply and returns false.
func (a *ProjectAPI) dwgPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	pr, ok := a.onDisk(w, r)
	if !ok {
		return "", false
	}
	rel, err := pjCleanRel(r.URL.Query().Get("path"), false)
	if err != nil {
		BadRequest(w, err.Error())
		return "", false
	}
	if !strings.EqualFold(path.Ext(rel), ".dwg") {
		BadRequest(w, "the file is not a .dwg drawing")
		return "", false
	}
	if a.d.Drawings == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "the drawing reader is not set up")
		return "", false
	}
	abs, err := pathguard.Resolve(pr.Root, rel)
	if err != nil {
		a.pathError(w, r, err)
		return "", false
	}
	if tree.IsSecret(rel) || tree.IsSecret(abs) {
		a.pathError(w, r, tree.ErrSecret)
		return "", false
	}
	st, err := os.Stat(abs)
	switch {
	case err != nil:
		a.pathError(w, r, err)
		return "", false
	case st.IsDir():
		a.pathError(w, r, tree.ErrIsDir)
		return "", false
	case !st.Mode().IsRegular():
		a.pathError(w, r, tree.ErrNotRegular)
		return "", false
	}
	return abs, true
}

// drawingError maps an error of the drawing reader to a reply.
func (a *ProjectAPI) drawingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, drawing.ErrUnavailable):
		WriteError(w, http.StatusServiceUnavailable, "unavailable", drawingDetail(err, drawing.ErrUnavailable))
	case errors.Is(err, drawing.ErrBadDrawing):
		WriteError(w, http.StatusUnprocessableEntity, "bad_drawing", drawingDetail(err, drawing.ErrBadDrawing))
	default:
		a.fail(w, r, "drawing", err)
	}
}

// drawingDetail is the text of err without the leading sentinel text ("drawing: chitralipi is
// not available: "), which the page already says in its own words. The sentinel text itself is
// the fallback when nothing follows it.
func drawingDetail(err, sentinel error) string {
	msg := strings.TrimPrefix(strings.TrimPrefix(err.Error(), sentinel.Error()), ": ")
	if msg == "" {
		msg = strings.TrimPrefix(sentinel.Error(), "drawing: ")
	}
	return msg
}
