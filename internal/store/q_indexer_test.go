package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

func TestIdxRebuildHistoryEventsAfterReindex(t *testing.T) {
	db := openTest(t)
	loc := time.FixedZone("IST", 5*3600+1800)
	start := time.Date(2026, 9, 20, 9, 0, 0, 0, loc)
	if _, err := db.AppendServerRun(bg(), ServerRun{Key: "/p/alpha|node vite", ProjectID: "alpha", Port: 5173,
		PID: 7, Name: "node", Started: start, Ended: start.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendServerRun(bg(), ServerRun{Key: "/p/beta|go run .", ProjectID: "beta", Port: 8080,
		Name: "go", Started: start}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFileChanges(bg(), "alpha", "2026-09-20", []string{"a.go", "b.go", "a.go"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Reindex(bg()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM events`); n != 0 {
		t.Fatalf("events after reindex = %d", n)
	}
	for round := 0; round < 2; round++ { // safe to repeat
		if err := db.RebuildHistoryEvents(bg(), loc); err != nil {
			t.Fatal(err)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM events WHERE kind='server_up'`); n != 2 {
			t.Fatalf("server_up = %d", n)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM events WHERE kind='server_down' AND project_id='alpha'`); n != 1 {
			t.Fatalf("server_down = %d", n)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM events WHERE kind='file_burst' AND project_id='alpha'
			AND day='2026-09-20' AND ref='files:2026-09-20' AND label='2 files changed'`); n != 1 {
			t.Fatalf("file_burst rows = %d", n)
		}
	}
}

func TestIdxDropCommitsRemovesEventsAndSearch(t *testing.T) {
	db := openTest(t)
	c := probe.Commit{ID: "aaaaaaa1111111111111111111111111111111aa", VCS: "git", Msg: "rewritten lantern", Time: time.Unix(1_790_000_000, 0),
		Files: []probe.CommitFile{{Path: "x.go", Plus: 1}}}
	mustWrite(t, db, func(tx *sql.Tx) error {
		if err := ReplaceCommitsTx(bg(), tx, "alpha", []probe.Commit{c}); err != nil {
			return err
		}
		_, err := AddEventTx(bg(), tx, Event{ProjectID: "alpha", TS: c.Time, Kind: "commit", Ref: "commit:" + c.ID, Label: c.Msg})
		return err
	})
	if hits, _ := db.Search(bg(), "lantern", "", "commit", 5); len(hits) != 1 {
		t.Fatal("setup: commit not searchable")
	}
	mustWrite(t, db, func(tx *sql.Tx) error { return IdxDropCommitsTx(bg(), tx, "alpha", []string{c.ID}) })
	for _, q := range []string{`SELECT COUNT(*) FROM commits`, `SELECT COUNT(*) FROM commit_files`, `SELECT COUNT(*) FROM events`} {
		if n := count(t, db, q); n != 0 {
			t.Errorf("%s = %d", q, n)
		}
	}
	if hits, _ := db.Search(bg(), "lantern", "", "commit", 5); len(hits) != 0 {
		t.Errorf("search still finds the dropped commit")
	}
}

func TestIdxReplaceAutoLinksKeepsOtherProjectsRows(t *testing.T) {
	db := openTest(t)
	links := func(project string) []string {
		rows, err := db.Read().Query(`SELECT task_id || ' ' || ref || ' ' || source FROM task_links WHERE project_id = ? ORDER BY 1`, project)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			out = append(out, s)
		}
		return out
	}
	// a manual link in alpha's T-1, equal to an auto ref: manual must win
	if err := db.UpsertAuthoredTaskLinks(bg(), "alpha", "T-1", "manual", []TaskLink{{Ref: "commit:m1"}}); err != nil {
		t.Fatal(err)
	}
	// beta's run: a cross link into alpha's T-1
	mustWrite(t, db, func(tx *sql.Tx) error {
		return IdxReplaceAutoLinksTx(bg(), tx, "beta", nil, []IdxAutoLink{{ProjectID: "alpha", TaskID: "T-1", Ref: "beta:commit:b1", Reason: "commit message"}})
	})
	// alpha's run: own links (one equals the manual ref)
	mustWrite(t, db, func(tx *sql.Tx) error {
		return IdxReplaceAutoLinksTx(bg(), tx, "alpha", []IdxAutoLink{
			{ProjectID: "alpha", TaskID: "T-1", Ref: "commit:a1", Reason: "commit message"},
			{ProjectID: "alpha", TaskID: "T-1", Ref: "commit:m1", Reason: "commit message"},
			{ProjectID: "alpha", TaskID: "T-2", Ref: "session:s1", Reason: "session text"},
		}, nil)
	})
	want := []string{"T-1 beta:commit:b1 auto", "T-1 commit:a1 auto", "T-1 commit:m1 manual", "T-2 session:s1 auto"}
	if got := links("alpha"); len(got) != len(want) {
		t.Fatalf("links = %v, want %v", got, want)
	} else {
		for n := range want {
			if got[n] != want[n] {
				t.Fatalf("links = %v, want %v", got, want)
			}
		}
	}
	// alpha again with fewer own links: beta's cross row stays, T-2's link goes
	mustWrite(t, db, func(tx *sql.Tx) error {
		return IdxReplaceAutoLinksTx(bg(), tx, "alpha", []IdxAutoLink{{ProjectID: "alpha", TaskID: "T-1", Ref: "commit:a1", Reason: "commit message"}}, nil)
	})
	if got := links("alpha"); len(got) != 3 || got[0] != "T-1 beta:commit:b1 auto" {
		t.Fatalf("after alpha's second run: %v", got)
	}
	// beta again with no cross links: only beta's row goes
	mustWrite(t, db, func(tx *sql.Tx) error { return IdxReplaceAutoLinksTx(bg(), tx, "beta", nil, nil) })
	if got := links("alpha"); len(got) != 2 || got[0] != "T-1 commit:a1 auto" {
		t.Fatalf("after beta's second run: %v", got)
	}
	// a project id with "_" is not a LIKE wildcard
	mustWrite(t, db, func(tx *sql.Tx) error {
		return IdxReplaceAutoLinksTx(bg(), tx, "a_b", nil, []IdxAutoLink{{ProjectID: "alpha", TaskID: "T-1", Ref: "a_b:commit:z", Reason: "x"}})
	})
	mustWrite(t, db, func(tx *sql.Tx) error {
		return IdxReplaceAutoLinksTx(bg(), tx, "axb", nil, nil)
	})
	if n := count(t, db, `SELECT COUNT(*) FROM task_links WHERE ref='a_b:commit:z'`); n != 1 {
		t.Fatalf("a run of project axb removed a_b's link")
	}
}

func TestIdxServerRunHelpers(t *testing.T) {
	db := openTest(t)
	t0 := time.Unix(1_790_000_000, 0)
	var id int64
	mustWrite(t, db, func(tx *sql.Tx) error {
		var err error
		id, err = IdxAppendServerRunTx(bg(), tx, ServerRun{Key: "k", ProjectID: "alpha", Port: 1, Started: t0, LastSeen: t0})
		return err
	})
	if err := db.IdxTouchServerRuns(bg(), t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var open map[string]ServerRun
	mustWrite(t, db, func(tx *sql.Tx) error {
		var err error
		open, err = IdxOpenServerRunsTx(bg(), tx)
		return err
	})
	if r := open["k"]; r.ID != id || !r.LastSeen.Equal(t0.Add(time.Minute)) {
		t.Fatalf("open run = %+v", r)
	}
	mustWrite(t, db, func(tx *sql.Tx) error { return IdxEndServerRunTx(bg(), tx, id, t0.Add(2*time.Minute)) })
	latest, err := db.IdxLatestServerRuns(bg())
	if err != nil || len(latest) != 1 || latest[0].Ended.IsZero() {
		t.Fatalf("latest = %+v %v", latest, err)
	}
	c, err := db.IdxCounts(bg())
	if err != nil || c != (IdxCounts{}) {
		t.Fatalf("counts on an empty index = %+v %v", c, err)
	}
}
