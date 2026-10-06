package git

import (
	"context"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// These tests cover probe.DayCounts. They live here because the probe package
// may only gain one new file (daycount.go), and the git fixtures are here.

func TestDayCountsAcrossTimezoneBoundary(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	pst := time.FixedZone("PST", -8*3600)
	// 2026-10-01 01:30 IST is 2026-09-30 20:00 UTC and 2026-09-30 12:00 PST.
	commits := []probe.Commit{
		{Time: time.Date(2026, 10, 1, 1, 30, 0, 0, ist), Plus: 10, Minus: 2},
		{Time: time.Date(2026, 10, 1, 9, 0, 0, 0, ist), Plus: 1, Minus: 1},
		{Time: time.Date(2026, 9, 30, 23, 59, 59, 0, ist), Plus: 5},
		{}, // zero time is skipped
	}

	inIST := probe.DayCounts(commits, ist)
	if len(inIST) != 2 {
		t.Fatalf("IST days = %v", inIST)
	}
	if got := inIST["2026-10-01"]; got != (probe.DayCount{Commits: 2, Plus: 11, Minus: 3}) {
		t.Errorf("IST 10-01 = %+v", got)
	}
	if got := inIST["2026-09-30"]; got != (probe.DayCount{Commits: 1, Plus: 5}) {
		t.Errorf("IST 09-30 = %+v", got)
	}

	inUTC := probe.DayCounts(commits, time.UTC)
	if got := inUTC["2026-09-30"]; got.Commits != 2 || got.Plus != 15 {
		t.Errorf("UTC 09-30 = %+v (the 01:30 IST commit belongs to the day before)", got)
	}
	if got := inUTC["2026-10-01"]; got.Commits != 1 {
		t.Errorf("UTC 10-01 = %+v", got)
	}

	inPST := probe.DayCounts(commits, pst)
	if len(inPST) != 1 || inPST["2026-09-30"].Commits != 3 {
		t.Errorf("PST days = %v", inPST)
	}

	if got := probe.DayCounts(commits, nil); len(got) == 0 { // nil means time.Local
		t.Error("nil zone should fall back to the local zone")
	}
	if got := probe.DayCounts(nil, ist); len(got) != 0 {
		t.Errorf("no commits should give an empty map, got %v", got)
	}
}

// Real git commits made late in the evening in one zone fall on the next day in another.
func TestDayCountsFromScan(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	write(t, dir, "a.txt", "one\n")
	commit(t, dir, "2026-09-30T23:30:00+05:30", "late evening IST")
	write(t, dir, "a.txt", "one\ntwo\nthree\n")
	commit(t, dir, "2026-10-01T01:30:00+05:30", "after midnight IST")

	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	ist := time.FixedZone("IST", 5*3600+1800)
	got := probe.DayCounts(res.Commits, ist)
	if got["2026-09-30"].Commits != 1 || got["2026-10-01"].Commits != 1 || got["2026-10-01"].Plus != 2 {
		t.Errorf("IST counts = %v", got)
	}
	got = probe.DayCounts(res.Commits, time.UTC)
	if len(got) != 1 || got["2026-09-30"].Commits != 2 || got["2026-09-30"].Plus != 3 {
		t.Errorf("UTC counts = %v (both commits are on 09-30 in UTC)", got)
	}
}
