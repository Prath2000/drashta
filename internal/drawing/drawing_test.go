package drawing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/runx"
)

// fakeRun stands in for runx.Run: it records every call and answers with canned output.
type fakeRun struct {
	mu    sync.Mutex
	calls []fakeCall
	// reply builds the answer. The default is "one JSONL line" for entities and "{}" for layers.
	reply func(args []string) (runx.Result, error)
	delay time.Duration
}

type fakeCall struct {
	opts runx.Options
	name string
	args []string
}

func (f *fakeRun) run(ctx context.Context, o runx.Options, name string, args ...string) (runx.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{opts: o, name: name, args: append([]string(nil), args...)})
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.reply != nil {
		return f.reply(args)
	}
	if len(args) > 0 && args[0] == "layers" {
		return runx.Result{Stdout: []byte(`{"layers":[]}`)}, nil
	}
	return runx.Result{Stdout: []byte(`{"format_version":2}` + "\n" + `{"type":"LINE"}` + "\n")}, nil
}

func (f *fakeRun) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fixture makes a reader with a fake program file, a drawing file and a fake runner.
type fixture struct {
	r     *Reader
	fake  *fakeRun
	bin   string
	dwg   string
	cache string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", "chitralipi")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A name with spaces, a hash and a percent sign: it is one argv element and nothing else.
	dwg := filepath.Join(dir, "plans", "Site Plan #3 100%.dwg")
	if err := os.MkdirAll(filepath.Dir(dwg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dwg, []byte("AC1024 fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache", "dwg")
	f := &fixture{fake: &fakeRun{}, bin: bin, dwg: dwg, cache: cache}
	f.r = New(bin, cache)
	f.r.Run = f.fake.run
	return f
}

func TestEntitiesCacheHit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p1, err := f.r.Entities(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := f.r.Entities(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Errorf("paths differ: %s vs %s", p1, p2)
	}
	if n := f.fake.count(); n != 1 {
		t.Errorf("Run called %d times, want 1", n)
	}
	got, err := os.ReadFile(p1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"format_version":2}`+"\n"+`{"type":"LINE"}`+"\n" {
		t.Errorf("cached bytes = %q", got)
	}
	if filepath.Dir(p1) != f.cache || !strings.HasSuffix(p1, ".jsonl") {
		t.Errorf("cache path = %s, want a .jsonl in %s", p1, f.cache)
	}
}

func TestEntitiesFileModes(t *testing.T) {
	f := newFixture(t)
	p, err := f.r.Entities(context.Background(), f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", st.Mode().Perm())
	}
	ds, err := os.Stat(f.cache)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", ds.Mode().Perm())
	}
	// No temp files are left behind.
	ents, _ := os.ReadDir(f.cache)
	if len(ents) != 1 {
		t.Errorf("cache dir has %d entries, want 1", len(ents))
	}
}

func TestEntitiesNewMtimeNewKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p1, err := f.r.Entities(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(f.dwg, later, later); err != nil {
		t.Fatal(err)
	}
	p2, err := f.r.Entities(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Error("a changed mtime must give a new cache file")
	}
	if n := f.fake.count(); n != 2 {
		t.Errorf("Run called %d times, want 2", n)
	}
	// A new size gives a new key too, even with the same mtime.
	if err := os.WriteFile(f.dwg, []byte("AC1024 fake, longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f.dwg, later, later); err != nil {
		t.Fatal(err)
	}
	p3, err := f.r.Entities(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	if p3 == p2 || p3 == p1 {
		t.Error("a changed size must give a new cache file")
	}
}

func TestEntitiesNewProgramNewKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p1, err := f.r.Entities(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(3 * time.Hour)
	if err := os.Chtimes(f.bin, later, later); err != nil {
		t.Fatal(err)
	}
	p2, err := f.r.Entities(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Error("an upgraded chitralipi must not reuse the old cache")
	}
}

func TestMissingBinIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	dwg := filepath.Join(dir, "a.dwg")
	if err := os.WriteFile(dwg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "no", "chitralipi")
	fake := &fakeRun{}
	r := New(missing, filepath.Join(dir, "cache"))
	r.Run = fake.run
	for name, call := range map[string]func() error{
		"Layers":   func() error { _, err := r.Layers(context.Background(), dwg); return err },
		"Entities": func() error { _, err := r.Entities(context.Background(), dwg); return err },
	} {
		err := call()
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s: err = %v, want ErrUnavailable", name, err)
		}
		if errors.Is(err, ErrBadDrawing) {
			t.Errorf("%s: must not also be ErrBadDrawing", name)
		}
		if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "config.toml") {
			t.Errorf("%s: message %q should name the path and config.toml", name, err)
		}
	}
	if fake.count() != 0 {
		t.Error("Run must not be called when the program is missing")
	}
	// An empty name is the same answer.
	if _, err := New("", dir).Layers(context.Background(), dwg); !errors.Is(err, ErrUnavailable) {
		t.Errorf("empty Bin: err = %v", err)
	}
}

func TestRunxNotFoundIsUnavailable(t *testing.T) {
	f := newFixture(t)
	f.fake.reply = func([]string) (runx.Result, error) {
		return runx.Result{ExitCode: -1}, fmt.Errorf("%w: chitralipi", runx.ErrNotFound)
	}
	if _, err := f.r.Entities(context.Background(), f.dwg); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Entities: err = %v, want ErrUnavailable", err)
	}
	if _, err := f.r.Layers(context.Background(), f.dwg); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Layers: err = %v, want ErrUnavailable", err)
	}
}

func TestNonZeroExitIsBadDrawing(t *testing.T) {
	f := newFixture(t)
	f.fake.reply = func([]string) (runx.Result, error) {
		return runx.Result{ExitCode: 2, Stderr: []byte("bad\nmore")}, nil
	}
	for name, call := range map[string]func() error{
		"Layers":   func() error { _, err := f.r.Layers(context.Background(), f.dwg); return err },
		"Entities": func() error { _, err := f.r.Entities(context.Background(), f.dwg); return err },
	} {
		err := call()
		if !errors.Is(err, ErrBadDrawing) {
			t.Fatalf("%s: err = %v, want ErrBadDrawing", name, err)
		}
		if !strings.Contains(err.Error(), "bad") || strings.Contains(err.Error(), "more") {
			t.Errorf("%s: message %q should hold the first stderr line only", name, err)
		}
	}
	// Nothing is cached after a failure.
	if ents, _ := os.ReadDir(f.cache); len(ents) != 0 {
		t.Errorf("a failed run left %d cache entries", len(ents))
	}
}

func TestLongStderrIsCut(t *testing.T) {
	f := newFixture(t)
	f.fake.reply = func([]string) (runx.Result, error) {
		return runx.Result{ExitCode: 1, Stderr: []byte(strings.Repeat("é", 500) + "\nsecond")}, nil
	}
	_, err := f.r.Layers(context.Background(), f.dwg)
	if !errors.Is(err, ErrBadDrawing) {
		t.Fatal(err)
	}
	line := strings.TrimPrefix(err.Error(), ErrBadDrawing.Error()+": ")
	if n := len([]rune(line)); n != 200 {
		t.Errorf("detail is %d characters, want 200", n)
	}
}

func TestEmptyStderrStillSaysSomething(t *testing.T) {
	f := newFixture(t)
	f.fake.reply = func([]string) (runx.Result, error) { return runx.Result{ExitCode: 3}, nil }
	_, err := f.r.Layers(context.Background(), f.dwg)
	if !errors.Is(err, ErrBadDrawing) || !strings.Contains(err.Error(), "3") {
		t.Errorf("err = %v, want ErrBadDrawing naming exit code 3", err)
	}
}

func TestTruncatedIsBadDrawing(t *testing.T) {
	f := newFixture(t)
	f.fake.reply = func([]string) (runx.Result, error) {
		return runx.Result{Stdout: []byte("partial"), Truncated: true}, nil
	}
	_, err := f.r.Entities(context.Background(), f.dwg)
	if !errors.Is(err, ErrBadDrawing) || !strings.Contains(err.Error(), "too large") {
		t.Errorf("Entities: err = %v, want ErrBadDrawing 'too large'", err)
	}
	if ents, _ := os.ReadDir(f.cache); len(ents) != 0 {
		t.Errorf("a truncated run was cached (%d entries)", len(ents))
	}
	if _, err := f.r.Layers(context.Background(), f.dwg); !errors.Is(err, ErrBadDrawing) {
		t.Errorf("Layers: err = %v, want ErrBadDrawing", err)
	}
}

func TestArgsAndOptions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.r.Entities(ctx, f.dwg); err != nil {
		t.Fatal(err)
	}
	out, err := f.r.Layers(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"layers":[]}` {
		t.Errorf("Layers = %q", out)
	}
	if len(f.fake.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(f.fake.calls))
	}
	ent, lay := f.fake.calls[0], f.fake.calls[1]
	if ent.name != f.bin || lay.name != f.bin {
		t.Errorf("program = %q / %q, want %q", ent.name, lay.name, f.bin)
	}
	if want := []string{"entities", f.dwg, "--space", "model", "--as", "jsonl"}; !reflect.DeepEqual(ent.args, want) {
		t.Errorf("entities args = %q, want %q", ent.args, want)
	}
	if want := []string{"layers", f.dwg, "--as", "json"}; !reflect.DeepEqual(lay.args, want) {
		t.Errorf("layers args = %q, want %q", lay.args, want)
	}
	if !strings.Contains(ent.args[1], " ") {
		t.Fatal("fixture path should contain a space")
	}
	if ent.opts.Timeout != Timeout || ent.opts.MaxOut != MaxEntitiesBytes {
		t.Errorf("entities options = %+v", ent.opts)
	}
	if lay.opts.Timeout != Timeout || lay.opts.MaxOut != 8<<20 {
		t.Errorf("layers options = %+v", lay.opts)
	}
	if MaxEntitiesBytes != 128<<20 || Timeout != 60*time.Second {
		t.Errorf("constants changed: %d %v", MaxEntitiesBytes, Timeout)
	}
}

func TestConcurrentEntitiesRunOnce(t *testing.T) {
	f := newFixture(t)
	f.fake.delay = 50 * time.Millisecond
	var wg sync.WaitGroup
	paths := make([]string, 4)
	errs := make([]error, 4)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths[i], errs[i] = f.r.Entities(context.Background(), f.dwg)
		}()
	}
	wg.Wait()
	for i := range paths {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if paths[i] != paths[0] {
			t.Errorf("call %d path = %s, want %s", i, paths[i], paths[0])
		}
	}
	if n := f.fake.count(); n != 1 {
		t.Errorf("Run called %d times for 4 concurrent callers, want 1", n)
	}
}

func TestConcurrentOtherFilesDoNotWait(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(filepath.Dir(f.dwg), "other.dwg")
	if err := os.WriteFile(other, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	var running, peak atomic.Int32
	f.fake.reply = func([]string) (runx.Result, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		running.Add(-1)
		return runx.Result{Stdout: []byte("{}\n")}, nil
	}
	var wg sync.WaitGroup
	for _, p := range []string{f.dwg, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.r.Entities(context.Background(), p); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak.Load() != 2 {
		t.Errorf("two different drawings ran %d at a time, want 2", peak.Load())
	}
}

func TestCacheKeepsAtMost200Files(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.cache, 0o700); err != nil {
		t.Fatal(err)
	}
	// 205 old cache files, the oldest first.
	base := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 205; i++ {
		p := filepath.Join(f.cache, fmt.Sprintf("old%03d.jsonl", i))
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	// A file that is not a cache file is never touched.
	stray := filepath.Join(f.cache, "README.txt")
	if err := os.WriteFile(stray, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := f.r.Entities(context.Background(), f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	ents, _ := filepath.Glob(filepath.Join(f.cache, "*.jsonl"))
	if len(ents) != 200 {
		t.Fatalf("%d cache files, want 200", len(ents))
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the file just written was pruned: %v", err)
	}
	// The oldest six went (205 old + 1 new = 206, minus 6).
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(f.cache, fmt.Sprintf("old%03d.jsonl", i))); !os.IsNotExist(err) {
			t.Errorf("old%03d should be gone (err=%v)", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.cache, "old006.jsonl")); err != nil {
		t.Errorf("old006 should stay: %v", err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("stray file removed: %v", err)
	}
}

func TestContextCancelIsNotBadDrawing(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.fake.reply = func([]string) (runx.Result, error) {
		return runx.Result{ExitCode: -1}, context.Canceled
	}
	_, err := f.r.Entities(ctx, f.dwg)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrBadDrawing) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestTimeoutIsBadDrawing(t *testing.T) {
	f := newFixture(t)
	f.fake.reply = func([]string) (runx.Result, error) {
		return runx.Result{ExitCode: -1}, context.DeadlineExceeded
	}
	_, err := f.r.Entities(context.Background(), f.dwg)
	if !errors.Is(err, ErrBadDrawing) || !strings.Contains(err.Error(), "60") {
		t.Errorf("err = %v, want ErrBadDrawing naming the 60 s limit", err)
	}
}

func TestMissingDrawingFileKeepsFsError(t *testing.T) {
	f := newFixture(t)
	_, err := f.r.Entities(context.Background(), filepath.Join(filepath.Dir(f.dwg), "gone.dwg"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want a not-exist error", err)
	}
	if f.fake.count() != 0 {
		t.Error("Run must not be called for a missing file")
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	if errors.Is(ErrUnavailable, ErrBadDrawing) || errors.Is(ErrBadDrawing, ErrUnavailable) {
		t.Error("the two errors must be distinct")
	}
}

// keyFor is the cache key the reader uses for the fixture drawing.
func keyFor(t *testing.T, f *fixture) string {
	t.Helper()
	st, err := os.Stat(f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	return cacheKey(f.dwg, st, f.bin)
}

func TestRunSurvivesCallerLeaving(t *testing.T) {
	for _, kind := range []string{"entities", "layers"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			started := make(chan struct{})
			release := make(chan struct{})
			var seenErr atomic.Value // error the runner saw on its context after the caller left
			f.r.Run = func(ctx context.Context, o runx.Options, name string, args ...string) (runx.Result, error) {
				close(started)
				<-release
				if err := ctx.Err(); err != nil {
					seenErr.Store(err)
					return runx.Result{ExitCode: -1}, err
				}
				if o.Timeout != Timeout {
					t.Errorf("Timeout = %v, want %v", o.Timeout, Timeout)
				}
				return runx.Result{Stdout: []byte(`{"layers":[]}` + "\n")}, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				if kind == "entities" {
					_, _ = f.r.Entities(ctx, f.dwg)
				} else {
					_, _ = f.r.Layers(ctx, f.dwg)
				}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("Run never started")
			}
			cancel() // the caller leaves while chitralipi is running
			close(release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the call did not return")
			}
			if err, _ := seenErr.Load().(error); err != nil {
				t.Fatalf("the run was cancelled with the caller: %v", err)
			}
			want := keyFor(t, f) + ".jsonl"
			if kind == "layers" {
				want = keyFor(t, f) + ".layers.json"
			}
			if st, err := os.Stat(filepath.Join(f.cache, want)); err != nil || st.Size() == 0 {
				t.Errorf("the finished run did not fill the cache: %v", err)
			}
		})
	}
}

func TestCancelledBeforeStartDoesNotRun(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.r.Entities(ctx, f.dwg); !errors.Is(err, context.Canceled) {
		t.Errorf("Entities: err = %v, want context.Canceled", err)
	}
	if _, err := f.r.Layers(ctx, f.dwg); !errors.Is(err, context.Canceled) {
		t.Errorf("Layers: err = %v, want context.Canceled", err)
	}
	if n := f.fake.count(); n != 0 {
		t.Errorf("Run called %d times for a caller that was already gone, want 0", n)
	}
}

func TestLayersAreCached(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.fake.reply = func(args []string) (runx.Result, error) {
		return runx.Result{Stdout: []byte(`{"layers":["0","WALLS"]}`)}, nil
	}
	first, err := f.r.Layers(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.r.Layers(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != `{"layers":["0","WALLS"]}` || string(second) != string(first) {
		t.Errorf("layers = %q then %q", first, second)
	}
	if n := f.fake.count(); n != 1 {
		t.Fatalf("Run called %d times for two Layers calls, want 1", n)
	}
	file := filepath.Join(f.cache, keyFor(t, f)+".layers.json")
	st, err := os.Stat(file)
	if err != nil {
		t.Fatalf("no layers cache file: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("layers file mode = %o, want 600", st.Mode().Perm())
	}
	ds, err := os.Stat(f.cache)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", ds.Mode().Perm())
	}
	// A changed drawing is a new key: chitralipi runs again.
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(f.dwg, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Layers(ctx, f.dwg); err != nil {
		t.Fatal(err)
	}
	if n := f.fake.count(); n != 2 {
		t.Errorf("Run called %d times after the drawing changed, want 2", n)
	}
	// Entities and layers of one drawing do not share a file.
	if _, err := f.r.Entities(ctx, f.dwg); err != nil {
		t.Fatal(err)
	}
	if n := f.fake.count(); n != 3 {
		t.Errorf("Run called %d times after Entities, want 3", n)
	}
	// No temp files are left behind.
	left, _ := filepath.Glob(filepath.Join(f.cache, ".tmp-*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestConcurrentLayersRunOnce(t *testing.T) {
	f := newFixture(t)
	f.fake.delay = 50 * time.Millisecond
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.r.Layers(context.Background(), f.dwg); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := f.fake.count(); n != 1 {
		t.Errorf("Run called %d times for 4 concurrent Layers callers, want 1", n)
	}
}

func TestLayersAndEntitiesRunAtTheSameTime(t *testing.T) {
	f := newFixture(t)
	var running, peak atomic.Int32
	f.fake.reply = func(args []string) (runx.Result, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		running.Add(-1)
		return runx.Result{Stdout: []byte("{}\n")}, nil
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = f.r.Layers(context.Background(), f.dwg) }()
	go func() { defer wg.Done(); _, _ = f.r.Entities(context.Background(), f.dwg) }()
	wg.Wait()
	if peak.Load() != 2 {
		t.Errorf("layers and entities of one drawing ran %d at a time, want 2", peak.Load())
	}
}

func TestEmptyCacheFileIsAMiss(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := os.MkdirAll(f.cache, 0o700); err != nil {
		t.Fatal(err)
	}
	key := keyFor(t, f)
	for _, name := range []string{key + ".jsonl", key + ".layers.json"} {
		if err := os.WriteFile(filepath.Join(f.cache, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := f.r.Entities(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	if n := f.fake.count(); n != 1 {
		t.Fatalf("Run called %d times for an empty entities file, want 1", n)
	}
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		t.Errorf("the empty entities file was not replaced: %v", err)
	}
	out, err := f.r.Layers(ctx, f.dwg)
	if err != nil {
		t.Fatal(err)
	}
	if n := f.fake.count(); n != 2 {
		t.Fatalf("Run called %d times after an empty layers file, want 2", n)
	}
	if string(out) != `{"layers":[]}` {
		t.Errorf("layers = %q", out)
	}
	if st, err := os.Stat(filepath.Join(f.cache, key+".layers.json")); err != nil || st.Size() == 0 {
		t.Errorf("the empty layers file was not replaced: %v", err)
	}
}

func TestPruneDeletesOldTempFiles(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.cache, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(f.cache, ".tmp-1111")
	recent := filepath.Join(f.cache, ".tmp-2222")
	other := filepath.Join(f.cache, ".keep-3333") // not a temp file of ours
	for _, p := range []string{old, recent, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	twoHours := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, twoHours, twoHours); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, twoHours, twoHours); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Entities(context.Background(), f.dwg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("a temp file older than 1 hour should be gone (err=%v)", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("a fresh temp file may belong to a running write and must stay: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a file that is not .tmp-* must stay: %v", err)
	}
}

func TestCacheLimitCountsBothKinds(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.cache, 0o700); err != nil {
		t.Fatal(err)
	}
	// 105 old entity files, then 100 newer layer files: 205, the oldest first.
	base := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 205; i++ {
		name := fmt.Sprintf("e%03d.jsonl", i)
		if i >= 105 {
			name = fmt.Sprintf("l%03d.layers.json", i)
		}
		p := filepath.Join(f.cache, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.r.Layers(context.Background(), f.dwg); err != nil {
		t.Fatal(err)
	}
	a, _ := filepath.Glob(filepath.Join(f.cache, "*.jsonl"))
	b, _ := filepath.Glob(filepath.Join(f.cache, "*.layers.json"))
	if len(a)+len(b) != 200 {
		t.Fatalf("%d cache files (%d entity, %d layer), want 200 in all", len(a)+len(b), len(a), len(b))
	}
	for i := 0; i < 6; i++ { // 205 old + 1 new = 206, minus the 6 oldest
		if _, err := os.Stat(filepath.Join(f.cache, fmt.Sprintf("e%03d.jsonl", i))); !os.IsNotExist(err) {
			t.Errorf("e%03d should be gone (err=%v)", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.cache, "e006.jsonl")); err != nil {
		t.Errorf("e006 should stay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.cache, keyFor(t, f)+".layers.json")); err != nil {
		t.Errorf("the layers file just written was pruned: %v", err)
	}
}
