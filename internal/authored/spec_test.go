package authored

import (
	"strings"
	"testing"
)

// The five examples of 04-data-model.md section 2.3, as written there.
func TestSpecExamplesLoadAndRoundTrip(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"milestones/M-1.md": `---
id: M-1
title: Chambers v3
due: 2026-10-05
features: [F-1, F-2]
state: open            # open | closed
created: 2026-09-29T13:00:00+05:30
---
All three relic chambers reviewed and approved.
`,
		"features/F-1.md": `---
id: F-1
title: Relic chambers
milestone: M-1         # optional; mirrors milestone.features (milestone file wins on conflict)
created: 2026-09-29T13:00:00+05:30
---
Each relic opens into its own chamber: philosophy, diary, voyage.
`,
		"tasks/T-1.md": `---
id: T-1
title: "Philosophy chamber: star dome over a compass rose"
status: review         # backlog | todo | in_progress | review | done
feature: F-1
assignee: alice + Claude
created: 2026-09-29T13:10:00+05:30
created_by: alice      # or session:<uuid>
updated: 2026-09-30T12:02:00+05:30
links:                 # manual links only; auto links live in SQLite (section 3)
  - commit:b7dc920bef
  - file:design/HANDOFF.md#L3
  - server:8090
unlinks: []            # auto links the user rejected
---
The dome should feel like standing under the night sky. The compass rose is the floor.

## Log
- 2026-09-30T12:02+05:30 session:121c3b19 posted snapshot v3
- 2026-09-30T13:05+05:30 alice status review -> in_progress
`,
		"snapshots/T-1/v3.md": `---
task: T-1
version: 3
title: Philosophy chamber
image: v3.png
source: url:http://localhost:5173/#/philosophy   # or file:design/screenshots/ph3.png, or upload
captured: 2026-09-30T12:02:00+05:30
author: session:121c3b19-ada3-495e-a518-7fe5acf0a71e
commit: b7dc920bef     # HEAD at capture time, if any
verdict: pending       # pending | approved | changes
reviewed_by: ""
reviewed_at: ""
review_note: ""
---
Constellation pass; lines now follow the dome curvature.
`,
		"notes/N-40.md": `---
id: N-40
ref: snapshot:T-1/v3   # any Ref, with an optional fragment
x: 0.46                # optional, image/snapshot pins (0..1)
y: 0.20
line: null             # optional, for file refs (same as #L30 in ref)
anchor: ""             # optional: text of the pinned line, used to re-find it after edits
author: alice
created: 2026-09-30T13:00:00+05:30
resolved: false
resolved_by: ""
resolved_at: ""
reply_to: null         # N-id for a threaded reply
---
Constellation lines slightly too bright against the dome.
`,
	}
	for rel, text := range files {
		writeFile(t, dotPath(root, rel), text)
	}
	tr, errs := LoadProject(root)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(tr.Milestones) != 1 || len(tr.Features) != 1 || len(tr.Tasks) != 1 || len(tr.Snapshots) != 1 || len(tr.Notes) != 1 {
		t.Fatalf("%+v", tr)
	}
	m, f, task, s, n := tr.Milestones[0], tr.Features[0], tr.Tasks[0], tr.Snapshots[0], tr.Notes[0]
	if m.State != "open" || m.Due != "2026-10-05" || strings.Join(m.Features, ",") != "F-1,F-2" {
		t.Errorf("milestone %+v", m)
	}
	if f.Milestone != "M-1" || f.Title != "Relic chambers" {
		t.Errorf("feature %+v", f)
	}
	if task.Title != "Philosophy chamber: star dome over a compass rose" || len(task.Links) != 3 || len(task.Unlinks) != 0 || len(task.Log) != 2 || task.Log[0].By != "session:121c3b19" {
		t.Errorf("task %+v", task)
	}
	if s.Source != "url:http://localhost:5173/#/philosophy" || s.Commit != "b7dc920bef" || s.Author != "session:121c3b19-ada3-495e-a518-7fe5acf0a71e" || s.Verdict != "pending" {
		t.Errorf("snapshot %+v", s)
	}
	if n.Ref != "snapshot:T-1/v3" || n.X == nil || *n.X != 0.46 || n.Y == nil || *n.Y != 0.2 || n.Line != nil || n.ReplyTo != "" || n.Resolved {
		t.Errorf("note %+v", n)
	}

	// Saving every item without a change leaves every file as it was, byte for byte.
	for rel, text := range files {
		kind, id, _ := Classify(root, ".drashta/"+rel)
		item, err := LoadItem(root, kind, id)
		if err != nil {
			t.Fatal(err)
		}
		rev := map[string]string{}
		switch v := item.(type) {
		case *Milestone:
			rev[rel] = v.Rev
		case *Feature:
			rev[rel] = v.Rev
		case *Task:
			rev[rel] = v.Rev
		case *Snapshot:
			rev[rel] = v.Rev
		case *Note:
			rev[rel] = v.Rev
		}
		if _, err := Save(root, item, rev[rel]); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, dotPath(root, rel)); got != text {
			t.Errorf("%s changed by a no-op save:\n%s", rel, got)
		}
	}

	// One change in the note touches one line.
	if _, err := UpdateNote(root, "N-40", func(n *Note) error {
		n.Resolved, n.ResolvedBy, n.ResolvedAt = true, "alice", "2026-09-30T14:00:00+05:30"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"resolved: false\n", "resolved: true\n",
		"resolved_by: \"\"\n", "resolved_by: alice\n",
		"resolved_at: \"\"\n", "resolved_at: 2026-09-30T14:00:00+05:30\n",
	).Replace(files["notes/N-40.md"])
	if got := readFile(t, dotPath(root, "notes/N-40.md")); got != want {
		t.Errorf("note after resolve:\n%s\nwant:\n%s", got, want)
	}
}
