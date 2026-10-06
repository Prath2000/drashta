package httpapi

// GET /api/resolve?ref=  (04-data-model.md section 6): the preview of any ref.

import "net/http"

func (t *trackerAPI) registerResolve() {
	t.s.Handle("GET", "/api/resolve", t.getResolve, false)
}

func (t *trackerAPI) getResolve(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		BadRequest(w, "ref is required")
		return
	}
	res, err := t.d.Resolver.Resolve(r.Context(), ref)
	if err != nil {
		trkFail(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}
