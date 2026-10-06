package servers

import (
	"crypto/sha1"
	"encoding/hex"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"
)

const maxEntries = 50

// Run is one remembered server, as stored by a HistoryStore (the store
// package maps it to the server_runs table). There is one Run per key.
// Started is the first time the key was seen. Ended is zero while the server
// is alive; when it dies Ended is set to LastSeen.
type Run struct {
	Key       string    `json:"key"`
	ProjectID string    `json:"projectId"`
	Port      int       `json:"port"`
	PID       int       `json:"pid"`
	Name      string    `json:"name"`
	Cmd       string    `json:"cmd"`
	Cwd       string    `json:"cwd"`
	Kind      string    `json:"kind"`
	Started   time.Time `json:"started"`
	LastSeen  time.Time `json:"lastSeen"`
	Ended     time.Time `json:"ended"`
}

// HistoryStore persists the history. Save replaces the whole set.
type HistoryStore interface {
	Load() ([]Run, error)
	Save([]Run) error
}

// MemStore is an in-memory HistoryStore for tests and for running without a DB.
type MemStore struct {
	mu   sync.Mutex
	runs []Run
	// LoadErr, when set, is returned by Load (to test the "bad store" path).
	LoadErr error
}

// Load returns a copy of the saved runs.
func (m *MemStore) Load() ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.LoadErr != nil {
		return nil, m.LoadErr
	}
	return append([]Run(nil), m.runs...), nil
}

// Save replaces the saved runs with a copy of runs.
func (m *MemStore) Save(runs []Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs = append([]Run(nil), runs...)
	return nil
}

// History keeps every server ever seen, keyed by serverKey.
type History struct {
	Entries map[string]*Server
	store   HistoryStore
	mu      sync.Mutex
}

// serverKey identifies a server across restarts. Servers whose enrichment
// failed (no Cwd and no Cmd) fall back to a pid/port key.
func serverKey(s Server) string {
	if s.Cwd == "" && s.Cmd == "" {
		return "pid:" + strconv.Itoa(s.PID) + "|" + strconv.Itoa(s.Port)
	}
	return s.Cwd + "|" + s.Cmd
}

// Key returns the history key of s: "<cwd>|<cmd>", or "pid:<pid>|<port>" when
// both are unknown. Restart and the API use this key.
func Key(s Server) string { return serverKey(s) }

// RefKey returns the stable ref form "server-key:<sha1(key)[:10]>" (04 §2.4).
func RefKey(key string) string {
	sum := sha1.Sum([]byte(key))
	return "server-key:" + hex.EncodeToString(sum[:])[:10]
}

// LoadHistory reads the history from st. A failing store yields an empty
// history (ledger rule for a corrupt file); the error is logged.
func LoadHistory(st HistoryStore) *History {
	h := &History{Entries: map[string]*Server{}, store: st}
	if st == nil {
		return h
	}
	runs, err := st.Load()
	if err != nil {
		slog.Warn("servers: load history", "err", err)
		return h
	}
	for _, r := range runs {
		s := runToServer(r)
		k := r.Key
		if k == "" {
			k = serverKey(s)
		}
		h.Entries[k] = &s
	}
	return h
}

func runToServer(r Run) Server {
	return Server{
		Port: r.Port, PID: r.PID, Name: r.Name, Cmd: r.Cmd, Cwd: r.Cwd,
		ProjectID: r.ProjectID, Kind: r.Kind,
		FirstSeen: r.Started, LastSeen: r.LastSeen, Alive: r.Ended.IsZero(),
	}
}

func serverToRun(k string, s Server) Run {
	r := Run{
		Key: k, ProjectID: s.ProjectID, Port: s.Port, PID: s.PID, Name: s.Name,
		Cmd: s.Cmd, Cwd: s.Cwd, Kind: s.Kind, Started: s.FirstSeen, LastSeen: s.LastSeen,
	}
	if !s.Alive {
		r.Ended = s.LastSeen
	}
	return r
}

// Merge folds a live scan into the history and reports whether anything
// meaningful changed (LastSeen bumps on already-alive entries do not count).
func (h *History) Merge(live []Server, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed := false
	seen := map[string]bool{}
	for _, s := range collapseByKey(live) {
		k := serverKey(s)
		seen[k] = true
		e, ok := h.Entries[k]
		if !ok {
			n := s
			n.FirstSeen, n.LastSeen, n.Alive = now, now, true
			h.Entries[k] = &n
			changed = true
			continue
		}
		if e.Port != s.Port || e.PID != s.PID || !e.Alive || e.ProjectID != s.ProjectID {
			changed = true
		}
		e.Port, e.PID, e.Name, e.Kind, e.ProjectID = s.Port, s.PID, s.Name, s.Kind, s.ProjectID
		e.LastSeen, e.Alive = now, true
	}
	for k, e := range h.Entries {
		if !seen[k] && e.Alive {
			e.Alive = false
			changed = true
		}
	}
	if len(h.Entries) > maxEntries {
		var dead []string
		for k, e := range h.Entries {
			if !e.Alive {
				dead = append(dead, k)
			}
		}
		sort.Slice(dead, func(i, j int) bool {
			return h.Entries[dead[i]].LastSeen.Before(h.Entries[dead[j]].LastSeen)
		})
		for _, k := range dead {
			if len(h.Entries) <= maxEntries {
				break
			}
			delete(h.Entries, k)
			changed = true
		}
	}
	return changed
}

// collapseByKey keeps one row per serverKey (lowest port, then lowest PID) so
// several live rows sharing a key (one process on two ports, or workers with the
// same cmd+cwd) do not flip the stored Port/PID on every scan.
func collapseByKey(live []Server) []Server {
	best := make(map[string]int, len(live))
	var out []Server
	for _, s := range live {
		k := serverKey(s)
		i, ok := best[k]
		if !ok {
			best[k] = len(out)
			out = append(out, s)
			continue
		}
		if b := out[i]; s.Port < b.Port || (s.Port == b.Port && s.PID < b.PID) {
			out[i] = s
		}
	}
	return out
}

// Runs returns the history as store rows, sorted by key.
func (h *History) Runs() []Run {
	h.mu.Lock()
	defer h.mu.Unlock()
	runs := make([]Run, 0, len(h.Entries))
	for k, e := range h.Entries {
		runs = append(runs, serverToRun(k, *e))
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Key < runs[j].Key })
	return runs
}

// Save writes the whole history to the store. Without a store it does nothing.
func (h *History) Save() error {
	if h.store == nil {
		return nil
	}
	return h.store.Save(h.Runs())
}

// Snapshot returns copies of the alive servers (by port) and the dead ones (newest first).
func (h *History) Snapshot() (active, recent []Server) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.Entries {
		if e.Alive {
			active = append(active, *e)
		} else {
			recent = append(recent, *e)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Port < active[j].Port })
	sort.Slice(recent, func(i, j int) bool { return recent[i].LastSeen.After(recent[j].LastSeen) })
	return active, recent
}

// Lookup returns a copy of the entry stored under key.
func (h *History) Lookup(key string) (Server, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.Entries[key]
	if !ok {
		return Server{}, false
	}
	return *e, true
}
