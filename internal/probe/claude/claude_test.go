package claude

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

const fxRoot = "/fx/projects"

var fxProjects = []probe.Project{
	{ID: "personal/alpha", Path: fxRoot + "/personal/alpha"},
	{ID: "personal/beta", Path: fxRoot + "/personal/beta"},
	{ID: "personal/site_v2", Path: fxRoot + "/personal/site_v2"},
	{ID: "personal/site-v2", Path: fxRoot + "/personal/site-v2"},
	{ID: "work/acme_auto", Path: fxRoot + "/work/acme_auto"},
}

func newTestScanner() *Scanner {
	s := NewScanner(fxProjects)
	s.Home = "/fx/home"
	return s
}

// copyFixtures copies testdata/claude into a temp dir and returns the profile path.
func copyFixtures(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "testdata", "claude")
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dst, "profile-a")
}

func findFile(t *testing.T, files []File, id string) File {
	t.Helper()
	for _, f := range files {
		if f.SessionID == id {
			return f
		}
	}
	t.Fatalf("file %s not found", id)
	return File{}
}

func TestEncode(t *testing.T) {
	got := Encode("/Users/p/projects/personal/site_v2/.claude/x")
	want := "-Users-p-projects-personal-site-v2--claude-x"
	if got != want {
		t.Fatalf("Encode = %q, want %q", got, want)
	}
}

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"ghp_FAKEFAKEFAKEFAKEFAKE1234 x":    "[redacted] x",
		"key sk-test-FAKE0000000000":        "key [redacted]",
		"id AKIAFAKEFAKEFAKE1234.":          "id [redacted].",
		"my password=hunter2 ok":            "my [redacted] ok",
		"API_KEY: abc":                      "[redacted]",
		"Token = zzz":                       "[redacted]",
		"-----BEGIN RSA PRIVATE KEY-----":   "[redacted]",
		"plain words stay":                  "plain words stay",
		"github_pat_FAKE_FAKE_FAKE_FAKE_12": "[redacted]",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRealText(t *testing.T) {
	cases := map[string]string{
		"hello": "hello",
		"<system-reminder>x</system-reminder> real words":                      "real words",
		"<command-name>/model</command-name><command-args>opus</command-args>": "opus",
		"<command-name>/clear</command-name><command-args></command-args>":     "",
		"Caveat: local command output":                                         "",
		"[Request interrupted by user]":                                        "",
		"<local-command-stdout>out</local-command-stdout>":                     "",
		"<unknown-tag>keep?</unknown-tag>":                                     "",
		"a <b>bold</b> word":                                                   "a <b>bold</b> word",
	}
	for in, want := range cases {
		if got := realText(in); got != want {
			t.Errorf("realText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := clean("a\n\n  b  "+strings.Repeat("x", 100), 10, ""); got != "a b xxxxxx…" {
		t.Errorf("clean = %q", got)
	}
}

func TestDiscoverAndGroup(t *testing.T) {
	prof := copyFixtures(t)
	files, err := Discover([]string{prof, filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 {
		t.Fatalf("got %d files, want 6", len(files))
	}
	for i := 1; i < len(files); i++ {
		if files[i].MTime.After(files[i-1].MTime) {
			t.Fatal("files not newest first")
		}
	}
	sub := findFile(t, files, "agent-a1")
	if !sub.IsSubagent || sub.ParentID != "s-sub" || sub.Owner() != "s-sub" || sub.Profile != "profile-a" {
		t.Fatalf("bad subagent file: %+v", sub)
	}
	if sub.Inode == 0 {
		t.Fatal("inode not read")
	}
	if want := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(sub.Path))), "s-sub.jsonl"); sub.ParentPath() != want {
		t.Fatalf("ParentPath = %s", sub.ParentPath())
	}
	fams := Group(files)
	if len(fams) != 5 {
		t.Fatalf("got %d families, want 5", len(fams))
	}
	for _, fam := range fams {
		if fam.ID == "s-sub" {
			if len(fam.Files) != 2 || fam.Files[0].IsSubagent || !fam.Files[1].IsSubagent {
				t.Fatalf("bad family: %+v", fam)
			}
		}
	}
	st, err := Stat(sub.Path, "profile-a")
	if err != nil || !st.IsSubagent || st.ParentID != "s-sub" {
		t.Fatalf("Stat = %+v, %v", st, err)
	}
}

func parseOne(t *testing.T, s *Scanner, files []File, id string) Result {
	t.Helper()
	r, err := s.Parse(context.Background(), findFile(t, files, id), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseFixtures(t *testing.T) {
	prof := copyFixtures(t)
	files, _ := Discover([]string{prof})
	s := newTestScanner()

	// Subfolder cwd, mentions, secrets, corrupt and unknown lines.
	r := parseOne(t, s, files, "s-sub")
	ss := r.Session
	if ss.ProjectID != "personal/alpha" || ss.CWD != fxRoot+"/personal/alpha/web" {
		t.Fatalf("project = %q cwd = %q", ss.ProjectID, ss.CWD)
	}
	if r.Mode != Full || r.Skipped != 1 || r.Unknown != 1 {
		t.Fatalf("mode %v skipped %d unknown %d", r.Mode, r.Skipped, r.Unknown)
	}
	if ss.Turns != 5 {
		t.Errorf("turns = %d, want 5", ss.Turns)
	}
	if !reflect.DeepEqual(ss.Tools, map[string]int{"Read": 1, "Bash": 1, "Edit": 2}) {
		t.Errorf("tools = %v", ss.Tools)
	}
	if !reflect.DeepEqual(ss.Edited, []string{"web/app.js"}) {
		t.Errorf("edited = %v", ss.Edited)
	}
	if !reflect.DeepEqual(r.Mentions, map[string]int{"personal/beta": 3}) {
		t.Errorf("mentions = %v", r.Mentions)
	}
	if !strings.HasPrefix(ss.Title, "Fix the login button, use [redacted] and [redacted] please") ||
		strings.Contains(ss.Title, "ghp_") || strings.Contains(ss.Title, "abc123") {
		t.Errorf("title = %q", ss.Title)
	}
	if n := len([]rune(ss.Title)); n != TitleMax+1 || !strings.HasSuffix(ss.Title, "…") {
		t.Errorf("title length %d: %q", n, ss.Title)
	}
	if !ss.Start.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) || !ss.End.Equal(time.Date(2026, 9, 1, 10, 5, 0, 0, time.UTC)) {
		t.Errorf("start %v end %v", ss.Start, ss.End)
	}
	if r.State.Offset != findFile(t, files, "s-sub").Size || ss.KB != 0 || ss.ID != "s-sub" || ss.Profile != "profile-a" {
		t.Errorf("state %+v session %+v", r.State, ss)
	}

	// Worktree cwd rolls up to the project.
	if got := parseOne(t, s, files, "s-wt").Session.ProjectID; got != "work/acme_auto" {
		t.Errorf("worktree project = %q", got)
	}
	// No cwd, folder matches two projects (site_v2 vs site-v2): unassigned.
	if got := parseOne(t, s, files, "s-amb").Session; got.ProjectID != "" || got.CWD != "" || got.Turns != 2 {
		t.Errorf("ambiguous: %+v", got)
	}
	// cwd decides exactly, and the other spelling is a mention.
	r = parseOne(t, s, files, "s-pv2")
	if r.Session.ProjectID != "personal/site_v2" || r.Mentions["personal/site-v2"] != 1 {
		t.Errorf("pv2: %+v %v", r.Session, r.Mentions)
	}
	// No cwd, fallback by folder name with a subfolder.
	r = parseOne(t, s, files, "s-nocwd")
	if r.Session.ProjectID != "personal/beta" || !reflect.DeepEqual(r.Session.Edited, []string{"docs/index.md"}) {
		t.Errorf("nocwd: %+v", r.Session)
	}
	if r.Session.Title != "Write the docs index" {
		t.Errorf("nocwd title = %q", r.Session.Title)
	}
}

func TestParseFamilySubagent(t *testing.T) {
	prof := copyFixtures(t)
	files, _ := Discover([]string{prof})
	s := newTestScanner()
	var fam Family
	for _, f := range Group(files) {
		if f.ID == "s-sub" {
			fam = f
		}
	}
	ctx := context.Background()
	fr, err := s.ParseFamily(ctx, fam, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ss := fr.Session
	if !fr.Changed || !fr.Rebuilt || ss.Subagents != 1 || ss.Turns != 5 {
		t.Fatalf("family: %+v", fr)
	}
	if ss.Tools["Write"] != 1 || ss.Tools["Grep"] != 1 || ss.Tools["Edit"] != 2 {
		t.Errorf("tools = %v", ss.Tools)
	}
	if !reflect.DeepEqual(ss.Edited, []string{"web/app.js", "web/helper.js"}) {
		t.Errorf("edited = %v", ss.Edited)
	}
	if !reflect.DeepEqual(fr.Mentions, map[string]int{"personal/beta": 3, "work/acme_auto": 1}) {
		t.Errorf("mentions = %v", fr.Mentions)
	}
	if !ss.End.Equal(time.Date(2026, 9, 1, 10, 6, 0, 0, time.UTC)) || ss.Title == "" || ss.JSONLPath != fam.ParentPath {
		t.Errorf("session = %+v", ss)
	}
	if len(fr.States) != 2 {
		t.Fatalf("states = %v", fr.States)
	}

	// Nothing changed: every file is skipped, the row stays the same.
	fr2, err := s.ParseFamily(ctx, fam, &ss, fr.States)
	if err != nil || fr2.Changed || fr2.Rebuilt || !reflect.DeepEqual(fr2.Session, ss) {
		t.Fatalf("skip pass: %+v %v", fr2, err)
	}

	// The subagent grows: only its new line is added.
	subPath := fam.Files[1].Path
	appendLine(t, subPath, `{"cwd":"/fx/projects/personal/alpha/web","type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"y3","name":"Grep","input":{"path":"/fx/projects/work/acme_auto"}}]},"timestamp":"2026-09-01T10:07:00.000Z"}`+"\n")
	fam.Files[1] = mustStat(t, subPath)
	fr3, err := s.ParseFamily(ctx, fam, &ss, fr.States)
	if err != nil || fr3.Rebuilt || fr3.Session.Tools["Grep"] != 2 || fr3.Session.Subagents != 1 || fr3.Mentions["work/acme_auto"] != 2 {
		t.Fatalf("append pass: %+v %v", fr3, err)
	}

	// The subagent shrinks: the family is rebuilt, not double counted.
	if err := os.WriteFile(subPath, []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Grep","input":{}}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fam.Files[1] = mustStat(t, subPath)
	s3 := fr3.Session
	fr4, err := s.ParseFamily(ctx, fam, &s3, fr3.States)
	if err != nil || !fr4.Rebuilt || fr4.Session.Tools["Grep"] != 1 || fr4.Session.Tools["Edit"] != 2 || fr4.Session.Subagents != 1 {
		t.Fatalf("rebuild pass: %+v %v", fr4, err)
	}
}

func appendLine(t *testing.T, path, s string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(s); err != nil {
		t.Fatal(err)
	}
	fh.Close()
	// Make sure mtime moves even on coarse clocks.
	later := time.Now().Add(time.Duration(len(s)) * time.Millisecond)
	os.Chtimes(path, later, later)
}

func mustStat(t *testing.T, path string) File {
	t.Helper()
	f, err := Stat(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func userLine(text, ts string) string {
	return fmt.Sprintf(`{"cwd":"/fx/projects/personal/alpha","type":"user","message":{"role":"user","content":%q},"timestamp":%q}`+"\n", text, ts)
}

func TestIncremental(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "-fx-projects-personal-alpha")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "s-inc.jsonl")
	partial := `{"cwd":"/fx/projects/personal/alpha","type":"user","message":{"role":"user","content":"third`
	body := userLine("first question", "2026-09-01T10:00:00Z") +
		`{"type":"wormhole"}` + "\n" +
		userLine("second question", "2026-09-01T10:01:00Z") + partial
	os.WriteFile(path, []byte(body), 0o644)
	s := newTestScanner()
	ctx := context.Background()

	f := mustStat(t, path)
	r, err := s.Parse(ctx, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Session.Turns != 2 || r.Unknown != 1 || r.Skipped != 0 {
		t.Fatalf("first pass: %+v", r)
	}
	if want := int64(len(body) - len(partial)); r.State.Offset != want {
		t.Fatalf("offset = %d, want %d (stop before the partial line)", r.State.Offset, want)
	}

	// Same file: Skip.
	if m := NeedsParse(&r.State, f); m != Skip {
		t.Fatalf("mode = %v, want skip", m)
	}
	r2, _ := s.Parse(ctx, f, &r.Session, &r.State)
	if r2.Mode != Skip || r2.Session.Turns != 2 {
		t.Fatalf("skip: %+v", r2)
	}

	// The partial line is completed and one more line is added: Append parses only those.
	appendLine(t, path, ` one"},"timestamp":"2026-09-01T10:02:00Z"}`+"\n"+userLine("fourth", "2026-09-01T10:03:00Z"))
	f = mustStat(t, path)
	if m := NeedsParse(&r.State, f); m != Append {
		t.Fatalf("mode = %v, want append", m)
	}
	r3, err := s.Parse(ctx, f, &r.Session, &r.State)
	if err != nil {
		t.Fatal(err)
	}
	if r3.Mode != Append || r3.Session.Turns != 4 || r3.Unknown != 0 || r3.State.Offset != f.Size {
		t.Fatalf("append: %+v", r3)
	}
	if r3.Session.Title != "first question" || !r3.Session.End.Equal(time.Date(2026, 9, 1, 10, 3, 0, 0, time.UTC)) {
		t.Fatalf("append session: %+v", r3.Session)
	}
	if r.Session.Turns != 2 {
		t.Fatal("prev was modified")
	}

	// Truncated: Full.
	short := userLine("only one", "2026-09-02T10:00:00Z")
	os.WriteFile(path, []byte(short), 0o644)
	f = mustStat(t, path)
	if m := NeedsParse(&r3.State, f); m != Full {
		t.Fatalf("mode = %v, want full", m)
	}
	r4, _ := s.Parse(ctx, f, &r3.Session, &r3.State)
	if r4.Mode != Full || r4.Session.Turns != 1 || r4.Session.Title != "only one" {
		t.Fatalf("full: %+v", r4)
	}

	// Replaced by a new file (new inode), even if it is bigger: Full.
	tmp := path + ".new"
	os.WriteFile(tmp, []byte(short+userLine("two", "2026-09-02T10:01:00Z")), 0o644)
	os.Rename(tmp, path)
	f = mustStat(t, path)
	if f.Inode == r4.State.Inode {
		t.Skip("file system reused the inode")
	}
	if m := NeedsParse(&r4.State, f); m != Full {
		t.Fatalf("mode = %v, want full after replace", m)
	}
}

func TestTooLongLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "-fx-projects-personal-alpha")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "s-long.jsonl")
	huge := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("a", MaxLine+10) + `"}}` + "\n"
	os.WriteFile(path, []byte(userLine("before", "2026-09-01T10:00:00Z")+huge+userLine("after", "2026-09-01T10:01:00Z")), 0o644)
	r, err := newTestScanner().Parse(context.Background(), mustStat(t, path), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Skipped != 1 || r.Session.Turns != 2 || r.State.Offset != r.State.Size {
		t.Fatalf("long line: skipped %d turns %d", r.Skipped, r.Session.Turns)
	}
}

func TestTranscript(t *testing.T) {
	prof := copyFixtures(t)
	files, _ := Discover([]string{prof})
	path := findFile(t, files, "s-sub").Path
	ctx := context.Background()

	all, err := Transcript(ctx, path, 0, PageMax, nil)
	if err != nil || !all.Done {
		t.Fatalf("all: %+v %v", all, err)
	}
	var roles []string
	for _, tr := range all.Turns {
		roles = append(roles, tr.Role)
	}
	want := []string{"user", "claude", "tool", "tool", "tool", "tool", "tool", "tool", "tool", "tool", "user", "claude"}
	if !reflect.DeepEqual(roles, want) {
		t.Fatalf("roles = %v", roles)
	}
	for i, tr := range all.Turns {
		if i > 0 && tr.I <= all.Turns[i-1].I {
			t.Fatal("turn ids not increasing")
		}
		for _, bad := range []string{"ghp_", "hunter2", "sk-test", "AKIAFAKE", "hush-hush", "abc123"} {
			if strings.Contains(tr.Text, bad) {
				t.Errorf("turn %d leaks %q: %q", i, bad, tr.Text)
			}
		}
	}
	if all.Turns[2].Tool != "Read" || all.Turns[3].Tool != "Bash" || all.Turns[4].Tool != "" || all.Turns[5].Text != "login.js\nmain.js" {
		t.Errorf("tool turns: %+v", all.Turns[2:6])
	}

	// Page through with a small limit and compare.
	var paged []Turn
	off := int64(0)
	for i := 0; ; i++ {
		p, err := Transcript(ctx, path, off, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, p.Turns...)
		if p.Done {
			break
		}
		if p.Next <= off || i > 50 {
			t.Fatal("paging does not advance")
		}
		off = p.Next
	}
	if !reflect.DeepEqual(paged, all.Turns) {
		t.Fatalf("paged turns differ: %d vs %d", len(paged), len(all.Turns))
	}

	// Role filter.
	u, _ := Transcript(ctx, path, 0, 0, []string{"user"})
	if len(u.Turns) != 2 || u.Turns[0].Role != "user" {
		t.Fatalf("user filter: %+v", u.Turns)
	}
	// Long tool output is cut to 2 KB.
	long := filepath.Join(filepath.Dir(path), "s-long.jsonl")
	os.WriteFile(long, []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"z","content":"`+strings.Repeat("y", 5000)+`"}]}}`+"\n"), 0o644)
	lp, _ := Transcript(ctx, long, 0, 0, nil)
	if len(lp.Turns) != 1 || len(lp.Turns[0].Text) > ToolTextMax+len("…") {
		t.Fatalf("tool output not cut: %d", len(lp.Turns[0].Text))
	}
	// Offset in the middle of a line.
	if _, err := Transcript(ctx, path, 5, 10, nil); err != ErrBadOffset {
		t.Fatalf("bad offset err = %v", err)
	}
}

func TestUsers(t *testing.T) {
	prof := copyFixtures(t)
	files, _ := Discover([]string{prof})
	got := Users(context.Background(), findFile(t, files, "s-sub").Path)
	if len(got) != 2 || !strings.HasPrefix(got[0], "Fix the login button") || strings.Contains(got[1], "hunter2") {
		t.Fatalf("Users = %q", got)
	}
}

func TestCheckPath(t *testing.T) {
	prof := copyFixtures(t)
	files, _ := Discover([]string{prof})
	p := findFile(t, files, "s-wt").Path
	if _, err := CheckPath([]string{prof}, p); err != nil {
		t.Fatalf("good path: %v", err)
	}
	other := filepath.Join(t.TempDir(), "x.jsonl")
	os.WriteFile(other, []byte("{}\n"), 0o644)
	if _, err := CheckPath([]string{prof}, other); err != ErrNotTranscript {
		t.Fatalf("outside path: %v", err)
	}
	if _, err := CheckPath([]string{prof}, filepath.Join(prof, "projects", "..", "..", "etc")); err != ErrNotTranscript {
		t.Fatalf("non-jsonl: %v", err)
	}
}

func TestProjectFor(t *testing.T) {
	s := newTestScanner()
	cases := map[string]string{
		fxRoot + "/personal/alpha":     "personal/alpha",
		fxRoot + "/personal/alpha/x/y": "personal/alpha",
		fxRoot + "/personal/alphabet":  "",
		fxRoot + "/personal/site-v2":   "personal/site-v2",
		"/fx/home":                     "",
	}
	for in, want := range cases {
		if got := s.ProjectFor(in); got != want {
			t.Errorf("ProjectFor(%q) = %q, want %q", in, got, want)
		}
	}
}
