package httpapi

// GET /api/desktop and POST /api/desktop {mode}: the Plash desktop mode buttons
// (contrib/desktop/README.md). The page sends only a mode name. The server runs the
// desktop_script from config.toml with that mode (actions.Config.PlanDesktop: no
// shell, script inside the project roots) and reads the mode back from the
// script's state file, which the Option-tap keys update too.

import (
	"net/http"
	"sync"

	"github.com/Prath2000/drashta/internal/actions"
)

// desktopMu serializes mode switches: plash-mode toggles Plash, so two runs at
// once could both toggle and land in the wrong mode.
var desktopMu sync.Mutex

func (t *trackerAPI) registerDesktop() {
	t.s.Handle("GET", "/api/desktop", t.getDesktop, false)
	t.s.Handle("POST", "/api/desktop", t.postDesktop, true)
}

func (t *trackerAPI) desktopView() map[string]any {
	_, err := t.d.Resolver.Actions().PlanDesktop(actions.DesktopModes[0])
	return map[string]any{
		"available": err == nil,
		"mode":      actions.ReadDesktopMode(t.d.DesktopState),
		"modes":     actions.DesktopModes,
	}
}

func (t *trackerAPI) getDesktop(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, t.desktopView())
}

func (t *trackerAPI) postDesktop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	cmd, err := t.d.Resolver.Actions().PlanDesktop(req.Mode)
	if err != nil {
		trkFail(w, err)
		return
	}
	desktopMu.Lock()
	err = t.d.Exec.Run(cmd)
	desktopMu.Unlock()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "action_failed", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, t.desktopView())
}
