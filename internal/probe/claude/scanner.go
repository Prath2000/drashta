package claude

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// TitleMax is the longest title in runes (89 + "…").
const TitleMax = 89

// EditedMax caps Session.Edited.
const EditedMax = 200

// Session is the summary of one Claude Code session (table sessions).
// Full transcript text is never kept here: only the title.
type Session struct {
	ID        string         `json:"id"`
	Profile   string         `json:"profile"`
	JSONLPath string         `json:"jsonlPath"`
	CWD       string         `json:"cwd"`
	ProjectID string         `json:"projectId"` // "" = unassigned
	Start     time.Time      `json:"start"`
	End       time.Time      `json:"end"`
	Title     string         `json:"title"` // "" when no real user text yet; the UI shows "claude session"
	Turns     int            `json:"turns"`
	KB        int            `json:"kb"` // all files of the session (main + subagents)
	Tools     map[string]int `json:"tools"`
	Edited    []string       `json:"edited"` // relative to the project when inside it, else absolute with "~/"
	Size      int64          `json:"size"`   // main .jsonl size (jsonl_size)
	MTime     time.Time      `json:"mtime"`  // main .jsonl mtime (jsonl_mtime)
	Mentions  map[string]int `json:"mentions,omitempty"`
	Subagents int            `json:"subagents,omitempty"` // subagent files counted in
	// LastRole is the role of the last counted turn of the main file
	// ("user", "claude" or "tool"). An Append pass starts from it, so a run of
	// tool calls split across two passes still counts as one turn.
	LastRole string `json:"lastRole,omitempty"`
}

// Result is what Parse returns for one file.
type Result struct {
	Session  Session
	State    State
	Mentions map[string]int // projectID -> count, other projects only (cumulative, same as Session.Mentions)
	Skipped  int            // corrupt or too-long lines in this pass
	Unknown  int            // records with an unknown type in this pass
	Mode     Mode
}

type proj struct {
	id, path, enc string
}

// Scanner resolves sessions to projects and parses transcript files.
// It is safe for concurrent use after NewScanner returns.
type Scanner struct {
	// Home is the user's home folder. It expands "~/" in text and shortens
	// paths in titles. NewScanner fills it from os.UserHomeDir; tests set it.
	Home  string
	projs []proj // longest path first
}

// NewScanner builds a Scanner for the known projects.
func NewScanner(projects []probe.Project) *Scanner {
	s := &Scanner{}
	s.Home, _ = os.UserHomeDir()
	for _, p := range projects {
		if p.Path == "" {
			continue
		}
		pp := filepath.Clean(p.Path)
		s.projs = append(s.projs, proj{id: p.ID, path: pp, enc: Encode(pp)})
	}
	sort.SliceStable(s.projs, func(i, j int) bool { return len(s.projs[i].path) > len(s.projs[j].path) })
	return s
}

// ProjectFor returns the ID of the project whose path is the longest prefix of
// abs (on a path boundary), or "".
func (s *Scanner) ProjectFor(abs string) string {
	if abs == "" || abs[0] != '/' {
		return ""
	}
	for _, p := range s.projs { // longest first, so the first hit wins
		if len(abs) >= len(p.path) && strings.HasPrefix(abs, p.path) &&
			(len(abs) == len(p.path) || abs[len(p.path)] == '/') {
			return p.id
		}
	}
	return ""
}

// ProjectForFolder is the fallback for a file with no cwd: the project whose
// encoded path equals the folder name or is a "-"-boundary prefix of it; the
// longest wins. Two different projects with the same best encoding (for
// example site_v2 and site-v2) are ambiguous and give "".
func (s *Scanner) ProjectForFolder(folder string) string {
	best, bestLen, tie := "", 0, false
	for _, p := range s.projs {
		if folder == p.enc || strings.HasPrefix(folder, p.enc+"-") {
			switch {
			case len(p.enc) > bestLen:
				best, bestLen, tie = p.id, len(p.enc), false
			case len(p.enc) == bestLen && p.id != best:
				tie = true
			}
		}
	}
	if tie {
		return ""
	}
	return best
}

func (s *Scanner) projectPath(id string) string {
	for _, p := range s.projs {
		if p.id == id {
			return p.path
		}
	}
	return ""
}

// Parse reads one file. The mode comes from NeedsParse(st, f):
//   - Skip: prev and st are returned unchanged (prev must not be nil).
//   - Append: only complete lines after st.Offset are read and merged into a
//     copy of prev (turns, tools, edited, mentions, end).
//   - Full (or prev == nil): the file is read from 0 and prev is ignored.
//
// A subagent file gives a Session with ID = its parent session; in Full mode
// it holds only that file's own counts. Use ParseFamily to keep a session and
// its subagents in one row. prev and st are never modified.
func (s *Scanner) Parse(ctx context.Context, f File, prev *Session, st *State) (Result, error) {
	mode := NeedsParse(st, f)
	if prev == nil {
		mode = Full
	}
	switch mode {
	case Skip:
		ss := cloneSession(prev)
		return Result{Session: *ss, State: *st, Mentions: ss.Mentions, Mode: Skip}, nil
	case Append:
		return s.parseInto(ctx, f, prev, st.Offset, Append)
	}
	return s.parseInto(ctx, f, nil, 0, Full)
}

// FamilyResult is what ParseFamily returns for one session.
type FamilyResult struct {
	Session  Session
	States   map[string]State // per file path, for every file of the family
	Mentions map[string]int
	Skipped  int
	Unknown  int
	Changed  bool // false when every file was skipped
	Rebuilt  bool // true when the session was parsed again from 0
}

// ParseFamily parses a session's main file and its subagent files into one
// Session. prev is the stored row (nil if none) and states holds the stored
// State per file path. When any already-known file needs a Full parse (or a
// known file is gone), the whole family is parsed again from 0, so subagent
// counts are never added twice. A new subagent file is just added.
func (s *Scanner) ParseFamily(ctx context.Context, fam Family, prev *Session, states map[string]State) (FamilyResult, error) {
	out := FamilyResult{States: map[string]State{}}
	rebuild := prev == nil
	present := map[string]bool{}
	for _, f := range fam.Files {
		present[f.Path] = true
		st, ok := states[f.Path]
		if !ok {
			if !f.IsSubagent {
				rebuild = true
			}
			continue
		}
		if NeedsParse(&st, f) == Full {
			rebuild = true
		}
	}
	for p := range states {
		if !present[p] {
			rebuild = true
		}
	}
	var cur *Session
	if !rebuild {
		cur = prev
	}
	for _, f := range fam.Files {
		st, ok := states[f.Path]
		var off int64
		mode := Full
		if !rebuild && ok {
			mode = NeedsParse(&st, f)
			if mode == Skip {
				out.States[f.Path] = st
				continue
			}
			off = st.Offset
		}
		r, err := s.parseInto(ctx, f, cur, off, mode)
		if err != nil {
			if ctx.Err() != nil {
				return out, err
			}
			if os.IsNotExist(err) { // vanished between Discover and now
				continue
			}
			return out, err
		}
		out.Changed = true
		out.Skipped += r.Skipped
		out.Unknown += r.Unknown
		out.States[f.Path] = r.State
		ss := r.Session
		cur = &ss
	}
	if cur == nil {
		if prev != nil {
			cur = cloneSession(prev)
		} else {
			cur = &Session{ID: fam.ID, Profile: fam.Profile, JSONLPath: fam.ParentPath}
		}
	}
	var total int64
	for _, f := range fam.Files {
		total += f.Size
	}
	cur.KB = int(total / 1024)
	out.Session = *cur
	out.Mentions = cur.Mentions
	out.Rebuilt = rebuild
	return out, nil
}

func cloneSession(p *Session) *Session {
	c := *p
	c.Tools = make(map[string]int, len(p.Tools))
	for k, v := range p.Tools {
		c.Tools[k] = v
	}
	c.Mentions = make(map[string]int, len(p.Mentions))
	for k, v := range p.Mentions {
		c.Mentions[k] = v
	}
	c.Edited = append([]string(nil), p.Edited...)
	return &c
}

var editTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// pass holds the running state of one parse.
type pass struct {
	s        *Scanner
	sess     *Session
	sub      bool
	needCWD  bool
	lastRole string
	edited   map[string]bool
	skipped  int
	unknown  int
}

// parseInto parses f from off and merges the counts into a copy of base
// (base nil = a fresh session).
func (s *Scanner) parseInto(ctx context.Context, f File, base *Session, off int64, mode Mode) (Result, error) {
	var sess *Session
	if base != nil {
		sess = cloneSession(base)
	} else {
		sess = &Session{ID: f.Owner(), Profile: f.Profile, JSONLPath: f.ParentPath(),
			Tools: map[string]int{}, Mentions: map[string]int{}}
	}
	if !f.IsSubagent {
		sess.Size, sess.MTime = f.Size, f.MTime
		sess.Profile = f.Profile
	}
	p := &pass{s: s, sess: sess, sub: f.IsSubagent, needCWD: sess.CWD == "", edited: map[string]bool{}}
	if !f.IsSubagent && off > 0 {
		p.lastRole = sess.LastRole // continue the run where the last pass stopped
	}
	for _, e := range sess.Edited {
		p.edited[e] = true
	}

	fh, lr, err := openAt(f.Path, off, f.Size)
	if err != nil {
		return Result{}, err
	}
	defer fh.Close()
	n := 0
	for {
		line, _, tooLong, err := lr.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, err
		}
		if n++; n&1023 == 0 && ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if tooLong {
			p.skipped++
			continue
		}
		p.line(line)
	}
	// A subagent file is counted once, on its Full parse. An Append from
	// offset 0 (the file was empty the first time) must not count it again.
	if f.IsSubagent && mode == Full {
		sess.Subagents++
	}
	if !f.IsSubagent {
		sess.LastRole = p.lastRole
	}
	p.finish(f)
	return Result{
		Session:  *sess,
		State:    State{Size: f.Size, MTime: f.MTime, Offset: lr.off, Inode: f.Inode},
		Mentions: sess.Mentions,
		Skipped:  p.skipped,
		Unknown:  p.unknown,
		Mode:     mode,
	}, nil
}

func (p *pass) line(line []byte) {
	// Trim blanks; an empty line is not an error.
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	line = line[i:]
	if len(line) == 0 {
		return
	}
	if line[0] != '{' {
		p.skipped++
		return
	}
	if !p.needCWD && isToolResultLine(line) {
		if line[len(line)-1] != '}' {
			p.skipped++
			return
		}
		p.stamp(rawTimestamp(line))
		return
	}
	var r record
	if !decode(line, &r) {
		p.skipped++
		return
	}
	if !knownTypes[r.Type] {
		p.unknown++
	}
	if p.needCWD && r.CWD != "" && r.CWD[0] == '/' {
		p.sess.CWD = filepath.Clean(r.CWD)
		p.sess.ProjectID = p.s.ProjectFor(p.sess.CWD)
		p.needCWD = false
	}
	if r.Type != "user" && r.Type != "assistant" {
		return
	}
	p.stamp(r.Timestamp)
	if r.IsMeta || r.Message == nil {
		return
	}
	c := r.Message.Content
	switch r.Type {
	case "user":
		if r.Message.Role != "user" {
			return
		}
		if c.IsStr {
			p.userText(c.Str)
			return
		}
		for _, b := range c.Blocks {
			if b.Type == "text" {
				p.userText(b.Text)
			}
		}
	case "assistant":
		for _, b := range c.Blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" && !p.sub {
					p.sess.Turns++
					p.lastRole = "claude"
				}
			case "tool_use":
				if !p.sub && p.lastRole != "tool" {
					p.sess.Turns++
				}
				p.lastRole = "tool"
				name := b.Name
				if name == "" {
					name = "?"
				}
				p.sess.Tools[name]++
				fp := b.Input.filePath()
				if editTools[name] && fp != "" {
					p.edited[fp] = true
				}
				p.mention(b.Input.FilePath)
				p.mention(b.Input.NotebookPath)
				p.mention(b.Input.Path)
				if b.Input.Command != "" {
					absPaths(b.Input.Command, p.s.Home, p.mention)
				}
			}
		}
	}
}

func (p *pass) userText(raw string) {
	t := realText(raw)
	if t == "" {
		return
	}
	absPaths(t, p.s.Home, p.mention)
	if p.sub { // a subagent's prompt is written by Claude, not the user
		return
	}
	p.sess.Turns++
	p.lastRole = "user"
	if p.sess.Title == "" {
		p.sess.Title = clean(t, TitleMax, p.s.Home)
	}
}

func (p *pass) mention(path string) {
	if path == "" || path[0] != '/' {
		return
	}
	id := p.s.ProjectFor(path)
	if id == "" || (id == p.sess.ProjectID && p.sess.ProjectID != "") {
		return
	}
	p.sess.Mentions[id]++
}

func (p *pass) stamp(ts string) {
	if ts == "" {
		return
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return
	}
	if p.sess.Start.IsZero() || t.Before(p.sess.Start) {
		p.sess.Start = t
	}
	if t.After(p.sess.End) {
		p.sess.End = t
	}
}

// finish resolves the project (fallback by folder name), drops mentions of
// the session's own project and makes edited paths relative.
func (p *pass) finish(f File) {
	s := p.sess
	if s.CWD == "" && s.ProjectID == "" {
		s.ProjectID = p.s.ProjectForFolder(f.folder())
	}
	if s.ProjectID != "" {
		delete(s.Mentions, s.ProjectID)
	}
	root := p.s.projectPath(s.ProjectID)
	out := make([]string, 0, len(p.edited))
	seen := map[string]bool{}
	for e := range p.edited {
		if strings.HasPrefix(e, "/") {
			switch {
			case root != "" && strings.HasPrefix(e, root+"/"):
				e = e[len(root)+1:]
			case p.s.Home != "" && strings.HasPrefix(e, p.s.Home+"/"):
				e = "~/" + e[len(p.s.Home)+1:]
			}
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	if len(out) > EditedMax {
		out = out[:EditedMax]
	}
	s.Edited = out
}
