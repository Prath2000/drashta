package autolink

import (
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

var t0 = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

// Invented full commit ids (40 hex).
const (
	shaA = "aaaaaaa1111111111111111111111111111111aa"
	shaB = "bbbbbbb2222222222222222222222222222222bb"
	shaC = "ccccccc3333333333333333333333333333333cc"
	shaD = "ddddddd4444444444444444444444444444444dd"
	shaE = "eeeeeee5555555555555555555555555555555ee"
)

// shaF is a sixth invented commit id.
var shaF = "fffffff" + strings.Repeat("6", 31) + "ff"

// Invented session ids.
const (
	sessX = "121c3b19-ada3-495e-a518-7fe5acf0a71e"
	sessY = "9f8e7d6c-1111-4222-8333-444455556666"
)

func commit(id, msg string, at time.Time, files ...string) probe.Commit {
	c := probe.Commit{ID: id, VCS: "git", Author: "alice", Time: at, Msg: msg}
	if i := strings.Index(msg, "\n\n"); i >= 0 {
		c.Msg, c.Body = msg[:i], msg[i+2:]
	}
	for _, f := range files {
		c.Files = append(c.Files, probe.CommitFile{Path: f, Plus: 1})
	}
	return c
}

func tasks(ids ...string) []TaskInfo {
	var out []TaskInfo
	for _, id := range ids {
		out = append(out, TaskInfo{ID: id, Title: "task " + id, Status: "in_progress"})
	}
	return out
}

// linkSet turns links into "project/task -> ref (reason)" strings for easy checks.
func linkSet(links []Link) map[string]string {
	m := map[string]string{}
	for _, l := range links {
		m[l.ProjectID+"/"+l.TaskID+" "+l.Ref] = l.Reason
	}
	return m
}

// want checks that links hold exactly the wanted entries: key "proj/T-1 ref" -> reason.
func want(t *testing.T, links []Link, exp map[string]string) {
	t.Helper()
	got := linkSet(links)
	for k, r := range exp {
		if got[k] != r {
			t.Errorf("missing or wrong link %q: got reason %q, want %q\nall links: %v", k, got[k], r, got)
		}
	}
	for k, r := range got {
		if _, ok := exp[k]; !ok {
			t.Errorf("unexpected link %q (%s)", k, r)
		}
	}
	for _, l := range links {
		if l.Source != "auto" {
			t.Errorf("Source = %q", l.Source)
		}
	}
}
