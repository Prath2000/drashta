package fossil

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/runx"
)

// DefaultDayWindow is the number of local days DayStats covers when days < 1.
const DefaultDayWindow = 35

// DayStats returns the number of check-ins per local day for the last `days`
// days (today included), from ONE `fossil timeline after <date> -n 0` call.
// There is no 200 limit here: every check-in of the window is counted.
//
// Plus and Minus are always 0: fossil line counts cost one process per
// check-in, so the caller takes the lines from the per-commit cache (Scan).
// The day is the check-in time (UTC from fossil) moved into loc (nil means
// time.Local). Only days inside the window with check-ins are returned.
// A folder that is not a checkout gives an empty map. The per-project lock
// is held, like every fossil call.
func DayStats(ctx context.Context, p probe.Project, days int, loc *time.Location) (map[string]probe.DayCount, error) {
	return DayStatsAt(ctx, p, time.Now(), days, loc)
}

// DayStatsAt is DayStats with "now" given by the caller (the indexer passes its
// own clock, so the window matches the days it writes).
func DayStatsAt(ctx context.Context, p probe.Project, now time.Time, days int, loc *time.Location) (map[string]probe.DayCount, error) {
	if loc == nil {
		loc = time.Local
	}
	if days < 1 {
		days = DefaultDayWindow
	}
	out := map[string]probe.DayCount{}
	if !filepath.IsAbs(p.Path) {
		return nil, fmt.Errorf("fossil: project path is not absolute: %q", p.Path)
	}
	if !Detect(p.Path) {
		return out, nil
	}
	lt := now.In(loc)
	today := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	first, end := today.AddDate(0, 0, -(days-1)), today.AddDate(0, 0, 1)
	// Two extra days of margin; the window check below cuts them off again.
	after := first.AddDate(0, 0, -2).UTC().Format("2006-01-02 15:04:05")

	res, err := call(ctx, p, runx.MaxStdout,
		"timeline", "after", after, "--utc", "-q", "-t", "ci", "-n", "0", "-F", timelineFormat)
	if err != nil {
		return nil, err
	}
	if err := failed(res, "timeline"); err != nil {
		return nil, err
	}
	for _, c := range parseTimeline(res.Stdout) {
		if c.Time.IsZero() {
			continue
		}
		t := c.Time.In(loc)
		if t.Before(first) || !t.Before(end) {
			continue
		}
		key := t.Format("2006-01-02")
		d := out[key]
		d.Commits++
		out[key] = d
	}
	return out, nil
}
