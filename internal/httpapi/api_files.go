package httpapi

// File routes of the project pages: a lazy tree, a live file read and the raw bytes.
//
//	GET /api/projects/{p}/tree?dir=&depth=1   {dir, entries:[{path, dir, size, mtime, kind, hasChildren, collapsed, dirty}]}
//	GET /api/projects/{p}/file?path=&from=&to= {path,size,mtime,kind,isDoc,lines,from,to,text,truncated,binary,redacted,notes:[]}
//	GET /api/projects/{p}/raw?path=            the file bytes (see the header rules below)
//	GET /api/projects/{p}/files/{path...}      the same bytes and headers, with the path in the URL
//	                                           (a model file loads its sibling files by relative URL)
//
// Rules (03-architecture.md sections 7, 8 and 10):
//   - A path is relative to the project. An absolute path or a ".." element is a 400.
//     A link that leaves the project is a 403 (pathguard). A missing path is a 404.
//   - Secret files (.env*, *.pem, *.key, id_*, *.p12) are listed in the tree, but /file gives
//     redacted:true with no text, and /raw gives 403.
//   - /file reads at most 1 MB and 2000 lines unless the caller asks for a window.
//   - /raw always sends "X-Content-Type-Options: nosniff", and "Content-Security-Policy: sandbox"
//     for everything but application/pdf (the browser's PDF viewer will not load in a sandbox).
//     The content type comes from tree.RawPath (html, xml and js are text/plain).

import (
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/Prath2000/drashta/internal/probe/tree"
	"github.com/Prath2000/drashta/internal/store"
)

const (
	pjTreeDepthMax = 6
	pjLineMax      = 1 << 30
)

func (a *ProjectAPI) registerFiles(s *Server) {
	s.Handle(http.MethodGet, "/api/projects/{p}/tree", a.handleTree, false)
	s.Handle(http.MethodGet, "/api/projects/{p}/file", a.handleFile, false)
	s.Handle(http.MethodGet, "/api/projects/{p}/raw", a.handleRaw, false)
	s.Handle(http.MethodGet, "/api/projects/{p}/files/{path...}", a.handleFiles, false)
}

// pjIntParam reads an optional integer query value in [min, max]. A bad value gives an error text.
func pjIntParam(q url.Values, name string, def, min, max int) (int, string) {
	v := q.Get(name)
	if v == "" {
		return def, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, name + " must be a whole number from " + strconv.Itoa(min) + " to " + strconv.Itoa(max)
	}
	return n, ""
}

type pjEntry struct {
	Path        string  `json:"path"`
	Dir         bool    `json:"dir"`
	Size        *int64  `json:"size"`
	MTime       *string `json:"mtime"`
	Kind        string  `json:"kind,omitempty"`
	HasChildren bool    `json:"hasChildren"`
	Collapsed   bool    `json:"collapsed,omitempty"`
	Dirty       string  `json:"dirty,omitempty"`
}

func (a *ProjectAPI) handleTree(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.onDisk(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	dir, err := pjCleanRel(q.Get("dir"), true)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}
	depth, bad := pjIntParam(q, "depth", 1, 1, pjTreeDepthMax)
	if bad != "" {
		BadRequest(w, bad)
		return
	}
	if a.d.Tree == nil {
		WriteJSON(w, http.StatusOK, map[string]any{"dir": dir, "entries": []pjEntry{}})
		return
	}
	entries, err := a.d.Tree.ListDir(pr.Root, dir, depth)
	if err != nil {
		a.pathError(w, r, err)
		return
	}
	states, err := a.d.Store.ProjDirtyStates(r.Context(), pr.Row.ID)
	if err != nil {
		a.fail(w, r, "dirty states", err)
		return
	}
	out := make([]pjEntry, 0, len(entries))
	for _, e := range entries {
		pe := pjEntry{Path: e.Path, Dir: e.Dir, Kind: e.Kind, HasChildren: e.HasChildren, Collapsed: e.Collapsed}
		if !e.Dir {
			size := e.Size
			pe.Size = &size
			pe.Dirty = states[e.Path]
		}
		pe.MTime = a.isoPtr(e.MTime)
		out = append(out, pe)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"dir": dir, "entries": out})
}

// pjFileReply is the /file answer: the live read plus the notes pinned on the file.
type pjFileReply struct {
	tree.FileText
	Notes []store.ProjNote `json:"notes"`
}

func (a *ProjectAPI) handleFile(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.onDisk(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	rel, err := pjCleanRel(q.Get("path"), false)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}
	from, bad := pjIntParam(q, "from", 0, 0, pjLineMax)
	if bad != "" {
		BadRequest(w, bad)
		return
	}
	to, bad := pjIntParam(q, "to", 0, 0, pjLineMax)
	if bad != "" {
		BadRequest(w, bad)
		return
	}
	if a.d.Tree == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "the file reader is not set up")
		return
	}
	ft, err := a.d.Tree.ReadFile(pr.Root, rel, from, to)
	if err != nil {
		a.pathError(w, r, err)
		return
	}
	notes, err := a.d.Store.ProjFileNotes(r.Context(), pr.Row.ID, ft.Path)
	if err != nil {
		a.fail(w, r, "file notes", err)
		return
	}
	WriteJSON(w, http.StatusOK, pjFileReply{FileText: ft, Notes: notes})
}

func (a *ProjectAPI) handleRaw(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.onDisk(w, r)
	if !ok {
		return
	}
	rel, err := pjCleanRel(r.URL.Query().Get("path"), false)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}
	a.serveRaw(w, r, pr, rel)
}

// handleFiles is /raw with the path in the URL: /files/dir/a.glb. A relative URL inside a
// .gltf (its .bin and textures) then resolves to a sibling file by itself.
func (a *ProjectAPI) handleFiles(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.onDisk(w, r)
	if !ok {
		return
	}
	rel, err := pjCleanRel(r.PathValue("path"), false)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}
	a.serveRaw(w, r, pr, rel)
}

// serveRaw sends the bytes of the file rel (already cleaned) of the project pr, with the
// header rules in the file comment. It is the body of both /raw and /files.
func (a *ProjectAPI) serveRaw(w http.ResponseWriter, r *http.Request, pr pjProject, rel string) {
	if a.d.Tree == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "the file reader is not set up")
		return
	}
	abs, mime, err := a.d.Tree.RawPath(pr.Root, rel)
	if err != nil {
		a.pathError(w, r, err)
		return
	}
	// RawPath already checked the file. Open it without following a link at the last step.
	f, err := openNoFollow(abs)
	if err != nil {
		a.pathError(w, r, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		BadRequest(w, "the path is not a regular file")
		return
	}
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("X-Content-Type-Options", "nosniff")
	// A browser's PDF viewer is a plugin, and plugins never load in a sandboxed document,
	// so a PDF skips the sandbox. It is still a fixed application/pdf with nosniff, never a page.
	if mime != "application/pdf" {
		h.Set("Content-Security-Policy", "sandbox")
	}
	h.Set("Cache-Control", "no-cache") // keep Last-Modified checks, but never serve a stale image unchecked
	http.ServeContent(w, r, path.Base(rel), st.ModTime().Truncate(time.Second), f)
}
