package httpapi

// POST /api/open {ref, action?} (03-architecture.md section 7, decisions U9 and D4).
// The page sends only a ref and an action name. The server finds the item in the index,
// builds the command with actions.Config.Plan (argv only, paths through pathguard, inside
// the project roots) and runs it with the injected executor. It never takes a path or a
// command from the request.

import (
	"net/http"

	"github.com/Prath2000/drashta/internal/actions"
)

func (t *trackerAPI) registerOpen() {
	t.s.Handle("POST", "/api/open", t.postOpen, true)
}

func (t *trackerAPI) postOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ref    string `json:"ref"`
		Action string `json:"action"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Ref == "" {
		BadRequest(w, "ref is required")
		return
	}
	switch req.Action {
	case "", actions.ActionDefault, actions.ActionEditor, actions.ActionFinder, actions.ActionTerminal,
		actions.ActionSafari, actions.ActionResume, actions.ActionApp:
	default:
		BadRequest(w, "unknown action "+trkQuote(req.Action))
		return
	}
	target, err := t.d.Resolver.Target(r.Context(), req.Ref)
	if err != nil {
		trkFail(w, err)
		return
	}
	ran, err := t.d.Resolver.Actions().Do(r.Context(), t.d.Exec, target, req.Action)
	if err != nil {
		if ran != "" { // planned, but the program failed
			WriteError(w, http.StatusInternalServerError, "action_failed", err.Error())
			return
		}
		trkFail(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "ran": ran})
}
