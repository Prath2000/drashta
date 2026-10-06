package autolink

import (
	"reflect"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

func TestProposeClose(t *testing.T) {
	task := TaskInfo{ID: "T-12", Status: "review"}
	cases := []struct {
		name    string
		msg     string
		wantSHA string // "" means no proposal
	}{
		{"Fixes", "Handle empty list. Fixes T-12", shaA},
		{"Closes lower case", "closes t-12", shaA},
		{"Closed with colon", "Closed: T-12", shaA},
		{"Resolves task prefix", "Resolves task:T-12", shaA},
		{"Fixed in the body", "Tidy up\n\nMore text\nFixed T-12", shaA},
		{"fix without s", "fix T-12 for good", shaA},
		{"no keyword", "Refs T-12", ""},
		{"only a mention", "Work on T-12", ""},
		{"other task", "Fixes T-14", ""},
		{"list: only the first", "Fixes T-14, T-12", ""},
		{"similar word", "Prefixes T-12", ""},
		{"longer id", "Fixes T-123", ""},
		{"other project", "Fixes ledger:T-12", ""},
		{"keyword on the line before", "Fixes\nT-12", ""},
	}
	for _, c := range cases {
		got := ProposeClose(task, []probe.Commit{commit(shaA, c.msg, t0)})
		if c.wantSHA == "" {
			if got != nil {
				t.Errorf("%s: want no proposal, got %+v", c.name, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("%s: want a proposal", c.name)
			continue
		}
		exp := Proposal{Kind: "close_task", TaskID: "T-12", TaskRef: "task:T-12", CommitRef: "commit:" + c.wantSHA}
		if *got != exp {
			t.Errorf("%s: got %+v, want %+v", c.name, *got, exp)
		}
	}
}

func TestProposeCloseSkips(t *testing.T) {
	commits := []probe.Commit{commit(shaA, "Fixes T-12", t0)}

	if got := ProposeClose(TaskInfo{ID: "T-12", Status: "done"}, commits); got != nil {
		t.Errorf("a done task needs no proposal: %+v", got)
	}
	if got := ProposeClose(TaskInfo{ID: "T-12", Status: "DONE"}, commits); got != nil {
		t.Errorf("status is compared without case: %+v", got)
	}
	// The user unlinked that commit: no proposal (short id is enough).
	if got := ProposeClose(TaskInfo{ID: "T-12", Status: "review", Unlinks: []string{"commit:" + shaA[:7]}}, commits); got != nil {
		t.Errorf("an unlinked commit must not propose: %+v", got)
	}
	// An unlink of some other commit changes nothing.
	if got := ProposeClose(TaskInfo{ID: "T-12", Status: "review", Unlinks: []string{"commit:" + shaB[:7]}}, commits); got == nil {
		t.Error("unlinking another commit must not hide the proposal")
	}
	for _, id := range []string{"", "bogus", "T-", "12"} {
		if got := ProposeClose(TaskInfo{ID: id, Status: "todo"}, commits); got != nil {
			t.Errorf("task id %q: %+v", id, got)
		}
	}
	if got := ProposeClose(TaskInfo{ID: "T-12"}, nil); got != nil {
		t.Errorf("no commits: %+v", got)
	}
	if got := ProposeClose(TaskInfo{ID: "T-12"}, []probe.Commit{commit("zz", "Fixes T-12", t0)}); got != nil {
		t.Errorf("a commit with a bad id cannot be linked: %+v", got)
	}
}

func TestProposeCloseUsesTheNewestCommit(t *testing.T) {
	task := TaskInfo{ID: "T-12", Status: "in_progress"}
	commits := []probe.Commit{
		commit(shaB, "Closes T-12", t0.Add(time.Hour)),
		commit(shaA, "Fixes T-12", t0),
		commit(shaC, "Fixes T-12 again", t0.Add(time.Hour)), // same time as B: smaller id wins
	}
	got := ProposeClose(task, commits)
	if got == nil || got.CommitRef != "commit:"+shaB {
		t.Fatalf("got %+v", got)
	}
	// The user rejects the newest one: the next one is proposed.
	task.Unlinks = []string{"commit:" + shaB}
	got = ProposeClose(task, commits)
	if got == nil || got.CommitRef != "commit:"+shaC {
		t.Fatalf("after unlinking B: %+v", got)
	}
}

func TestProposalsForAProject(t *testing.T) {
	in := Input{
		ProjectID: "drashta",
		Tasks: []TaskInfo{
			{ID: "T-10", Status: "todo"},
			{ID: "T-2", Status: "review"},
			{ID: "T-3", Status: "done"},
			{ID: "T-4", Status: "todo"},
			{ID: "T-2", Status: "todo"}, // duplicate id: the first one counts
		},
		Commits: []probe.Commit{
			commit(shaA, "Fixes T-10", t0),
			commit(shaB, "Closes drashta:T-2", t0), // own project name in front is fine
			commit(shaC, "Closes T-3", t0),         // already done
			commit(shaD, "Fixes tools:T-4", t0),    // another project's task
		},
	}
	got := Proposals(in)
	exp := []Proposal{
		{Kind: "close_task", ProjectID: "drashta", TaskID: "T-2", TaskRef: "task:T-2", CommitRef: "commit:" + shaB},
		{Kind: "close_task", ProjectID: "drashta", TaskID: "T-10", TaskRef: "task:T-10", CommitRef: "commit:" + shaA},
	}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("Proposals = %+v\nwant %+v", got, exp)
	}
}
