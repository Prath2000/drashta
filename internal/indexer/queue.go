package indexer

import (
	"sort"
	"time"
)

// jobKind names the work a job does.
type jobKind uint8

const (
	kindProjects  jobKind = iota + 1 // discover projects (bucket bucketProjects)
	kindTree                         // walk one project's files (+ docs)
	kindVCS                          // git and fossil of one project, always together (serialised)
	kindAuthored                     // .drashta/** files of one project
	kindFull                         // all of the above for one project
	kindClaude                       // one Claude session family (bucket bucketClaude, sub = main .jsonl path)
	kindClaudeAll                    // every Claude transcript (bucket bucketClaude)
)

func (k jobKind) String() string {
	switch k {
	case kindProjects:
		return "projects"
	case kindTree:
		return "tree"
	case kindVCS:
		return "vcs"
	case kindAuthored:
		return "authored"
	case kindFull:
		return "full"
	case kindClaude:
		return "claude"
	case kindClaudeAll:
		return "claude-all"
	}
	return "unknown"
}

// Buckets that are not a project. A bucket runs at most one job at a time.
const (
	bucketProjects = "~projects"
	bucketClaude   = "~claude"
)

// jobKey identifies one pending job. Events with the same key are merged.
type jobKey struct {
	bucket string // project id, or bucketProjects / bucketClaude
	kind   jobKind
	sub    string // kindClaude: the session's main .jsonl path
}

// job is one unit of work for a worker.
type job struct {
	key       jobKey
	first     time.Time // first event since the job was queued
	last      time.Time // newest event
	immediate bool      // run as soon as a worker and the bucket are free (refresh, sweep)
	paths     map[string]bool
	dirty     bool      // kindVCS: refresh the dirty list even when the cache key did not change
	force     bool      // run the probes even when cache keys did not change
	push      bool      // a push was seen (refs/remotes): ask for a gh refresh
	withGH    bool      // run the gh hook too (per-project refresh)
	backfill  time.Time // kindFull in a sweep: mtime backfill since this time (zero = none)
	scanEvent bool      // publish scan running/done for this project
	waiters   []chan struct{}
}

// request is what is added to the queue.
type request struct {
	key       jobKey
	path      string
	immediate bool
	dirty     bool
	force     bool
	push      bool
	withGH    bool
	backfill  time.Time
	scanEvent bool
	done      chan struct{} // closed when the job that absorbed this request has run (may be nil)
}

// queue holds pending jobs and decides when each one may run.
//
// Debounce: a job runs `debounce` after its newest event, but never later than
// `storm` after its first event. So in a storm (npm install, git checkout) the
// job runs about every `storm`, and after a single change it runs `debounce` later.
// A bucket (one project) runs at most one job at a time.
//
// queue is not safe for concurrent use: only the scheduler goroutine touches it.
type queue struct {
	debounce time.Duration
	storm    time.Duration
	items    map[jobKey]*job
	busy     map[string]bool
}

func newQueue(debounce, storm time.Duration) *queue {
	return &queue{debounce: debounce, storm: storm, items: map[jobKey]*job{}, busy: map[string]bool{}}
}

// project-level kinds that a pending kindFull absorbs.
func absorbedByFull(k jobKind) bool {
	return k == kindTree || k == kindVCS || k == kindAuthored
}

// add queues a request at time now, merging it into a pending job when possible.
func (q *queue) add(r request, now time.Time) {
	key := r.key
	// A pending full job for the project absorbs tree/vcs/authored work.
	if absorbedByFull(key.kind) {
		if fj, ok := q.items[jobKey{bucket: key.bucket, kind: kindFull}]; ok {
			q.merge(fj, r, now)
			return
		}
	}
	// A pending "all transcripts" job absorbs single-session work.
	if key.kind == kindClaude {
		if aj, ok := q.items[jobKey{bucket: key.bucket, kind: kindClaudeAll}]; ok {
			q.merge(aj, r, now)
			return
		}
	}
	j, ok := q.items[key]
	if !ok {
		j = &job{key: key, first: now, last: now, paths: map[string]bool{}}
		q.items[key] = j
	}
	q.merge(j, r, now)
	// A new full / all job takes over the smaller pending jobs of its bucket.
	if key.kind == kindFull || key.kind == kindClaudeAll {
		for k, other := range q.items {
			if k.bucket != key.bucket || k == key {
				continue
			}
			if (key.kind == kindFull && absorbedByFull(k.kind)) || (key.kind == kindClaudeAll && k.kind == kindClaude) {
				j.waiters = append(j.waiters, other.waiters...)
				j.dirty = j.dirty || other.dirty
				j.push = j.push || other.push
				j.withGH = j.withGH || other.withGH
				j.immediate = j.immediate || other.immediate
				if other.first.Before(j.first) {
					j.first = other.first
				}
				delete(q.items, k)
			}
		}
	}
}

func (q *queue) merge(j *job, r request, now time.Time) {
	if now.After(j.last) {
		j.last = now
	}
	if r.path != "" {
		j.paths[r.path] = true
	}
	j.immediate = j.immediate || r.immediate
	j.dirty = j.dirty || r.dirty
	j.force = j.force || r.force
	j.push = j.push || r.push
	j.withGH = j.withGH || r.withGH
	j.scanEvent = j.scanEvent || r.scanEvent
	if !r.backfill.IsZero() && (j.backfill.IsZero() || r.backfill.Before(j.backfill)) {
		j.backfill = r.backfill
	}
	if r.done != nil {
		j.waiters = append(j.waiters, r.done)
	}
}

// deadline is when j may run.
func (q *queue) deadline(j *job) time.Time {
	if j.immediate {
		return j.first
	}
	quiet := j.last.Add(q.debounce)
	capAt := j.first.Add(q.storm)
	if capAt.Before(quiet) {
		return capAt
	}
	return quiet
}

// pop removes and returns up to max jobs that are due at now, earliest first,
// at most one per bucket, never for a busy bucket. The buckets become busy.
func (q *queue) pop(now time.Time, max int) []*job {
	if max <= 0 {
		return nil
	}
	var due []*job
	for _, j := range q.items {
		if q.busy[j.key.bucket] {
			continue
		}
		if !q.deadline(j).After(now) {
			due = append(due, j)
		}
	}
	sort.Slice(due, func(a, b int) bool {
		da, db := q.deadline(due[a]), q.deadline(due[b])
		if !da.Equal(db) {
			return da.Before(db)
		}
		// projects discovery first, then by bucket and kind for a stable order
		if due[a].key.bucket != due[b].key.bucket {
			if due[a].key.bucket == bucketProjects {
				return true
			}
			if due[b].key.bucket == bucketProjects {
				return false
			}
			return due[a].key.bucket < due[b].key.bucket
		}
		if due[a].key.kind != due[b].key.kind {
			return due[a].key.kind < due[b].key.kind
		}
		return due[a].key.sub < due[b].key.sub
	})
	var out []*job
	for _, j := range due {
		if len(out) >= max {
			break
		}
		if q.busy[j.key.bucket] {
			continue
		}
		q.busy[j.key.bucket] = true
		delete(q.items, j.key)
		out = append(out, j)
	}
	return out
}

// next returns the earliest deadline among jobs whose bucket is free.
func (q *queue) next() (time.Time, bool) {
	var best time.Time
	found := false
	for _, j := range q.items {
		if q.busy[j.key.bucket] {
			continue
		}
		d := q.deadline(j)
		if !found || d.Before(best) {
			best, found = d, true
		}
	}
	return best, found
}

// release marks a bucket free again after its job finished.
func (q *queue) release(bucket string) { delete(q.busy, bucket) }

// pending is the number of queued jobs (tests).
func (q *queue) pending() int { return len(q.items) }
