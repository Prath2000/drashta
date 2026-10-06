package httpapi

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHomeRefreshAll(t *testing.T) {
	e := ahNew(t, nil)
	for _, body := range []string{`{}`, `{"projectId":""}`} {
		var got struct {
			Started bool `json:"started"`
		}
		ahDecode(t, e.do("POST", "/api/refresh", body, true), 200, &got)
		if !got.Started {
			t.Errorf("%s: started = false", body)
		}
		if c := e.idx.wait(t); c != "all" {
			t.Errorf("%s: indexer call = %q, want all", body, c)
		}
	}
}

func TestHomeRefreshEmptyBodyMeansAll(t *testing.T) {
	e := ahNew(t, nil)
	rec := e.do("POST", "/api/refresh", "", true)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if c := e.idx.wait(t); c != "all" {
		t.Errorf("indexer call = %q", c)
	}
}

func TestHomeRefreshOneProject(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	ahDecode(t, e.do("POST", "/api/refresh", `{"projectId":"alpha"}`, true), 200, nil)
	if c := e.idx.wait(t); c != "project:alpha:gh=true" {
		t.Errorf("a project refresh also refreshes GitHub: %q", c)
	}
	ahDecode(t, e.do("POST", "/api/refresh", `{"projectId":"alpha","withGH":false}`, true), 200, nil)
	if c := e.idx.wait(t); c != "project:alpha:gh=false" {
		t.Errorf("withGH:false: %q", c)
	}
	ahDecode(t, e.do("POST", "/api/refresh", `{"withGH":true}`, true), 200, nil)
	if c := e.idx.wait(t); c != "all" {
		t.Errorf("no project: %q", c)
	}
}

func TestHomeRefreshUnknownAndBadInput(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	ahErrCode(t, e.do("POST", "/api/refresh", `{"projectId":"nobody"}`, true), 404, "not_found")
	for name, body := range map[string]string{
		"not json":     `nope`,
		"wrong type":   `{"projectId":5}`,
		"long id":      `{"projectId":"` + strings.Repeat("x", 300) + `"}`,
		"control char": "{\"projectId\":\"a\\nb\"}",
		"two docs":     `{} {}`,
	} {
		if rec := e.do("POST", "/api/refresh", body, true); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	e.idx.noCall(t)
}

func TestHomeRefreshNeedsToken(t *testing.T) {
	e := ahNew(t, nil)
	if rec := e.do("POST", "/api/refresh", `{}`, false); rec.Code != 403 {
		t.Errorf("no token gave %d", rec.Code)
	}
	req := newReq("POST", "/api/refresh", `{}`)
	req.Header.Set(TokenHeader, "0000")
	req.Header.Set("Content-Type", "application/json")
	if rec := serve(e, req); rec.Code != 403 {
		t.Errorf("wrong token gave %d", rec.Code)
	}
	req = newReq("POST", "/api/refresh", `{}`)
	req.Header.Set(TokenHeader, ahTestToken)
	req.Header.Set("Content-Type", "text/plain")
	if rec := serve(e, req); rec.Code != 415 {
		t.Errorf("text/plain gave %d", rec.Code)
	}
	if rec := e.get("/api/refresh"); rec.Code != 405 {
		t.Errorf("GET gave %d", rec.Code)
	}
	e.idx.noCall(t)
}

func TestHomeRefreshReturnsAtOnceAndDoesNotPile(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	release := make(chan struct{})
	e.idx.mu.Lock()
	e.idx.block = release
	e.idx.mu.Unlock()

	done := make(chan struct{})
	go func() {
		ahDecode(t, e.do("POST", "/api/refresh", `{}`, true), 200, nil)
		close(done)
	}()
	select {
	case <-done: // the handler answered although the indexer is still busy
	case <-time.After(2 * time.Second):
		t.Fatal("the request waited for the indexer")
	}
	if c := e.idx.wait(t); c != "all" {
		t.Fatalf("call = %q", c)
	}
	// a second "all" while the first runs starts nothing new, and still says started
	var got struct {
		Started bool `json:"started"`
	}
	ahDecode(t, e.do("POST", "/api/refresh", `{}`, true), 200, &got)
	if !got.Started {
		t.Error("started = false")
	}
	e.idx.noCall(t)
	// another scope is independent
	ahDecode(t, e.do("POST", "/api/refresh", `{"projectId":"alpha"}`, true), 200, nil)
	if c := e.idx.wait(t); c != "project:alpha:gh=true" {
		t.Errorf("call = %q", c)
	}
	// after the first one ends a new "all" starts again
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.idx.mu.Lock()
		e.idx.block = nil
		e.idx.mu.Unlock()
		ahDecode(t, e.do("POST", "/api/refresh", `{}`, true), 200, nil)
		select {
		case c := <-e.idx.called:
			if c == "all" {
				if n := len(e.idx.snapshot()); n < 3 {
					t.Errorf("calls = %v", e.idx.snapshot())
				}
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slot was never freed; calls = %v", e.idx.snapshot())
		}
	}
}

func TestHomeRefreshSurvivesIndexerPanic(t *testing.T) {
	e := ahNew(t, nil)
	e.project("alpha", "g", "git", "main", false, true, "")
	e.idx.panicOn = "alpha"
	ahDecode(t, e.do("POST", "/api/refresh", `{"projectId":"alpha"}`, true), 200, nil)
	if c := e.idx.wait(t); c != "project:alpha:gh=true" {
		t.Fatalf("call = %q", c)
	}
	// the slot is freed again after the panic
	deadline := time.Now().Add(2 * time.Second)
	for {
		ahDecode(t, e.do("POST", "/api/refresh", `{"projectId":"alpha"}`, true), 200, nil)
		select {
		case <-e.idx.called:
			if reflect.DeepEqual(e.idx.snapshot()[0], e.idx.snapshot()[1]) {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the slot stayed taken after a panic")
		}
	}
}

func TestHomeRefreshWithoutIndexer(t *testing.T) {
	e := ahNew(t, func(d *HomeDeps) { d.Indexer = nil })
	ahErrCode(t, e.do("POST", "/api/refresh", `{}`, true), 503, "unavailable")
}

func TestHomeRegisterNeedsStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("RegisterHome without a store must panic")
		}
	}()
	RegisterHome(New(Options{}), HomeDeps{})
}
