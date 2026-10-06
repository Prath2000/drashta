package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// istLoc is the "user's clock" of these tests.
var istLoc = time.FixedZone("IST", 5*3600+1800)

// fixtureCommit is one commit of the fast-import fixture.
type fixtureCommit struct {
	when    time.Time // with the zone git should record
	add     int       // lines in a new text file
	delPrev bool      // delete the file of the commit before (its lines count as minus)
	binary  bool      // also add a binary file (0 lines)
}

// buildHistory writes many commits with one `git fast-import` process (one
// process per commit would make the test slow). It returns the true per-day
// numbers in loc, counted from the fixture itself, and the newest commit time.
// A side branch with one commit is merged in at the end; the merge commit counts
// as a commit with 0 lines, like Scan.
func buildHistory(t *testing.T, dir string, list []fixtureCommit, loc *time.Location) map[string]probe.DayCount {
	t.Helper()
	var b bytes.Buffer
	want := map[string]probe.DayCount{}
	sizes := map[string]int{}
	day := func(tm time.Time) string { return tm.In(loc).Format("2006-01-02") }
	stamp := func(tm time.Time) string { return fmt.Sprintf("%d %s", tm.Unix(), tm.Format("-0700")) }
	data := func(s string) { fmt.Fprintf(&b, "data %d\n%s\n", len(s), s) }
	for i, c := range list {
		fmt.Fprintf(&b, "commit refs/heads/main\nmark :%d\n", i+1)
		fmt.Fprintf(&b, "author A <a@example.com> %s\ncommitter A <a@example.com> %s\n", stamp(c.when), stamp(c.when))
		data(fmt.Sprintf("commit %d", i))
		if i > 0 {
			fmt.Fprintf(&b, "from :%d\n", i)
		}
		name := fmt.Sprintf("f%04d.txt", i)
		b.WriteString("M 100644 inline " + name + "\n")
		data(strings.TrimSuffix(lines(c.add, name), "\n"))
		sizes[name] = c.add
		d := want[day(c.when)]
		d.Commits++
		d.Plus += c.add
		if c.delPrev && i > 0 {
			prev := fmt.Sprintf("f%04d.txt", i-1)
			b.WriteString("D " + prev + "\n")
			d.Minus += sizes[prev]
		}
		if c.binary {
			b.WriteString("M 100644 inline " + fmt.Sprintf("b%04d.bin", i) + "\n")
			data("\x00\x01\x02binary\x00")
		}
		want[day(c.when)] = d
	}
	// A side branch from the first commit, then a merge into main.
	last := list[len(list)-1].when
	side := last.Add(time.Minute)
	merge := last.Add(2 * time.Minute)
	fmt.Fprintf(&b, "commit refs/heads/side\nmark :%d\n", len(list)+1)
	fmt.Fprintf(&b, "author A <a@example.com> %s\ncommitter A <a@example.com> %s\n", stamp(side), stamp(side))
	data("side")
	b.WriteString("from :1\nM 100644 inline side.txt\n")
	data("s1\ns2\ns3")
	fmt.Fprintf(&b, "commit refs/heads/main\nmark :%d\n", len(list)+2)
	fmt.Fprintf(&b, "author A <a@example.com> %s\ncommitter A <a@example.com> %s\n", stamp(merge), stamp(merge))
	data("merge")
	fmt.Fprintf(&b, "from :%d\nmerge :%d\n", len(list), len(list)+1)
	ds := want[day(side)]
	ds.Commits++
	ds.Plus += 3
	want[day(side)] = ds
	dm := want[day(merge)]
	dm.Commits++
	want[day(merge)] = dm

	cmd := exec.Command("git", "-C", dir, "fast-import", "--quiet")
	cmd.Stdin = &b
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("fast-import: %v\n%s", err, errb.String())
	}
	gitDo(t, dir, "2026-09-30T10:00:00+05:30", "checkout", "-q", "-f", "main")
	return want
}

// fixture300 is 300 commits over the 10 local (IST) days 2026-09-20..29, some
// written in UTC right around IST midnight, plus 5 old commits outside the window.
func fixture300() []fixtureCommit {
	var list []fixtureCommit
	utc := time.UTC
	for i := 0; i < 5; i++ { // old: 2026-08-01, outside any window
		list = append(list, fixtureCommit{when: time.Date(2026, 8, 1, 10, i, 0, 0, istLoc), add: 2})
	}
	n := 0
	for d := 20; d <= 29; d++ {
		for k := 0; k < 30; k++ {
			var when time.Time
			switch {
			case k == 0: // 23:50 IST of the day before, written in UTC (18:20Z)
				when = time.Date(2026, 9, d-1, 18, 20, 0, 0, utc)
			case k == 1: // 00:10 IST of this day, written in UTC (18:40Z the day before)
				when = time.Date(2026, 9, d-1, 18, 40, 0, 0, utc)
			case k == 2: // 23:59 IST of this day, written in a west zone (-07:00)
				when = time.Date(2026, 9, d, 23, 59, 0, 0, istLoc).In(time.FixedZone("PDT", -7*3600))
			default:
				when = time.Date(2026, 9, d, 9, k, 0, 0, istLoc)
			}
			c := fixtureCommit{when: when, add: 1 + n%7}
			c.delPrev = n%10 == 9
			if n%25 == 0 {
				c.binary = true
			}
			list = append(list, c)
			n++
		}
	}
	// git log walks by commit order; keep the list sorted by time so parents are older.
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].when.Before(list[j-1].when); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	return list
}

func TestDayStatsExactPastTheCap(t *testing.T) {
	isolate(t)
	dir := newRepo(t)
	list := fixture300()
	all := buildHistory(t, dir, list, istLoc)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, istLoc)

	got, err := DayStatsAt(context.Background(), proj(dir), now, 12, istLoc)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]probe.DayCount{}
	total := 0
	for day, c := range all {
		if day >= "2026-09-19" && day <= "2026-09-30" {
			want[day] = c
			total += c.Commits
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DayStats:\n got %v\nwant %v", got, want)
	}
	if total < 300 {
		t.Fatalf("fixture has only %d commits in the window", total)
	}
	// The midnight cases: 2026-09-19 has only the 23:50 IST commit of the 20th's batch.
	if got["2026-09-19"].Commits != 1 {
		t.Fatalf("2026-09-19 = %+v, want 1 commit", got["2026-09-19"])
	}
	if _, ok := got["2026-08-01"]; ok {
		t.Fatal("old commits leaked into the window")
	}

	// Scan stops at 200, so its day counts are smaller: that was the bug.
	res, err := Scan(context.Background(), proj(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != MaxCommits {
		t.Fatalf("Scan gave %d commits", len(res.Commits))
	}
	capped := 0
	for _, c := range probe.DayCounts(res.Commits, istLoc) {
		capped += c.Commits
	}
	if capped >= total {
		t.Fatalf("capped %d >= exact %d", capped, total)
	}

	// A short window only returns its own days.
	short, err := DayStatsAt(context.Background(), proj(dir), now, 3, istLoc)
	if err != nil {
		t.Fatal(err)
	}
	for day := range short {
		if day < "2026-09-28" {
			t.Fatalf("day %s outside a 3-day window", day)
		}
	}
	if short["2026-09-29"] != want["2026-09-29"] {
		t.Fatalf("short window 29th = %+v want %+v", short["2026-09-29"], want["2026-09-29"])
	}
}

func TestDayStatsNonRepoAndEmptyRepo(t *testing.T) {
	isolate(t)
	got, err := DayStats(context.Background(), proj(t.TempDir()), 0, istLoc)
	if err != nil || len(got) != 0 {
		t.Fatalf("non repo: %v %v", got, err)
	}
	dir := newRepo(t)
	got, err = DayStats(context.Background(), proj(dir), 0, istLoc)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty repo: %v %v", got, err)
	}
}

func TestParseDayStatsTruncated(t *testing.T) {
	out := []byte("\x1e2026-09-29T10:00:00+05:30\n\n3\t1\ta.txt\n-\t-\tb.bin\n" +
		"\x1e2026-09-28T10:00:00+05:30\n\n" + // a merge: no numstat
		"\x1e2026-09-27T10:00:00+05:30\n\n2\t0\tc.txt\n" +
		"\x1e2026-09-26T10:0") // cut
	recs := parseDayStats(out, true)
	if len(recs) != 3 {
		t.Fatalf("got %d records", len(recs))
	}
	if recs[0].plus != 3 || recs[0].minus != 1 || recs[1].plus != 0 || recs[2].plus != 2 {
		t.Fatalf("records %+v", recs)
	}
	// Not truncated: the last record is complete and kept.
	recs = parseDayStats([]byte("\x1e2026-09-29T10:00:00Z\n\n1\t2\tx\n"), false)
	if len(recs) != 1 || recs[0].plus != 1 || recs[0].minus != 2 {
		t.Fatalf("records %+v", recs)
	}
}

func BenchmarkParseDayStats(b *testing.B) {
	var buf bytes.Buffer
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&buf, "\x1e2026-09-%02dT10:00:00+05:30\n\n", 1+i%28)
		for j := 0; j < 10; j++ {
			fmt.Fprintf(&buf, "%d\t%d\tsrc/file%d.go\n", i%50, j, j)
		}
	}
	data := buf.Bytes()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseDayStats(data, false)
	}
}
