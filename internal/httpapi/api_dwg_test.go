package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Prath2000/drashta/internal/drawing"
)

// pjtDrawings is a fake ProjectDrawings: it records the paths it is asked about and answers
// with canned bytes, a canned JSONL file or a canned error.
type pjtDrawings struct {
	mu        sync.Mutex
	layers    []byte
	entities  string // path of the "cached" JSONL file
	layersErr error
	entErr    error
	seen      []string
}

func (d *pjtDrawings) Layers(_ context.Context, abs string) ([]byte, error) {
	d.mu.Lock()
	d.seen = append(d.seen, abs)
	d.mu.Unlock()
	return d.layers, d.layersErr
}

func (d *pjtDrawings) Entities(_ context.Context, abs string) (string, error) {
	d.mu.Lock()
	d.seen = append(d.seen, abs)
	d.mu.Unlock()
	return d.entities, d.entErr
}

func (d *pjtDrawings) last() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.seen) == 0 {
		return ""
	}
	return d.seen[len(d.seen)-1]
}

const pjtJSONL = `{"format_version":2,"space":"model"}` + "\n" + `{"type":"LINE","layer":"A-WALL"}` + "\n"

// pjtDwgEnv is a test world with a fake drawing reader and a few invented .dwg files.
func pjtDwgEnv(t *testing.T) (*pjtEnv, *pjtDrawings) {
	t.Helper()
	fake := &pjtDrawings{layers: []byte(`{"layers":[{"name":"A-WALL"}]}`)}
	e := pjtNew(t, func(d *ProjectDeps) { d.Drawings = fake })
	cache := filepath.Join(t.TempDir(), "cached.jsonl")
	if err := os.WriteFile(cache, []byte(pjtJSONL), 0o600); err != nil {
		t.Fatal(err)
	}
	fake.entities = cache
	pjtWrite(t, filepath.Join(e.alpha, "plans", "site.dwg"), "AC1024 not really")
	pjtWrite(t, filepath.Join(e.alpha, "plans", "Overall Layout #3 100% é.DWG"), "AC1024 not really")
	pjtWrite(t, filepath.Join(e.alpha, "plans", "site.dxf"), "0\nSECTION\n")
	return e, fake
}

func dwgURL(kind, rel string) string {
	return "/api/projects/alpha/dwg/" + kind + "?path=" + url.QueryEscape(rel)
}

func TestProjectDwgOK(t *testing.T) {
	e, fake := pjtDwgEnv(t)

	rec := e.get(dwgURL("layers", "plans/site.dwg"))
	pjtWant(t, rec, 200)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("layers Content-Type = %q", ct)
	}
	if rec.Body.String() != `{"layers":[{"name":"A-WALL"}]}` {
		t.Errorf("layers body = %q", rec.Body.String())
	}
	if want := filepath.Join(e.alpha, "plans", "site.dwg"); !samePath(fake.last(), want) {
		t.Errorf("reader got %q, want the file inside the project (%q)", fake.last(), want)
	}

	rec = e.get(dwgURL("entities", "plans/site.dwg"))
	pjtWant(t, rec, 200)
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("entities Content-Type = %q", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("entities: no nosniff header")
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("entities Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Body.String() != pjtJSONL {
		t.Errorf("entities body = %q", rec.Body.String())
	}
	// ServeContent: a range request works, so a client may fetch the tail of a big file.
	rec = e.getH(dwgURL("entities", "plans/site.dwg"), map[string]string{"Range": "bytes=0-5"})
	if rec.Code != 206 || rec.Body.String() != pjtJSONL[:6] {
		t.Errorf("range: %d %q", rec.Code, rec.Body.String())
	}
}

func TestProjectDwgFileNameWithSpacesHashPercentUnicode(t *testing.T) {
	e, fake := pjtDwgEnv(t)
	rel := "plans/Overall Layout #3 100% é.DWG" // upper-case extension is fine
	for _, kind := range []string{"layers", "entities"} {
		rec := e.get(dwgURL(kind, rel))
		pjtWant(t, rec, 200)
		want := filepath.Join(e.alpha, "plans", "Overall Layout #3 100% é.DWG")
		if !samePath(fake.last(), want) {
			t.Errorf("%s: reader got %q, want %q (the query is decoded once)", kind, fake.last(), want)
		}
	}
}

func TestProjectDwgRefusals(t *testing.T) {
	e, fake := pjtDwgEnv(t)
	pjtWrite(t, filepath.Join(e.alpha, "folder.dwg", "inner.txt"), "x")
	if err := os.Symlink(filepath.Join(e.alpha, ".env"), filepath.Join(e.alpha, "plans", "env-link.dwg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.outside, "secret.txt"), filepath.Join(e.alpha, "plans", "out-link.dwg")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string // the raw query string after "?"
		want int
	}{
		{"not a dwg", "path=" + url.QueryEscape("plans/site.dxf"), 400},
		{"no extension", "path=" + url.QueryEscape("README"), 400},
		{"no path", "", 400},
		{"empty path", "path=", 400},
		{"absolute", "path=" + url.QueryEscape("/etc/x.dwg"), 400},
		{"dot dot", "path=" + url.QueryEscape("../x.dwg"), 400},
		{"dot dot inside", "path=" + url.QueryEscape("plans/../../x.dwg"), 400},
		{"missing", "path=" + url.QueryEscape("plans/gone.dwg"), 404},
		{"a folder", "path=" + url.QueryEscape("folder.dwg"), 400},
		{"link to a secret", "path=" + url.QueryEscape("plans/env-link.dwg"), 403},
		{"link out of the project", "path=" + url.QueryEscape("plans/out-link.dwg"), 403},
	}
	for _, kind := range []string{"layers", "entities"} {
		for _, c := range cases {
			rec := e.get("/api/projects/alpha/dwg/" + kind + "?" + c.path)
			if rec.Code != c.want {
				t.Errorf("%s %s: status = %d, want %d (%s)", kind, c.name, rec.Code, c.want, rec.Body.String())
				continue
			}
			pjtWant(t, rec, c.want)
			if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "TOP SECRET") {
				t.Errorf("%s %s leaked", kind, c.name)
			}
		}
		pjtWant(t, e.get("/api/projects/nosuch/dwg/"+kind+"?path=plans/site.dwg"), 404)
	}
	if len(fake.seen) != 0 {
		t.Errorf("the reader was asked about %v although every request was refused", fake.seen)
	}
}

func TestProjectDwgUnavailable(t *testing.T) {
	e, fake := pjtDwgEnv(t)
	msg := "/Users/x/.local/bin/chitralipi not found; set chitralipi in config.toml"
	fake.layersErr = fmt.Errorf("%w: %s", drawing.ErrUnavailable, msg)
	fake.entErr = fake.layersErr
	for _, kind := range []string{"layers", "entities"} {
		rec := e.get(dwgURL(kind, "plans/site.dwg"))
		pjtWant(t, rec, 503)
		var body struct {
			Error struct{ Code, Message string }
		}
		pjtJSON(t, rec, &body)
		if body.Error.Code != "unavailable" {
			t.Errorf("%s: code = %q, want unavailable", kind, body.Error.Code)
		}
		if body.Error.Message != msg {
			t.Errorf("%s: message = %q, want %q", kind, body.Error.Message, msg)
		}
	}
}

func TestProjectDwgBadDrawing(t *testing.T) {
	e, fake := pjtDwgEnv(t)
	fake.layersErr = fmt.Errorf("%w: %s", drawing.ErrBadDrawing, "x.dwg is not a DWG")
	fake.entErr = fake.layersErr
	for _, kind := range []string{"layers", "entities"} {
		rec := e.get(dwgURL(kind, "plans/site.dwg"))
		pjtWant(t, rec, 422)
		var body struct {
			Error struct{ Code, Message string }
		}
		pjtJSON(t, rec, &body)
		if body.Error.Code != "bad_drawing" {
			t.Errorf("%s: code = %q, want bad_drawing", kind, body.Error.Code)
		}
		if body.Error.Message != "x.dwg is not a DWG" {
			t.Errorf("%s: message = %q", kind, body.Error.Message)
		}
	}
}

func TestProjectDwgOtherErrorsAre500WithoutDetail(t *testing.T) {
	e, fake := pjtDwgEnv(t)
	fake.layersErr = errors.New("disk exploded at /secret/place")
	fake.entErr = fake.layersErr
	for _, kind := range []string{"layers", "entities"} {
		rec := e.get(dwgURL(kind, "plans/site.dwg"))
		pjtWant(t, rec, 500)
		if strings.Contains(rec.Body.String(), "exploded") || strings.Contains(rec.Body.String(), "/secret/place") {
			t.Errorf("%s: the detail leaked: %s", kind, rec.Body.String())
		}
	}
	// A cached file that vanished between the reader and the response is a 500 too.
	fake.entErr = nil
	fake.entities = filepath.Join(t.TempDir(), "gone.jsonl")
	pjtWant(t, e.get(dwgURL("entities", "plans/site.dwg")), 500)
}

func TestProjectDwgNoReaderIs503(t *testing.T) {
	e := pjtNew(t, nil) // Drawings is nil
	pjtWrite(t, filepath.Join(e.alpha, "plans", "site.dwg"), "AC1024")
	for _, kind := range []string{"layers", "entities"} {
		pjtWant(t, e.get(dwgURL(kind, "plans/site.dwg")), 503)
	}
}

// samePath compares two paths after resolving links (the temp dir may be a link on macOS).
func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return ra == rb
}
