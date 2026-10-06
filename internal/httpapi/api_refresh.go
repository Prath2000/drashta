package httpapi

// POST /api/refresh {projectId?, withGH?}  ->  {started:true}
//
// It starts a rescan and returns at once. The indexer publishes the "scan" events while it works.
// No projectId means "refresh all". With a projectId the GitHub data is refreshed too, unless the body
// says withGH:false. While a refresh of the same scope is still running, a second request starts
// nothing new and still answers {started:true}.

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ahRefreshTimeout stops a refresh that never ends from holding its slot forever.
const ahRefreshTimeout = 15 * time.Minute

type ahRefreshReq struct {
	ProjectID string `json:"projectId"`
	WithGH    *bool  `json:"withGH"`
}

func (h *homeAPI) registerRefresh() {
	h.s.Handle(http.MethodPost, "/api/refresh", h.handleRefresh, true)
}

func (h *homeAPI) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if h.d.Indexer == nil {
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "the indexer is not running")
		return
	}
	var req ahRefreshReq
	if r.ContentLength != 0 { // an empty body means "refresh all"
		if !ReadJSON(w, r, &req) {
			return
		}
	}
	if req.ProjectID != "" {
		if len(req.ProjectID) > 200 || !ahPlain(req.ProjectID) {
			BadRequest(w, "bad project id")
			return
		}
		ok, err := h.d.Store.HomeProjectExists(r.Context(), req.ProjectID)
		if err != nil {
			h.fail(w, "refresh", err)
			return
		}
		if !ok {
			NotFound(w, "unknown project "+req.ProjectID)
			return
		}
	}
	withGH := req.ProjectID != ""
	if req.WithGH != nil {
		withGH = *req.WithGH
	}
	h.startRefresh(req.ProjectID, withGH)
	WriteJSON(w, http.StatusOK, map[string]bool{"started": true})
}

// startRefresh runs the refresh in the background. It returns false when one with the same scope
// ("" = all) is already running. A panic in the indexer is logged and does not stop the server.
func (h *homeAPI) startRefresh(projectID string, withGH bool) bool {
	h.mu.Lock()
	if h.running[projectID] {
		h.mu.Unlock()
		return false
	}
	h.running[projectID] = true
	h.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ahRefreshTimeout)
		defer func() {
			cancel()
			if rec := recover(); rec != nil {
				h.log.Error("refresh panic", "project", projectID, "panic", fmt.Sprint(rec))
			}
			h.mu.Lock()
			delete(h.running, projectID)
			h.mu.Unlock()
		}()
		if projectID == "" {
			h.d.Indexer.RefreshAll(ctx)
		} else {
			h.d.Indexer.Refresh(ctx, projectID, withGH)
		}
	}()
	return true
}
