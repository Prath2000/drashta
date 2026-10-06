package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/live"
)

// coreFrame is one parsed SSE block.
type coreFrame struct {
	ID      string
	HasID   bool
	Event   string
	Data    string
	Comment string
	Retry   string
}

// coreSSE is a test client for GET /api/events.
type coreSSE struct {
	t      *testing.T
	resp   *http.Response
	frames chan coreFrame
	cancel context.CancelFunc
}

const coreWait = 5 * time.Second

func coreOpenSSE(t *testing.T, addr string, hdr map[string]string) *coreSSE {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	c := &coreSSE{t: t, resp: resp, frames: make(chan coreFrame, 256), cancel: cancel}
	t.Cleanup(c.close)
	go c.read()
	return c
}

func (c *coreSSE) read() {
	defer close(c.frames)
	sc := bufio.NewScanner(c.resp.Body)
	var f coreFrame
	var data []string
	flush := func() {
		if f.HasID || f.Event != "" || len(data) > 0 || f.Comment != "" || f.Retry != "" {
			f.Data = strings.Join(data, "\n")
			c.frames <- f
		}
		f, data = coreFrame{}, nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, ":"):
			f.Comment = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "id:"):
			f.ID, f.HasID = strings.TrimSpace(line[3:]), true
		case strings.HasPrefix(line, "event:"):
			f.Event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[5:], " "))
		case strings.HasPrefix(line, "retry:"):
			f.Retry = strings.TrimSpace(line[6:])
		}
	}
}

// next returns the next frame of any kind.
func (c *coreSSE) next() coreFrame {
	c.t.Helper()
	select {
	case f, ok := <-c.frames:
		if !ok {
			c.t.Fatal("stream ended")
		}
		return f
	case <-time.After(coreWait):
		c.t.Fatal("timed out waiting for an SSE frame")
	}
	return coreFrame{}
}

// nextEvent skips the retry line and comments and returns the next real event.
func (c *coreSSE) nextEvent() coreFrame {
	c.t.Helper()
	for {
		f := c.next()
		if f.Event != "" {
			return f
		}
	}
}

// expectNothing fails if a frame arrives within a short time.
func (c *coreSSE) expectNothing() {
	c.t.Helper()
	select {
	case f, ok := <-c.frames:
		if ok {
			c.t.Fatalf("unexpected frame %+v", f)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

// expectClosed waits for the server to end the stream.
func (c *coreSSE) expectClosed() {
	c.t.Helper()
	deadline := time.After(coreWait)
	for {
		select {
		case _, ok := <-c.frames:
			if !ok {
				return
			}
		case <-deadline:
			c.t.Fatal("stream did not end")
		}
	}
}

func (c *coreSSE) close() { c.cancel(); _ = c.resp.Body.Close() }

func (c *coreSSE) hello() live.Hello {
	c.t.Helper()
	f := c.nextEvent()
	if f.Event != "hello" {
		c.t.Fatalf("first event = %q, want hello", f.Event)
	}
	var h live.Hello
	if err := json.Unmarshal([]byte(f.Data), &h); err != nil {
		c.t.Fatalf("hello data %q: %v", f.Data, err)
	}
	return h
}

func coreWaitConns(t *testing.T, hub *live.Hub, want int) {
	t.Helper()
	deadline := time.After(coreWait)
	for hub.Conns() != want {
		select {
		case <-hub.Changed():
		case <-deadline:
			t.Fatalf("connections = %d, want %d", hub.Conns(), want)
		}
	}
}

func coreHub(opts live.HubOptions) *live.Hub {
	if opts.StartSeq == 0 {
		opts.StartSeq = 100
	}
	return live.NewHub(opts)
}

func TestSSEHelloComesFirst(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s, addr := coreStart(t, func(o *Options) {
		o.Hub = hub
		o.Now = func() time.Time { return now }
		o.Counts = func() live.Counts { return live.Counts{LiveServers: 2, OpenTasks: 5, AwaitingReview: 1, OpenNotes: 3} }
	})
	_ = s
	hub.Publish(live.ActivityEvent("g/a", "2026-10-01")) // id 101, before we connect
	c := coreOpenSSE(t, addr, nil)

	if ct := c.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := c.resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if c.resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("missing X-Accel-Buffering: no")
	}
	for k := range c.resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("CORS header %s on the stream", k)
		}
	}

	f := c.next()
	if f.Retry != "3000" {
		t.Errorf("first frame = %+v, want the retry line", f)
	}
	f = c.next()
	if f.Event != "hello" {
		t.Fatalf("second frame = %+v, want hello", f)
	}
	// A first connection continues from the head: id is the newest event id.
	if !f.HasID || f.ID != "101" {
		t.Errorf("hello id = %q (has %v), want 101", f.ID, f.HasID)
	}
	var h live.Hello
	if err := json.Unmarshal([]byte(f.Data), &h); err != nil {
		t.Fatal(err)
	}
	if !h.Now.Equal(now) || h.Counts != (live.Counts{LiveServers: 2, OpenTasks: 5, AwaitingReview: 1, OpenNotes: 3}) || h.Conn == "" {
		t.Errorf("hello = %+v", h)
	}
	// No replay for a fresh page: the old event 101 is not sent again.
	c.expectNothing()
}

func TestSSEHelloCountsDefaultToZero(t *testing.T) {
	_, addr := coreStart(t, nil)
	c := coreOpenSSE(t, addr, nil)
	f := c.nextEvent()
	if !strings.Contains(f.Data, `"counts":{"liveServers":0,"openTasks":0,"awaitingReview":0,"openNotes":0}`) {
		t.Errorf("hello data = %s", f.Data)
	}
}

func TestSSEDeliversLiveEventsWithIDs(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	c := coreOpenSSE(t, addr, nil)
	c.hello()
	hub.Publish(live.TrackerEvent("g/a", "task", "T-1"))
	hub.Publish(live.ServersEvent("", []int{3000, 5173}))
	f := c.nextEvent()
	if f.Event != "tracker" || f.ID != "101" || f.Data != `{"projectId":"g/a","kind":"task","id":"T-1"}` {
		t.Errorf("frame = %+v", f)
	}
	f = c.nextEvent()
	if f.Event != "servers" || f.ID != "102" || f.Data != `{"active":[3000,5173]}` {
		t.Errorf("frame = %+v", f)
	}
}

func TestSSEReplayWithLastEventID(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	for i := 0; i < 3; i++ {
		hub.Publish(live.ActivityEvent("g/a", "2026-10-0"+strconv.Itoa(i+1))) // ids 101..103
	}
	c := coreOpenSSE(t, addr, map[string]string{"Last-Event-ID": "101"})
	hf := c.nextEvent()
	if hf.Event != "hello" {
		t.Fatalf("first = %+v", hf)
	}
	// While replaying, hello must not move the page's cursor.
	if hf.HasID {
		t.Errorf("hello has id %q during a replay", hf.ID)
	}
	a, b := c.nextEvent(), c.nextEvent()
	if a.ID != "102" || b.ID != "103" || a.Event != "activity" || !strings.Contains(a.Data, "2026-10-02") || !strings.Contains(b.Data, "2026-10-03") {
		t.Errorf("replay = %+v, %+v", a, b)
	}
	hub.Publish(live.ActivityEvent("g/a", "live"))
	if f := c.nextEvent(); f.ID != "104" || !strings.Contains(f.Data, "live") {
		t.Errorf("live after replay = %+v", f)
	}
}

func TestSSELastEventIDQueryParameter(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	hub.Publish(live.ActivityEvent("g/a", "d1"))
	hub.Publish(live.ActivityEvent("g/a", "d2"))
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/events?lastEventId=101", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	var seen []string
	for len(seen) < 8 {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, line)
		if strings.HasPrefix(line, "id: 102") {
			return
		}
	}
	t.Fatalf("event 102 not replayed: %q", seen)
}

func TestSSEResyncWhenIDFellOutOfTheRing(t *testing.T) {
	hub := coreHub(live.HubOptions{RingSize: 3})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	for i := 0; i < 10; i++ {
		hub.Publish(live.ActivityEvent("g/a", "d")) // 101..110, ring keeps 108..110
	}
	cases := map[string]string{
		"fell out":         "101",
		"from the future":  "999999999",
		"not a number":     "abc",
		"one before ring":  "106",
		"negative number":  "-5",
		"number with junk": "12x",
	}
	for name, id := range cases {
		c := coreOpenSSE(t, addr, map[string]string{"Last-Event-ID": id})
		hf := c.nextEvent()
		if hf.Event != "hello" || !hf.HasID || hf.ID != "110" {
			t.Errorf("%s: hello = %+v", name, hf)
		}
		rf := c.nextEvent()
		if rf.Event != "resync" || rf.Data != "{}" || rf.ID != "110" {
			t.Errorf("%s: resync frame = %+v", name, rf)
		}
		c.expectNothing() // no replay on top of a resync
		c.close()
	}
	// 107 is the oldest id we can still serve from: the next needed event (108) is in the ring.
	c := coreOpenSSE(t, addr, map[string]string{"Last-Event-ID": "107"})
	c.hello()
	if f := c.nextEvent(); f.ID != "108" || f.Event != "activity" {
		t.Errorf("replay from 107 started at %+v", f)
	}
}

func TestSSEUpToDatePageGetsNothingExtra(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	hub.Publish(live.ActivityEvent("g/a", "d"))
	c := coreOpenSSE(t, addr, map[string]string{"Last-Event-ID": "101"})
	c.hello()
	c.expectNothing()
}

func TestSSEPingEvery25SecondsUsesTheTicker(t *testing.T) {
	tick := make(chan time.Time)
	asked := make(chan time.Duration, 4)
	stopped := make(chan struct{}, 4)
	_, addr := coreStart(t, func(o *Options) {
		o.NewTicker = func(d time.Duration) (<-chan time.Time, func()) {
			asked <- d
			return tick, func() { stopped <- struct{}{} }
		}
	})
	c := coreOpenSSE(t, addr, nil)
	c.hello()
	if d := <-asked; d != 25*time.Second {
		t.Errorf("ping interval = %v", d)
	}
	c.expectNothing() // nothing is sent until the ticker fires
	tick <- time.Now()
	f := c.next()
	if f.Comment != "ping" || f.Event != "" {
		t.Errorf("frame = %+v, want a ': ping' comment", f)
	}
	tick <- time.Now()
	if f := c.next(); f.Comment != "ping" {
		t.Errorf("second frame = %+v", f)
	}
	c.close()
	select {
	case <-stopped:
	case <-time.After(coreWait):
		t.Fatal("ticker was not stopped when the stream ended")
	}
}

func TestSSECoalescesProjectEvents(t *testing.T) {
	clk := &coreClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	hub := coreHub(live.HubOptions{Now: clk.Now})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	c := coreOpenSSE(t, addr, nil)
	c.hello()
	for i := 0; i < 20; i++ {
		hub.Publish(live.ProjectEvent("g/a", "files"))
		clk.Advance(10 * time.Millisecond)
	}
	hub.Publish(live.ProjectEvent("g/a", "commits"))
	f := c.nextEvent()
	if f.Event != "project" || f.Data != `{"projectId":"g/a","what":"files"}` {
		t.Fatalf("first = %+v", f)
	}
	c.expectNothing() // the other 20 are held
	clk.Advance(2 * time.Second)
	if n := hub.Flush(); n != 1 {
		t.Fatalf("flush emitted %d", n)
	}
	f = c.nextEvent()
	if f.Event != "project" || f.Data != `{"projectId":"g/a","what":"all","parts":["files","commits"]}` {
		t.Fatalf("merged = %+v", f)
	}
}

type coreClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *coreClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *coreClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestSSEPresenceFlagFollowsTheConnection(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	s, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	if hub.AnyVisible() {
		t.Fatal("no streams yet")
	}
	c := coreOpenSSE(t, addr, nil)
	h := c.hello()
	if !hub.AnyVisible() || hub.Conns() != 1 {
		t.Fatal("an open stream counts as visible")
	}

	post := func(body string) int {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/api/presence", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(TokenHeader, s.Token())
		resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(`{"visible":false,"conn":"` + h.Conn + `"}`); code != 200 {
		t.Fatalf("presence status = %d", code)
	}
	if hub.AnyVisible() {
		t.Fatal("page said hidden")
	}
	if code := post(`{"visible":true,"conn":"` + h.Conn + `"}`); code != 200 || !hub.AnyVisible() {
		t.Fatalf("page said visible: status %d, any=%v", code, hub.AnyVisible())
	}
	// Hidden, then the connection drops: the flag goes away with it.
	post(`{"visible":false,"conn":"` + h.Conn + `"}`)
	c.close()
	coreWaitConns(t, hub, 0)
	if hub.AnyVisible() {
		t.Fatal("flag survived the disconnect")
	}
	// The old connection id is now unknown.
	if code := post(`{"visible":true,"conn":"` + h.Conn + `"}`); code != http.StatusNotFound {
		t.Errorf("presence for a dropped connection status = %d", code)
	}
}

func TestSSEVisibleFlagDroppedOnDisconnectWhileVisible(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	a := coreOpenSSE(t, addr, nil)
	b := coreOpenSSE(t, addr, nil)
	a.hello()
	b.hello()
	coreWaitConns(t, hub, 2)
	a.close()
	coreWaitConns(t, hub, 1)
	if !hub.AnyVisible() {
		t.Fatal("b is still open and visible")
	}
	b.close()
	coreWaitConns(t, hub, 0)
	if hub.AnyVisible() {
		t.Fatal("nobody is connected")
	}
}

func TestSSEGuardApplies(t *testing.T) {
	_, addr := coreStart(t, nil)
	for name, hdr := range map[string]map[string]string{
		"foreign host":   {"Host": "evil.example:4455"},
		"foreign origin": {"Origin": "http://evil.example"},
		"null origin":    {"Origin": "null"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), coreWait)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/events", nil)
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
			} else {
				req.Header.Set(k, v)
			}
		}
		resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
		if err != nil {
			cancel()
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d", name, resp.StatusCode)
		}
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			t.Errorf("%s: answered with an event stream", name)
		}
		if !strings.Contains(string(body), `"code":"forbidden"`) {
			t.Errorf("%s: body = %s", name, body)
		}
	}
}

func TestSSEShutdownEndsOpenStreams(t *testing.T) {
	s, addr := coreStart(t, nil)
	c := coreOpenSSE(t, addr, nil)
	c.hello()
	ctx, cancel := context.WithTimeout(context.Background(), coreWait)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(ctx) }()
	c.expectClosed()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(coreWait):
		t.Fatal("Shutdown did not return")
	}
}

func TestSSEHubCloseEndsStream(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	c := coreOpenSSE(t, addr, nil)
	c.hello()
	hub.Close()
	c.expectClosed()
}

func TestSSEEventDataWithNewlinesIsSplitAcrossDataLines(t *testing.T) {
	hub := coreHub(live.HubOptions{})
	_, addr := coreStart(t, func(o *Options) { o.Hub = hub })
	c := coreOpenSSE(t, addr, nil)
	c.hello()
	hub.Publish(live.Event{Type: "custom", Data: json.RawMessage("{\n\"a\": 1\n}")})
	hub.Publish(live.ActivityEvent("g/a", "after"))
	f := c.nextEvent()
	if f.Event != "custom" || f.Data != "{\n\"a\": 1\n}" {
		t.Errorf("frame = %+v", f)
	}
	// The frame after it is not corrupted.
	if f := c.nextEvent(); f.Event != "activity" {
		t.Errorf("next frame = %+v", f)
	}
}
