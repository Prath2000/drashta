package resolve

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/store"
)

// The JSON views below are what the API sends for tracker items. Field names follow
// docs/spec/04-data-model.md section 6 and web/app/lib/types.ts.

// Task is the API view of a task.
type Task struct {
	ID             string   `json:"id"`
	ProjectID      string   `json:"projectId"`
	Ref            string   `json:"ref"` // "<project>:task:T-1"
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	FeatureID      *string  `json:"featureId"`
	MilestoneID    *string  `json:"milestoneId"`
	Assignee       string   `json:"assignee"`
	Created        string   `json:"created"`
	CreatedBy      string   `json:"createdBy,omitempty"`
	Updated        string   `json:"updated,omitempty"`
	Body           string   `json:"body"`
	Links          []string `json:"links"`   // manual links as written in the file
	Unlinks        []string `json:"unlinks"` // auto links the user rejected
	Rev            string   `json:"rev"`
	LatestSnapshot *SnapRef `json:"latestSnapshot"`
	LinkCount      int      `json:"linkCount"`
	OpenNotes      int      `json:"openNotes"`
}

// SnapRef is the short form of a task's latest snapshot.
type SnapRef struct {
	Version int    `json:"version"`
	Verdict string `json:"verdict"`
}

// Feature is the API view of a feature. Summary is the body (the web client reads summary).
type Feature struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"projectId"`
	Ref         string  `json:"ref"`
	Title       string  `json:"title"`
	Summary     string  `json:"summary"`
	MilestoneID *string `json:"milestoneId"`
	Created     string  `json:"created,omitempty"`
	Rev         string  `json:"rev"`
}

// Milestone is the API view of a milestone.
type Milestone struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"projectId"`
	Ref        string   `json:"ref"`
	Title      string   `json:"title"`
	Due        *string  `json:"due"`
	FeatureIDs []string `json:"featureIds"`
	State      string   `json:"state"`
	Created    string   `json:"created,omitempty"`
	Body       string   `json:"body"`
	Rev        string   `json:"rev"`
}

// Snapshot is the API view of one snapshot version.
type Snapshot struct {
	ID         string  `json:"id"` // "T-1/v3"
	Ref        string  `json:"ref"`
	ProjectID  string  `json:"projectId"`
	TaskID     string  `json:"taskId"`
	Version    int     `json:"version"`
	Title      string  `json:"title"`
	URL        string  `json:"url"`  // raw image URL (/api/projects/{p}/raw?path=...)
	Path       string  `json:"path"` // image path relative to the project root
	Source     string  `json:"source,omitempty"`
	Captured   string  `json:"captured"`
	Author     string  `json:"author"`
	Commit     *string `json:"commit"`
	Verdict    string  `json:"verdict"`
	ReviewedBy string  `json:"reviewedBy,omitempty"`
	ReviewedAt string  `json:"reviewedAt,omitempty"`
	ReviewNote string  `json:"reviewNote"`
	Body       string  `json:"body"`
	Latest     bool    `json:"latest"`
	Rev        string  `json:"rev"`
	Notes      []Note  `json:"notes"`
}

// Note is the API view of a note. Ref is the full ref of the item the note is on.
type Note struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"projectId"`
	Ref         string   `json:"ref"`
	Text        string   `json:"text"`
	Author      string   `json:"author"`
	Date        string   `json:"date"` // the created time
	Resolved    bool     `json:"resolved"`
	ResolvedBy  string   `json:"resolvedBy,omitempty"`
	ResolvedAt  string   `json:"resolvedAt,omitempty"`
	Line        *int     `json:"line,omitempty"`
	X           *float64 `json:"x,omitempty"`
	Y           *float64 `json:"y,omitempty"`
	Anchor      string   `json:"anchor,omitempty"`
	ReplyTo     string   `json:"replyTo,omitempty"`
	Rev         string   `json:"rev"`
	TargetTitle string   `json:"targetTitle,omitempty"`
}

// LogEntry is one line of a task log.
type LogEntry struct {
	TS     string `json:"ts"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// Link is one effective link of a task (manual from the file, auto from the index).
type Link struct {
	Ref    string `json:"ref"` // as stored: local or "<project>:..."
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"`
}

// Board is one project's tracker, loaded from its .drashta files, plus the auto links
// from the index. It turns authored items into API views.
type Board struct {
	ProjectID string
	Root      string
	Tracker   authored.Tracker
	Auto      map[string][]store.TaskLink // task id -> auto links

	featMilestone map[string]string
}

// NewBoard builds a Board. auto may be nil.
func NewBoard(project, root string, tr authored.Tracker, auto map[string][]store.TaskLink) *Board {
	b := &Board{ProjectID: project, Root: root, Tracker: tr, Auto: auto, featMilestone: map[string]string{}}
	if b.Auto == nil {
		b.Auto = map[string][]store.TaskLink{}
	}
	for _, f := range tr.Features {
		if f.Milestone != "" {
			b.featMilestone[f.ID] = f.Milestone
		}
	}
	// The milestone file wins on conflict (04 section 2.3).
	for _, m := range tr.Milestones {
		for _, f := range m.Features {
			b.featMilestone[f] = m.ID
		}
	}
	return b
}

// FullRef qualifies a ref string stored in this project: a local ref gets the project id.
// A string that does not parse is returned as it is.
func (b *Board) FullRef(s string) string {
	r, err := ParseRef(s)
	if err != nil {
		return s
	}
	if r.Project == "" && r.Type != authored.RefSession && r.Type != authored.RefServer &&
		r.Type != authored.RefServerKey && r.Type != authored.RefProject {
		r.Project = b.ProjectID
	}
	return r.String()
}

// Task finds a task by id.
func (b *Board) Task(id string) (authored.Task, bool) {
	for _, t := range b.Tracker.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return authored.Task{}, false
}

// Feature finds a feature by id.
func (b *Board) Feature(id string) (authored.Feature, bool) {
	for _, f := range b.Tracker.Features {
		if f.ID == id {
			return f, true
		}
	}
	return authored.Feature{}, false
}

// Milestone finds a milestone by id.
func (b *Board) Milestone(id string) (authored.Milestone, bool) {
	for _, m := range b.Tracker.Milestones {
		if m.ID == id {
			return m, true
		}
	}
	return authored.Milestone{}, false
}

// Snapshot finds a snapshot by id ("T-1/v3").
func (b *Board) Snapshot(id string) (authored.Snapshot, bool) {
	for _, s := range b.Tracker.Snapshots {
		if s.ID == id {
			return s, true
		}
	}
	return authored.Snapshot{}, false
}

// Note finds a note by id.
func (b *Board) Note(id string) (authored.Note, bool) {
	for _, n := range b.Tracker.Notes {
		if n.ID == id {
			return n, true
		}
	}
	return authored.Note{}, false
}

// SnapshotsOf returns the snapshots of a task, oldest version first.
func (b *Board) SnapshotsOf(taskID string) []authored.Snapshot {
	out := []authored.Snapshot{}
	for _, s := range b.Tracker.Snapshots {
		if s.Task == taskID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

// Latest returns the newest snapshot of a task.
func (b *Board) Latest(taskID string) (authored.Snapshot, bool) {
	ss := b.SnapshotsOf(taskID)
	if len(ss) == 0 {
		return authored.Snapshot{}, false
	}
	return ss[len(ss)-1], true
}

// Links returns the effective links of a task: the manual links of the file first, then
// the auto links of the index that are neither manual nor in unlinks.
func (b *Board) Links(t authored.Task) []Link {
	out := []Link{}
	seen := map[string]bool{}
	for _, l := range t.Links {
		k := b.linkKey(l)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, Link{Ref: l, Source: "manual"})
	}
	un := map[string]bool{}
	for _, u := range t.Unlinks {
		un[b.linkKey(u)] = true
	}
	for _, a := range b.Auto[t.ID] {
		k := b.linkKey(a.Ref)
		if seen[k] || un[k] {
			continue
		}
		seen[k] = true
		out = append(out, Link{Ref: a.Ref, Source: "auto", Reason: a.Reason})
	}
	return out
}

// HasAuto reports whether ref is an auto link of the task (unlinked or not).
func (b *Board) HasAuto(taskID, ref string) bool {
	k := b.linkKey(ref)
	for _, a := range b.Auto[taskID] {
		if b.linkKey(a.Ref) == k {
			return true
		}
	}
	return false
}

// SameLink reports whether two stored ref strings point at the same item (fragments count).
func (b *Board) SameLink(x, y string) bool { return b.linkKey(x) == b.linkKey(y) }

func (b *Board) linkKey(s string) string {
	r, err := ParseRef(s)
	if err != nil {
		return strings.TrimSpace(s)
	}
	if r.Project == b.ProjectID {
		r.Project = ""
	}
	return r.Canonical()
}

func (b *Board) notesOn(match func(authored.Ref) bool) []authored.Note {
	out := []authored.Note{}
	for _, n := range b.Tracker.Notes {
		r, err := ParseRef(n.Ref)
		if err != nil {
			continue
		}
		if r.Project == "" {
			r.Project = b.ProjectID
		}
		if match(r) {
			out = append(out, n)
		}
	}
	return out
}

// NotesOn returns the notes of this project that point at ref (fragments ignored).
func (b *Board) NotesOn(ref authored.Ref) []authored.Note {
	return b.notesOn(func(r authored.Ref) bool { return SameItem(r, ref) })
}

func (b *Board) openNotesOfTask(taskID string) int {
	n := 0
	for _, note := range b.notesOn(func(r authored.Ref) bool {
		if r.Project != b.ProjectID {
			return false
		}
		if r.Type == authored.RefTask && r.ID == taskID {
			return true
		}
		return r.Type == authored.RefSnapshot && strings.HasPrefix(r.ID, taskID+"/")
	}) {
		if !note.Resolved {
			n++
		}
	}
	return n
}

// TaskView builds the API view of a task.
func (b *Board) TaskView(t authored.Task) Task {
	v := Task{
		ID: t.ID, ProjectID: b.ProjectID, Ref: b.ProjectID + ":task:" + t.ID, Title: t.Title, Status: t.Status,
		Assignee: t.Assignee, Created: t.Created, CreatedBy: t.CreatedBy, Updated: t.Updated, Body: t.Body,
		Links: nonNil(t.Links), Unlinks: nonNil(t.Unlinks), Rev: t.Rev,
	}
	if t.Feature != "" {
		f := t.Feature
		v.FeatureID = &f
		if m, ok := b.featMilestone[f]; ok {
			v.MilestoneID = &m
		}
	}
	if s, ok := b.Latest(t.ID); ok {
		v.LatestSnapshot = &SnapRef{Version: s.Version, Verdict: s.Verdict}
	}
	v.LinkCount = len(b.Links(t))
	v.OpenNotes = b.openNotesOfTask(t.ID)
	return v
}

// FeatureView builds the API view of a feature.
func (b *Board) FeatureView(f authored.Feature) Feature {
	v := Feature{ID: f.ID, ProjectID: b.ProjectID, Ref: b.ProjectID + ":feature:" + f.ID, Title: f.Title,
		Summary: f.Body, Created: f.Created, Rev: f.Rev}
	if m, ok := b.featMilestone[f.ID]; ok {
		v.MilestoneID = &m
	}
	return v
}

// MilestoneView builds the API view of a milestone.
func (b *Board) MilestoneView(m authored.Milestone) Milestone {
	v := Milestone{ID: m.ID, ProjectID: b.ProjectID, Ref: b.ProjectID + ":milestone:" + m.ID, Title: m.Title,
		FeatureIDs: nonNil(m.Features), State: m.State, Created: m.Created, Body: m.Body, Rev: m.Rev}
	if m.Due != "" {
		d := m.Due
		v.Due = &d
	}
	return v
}

// SnapshotView builds the API view of a snapshot, with its notes.
func (b *Board) SnapshotView(s authored.Snapshot) Snapshot {
	rel := ""
	if s.Image != "" {
		rel = s.ImageRel()
	}
	v := Snapshot{
		ID: s.ID, Ref: b.ProjectID + ":snapshot:" + s.ID, ProjectID: b.ProjectID, TaskID: s.Task,
		Version: s.Version, Title: s.Title, Path: rel, Source: s.Source, Captured: s.Captured, Author: s.Author,
		Verdict: s.Verdict, ReviewedBy: s.ReviewedBy, ReviewedAt: s.ReviewedAt, ReviewNote: s.ReviewNote,
		Body: s.Body, Rev: s.Rev, Notes: []Note{},
	}
	if rel != "" {
		v.URL = RawURL(b.ProjectID, rel)
	}
	if s.Commit != "" {
		c := s.Commit
		v.Commit = &c
	}
	if l, ok := b.Latest(s.Task); ok && l.Version == s.Version {
		v.Latest = true
	}
	ref := authored.Ref{Project: b.ProjectID, Type: authored.RefSnapshot, ID: s.ID}
	for _, n := range b.NotesOn(ref) {
		v.Notes = append(v.Notes, b.NoteView(n))
	}
	return v
}

// NoteView builds the API view of a note.
func (b *Board) NoteView(n authored.Note) Note {
	return Note{
		ID: n.ID, ProjectID: b.ProjectID, Ref: b.FullRef(n.Ref), Text: n.Body, Author: n.Author, Date: n.Created,
		Resolved: n.Resolved, ResolvedBy: n.ResolvedBy, ResolvedAt: n.ResolvedAt, Line: n.Line, X: n.X, Y: n.Y,
		Anchor: n.Anchor, ReplyTo: n.ReplyTo, Rev: n.Rev,
	}
}

// LogView turns a task log into API entries.
func LogView(t authored.Task) []LogEntry {
	out := make([]LogEntry, 0, len(t.Log))
	for _, l := range t.Log {
		out = append(out, LogEntry{TS: l.Time, Author: l.By, Text: l.Text})
	}
	return out
}

// RawURL is the URL of the raw-file endpoint for one project file.
func RawURL(project, rel string) string {
	return "/api/projects/" + url.PathEscape(project) + "/raw?path=" + url.QueryEscape(rel)
}

// isoTime turns a front-matter time text into RFC 3339. Unknown text is returned as it is.
func isoTime(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, ok := authored.ParseTime(s); ok {
		v := t.Format(time.RFC3339)
		return &v
	}
	return &s
}

func isoOf(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	v := t.Format(time.RFC3339)
	return &v
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func itoa(n int) string { return strconv.Itoa(n) }
