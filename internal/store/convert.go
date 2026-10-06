package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

// DayLayout is the format of a day key: YYYY-MM-DD in the local time zone.
const DayLayout = "2006-01-02"

// MaxBody is the longest text the store keeps for one search row or one commit body: 64 KB.
// Longer text is cut (at a character boundary).
const MaxBody = 64 << 10

// unixOrNil returns the unix seconds of t, or nil (SQL NULL) for the zero time.
func unixOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

// unixOrZero returns the unix seconds of t, or 0 for the zero time.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// fromUnix turns a nullable unix-seconds column into a time. NULL and 0 become the zero time.
func fromUnix(n sql.NullInt64) time.Time {
	if !n.Valid || n.Int64 == 0 {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// jsonText marshals v to JSON text. A nil value (or a marshal failure) gives def.
func jsonText(v any, def string) string {
	if v == nil {
		return def
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return def
	}
	return string(b)
}

// clip cuts s to at most max bytes without splitting a UTF-8 character.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// parseTime reads the time strings used in .drashta front matter.
// It accepts RFC 3339 (with or without fractions) and a plain date. It returns the zero time otherwise.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", DayLayout} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// dayBounds returns the unix-second range [start, end) of a local day.
func dayBounds(day string, loc *time.Location) (start, end int64, err error) {
	s, err := DayStart(day, loc)
	if err != nil {
		return 0, 0, err
	}
	next, err := AddDays(day, 1)
	if err != nil {
		return 0, 0, err
	}
	e, err := DayStart(next, loc)
	if err != nil {
		return 0, 0, err
	}
	return s.Unix(), e.Unix(), nil
}

// AddDays returns the day key n calendar days after day (n < 0 goes back).
// It is pure calendar math in UTC, so no time zone and no DST change can skip
// or repeat a day. Use it to step through days; never step a local midnight
// instant (some zones, for example America/Santiago, have no 00:00 on the day
// the clocks go forward).
func AddDays(day string, n int) (string, error) {
	t, err := time.Parse(DayLayout, day)
	if err != nil {
		return "", fmt.Errorf("store: bad day %q (want YYYY-MM-DD): %w", day, err)
	}
	return t.AddDate(0, 0, n).Format(DayLayout), nil
}

// mustAddDays is AddDays for a day key the caller made itself (always valid).
func mustAddDays(day string, n int) string {
	out, err := AddDays(day, n)
	if err != nil {
		panic(err)
	}
	return out
}

// DayStart returns the first instant of a local day (day is YYYY-MM-DD, nil loc
// means time.Local). It is right also where the clocks jump at midnight: then
// the day starts at 01:00, and time.Date(.., 0, 0, 0, 0, loc) would give 23:00
// of the day before.
func DayStart(day string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	d, err := time.Parse(DayLayout, day)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: bad day %q (want YYYY-MM-DD): %w", day, err)
	}
	t := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	if t.Format(DayLayout) == day {
		// Midnight exists. If the clocks went back over midnight, an earlier
		// instant may also carry this day: look back at most 3 hours.
		lo := t.Add(-3 * time.Hour)
		if lo.In(loc).Format(DayLayout) != day {
			return firstOfDay(lo, t, day, loc), nil
		}
		return t, nil
	}
	// Midnight does not exist (or fell on the day before): the day starts later.
	hi := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc)
	return firstOfDay(t, hi, day, loc), nil
}

// firstOfDay finds, by binary search on whole seconds, the first instant in (lo, hi]
// whose local day is day. lo must be before the day and hi inside it.
func firstOfDay(lo, hi time.Time, day string, loc *time.Location) time.Time {
	a, b := lo.Unix(), hi.Unix() // day(a) < day, day(b) == day
	for b-a > 1 {
		m := a + (b-a)/2
		if time.Unix(m, 0).In(loc).Format(DayLayout) >= day {
			b = m
		} else {
			a = m
		}
	}
	return time.Unix(b, 0).In(loc)
}

// DayOf returns the local day key (YYYY-MM-DD) of t in loc. A nil loc means time.Local.
func DayOf(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format(DayLayout)
}

// now is the clock. Tests replace it.
var now = time.Now
