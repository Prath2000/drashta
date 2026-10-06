package autolink

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

func TestCommitMessageRule(t *testing.T) {
	in := Input{
		ProjectID: "drashta",
		Tasks:     tasks("T-12", "T-14", "T-3"),
		Commits: []probe.Commit{
			commit(shaA, "Add chart tooltips (T-12)", t0),
			commit(shaB, "Tidy styles\n\nTask: T-14", t0),
			commit(shaC, "Many tasks\n\nRefs: T-12, T-3", t0),
			commit(shaD, "lower case t-3 works", t0),
			commit(shaE, "Words around: ST-12, T-123, T-12abc, T-99 are not links", t0),
		},
	}
	want(t, Links(in), map[string]string{
		"drashta/T-12 commit:" + shaA: ReasonCommitMessage,
		"drashta/T-14 commit:" + shaB: ReasonCommitMessage,
		"drashta/T-12 commit:" + shaC: ReasonCommitMessage,
		"drashta/T-3 commit:" + shaC:  ReasonCommitMessage,
		"drashta/T-3 commit:" + shaD:  ReasonCommitMessage,
	})
}

func TestCommitMessageUppercaseHashAndOddIDs(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-1"),
		Commits: []probe.Commit{
			commit("ABCDEF1234567890ABCDEF1234567890ABCDEF12", "T-1 upper hash", t0),
			commit("", "T-1 no id", t0),
			commit("xyz", "T-1 bad id", t0),
		},
	}
	want(t, Links(in), map[string]string{
		"p/T-1 commit:abcdef1234567890abcdef1234567890abcdef12": ReasonCommitMessage,
	})
}

func TestBranchRule(t *testing.T) {
	in := Input{
		ProjectID: "drashta",
		Tasks:     tasks("T-12", "T-14", "T-2"),
		Commits:   []probe.Commit{commit(shaE, "wip", t0)},
		Branches: []BranchInfo{
			{Name: "t-12-chart-tooltips", Commits: []string{shaA, shaB}},
			{Name: "T-14/cleanup", Commits: []string{shaC}},
			{Name: "feat/T-2-thing", Commits: []string{shaD}},
			{Name: "fix_t-14", Commits: []string{shaD}},
			{Name: "main", Default: true, Commits: []string{shaE}},
			{Name: "T-12-on-default", Default: true, Commits: []string{shaE}},
			{Name: "release-2026", Commits: []string{shaE}},
			{Name: "ST-12-nope", Commits: []string{shaE}},
			{Name: "T-99-no-such-task", Commits: []string{shaE}},
		},
	}
	want(t, Links(in), map[string]string{
		"drashta/T-12 commit:" + shaA: ReasonBranch,
		"drashta/T-12 commit:" + shaB: ReasonBranch,
		"drashta/T-14 commit:" + shaC: ReasonBranch,
		"drashta/T-2 commit:" + shaD:  ReasonBranch,
		"drashta/T-14 commit:" + shaD: ReasonBranch,
	})
}

func TestBranchTwoTasksAndShortIDs(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-1", "T-2"),
		Commits:   []probe.Commit{commit(shaA, "no mention here", t0)},
		Branches:  []BranchInfo{{Name: "T-1-T-2-combo", Commits: []string{shaA[:7], shaB[:10]}}},
	}
	// shaA[:7] resolves to the full id found in Input.Commits; shaB[:10] has no full id and stays short.
	want(t, Links(in), map[string]string{
		"p/T-1 commit:" + shaA:      ReasonBranch,
		"p/T-2 commit:" + shaA:      ReasonBranch,
		"p/T-1 commit:" + shaB[:10]: ReasonBranch,
		"p/T-2 commit:" + shaB[:10]: ReasonBranch,
	})
}

func TestCommitMessageBeatsBranchAndDoesNotDuplicate(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-12"),
		Commits:   []probe.Commit{commit(shaA, "T-12 first", t0), commit(shaB, "second", t0)},
		Branches:  []BranchInfo{{Name: "t-12-x", Commits: []string{shaA, shaB}}},
		Snapshots: []SnapshotInfo{{TaskID: "T-12", Version: 1, Commit: shaA}},
	}
	got := Links(in)
	want(t, got, map[string]string{
		"p/T-12 commit:" + shaA: ReasonCommitMessage,
		"p/T-12 commit:" + shaB: ReasonBranch,
	})
	if len(got) != 2 {
		t.Errorf("one link per (task, ref); got %d", len(got))
	}
}

func TestSnapshotRule(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-1", "T-2"),
		Commits:   []probe.Commit{commit(shaA, "x", t0)},
		Snapshots: []SnapshotInfo{
			{TaskID: "T-1", Version: 3, Commit: shaA[:10]}, // short: resolved to the full id
			{TaskID: "T-2", Version: 1, Commit: "b7dc920bef"},
			{TaskID: "T-2", Version: 2, Commit: ""},
			{TaskID: "T-2", Version: 3, Commit: "zzzz"},
			{TaskID: "T-9", Version: 1, Commit: shaB}, // no such task
		},
	}
	want(t, Links(in), map[string]string{
		"p/T-1 commit:" + shaA:    ReasonSnapshot,
		"p/T-2 commit:b7dc920bef": ReasonSnapshot,
	})
}

func TestSessionTextRule(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-12", "T-4", "T-5", "T-6"),
		Sessions: []SessionInfo{
			{ID: sessX, ProjectID: "p", Title: "Work on T-12", UserTexts: []string{"please look at task:T-4", "and TASK:t-5 too"}},
			{ID: sessY, ProjectID: "p", UserTexts: []string{"lower case t-6 alone is ignored", "ST-12 is not a mention"}},
			{ID: "aaaaaaaa-1111-4222-8333-444455556666", ProjectID: "other", Title: "T-12 in another project's session"},
			{ID: "bbbbbbbb-1111-4222-8333-444455556666", ProjectID: "", Title: "T-6 with unknown project"},
			{ID: "not-a-session-id", ProjectID: "p", Title: "T-12"},
		},
	}
	want(t, Links(in), map[string]string{
		"p/T-12 session:" + sessX:                            ReasonSessionText,
		"p/T-4 session:" + sessX:                             ReasonSessionText,
		"p/T-5 session:" + sessX:                             ReasonSessionText,
		"p/T-6 session:bbbbbbbb-1111-4222-8333-444455556666": ReasonSessionText,
	})
}

func TestSessionEditRule(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-12", "T-7", "T-8", "T-9"),
		Sessions: []SessionInfo{
			{ID: sessX, ProjectID: "p", Title: "T-12 chat", Edited: []string{
				".drashta/tasks/T-12.md",                   // edit beats text for the same pair
				".drashta/snapshots/T-7/v2.png",            // snapshot folder of T-7
				".drashta/notes/N-1.md",                    // notes name no task
				"~/projects/g/other/.drashta/tasks/T-9.md", // outside the project
				".drashta/tasks/T-99.md",                   // no such task
			}},
			{ID: sessY, ProjectID: "p", Commands: []string{
				"drashta task set T-8 status=review",
				"echo T-9",
				"/opt/homebrew/bin/drashta snap T-9 shot.png",
			}},
		},
	}
	want(t, Links(in), map[string]string{
		"p/T-12 session:" + sessX: ReasonSessionEdit,
		"p/T-7 session:" + sessX:  ReasonSessionEdit,
		"p/T-8 session:" + sessY:  ReasonSessionEdit,
		"p/T-9 session:" + sessY:  ReasonSessionEdit,
	})
}

func TestSessionOutputRule(t *testing.T) {
	start := t0
	end := t0.Add(90 * time.Minute)
	in := Input{
		ProjectID: "p",
		Tasks: []TaskInfo{
			{ID: "T-12", Status: "in_progress"},
			{ID: "T-13", Status: "in_progress", Manual: []string{"session:" + sessY[:8]}},
			{ID: "T-14", Status: "in_progress", Unlinks: []string{"session:" + sessX}},
		},
		Sessions: []SessionInfo{
			{ID: sessX, ProjectID: "p", Start: start, End: end, Title: "T-12 and T-14", Edited: []string{"a.go", "b.go"}},
			{ID: sessY, ProjectID: "p", Start: start, End: end, Title: "no task named", Edited: []string{"c.go"}},
		},
		Commits: []probe.Commit{
			commit(shaA, "inside a.go", start.Add(30*time.Minute), "a.go", "z.go"),
			commit(shaB, "after the session but inside 2h", end.Add(119*time.Minute), "b.go"),
			commit(shaC, "too late", end.Add(121*time.Minute), "a.go"),
			commit(shaD, "before the session", start.Add(-time.Minute), "a.go"),
			commit(shaE, "session Y file", start.Add(10*time.Minute), "c.go"),
		},
	}
	want(t, Links(in), map[string]string{
		// T-12: session X by text; its output commits A and B.
		"p/T-12 session:" + sessX: ReasonSessionText,
		"p/T-12 commit:" + shaA:   ReasonSessionOutput,
		"p/T-12 commit:" + shaB:   ReasonSessionOutput,
		// T-13: session Y was linked by hand (short id); its output commit E.
		"p/T-13 commit:" + shaE: ReasonSessionOutput,
		// T-14: session X is in its unlinks, so no session link and no output commits.
	})
}

func TestSessionOutputNeedsALinkedSession(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-1"),
		Sessions:  []SessionInfo{{ID: sessX, ProjectID: "p", Start: t0, End: t0.Add(time.Hour), Title: "nothing", Edited: []string{"a.go"}}},
		Commits:   []probe.Commit{commit(shaA, "no mention", t0.Add(time.Minute), "a.go")},
	}
	if got := Links(in); len(got) != 0 {
		t.Errorf("a session that is linked to no task must produce no output links: %+v", got)
	}
}

func TestSessionCommitsPairs(t *testing.T) {
	sessions := []SessionInfo{
		{ID: sessY, Start: t0, End: t0.Add(time.Hour), Edited: []string{"a.go", "~/x/y.go", "/abs/z.go"}},
		{ID: sessX, Start: t0, Edited: []string{"b.go"}},                                // End zero: End = Start
		{ID: "cccccccc-1111-4222-8333-444455556666", Start: t0, End: t0.Add(time.Hour)}, // edited nothing
		{ID: "dddddddd-1111-4222-8333-444455556666", Edited: []string{"a.go"}},          // no times
	}
	commits := []probe.Commit{
		commit(shaA, "a", t0.Add(3*time.Hour), "a.go"),             // exactly End+2h: inside
		commit(shaB, "b", t0.Add(2*time.Hour+time.Second), "b.go"), // X: End+2h+1s: outside
		commit(shaC, "c", t0.Add(time.Minute), "~/x/y.go"),         // outside paths never match
		commit(shaD, "d", t0.Add(time.Minute)),                     // no files
		commit(shaE, "e", t0.Add(time.Minute), "b.go", "a.go"),     // both sessions
	}
	got := SessionCommits(sessions, commits)
	exp := []SessionCommit{
		{SessionID: sessX, CommitID: shaE},
		{SessionID: sessY, CommitID: shaA},
		{SessionID: sessY, CommitID: shaE},
	}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("SessionCommits = %+v\nwant %+v", got, exp)
	}
}

func TestCrossProject(t *testing.T) {
	in := Input{
		ProjectID: "drashta",
		Tasks:     tasks("T-12", "T-1"),
		Others: []ProjectTasks{
			{ProjectID: "ledger", Tasks: []TaskInfo{
				{ID: "T-3"},
				{ID: "T-5", Manual: []string{"drashta:commit:" + shaB[:7]}},
				{ID: "T-6", Unlinks: []string{"drashta:session:" + sessX}},
			}},
		},
		Commits: []probe.Commit{
			commit(shaA, "Port the parser from ledger:T-3 and finish T-1", t0),
			commit(shaB, "Same for ledger:task:T-5", t0),
			commit(shaC, "Unknown project nope:T-12 must not hit the local task", t0),
			commit(shaD, "Own prefix drashta:T-12 is local", t0),
			commit(shaE, "Refs:T-1 trailer without space", t0),
		},
		Sessions: []SessionInfo{
			{ID: sessX, ProjectID: "drashta", Title: "see ledger:T-6 and ledger:T-3"},
		},
	}
	want(t, Links(in), map[string]string{
		"drashta/T-1 commit:" + shaA:          ReasonCommitMessage,
		"drashta/T-12 commit:" + shaD:         ReasonCommitMessage,
		"drashta/T-1 commit:" + shaE:          ReasonCommitMessage,
		"ledger/T-3 drashta:commit:" + shaA:   ReasonCommitMessage,
		"ledger/T-3 drashta:session:" + sessX: ReasonSessionText,
		// T-5: the manual link drashta:commit:<short> wins; T-6: the session is in its unlinks.
	})
}

func TestCrossFlagAndFrom(t *testing.T) {
	in := Input{
		ProjectID: "drashta",
		Tasks:     tasks("T-1"),
		Others:    []ProjectTasks{{ProjectID: "ledger", Tasks: tasks("T-3")}},
		Commits:   []probe.Commit{commit(shaA, "T-1 and ledger:T-3", t0)},
	}
	got := Links(in)
	if len(got) != 2 {
		t.Fatalf("links = %+v", got)
	}
	for _, l := range got {
		if l.From != "drashta" {
			t.Errorf("From = %q", l.From)
		}
		if l.Cross() != (l.ProjectID == "ledger") {
			t.Errorf("Cross() wrong for %+v", l)
		}
	}
}

func TestBranchTaskIDs(t *testing.T) {
	cases := map[string][]string{
		"t-12-chart":         {"T-12"},
		"T-12/x":             {"T-12"},
		"feat/T-12-x":        {"T-12"},
		"fix_t-3":            {"T-3"},
		"T-1-T-2-combo":      {"T-1", "T-2"},
		"T-1-and-T-1-again":  {"T-1"},
		"main":               nil,
		"ST-12":              nil,
		"release/2026-t-":    nil,
		"user/alex/T-7_work": {"T-7"},
	}
	for in, exp := range cases {
		if got := BranchTaskIDs(in); !reflect.DeepEqual(got, exp) {
			t.Errorf("BranchTaskIDs(%q) = %v, want %v", in, got, exp)
		}
	}
}

func TestManualAndUnlinkPrecedence(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks: []TaskInfo{
			{ID: "T-1", Manual: []string{"commit:" + shaA[:7]}},                  // short manual vs full auto
			{ID: "T-2", Manual: []string{"p:commit:" + shaA}},                    // qualified with own project
			{ID: "T-3", Unlinks: []string{"commit:" + shaA[:8]}},                 // rejected by short id
			{ID: "T-4", Manual: []string{"file:a.go#L3", "server:8090", "junk"}}, // unrelated manual links
			{ID: "T-5", Unlinks: []string{"other:commit:" + shaA}},               // a different project's commit
			{ID: "T-6", Manual: []string{"commit:" + shaB[:6]}},                  // too short a prefix is still a prefix >= 6
		},
		Commits: []probe.Commit{
			commit(shaA, "T-1 T-2 T-3 T-4 T-5", t0),
			commit(shaB, "T-6", t0),
		},
	}
	want(t, Links(in), map[string]string{
		"p/T-4 commit:" + shaA: ReasonCommitMessage,
		"p/T-5 commit:" + shaA: ReasonCommitMessage,
	})
}

func TestManualSessionBlocksAutoSession(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks: []TaskInfo{
			{ID: "T-1", Manual: []string{"session:" + sessX[:8]}},
			{ID: "T-2", Unlinks: []string{"session:" + sessX}},
			{ID: "T-3"},
		},
		Sessions: []SessionInfo{{ID: sessX, ProjectID: "p", Title: "T-1 T-2 T-3"}},
	}
	want(t, Links(in), map[string]string{
		"p/T-3 session:" + sessX: ReasonSessionText,
	})
}

func TestDuplicateAndCaseTaskIDs(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     []TaskInfo{{ID: "t-1"}, {ID: "T-1"}, {ID: " T-2 "}, {ID: "bogus"}, {ID: ""}},
		Commits:   []probe.Commit{commit(shaA, "T-1 T-1 t-1 T-2", t0)},
	}
	want(t, Links(in), map[string]string{
		"p/T-1 commit:" + shaA: ReasonCommitMessage,
		"p/T-2 commit:" + shaA: ReasonCommitMessage,
	})
}

func TestEmptyInput(t *testing.T) {
	if got := Links(Input{}); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
	if got := Links(Input{ProjectID: "p", Commits: []probe.Commit{commit(shaA, "T-1", t0)}}); len(got) != 0 {
		t.Errorf("no tasks, no links: %+v", got)
	}
}

func TestOrderIsNaturalAndStable(t *testing.T) {
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-10", "T-2", "T-1"),
		Commits: []probe.Commit{
			commit(shaB, "T-10 T-2 T-1", t0),
			commit(shaA, "T-10 T-2 T-1", t0),
		},
		Sessions: []SessionInfo{{ID: sessX, ProjectID: "p", Title: "T-2"}},
	}
	got := Links(in)
	var order []string
	for _, l := range got {
		order = append(order, l.TaskID+" "+l.Ref)
	}
	exp := []string{
		"T-1 commit:" + shaA, "T-1 commit:" + shaB,
		"T-2 commit:" + shaA, "T-2 commit:" + shaB, "T-2 session:" + sessX,
		"T-10 commit:" + shaA, "T-10 commit:" + shaB,
	}
	if !reflect.DeepEqual(order, exp) {
		t.Errorf("order = %v\nwant %v", order, exp)
	}
}

func TestFullyLoadedReasonsAndPriority(t *testing.T) {
	// One session, one commit, many signals for the same pair: the strongest reason wins.
	in := Input{
		ProjectID: "p",
		Tasks:     tasks("T-1"),
		Sessions: []SessionInfo{{
			ID: sessX, ProjectID: "p", Start: t0, End: t0.Add(time.Hour),
			Title: "T-1", Edited: []string{".drashta/tasks/T-1.md", "a.go"},
		}},
		Commits:  []probe.Commit{commit(shaA, "work", t0.Add(time.Minute), "a.go")},
		Branches: []BranchInfo{{Name: "T-1-x", Commits: []string{shaA}}},
	}
	want(t, Links(in), map[string]string{
		"p/T-1 session:" + sessX: ReasonSessionEdit,
		"p/T-1 commit:" + shaA:   ReasonBranch, // branch (3) beats session output (4)
	})
}

func TestInputIsNotChanged(t *testing.T) {
	in := richInput()
	snapshot := richInput()
	_ = Links(in)
	_ = Proposals(in)
	if !reflect.DeepEqual(in, snapshot) {
		t.Error("Links or Proposals changed their input")
	}
}

func TestConcurrentUse(t *testing.T) {
	in := richInput()
	first := Links(in)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := Links(in); !reflect.DeepEqual(got, first) {
				t.Error("concurrent Links gave a different result")
			}
			Proposals(in)
			SessionCommits(in.Sessions, in.Commits)
		}()
	}
	wg.Wait()
}
