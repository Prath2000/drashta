package indexer

import (
	"path/filepath"
	"testing"
	"time"
)

var ixT0 = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func ixTreeReq(project, path string) request {
	return request{key: jobKey{bucket: project, kind: kindTree}, path: path}
}

func TestQueue_DebounceWaitsForQuiet(t *testing.T) {
	q := newQueue(750*time.Millisecond, 5*time.Second)
	q.add(ixTreeReq("alpha", "a.go"), ixT0)
	q.add(ixTreeReq("alpha", "b.go"), ixT0.Add(500*time.Millisecond))
	if d, ok := q.next(); !ok || !d.Equal(ixT0.Add(1250*time.Millisecond)) {
		t.Fatalf("next deadline = %v, %v; want t0+1.25s", d, ok)
	}
	if got := q.pop(ixT0.Add(1249*time.Millisecond), 4); len(got) != 0 {
		t.Fatalf("job ran before 750 ms of quiet: %d", len(got))
	}
	got := q.pop(ixT0.Add(1250*time.Millisecond), 4)
	if len(got) != 1 {
		t.Fatalf("want 1 job, got %d", len(got))
	}
	if len(got[0].paths) != 2 || !got[0].paths["a.go"] || !got[0].paths["b.go"] {
		t.Fatalf("events were not merged: %v", got[0].paths)
	}
}

func TestQueue_StormRunsAtMostEveryFiveSeconds(t *testing.T) {
	q := newQueue(750*time.Millisecond, 5*time.Second)
	var runs []time.Time
	// An event every 200 ms for 12 s (a build tool storm), then quiet.
	for ms := 0; ms <= 14000; ms += 50 {
		now := ixT0.Add(time.Duration(ms) * time.Millisecond)
		if ms <= 12000 && ms%200 == 0 {
			q.add(ixTreeReq("alpha", "x"), now)
		}
		for _, j := range q.pop(now, 4) {
			runs = append(runs, now)
			q.release(j.key.bucket)
		}
	}
	if len(runs) < 2 || len(runs) > 4 {
		t.Fatalf("runs during a 12 s storm = %d (%v); want 2..4", len(runs), runs)
	}
	for n := 1; n < len(runs)-1; n++ { // the last run may follow the end of the storm sooner
		if gap := runs[n].Sub(runs[n-1]); gap < 5*time.Second {
			t.Fatalf("runs %d and %d only %v apart during the storm", n-1, n, gap)
		}
	}
	if first := runs[0].Sub(ixT0); first != 5*time.Second {
		t.Fatalf("first run after %v; want exactly the 5 s cap", first)
	}
	if last := runs[len(runs)-1]; last.Before(ixT0.Add(12750 * time.Millisecond)) {
		t.Fatalf("the storm's last events were never processed (last run %v)", last.Sub(ixT0))
	}
}

func TestQueue_OneJobPerBucket(t *testing.T) {
	q := newQueue(750*time.Millisecond, 5*time.Second)
	q.add(request{key: jobKey{bucket: "alpha", kind: kindTree}, immediate: true}, ixT0)
	q.add(request{key: jobKey{bucket: "alpha", kind: kindVCS}, immediate: true}, ixT0)
	q.add(request{key: jobKey{bucket: "beta", kind: kindVCS}, immediate: true}, ixT0)
	got := q.pop(ixT0, 4)
	if len(got) != 2 {
		t.Fatalf("want one job per bucket (2), got %d", len(got))
	}
	if more := q.pop(ixT0, 4); len(more) != 0 {
		t.Fatalf("a busy bucket ran a second job: %d", len(more))
	}
	if _, ok := q.next(); ok {
		t.Fatal("next() must ignore busy buckets")
	}
	q.release("alpha")
	more := q.pop(ixT0, 4)
	if len(more) != 1 || more[0].key.bucket != "alpha" {
		t.Fatalf("after release want alpha's second job, got %+v", more)
	}
}

func TestQueue_MaxWorkers(t *testing.T) {
	q := newQueue(750*time.Millisecond, 5*time.Second)
	for _, p := range []string{"a", "b", "c", "d", "e", "f"} {
		q.add(request{key: jobKey{bucket: p, kind: kindTree}, immediate: true}, ixT0)
	}
	if got := q.pop(ixT0, 4); len(got) != 4 {
		t.Fatalf("want 4 (the worker count), got %d", len(got))
	}
}

func TestQueue_FullAbsorbsSmallerJobs(t *testing.T) {
	q := newQueue(750*time.Millisecond, 5*time.Second)
	done := make(chan struct{})
	q.add(request{key: jobKey{bucket: "alpha", kind: kindTree}, path: "a", done: done}, ixT0)
	q.add(request{key: jobKey{bucket: "alpha", kind: kindVCS}, dirty: true}, ixT0)
	q.add(request{key: jobKey{bucket: "alpha", kind: kindFull}}, ixT0)
	q.add(request{key: jobKey{bucket: "alpha", kind: kindAuthored}, path: "/x"}, ixT0)
	if q.pending() != 1 {
		t.Fatalf("pending = %d; want only the full job", q.pending())
	}
	got := q.pop(ixT0.Add(time.Second), 4)
	if len(got) != 1 || got[0].key.kind != kindFull || len(got[0].waiters) != 1 || !got[0].dirty {
		t.Fatalf("full job did not take over: %+v", got)
	}
	// claude-all absorbs single sessions
	q.add(request{key: jobKey{bucket: bucketClaude, kind: kindClaude, sub: "s1"}}, ixT0)
	q.add(request{key: jobKey{bucket: bucketClaude, kind: kindClaudeAll}}, ixT0)
	q.add(request{key: jobKey{bucket: bucketClaude, kind: kindClaude, sub: "s2"}}, ixT0)
	if q.pending() != 1 {
		t.Fatalf("claude pending = %d; want 1", q.pending())
	}
}

func TestQueue_ImmediateSkipsDebounce(t *testing.T) {
	q := newQueue(750*time.Millisecond, 5*time.Second)
	q.add(request{key: jobKey{bucket: "alpha", kind: kindFull}, immediate: true}, ixT0)
	if got := q.pop(ixT0, 4); len(got) != 1 {
		t.Fatal("an immediate job must run at once")
	}
}

func TestRoute_Table(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "projects")
	prof := filepath.Join(root, ".claude-work")
	r := newRouter(proj, []string{"personal", "tools"}, []string{"generated", "docs/gen"}, []string{prof})
	r.byRel["personal/alpha"] = "alpha"
	r.byRel["tools/bigvenv"] = "bigvenv"
	r.hidden["bigvenv"] = true
	a := filepath.Join(proj, "personal", "alpha")

	type want struct {
		kinds    []jobKind
		activity bool
		sweep    bool
		push     bool
		dirty    bool
	}
	cases := []struct {
		path string
		kind WatchKind
		want want
	}{
		{a + "/src/main.go", Modify, want{kinds: []jobKind{kindTree, kindVCS}, activity: true, dirty: true}},
		{a + "/node_modules/x/index.js", Modify, want{}},
		{a + "/web/dist/app.js", Create, want{}},
		{a + "/generated/a.go", Create, want{}},
		{a + "/docs/gen/a.md", Create, want{}},
		{a + "/.git/HEAD", Modify, want{kinds: []jobKind{kindVCS}}},
		{a + "/.git/index", Modify, want{kinds: []jobKind{kindVCS}}},
		{a + "/.git/refs/heads/main", Modify, want{kinds: []jobKind{kindVCS}}},
		{a + "/.git/refs/remotes/origin/main", Modify, want{kinds: []jobKind{kindVCS}, push: true}},
		{a + "/.git/objects/ab/cdef", Create, want{}},
		{a + "/.fslckout", Modify, want{kinds: []jobKind{kindVCS}}},
		{a + "/repo.fossil", Modify, want{kinds: []jobKind{kindVCS}}},
		{a + "/.drashta/tasks/T-1.md", Modify, want{kinds: []jobKind{kindAuthored}}},
		{a, Modify, want{kinds: []jobKind{kindProjects}}},
		{filepath.Join(proj, "personal", "newproj"), Create, want{kinds: []jobKind{kindProjects}}},
		{filepath.Join(proj, "personal", "unknown", "x.go"), Create, want{kinds: []jobKind{kindProjects}}},
		{filepath.Join(proj, "personal", "notes.md"), Create, want{kinds: []jobKind{kindProjects}}},
		{filepath.Join(proj, "other_group", "x", "y.go"), Create, want{}},
		{proj, MustScan, want{kinds: []jobKind{kindProjects}, sweep: true}},
		{a + "/src", MustScan, want{kinds: []jobKind{kindFull}}},
		{filepath.Join(proj, "tools", "bigvenv", "lib", "x.py"), Modify, want{}},
		{prof + "/projects/-x-alpha/abc.jsonl", Modify, want{kinds: []jobKind{kindClaude}}},
		{prof + "/projects/-x-alpha/abc/subagents/agent-1.jsonl", Create, want{kinds: []jobKind{kindClaude}}},
		{prof + "/projects/-x-alpha/abc/tool-results/x.txt", Create, want{}},
		{prof + "/projects", MustScan, want{kinds: []jobKind{kindClaudeAll}}},
		{"/somewhere/else/file.go", Modify, want{}},
	}
	for _, c := range cases {
		got := r.route(WatchEvent{Path: c.path, Kind: c.kind})
		var kinds []jobKind
		push, dirty := false, false
		for _, rq := range got.reqs {
			kinds = append(kinds, rq.key.kind)
			push = push || rq.push
			dirty = dirty || rq.dirty
		}
		if len(kinds) != len(c.want.kinds) {
			t.Errorf("%s (%v): kinds %v, want %v", c.path, c.kind, kinds, c.want.kinds)
			continue
		}
		for n := range kinds {
			if kinds[n] != c.want.kinds[n] {
				t.Errorf("%s: kinds %v, want %v", c.path, kinds, c.want.kinds)
			}
		}
		if got.activity != c.want.activity || got.sweep != c.want.sweep || push != c.want.push || dirty != c.want.dirty {
			t.Errorf("%s: activity=%v sweep=%v push=%v dirty=%v; want %+v", c.path, got.activity, got.sweep, push, dirty, c.want)
		}
	}

	// a subagent file is routed to its parent session
	got := r.route(WatchEvent{Path: prof + "/projects/-x-alpha/abc/subagents/agent-1.jsonl"})
	if want := prof + "/projects/-x-alpha/abc.jsonl\x00.claude-work"; got.reqs[0].key.sub != want {
		t.Errorf("subagent parent = %q, want %q", got.reqs[0].key.sub, want)
	}
	// activity path is project-relative
	got = r.route(WatchEvent{Path: a + "/src/main.go"})
	if got.project != "alpha" || got.rel != "src/main.go" {
		t.Errorf("activity target = %q %q", got.project, got.rel)
	}
}
