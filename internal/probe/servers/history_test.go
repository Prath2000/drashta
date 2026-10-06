package servers

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

// Copied from ledger history_test.go. The only change: LoadHistory takes a
// HistoryStore instead of a file path (history.json is replaced by a store).

func srv(port, pid int, cwd, cmd string) Server {
	return Server{Port: port, PID: pid, Name: "node", Cwd: cwd, Cmd: cmd}
}

func TestMergeNewEntry(t *testing.T) {
	h := LoadHistory(&MemStore{})
	now := time.Now()
	if !h.Merge([]Server{srv(3000, 1, "/a", "node a")}, now) {
		t.Fatal("expected change for new entry")
	}
	s, ok := h.Lookup("/a|node a")
	if !ok || !s.Alive || !s.FirstSeen.Equal(now) || !s.LastSeen.Equal(now) {
		t.Fatalf("unexpected entry: %+v ok=%v", s, ok)
	}
}

func TestMergeServerGone(t *testing.T) {
	h := LoadHistory(&MemStore{})
	t0 := time.Now()
	h.Merge([]Server{srv(3000, 1, "/a", "node a")}, t0)
	if !h.Merge(nil, t0.Add(time.Minute)) {
		t.Fatal("expected change when alive flag flips")
	}
	s, _ := h.Lookup("/a|node a")
	if s.Alive || !s.LastSeen.Equal(t0) {
		t.Fatalf("want dead with LastSeen preserved, got %+v", s)
	}
}

func TestMergeRestartNewPID(t *testing.T) {
	h := LoadHistory(&MemStore{})
	t0 := time.Now()
	h.Merge([]Server{srv(3000, 1, "/a", "node a")}, t0)
	h.Merge(nil, t0.Add(time.Minute))
	t2 := t0.Add(2 * time.Minute)
	if !h.Merge([]Server{srv(3000, 2, "/a", "node a")}, t2) {
		t.Fatal("expected change on restart")
	}
	s, _ := h.Lookup("/a|node a")
	if s.PID != 2 || !s.Alive || !s.FirstSeen.Equal(t0) || !s.LastSeen.Equal(t2) {
		t.Fatalf("unexpected: %+v", s)
	}
	if len(h.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(h.Entries))
	}
}

func TestMergeLastSeenOnlyNoChange(t *testing.T) {
	h := LoadHistory(&MemStore{})
	t0 := time.Now()
	h.Merge([]Server{srv(3000, 1, "/a", "node a")}, t0)
	if h.Merge([]Server{srv(3000, 1, "/a", "node a")}, t0.Add(time.Minute)) {
		t.Fatal("LastSeen bump alone must not count as change")
	}
	s, _ := h.Lookup("/a|node a")
	if !s.LastSeen.Equal(t0.Add(time.Minute)) {
		t.Fatalf("LastSeen not bumped: %+v", s)
	}
}

func TestMergeUnenrichedKey(t *testing.T) {
	h := LoadHistory(&MemStore{})
	h.Merge([]Server{srv(3000, 42, "", "")}, time.Now())
	if _, ok := h.Lookup("pid:42|3000"); !ok {
		t.Fatalf("missing pid-keyed entry: %v", h.Entries)
	}
}

func TestMergeCapEvictsOldestDeadOnly(t *testing.T) {
	h := LoadHistory(&MemStore{})
	t0 := time.Now()
	// 50 dead entries with increasing LastSeen, then one alive.
	for i := 0; i < 50; i++ {
		h.Merge([]Server{srv(4000+i, i, "/d"+strconv.Itoa(i), "c")}, t0.Add(time.Duration(i)*time.Second))
	}
	h.Merge([]Server{srv(9000, 99, "/alive", "c")}, t0.Add(time.Hour))
	if len(h.Entries) != 50 {
		t.Fatalf("want 50 entries, got %d", len(h.Entries))
	}
	if _, ok := h.Lookup("/d0|c"); ok {
		t.Fatal("oldest dead entry should be evicted")
	}
	if _, ok := h.Lookup("/d1|c"); !ok {
		t.Fatal("second-oldest dead entry should remain")
	}
	if _, ok := h.Lookup("/alive|c"); !ok {
		t.Fatal("alive entry must never be evicted")
	}
}

func TestSnapshotOrdering(t *testing.T) {
	h := LoadHistory(&MemStore{})
	t0 := time.Now()
	h.Merge([]Server{srv(1, 1, "/o1", "c"), srv(2, 2, "/o2", "c")}, t0)
	h.Merge([]Server{srv(9000, 9, "/b", "c"), srv(8000, 8, "/a", "c")}, t0.Add(time.Minute))
	active, recent := h.Snapshot()
	if len(active) != 2 || active[0].Port != 8000 || active[1].Port != 9000 {
		t.Fatalf("active order wrong: %+v", active)
	}
	if len(recent) != 2 {
		t.Fatalf("recent: %+v", recent)
	}
	h.Entries["/o1|c"].LastSeen = t0.Add(time.Second)
	_, recent = h.Snapshot()
	if recent[0].Cwd != "/o1" {
		t.Fatalf("recent should be LastSeen desc: %+v", recent)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	st := &MemStore{}
	h := LoadHistory(st)
	h.Merge([]Server{srv(3000, 1, "/a", "node a")}, time.Now())
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}
	h2 := LoadHistory(st)
	want, _ := h.Lookup("/a|node a")
	got, ok := h2.Lookup("/a|node a")
	if !ok || got.Port != want.Port || !got.FirstSeen.Equal(want.FirstSeen) || !got.Alive {
		t.Fatalf("round trip mismatch: %+v vs %+v", got, want)
	}
}

func TestLoadCorruptFileEmpty(t *testing.T) {
	h := LoadHistory(&MemStore{LoadErr: errors.New("corrupt")})
	if h == nil || len(h.Entries) != 0 {
		t.Fatalf("want empty history, got %+v", h)
	}
}

func TestMergeSharedKeyStable(t *testing.T) {
	h := LoadHistory(&MemStore{})
	t0 := time.Now()
	// One process on two ports, plus a worker sharing cmd+cwd.
	live := []Server{srv(5173, 7, "/a", "node a"), srv(3000, 7, "/a", "node a"), srv(3000, 9, "/a", "node a")}
	if !h.Merge(live, t0) {
		t.Fatal("expected change on first merge")
	}
	if h.Merge(live, t0.Add(time.Second)) {
		t.Fatal("second merge with identical input reported a change")
	}
	// Order of rows must not matter.
	rev := []Server{live[2], live[1], live[0]}
	if h.Merge(rev, t0.Add(2*time.Second)) {
		t.Fatal("reordered input reported a change")
	}
	s, _ := h.Lookup("/a|node a")
	if s.Port != 3000 || s.PID != 7 {
		t.Fatalf("kept row = port %d pid %d, want lowest port then pid (3000, 7)", s.Port, s.PID)
	}
}

// drashta additions below.

func TestRunsDeadHasEnded(t *testing.T) {
	st := &MemStore{}
	h := LoadHistory(st)
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	a := srv(3000, 1, "/p/a", "node a")
	a.ProjectID, a.Kind = "g/a", KindDev
	h.Merge([]Server{a, srv(4000, 2, "/p/b", "node b")}, t0)
	h.Merge([]Server{a}, t0.Add(time.Minute))
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}
	runs, _ := st.Load()
	if len(runs) != 2 {
		t.Fatalf("runs = %+v", runs)
	}
	byKey := map[string]Run{}
	for _, r := range runs {
		byKey[r.Key] = r
	}
	ra, rb := byKey["/p/a|node a"], byKey["/p/b|node b"]
	if !ra.Ended.IsZero() || ra.ProjectID != "g/a" || ra.Kind != KindDev || !ra.Started.Equal(t0) {
		t.Fatalf("alive run = %+v", ra)
	}
	if !rb.Ended.Equal(t0) || !rb.LastSeen.Equal(t0) {
		t.Fatalf("dead run = %+v", rb)
	}
	h2 := LoadHistory(st)
	if s, _ := h2.Lookup("/p/b|node b"); s.Alive {
		t.Fatal("dead run loaded as alive")
	}
	if s, _ := h2.Lookup("/p/a|node a"); !s.Alive || s.ProjectID != "g/a" {
		t.Fatalf("alive run loaded wrong: %+v", s)
	}
}

func TestMergeProjectChangeCounts(t *testing.T) {
	h := LoadHistory(nil)
	t0 := time.Now()
	a := srv(3000, 1, "/a", "node a")
	h.Merge([]Server{a}, t0)
	a.ProjectID = "g/a"
	if !h.Merge([]Server{a}, t0.Add(time.Second)) {
		t.Fatal("project id change should count as a change")
	}
	if err := h.Save(); err != nil {
		t.Fatal("Save without store must be a no-op:", err)
	}
}

func TestRefKey(t *testing.T) {
	k := RefKey("/a|node a")
	if len(k) != len("server-key:")+10 || k[:11] != "server-key:" {
		t.Fatalf("RefKey = %q", k)
	}
	if RefKey("/a|node a") != k || RefKey("/b|node b") == k {
		t.Fatal("RefKey must be stable and distinct")
	}
	if Key(srv(1, 2, "", "")) != "pid:2|1" {
		t.Fatal("Key fallback")
	}
}
