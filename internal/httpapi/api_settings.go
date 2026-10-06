package httpapi

// GET /api/settings and PATCH /api/settings: {hiddenFolders[], author, editorCmd}.
//
// Where each value lives:
//   - hiddenFolders: config.toml is the source of truth (config.SetHidden changes only the hidden line).
//     The list is also saved in the store settings (key hidden_folders) and so in settings.json.
//     PATCH updates projects.hidden at once, so /boot shows the change without waiting for a sweep.
//   - author: store setting "author". Empty means "use the default" (git user.name, see GitUserName).
//   - editorCmd: READ ONLY. It is the editor argv from config.toml, joined with spaces. PATCH refuses
//     it with 400 read_only (security I-1): a page (or a stolen token) must never choose the program
//     that /open runs. To change the editor, edit `editor` in ~/.drashta/config.toml and restart.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/runx"
	"github.com/Prath2000/drashta/internal/store"
)

// Keys in the store settings table.
const (
	settingAuthor = "author"
	settingHidden = "hidden_folders"
)

const (
	ahMaxHiddenFolders = 500
	ahMaxNameLen       = 200
	ahMaxAuthorLen     = 100
)

// HomeSettings is the body of GET and PATCH /api/settings.
type HomeSettings struct {
	HiddenFolders []string `json:"hiddenFolders"`
	Author        string   `json:"author"`
	EditorCmd     string   `json:"editorCmd"`
}

// HomeConfig is the part of config.toml this API reads and changes.
type HomeConfig interface {
	// Hidden is the current hide list.
	Hidden() []string
	// SetHidden saves a new hide list to config.toml.
	SetHidden(names []string) error
	// IsHidden says whether a project (group folder, name) is on the list.
	IsHidden(group, name string) bool
	// Editor is the editor argv template, for example ["code","-g","{path}:{line}"].
	Editor() []string
}

// homeConfig adapts a *config.Config (and the path of its file) to HomeConfig.
// The Config holds its own lock (config.Config.mu), so this adapter needs none.
type homeConfig struct {
	cfg  *config.Config
	path string
}

// NewHomeConfig wraps a loaded config and the path of its file (config.DefaultPath()) as a HomeConfig.
// It is safe for concurrent use: the Config locks Hidden and Editor itself.
func NewHomeConfig(cfg *config.Config, path string) HomeConfig {
	return &homeConfig{cfg: cfg, path: path}
}

func (c *homeConfig) Hidden() []string { return c.cfg.HiddenList() }

func (c *homeConfig) SetHidden(names []string) error { return c.cfg.SetHidden(c.path, names) }

func (c *homeConfig) IsHidden(group, name string) bool { return c.cfg.IsHidden(group, name) }

func (c *homeConfig) Editor() []string { return c.cfg.EditorList() }

// GitUserName returns `git config user.name`, or "" when git or the setting is missing.
// Pass it as HomeDeps.DefaultAuthor.
func GitUserName(ctx context.Context) string {
	res, err := runx.Run(ctx, runx.Options{Timeout: 3 * time.Second, MaxOut: 4096}, "git", "config", "--get", "user.name")
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}

func (h *homeAPI) registerSettings() {
	h.s.Handle(http.MethodGet, "/api/settings", h.handleSettingsGet, false)
	h.s.Handle(http.MethodPatch, "/api/settings", h.handleSettingsPatch, true)
}

// settings reads the current values.
func (h *homeAPI) settings(ctx context.Context) (HomeSettings, error) {
	out := HomeSettings{HiddenFolders: []string{}}
	if h.d.Config != nil {
		out.HiddenFolders = append(out.HiddenFolders, h.d.Config.Hidden()...)
	} else if v, ok, err := h.d.Store.GetSetting(ctx, settingHidden); err != nil {
		return out, err
	} else if ok {
		var names []string
		if json.Unmarshal([]byte(v), &names) == nil {
			out.HiddenFolders = append(out.HiddenFolders, names...)
		}
	}
	out.Author = h.author(ctx)
	if h.d.Config != nil {
		out.EditorCmd = strings.Join(h.d.Config.Editor(), " ") // the editor that really runs
	}
	return out, nil
}

func (h *homeAPI) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings(r.Context())
	if err != nil {
		h.fail(w, "settings", err)
		return
	}
	WriteJSON(w, http.StatusOK, s)
}

type ahSettingsPatch struct {
	HiddenFolders *[]string `json:"hiddenFolders"`
	Author        *string   `json:"author"`
	// EditorCmd is only checked for presence: any value is refused (read only, security I-1).
	EditorCmd json.RawMessage `json:"editorCmd"`
}

func (h *homeAPI) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req ahSettingsPatch
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.EditorCmd != nil {
		// The editor is never set through the API (security I-1). Nothing else is written either.
		WriteError(w, http.StatusBadRequest, "read_only", "set `editor` in ~/.drashta/config.toml")
		return
	}
	if req.HiddenFolders == nil && req.Author == nil {
		BadRequest(w, "nothing to change: send hiddenFolders or author")
		return
	}

	// Check everything before anything is written.
	var names []string
	if req.HiddenFolders != nil {
		seen := map[string]bool{}
		names = []string{}
		for _, n := range *req.HiddenFolders {
			n = strings.TrimSpace(n)
			if n == "" || seen[n] {
				continue
			}
			if len(n) > ahMaxNameLen || !ahPlain(n) {
				BadRequest(w, "a hidden folder name is too long or has control characters")
				return
			}
			seen[n] = true
			names = append(names, n)
		}
		if len(names) > ahMaxHiddenFolders {
			BadRequest(w, "too many hidden folders")
			return
		}
	}
	var author string
	if req.Author != nil {
		author = strings.TrimSpace(*req.Author)
		if len(author) > ahMaxAuthorLen || !ahPlain(author) {
			BadRequest(w, "author is too long or has control characters")
			return
		}
	}

	// Write.
	var changes []store.HomeHiddenChange
	if req.HiddenFolders != nil {
		var err error
		if changes, err = h.applyHidden(ctx, names); err != nil {
			h.log.Error("settings: hidden folders", "err", err)
			Internal(w, err.Error())
			return
		}
	}
	if req.Author != nil {
		if err := h.setOrDelete(ctx, settingAuthor, author); err != nil {
			h.fail(w, "settings", err)
			return
		}
	}

	// Tell the pages and the indexer.
	for _, c := range changes {
		h.note.Publish(live.ProjectEvent(c.ID, live.WhatAll))
		if !c.Hidden && h.d.Indexer != nil {
			h.startRefresh(c.ID, false) // a project that was hidden has not been walked: do it now
		}
	}

	now, err := h.settings(ctx)
	if err != nil {
		h.fail(w, "settings", err)
		return
	}
	if h.d.OnSettings != nil {
		h.d.OnSettings(now)
	}
	WriteJSON(w, http.StatusOK, now)
}

// applyHidden saves the list to config.toml first (the source of truth), then to the store settings,
// then updates projects.hidden. It returns the projects whose flag changed.
func (h *homeAPI) applyHidden(ctx context.Context, names []string) ([]store.HomeHiddenChange, error) {
	rule := ahHiddenRule(names)
	if h.d.Config != nil {
		if err := h.d.Config.SetHidden(names); err != nil {
			return nil, ahErr("could not save config.toml: " + err.Error())
		}
		rule = h.d.Config.IsHidden
	}
	b, _ := json.Marshal(names)
	if err := h.d.Store.SetSetting(ctx, settingHidden, string(b)); err != nil {
		return nil, ahErr("saved to config.toml but not to the index: " + err.Error())
	}
	st, err := h.d.Store.HomeApplyHidden(ctx, rule)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// setOrDelete saves a setting, or removes it when the value is empty.
func (h *homeAPI) setOrDelete(ctx context.Context, key, value string) error {
	if value == "" {
		return h.d.Store.DeleteSetting(ctx, key)
	}
	return h.d.Store.SetSetting(ctx, key, value)
}

// ahHiddenRule is the same rule as config.Config.IsHidden, for when there is no config object:
// an entry matches the folder name, "group/name" or "group.name".
func ahHiddenRule(names []string) func(group, name string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(group, name string) bool {
		return set[name] || set[group+"/"+name] || set[group+"."+name]
	}
}

// ahErr is a plain error made from text.
type ahErr string

func (e ahErr) Error() string { return string(e) }
