package autolink

import (
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// richInput uses every rule at once.
func richInput() Input {
	start := t0
	end := t0.Add(80 * time.Minute)
	return Input{
		ProjectID: "drashta",
		Tasks: []TaskInfo{
			{ID: "T-1", Status: "todo"},
			{ID: "T-2", Status: "in_progress", Manual: []string{"commit:" + shaC[:8], "file:a.go#L3"}},
			{ID: "T-3", Status: "review", Unlinks: []string{"session:" + sessY}},
			{ID: "T-10", Status: "done"},
			{ID: "T-11", Status: "backlog"},
		},
		Commits: []probe.Commit{
			commit(shaA, "T-1 first\n\nRefs: T-2, T-3", t0, "a.go", "b.go"),
			commit(shaB, "Fixes T-10 and ledger:T-3", t0.Add(time.Minute), "c.go"),
			commit(shaC, "T-2 manual overlap", t0.Add(2*time.Minute), "a.go"),
			commit(shaD, "work on the branch", t0.Add(30*time.Minute), "d.go", "a.go"),
			commit(shaE, "later work", t0.Add(3*time.Hour), "d.go"),
			commit(shaF, "Closes T-11 and refs T-1", t0.Add(10*time.Minute), "e.go"),
		},
		Sessions: []SessionInfo{
			{ID: sessX, ProjectID: "drashta", Start: start, End: end, Title: "T-11 work",
				UserTexts: []string{"look at task:T-3", "also T-1", "and ledger:T-3"},
				Edited:    []string{".drashta/tasks/T-11.md", "a.go", "d.go", "~/elsewhere/x.go"},
				Commands:  []string{"drashta task set T-2 status=review", "ls"}},
			{ID: sessY, ProjectID: "drashta", Start: start, End: end, Title: "T-3 again", Edited: []string{"b.go"}},
		},
		Branches: []BranchInfo{
			{Name: "feat/T-11-new-thing", Commits: []string{shaD, shaE[:9]}},
			{Name: "main", Default: true, Commits: []string{shaA}},
		},
		Snapshots: []SnapshotInfo{
			{TaskID: "T-1", Version: 1, Commit: shaD[:10]},
			{TaskID: "T-2", Version: 1, Commit: shaE},
		},
		Others: []ProjectTasks{
			{ProjectID: "ledger", Tasks: []TaskInfo{{ID: "T-3"}, {ID: "T-4"}}},
			{ProjectID: "tools", Tasks: []TaskInfo{{ID: "T-1"}}},
		},
	}
}

func shuffled(in Input, rng *rand.Rand) Input {
	out := in
	shuf := func(n int, swap func(i, j int)) { rng.Shuffle(n, swap) }

	out.Tasks = append([]TaskInfo(nil), in.Tasks...)
	shuf(len(out.Tasks), func(i, j int) { out.Tasks[i], out.Tasks[j] = out.Tasks[j], out.Tasks[i] })
	for i := range out.Tasks {
		t := &out.Tasks[i]
		t.Manual = append([]string(nil), t.Manual...)
		t.Unlinks = append([]string(nil), t.Unlinks...)
		shuf(len(t.Manual), func(a, b int) { t.Manual[a], t.Manual[b] = t.Manual[b], t.Manual[a] })
	}
	out.Commits = append([]probe.Commit(nil), in.Commits...)
	shuf(len(out.Commits), func(i, j int) { out.Commits[i], out.Commits[j] = out.Commits[j], out.Commits[i] })
	for i := range out.Commits {
		c := &out.Commits[i]
		c.Files = append([]probe.CommitFile(nil), c.Files...)
		shuf(len(c.Files), func(a, b int) { c.Files[a], c.Files[b] = c.Files[b], c.Files[a] })
	}
	out.Sessions = append([]SessionInfo(nil), in.Sessions...)
	shuf(len(out.Sessions), func(i, j int) { out.Sessions[i], out.Sessions[j] = out.Sessions[j], out.Sessions[i] })
	for i := range out.Sessions {
		s := &out.Sessions[i]
		s.Edited = append([]string(nil), s.Edited...)
		s.UserTexts = append([]string(nil), s.UserTexts...)
		s.Commands = append([]string(nil), s.Commands...)
		shuf(len(s.Edited), func(a, b int) { s.Edited[a], s.Edited[b] = s.Edited[b], s.Edited[a] })
		shuf(len(s.UserTexts), func(a, b int) { s.UserTexts[a], s.UserTexts[b] = s.UserTexts[b], s.UserTexts[a] })
		shuf(len(s.Commands), func(a, b int) { s.Commands[a], s.Commands[b] = s.Commands[b], s.Commands[a] })
	}
	out.Branches = append([]BranchInfo(nil), in.Branches...)
	shuf(len(out.Branches), func(i, j int) { out.Branches[i], out.Branches[j] = out.Branches[j], out.Branches[i] })
	for i := range out.Branches {
		b := &out.Branches[i]
		b.Commits = append([]string(nil), b.Commits...)
		shuf(len(b.Commits), func(x, y int) { b.Commits[x], b.Commits[y] = b.Commits[y], b.Commits[x] })
	}
	out.Snapshots = append([]SnapshotInfo(nil), in.Snapshots...)
	shuf(len(out.Snapshots), func(i, j int) { out.Snapshots[i], out.Snapshots[j] = out.Snapshots[j], out.Snapshots[i] })
	out.Others = append([]ProjectTasks(nil), in.Others...)
	shuf(len(out.Others), func(i, j int) { out.Others[i], out.Others[j] = out.Others[j], out.Others[i] })
	for i := range out.Others {
		o := &out.Others[i]
		o.Tasks = append([]TaskInfo(nil), o.Tasks...)
		shuf(len(o.Tasks), func(a, b int) { o.Tasks[a], o.Tasks[b] = o.Tasks[b], o.Tasks[a] })
	}
	return out
}

func TestDeterministicWhateverTheInputOrder(t *testing.T) {
	base := richInput()
	expLinks := Links(base)
	expProps := Proposals(base)
	expPairs := SessionCommits(base.Sessions, base.Commits)
	if len(expLinks) < 10 {
		t.Fatalf("the rich input should produce many links, got %d: %+v", len(expLinks), expLinks)
	}
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 200; n++ {
		in := shuffled(base, rng)
		if got := Links(in); !reflect.DeepEqual(got, expLinks) {
			t.Fatalf("run %d: Links changed with the input order\n got  %+v\n want %+v", n, got, expLinks)
		}
		if got := Proposals(in); !reflect.DeepEqual(got, expProps) {
			t.Fatalf("run %d: Proposals changed with the input order\n got  %+v\n want %+v", n, got, expProps)
		}
		if got := SessionCommits(in.Sessions, in.Commits); !reflect.DeepEqual(got, expPairs) {
			t.Fatalf("run %d: SessionCommits changed with the input order", n)
		}
	}
}

// What the rich input must produce, rule by rule. It also pins the output as a whole.
func TestRichInputExpectations(t *testing.T) {
	d := "drashta/"
	want(t, Links(richInput()), map[string]string{
		d + "T-1 commit:" + shaA:   ReasonCommitMessage, // message
		d + "T-1 commit:" + shaC:   ReasonSessionOutput, // session X edited a.go
		d + "T-1 commit:" + shaD:   ReasonSnapshot,      // snapshot beats session output
		d + "T-1 commit:" + shaE:   ReasonSessionOutput,
		d + "T-1 commit:" + shaF:   ReasonCommitMessage,
		d + "T-1 session:" + sessX: ReasonSessionText,

		d + "T-2 commit:" + shaA: ReasonCommitMessage, // trailer "Refs: T-2, T-3"
		// commit C is a manual link of T-2 (short id), so no auto link for it
		d + "T-2 commit:" + shaD:   ReasonSessionOutput,
		d + "T-2 commit:" + shaE:   ReasonSnapshot,
		d + "T-2 session:" + sessX: ReasonSessionEdit, // the drashta CLI command

		d + "T-3 commit:" + shaA:   ReasonCommitMessage,
		d + "T-3 commit:" + shaC:   ReasonSessionOutput,
		d + "T-3 commit:" + shaD:   ReasonSessionOutput,
		d + "T-3 commit:" + shaE:   ReasonSessionOutput,
		d + "T-3 session:" + sessX: ReasonSessionText,
		// session Y is in the unlinks of T-3: no session link and no output of Y

		d + "T-10 commit:" + shaB: ReasonCommitMessage,

		d + "T-11 commit:" + shaA:   ReasonSessionOutput,
		d + "T-11 commit:" + shaC:   ReasonSessionOutput,
		d + "T-11 commit:" + shaD:   ReasonBranch, // branch beats session output
		d + "T-11 commit:" + shaE:   ReasonBranch, // short branch id resolves to the full id
		d + "T-11 commit:" + shaF:   ReasonCommitMessage,
		d + "T-11 session:" + sessX: ReasonSessionEdit, // edit beats text

		"ledger/T-3 drashta:commit:" + shaB:   ReasonCommitMessage,
		"ledger/T-3 drashta:session:" + sessX: ReasonSessionText,
	})
}

func TestRichInputProposals(t *testing.T) {
	got := Proposals(richInput())
	exp := []Proposal{{
		Kind: "close_task", ProjectID: "drashta", TaskID: "T-11",
		TaskRef: "task:T-11", CommitRef: "commit:" + shaF,
	}}
	// T-10 is "Fixes"-ed by commit B but its status is done: no proposal.
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("Proposals = %+v\nwant %+v", got, exp)
	}
}
