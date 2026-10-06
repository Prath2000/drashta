package fossil

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

var istLoc = time.FixedZone("IST", 5*3600+1800)

// TestDayStatsCountsPastTheCap makes more than MaxCommits check-ins over a few
// IST days (some right around IST midnight, which is 18:30 UTC) and checks that
// DayStats counts every one of them per local day, with no line counts.
func TestDayStatsCountsPastTheCap(t *testing.T) {
	if testing.Short() {
		t.Skip("makes 200+ fossil check-ins")
	}
	isolate(t)
	dir, _ := newCheckout(t) // the initial check-in is dated 2026-09-01 (outside the window)
	write(t, dir, "a.txt", "start\n")
	fossilDo(t, dir, "add", "a.txt")

	want := map[string]probe.DayCount{}
	base := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC) // 2026-09-26 23:30 IST
	n := MaxCommits + 10
	for i := 0; i < n; i++ {
		when := base.Add(time.Duration(i) * 20 * time.Minute) // about 70 hours: 4 IST days
		write(t, dir, "a.txt", lines(i+2, "a"))
		fossilDo(t, dir, "commit", "-m", fmt.Sprintf("c%d", i), "--date-override",
			when.Format("2006-01-02T15:04:05"), "--no-warnings", "--no-prompt", "--allow-older")
		key := when.In(istLoc).Format("2006-01-02")
		d := want[key]
		d.Commits++
		want[key] = d
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, istLoc)

	got, err := DayStatsAt(context.Background(), proj(dir), now, 10, istLoc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DayStats:\n got %v\nwant %v", got, want)
	}
	total := 0
	for _, d := range got {
		total += d.Commits
		if d.Plus != 0 || d.Minus != 0 {
			t.Fatalf("fossil day stats must not carry lines: %+v", d)
		}
	}
	if total != n {
		t.Fatalf("total %d, want %d", total, n)
	}
	if got["2026-09-26"].Commits != 2 { // 23:30 and 23:50 IST
		t.Fatalf("2026-09-26 = %+v", got["2026-09-26"])
	}
}

func TestDayStatsNotACheckout(t *testing.T) {
	isolate(t)
	got, err := DayStats(context.Background(), proj(t.TempDir()), 0, istLoc)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := DayStats(context.Background(), probe.Project{Path: "rel"}, 0, istLoc); err == nil {
		t.Fatal("want an error for a relative path")
	}
}
