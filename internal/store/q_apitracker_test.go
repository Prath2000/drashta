package store

import (
	"context"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

func TestTrkQueries(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	ts := time.Unix(1700000000, 0)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.ReplaceCommits(ctx, "p_1", []probe.Commit{
		{ID: "abcdef1234", VCS: "git", Msg: "old", Time: ts},
		{ID: "abcdef9999", VCS: "git", Msg: "new", Time: ts.Add(time.Hour), Files: []probe.CommitFile{{Path: "a.go", Plus: 1}}},
	}))
	c, ok, err := db.TrkCommit(ctx, "p_1", "ABCDEF")
	if err != nil || !ok || c.Msg != "new" || len(c.Files) != 1 {
		t.Fatalf("commit prefix: %+v %v %v", c, ok, err)
	}
	if _, ok, _ := db.TrkCommit(ctx, "p_1", "abcdef1"); !ok {
		t.Fatal("exact prefix not found")
	}
	if _, ok, _ := db.TrkCommit(ctx, "pX1", "abcdef"); ok {
		t.Fatal("LIKE wildcard leaked into the project id")
	}
	if _, ok, _ := db.TrkCommit(ctx, "p_1", "abc%"); ok {
		t.Fatal("percent must not be a wildcard")
	}

	must(db.UpsertSessions(ctx, []Session{{ID: "121c3b19-ada3-495e-a518-7fe5acf0a71e", Profile: ".claude", ProjectID: "p_1",
		Start: ts, End: ts.Add(time.Minute), Title: "s", Tools: map[string]int{"Edit": 2}, Edited: []string{"a.go"}}}))
	s, ok, err := db.TrkSession(ctx, "121c3b19")
	if err != nil || !ok || s.Tools["Edit"] != 2 || len(s.Edited) != 1 || !s.End.Equal(ts.Add(time.Minute)) {
		t.Fatalf("session: %+v %v %v", s, ok, err)
	}

	must(db.ReplaceFiles(ctx, "p_1", []File{{Path: "a.go", Size: 3, MTime: ts, Kind: "code", Lines: 1}}))
	f, ok, err := db.TrkFile(ctx, "p_1", "a.go")
	if err != nil || !ok || f.Lines != 1 {
		t.Fatalf("file: %+v %v %v", f, ok, err)
	}
	if _, ok, _ := db.TrkFile(ctx, "p_1", "b.go"); ok {
		t.Fatal("missing file found")
	}

	must(db.ReplaceServersNow(ctx, []ServerNow{{Key: "/x|npm", Port: 5173, ProjectID: "p_1", FirstSeen: ts}}))
	srv, err := db.TrkServersNow(ctx)
	if err != nil || len(srv) != 1 || srv[0].Port != 5173 || !srv[0].FirstSeen.Equal(ts) {
		t.Fatalf("servers: %+v %v", srv, err)
	}

	must(db.UpsertAuthoredTaskLinks(ctx, "p_1", "T-1", "auto", []TaskLink{{Ref: "commit:abcdef1", Reason: "commit message"}}))
	must(db.UpsertAuthoredTaskLinks(ctx, "p_1", "T-2", "manual", []TaskLink{{Ref: "commit:abcdef9"}}))
	must(db.UpsertAuthoredTaskLinks(ctx, "other", "T-5", "manual", []TaskLink{{Ref: "p_1:commit:abcdef9"}, {Ref: "file:x"}}))
	auto, err := db.TrkAutoLinks(ctx, "p_1")
	if err != nil || len(auto) != 1 || auto["T-1"][0].Reason != "commit message" {
		t.Fatalf("auto: %v %v", auto, err)
	}
	links, err := db.TrkLinksByType(ctx, "p_1", "commit")
	if err != nil || len(links) != 3 {
		t.Fatalf("links by type: %+v %v", links, err)
	}
}
