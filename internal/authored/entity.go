package authored

import (
	"path"
	"strconv"
	"strings"
)

// entity is what load, save and delete need from the five item types.
type entity interface {
	kind() string
	// fileID is the id that names the file: "T-12", or "T-1/v3" for a snapshot.
	fileID() string
	// setFileID sets the identity fields from a file id (used when the file name wins).
	setFileID(id string) error
	// rel is the file path below .drashta, with forward slashes.
	rel() string
	fields() []field
	bodyPtr() *string
	logPtr() *[]LogEntry // only tasks have a log
	setRev(string)
	getRev() string
	// normalize fills defaults and checks values. It must pass before a write.
	normalize() error
}

func newEntity(kind string) entity {
	switch kind {
	case KindMilestone:
		return &Milestone{}
	case KindFeature:
		return &Feature{}
	case KindTask:
		return &Task{}
	case KindSnapshot:
		return &Snapshot{}
	case KindNote:
		return &Note{}
	}
	return nil
}

// toEntity accepts a pointer or a value of the five types.
func toEntity(item any) (entity, error) {
	switch v := item.(type) {
	case *Milestone:
		return v, nil
	case Milestone:
		return &v, nil
	case *Feature:
		return v, nil
	case Feature:
		return &v, nil
	case *Task:
		return v, nil
	case Task:
		return &v, nil
	case *Snapshot:
		return v, nil
	case Snapshot:
		return &v, nil
	case *Note:
		return v, nil
	case Note:
		return &v, nil
	}
	return nil, invalid("cannot save a %T", item)
}

func relFor(kind, id string) string {
	if kind == KindSnapshot {
		return path.Join(kindDir[kind], id+".md") // id is "T-1/v3"
	}
	return path.Join(kindDir[kind], id+".md")
}

func checkTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return invalid("title is empty")
	}
	if strings.ContainsAny(title, "\r\n") {
		return invalid("title must be one line")
	}
	return nil
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// ---- Milestone ----

func (m *Milestone) kind() string              { return KindMilestone }
func (m *Milestone) fileID() string            { return m.ID }
func (m *Milestone) setFileID(id string) error { m.ID = id; return nil }
func (m *Milestone) rel() string               { return relFor(KindMilestone, m.ID) }
func (m *Milestone) bodyPtr() *string          { return &m.Body }
func (m *Milestone) logPtr() *[]LogEntry       { return nil }
func (m *Milestone) setRev(r string)           { m.Rev = r }
func (m *Milestone) getRev() string            { return m.Rev }
func (m *Milestone) fields() []field {
	return []field{
		strF("id", &m.ID, false),
		strF("title", &m.Title, false),
		timeF("due", &m.Due, true),
		listF("features", &m.Features, true),
		strF("state", &m.State, false),
		timeF("created", &m.Created, true),
	}
}
func (m *Milestone) normalize() error {
	if err := checkID(KindMilestone, m.ID); err != nil {
		return err
	}
	if err := checkTitle(m.Title); err != nil {
		return err
	}
	if m.State == "" {
		m.State = StateOpen
	}
	if !oneOf(m.State, StateOpen, StateClosed) {
		return invalid("milestone state %q (want open or closed)", m.State)
	}
	if m.Features == nil {
		m.Features = []string{}
	}
	return nil
}

// ---- Feature ----

func (f *Feature) kind() string              { return KindFeature }
func (f *Feature) fileID() string            { return f.ID }
func (f *Feature) setFileID(id string) error { f.ID = id; return nil }
func (f *Feature) rel() string               { return relFor(KindFeature, f.ID) }
func (f *Feature) bodyPtr() *string          { return &f.Body }
func (f *Feature) logPtr() *[]LogEntry       { return nil }
func (f *Feature) setRev(r string)           { f.Rev = r }
func (f *Feature) getRev() string            { return f.Rev }
func (f *Feature) fields() []field {
	return []field{
		strF("id", &f.ID, false),
		strF("title", &f.Title, false),
		strF("milestone", &f.Milestone, true),
		timeF("created", &f.Created, true),
	}
}
func (f *Feature) normalize() error {
	if err := checkID(KindFeature, f.ID); err != nil {
		return err
	}
	if err := checkTitle(f.Title); err != nil {
		return err
	}
	if f.Milestone != "" {
		if err := checkID(KindMilestone, f.Milestone); err != nil {
			return err
		}
	}
	return nil
}

// ---- Task ----

func (t *Task) kind() string              { return KindTask }
func (t *Task) fileID() string            { return t.ID }
func (t *Task) setFileID(id string) error { t.ID = id; return nil }
func (t *Task) rel() string               { return relFor(KindTask, t.ID) }
func (t *Task) bodyPtr() *string          { return &t.Body }
func (t *Task) logPtr() *[]LogEntry       { return &t.Log }
func (t *Task) setRev(r string)           { t.Rev = r }
func (t *Task) getRev() string            { return t.Rev }
func (t *Task) fields() []field {
	return []field{
		strF("id", &t.ID, false),
		strF("title", &t.Title, false),
		strF("status", &t.Status, false),
		strF("feature", &t.Feature, true),
		strF("assignee", &t.Assignee, true),
		timeF("created", &t.Created, true),
		strF("created_by", &t.CreatedBy, true),
		timeF("updated", &t.Updated, true),
		listF("links", &t.Links, false),
		listF("unlinks", &t.Unlinks, false),
	}
}
func (t *Task) normalize() error {
	if err := checkID(KindTask, t.ID); err != nil {
		return err
	}
	if err := checkTitle(t.Title); err != nil {
		return err
	}
	if t.Status == "" {
		t.Status = StatusTodo
	}
	if !oneOf(t.Status, Statuses...) {
		return invalid("task status %q (want one of %s)", t.Status, strings.Join(Statuses, ", "))
	}
	if t.Feature != "" {
		if err := checkID(KindFeature, t.Feature); err != nil {
			return err
		}
	}
	if t.Links == nil {
		t.Links = []string{}
	}
	if t.Unlinks == nil {
		t.Unlinks = []string{}
	}
	if t.Log == nil {
		t.Log = []LogEntry{}
	}
	if _, _, found := splitLog(t.Body); found {
		return invalid("a task body must not contain a line that is just \"## Log\"")
	}
	for _, e := range t.Log {
		if strings.ContainsAny(e.Time+e.By+e.Text+e.Raw, "\r\n") {
			return invalid("a log entry must be one line")
		}
		if e.Raw == "" && (strings.ContainsAny(e.Time, " \t") || strings.ContainsAny(e.By, " \t")) {
			return invalid("log time and author must be one word")
		}
	}
	return nil
}

// ---- Snapshot ----

// SnapshotID builds the id "T-1/v3".
func SnapshotID(task string, version int) string { return task + "/v" + strconv.Itoa(version) }

// ImageRel is the image path relative to the project root, for example
// .drashta/snapshots/T-1/v3.png. It is empty when the snapshot has no image.
func (s *Snapshot) ImageRel() string {
	if s.Image == "" || s.Task == "" {
		return ""
	}
	return path.Join(".drashta", "snapshots", s.Task, s.Image)
}

func (s *Snapshot) kind() string        { return KindSnapshot }
func (s *Snapshot) fileID() string      { return SnapshotID(s.Task, s.Version) }
func (s *Snapshot) rel() string         { return relFor(KindSnapshot, s.fileID()) }
func (s *Snapshot) bodyPtr() *string    { return &s.Body }
func (s *Snapshot) logPtr() *[]LogEntry { return nil }
func (s *Snapshot) setRev(r string)     { s.Rev = r }
func (s *Snapshot) getRev() string      { return s.Rev }
func (s *Snapshot) setFileID(id string) error {
	t, v, ok := strings.Cut(id, "/v")
	n, err := strconv.Atoi(v)
	if !ok || err != nil || n < 1 {
		return invalid("snapshot id %q", id)
	}
	s.Task, s.Version = t, n
	return nil
}
func (s *Snapshot) fields() []field {
	return []field{
		strF("task", &s.Task, false),
		intF("version", &s.Version),
		strF("title", &s.Title, false),
		strF("image", &s.Image, false),
		strF("source", &s.Source, true),
		timeF("captured", &s.Captured, true),
		strF("author", &s.Author, true),
		strF("commit", &s.Commit, true),
		strF("verdict", &s.Verdict, false),
		strF("reviewed_by", &s.ReviewedBy, false),
		timeF("reviewed_at", &s.ReviewedAt, false),
		strF("review_note", &s.ReviewNote, false),
	}
}
func (s *Snapshot) normalize() error {
	if err := checkID(KindTask, s.Task); err != nil {
		return err
	}
	if s.Version < 1 {
		return invalid("snapshot version %d", s.Version)
	}
	if s.Verdict == "" {
		s.Verdict = VerdictPending
	}
	if !oneOf(s.Verdict, VerdictPending, VerdictApproved, VerdictChanges) {
		return invalid("snapshot verdict %q", s.Verdict)
	}
	if strings.ContainsAny(s.Image, "/\\") || strings.Contains(s.Image, "..") {
		return invalid("snapshot image %q must be a plain file name", s.Image)
	}
	s.ID = s.fileID()
	return nil
}

// ---- Note ----

func (n *Note) kind() string              { return KindNote }
func (n *Note) fileID() string            { return n.ID }
func (n *Note) setFileID(id string) error { n.ID = id; return nil }
func (n *Note) rel() string               { return relFor(KindNote, n.ID) }
func (n *Note) bodyPtr() *string          { return &n.Body }
func (n *Note) logPtr() *[]LogEntry       { return nil }
func (n *Note) setRev(r string)           { n.Rev = r }
func (n *Note) getRev() string            { return n.Rev }
func (n *Note) fields() []field {
	return []field{
		strF("id", &n.ID, false),
		strF("ref", &n.Ref, false),
		floatNullF("x", &n.X),
		floatNullF("y", &n.Y),
		intNullF("line", &n.Line),
		strF("anchor", &n.Anchor, true),
		strF("author", &n.Author, true),
		timeF("created", &n.Created, true),
		boolF("resolved", &n.Resolved),
		strF("resolved_by", &n.ResolvedBy, true),
		timeF("resolved_at", &n.ResolvedAt, true),
		nullF("reply_to", &n.ReplyTo),
	}
}
func (n *Note) normalize() error {
	if err := checkID(KindNote, n.ID); err != nil {
		return err
	}
	if _, err := ParseRef(n.Ref); err != nil {
		return err
	}
	for _, p := range []*float64{n.X, n.Y} {
		if p != nil && (*p < 0 || *p > 1) {
			return invalid("note pin %v must be between 0 and 1", *p)
		}
	}
	if (n.X == nil) != (n.Y == nil) {
		return invalid("note needs both x and y, or neither")
	}
	if n.Line != nil && *n.Line < 1 {
		return invalid("note line %d", *n.Line)
	}
	if n.ReplyTo != "" {
		if err := checkID(KindNote, n.ReplyTo); err != nil {
			return err
		}
	}
	return nil
}
