package authored

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// pngReader returns a reader with some bytes (the content is not inspected).
func pngReader() io.Reader { return bytes.NewReader([]byte("\x89PNG\r\n\x1a\nfake")) }

// zeroReader gives n zero bytes.
type zeroReader struct{ n int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.n {
		p = p[:z.n]
	}
	for i := range p {
		p[i] = 0
	}
	z.n -= int64(len(p))
	return len(p), nil
}

func TestAddSnapshotImageVersionsAndPath(t *testing.T) {
	root := t.TempDir()
	v, rel, err := AddSnapshotImage(root, "T-12", pngReader(), "png")
	if err != nil || v != 1 || rel != ".drashta/snapshots/T-12/v1.png" {
		t.Fatalf("%d %q %v", v, rel, err)
	}
	if got := readFile(t, filepath.Join(root, rel)); !strings.HasPrefix(got, "\x89PNG") {
		t.Errorf("image bytes not stored")
	}
	// Extension: dot, case and jpeg are fine.
	for i, ext := range []string{".JPG", "WebP", " gif ", "jpeg"} {
		v, rel, err := AddSnapshotImage(root, "T-12", pngReader(), ext)
		if err != nil || v != i+2 {
			t.Fatalf("ext %q: %d %q %v", ext, v, rel, err)
		}
		if want := strings.ToLower(strings.Trim(ext, ". ")); !strings.HasSuffix(rel, "v"+string(rune('0'+v))+"."+want) {
			t.Errorf("ext %q gave %q", ext, rel)
		}
	}
	// A version taken by a metadata file alone still counts.
	writeFile(t, dotPath(root, "snapshots", "T-12", "v9.md"), "---\ntask: T-12\nversion: 9\n---\n")
	if v, _, err := AddSnapshotImage(root, "T-12", pngReader(), "png"); err != nil || v != 10 {
		t.Errorf("version after v9.md: %d %v", v, err)
	}
	// Another task starts at 1.
	if v, _, err := AddSnapshotImage(root, "T-13", pngReader(), "png"); err != nil || v != 1 {
		t.Errorf("T-13: %d %v", v, err)
	}
	// No temp files are left behind.
	entries, _ := os.ReadDir(dotPath(root, "snapshots", "T-12"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestAddSnapshotImageNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	writeFile(t, dotPath(root, "snapshots", "T-1", "v1.png"), "old")
	writeFile(t, dotPath(root, "snapshots", "T-1", "v2.jpg"), "old2")
	v, rel, err := AddSnapshotImage(root, "T-1", pngReader(), "png")
	if err != nil || v != 3 {
		t.Fatalf("%d %q %v", v, rel, err)
	}
	if readFile(t, dotPath(root, "snapshots", "T-1", "v1.png")) != "old" || readFile(t, dotPath(root, "snapshots", "T-1", "v2.jpg")) != "old2" {
		t.Errorf("an old image was changed")
	}
}

func TestAddSnapshotImageRace(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[int]bool{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ext := []string{"png", "jpg", "webp", "gif"}[i%4]
			v, _, err := AddSnapshotImage(root, "T-1", pngReader(), ext)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			if got[v] {
				t.Errorf("version %d given twice", v)
			}
			got[v] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if len(got) != 20 {
		t.Errorf("%d versions", len(got))
	}
}

func TestAddSnapshotImageRejects(t *testing.T) {
	root := t.TempDir()
	for _, ext := range []string{"exe", "svg", "html", "", "png.exe", "../png", "tiff", "pdf"} {
		if _, _, err := AddSnapshotImage(root, "T-1", pngReader(), ext); !errors.Is(err, ErrBadExt) {
			t.Errorf("ext %q: want ErrBadExt, got %v", ext, err)
		}
	}
	for _, id := range []string{"../x", "T-1/../x", "T-1/..", "..", "", "T-", "T-1x", "t-1", "T-1\n", "/etc", "T-1/v1", "inbox", "T-1;rm"} {
		if _, _, err := AddSnapshotImage(root, id, pngReader(), "png"); !errors.Is(err, ErrInvalid) {
			t.Errorf("task id %q: want ErrInvalid, got %v", id, err)
		}
	}
	if _, _, err := AddSnapshotImage(root, "T-1", bytes.NewReader(nil), "png"); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty image: %v", err)
	}
	// Nothing was written for any rejected call.
	if entries, _ := os.ReadDir(dotPath(root, "snapshots")); len(entries) > 1 {
		for _, e := range entries {
			t.Logf("left: %s", e.Name())
		}
	}
	if entries, err := os.ReadDir(dotPath(root, "snapshots", "T-1")); err == nil && len(entries) != 0 {
		t.Errorf("rejected calls left %d files", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err == nil {
		t.Errorf("traversal wrote outside .drashta")
	}
}

func TestAddSnapshotImageSizeCap(t *testing.T) {
	root := t.TempDir()
	// Exactly the cap is fine.
	if v, _, err := AddSnapshotImage(root, "T-1", &zeroReader{n: MaxImageBytes}, "png"); err != nil || v != 1 {
		t.Fatalf("20 MB exactly: %d %v", v, err)
	}
	// One byte more is not.
	_, _, err := AddSnapshotImage(root, "T-1", &zeroReader{n: MaxImageBytes + 1}, "png")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	entries, _ := os.ReadDir(dotPath(root, "snapshots", "T-1"))
	if len(entries) != 1 {
		t.Errorf("a too large image left files: %d entries", len(entries))
	}
	// A reader of unknown (huge) size is cut at the cap, not read to the end.
	r := &zeroReader{n: 1 << 40}
	if _, _, err := AddSnapshotImage(root, "T-1", r, "png"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("huge: %v", err)
	}
	if read := (1 << 40) - r.n; read > MaxImageBytes+1<<20 {
		t.Errorf("read %d bytes of a huge image", read)
	}
}

func TestAddSnapshotImageRefusesSymlinkedTaskFolder(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(dotPath(root, "snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dotPath(root, "snapshots", "T-1")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddSnapshotImage(root, "T-1", pngReader(), "png"); err == nil {
		t.Fatal("must refuse a task folder that points outside the project")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("files were written outside the project: %d", len(entries))
	}
}

func TestEnsureGitignore(t *testing.T) {
	root := t.TempDir()
	if err := EnsureGitignore(root); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dotPath(root, ".gitignore"))
	for _, want := range []string{"snapshots/**/*.png\n", "snapshots/**/*.jpg\n", "snapshots/**/*.jpeg\n", "snapshots/**/*.webp\n", "snapshots/**/*.gif\n"} {
		if !strings.Contains(got, "\n"+want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "snapshots/**/*.md") || strings.Contains(got, "\n*") {
		t.Errorf("the text files must stay committed:\n%s", got)
	}
	// An existing file (edited by the user) is not touched.
	writeFile(t, dotPath(root, ".gitignore"), "# mine\n")
	if err := EnsureGitignore(root); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dotPath(root, ".gitignore")); got != "# mine\n" {
		t.Errorf("an existing .gitignore was changed: %q", got)
	}
}

func TestEnsureLayout(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"milestones", "features", "tasks", "snapshots", "notes"} {
		if fi, err := os.Stat(dotPath(root, d)); err != nil || !fi.IsDir() {
			t.Errorf("missing folder %s", d)
		}
	}
	readme := readFile(t, dotPath(root, "README.md"))
	if !strings.Contains(readme, "never \"done\"") || !strings.Contains(readme, "drashta task new") {
		t.Errorf("README:\n%s", readme)
	}
	writeFile(t, dotPath(root, "README.md"), "mine")
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	if readFile(t, dotPath(root, "README.md")) != "mine" {
		t.Errorf("README was overwritten")
	}
	if err := EnsureLayout(filepath.Join(root, "missing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project folder: %v", err)
	}
}
