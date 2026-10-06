package authored

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseRefTable(t *testing.T) {
	good := []struct {
		in         string
		project    string
		typ, id    string
		frag       string
		wantString string // when the output differs from the input
	}{
		{in: "project:ledger", typ: "project", id: "ledger"},
		{in: "file:discover.go#L30", typ: "file", id: "discover.go", frag: "L30"},
		{in: "file:src/x.go#L40-L52", typ: "file", id: "src/x.go", frag: "L40-L52"},
		{in: "file:plan/roadmap.md", typ: "file", id: "plan/roadmap.md"},
		{in: "file:docs/my notes.md", typ: "file", id: "docs/my notes.md"},
		{in: "file:a:b.txt", typ: "file", id: "a:b.txt"},
		{in: "image:f0_gate.png", typ: "image", id: "f0_gate.png"},
		{in: "image:shots/a.png#x=0.46,y=0.20", typ: "image", id: "shots/a.png", frag: "x=0.46,y=0.20"},
		{in: "commit:abc1234", typ: "commit", id: "abc1234"},
		{in: "commit:b7dc920bef", typ: "commit", id: "b7dc920bef"},
		{in: "commit:BDCB99C0", typ: "commit", id: "BDCB99C0"},
		{in: "session:121c3b19-ada3-495e-a518-7fe5acf0a71e", typ: "session", id: "121c3b19-ada3-495e-a518-7fe5acf0a71e"},
		{in: "session:121c3b19", typ: "session", id: "121c3b19"},
		{in: "server:8090", typ: "server", id: "8090"},
		{in: "server-key:0123456789", typ: "server-key", id: "0123456789"},
		{in: "task:T-12", typ: "task", id: "T-12"},
		{in: "feature:F-3", typ: "feature", id: "F-3"},
		{in: "milestone:M-1", typ: "milestone", id: "M-1"},
		{in: "note:N-40", typ: "note", id: "N-40"},
		{in: "snapshot:T-12/v3", typ: "snapshot", id: "T-12/v3"},
		{in: "snapshot:T-1/v3#x=0.5,y=0.9", typ: "snapshot", id: "T-1/v3", frag: "x=0.5,y=0.9"},
		{in: "ledger:commit:bdcb99c", project: "ledger", typ: "commit", id: "bdcb99c"},
		{in: "ledger:task:T-4", project: "ledger", typ: "task", id: "T-4"},
		{in: "personal/ledger:file:main.go#L3", project: "personal/ledger", typ: "file", id: "main.go", frag: "L3"},
		{in: "personal.ledger:snapshot:T-1/v2", project: "personal.ledger", typ: "snapshot", id: "T-1/v2"},
		{in: "  task:T-12  ", typ: "task", id: "T-12", wantString: "task:T-12"},
	}
	for _, c := range good {
		r, err := ParseRef(c.in)
		if err != nil {
			t.Errorf("ParseRef(%q): %v", c.in, err)
			continue
		}
		if r.Project != c.project || r.Type != c.typ || r.ID != c.id || r.Frag != c.frag {
			t.Errorf("ParseRef(%q) = %+v", c.in, r)
		}
		want := c.wantString
		if want == "" {
			want = c.in
		}
		if r.String() != want {
			t.Errorf("String() of %q = %q", c.in, r.String())
		}
		// Parsing the output gives the same ref.
		if r2, err := ParseRef(r.String()); err != nil || r2 != r {
			t.Errorf("round trip of %q: %+v %v", c.in, r2, err)
		}
	}

	bad := []string{
		"", "   ", "task", "task:", ":T-1", "T-12", "bogus:T-12", "ledger:bogus:x",
		"task:12", "task:t-12", "task:T-", "task:T-1/v3", "task:T-1x",
		"feature:T-3", "milestone:F-1", "note:N-", "snapshot:T-12", "snapshot:T-12/v", "snapshot:T-12/3", "snapshot:x/v3",
		"commit:abc", "commit:xyz1234", "commit:12345", "commit:" + string(make([]byte, 3)),
		"session:xyz", "session:121c3b1", "session:121c3b19-ada3",
		"server:abc", "server:0", "server:70000", "server:80 80", "server:",
		"server-key:123", "file:", "file:/etc/passwd", "file:../secret", "file:a/../../b", "file:~/x", "file:a\\b",
		"file:x#", "file:x#L0", "file:x#L5-L3", "file:x#L", "file:x#Lx", "file:x#x=0.5", "file:x#x=2,y=0.5", "file:x#x=0.5,y=-1",
		"image:a.png#L3", "commit:abc1234#L3", "task:T-1#frag", "server:8090#x=0.1,y=0.1",
		"../evil:commit:abc1234", "a b:commit:abc1234", "/abs:task:T-1", "ledger/:task:T-1", "-x:task:T-1",
		"task:T-1\nfile:x", "file:a\x00b",
	}
	for _, in := range bad {
		if r, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q) should fail, got %+v", in, r)
		} else if !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseRef(%q): error should wrap ErrInvalid: %v", in, err)
		}
	}
}

func TestRefHelpers(t *testing.T) {
	r, _ := ParseRef("file:src/x.go#L40-L52")
	if from, to, ok := r.Lines(); !ok || from != 40 || to != 52 {
		t.Errorf("Lines: %d %d %v", from, to, ok)
	}
	if _, _, ok := r.Point(); ok {
		t.Errorf("Point on a line fragment")
	}
	p, _ := ParseRef("image:a.png#x=0.46,y=0.20")
	if x, y, ok := p.Point(); !ok || x != 0.46 || y != 0.2 {
		t.Errorf("Point: %v %v %v", x, y, ok)
	}
	one, _ := ParseRef("file:a.go#L7")
	if from, to, _ := one.Lines(); from != 7 || to != 7 {
		t.Errorf("single line: %d %d", from, to)
	}
	if !r.Local() || r.IsAuthored() {
		t.Errorf("Local/IsAuthored wrong for %v", r)
	}
	tk, _ := ParseRef("ledger:task:T-1")
	if tk.Local() || !tk.IsAuthored() {
		t.Errorf("Local/IsAuthored wrong for %v", tk)
	}
	img, _ := ParseRef("image:a.png")
	if img.Canonical() != "file:a.png" || img.String() != "image:a.png" {
		t.Errorf("image canonical: %q", img.Canonical())
	}
	up, _ := ParseRef("commit:BDCB99C")
	if up.Canonical() != "commit:bdcb99c" {
		t.Errorf("hex canonical: %q", up.Canonical())
	}
}

func TestRefJSON(t *testing.T) {
	var r Ref
	if err := json.Unmarshal([]byte(`"ledger:commit:bdcb99c"`), &r); err != nil || r.Project != "ledger" || r.ID != "bdcb99c" {
		t.Errorf("string form: %+v %v", r, err)
	}
	var o Ref
	if err := json.Unmarshal([]byte(`{"type":"snapshot","projectId":"portfolio","id":"T-1/v3","frag":"x=0.5,y=0.5"}`), &o); err != nil {
		t.Fatal(err)
	}
	if o.String() != "portfolio:snapshot:T-1/v3#x=0.5,y=0.5" {
		t.Errorf("object form: %q", o.String())
	}
	b, _ := json.Marshal(o)
	if string(b) != `{"projectId":"portfolio","type":"snapshot","id":"T-1/v3","frag":"x=0.5,y=0.5"}` {
		t.Errorf("marshal: %s", b)
	}
	for _, bad := range []string{`"bogus"`, `{"type":"task","id":"12"}`, `{"type":"x","id":"1"}`, `{"projectId":"../x","type":"task","id":"T-1"}`, `42`} {
		var v Ref
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
}
