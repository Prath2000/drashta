package authored

import (
	"strings"
	"testing"
)

// Each case: a hand-written file, a change, and the exact file that must come out.
func TestPatchKeepsEveryOtherByte(t *testing.T) {
	cases := []struct {
		name   string
		before string
		change func(*Task)
		after  string
	}{
		{
			name: "comment lines before the next key stay",
			before: `---
id: T-1
title: A
status: todo
# about the feature
# (two lines)

feature: F-1
---
`,
			change: func(tk *Task) { tk.Status = StatusReview },
			after: `---
id: T-1
title: A
status: review
# about the feature
# (two lines)

feature: F-1
---
`,
		},
		{
			name: "block list in the middle is replaced as a whole",
			before: `---
id: T-1
title: A
status: todo
links:
- commit:aaaaaaa
- commit:bbbbbbb
# keep me
created: 2026-09-28T09:30:00+05:30
---
`,
			change: func(tk *Task) { tk.Links = []string{"commit:ccccccc"} },
			after: `---
id: T-1
title: A
status: todo
links:
  - commit:ccccccc
# keep me
created: 2026-09-28T09:30:00+05:30
---
`,
		},
		{
			name: "multi-line literal value",
			before: `---
id: T-1
title: |
  line one
  # not a comment

  line two
status: todo
---
`,
			change: func(tk *Task) { tk.Title = "single" },
			after: `---
id: T-1
title: single
status: todo
---
`,
		},
		{
			name: "missing known key is added at the end, after unknown keys",
			before: `---
id: T-1
title: A
status: todo
mystery: 42
---
Body
`,
			change: func(tk *Task) { tk.Assignee = "asha" },
			after: `---
id: T-1
title: A
status: todo
mystery: 42
assignee: asha
---
Body
`,
		},
		{
			name: "trailing comment after the last key",
			before: `---
id: T-1
title: A
status: todo
# end of keys
---
`,
			change: func(tk *Task) { tk.Status = StatusDone },
			after: `---
id: T-1
title: A
status: done
# end of keys
---
`,
		},
		{
			name: "keys in a different order, with quotes",
			before: `---
status: todo
"title": 'Quoted'
id: T-1
---
`,
			change: func(tk *Task) { tk.Status = StatusDone },
			after: `---
status: done
"title": 'Quoted'
id: T-1
---
`,
		},
		{
			name: "flow mapping falls back to writing the whole tree",
			before: `---
{id: T-1, title: A, status: todo}
---
`,
			change: func(tk *Task) { tk.Status = StatusDone },
			after:  "", // only checked loosely below
		},
		{
			name: "empty front matter",
			before: `---
---
Body.
`,
			change: func(tk *Task) { tk.ID = "T-1"; tk.Title = "A" },
			after:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			path := dotPath(root, "tasks", "T-1.md")
			writeFile(t, path, c.before)
			tk, err := LoadTask(root, "T-1")
			if err != nil {
				t.Fatal(err)
			}
			c.change(tk)
			if _, err := Save(root, tk, tk.Rev); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, path)
			if c.after != "" && got != c.after {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.after)
			}
			// Whatever the path, the file must load back with the new values.
			again, err := LoadTask(root, "T-1")
			if err != nil {
				t.Fatalf("reload: %v\n%s", err, got)
			}
			if again.Status != tk.Status || again.Title != tk.Title || again.Assignee != tk.Assignee {
				t.Errorf("reload differs: %+v vs %+v\n%s", again, tk, got)
			}
		})
	}
}

func TestFlowMappingKeepsValues(t *testing.T) {
	root := t.TempDir()
	path := dotPath(root, "tasks", "T-1.md")
	writeFile(t, path, "---\n{id: T-1, title: A, status: todo, extra: [1, 2]}\n---\nBody\n")
	tk, _ := LoadTask(root, "T-1")
	tk.Status = StatusDone
	if _, err := Save(root, tk, tk.Rev); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "status: done") || !strings.Contains(got, "extra") || !strings.HasSuffix(got, "---\nBody\n") {
		t.Errorf("flow mapping rewrite:\n%s", got)
	}
}

func TestBodyStaysWhenOnlyFrontMatterChanges(t *testing.T) {
	root := t.TempDir()
	body := "\n\n  indented first line\n\nsecond\n\n\n"
	writeFile(t, dotPath(root, "notes", "N-1.md"), "---\nid: N-1\nref: task:T-1\nresolved: false\n---"+body)
	n, err := LoadNote(root, "N-1")
	if err != nil {
		t.Fatal(err)
	}
	n.Resolved = true
	if _, err := Save(root, n, n.Rev); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dotPath(root, "notes", "N-1.md"))
	if want := "---\nid: N-1\nref: task:T-1\nresolved: true\n---" + body; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestCRLFAndBOMFilesLoad(t *testing.T) {
	root := t.TempDir()
	writeFile(t, dotPath(root, "tasks", "T-1.md"), "\xEF\xBB\xBF---\r\nid: T-1\r\ntitle: Windows\r\nstatus: todo\r\n---\r\nBody\r\n")
	tk, err := LoadTask(root, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Title != "Windows" || tk.Body != "Body" {
		t.Errorf("%+v", tk)
	}
}
