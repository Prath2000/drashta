package git

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/runx"
)

// DefaultDayWindow is the number of local days DayStats covers when days < 1.
// Home shows at most 30 days, so 35 leaves a small margin.
const DefaultDayWindow = 35

// dayStatsFormat is a small fixed header per commit: 0x1e, then the committer
// date. The numstat rows follow it.
const dayStatsFormat = "--format=%x1e%cI"

// DayStats returns the exact commit count and line counts per local day for
// the last `days` days (today included), read from ONE `git log --numstat`
// pass over the window. It does not depend on the MaxCommits cap of Scan.
//
// The rules are the same as Scan, so the numbers match the commit list:
//   - The day is the committer date (%cI) moved into loc (nil means time.Local).
//   - Merge commits count as commits. git shows no diff for them, so they add 0 lines.
//   - A binary file adds 0 lines. Rename detection is git's default, as in Scan.
//
// Only days inside the window are returned, and only days with commits.
// A folder without .git, or a repo with no commit, gives an empty map.
//
// If the output hits runx's 8 MB cap, the complete records still give their
// lines, and a second counts-only pass (`--format=%cI`) gives the commit
// counts. Lines of the commits past the cap are then 0.
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
	ok, err := hasDotGit(p.Path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return out, nil
	}
	first, last := dayWindow(now, days, loc)
	// Ask git for two extra days: commit times in other zones can still land in the window.
	since := "--since=" + first.AddDate(0, 0, -2).Format(time.RFC3339)
	inWindow := func(t time.Time) (string, bool) {
		if t.IsZero() {
			return "", false
		}
		lt := t.In(loc)
		if lt.Before(first) || !lt.Before(last) {
			return "", false
		}
		return lt.Format("2006-01-02"), true
	}

	res, err := run(ctx, p.Path, runx.MaxStdout,
		"log", "--numstat", "--no-ext-diff", "--no-textconv", since, dayStatsFormat)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(string(res.Stderr), "does not have any commits yet") {
			return out, nil
		}
		return nil, failed(res, "log")
	}

	recs := parseDayStats(res.Stdout, res.Truncated)
	for _, r := range recs {
		key, ok := inWindow(r.t)
		if !ok {
			continue
		}
		d := out[key]
		d.Commits++
		d.Plus += r.plus
		d.Minus += r.minus
		out[key] = d
	}
	if !res.Truncated {
		return out, nil
	}

	// The cap was hit: count every commit again with a tiny format.
	cnt, err := run(ctx, p.Path, runx.MaxStdout, "log", since, "--format=%cI")
	if err != nil {
		return nil, err
	}
	if err := failed(cnt, "log"); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, line := range strings.Split(string(cnt.Stdout), "\n") {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(line))
		if err != nil {
			continue
		}
		if key, ok := inWindow(t); ok {
			counts[key]++
		}
	}
	for key, n := range counts {
		d := out[key]
		d.Commits = n
		out[key] = d
	}
	return out, nil
}

// dayWindow returns the start of the first day and the start of the day after
// the last day (today) of a window of `days` local days.
func dayWindow(t time.Time, days int, loc *time.Location) (first, end time.Time) {
	lt := t.In(loc)
	today := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	return today.AddDate(0, 0, -(days - 1)), today.AddDate(0, 0, 1)
}

type dayRec struct {
	t           time.Time
	plus, minus int
}

// parseDayStats reads `git log --numstat --format=%x1e%cI` output without
// building one big string: each record is "\x1e<date>\n\n<plus>\t<minus>\t<path>\n...".
// When truncated is true the last record may be cut, so it is dropped.
func parseDayStats(out []byte, truncated bool) []dayRec {
	var recs []dayRec
	for len(out) > 0 {
		if out[0] != 0x1e {
			i := bytes.IndexByte(out, 0x1e)
			if i < 0 {
				break
			}
			out = out[i:]
		}
		out = out[1:]
		end := bytes.IndexByte(out, 0x1e)
		rec := out
		if end >= 0 {
			rec, out = out[:end], out[end:]
		} else {
			if truncated {
				break // the last record may be cut
			}
			out = nil
		}
		nl := bytes.IndexByte(rec, '\n')
		head := rec
		if nl >= 0 {
			head, rec = rec[:nl], rec[nl+1:]
		} else {
			rec = nil
		}
		t, err := time.Parse(time.RFC3339, string(bytes.TrimSpace(head)))
		if err != nil {
			continue
		}
		r := dayRec{t: t}
		for len(rec) > 0 {
			line := rec
			if j := bytes.IndexByte(rec, '\n'); j >= 0 {
				line, rec = rec[:j], rec[j+1:]
			} else {
				rec = nil
			}
			a := bytes.IndexByte(line, '\t')
			if a < 0 {
				continue
			}
			b := bytes.IndexByte(line[a+1:], '\t')
			if b < 0 {
				continue
			}
			plus, _ := strconv.Atoi(string(line[:a])) // "-" (binary) gives 0
			minus, _ := strconv.Atoi(string(line[a+1 : a+1+b]))
			r.plus += plus
			r.minus += minus
		}
		recs = append(recs, r)
	}
	return recs
}
