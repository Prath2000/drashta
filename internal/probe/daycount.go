package probe

import "time"

// DayCount is the commit activity of one local day. The indexer turns it
// into the commit bars of the seismograph (03-architecture.md section 4).
type DayCount struct {
	Commits int `json:"commits"`
	Plus    int `json:"plus"`
	Minus   int `json:"minus"`
}

// DayCounts groups commits by calendar day in the zone loc. The key is
// "YYYY-MM-DD". Pass the system local zone (time.Local) in the app. A nil loc
// means time.Local. A commit with a zero time is skipped, because it has no day.
//
// A commit stores the zone of its author or committer, but the day on the
// seismograph is always the day on the user's clock. So each time is first
// moved into loc, then cut to a date.
func DayCounts(commits []Commit, loc *time.Location) map[string]DayCount {
	if loc == nil {
		loc = time.Local
	}
	out := make(map[string]DayCount)
	for _, c := range commits {
		if c.Time.IsZero() {
			continue
		}
		key := c.Time.In(loc).Format("2006-01-02")
		d := out[key]
		d.Commits++
		d.Plus += c.Plus
		d.Minus += c.Minus
		out[key] = d
	}
	return out
}
