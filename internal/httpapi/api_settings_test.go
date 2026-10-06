package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/live"
)

type ahSettingsResp struct {
	HiddenFolders []string `json:"hiddenFolders"`
	Author        string   `json:"author"`
	EditorCmd     string   `json:"editorCmd"`
}

func (e *ahEnv) settings() ahSettingsResp {
	e.t.Helper()
	var s ahSettingsResp
	ahDecode(e.t, e.get("/api/settings"), 200, &s)
	return s
}

func (e *ahEnv) patch(body string) *httptest.ResponseRecorder {
	return e.do("PATCH", "/api/settings", body, true)
}

func TestHomeSettingsGetDefaults(t *testing.T) {
	e := ahNew(t, nil)
	s := e.settings()
	if !reflect.DeepEqual(s.HiddenFolders, []string{"beta"}) || s.Author != "gitname" || s.EditorCmd != "code -g {path}:{line}" {
		t.Errorf("settings = %+v", s)
	}
	// keys are exactly the three of 04 section 6
	m := ahRaw(t, e.get("/api/settings"), 200)
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	if len(keys) != 3 {
		t.Errorf("keys = %v", keys)
	}
}

func TestHomeSettingsPatchHiddenPersistsEverywhere(t *testing.T) {
	var applied []HomeSettings
	e := ahNew(t, func(d *HomeDeps) { d.OnSettings = func(s HomeSettings) { applied = append(applied, s) } })
	e.project("alpha", "personal", "git", "main", false, true, "")
	e.project("beta", "tools", "", "", true, true, "")
	e.project("dup", "tools", "", "", false, true, "")

	rec := e.patch(`{"hiddenFolders":[" alpha ","tools/dup","alpha","","venv"]}`)
	var got ahSettingsResp
	ahDecode(t, rec, 200, &got)
	if want := []string{"alpha", "tools/dup", "venv"}; !reflect.DeepEqual(got.HiddenFolders, want) {
		t.Errorf("reply = %v, want %v", got.HiddenFolders, want)
	}
	if !reflect.DeepEqual(e.settings().HiddenFolders, []string{"alpha", "tools/dup", "venv"}) {
		t.Errorf("GET after PATCH = %v", e.settings().HiddenFolders)
	}

	// 1. config.toml: only the hidden line changed, and a fresh Load reads the same list
	text, err := os.ReadFile(e.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), `hidden  = ["alpha", "tools/dup", "venv"]`) || !strings.Contains(string(text), "# test config") ||
		!strings.Contains(string(text), `roots = ["personal", "tools"]`) {
		t.Errorf("config.toml:\n%s", text)
	}
	again, err := config.Load(e.cfgPath)
	if err != nil || !reflect.DeepEqual(again.Hidden, []string{"alpha", "tools/dup", "venv"}) {
		t.Errorf("reloaded config: %v %v", again, err)
	}

	// 2. store settings and the settings.json mirror
	v, ok, err := e.db.GetSetting(context.Background(), "hidden_folders")
	if err != nil || !ok || v != `["alpha","tools/dup","venv"]` {
		t.Errorf("store setting = %q %v %v", v, ok, err)
	}
	mirror, err := os.ReadFile(filepath.Join(e.dir, "data", "settings.json"))
	if err != nil || !strings.Contains(string(mirror), "hidden_folders") || !strings.Contains(string(mirror), "tools/dup") {
		t.Errorf("settings.json = %q %v", mirror, err)
	}

	// 3. projects.hidden follows at once, so /boot shows it without a sweep
	var boot struct {
		Projects []struct {
			ID     string `json:"id"`
			Hidden bool   `json:"hidden"`
		} `json:"projects"`
	}
	ahDecode(t, e.get("/api/boot"), 200, &boot)
	hidden := map[string]bool{}
	for _, p := range boot.Projects {
		hidden[p.ID] = p.Hidden
	}
	if want := map[string]bool{"alpha": true, "beta": false, "dup": true}; !reflect.DeepEqual(hidden, want) {
		t.Errorf("hidden flags = %v, want %v", hidden, want)
	}

	// 4. one project event per project whose flag changed (alpha hidden, beta shown, dup hidden); no resync
	var ids []string
	for _, ev := range e.note.snapshot() {
		if ev.Type != live.TypeProject {
			t.Errorf("unexpected event type %q", ev.Type)
			continue
		}
		var d live.ProjectData
		if err := json.Unmarshal(ev.Data, &d); err != nil || d.What != live.WhatAll {
			t.Errorf("event data = %s", ev.Data)
		}
		ids = append(ids, d.ProjectID)
	}
	if want := []string{"alpha", "beta", "dup"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("project events for %v, want %v", ids, want)
	}

	// 5. the project that became visible is scanned now (it was never walked while hidden); hidden ones are not
	if got := e.idx.wait(t); got != "project:beta:gh=false" {
		t.Errorf("indexer call = %q", got)
	}
	e.idx.noCall(t)

	if len(applied) != 1 || !reflect.DeepEqual(applied[0].HiddenFolders, []string{"alpha", "tools/dup", "venv"}) {
		t.Errorf("OnSettings = %+v", applied)
	}

	// an empty list shows everything again
	var cleared ahSettingsResp
	ahDecode(t, e.patch(`{"hiddenFolders":[]}`), 200, &cleared)
	if cleared.HiddenFolders == nil || len(cleared.HiddenFolders) != 0 {
		t.Errorf("cleared = %#v (want [])", cleared.HiddenFolders)
	}
	text, _ = os.ReadFile(e.cfgPath)
	if !strings.Contains(string(text), "hidden  = []") {
		t.Errorf("config.toml after clear:\n%s", text)
	}
}

func TestHomeSettingsPatchAuthor(t *testing.T) {
	e := ahNew(t, nil)
	var s ahSettingsResp
	ahDecode(t, e.patch(`{"author":"  Alex  "}`), 200, &s)
	if s.Author != "Alex" || s.EditorCmd != "code -g {path}:{line}" || !reflect.DeepEqual(s.HiddenFolders, []string{"beta"}) {
		t.Errorf("reply = %+v", s)
	}
	if got := e.settings(); !reflect.DeepEqual(got, s) {
		t.Errorf("GET = %+v, want %+v", got, s)
	}
	// boot uses the saved author
	var boot struct {
		Author string `json:"author"`
	}
	ahDecode(t, e.get("/api/boot"), 200, &boot)
	if boot.Author != "Alex" {
		t.Errorf("boot author = %q", boot.Author)
	}
	// an unrelated PATCH keeps it
	ahDecode(t, e.patch(`{"hiddenFolders":["beta"]}`), 200, &s)
	if s.Author != "Alex" {
		t.Errorf("after unrelated patch = %+v", s)
	}
	// an empty string goes back to the default
	ahDecode(t, e.patch(`{"author":""}`), 200, &s)
	if s.Author != "gitname" {
		t.Errorf("after reset = %+v", s)
	}
	all, _ := e.db.Settings(context.Background())
	if _, ok := all["author"]; ok {
		t.Errorf("author setting still there: %v", all)
	}
}

// Security I-1: the editor is read only through the API. A PATCH with editorCmd is refused
// with 400 read_only and writes nothing at all (not the other fields either); GET still
// shows the editor from config.toml.
func TestHomeSettingsEditorIsReadOnly(t *testing.T) {
	var applied int
	e := ahNew(t, func(d *HomeDeps) { d.OnSettings = func(HomeSettings) { applied++ } })
	before, _ := os.ReadFile(e.cfgPath)
	for _, body := range []string{
		`{"editorCmd":"sh -c touch$IFS/tmp/x"}`,
		`{"editorCmd":["sh","-c","touch x"]}`,
		`{"editorCmd":"zed {path}"}`,
		`{"author":"someone","editorCmd":"zed {path}"}`,
		`{"hiddenFolders":[],"editorCmd":""}`,
	} {
		rec := e.patch(body)
		if rec.Code != 400 {
			t.Fatalf("%s: status %d, want 400", body, rec.Code)
		}
		var er struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || er.Error.Code != "read_only" ||
			!strings.Contains(er.Error.Message, "config.toml") {
			t.Fatalf("%s: body %s", body, rec.Body.String())
		}
	}
	after, _ := os.ReadFile(e.cfgPath)
	if string(after) != string(before) {
		t.Errorf("config.toml changed:\n%s", after)
	}
	all, _ := e.db.Settings(context.Background())
	if len(all) != 0 {
		t.Errorf("settings stored by a refused PATCH: %v", all)
	}
	if applied != 0 {
		t.Errorf("OnSettings called %d times", applied)
	}
	if s := e.settings(); s.EditorCmd != "code -g {path}:{line}" || s.Author != "gitname" || !reflect.DeepEqual(s.HiddenFolders, []string{"beta"}) {
		t.Errorf("GET = %+v", s)
	}
}

func TestHomeSettingsWithoutConfig(t *testing.T) {
	e := ahNew(t, func(d *HomeDeps) { d.Config = nil })
	e.project("alpha", "g", "", "", false, true, "")
	e.project("beta", "g", "", "", true, true, "")
	if s := e.settings(); len(s.HiddenFolders) != 0 || s.EditorCmd != "" {
		t.Errorf("settings = %+v", s)
	}
	var s ahSettingsResp
	ahDecode(t, e.patch(`{"hiddenFolders":["g.alpha","g/beta"]}`), 200, &s)
	if !reflect.DeepEqual(s.HiddenFolders, []string{"g.alpha", "g/beta"}) {
		t.Errorf("reply = %+v", s)
	}
	if !reflect.DeepEqual(e.settings().HiddenFolders, []string{"g.alpha", "g/beta"}) {
		t.Errorf("GET = %+v", e.settings())
	}
	hidden, _ := e.db.HomeHiddenIDs(context.Background())
	if !reflect.DeepEqual(hidden, map[string]bool{"alpha": true, "beta": true}) {
		t.Errorf("projects.hidden = %v (the same rule as config.IsHidden: group.name and group/name)", hidden)
	}
}

func TestHomeSettingsValidation(t *testing.T) {
	e := ahNew(t, nil)
	long := strings.Repeat("x", 301)
	tooMany := make([]string, 0, 501)
	for i := 0; i < 501; i++ {
		tooMany = append(tooMany, "f"+strings.Repeat("a", i%5)+string(rune('A'+i%26))+string(rune('a'+(i/26)%26))+string(rune('a'+i/676)))
	}
	tm, _ := json.Marshal(map[string]any{"hiddenFolders": tooMany})
	bad := map[string]string{
		"nothing to change":     `{}`,
		"only unknown keys":     `{"colour":"red"}`,
		"null values":           `{"hiddenFolders":null,"author":null}`,
		"not json":              `not json`,
		"empty body":            ``,
		"two documents":         `{"author":"a"} {"author":"b"}`,
		"hidden not a list":     `{"hiddenFolders":"venv"}`,
		"hidden item not text":  `{"hiddenFolders":[1,2]}`,
		"author not text":       `{"author":5}`,
		"control char in name":  "{\"hiddenFolders\":[\"a\\nb\"]}",
		"nul in name":           "{\"hiddenFolders\":[\"a\\u0000b\"]}",
		"name too long":         `{"hiddenFolders":["` + strings.Repeat("n", 201) + `"]}`,
		"too many names":        string(tm),
		"author control char":   "{\"author\":\"a\\tb\"}",
		"author too long":       `{"author":"` + strings.Repeat("a", 101) + `"}`,
		"editor control char":   "{\"editorCmd\":\"code\\nrm\"}",
		"editor too long":       `{"editorCmd":"` + long + `"}`,
		"good field, bad field": `{"author":"fine","hiddenFolders":["a\u0007b"]}`,
	}
	for name, body := range bad {
		rec := e.patch(body)
		if rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
	// a rejected request wrote nothing
	if s := e.settings(); s.Author != "gitname" || !reflect.DeepEqual(s.HiddenFolders, []string{"beta"}) {
		t.Errorf("settings changed by a rejected PATCH: %+v", s)
	}
	text, _ := os.ReadFile(e.cfgPath)
	if !strings.Contains(string(text), `hidden = ["beta"]`) {
		t.Errorf("config.toml changed by a rejected PATCH:\n%s", text)
	}
	if n := len(e.note.snapshot()); n != 0 {
		t.Errorf("%d events published by rejected PATCHes", n)
	}
}

func TestHomeSettingsPatchNeedsTokenAndJSON(t *testing.T) {
	e := ahNew(t, nil)
	if rec := e.do("PATCH", "/api/settings", `{"author":"x"}`, false); rec.Code != 403 {
		t.Errorf("no token gave %d", rec.Code)
	}
	req := newReq("PATCH", "/api/settings", `{"author":"x"}`)
	req.Header.Set(TokenHeader, ahTestToken)
	req.Header.Set("Content-Type", "text/plain")
	rec := serve(e, req)
	if rec.Code != 415 {
		t.Errorf("text/plain gave %d", rec.Code)
	}
	if e.settings().Author != "gitname" {
		t.Error("the author changed although the request was refused")
	}
}

func TestHomeSettingsConfigWriteFailure(t *testing.T) {
	e := ahNew(t, nil)
	// A directory where config.toml should be makes the atomic rename fail.
	if err := os.Remove(e.cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(e.cfgPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.cfgPath, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ahErrCode(t, e.patch(`{"hiddenFolders":["a"]}`), 500, "internal")
	if v, ok, _ := e.db.GetSetting(context.Background(), "hidden_folders"); ok {
		t.Errorf("the store was written although config.toml failed: %q", v)
	}
}

func TestHomeGitUserName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	gc := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(gc, []byte("[user]\n\tname = Test Person\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", dir)
	if got := GitUserName(context.Background()); got != "Test Person" {
		t.Errorf("GitUserName = %q", got)
	}
	if err := os.WriteFile(gc, []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GitUserName(context.Background()); got != "" {
		t.Errorf("without a name GitUserName = %q", got)
	}
}
