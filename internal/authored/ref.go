package authored

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Ref points at one thing that can be previewed and opened (04-data-model.md section 2.4):
//
//	[<project>:]<type>:<id>[#<fragment>]
type Ref struct {
	Project string `json:"projectId,omitempty"` // empty means "this project"
	Type    string `json:"type"`
	ID      string `json:"id"`
	Frag    string `json:"frag,omitempty"` // without the leading #
}

// Ref types.
const (
	RefProject   = "project"
	RefFile      = "file"
	RefImage     = "image" // alias of file with an image viewer
	RefCommit    = "commit"
	RefSession   = "session"
	RefServer    = "server"
	RefServerKey = "server-key"
	RefTask      = "task"
	RefFeature   = "feature"
	RefMilestone = "milestone"
	RefSnapshot  = "snapshot"
	RefNote      = "note"
)

var refTypes = map[string]bool{
	RefProject: true, RefFile: true, RefImage: true, RefCommit: true, RefSession: true,
	RefServer: true, RefServerKey: true, RefTask: true, RefFeature: true,
	RefMilestone: true, RefSnapshot: true, RefNote: true,
}

var (
	projectRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	commitRe    = regexp.MustCompile(`^[0-9a-fA-F]{6,64}$`)
	sessionRe   = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$`)
	portRe      = regexp.MustCompile(`^[0-9]{1,5}$`)
	serverKeyRe = regexp.MustCompile(`^[0-9a-fA-F]{10}$`)
	lineFragRe  = regexp.MustCompile(`^L([0-9]+)(?:-L([0-9]+))?$`)
	pointFragRe = regexp.MustCompile(`^x=([0-9]*\.?[0-9]+),y=([0-9]*\.?[0-9]+)$`)
)

// ParseRef reads a ref string. It rejects unknown types, ids in the wrong form and bad fragments.
//
// Rule: if the first segment is a known type the ref is local. Otherwise the first segment
// is a project id and a known type must follow.
func ParseRef(s string) (Ref, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, invalid("empty ref")
	}
	if strings.ContainsAny(s, "\x00\r\n") {
		return Ref{}, invalid("ref %q has control characters", orig)
	}
	body, frag := s, ""
	if i := strings.IndexByte(s, '#'); i >= 0 {
		body, frag = s[:i], s[i+1:]
		if frag == "" {
			return Ref{}, invalid("ref %q has an empty fragment", orig)
		}
	}
	first, rest, ok := strings.Cut(body, ":")
	if !ok {
		return Ref{}, invalid("ref %q needs the form type:id", orig)
	}
	var r Ref
	if refTypes[first] {
		r.Type = first
		r.ID = rest
	} else {
		typ, id, ok := strings.Cut(rest, ":")
		if !ok || !refTypes[typ] {
			return Ref{}, invalid("ref %q: unknown type", orig)
		}
		if !projectRe.MatchString(first) || strings.Contains(first, "..") || strings.HasSuffix(first, "/") {
			return Ref{}, invalid("ref %q: bad project id", orig)
		}
		r.Project, r.Type, r.ID = first, typ, id
	}
	r.Frag = frag
	if err := r.validate(); err != nil {
		return Ref{}, invalid("ref %q: %v", orig, err)
	}
	return r, nil
}

func (r Ref) validate() error {
	id := r.ID
	if id == "" {
		return errString("missing id")
	}
	switch r.Type {
	case RefProject:
		if !projectRe.MatchString(id) || strings.Contains(id, "..") || strings.HasSuffix(id, "/") {
			return errString("bad project id")
		}
	case RefFile, RefImage:
		if err := checkRelPath(id); err != nil {
			return err
		}
	case RefCommit:
		if !commitRe.MatchString(id) {
			return errString("commit id must be 6 to 64 hex characters")
		}
	case RefSession:
		if !sessionRe.MatchString(id) {
			return errString("session id must be a UUID (or its first 8 characters)")
		}
	case RefServer:
		if !portRe.MatchString(id) {
			return errString("server id must be a port number")
		}
		if p, _ := strconv.Atoi(id); p < 1 || p > 65535 {
			return errString("port out of range")
		}
	case RefServerKey:
		if !serverKeyRe.MatchString(id) {
			return errString("server-key must be 10 hex characters")
		}
	case RefTask, RefFeature, RefMilestone, RefSnapshot, RefNote:
		if err := checkID(r.Type, id); err != nil {
			return errString("bad " + r.Type + " id")
		}
	default:
		return errString("unknown type")
	}
	return r.validateFrag()
}

func (r Ref) validateFrag() error {
	if r.Frag == "" {
		return nil
	}
	if m := lineFragRe.FindStringSubmatch(r.Frag); m != nil {
		if r.Type != RefFile {
			return errString("a line fragment only works on file refs")
		}
		from, _ := strconv.Atoi(m[1])
		to := from
		if m[2] != "" {
			to, _ = strconv.Atoi(m[2])
		}
		if from < 1 || to < from {
			return errString("bad line range")
		}
		return nil
	}
	if m := pointFragRe.FindStringSubmatch(r.Frag); m != nil {
		if r.Type != RefFile && r.Type != RefImage && r.Type != RefSnapshot {
			return errString("a point fragment only works on file, image and snapshot refs")
		}
		x, errx := strconv.ParseFloat(m[1], 64)
		y, erry := strconv.ParseFloat(m[2], 64)
		if errx != nil || erry != nil || x < 0 || x > 1 || y < 0 || y > 1 {
			return errString("point must be between 0 and 1")
		}
		return nil
	}
	return errString("bad fragment " + strconv.Quote(r.Frag))
}

type errString string

func (e errString) Error() string { return string(e) }

// checkRelPath accepts a project-relative path: not absolute, no .. element, no NUL.
func checkRelPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return errString("file path must be relative to the project")
	}
	if strings.ContainsAny(p, "\x00\\") {
		return errString("file path has a forbidden character")
	}
	if strings.TrimSpace(p) != p {
		return errString("file path has leading or trailing space")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return errString("file path must not contain ..")
		}
	}
	return nil
}

// String writes the ref in its text form. It is the inverse of ParseRef for refs that came from it.
func (r Ref) String() string {
	var b strings.Builder
	if r.Project != "" {
		b.WriteString(r.Project)
		b.WriteByte(':')
	}
	b.WriteString(r.Type)
	b.WriteByte(':')
	b.WriteString(r.ID)
	if r.Frag != "" {
		b.WriteByte('#')
		b.WriteString(r.Frag)
	}
	return b.String()
}

// Canonical returns the form used as a key: "image:" becomes "file:" and hex ids are lower case.
func (r Ref) Canonical() string {
	c := r
	if c.Type == RefImage {
		c.Type = RefFile
	}
	switch c.Type {
	case RefCommit, RefSession, RefServerKey:
		c.ID = strings.ToLower(c.ID)
	}
	return c.String()
}

// Local reports whether the ref points into the same project.
func (r Ref) Local() bool { return r.Project == "" }

// IsAuthored reports whether the ref points at a tracker item (task, feature, milestone, snapshot, note).
func (r Ref) IsAuthored() bool {
	switch r.Type {
	case RefTask, RefFeature, RefMilestone, RefSnapshot, RefNote:
		return true
	}
	return false
}

// Lines returns the line range of a #L40 or #L40-L52 fragment.
func (r Ref) Lines() (from, to int, ok bool) {
	m := lineFragRe.FindStringSubmatch(r.Frag)
	if m == nil {
		return 0, 0, false
	}
	from, _ = strconv.Atoi(m[1])
	to = from
	if m[2] != "" {
		to, _ = strconv.Atoi(m[2])
	}
	return from, to, true
}

// Point returns the x,y of a #x=0.46,y=0.20 fragment.
func (r Ref) Point() (x, y float64, ok bool) {
	m := pointFragRe.FindStringSubmatch(r.Frag)
	if m == nil {
		return 0, 0, false
	}
	x, _ = strconv.ParseFloat(m[1], 64)
	y, _ = strconv.ParseFloat(m[2], 64)
	return x, y, true
}

// UnmarshalJSON accepts both forms: the string "ledger:commit:bdcb99c" and the object
// {type, projectId, id, frag}.
func (r *Ref) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := ParseRef(s)
		if err != nil {
			return err
		}
		*r = v
		return nil
	}
	var o struct {
		Project string `json:"projectId"`
		Type    string `json:"type"`
		ID      string `json:"id"`
		Frag    string `json:"frag"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	v := Ref{Project: o.Project, Type: o.Type, ID: o.ID, Frag: o.Frag}
	if v.Project != "" && (!projectRe.MatchString(v.Project) || strings.Contains(v.Project, "..")) {
		return invalid("bad project id %q", v.Project)
	}
	if err := v.validate(); err != nil {
		return invalid("%v", err)
	}
	*r = v
	return nil
}
