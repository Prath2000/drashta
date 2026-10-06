package authored

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Task statuses, in board order (04-data-model.md section 2.3).
const (
	StatusBacklog    = "backlog"
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusReview     = "review"
	StatusDone       = "done"
)

// Statuses lists every valid task status in board order.
var Statuses = []string{StatusBacklog, StatusTodo, StatusInProgress, StatusReview, StatusDone}

// Snapshot verdicts.
const (
	VerdictPending  = "pending"
	VerdictApproved = "approved"
	VerdictChanges  = "changes"
)

// Milestone states.
const (
	StateOpen   = "open"
	StateClosed = "closed"
)

// Kind names accepted by NextID, Delete, Load and Update.
const (
	KindMilestone = "milestone"
	KindFeature   = "feature"
	KindTask      = "task"
	KindSnapshot  = "snapshot"
	KindNote      = "note"
)

// MaxImageBytes is the size cap for one snapshot image (20 MB).
const MaxImageBytes = 20 << 20

// Errors. Use errors.Is: the returned errors add detail around these.
var (
	// ErrConflict means the file on disk is not the version the caller read. The API maps it to 409.
	ErrConflict = errors.New("authored: file changed on disk since it was read")
	// ErrExists means a new item would overwrite a file that already exists.
	ErrExists = errors.New("authored: file already exists")
	// ErrNotFound means the item (or its file) does not exist.
	ErrNotFound = errors.New("authored: not found")
	// ErrInvalid means a value is not allowed (bad id, status, ref, path).
	ErrInvalid = errors.New("authored: invalid value")
	// ErrTooLarge means a snapshot image is bigger than MaxImageBytes.
	ErrTooLarge = errors.New("authored: image is larger than 20 MB")
	// ErrBadExt means the image extension is not in the allow-list.
	ErrBadExt = errors.New("authored: image extension not allowed")
	// ErrLockTimeout means .drashta/.lock stayed busy for too long.
	ErrLockTimeout = errors.New("authored: .drashta/.lock is busy")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Milestone is a dated target that holds features (milestones/M-1.md).
type Milestone struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Due      string   `json:"due,omitempty"` // date text, for example 2026-10-05
	Features []string `json:"featureIds"`
	State    string   `json:"state"` // open | closed
	Created  string   `json:"created,omitempty"`
	Body     string   `json:"body"`
	Rev      string   `json:"rev,omitempty"`
}

// Feature groups tasks (features/F-1.md).
type Feature struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Milestone string `json:"milestoneId,omitempty"`
	Created   string `json:"created,omitempty"`
	Body      string `json:"body"`
	Rev       string `json:"rev,omitempty"`
}

// LogEntry is one line of a task's "## Log" section. Old entries are never edited.
type LogEntry struct {
	Raw  string `json:"-"`    // the exact original line; empty for a new entry
	Time string `json:"time"` // for example 2026-09-30T12:02+05:30
	By   string `json:"by"`
	Text string `json:"text"`
}

// Task is one unit of work (tasks/T-12.md).
type Task struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	Feature   string     `json:"featureId,omitempty"`
	Assignee  string     `json:"assignee,omitempty"`
	Created   string     `json:"created,omitempty"`
	CreatedBy string     `json:"createdBy,omitempty"`
	Updated   string     `json:"updated,omitempty"`
	Links     []string   `json:"links"`   // manual links, ref strings
	Unlinks   []string   `json:"unlinks"` // auto links the user rejected
	Body      string     `json:"body"`    // text before "## Log"
	Log       []LogEntry `json:"log"`
	Rev       string     `json:"rev,omitempty"`
}

// Snapshot is one numbered version of a task's deliverable (snapshots/T-1/v3.md).
type Snapshot struct {
	ID         string `json:"id"` // "T-1/v3"; set by Load and Save, never written to the file
	Task       string `json:"taskId"`
	Version    int    `json:"version"`
	Title      string `json:"title"`
	Image      string `json:"image,omitempty"` // file name next to the .md, for example v3.png
	Source     string `json:"source,omitempty"`
	Captured   string `json:"captured,omitempty"`
	Author     string `json:"author,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Verdict    string `json:"verdict"` // pending | approved | changes
	ReviewedBy string `json:"reviewedBy,omitempty"`
	ReviewedAt string `json:"reviewedAt,omitempty"`
	ReviewNote string `json:"reviewNote,omitempty"`
	Body       string `json:"body"`
	Rev        string `json:"rev,omitempty"`
}

// Note is a comment on any Ref (notes/N-40.md). The text is Body.
type Note struct {
	ID         string   `json:"id"`
	Ref        string   `json:"ref"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Line       *int     `json:"line,omitempty"`
	Anchor     string   `json:"anchor,omitempty"`
	Author     string   `json:"author,omitempty"`
	Created    string   `json:"created,omitempty"`
	Resolved   bool     `json:"resolved"`
	ResolvedBy string   `json:"resolvedBy,omitempty"`
	ResolvedAt string   `json:"resolvedAt,omitempty"`
	ReplyTo    string   `json:"replyTo,omitempty"`
	Body       string   `json:"body"`
	Rev        string   `json:"rev,omitempty"`
}

// Tracker is everything LoadProject found in one project. Each slice is sorted by number.
type Tracker struct {
	Milestones []Milestone `json:"milestones"`
	Features   []Feature   `json:"features"`
	Tasks      []Task      `json:"tasks"`
	Snapshots  []Snapshot  `json:"snapshots"`
	Notes      []Note      `json:"notes"`
}

// FileError describes a problem with one file. When Warn is true the item was still
// loaded (for example the id: key differed from the file name); otherwise it was skipped.
type FileError struct {
	Path string // relative to the project root, for example .drashta/tasks/T-3.md
	Warn bool
	Err  error
}

func (e *FileError) Error() string {
	kind := "error"
	if e.Warn {
		kind = "warning"
	}
	return fmt.Sprintf("%s: %s: %v", kind, e.Path, e.Err)
}

func (e *FileError) Unwrap() error { return e.Err }

// ---- ids ----

var (
	idRe = map[string]*regexp.Regexp{
		KindMilestone: regexp.MustCompile(`^M-[0-9]+$`),
		KindFeature:   regexp.MustCompile(`^F-[0-9]+$`),
		KindTask:      regexp.MustCompile(`^T-[0-9]+$`),
		KindNote:      regexp.MustCompile(`^N-[0-9]+$`),
		KindSnapshot:  regexp.MustCompile(`^T-[0-9]+/v[0-9]+$`),
	}
	kindDir = map[string]string{
		KindMilestone: "milestones",
		KindFeature:   "features",
		KindTask:      "tasks",
		KindNote:      "notes",
		KindSnapshot:  "snapshots",
	}
	kindPrefix = map[string]string{
		KindMilestone: "M",
		KindFeature:   "F",
		KindTask:      "T",
		KindNote:      "N",
	}
	taskIDRe = idRe[KindTask]
)

// normKind accepts "task", "tasks", " Task " and returns the canonical kind name.
func normKind(k string) (string, error) {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.TrimSuffix(k, "s")
	if _, ok := kindDir[k]; !ok {
		return "", invalid("unknown kind %q", k)
	}
	return k, nil
}

func checkID(kind, id string) error {
	re, ok := idRe[kind]
	if !ok {
		return invalid("unknown kind %q", kind)
	}
	if !re.MatchString(id) {
		return invalid("%s id %q is not in the right form", kind, id)
	}
	return nil
}

// ---- times ----

// FormatTime formats t the way front matter keys like created: and updated: are written.
func FormatTime(t time.Time) string { return t.Format(time.RFC3339) }

// FormatLogTime formats t for a task log line: minute precision, for example 2026-09-30T12:02+05:30.
func FormatLogTime(t time.Time) string { return t.Format("2006-01-02T15:04Z07:00") }

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ParseTime reads the time formats that appear in .drashta files. It returns false when s is
// empty or in an unknown format. A text without a zone is read in the local zone.
func ParseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
