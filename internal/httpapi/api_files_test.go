package httpapi

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectTreeLazy(t *testing.T) {
	e := pjtNew(t, nil)
	type entry struct {
		Path        string
		Dir         bool
		Size        *int64
		MTime       *string
		Kind        string
		HasChildren bool
		Collapsed   bool
		Dirty       string
	}
	type reply struct {
		Dir     string
		Entries []entry
	}
	by := func(r reply) map[string]entry {
		m := map[string]entry{}
		for _, en := range r.Entries {
			m[en.Path] = en
		}
		return m
	}

	rec := e.get("/api/projects/alpha/tree")
	pjtWant(t, rec, 200)
	var root reply
	pjtJSON(t, rec, &root)
	m := by(root)
	if root.Dir != "" || m["src"].Path == "" || !m["src"].Dir || !m["src"].HasChildren || m["src"].Size != nil {
		t.Errorf("root listing = %+v", root)
	}
	if _, deep := m["src/main.go"]; deep {
		t.Error("depth 1 must not list grandchildren")
	}
	if nm := m["node_modules"]; !nm.Dir || !nm.Collapsed {
		t.Errorf("node_modules should be listed and collapsed: %+v", nm)
	}
	if env, ok := m[".env"]; !ok || env.Size == nil || env.Dirty != "?" {
		t.Errorf(".env must be listed (its body is hidden elsewhere): %+v", env)
	}
	if png := m["logo.png"]; png.Kind != "image" || png.MTime == nil {
		t.Errorf("logo.png = %+v", png)
	}
	// Folders come first.
	sawFile := false
	for _, en := range root.Entries {
		if !en.Dir {
			sawFile = true
		} else if sawFile {
			t.Errorf("folder %s after a file", en.Path)
		}
	}

	// A sub folder, with the dirty mark on the changed file.
	rec = e.get("/api/projects/alpha/tree?dir=src")
	pjtWant(t, rec, 200)
	var sub reply
	pjtJSON(t, rec, &sub)
	if sub.Dir != "src" || len(sub.Entries) != 1 || sub.Entries[0].Path != "src/main.go" || sub.Entries[0].Dirty != "M" {
		t.Errorf("src listing = %+v", sub)
	}
	// depth=2 lists the grandchildren in one call; a trailing slash is cleaned.
	rec = e.get("/api/projects/alpha/tree?dir=./&depth=2")
	pjtWant(t, rec, 200)
	var deep reply
	pjtJSON(t, rec, &deep)
	if _, ok := by(deep)["src/main.go"]; !ok {
		t.Errorf("depth 2 listing misses src/main.go: %+v", deep)
	}
	pjtWant(t, e.get("/api/projects/alpha/tree?dir=src/"), 200)
}

func TestProjectTreeRefusals(t *testing.T) {
	e := pjtNew(t, nil)
	for name, q := range map[string]string{
		"dotdot":         "dir=..",
		"dotdot deep":    "dir=src/../../..",
		"absolute":       "dir=" + url.QueryEscape(e.outside),
		"absolute slash": "dir=/",
		"encoded dotdot": "dir=%2e%2e%2foutside",
		"nul":            "dir=src%00",
		"depth zero":     "depth=0",
		"depth huge":     "depth=99",
		"depth text":     "depth=x",
	} {
		rec := e.get("/api/projects/alpha/tree?" + q)
		if rec.Code != 400 {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
	// A link that leaves the project is 403 and the outside folder is never listed.
	rec := e.get("/api/projects/alpha/tree?dir=escape")
	pjtWant(t, rec, 403)
	if strings.Contains(rec.Body.String(), "secret.txt") {
		t.Error("the outside folder was listed")
	}
	pjtWant(t, e.get("/api/projects/alpha/tree?dir=nope"), 404)
	pjtWant(t, e.get("/api/projects/alpha/tree?dir=README.md"), 400) // a file, not a folder
}

func TestProjectFileRead(t *testing.T) {
	e := pjtNew(t, nil)
	type reply struct {
		Path      string
		Size      int64
		MTime     string
		Kind      string
		IsDoc     bool
		Lines     int
		From, To  int
		Text      string
		Truncated bool
		Binary    bool
		Redacted  bool
		Notes     []struct {
			ID, Ref, Text string
			Line          *int
			ProjectID     string
			Resolved      bool
		}
	}
	rec := e.get("/api/projects/alpha/file?path=src/main.go")
	pjtWant(t, rec, 200)
	var f reply
	pjtJSON(t, rec, &f)
	if f.Path != "src/main.go" || f.Kind != "code" || f.IsDoc || f.Lines != 5 || f.From != 1 || f.To != 5 ||
		!strings.Contains(f.Text, `"dirty"`) || f.Truncated || f.MTime == "" {
		t.Errorf("file = %+v", f)
	}
	// The live read shows the working copy, and notes pinned on the file are attached.
	if len(f.Notes) != 1 || f.Notes[0].ID != "N-1" || f.Notes[0].Text != "check this" || f.Notes[0].Line == nil ||
		*f.Notes[0].Line != 2 || f.Notes[0].ProjectID != "alpha" {
		t.Errorf("notes = %+v", f.Notes)
	}

	// A window of lines.
	rec = e.get("/api/projects/alpha/file?path=lines.txt&from=2&to=3")
	pjtWant(t, rec, 200)
	pjtJSON(t, rec, &f)
	if f.Text != "two\nthree" || f.From != 2 || f.To != 3 || f.Lines != 5 || !f.Truncated || len(f.Notes) != 0 {
		t.Errorf("window = %+v", f)
	}
	if !strings.Contains(e.get("/api/projects/alpha/file?path=lines.txt").Body.String(), `"notes":[]`) {
		t.Error("notes must be an empty array, not null")
	}
	// A doc says so.
	rec = e.get("/api/projects/alpha/file?path=docs/guide.md")
	pjtJSON(t, rec, &f)
	if !f.IsDoc || f.Kind != "doc" {
		t.Errorf("doc = %+v", f)
	}
	// Binary: no text.
	rec = e.get("/api/projects/alpha/file?path=data.bin")
	pjtWant(t, rec, 200)
	pjtJSON(t, rec, &f)
	if !f.Binary || f.Text != "" {
		t.Errorf("binary = %+v", f)
	}
}

func TestProjectFileNeverServesSecrets(t *testing.T) {
	e := pjtNew(t, nil)
	for _, p := range []string{".env", "link-to-env.txt"} { // the second is a link to .env inside the project
		rec := e.get("/api/projects/alpha/file?path=" + p)
		pjtWant(t, rec, 200)
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Fatalf("%s leaked the secret: %s", p, rec.Body.String())
		}
		var f struct {
			Redacted bool
			Text     string
		}
		pjtJSON(t, rec, &f)
		if !f.Redacted || f.Text != "" {
			t.Errorf("%s = %+v, want redacted with no text", p, f)
		}
	}
}

func TestProjectFileRefusals(t *testing.T) {
	e := pjtNew(t, nil)
	for name, q := range map[string]string{
		"dotdot":     "path=../../../../etc/passwd",
		"dotdot mid": "path=src/../../outside/secret.txt",
		"absolute":   "path=" + url.QueryEscape(e.outside+"/secret.txt"),
		"etc passwd": "path=/etc/passwd",
		"encoded":    "path=%2e%2e/outside/secret.txt",
		"missing":    "",
		"empty":      "path=",
		"dot":        "path=.",
		"nul":        "path=README.md%00.png",
		"from text":  "path=README.md&from=x",
		"from neg":   "path=README.md&from=-1",
		"to text":    "path=README.md&to=1.5",
	} {
		rec := e.get("/api/projects/alpha/file?" + q)
		if rec.Code != 400 {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "TOP SECRET") || strings.Contains(rec.Body.String(), "root:") {
			t.Errorf("%s leaked a file", name)
		}
	}
	// A link that leaves the project: 403, nothing from outside.
	rec := e.get("/api/projects/alpha/file?path=escape-file.txt")
	pjtWant(t, rec, 403)
	if strings.Contains(rec.Body.String(), "TOP SECRET") {
		t.Error("a link out of the project leaked the file")
	}
	pjtWant(t, e.get("/api/projects/alpha/file?path=escape/secret.txt"), 403)
	pjtWant(t, e.get("/api/projects/alpha/file?path=nope.txt"), 404)
	pjtWant(t, e.get("/api/projects/alpha/file?path=src"), 400) // a folder
}

func TestProjectRaw(t *testing.T) {
	e := pjtNew(t, nil)
	rec := e.get("/api/projects/alpha/raw?path=logo.png")
	pjtWant(t, rec, 200)
	if !bytes.Equal(rec.Body.Bytes(), pjtPNG) {
		t.Error("raw bytes differ from the file")
	}
	h := rec.Header()
	if h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("headers = %v", h)
	}
	if h.Get("Cache-Control") == "" || h.Get("Last-Modified") == "" {
		t.Errorf("caching headers = %v", h)
	}

	// HTML must never run as a page: text/plain, nosniff and sandbox.
	rec = e.get("/api/projects/alpha/raw?path=page.html")
	pjtWant(t, rec, 200)
	h = rec.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "text/plain") || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("html headers = %v", h)
	}

	// A PDF keeps its type and nosniff but has no sandbox, so the browser's viewer can show it.
	pjtWrite(t, filepath.Join(e.alpha, "spec.pdf"), "%PDF-1.4\n%%EOF\n")
	rec = e.get("/api/projects/alpha/raw?path=spec.pdf")
	pjtWant(t, rec, 200)
	h = rec.Header()
	if h.Get("Content-Type") != "application/pdf" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "" {
		t.Errorf("pdf headers = %v", h)
	}

	// A range request works (video and big images).
	rec = e.getH("/api/projects/alpha/raw?path=lines.txt", map[string]string{"Range": "bytes=0-2"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "one" {
		t.Errorf("range: %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Error("range reply lost the sandbox header")
	}

	// Secret files are never served, also through a link.
	for _, p := range []string{".env", "link-to-env.txt"} {
		rec = e.get("/api/projects/alpha/raw?path=" + p)
		pjtWant(t, rec, 403)
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Fatalf("%s leaked the secret", p)
		}
	}
	// Traversal and links out of the project.
	for _, q := range []string{"path=../outside/secret.txt", "path=/etc/passwd", "path=" + url.QueryEscape(e.outside+"/secret.txt"), "path=", "path=."} {
		pjtWant(t, e.get("/api/projects/alpha/raw?"+q), 400)
	}
	rec = e.get("/api/projects/alpha/raw?path=escape-file.txt")
	pjtWant(t, rec, 403)
	if strings.Contains(rec.Body.String(), "TOP SECRET") {
		t.Error("raw leaked a file outside the project")
	}
	pjtWant(t, e.get("/api/projects/alpha/raw?path=nope.png"), 404)
	pjtWant(t, e.get("/api/projects/alpha/raw?path=src"), 400)
}

// TestProjectWiderSecretList (review M-6): .git/config and the other new
// secret files are redacted on /file and refused on /raw, with CORP set.
func TestProjectWiderSecretList(t *testing.T) {
	e := pjtNew(t, nil)
	for _, rel := range []string{".git/config", ".npmrc", "infra/prod.tfvars", ".aws/credentials"} {
		p := filepath.Join(e.alpha, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("url = https://bob:FAKEtoken0000@example.com/x.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rec := e.get("/api/projects/alpha/file?path=" + url.QueryEscape(rel))
		pjtWant(t, rec, 200)
		if strings.Contains(rec.Body.String(), "FAKEtoken0000") {
			t.Fatalf("%s leaked: %s", rel, rec.Body.String())
		}
		var f struct{ Redacted bool }
		pjtJSON(t, rec, &f)
		if !f.Redacted {
			t.Errorf("%s not redacted", rel)
		}
		rec = e.get("/api/projects/alpha/raw?path=" + url.QueryEscape(rel))
		pjtWant(t, rec, 403)
		if rec.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Errorf("%s raw: no CORP header", rel)
		}
	}
}

// TestProjectFilesPathRoute: /files/{path...} sends the same bytes and headers as /raw, with
// the path in the URL (each segment percent-encoded), and has the same refusals.
func TestProjectFilesPathRoute(t *testing.T) {
	e := pjtNew(t, nil)
	pjtWrite(t, filepath.Join(e.alpha, "dir a", "b#c.glb"), "glTF-bytes")
	pjtWrite(t, filepath.Join(e.alpha, "dir a", "b#c.gltf"), `{"asset":{"version":"2.0"}}`)

	rec := e.get("/api/projects/alpha/files/dir%20a/b%23c.glb")
	pjtWant(t, rec, 200)
	if rec.Body.String() != "glTF-bytes" {
		t.Errorf("body = %q", rec.Body.String())
	}
	raw := e.get("/api/projects/alpha/raw?path=" + url.QueryEscape("dir a/b#c.glb"))
	pjtWant(t, raw, 200)
	for _, k := range []string{"Content-Type", "X-Content-Type-Options", "Content-Security-Policy", "Cache-Control", "Last-Modified"} {
		if rec.Header().Get(k) != raw.Header().Get(k) {
			t.Errorf("%s: files %q, raw %q", k, rec.Header().Get(k), raw.Header().Get(k))
		}
	}

	// Same bytes as /raw for an image, and a range request still works.
	rec = e.get("/api/projects/alpha/files/logo.png")
	pjtWant(t, rec, 200)
	if !bytes.Equal(rec.Body.Bytes(), pjtPNG) || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("png: %q %d bytes", rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	rec = e.getH("/api/projects/alpha/files/lines.txt", map[string]string{"Range": "bytes=0-2"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "one" {
		t.Errorf("range: %d %q", rec.Code, rec.Body.String())
	}

	// Refusals: traversal is 400, a secret file and a link out are 403, missing is 404, a folder is 400.
	// A literal ".." never reaches the handler: the mux cleans the path and answers a redirect
	// to the cleaned URL (which cannot leave /api/projects/alpha/). An encoded ".." reaches
	// pjCleanRel and is a 400.
	for _, p := range []string{"../x", "src/../../x"} {
		rec = e.get("/api/projects/alpha/files/" + p)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusTemporaryRedirect {
			t.Errorf("files/%s: status = %d, want 400 or the mux redirect (%s)", p, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "TOP SECRET") {
			t.Errorf("files/%s leaked", p)
		}
	}
	for _, p := range []string{"%2e%2e/x", "src/%2e%2e/%2e%2e/x", "..%2fx", "src%2f..%2f..%2fx"} {
		rec = e.get("/api/projects/alpha/files/" + p)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("files/%s: status = %d, want 400 (%s)", p, rec.Code, rec.Body.String())
		}
	}
	for _, p := range []string{".env", "link-to-env.txt", "escape-file.txt"} {
		rec = e.get("/api/projects/alpha/files/" + p)
		pjtWant(t, rec, 403)
		if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "TOP SECRET") {
			t.Fatalf("%s leaked", p)
		}
	}
	pjtWant(t, e.get("/api/projects/alpha/files/nope.glb"), 404)
	pjtWant(t, e.get("/api/projects/alpha/files/src"), 400)
	pjtWant(t, e.get("/api/projects/nosuch/files/logo.png"), 404)
}

// TestProjectDwgRoutesRegistered: the two drawing routes exist and, with no reader set up,
// answer 503 "unavailable" (not the SPA page and not a 404).
func TestProjectDwgRoutesRegistered(t *testing.T) {
	e := pjtNew(t, nil)
	for _, p := range []string{"layers", "entities"} {
		rec := e.get("/api/projects/alpha/dwg/" + p + "?path=a.dwg")
		pjtWant(t, rec, 503)
		if !strings.Contains(rec.Body.String(), `"unavailable"`) {
			t.Errorf("dwg/%s body = %s", p, rec.Body.String())
		}
	}
}
