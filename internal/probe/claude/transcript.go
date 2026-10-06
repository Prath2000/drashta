package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// Limits for the live transcript.
const (
	ToolTextMax  = 2 << 10  // tool input/output text per turn, bytes
	TurnTextMax  = 16 << 10 // user and claude text per turn, bytes
	PageDefault  = 40
	PageMax      = 500
	UserTextMax  = 499 // runes per Users() text (+ "…")
	UserTextsMax = 50
)

// Turn is one row of the live transcript.
// I is unique and grows through the file: the byte offset of the record's
// line plus the block index inside the record. It is stable across pages.
type Turn struct {
	I    int       `json:"i"`
	Role string    `json:"role"` // user | claude | tool
	TS   time.Time `json:"ts"`
	Text string    `json:"text"`
	Tool string    `json:"tool,omitempty"`
}

// Page is one page of a transcript.
type Page struct {
	Turns []Turn `json:"turns"`
	Next  int64  `json:"next"` // offset to pass for the next page
	Done  bool   `json:"done"` // no more complete lines after Next (for now)
}

// ErrBadOffset means the offset is not the start of a line in the file.
var ErrBadOffset = errors.New("claude: offset is not at a line start")

// Transcript reads turns from path, starting at byte offset (0 or a Next
// value). It stops after the line that reaches limit turns (default 40, max
// 500). roles filters by "user", "claude", "tool" (empty = all). All text is
// redacted; tool text is cut to 2 KB per turn. Only complete lines are read.
// The caller must pass a path from the index or one checked with CheckPath.
func Transcript(ctx context.Context, path string, offset int64, limit int, roles []string) (Page, error) {
	if limit <= 0 {
		limit = PageDefault
	}
	if limit > PageMax {
		limit = PageMax
	}
	want := map[string]bool{}
	for _, r := range roles {
		want[strings.TrimSpace(r)] = true
	}
	if err := checkOffset(path, offset); err != nil {
		return Page{}, err
	}
	fh, lr, err := openAt(path, offset, -1)
	if err != nil {
		return Page{}, err
	}
	defer fh.Close()
	page := Page{Turns: []Turn{}, Next: offset}
	n := 0
	for len(page.Turns) < limit {
		line, start, tooLong, err := lr.next()
		if err == io.EOF {
			page.Done = true
			break
		}
		if err != nil {
			return page, err
		}
		if n++; n&255 == 0 && ctx.Err() != nil {
			return page, ctx.Err()
		}
		page.Next = lr.off
		if tooLong {
			continue
		}
		for _, t := range lineTurns(line, start) {
			if len(want) == 0 || want[t.Role] {
				page.Turns = append(page.Turns, t)
			}
		}
	}
	return page, nil
}

func checkOffset(path string, offset int64) error {
	if offset < 0 {
		return ErrBadOffset
	}
	if offset == 0 {
		return nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	var b [1]byte
	if _, err := fh.ReadAt(b[:], offset-1); err != nil || b[0] != '\n' {
		return ErrBadOffset
	}
	return nil
}

// lineTurns turns one jsonl line into transcript rows. The result of a tool
// call has an empty Tool: it depends on this line only, so a page looks the
// same whatever offset it starts from.
func lineTurns(line []byte, start int64) []Turn {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var r record
	if !decode(line, &r) || r.IsMeta || r.Message == nil {
		return nil
	}
	if r.Type != "user" && r.Type != "assistant" {
		return nil
	}
	ts, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	c := r.Message.Content
	var out []Turn
	add := func(k int, role, text, tool string) {
		out = append(out, Turn{I: int(start) + k, Role: role, TS: ts, Text: text, Tool: tool})
	}
	switch r.Type {
	case "user":
		if c.IsStr {
			if t := realText(c.Str); t != "" {
				add(0, "user", cutBytes(Redact(t), TurnTextMax), "")
			}
			return out
		}
		for k, b := range c.Blocks {
			switch b.Type {
			case "text":
				if t := realText(b.Text); t != "" {
					add(k, "user", cutBytes(Redact(t), TurnTextMax), "")
				}
			case "tool_result":
				text := resultText(b.Content)
				if b.IsError {
					text = "error: " + text
				}
				add(k, "tool", cutBytes(Redact(text), ToolTextMax), "")
			}
		}
	case "assistant":
		for k, b := range c.Blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					add(k, "claude", cutBytes(Redact(b.Text), TurnTextMax), "")
				}
			case "tool_use":
				add(k, "tool", cutBytes(Redact(toolSummary(b.Input)), ToolTextMax), b.Name)
			}
		}
	}
	return out
}

// toolSummary is the short text for a tool call: its path, its command, or its input JSON.
func toolSummary(in toolInput) string {
	if fp := in.filePath(); fp != "" {
		return fp
	}
	if in.Command != "" {
		return in.Command
	}
	if len(in.Raw) > ToolTextMax*2 {
		return string(in.Raw[:ToolTextMax*2])
	}
	return string(in.Raw)
}

// resultText flattens a tool_result body (a string or a list of blocks).
func resultText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, bl := range blocks {
		if b.Len() > ToolTextMax*2 {
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		switch bl.Type {
		case "text":
			b.WriteString(bl.Text)
		case "image":
			b.WriteString("[image]")
		default:
			b.WriteString("[" + bl.Type + "]")
		}
	}
	return b.String()
}

// Users returns the user's own texts from a transcript, for the search index:
// redacted, white space collapsed, at most 500 runes each, at most 50 texts.
// It returns nil if the file cannot be read.
func Users(ctx context.Context, path string) []string {
	fh, lr, err := openAt(path, 0, -1)
	if err != nil {
		return nil
	}
	defer fh.Close()
	home, _ := os.UserHomeDir()
	var out []string
	n := 0
	for len(out) < UserTextsMax {
		line, _, tooLong, err := lr.next()
		if err != nil {
			break
		}
		if n++; n&1023 == 0 && ctx.Err() != nil {
			break
		}
		if tooLong || isToolResultLine(line) || !bytes.Contains(line, patUser) {
			continue
		}
		var r record
		if !decode(line, &r) || r.Type != "user" || r.IsMeta || r.Message == nil || r.Message.Role != "user" {
			continue
		}
		c := r.Message.Content
		texts := []string{c.Str}
		if !c.IsStr {
			texts = texts[:0]
			for _, b := range c.Blocks {
				if b.Type == "text" {
					texts = append(texts, b.Text)
				}
			}
		}
		for _, raw := range texts {
			if t := realText(raw); t != "" && len(out) < UserTextsMax {
				out = append(out, clean(t, UserTextMax, home))
			}
		}
	}
	return out
}
