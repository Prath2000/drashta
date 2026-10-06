package httpapi

import (
	"bytes"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// upload posts a multipart snapshot with one image part.
func (f *trkFix) upload(task, filename string, data []byte, fields map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if filename != "" {
		part, err := mw.CreateFormFile("image", filename)
		if err != nil {
			f.t.Fatal(err)
		}
		_, _ = part.Write(data)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/projects/alpha/tasks/"+task+"/snapshots", &buf)
	req.Host = "localhost:4455"
	req.Header.Set(TokenHeader, hex.EncodeToString([]byte("tok")))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	return w
}

func TestTrkSnapshotUploadAndRuleA(t *testing.T) {
	f := newTrkFix(t)
	head := f.srv.Hub().Head()
	w := f.upload("T-1", "shot.PNG", []byte(trkPNG), map[string]string{"title": "First pass", "note": "Banner only."})
	m := trkJSON(t, w, http.StatusCreated)
	s := m["snapshot"].(map[string]any)
	if s["version"] != 1.0 || s["verdict"] != "pending" || s["title"] != "First pass" || s["source"] != "upload" ||
		s["url"] != "/api/projects/alpha/raw?path=.drashta%2Fsnapshots%2FT-1%2Fv1.png" || s["author"] != "alice" {
		t.Fatalf("snapshot: %v", s)
	}
	if b, err := os.ReadFile(filepath.Join(f.root, ".drashta/snapshots/T-1/v1.png")); err != nil || string(b) != trkPNG {
		t.Fatalf("image not stored: %v", err)
	}
	// Rule (a): in_progress -> review, with a log line.
	text := trkRead(t, f.taskFile("T-1"))
	if !strings.Contains(text, "status: review") || !strings.Contains(text, "posted snapshot v1; status in_progress -> review") {
		t.Fatalf("rule (a) not applied:\n%s", text)
	}
	kinds := map[string]bool{}
	for _, e := range f.trackerEvents(head) {
		kinds[e.Kind+":"+e.ID] = true
	}
	if !kinds["snapshot:T-1/v1"] || !kinds["task:T-1"] {
		t.Fatalf("events: %v", kinds)
	}
	if !f.reindex.has("alpha:.drashta/snapshots/T-1/v1.md") {
		t.Fatalf("snapshot not reindexed: %v", f.reindex.paths)
	}

	// Second version on a task that is now in review: status stays, log line added.
	trkJSON(t, f.upload("T-1", "b.webp", []byte("webp"), nil), 201)
	text = trkRead(t, f.taskFile("T-1"))
	if !strings.Contains(text, "status: review") || !strings.Contains(text, "posted snapshot v2\n") {
		t.Fatalf("second version:\n%s", text)
	}
	// Rule (a) does not touch a todo task's status.
	trkJSON(t, f.upload("T-2", "c.jpg", []byte("jpg"), nil), 201)
	if !strings.Contains(trkRead(t, f.taskFile("T-2")), "status: todo") {
		t.Fatal("todo task moved")
	}

	m = trkJSON(t, f.do("GET", "/api/projects/alpha/tasks/T-1/snapshots", nil), 200)
	if ss := m["snapshots"].([]any); len(ss) != 2 || ss[1].(map[string]any)["latest"] != true {
		t.Fatalf("list: %v", m)
	}
	m = trkJSON(t, f.do("GET", "/api/tracker?project=alpha&filter=review", nil), 200)
	if len(m["tasks"].([]any)) != 2 {
		t.Fatalf("review filter: %v", m["tasks"])
	}
}

func TestTrkSnapshotUploadLimits(t *testing.T) {
	f := newTrkFix(t)
	for _, name := range []string{"x.svg", "x.html", "x.png.exe", "x"} {
		w := f.upload("T-1", name, []byte("data"), nil)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// 20 MB is fine, one byte more is refused.
	big := bytes.Repeat([]byte{'a'}, 20<<20)
	trkJSON(t, f.upload("T-2", "ok.png", big, nil), 201)
	trkCode(t, f.upload("T-2", "big.png", append(big, 'b'), nil), http.StatusRequestEntityTooLarge, "payload_too_large")
	// A body far over the cap is refused by the guard.
	huge := bytes.Repeat([]byte{'a'}, 22<<20)
	trkCode(t, f.upload("T-2", "huge.png", huge, nil), http.StatusRequestEntityTooLarge, "payload_too_large")
	entries, _ := os.ReadDir(filepath.Join(f.root, ".drashta/snapshots/T-2"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "v2") || strings.HasPrefix(e.Name(), ".tmp") {
			t.Fatalf("left over after a refused upload: %s", e.Name())
		}
	}
	trkCode(t, f.upload("T-1", "", nil, map[string]string{"title": "no image"}), 400, "")
	trkCode(t, f.upload("T-9", "a.png", []byte("x"), nil), 404, "")
}

func TestTrkSnapshotJSONSources(t *testing.T) {
	f := newTrkFix(t)
	base := "/api/projects/alpha/tasks/T-2/snapshots"
	trkCode(t, f.do("POST", base, map[string]any{"url": "http://localhost:5173/"}), http.StatusNotImplemented, "not_implemented")
	w := f.do("POST", base, map[string]any{"url": "http://localhost:5173/"})
	if !strings.Contains(w.Body.String(), "URL snapshots arrive later (Safari capture, decision A3)") {
		t.Fatalf("501 message: %s", w.Body.String())
	}

	m := trkJSON(t, f.do("POST", base, map[string]any{"path": "shots/a.png", "title": "From disk"}), 201)
	if trkGet(m, "snapshot", "source") != "file:shots/a.png" || trkGet(m, "snapshot", "version") != 1.0 {
		t.Fatalf("path source: %v", m)
	}
	for _, bad := range []string{"../outside/secret.png", "/etc/hosts", "shots/../../outside/secret.png", "~/x.png",
		"shots/escape.png", "src"} {
		w := f.do("POST", base, map[string]any{"path": bad})
		if w.Code != 400 && w.Code != 404 {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body.String())
		}
	}
	trkCode(t, f.do("POST", base, map[string]any{"path": "src/x.go"}), http.StatusUnsupportedMediaType, "")
	trkCode(t, f.do("POST", base, map[string]any{"path": "shots/missing.png"}), 404, "")
	trkCode(t, f.do("POST", base, map[string]any{}), 400, "")
	trkCode(t, f.do("POST", base, map[string]any{"path": "shots/a.png", "url": "http://localhost:1/"}), 400, "")
}

func TestTrkVerdictRules(t *testing.T) {
	f := newTrkFix(t)
	trkJSON(t, f.upload("T-1", "a.png", []byte("1"), nil), 201) // T-1: in_progress -> review
	trkJSON(t, f.upload("T-1", "b.png", []byte("2"), nil), 201)
	verdict := func(v string, body map[string]any) *httptest.ResponseRecorder {
		return f.do("PUT", "/api/projects/alpha/tasks/T-1/snapshots/"+v+"/verdict", body)
	}

	// Rule (b) only on the latest version: changes on v1 does not move the task.
	m := trkJSON(t, verdict("1", map[string]any{"verdict": "changes", "note": "old one"}), 200)
	if trkGet(m, "task", "status") != "review" || trkGet(m, "snapshot", "verdict") != "changes" || m["suggestion"] != nil {
		t.Fatalf("changes on old version: %v", m)
	}
	// Rule (b): changes on the latest moves review -> in_progress, with a log line.
	m = trkJSON(t, verdict("v2", map[string]any{"verdict": "changes", "note": "too bright"}), 200)
	if trkGet(m, "task", "status") != "in_progress" || trkGet(m, "snapshot", "reviewNote") != "too bright" ||
		trkGet(m, "snapshot", "reviewedBy") != "alice" {
		t.Fatalf("rule (b): %v", m)
	}
	if !strings.Contains(trkRead(t, f.taskFile("T-1")), "changes requested on snapshot v2; status review -> in_progress") {
		t.Fatal("no log line for rule (b)")
	}

	// Rule (c): approved on the latest only suggests done; the task file does not change.
	before := trkRead(t, f.taskFile("T-1"))
	m = trkJSON(t, verdict("2", map[string]any{"verdict": "approved"}), 200)
	if trkGet(m, "suggestion", "status") != "done" || trkGet(m, "task", "status") != "in_progress" {
		t.Fatalf("rule (c): %v", m)
	}
	if trkRead(t, f.taskFile("T-1")) != before {
		t.Fatal("approved changed the task file")
	}
	// Approved on an older version: no suggestion.
	m = trkJSON(t, verdict("1", map[string]any{"verdict": "approved"}), 200)
	if m["suggestion"] != nil {
		t.Fatalf("suggestion for an old version: %v", m)
	}
	// Pending resets the review fields.
	m = trkJSON(t, verdict("2", map[string]any{"verdict": "pending"}), 200)
	if trkGet(m, "snapshot", "verdict") != "pending" || trkGet(m, "snapshot", "reviewNote") != "" {
		t.Fatalf("pending: %v", m)
	}
	trkCode(t, verdict("2", map[string]any{"verdict": "maybe"}), 422, "")
	trkCode(t, verdict("9", map[string]any{"verdict": "approved"}), 404, "")
	trkCode(t, verdict("x", map[string]any{"verdict": "approved"}), 400, "")
}

func TestTrkInboxFile(t *testing.T) {
	f := newTrkFix(t)
	inbox := filepath.Join(f.root, ".drashta/snapshots/inbox")
	trkWrite(t, filepath.Join(inbox, "drop one.png"), trkPNG)
	trkWrite(t, filepath.Join(inbox, "notes.txt"), "x")
	m := trkJSON(t, f.do("POST", "/api/projects/alpha/snapshots/inbox/drop%20one.png/file", map[string]any{"taskId": "T-1"}), 201)
	s := m["snapshot"].(map[string]any)
	if s["version"] != 1.0 || s["taskId"] != "T-1" || s["verdict"] != "pending" {
		t.Fatalf("filed snapshot: %v", m)
	}
	if _, err := os.Stat(filepath.Join(inbox, "drop one.png")); !os.IsNotExist(err) {
		t.Fatal("drop not removed")
	}
	if b, err := os.ReadFile(filepath.Join(f.root, ".drashta/snapshots/T-1/v1.png")); err != nil || string(b) != trkPNG {
		t.Fatal("drop bytes not stored")
	}
	if !strings.Contains(trkRead(t, f.taskFile("T-1")), "status: review") {
		t.Fatal("rule (a) not applied to a filed drop")
	}

	trkWrite(t, filepath.Join(inbox, "b.png"), trkPNG)
	trkCode(t, f.do("POST", "/api/projects/alpha/snapshots/inbox/..%2Fsnapshots%2Fb.png/file", map[string]any{"taskId": "T-1"}), 400, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/snapshots/inbox/.hidden.png/file", map[string]any{"taskId": "T-1"}), 400, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/snapshots/inbox/notes.txt/file", map[string]any{"taskId": "T-1"}), 415, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/snapshots/inbox/missing.png/file", map[string]any{"taskId": "T-1"}), 404, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/snapshots/inbox/b.png/file", map[string]any{"taskId": "../T-1"}), 422, "")
	trkCode(t, f.do("POST", "/api/projects/alpha/snapshots/inbox/b.png/file", map[string]any{"taskId": "T-77"}), 404, "")
	// A link in the inbox that points outside is refused.
	if err := os.Symlink(filepath.Join(f.outside, "secret.png"), filepath.Join(inbox, "link.png")); err != nil {
		t.Fatal(err)
	}
	trkCode(t, f.do("POST", "/api/projects/alpha/snapshots/inbox/link.png/file", map[string]any{"taskId": "T-1"}), 400, "")
	if _, err := os.Stat(filepath.Join(f.outside, "secret.png")); err != nil {
		t.Fatal("the outside file was touched")
	}
}
