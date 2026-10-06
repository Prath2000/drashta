package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// Defaults for HubOptions.
const (
	DefaultRingSize   = 1000
	DefaultWindow     = time.Second
	DefaultConnBuffer = 256
	DefaultTick       = 250 * time.Millisecond
)

// HubOptions configure a Hub. Every field is optional.
type HubOptions struct {
	// Now is the clock. Default time.Now. Tests inject a fake one.
	Now func() time.Time
	// RingSize is how many past events are kept for replay. Default 1000.
	RingSize int
	// Window is the coalescing window for "project" events. Default 1 s.
	Window time.Duration
	// ConnBuffer is the size of each connection's channel. Default 256.
	// A connection that falls further behind is closed (it can reconnect
	// with Last-Event-ID and get a replay or a resync).
	ConnBuffer int
	// StartSeq is the sequence number before the first event. Default: the
	// start time in microseconds. A time-based start means ids from before a
	// restart are always older than anything the new hub holds, so a page
	// that reconnects after a restart gets a resync, never a wrong replay.
	StartSeq uint64
	// Tick is the flush interval used by Start. Default 250 ms.
	Tick time.Duration
}

// Hub is the in-process event bus behind GET /api/events.
//
// It numbers events, keeps the last RingSize of them, coalesces "project"
// events and tracks which connections say they are visible.
// All methods are safe for concurrent use.
type Hub struct {
	now      func() time.Time
	window   time.Duration
	connBuf  int
	tick     time.Duration
	ringSize int

	mu       sync.Mutex
	seq      uint64  // last assigned id
	ring     []Event // circular buffer
	ringHead int     // index of the oldest event
	ringLen  int
	conns    map[string]*Conn
	lastProj map[string]time.Time       // project -> time of last emitted project event
	pending  map[string]*pendingProject // project -> events held back by coalescing
	changed  chan struct{}
	closed   bool
}

type pendingProject struct {
	parts []string // distinct "what" values, in arrival order
}

// NewHub makes a hub.
func NewHub(o HubOptions) *Hub {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RingSize <= 0 {
		o.RingSize = DefaultRingSize
	}
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	if o.ConnBuffer <= 0 {
		o.ConnBuffer = DefaultConnBuffer
	}
	if o.Tick <= 0 {
		o.Tick = DefaultTick
	}
	if o.StartSeq == 0 {
		if us := o.Now().UnixMicro(); us > 0 {
			o.StartSeq = uint64(us)
		}
	}
	return &Hub{
		now:      o.Now,
		window:   o.Window,
		connBuf:  o.ConnBuffer,
		tick:     o.Tick,
		ringSize: o.RingSize,
		seq:      o.StartSeq,
		ring:     make([]Event, o.RingSize),
		conns:    map[string]*Conn{},
		lastProj: map[string]time.Time{},
		pending:  map[string]*pendingProject{},
		changed:  make(chan struct{}, 1),
	}
}

// Publish gives the event the next id, stores it in the ring and sends it to
// every connection. It returns the id.
//
// "project" events are coalesced: after one is emitted for a project, later
// ones for the same project wait until the window has passed. They are then
// merged into a single event (see ProjectData). While an event is held back,
// Publish returns 0. Held events are sent by Flush (or by the next Publish
// for that project once the window is over).
func (h *Hub) Publish(e Event) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0
	}
	if e.Type == TypeProject {
		var pd ProjectData
		if json.Unmarshal(e.Data, &pd) == nil && pd.ProjectID != "" {
			return h.publishProjectLocked(pd)
		}
	}
	return h.appendLocked(e)
}

func (h *Hub) publishProjectLocked(pd ProjectData) uint64 {
	now := h.now()
	p := h.pending[pd.ProjectID]
	last, seen := h.lastProj[pd.ProjectID]
	inWindow := seen && now.Sub(last) < h.window
	if inWindow {
		// Too soon: hold it.
		if p == nil {
			p = &pendingProject{}
			h.pending[pd.ProjectID] = p
		}
		p.add(pd)
		return 0
	}
	if p != nil {
		// Window is over and something was waiting: send it merged with this one.
		p.add(pd)
		delete(h.pending, pd.ProjectID)
		return h.emitProjectLocked(pd.ProjectID, p.parts, now)
	}
	return h.emitProjectLocked(pd.ProjectID, []string{pd.What}, now)
}

func (p *pendingProject) add(pd ProjectData) {
	whats := pd.Parts
	if len(whats) == 0 {
		whats = []string{pd.What}
	}
	for _, w := range whats {
		dup := false
		for _, have := range p.parts {
			if have == w {
				dup = true
				break
			}
		}
		if !dup {
			p.parts = append(p.parts, w)
		}
	}
}

func (h *Hub) emitProjectLocked(project string, parts []string, now time.Time) uint64 {
	pd := ProjectData{ProjectID: project}
	if len(parts) == 1 {
		pd.What = parts[0]
	} else {
		pd.What = WhatAll
		pd.Parts = append([]string(nil), parts...)
	}
	h.lastProj[project] = now
	return h.appendLocked(NewEvent(TypeProject, pd))
}

// Flush sends every held-back project event whose window is over.
// It returns how many events it emitted. Run and Start call it on a tick.
func (h *Hub) Flush() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.pending) == 0 {
		return 0
	}
	now := h.now()
	ids := make([]string, 0, len(h.pending))
	for id := range h.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	n := 0
	for _, id := range ids {
		if now.Sub(h.lastProj[id]) < h.window {
			continue
		}
		p := h.pending[id]
		delete(h.pending, id)
		h.emitProjectLocked(id, p.parts, now)
		n++
	}
	return n
}

// Run calls Flush on every tick until ctx ends. Tests pass their own channel.
func (h *Hub) Run(ctx context.Context, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			h.Flush()
		}
	}
}

// Start runs Run in a goroutine with a real ticker (HubOptions.Tick).
func (h *Hub) Start(ctx context.Context) {
	t := time.NewTicker(h.tick)
	go func() {
		defer t.Stop()
		h.Run(ctx, t.C)
	}()
}

// appendLocked numbers the event, stores it and fans it out.
func (h *Hub) appendLocked(e Event) uint64 {
	h.seq++
	e.ID = h.seq
	if e.Data == nil {
		e.Data = json.RawMessage("{}")
	}
	if h.ringLen < h.ringSize {
		h.ring[(h.ringHead+h.ringLen)%h.ringSize] = e
		h.ringLen++
	} else {
		h.ring[h.ringHead] = e
		h.ringHead = (h.ringHead + 1) % h.ringSize
	}
	for _, c := range h.conns {
		select {
		case c.ch <- e:
		default:
			// Too slow. Close it; the page reconnects and catches up.
			h.closeConnLocked(c)
		}
	}
	return e.ID
}

// Head returns the id of the newest event (the start sequence if none yet).
func (h *Hub) Head() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// Since returns the events after lastID, in order. resync is true when the
// caller cannot be caught up: lastID is no longer in the ring, or it is newer
// than anything this hub has issued (the server restarted).
// lastID 0 means "I know nothing": no events and no resync, just the head.
func (h *Hub) Since(lastID uint64) (events []Event, resync bool, head uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	events, resync = h.sinceLocked(lastID)
	return events, resync, h.seq
}

func (h *Hub) sinceLocked(lastID uint64) ([]Event, bool) {
	if lastID == 0 || lastID == h.seq {
		return nil, false
	}
	if lastID > h.seq {
		return nil, true
	}
	oldest := h.seq - uint64(h.ringLen) + 1 // id of the oldest event in the ring
	if lastID+1 < oldest {
		return nil, true
	}
	skip := int(lastID + 1 - oldest)
	out := make([]Event, 0, h.ringLen-skip)
	for i := skip; i < h.ringLen; i++ {
		out = append(out, h.ring[(h.ringHead+i)%h.ringSize])
	}
	return out, false
}

// Conn is one open event stream. Get it from Hub.Connect and call Close when
// the stream ends.
type Conn struct {
	// ID identifies the connection for POST /api/presence.
	ID string
	// Replay holds the events missed since the Last-Event-ID, oldest first.
	Replay []Event
	// Resync is true when the page must refetch everything (Replay is empty).
	Resync bool
	// Events delivers new events. It is closed when the connection is closed,
	// when it fell too far behind, or when the hub is closed.
	Events <-chan Event
	// Head is the hub's newest id at connect time.
	Head uint64

	h       *Hub
	ch      chan Event
	visible bool // guarded by h.mu
	closed  bool // guarded by h.mu
}

// Connect registers a new stream. lastID is the Last-Event-ID the page sent
// (0 for a first connection). Replay and the live channel are taken under one
// lock, so no event is lost or repeated between them.
// A new connection counts as visible until the page says otherwise.
func (h *Hub) Connect(lastID uint64) *Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan Event, h.connBuf)
	c := &Conn{ID: newConnID(), Events: ch, ch: ch, h: h, visible: true, Head: h.seq}
	c.Replay, c.Resync = h.sinceLocked(lastID)
	if h.closed {
		c.closed = true
		close(ch)
		return c
	}
	h.conns[c.ID] = c
	h.notifyLocked()
	return c
}

// Subscribe is Connect in the shape of the original brief: the replay, the
// live channel, the resync flag and a cancel function.
func (h *Hub) Subscribe(lastID uint64) (replay []Event, ch <-chan Event, resync bool, cancel func()) {
	c := h.Connect(lastID)
	return c.Replay, c.Events, c.Resync, c.Close
}

// Close ends the connection. It is safe to call more than once.
func (c *Conn) Close() {
	c.h.mu.Lock()
	defer c.h.mu.Unlock()
	c.h.closeConnLocked(c)
}

// SetVisible sets this connection's visible flag.
func (c *Conn) SetVisible(v bool) {
	c.h.mu.Lock()
	defer c.h.mu.Unlock()
	if c.closed || c.visible == v {
		return
	}
	c.visible = v
	c.h.notifyLocked()
}

// Visible reports this connection's flag. A closed connection is not visible.
func (c *Conn) Visible() bool {
	c.h.mu.Lock()
	defer c.h.mu.Unlock()
	return !c.closed && c.visible
}

func (h *Hub) closeConnLocked(c *Conn) {
	if c.closed {
		return
	}
	c.closed = true
	delete(h.conns, c.ID)
	close(c.ch)
	h.notifyLocked()
}

// SetVisible sets the flag of one connection by id. It returns false if the
// id is not an open connection.
func (h *Hub) SetVisible(connID string, visible bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[connID]
	if !ok {
		return false
	}
	if c.visible != visible {
		c.visible = visible
		h.notifyLocked()
	}
	return true
}

// SetAllVisible sets the flag of every open connection. The HTTP layer uses it
// when a page posts presence without a connection id. It returns how many
// connections it touched.
func (h *Hub) SetAllVisible(visible bool) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed := false
	for _, c := range h.conns {
		if c.visible != visible {
			c.visible = visible
			changed = true
		}
	}
	if changed {
		h.notifyLocked()
	}
	return len(h.conns)
}

// AnyVisible reports whether at least one open connection is visible.
// The lsof loop uses it: 5 s while true, 60 s while false (S8).
func (h *Hub) AnyVisible() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.conns {
		if c.visible {
			return true
		}
	}
	return false
}

// Conns returns the number of open connections.
func (h *Hub) Conns() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// Changed returns a channel that gets a signal (never blocks the hub, signals
// may merge) each time a connection opens, closes or changes its flag. A
// scheduler waits on it to switch cadence without polling.
func (h *Hub) Changed() <-chan struct{} { return h.changed }

func (h *Hub) notifyLocked() {
	select {
	case h.changed <- struct{}{}:
	default:
	}
}

// Close closes every connection and stops accepting events.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, c := range h.conns {
		h.closeConnLocked(c)
	}
}

func newConnID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported systems; fall back to the clock.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return hex.EncodeToString(b[:])
}
