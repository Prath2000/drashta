package drawing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/runx"
)

// ErrUnavailable means chitralipi is not available (not installed, or not set up).
var ErrUnavailable = errors.New("drawing: chitralipi is not available")

// ErrBadDrawing means chitralipi ran but could not read the file.
var ErrBadDrawing = errors.New("drawing: chitralipi could not read the file")

const (
	// MaxEntitiesBytes caps the stdout of `chitralipi entities`. A drawing with 146,143
	// entities is 44 MB over all spaces; its model space alone is about 3 MB.
	MaxEntitiesBytes = 128 << 20
	// Timeout is how long one chitralipi run may take.
	Timeout = 60 * time.Second

	maxLayersBytes = 8 << 20
	maxCacheFiles  = 200
	maxDetailRunes = 200

	entitiesExt = ".jsonl"       // cache file of `chitralipi entities`: <key>.jsonl
	layersExt   = ".layers.json" // cache file of `chitralipi layers`: <key>.layers.json
	tempPrefix  = ".tmp-"        // writeAtomic temp files
	staleTemp   = time.Hour      // a temp file older than this is a leftover of a crashed write
)

// Reader runs chitralipi on DWG files. It is safe for concurrent use.
type Reader struct {
	Bin      string // the chitralipi program (config.Chitralipi)
	CacheDir string // where the answers are kept: <key>.jsonl (entities) and <key>.layers.json (layers)

	// Run runs a program. The default is runx.Run (argv only, no shell). Tests replace it.
	Run func(ctx context.Context, o runx.Options, name string, args ...string) (runx.Result, error)

	mu       sync.Mutex
	inflight map[string]*sync.Mutex // one chitralipi run per cache key at a time
}

// New returns a Reader for the chitralipi program at bin. Entities and layers are cached in cacheDir.
func New(bin, cacheDir string) *Reader {
	return &Reader{Bin: bin, CacheDir: cacheDir, Run: runx.Run, inflight: map[string]*sync.Mutex{}}
}

// Layers returns the stdout of `chitralipi layers <abs> --as json`. It is cached next to the
// entities (<key>.layers.json), so a drawing is read for its layers only once per version.
func (r *Reader) Layers(ctx context.Context, abs string) ([]byte, error) {
	file, fresh, err := r.cachedRun(ctx, abs, layersExt, runx.Options{Timeout: Timeout, MaxOut: maxLayersBytes},
		"layers", abs, "--as", "json")
	if err != nil {
		return nil, err
	}
	if fresh != nil {
		return fresh, nil
	}
	return os.ReadFile(file)
}

// Entities returns the path of a cached JSONL file with the model-space entities of abs. The
// first call for a file (or for a changed file) runs chitralipi; later calls only find the file.
func (r *Reader) Entities(ctx context.Context, abs string) (string, error) {
	file, _, err := r.cachedRun(ctx, abs, entitiesExt, runx.Options{Timeout: Timeout, MaxOut: MaxEntitiesBytes},
		"entities", abs, "--space", "model", "--as", "jsonl")
	return file, err
}

// cachedRun finds the cache file of one kind of answer (ext) for abs, or runs chitralipi with
// args to make it. It returns the file path, and the stdout when it just ran (nil for a hit).
//
// A run belongs to the drawing, not to the caller: it is detached from ctx, so a caller that
// leaves (a closed tab) does not kill chitralipi half way and waste the work. The run still ends
// after Timeout, and the next caller finds the file.
func (r *Reader) cachedRun(ctx context.Context, abs, ext string, o runx.Options, args ...string) (string, []byte, error) {
	bin, err := r.program()
	if err != nil {
		return "", nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	key := cacheKey(abs, st, bin)
	file := filepath.Join(r.CacheDir, key+ext)
	if cacheHit(file) {
		return file, nil, nil
	}

	// One run per key and kind: later callers wait here, then find the file. Layers and
	// entities of one drawing may run at the same time.
	unlock := r.lock(key + ext)
	defer unlock()
	if cacheHit(file) {
		return file, nil, nil
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err // gone before we started: do not begin a long run for nobody
	}

	res, err := r.run(ctx, o, args...)
	if err != nil {
		return "", nil, err
	}
	if err := writeAtomic(r.CacheDir, file, res.Stdout); err != nil {
		return "", nil, err
	}
	pruneCache(r.CacheDir, maxCacheFiles)
	return file, res.Stdout, nil
}

// run runs chitralipi with args and turns the outcomes into the package errors.
func (r *Reader) run(ctx context.Context, o runx.Options, args ...string) (runx.Result, error) {
	run := r.Run
	if run == nil {
		run = runx.Run
	}
	// The run does not end with the caller (see cachedRun); o.Timeout still ends it.
	res, err := run(context.WithoutCancel(ctx), o, r.Bin, args...)
	switch {
	case errors.Is(err, runx.ErrNotFound):
		return res, r.notFound()
	case err != nil && ctx.Err() != nil:
		return res, ctx.Err() // the caller left; not the drawing's fault
	case errors.Is(err, context.DeadlineExceeded):
		return res, fmt.Errorf("%w: it took longer than %d s", ErrBadDrawing, int(Timeout/time.Second))
	case err != nil:
		return res, fmt.Errorf("%w: %s", ErrBadDrawing, clip(err.Error()))
	case res.ExitCode != 0:
		detail := firstLine(res.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", res.ExitCode)
		}
		return res, fmt.Errorf("%w: %s", ErrBadDrawing, detail)
	case res.Truncated:
		return res, fmt.Errorf("%w: the drawing is too large to show", ErrBadDrawing)
	}
	return res, nil
}

// program checks that the chitralipi program exists and returns its path. A name without a
// folder is looked up on PATH.
func (r *Reader) program() (string, error) {
	bin := r.Bin
	if bin == "" {
		return "", r.notFound()
	}
	if !strings.ContainsRune(bin, filepath.Separator) {
		p, err := exec.LookPath(bin)
		if err != nil {
			return "", r.notFound()
		}
		bin = p
	}
	st, err := os.Stat(bin)
	if err != nil || st.IsDir() {
		return "", r.notFound()
	}
	return bin, nil
}

func (r *Reader) notFound() error {
	name := r.Bin
	if name == "" {
		name = "chitralipi"
	}
	return fmt.Errorf("%w: %s not found; set chitralipi in config.toml", ErrUnavailable, name)
}

// lock takes the run lock of one cache key and returns the function that releases it.
func (r *Reader) lock(key string) func() {
	r.mu.Lock()
	if r.inflight == nil {
		r.inflight = map[string]*sync.Mutex{}
	}
	m, ok := r.inflight[key]
	if !ok {
		m = &sync.Mutex{}
		r.inflight[key] = m
	}
	r.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// cacheKey names the cached answer for one version of a drawing read by one version of the
// program: a changed drawing or an upgraded chitralipi gives a new key.
func cacheKey(abs string, st fs.FileInfo, bin string) string {
	var binSize, binMtime int64
	if bs, err := os.Stat(bin); err == nil {
		binSize, binMtime = bs.Size(), bs.ModTime().UnixNano()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d|%d", abs, st.Size(), st.ModTime().UnixNano(), binSize, binMtime)))
	return hex.EncodeToString(sum[:])
}

// cacheHit is true for a cache file that holds something. A 0-byte file (a crash or a full
// disk after the rename) is a miss: it is written again.
func cacheHit(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// writeAtomic writes data to dst (mode 0600) through a temp file and a rename, so a reader
// never sees half a file. The folder is made with mode 0700.
func writeAtomic(dir, dst string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, dst); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// pruneCache keeps the newest max cache files of dir (entities and layers together) and deletes
// the rest. It also deletes temp files that a crashed write left more than an hour ago. It is
// best effort.
func pruneCache(dir string, max int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type aged struct {
		path string
		mt   time.Time
	}
	var files []aged
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		isTemp := strings.HasPrefix(name, tempPrefix)
		if !isTemp && !strings.HasSuffix(name, entitiesExt) && !strings.HasSuffix(name, layersExt) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if isTemp {
			if time.Since(info.ModTime()) > staleTemp {
				_ = os.Remove(filepath.Join(dir, name))
			}
			continue
		}
		files = append(files, aged{filepath.Join(dir, name), info.ModTime()})
	}
	if len(files) <= max {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mt.Before(files[j].mt) })
	for _, f := range files[:len(files)-max] {
		_ = os.Remove(f.path)
	}
}

// firstLine is the first non-empty line of stderr, cut to maxDetailRunes characters.
func firstLine(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return clip(line)
		}
	}
	return ""
}

func clip(s string) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	if utf8.RuneCountInString(s) <= maxDetailRunes {
		return s
	}
	return string([]rune(s)[:maxDetailRunes])
}
