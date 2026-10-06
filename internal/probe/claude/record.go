package claude

import (
	"bytes"
	"encoding/json"
	"errors"
)

// record is the part of one jsonl line the parser reads. The format is not a
// public contract (03 section 12, R5), so every field is optional and a field
// with an unexpected type is ignored instead of failing the line.
type record struct {
	Type      string   `json:"type"`
	CWD       string   `json:"cwd"`
	Timestamp string   `json:"timestamp"`
	IsMeta    bool     `json:"isMeta"`
	Message   *message `json:"message"`
}

type message struct {
	Role    string  `json:"role"`
	Content content `json:"content"`
}

// content is message.content: a plain string or a list of blocks.
type content struct {
	Str    string
	IsStr  bool
	Blocks []block
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     toolInput       `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // tool_result body (string or blocks)
	IsError   bool            `json:"is_error"`
}

// toolInput keeps the few tool input fields the parser needs, plus the raw input.
type toolInput struct {
	FilePath     string
	Path         string
	NotebookPath string
	Command      string
	Raw          []byte
}

func (c *content) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	switch b[0] {
	case '"':
		c.IsStr = true
		return json.Unmarshal(b, &c.Str)
	case '[':
		var raws []json.RawMessage
		if err := json.Unmarshal(b, &raws); err != nil {
			return err
		}
		c.Blocks = make([]block, 0, len(raws))
		for _, r := range raws {
			var bl block
			if err := json.Unmarshal(r, &bl); err != nil && !isTypeErr(err) {
				continue // a strange block is ignored, the line still counts
			}
			c.Blocks = append(c.Blocks, bl)
		}
	}
	return nil
}

func (in *toolInput) UnmarshalJSON(b []byte) error {
	var m struct {
		FilePath     any `json:"file_path"`
		Path         any `json:"path"`
		NotebookPath any `json:"notebook_path"`
		Command      any `json:"command"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil // input that is not an object: keep only the raw bytes
	}
	in.FilePath, _ = m.FilePath.(string)
	in.Path, _ = m.Path.(string)
	in.NotebookPath, _ = m.NotebookPath.(string)
	in.Command, _ = m.Command.(string)
	in.Raw = append([]byte(nil), b...)
	return nil
}

// filePath is the first of file_path, notebook_path, path that is set.
func (in toolInput) filePath() string {
	switch {
	case in.FilePath != "":
		return in.FilePath
	case in.NotebookPath != "":
		return in.NotebookPath
	}
	return in.Path
}

func isTypeErr(err error) bool {
	var te *json.UnmarshalTypeError
	return errors.As(err, &te)
}

// decode reads one line. ok is false for a corrupt line (bad JSON).
func decode(line []byte, r *record) bool {
	*r = record{}
	if err := json.Unmarshal(line, r); err != nil && !isTypeErr(err) {
		return false
	}
	return true
}

// knownTypes are the top-level record types seen in Claude Code transcripts.
// Only "user" and "assistant" are parsed. The rest are ordinary bookkeeping
// records: known, and ignored. Any other type is counted as unknown (R5) and
// otherwise ignored too, so a new Claude Code version never breaks parsing.
// The list was checked against real transcripts (2026-10-01): every type seen
// there is in it, so Unknown only counts genuinely new types.
var knownTypes = map[string]bool{
	"user": true, "assistant": true, "system": true, "summary": true,
	"file-history-snapshot": true, "file-history-delta": true, "queue-operation": true, "progress": true,
	"attachment": true, "custom-title": true, "ai-title": true, "agent-name": true,
	"last-prompt": true, "permission-mode": true, "pr-link": true, "tag": true,
	"mode": true, "atis-latch": true, "frame-link": true, "worktree-state": true,
	"relocated": true, "cost-state": true, "fork-context-ref": true, "continued-in": true,
	"artifact-autoreact-ledger": true, "artifact-comment-monitor": true,
}

var (
	patAssistant  = []byte(`"type":"assistant"`)
	patUser       = []byte(`"type":"user"`)
	patToolResult = []byte(`"type":"tool_result"`)
	patProgress   = []byte(`"type":"progress"`)
	patTimestamp  = []byte(`"timestamp":"`)
)

// isToolResultLine reports a user line that only carries tool output. Such
// lines can be megabytes and hold no user words, so the parser skips the JSON
// decode for them (as sessions3.py does). Raw `"key":"` patterns only match
// real keys, because quotes inside JSON strings are escaped.
func isToolResultLine(line []byte) bool {
	return bytes.Contains(line, patToolResult) && bytes.Contains(line, patUser) &&
		!bytes.Contains(line, patAssistant) && !bytes.Contains(line, patProgress)
}

// rawTimestamp returns the first "timestamp" value in the line, without decoding it.
func rawTimestamp(line []byte) string {
	i := bytes.Index(line, patTimestamp)
	if i < 0 {
		return ""
	}
	rest := line[i+len(patTimestamp):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 || j > 64 {
		return ""
	}
	return string(rest[:j])
}
