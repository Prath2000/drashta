package live

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a clock the test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestHub(t *testing.T, o HubOptions) (*Hub, *fakeClock) {
	t.Helper()
	clk := newClock()
	o.Now = clk.Now
	if o.StartSeq == 0 {
		o.StartSeq = 100
	}
	return NewHub(o), clk
}

func decodeProject(t *testing.T, e Event) ProjectData {
	t.Helper()
	if e.Type != TypeProject {
		t.Fatalf("type = %q, want project", e.Type)
	}
	var pd ProjectData
	if err := json.Unmarshal(e.Data, &pd); err != nil {
		t.Fatal(err)
	}
	return pd
}

// drain reads every event that is already waiting in ch, without blocking.
func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestPublishNumbersEventsInOrder(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()
	a := h.Publish(ActivityEvent("p/a", "2026-10-01"))
	b := h.Publish(TrackerEvent("p/a", "task", "T-1"))
	if a != 101 || b != 102 {
		t.Fatalf("ids = %d, %d", a, b)
	}
	got := drain(c.Events)
	if len(got) != 2 || got[0].ID != 101 || got[1].ID != 102 || got[0].Type != TypeActivity || got[1].Type != TypeTracker {
		t.Fatalf("got %+v", got)
	}
	if h.Head() != 102 {
		t.Fatalf("head = %d", h.Head())
	}
}

func TestDefaultStartSeqIsTimeBased(t *testing.T) {
	clk := newClock()
	h := NewHub(HubOptions{Now: clk.Now})
	if got, want := h.Head(), uint64(clk.Now().UnixMicro()); got != want {
		t.Fatalf("head = %d, want %d", got, want)
	}
	// A later hub (a restart) always starts above an id the first one could issue soon after.
	clk.Advance(time.Second)
	h2 := NewHub(HubOptions{Now: clk.Now})
	if h2.Head() <= h.Head()+1000 {
		t.Fatalf("restart head %d not above %d", h2.Head(), h.Head())
	}
}

func TestEmptyDataBecomesObject(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()
	h.Publish(Event{Type: "custom"})
	got := drain(c.Events)
	if len(got) != 1 || string(got[0].Data) != "{}" {
		t.Fatalf("got %+v", got)
	}
}

func TestRingKeepsLastN(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{}) // default ring 1000
	for i := 0; i < 1005; i++ {
		h.Publish(ActivityEvent("p", fmt.Sprint(i)))
	}
	head := h.Head() // 1105
	// Oldest id still in the ring is head-999. Asking from head-1000 means we need head-999: ok.
	ev, resync, _ := h.Since(head - 1000)
	if resync || len(ev) != 1000 || ev[0].ID != head-999 || ev[len(ev)-1].ID != head {
		t.Fatalf("resync=%v len=%d first=%d last=%d", resync, len(ev), firstID(ev), lastID(ev))
	}
	// One more back needs head-1000, which fell out.
	if _, resync, _ := h.Since(head - 1001); !resync {
		t.Fatal("want resync when the next needed id fell out of the ring")
	}
}

func firstID(ev []Event) uint64 {
	if len(ev) == 0 {
		return 0
	}
	return ev[0].ID
}

func lastID(ev []Event) uint64 {
	if len(ev) == 0 {
		return 0
	}
	return ev[len(ev)-1].ID
}

func TestSubscribeReplayFromLastID(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	for i := 0; i < 5; i++ {
		h.Publish(ActivityEvent("p", fmt.Sprint(i)))
	}
	replay, ch, resync, cancel := h.Subscribe(102)
	defer cancel()
	if resync {
		t.Fatal("unexpected resync")
	}
	if len(replay) != 3 || replay[0].ID != 103 || replay[2].ID != 105 {
		t.Fatalf("replay = %+v", replay)
	}
	h.Publish(ActivityEvent("p", "live"))
	got := drain(ch)
	if len(got) != 1 || got[0].ID != 106 {
		t.Fatalf("live = %+v", got)
	}
}

func TestSubscribeUpToDateAndFresh(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	h.Publish(ActivityEvent("p", "a"))
	h.Publish(ActivityEvent("p", "b"))
	// Caught up: nothing to replay, no resync.
	c := h.Connect(h.Head())
	if len(c.Replay) != 0 || c.Resync {
		t.Fatalf("caught-up: %+v resync=%v", c.Replay, c.Resync)
	}
	c.Close()
	// Fresh (0): nothing, no resync.
	c = h.Connect(0)
	defer c.Close()
	if len(c.Replay) != 0 || c.Resync || c.Head != h.Head() {
		t.Fatalf("fresh: %+v resync=%v head=%d", c.Replay, c.Resync, c.Head)
	}
}

func TestSubscribeResyncWhenIDFellOutOrIsFromTheFuture(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{RingSize: 3})
	for i := 0; i < 10; i++ {
		h.Publish(ActivityEvent("p", fmt.Sprint(i)))
	}
	c := h.Connect(101) // long gone
	if !c.Resync || len(c.Replay) != 0 {
		t.Fatalf("old id: resync=%v replay=%d", c.Resync, len(c.Replay))
	}
	c.Close()
	c = h.Connect(h.Head() + 50) // an id from before a restart that is bigger than ours
	if !c.Resync {
		t.Fatal("future id: want resync")
	}
	c.Close()
}

func TestResyncAfterRestartWithEmptyRing(t *testing.T) {
	// A new hub (restart) has an empty ring. An old page id below the new head must resync.
	h, _ := newTestHub(t, HubOptions{StartSeq: 5000})
	c := h.Connect(4000)
	defer c.Close()
	if !c.Resync {
		t.Fatal("want resync")
	}
}

func TestResyncEventIsNormalEvent(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()
	h.Publish(ResyncEvent())
	got := drain(c.Events)
	if len(got) != 1 || got[0].Type != TypeResync || string(got[0].Data) != "{}" {
		t.Fatalf("got %+v", got)
	}
}

func TestEventPayloadShapes(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		ev   Event
		typ  string
		data string
	}{
		{ProjectEvent("g/a", "files"), "project", `{"projectId":"g/a","what":"files"}`},
		{ServersEvent("g/a", []int{3000}), "servers", `{"projectId":"g/a","active":[3000]}`},
		{ServersEvent("", nil), "servers", `{"active":[]}`},
		{TrackerEvent("g/a", "task", "T-1"), "tracker", `{"projectId":"g/a","kind":"task","id":"T-1"}`},
		{ActivityEvent("g/a", "2026-10-01"), "activity", `{"projectId":"g/a","day":"2026-10-01"}`},
		{ScanEvent("", "running", at), "scan", `{"state":"running","at":"2026-10-01T09:30:00Z"}`},
		{CountsEvent(Counts{LiveServers: 2, OpenTasks: 3, AwaitingReview: 1, OpenNotes: 4}), "counts",
			`{"liveServers":2,"openTasks":3,"awaitingReview":1,"openNotes":4}`},
		{ResyncEvent(), "resync", `{}`},
		{HelloEvent(Hello{Now: at, Conn: "abc"}), "hello",
			`{"now":"2026-10-01T09:30:00Z","counts":{"liveServers":0,"openTasks":0,"awaitingReview":0,"openNotes":0},"conn":"abc"}`},
	}
	for _, c := range cases {
		if c.ev.Type != c.typ || string(c.ev.Data) != c.data {
			t.Errorf("%s: got %s %s", c.typ, c.ev.Type, c.ev.Data)
		}
	}
}

func TestCoalesceProjectEventsPerProject(t *testing.T) {
	h, clk := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()

	// First event goes out at once.
	if id := h.Publish(ProjectEvent("g/a", "files")); id == 0 {
		t.Fatal("first project event should be emitted")
	}
	// More inside the same second are held.
	for i := 0; i < 5; i++ {
		clk.Advance(100 * time.Millisecond)
		if id := h.Publish(ProjectEvent("g/a", "files")); id != 0 {
			t.Fatalf("event %d inside the window was emitted (id %d)", i, id)
		}
	}
	// Another project is independent.
	if id := h.Publish(ProjectEvent("g/b", "commits")); id == 0 {
		t.Fatal("other project should not be held")
	}
	if got := drain(c.Events); len(got) != 2 {
		t.Fatalf("before flush: %d events", len(got))
	}
	// Not yet a full second since the first emit (500 ms passed).
	if n := h.Flush(); n != 0 {
		t.Fatalf("early flush emitted %d", n)
	}
	// After the window, flush sends exactly one event for g/a.
	clk.Advance(600 * time.Millisecond)
	if n := h.Flush(); n != 1 {
		t.Fatalf("flush emitted %d", n)
	}
	got := drain(c.Events)
	if len(got) != 1 {
		t.Fatalf("after flush: %+v", got)
	}
	if pd := decodeProject(t, got[0]); pd.ProjectID != "g/a" || pd.What != "files" || pd.Parts != nil {
		t.Fatalf("payload = %+v", pd)
	}
	// Nothing left.
	clk.Advance(5 * time.Second)
	if n := h.Flush(); n != 0 {
		t.Fatalf("second flush emitted %d", n)
	}
}

func TestCoalesceMergesDifferentParts(t *testing.T) {
	h, clk := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()
	h.Publish(ProjectEvent("g/a", "files"))
	h.Publish(ProjectEvent("g/a", "commits"))
	h.Publish(ProjectEvent("g/a", "dirty"))
	h.Publish(ProjectEvent("g/a", "commits")) // duplicate part
	clk.Advance(time.Second)
	h.Flush()
	got := drain(c.Events)
	if len(got) != 2 {
		t.Fatalf("got %d events: %+v", len(got), got)
	}
	pd := decodeProject(t, got[1])
	if pd.What != WhatAll || len(pd.Parts) != 2 || pd.Parts[0] != "commits" || pd.Parts[1] != "dirty" {
		t.Fatalf("merged payload = %+v", pd)
	}
}

func TestCoalesceFlushesPendingWithNextPublishAfterWindow(t *testing.T) {
	// Even without a tick driver, a later event after the window sends the held ones with it.
	h, clk := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()
	h.Publish(ProjectEvent("g/a", "files"))
	h.Publish(ProjectEvent("g/a", "commits")) // held
	clk.Advance(1100 * time.Millisecond)
	if id := h.Publish(ProjectEvent("g/a", "dirty")); id == 0 {
		t.Fatal("window is over: should emit")
	}
	got := drain(c.Events)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	pd := decodeProject(t, got[1])
	if pd.What != WhatAll || len(pd.Parts) != 2 {
		t.Fatalf("payload = %+v", pd)
	}
	if n := h.Flush(); n != 0 {
		t.Fatalf("nothing should be pending, flushed %d", n)
	}
}

func TestOtherEventTypesAreNotCoalesced(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()
	for i := 0; i < 20; i++ {
		h.Publish(ActivityEvent("g/a", "2026-10-01"))
		h.Publish(ServersEvent("g/a", []int{3000}))
	}
	if got := drain(c.Events); len(got) != 40 {
		t.Fatalf("got %d, want 40", len(got))
	}
}

func TestProjectEventWithBadPayloadIsNotCoalesced(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()
	h.Publish(Event{Type: TypeProject, Data: json.RawMessage(`{"nope":1}`)})
	h.Publish(Event{Type: TypeProject, Data: json.RawMessage(`{"nope":1}`)})
	if got := drain(c.Events); len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
}

func TestRunFlushesOnTick(t *testing.T) {
	h, clk := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	defer c.Close()
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx, tick); close(done) }()

	h.Publish(ProjectEvent("g/a", "files"))
	h.Publish(ProjectEvent("g/a", "commits")) // held
	<-c.Events                                // the first one
	clk.Advance(time.Second)
	tick <- clk.Now() // unbuffered: Run has taken it, but Flush may still be running
	select {
	case e := <-c.Events:
		if pd := decodeProject(t, e); pd.ProjectID != "g/a" || pd.What != "commits" {
			t.Fatalf("payload = %+v", pd)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event after tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestVisibleFlagPerConnection(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	if h.AnyVisible() {
		t.Fatal("no connections: not visible")
	}
	a := h.Connect(0)
	b := h.Connect(0)
	if !h.AnyVisible() || !a.Visible() || !b.Visible() {
		t.Fatal("new connections count as visible")
	}
	a.SetVisible(false)
	if !h.AnyVisible() {
		t.Fatal("b is still visible")
	}
	if !h.SetVisible(b.ID, false) {
		t.Fatal("SetVisible by id failed")
	}
	if h.AnyVisible() {
		t.Fatal("both hidden")
	}
	if h.SetVisible("nope", true) {
		t.Fatal("unknown id should return false")
	}
	h.SetAllVisible(true)
	if !a.Visible() || !b.Visible() {
		t.Fatal("SetAllVisible(true) failed")
	}
	if h.Conns() != 2 {
		t.Fatalf("conns = %d", h.Conns())
	}
}

func TestVisibleFlagDroppedOnClose(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	a := h.Connect(0)
	b := h.Connect(0)
	b.SetVisible(false)
	a.Close()
	if h.AnyVisible() {
		t.Fatal("the only visible connection closed")
	}
	if a.Visible() {
		t.Fatal("closed connection reports visible")
	}
	a.Close() // second close is a no-op
	b.Close()
	if h.Conns() != 0 {
		t.Fatalf("conns = %d", h.Conns())
	}
	if _, open := <-a.Events; open {
		t.Fatal("events channel should be closed")
	}
}

func TestChangedSignals(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	signalled := func() bool {
		select {
		case <-h.Changed():
			return true
		default:
			return false
		}
	}
	c := h.Connect(0)
	if !signalled() {
		t.Fatal("connect should signal")
	}
	c.SetVisible(false)
	if !signalled() {
		t.Fatal("visibility change should signal")
	}
	c.SetVisible(false) // no change
	if signalled() {
		t.Fatal("no change, no signal")
	}
	c.Close()
	if !signalled() {
		t.Fatal("close should signal")
	}
}

func TestSlowConnectionIsClosedNotBlocking(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{ConnBuffer: 2})
	slow := h.Connect(0)
	fast := h.Connect(0)
	defer fast.Close()
	for i := 0; i < 5; i++ {
		h.Publish(ActivityEvent("p", fmt.Sprint(i)))
		drain(fast.Events)
	}
	// slow got 2 events, then was closed.
	n := 0
	for range slow.Events {
		n++
	}
	if n != 2 {
		t.Fatalf("slow got %d events before close", n)
	}
	if h.Conns() != 1 {
		t.Fatalf("conns = %d", h.Conns())
	}
}

func TestCloseHubClosesConnections(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	c := h.Connect(0)
	h.Close()
	if _, open := <-c.Events; open {
		t.Fatal("channel should be closed")
	}
	if id := h.Publish(ActivityEvent("p", "d")); id != 0 {
		t.Fatalf("publish after close returned %d", id)
	}
	late := h.Connect(0)
	if _, open := <-late.Events; open {
		t.Fatal("connect after close should give a closed channel")
	}
	h.Close() // twice is fine
}

// Run with -race: publishers, subscribers, flushers and visibility changes at once.
func TestConcurrentUse(t *testing.T) {
	h := NewHub(HubOptions{RingSize: 50, ConnBuffer: 8})
	var work sync.WaitGroup
	for i := 0; i < 4; i++ {
		work.Add(1)
		go func() {
			defer work.Done()
			for n := 0; n < 300; n++ {
				h.Publish(ProjectEvent(fmt.Sprintf("g/p%d", n%3), "files"))
				h.Publish(ActivityEvent("g/p", "2026-10-01"))
			}
		}()
	}
	for i := 0; i < 4; i++ {
		work.Add(1)
		go func() {
			defer work.Done()
			for n := 0; n < 100; n++ {
				c := h.Connect(h.Head() - 5)
				c.SetVisible(n%2 == 0)
				h.AnyVisible()
				drain(c.Events)
				c.Close()
			}
		}()
	}
	stop := make(chan struct{})
	var flusher sync.WaitGroup
	flusher.Add(1)
	go func() {
		defer flusher.Done()
		for {
			select {
			case <-stop:
				return
			default:
				h.Flush()
				h.Since(1)
				h.Conns()
			}
		}
	}()
	work.Wait()
	close(stop)
	flusher.Wait()
}
