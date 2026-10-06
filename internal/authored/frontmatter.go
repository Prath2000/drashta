package authored

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// splitFrontMatter cuts a file into: the front matter text (without the --- lines),
// the prefix (the file up to and including the closing --- line) and the rest (the body).
func splitFrontMatter(data []byte) (fm, prefix, rest string, err error) {
	s := strings.TrimPrefix(string(data), "\uFEFF")
	nl := strings.IndexByte(s, '\n')
	if nl < 0 || strings.TrimRight(s[:nl], " \t\r") != "---" {
		return "", "", "", errors.New("file must start with a --- line (YAML front matter)")
	}
	start := nl + 1
	for cur := start; cur < len(s); {
		next := len(s)
		line := s[cur:]
		if e := strings.IndexByte(line, '\n'); e >= 0 {
			line = line[:e]
			next = cur + e + 1
		}
		if t := strings.TrimRight(line, " \t\r"); t == "---" || t == "..." {
			return s[start:cur], s[:next], s[next:], nil
		}
		cur = next
	}
	return "", "", "", errors.New("front matter is not closed (missing second --- line)")
}

// fmDoc is the parsed YAML front matter. It keeps the node tree, so keys we do not know
// keep their order, comments and style when we write the file again.
type fmDoc struct {
	doc  *yaml.Node
	m    *yaml.Node
	text string // the original front matter text ("" for a new file)
}

func newFMDoc() *fmDoc {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	return &fmDoc{doc: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{m}}, m: m}
}

func parseFMDoc(text string) (*fmDoc, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || (doc.Kind == yaml.DocumentNode && len(doc.Content) == 0) {
		return newFMDoc(), nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, errors.New("front matter is not a YAML document")
	}
	m := doc.Content[0]
	if isNullNode(m) {
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content[0] = m
	}
	if m.Kind != yaml.MappingNode {
		return nil, errors.New("front matter must be key: value pairs")
	}
	return &fmDoc{doc: &doc, m: m, text: text}, nil
}

// index returns the position of the key's value node in m.Content, or -1.
func (d *fmDoc) index(key string) int {
	for i := 0; i+1 < len(d.m.Content); i += 2 {
		if d.m.Content[i].Value == key {
			return i + 1
		}
	}
	return -1
}

// read fills the Go fields from the keys that exist.
func (d *fmDoc) read(fs []field) error {
	var errs []error
	for _, f := range fs {
		if i := d.index(f.key); i >= 0 {
			if err := f.fromNode(d.m.Content[i]); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// change is one key that apply touched. pair is the position of the key in the original
// mapping, or -1 for a key that was added at the end.
type change struct {
	pair int
	key  *yaml.Node // the existing key node, or the new one
	val  *yaml.Node // the new value node
}

// apply writes the Go field values into the tree. A key is only touched when its value
// really changed. It returns what it touched.
func (d *fmDoc) apply(fs []field, creating bool) []change {
	var out []change
	for _, f := range fs {
		i := d.index(f.key)
		if i >= 0 {
			old := d.m.Content[i]
			if f.sameAsNode(old) {
				continue
			}
			nn := f.toNode()
			nn.LineComment = old.LineComment
			nn.HeadComment = old.HeadComment
			nn.FootComment = old.FootComment
			d.m.Content[i] = nn
			out = append(out, change{pair: (i - 1) / 2, key: d.m.Content[i-1], val: nn})
			continue
		}
		if creating && f.optional && f.isEmpty() {
			continue
		}
		if !creating && f.isEmpty() {
			continue
		}
		k := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: f.key}
		v := f.toNode()
		d.m.Content = append(d.m.Content, k, v)
		out = append(out, change{pair: -1, key: k, val: v})
	}
	return out
}

// patchText applies the changes to the original text, line by line. Lines that did not change
// stay byte for byte (comments, quotes, spacing). It returns false when the text cannot be
// patched safely (for example a { flow } mapping), and the caller then writes the whole tree.
func (d *fmDoc) patchText(changes []change, origPairs int) (string, bool) {
	if d.m.Style&yaml.FlowStyle != 0 {
		return "", false
	}
	lines := strings.SplitAfter(d.text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if origPairs > 0 && len(lines) == 0 {
		return "", false
	}
	keyLine := make([]int, origPairs)
	for p := 0; p < origPairs; p++ {
		l := d.m.Content[2*p].Line - 1
		if l < 0 || l >= len(lines) || (p > 0 && l <= keyLine[p-1]) {
			return "", false
		}
		keyLine[p] = l
	}
	type span struct {
		from, to int
		text     string
	}
	var spans []span
	var appended strings.Builder
	for _, c := range changes {
		k := *c.key
		k.HeadComment, k.FootComment = "", ""
		v := *c.val
		v.HeadComment, v.FootComment = "", ""
		text, err := encodePair(&k, &v)
		if err != nil {
			return "", false
		}
		if c.pair < 0 {
			appended.WriteString(text)
			continue
		}
		from := keyLine[c.pair]
		to := len(lines)
		if c.pair+1 < origPairs {
			to = keyLine[c.pair+1]
		}
		// Comment lines and blank lines right before the next key belong to that key.
		for to > from+1 {
			l := lines[to-1]
			if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
				to--
				continue
			}
			break
		}
		spans = append(spans, span{from, to, text})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	var sb strings.Builder
	pos := 0
	for _, sp := range spans {
		if sp.from < pos {
			return "", false
		}
		for ; pos < sp.from; pos++ {
			sb.WriteString(lines[pos])
		}
		sb.WriteString(sp.text)
		pos = sp.to
	}
	for ; pos < len(lines); pos++ {
		sb.WriteString(lines[pos])
	}
	if appended.Len() > 0 {
		out := sb.String()
		if out != "" && !strings.HasSuffix(out, "\n") {
			sb.WriteByte('\n')
		}
		sb.WriteString(appended.String())
	}
	return sb.String(), true
}

// verifyPatched re-reads the patched text and checks it says what the tree says: the same keys
// in the same order, every known field equal to the Go value, and every unknown key unchanged.
// If anything differs, the caller writes the whole tree instead.
func (d *fmDoc) verifyPatched(text string, e entity) bool {
	nd, err := parseFMDoc(text)
	if err != nil || len(nd.m.Content) != len(d.m.Content) {
		return false
	}
	known := map[string]bool{}
	for _, f := range e.fields() {
		known[f.key] = true
		i := nd.index(f.key)
		if i < 0 {
			if !f.isEmpty() {
				return false
			}
			continue
		}
		c := f.clone()
		if c.fromNode(nd.m.Content[i]) != nil || c.value() != f.value() {
			return false
		}
	}
	for i := 0; i < len(d.m.Content); i += 2 {
		if d.m.Content[i].Value != nd.m.Content[i].Value {
			return false
		}
		if known[d.m.Content[i].Value] {
			continue
		}
		a, errA := encodePair(d.m.Content[i], d.m.Content[i+1])
		b, errB := encodePair(nd.m.Content[i], nd.m.Content[i+1])
		if errA != nil || errB != nil || a != b {
			return false
		}
	}
	return true
}

func encodePair(k, v *yaml.Node) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{k, v}}
	if err := enc.Encode(m); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (d *fmDoc) encode() (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ---- body and log ----

// bodyOf turns the raw text after the front matter into the Body value:
// no blank lines at the start, no trailing white space.
func bodyOf(raw string) string {
	return strings.TrimRight(strings.TrimLeft(raw, "\r\n"), " \t\r\n")
}

// splitLog cuts a task body at the first "## Log" line that is not inside a code fence.
func splitLog(rest string) (body, log string, had bool) {
	inFence := false
	for cur := 0; cur < len(rest); {
		next := len(rest)
		line := rest[cur:]
		if e := strings.IndexByte(line, '\n'); e >= 0 {
			line = line[:e]
			next = cur + e + 1
		}
		t := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
		} else if !inFence && t == "## Log" {
			return rest[:cur], rest[next:], true
		}
		cur = next
	}
	return rest, "", false
}

var logLineRe = regexp.MustCompile(`^\s*[-*]\s+(\d{4}-\d{2}-\d{2}T\S+)\s+(\S+)\s*(.*)$`)

func parseLog(raw string) []LogEntry {
	out := []LogEntry{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		e := LogEntry{Raw: line}
		if m := logLineRe.FindStringSubmatch(line); m != nil {
			e.Time, e.By, e.Text = m[1], m[2], strings.TrimSpace(m[3])
		} else {
			e.Text = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*"))
		}
		out = append(out, e)
	}
	return out
}

func (e LogEntry) line() string {
	if e.Raw != "" {
		return e.Raw
	}
	s := "- " + e.Time
	if e.By != "" {
		s += " " + e.By
	}
	if e.Text != "" {
		s += " " + e.Text
	}
	return s
}

// AppendLog adds one entry at the end of the log. Time is written with FormatLogTime by the caller.
func (t *Task) AppendLog(logTime, by, text string) {
	t.Log = append(t.Log, LogEntry{Time: logTime, By: by, Text: text})
}

// ---- parse and render a whole file ----

// base is what we remember about a file on disk. A rewrite starts from it, so everything the
// caller did not change stays as it was.
type base struct {
	fm      *fmDoc
	prefix  string // file text up to and including the closing --- line
	head    string // the opening --- line, with its newline
	tail    string // the closing --- line, with its newline
	rawBody string // text before "## Log" (the whole rest for items without a log)
	rawLog  string // text after the "## Log" line
	hadLog  bool
	logs    []LogEntry
}

// parseEntity reads one file into a typed item.
func parseEntity(kind string, data []byte) (entity, *base, error) {
	fmText, prefix, rest, err := splitFrontMatter(data)
	if err != nil {
		return nil, nil, err
	}
	doc, err := parseFMDoc(fmText)
	if err != nil {
		return nil, nil, fmt.Errorf("front matter: %w", err)
	}
	e := newEntity(kind)
	if err := doc.read(e.fields()); err != nil {
		return nil, nil, fmt.Errorf("front matter: %w", err)
	}
	head := prefix[:strings.IndexByte(prefix, '\n')+1]
	b := &base{fm: doc, prefix: prefix, head: head, tail: prefix[len(head)+len(fmText):]}
	if lp := e.logPtr(); lp != nil {
		b.rawBody, b.rawLog, b.hadLog = splitLog(rest)
		b.logs = parseLog(b.rawLog)
		*lp = append([]LogEntry{}, b.logs...)
	} else {
		b.rawBody = rest
	}
	*e.bodyPtr() = bodyOf(b.rawBody)
	return e, b, nil
}

// render builds the new file bytes. b is the file as it is on disk now (nil for a new file).
func render(e entity, b *base) ([]byte, error) {
	var prefix string
	if b == nil {
		d := newFMDoc()
		d.apply(e.fields(), true)
		text, err := d.encode()
		if err != nil {
			return nil, err
		}
		prefix = "---\n" + text + "---\n"
	} else {
		origPairs := len(b.fm.m.Content) / 2
		changes := b.fm.apply(e.fields(), false)
		switch {
		case len(changes) == 0:
			prefix = b.prefix
		default:
			if text, ok := b.fm.patchText(changes, origPairs); ok && b.fm.verifyPatched(text, e) {
				prefix = b.head + text + b.tail
			} else {
				text, err := b.fm.encode()
				if err != nil {
					return nil, err
				}
				prefix = "---\n" + text + "---\n"
			}
		}
	}
	body := *e.bodyPtr()

	bodyPart := ""
	switch {
	case b != nil && bodyOf(b.rawBody) == body:
		bodyPart = b.rawBody
	case body != "":
		bodyPart = body + "\n"
	}

	logPart := ""
	if lp := e.logPtr(); lp != nil && (len(*lp) > 0 || (b != nil && b.hadLog)) {
		logPart = renderLog(*lp, b)
		if bodyPart != "" {
			if !strings.HasSuffix(bodyPart, "\n") {
				bodyPart += "\n"
			}
			if !strings.HasSuffix(bodyPart, "\n\n") {
				bodyPart += "\n"
			}
		}
	}
	return []byte(prefix + bodyPart + logPart), nil
}

// renderLog writes "## Log" and the entries. Old lines stay byte for byte when the new
// list starts with them (append only); otherwise the whole log is written again.
func renderLog(entries []LogEntry, b *base) string {
	if b != nil && b.hadLog && hasPrefixLog(entries, b.logs) {
		raw := b.rawLog
		if len(entries) == len(b.logs) {
			return "## Log\n" + raw
		}
		raw = strings.TrimRight(raw, "\n")
		var sb strings.Builder
		sb.WriteString("## Log\n")
		if raw != "" {
			sb.WriteString(raw)
			sb.WriteByte('\n')
		}
		for _, e := range entries[len(b.logs):] {
			sb.WriteString(e.line())
			sb.WriteByte('\n')
		}
		return sb.String()
	}
	var sb strings.Builder
	sb.WriteString("## Log\n")
	for _, e := range entries {
		sb.WriteString(e.line())
		sb.WriteByte('\n')
	}
	return sb.String()
}

func hasPrefixLog(entries, old []LogEntry) bool {
	if len(entries) < len(old) {
		return false
	}
	for i := range old {
		if entries[i].Raw != old[i].Raw {
			return false
		}
	}
	return true
}
