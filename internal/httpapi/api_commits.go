package httpapi

// Commit routes of the project pages. git and fossil history are merged into one list.
//
//	GET /api/projects/{p}/commits?cursor=&limit=50&path=   {commits:[{id,vcs,author,ts,msg,plus,minus,nfiles,tasks}], next}
//	GET /api/projects/{p}/commits/{id}                     {commit, body, files:[{path,plus,minus}], patch, patchTruncated}
//	GET /api/projects/{p}/dirty                            {files:[{path,state}], diff, diffTruncated}
//
// How paging works (03-architecture.md section 10: the index keeps only the newest commits):
//   - Phase 1 reads the cached commits (SQLite), newest first, with a keyset cursor "c.<unix>.<id>".
//   - When the cache is used up and a vcs holds at least pjCacheCap cached commits, phase 2 asks
//     the probes live for older ones. Its cursor is "l.<git skip>.<fossil skip>": how many commits
//     of each vcs were already given out.
//   - With path=<file or folder> every page is live (the cache has no full per-path history), so
//     the cursor is always the "l." form.
//   - next is null when there are no more commits.
//
// tasks:[id] come from task_links: a link "commit:<hash or prefix>" (6+ characters) marks a commit.
// The detail route validates the id (hex, 6 to 64 characters), asks the probe live, and cuts the
// patch at 400 KB (patchTruncated).

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Prath2000/drashta/internal/probe"
	"github.com/Prath2000/drashta/internal/probe/fossil"
	"github.com/Prath2000/drashta/internal/probe/git"
	"github.com/Prath2000/drashta/internal/store"
)

const (
	pjCommitsDefault = 50
	pjCommitsMax     = 200
	pjCacheCap       = 200       // the index holds the newest 200 commits per vcs; more may exist
	pjPatchCap       = 400 << 10 // largest patch or diff text in a reply
)

var pjCommitIDRe = regexp.MustCompile(`^[0-9a-f]{6,64}$`)

func (a *ProjectAPI) registerCommits(s *Server) {
	s.Handle(http.MethodGet, "/api/projects/{p}/commits", a.handleCommits, false)
	s.Handle(http.MethodGet, "/api/projects/{p}/commits/{id}", a.handleCommit, false)
	s.Handle(http.MethodGet, "/api/projects/{p}/dirty", a.handleDirty, false)
}

type pjCommit struct {
	ID     string   `json:"id"`
	VCS    string   `json:"vcs"`
	Author string   `json:"author"`
	TS     string   `json:"ts"`
	Msg    string   `json:"msg"`
	Plus   int      `json:"plus"`
	Minus  int      `json:"minus"`
	NFiles int      `json:"nfiles"`
	Tasks  []string `json:"tasks"`
}

// pjCommitCursor is a parsed commits cursor.
type pjCommitCursor struct {
	live  bool
	store store.ProjCursor
	skip  map[string]int // vcs -> commits already given (live phase)
}

func pjParseCommitCursor(s string) (pjCommitCursor, bool) {
	var c pjCommitCursor
	if s == "" {
		return c, true
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return c, false
	}
	switch parts[0] {
	case "c":
		unix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || unix < 0 || !pjCommitIDRe.MatchString(parts[2]) {
			return c, false
		}
		c.store = store.ProjCursor{Set: true, Unix: unix, ID: parts[2]}
	case "l":
		g, err1 := strconv.Atoi(parts[1])
		f, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || g < 0 || f < 0 || g > 1<<24 || f > 1<<24 {
			return c, false
		}
		c.live = true
		c.skip = map[string]int{"git": g, "fossil": f}
	default:
		return c, false
	}
	return c, true
}

func pjStoreCursor(c store.ProjCommit) string {
	return "c." + strconv.FormatInt(c.Time.Unix(), 10) + "." + c.ID
}

func pjLiveCursor(skip map[string]int) string {
	return "l." + strconv.Itoa(skip["git"]) + "." + strconv.Itoa(skip["fossil"])
}

// pjTasksFor returns the task ids whose links point at fullID. A link ref has the form
// "<type>:<id>[#frag]" or "<project>:<type>:<id>[#frag]"; the link id may be a prefix of fullID
// when it has at least minLen characters.
func pjTasksFor(refs []store.ProjTaskRef, project, typ, fullID string, minLen int) []string {
	fullID = strings.ToLower(fullID)
	seen := map[string]bool{}
	out := []string{}
	for _, r := range refs {
		ref := strings.TrimPrefix(r.Ref, project+":")
		rest, ok := strings.CutPrefix(ref, typ+":")
		if !ok {
			continue
		}
		if i := strings.IndexByte(rest, '#'); i >= 0 {
			rest = rest[:i]
		}
		rest = strings.ToLower(rest)
		if len(rest) < minLen || !strings.HasPrefix(fullID, rest) || seen[r.TaskID] {
			continue
		}
		seen[r.TaskID] = true
		out = append(out, r.TaskID)
	}
	sort.SliceStable(out, func(i, j int) bool { return pjNatLess(out[i], out[j]) })
	return out
}

func (a *ProjectAPI) commitView(c probe.Commit, refs []store.ProjTaskRef, project string) pjCommit {
	return pjCommit{
		ID: c.ID, VCS: c.VCS, Author: c.Author, TS: a.iso(c.Time), Msg: c.Msg,
		Plus: c.Plus, Minus: c.Minus, NFiles: c.NFiles,
		Tasks: pjTasksFor(refs, project, "commit", c.ID, 6),
	}
}

func (a *ProjectAPI) handleCommits(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.lookup(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, bad := pjIntParam(q, "limit", pjCommitsDefault, 1, pjCommitsMax)
	if bad != "" {
		BadRequest(w, bad)
		return
	}
	cur, okc := pjParseCommitCursor(q.Get("cursor"))
	if !okc {
		BadRequest(w, "bad cursor")
		return
	}
	rel := ""
	if p := q.Get("path"); p != "" {
		var err error
		if rel, err = pjCleanRel(p, false); err != nil {
			BadRequest(w, err.Error())
			return
		}
	}
	ctx := r.Context()
	refs, err := a.d.Store.ProjTaskRefs(ctx, pr.Row.ID, "commit")
	if err != nil {
		a.fail(w, r, "task links", err)
		return
	}

	out := []probe.Commit{}
	var next *string
	vcsList := pjVCSList(pr.Row.VCS)

	switch {
	case rel != "" || cur.live:
		// Live only. A path filter is always live; a live cursor continues a live listing.
		if !a.diskGuard(w, pr) {
			return
		}
		skip := cur.skip
		if skip == nil {
			skip = map[string]int{}
		}
		items, nextSkip, more, err := a.liveCommits(ctx, pr, vcsList, skip, limit, rel)
		if err != nil {
			a.commitError(w, r, err)
			return
		}
		out = items
		if more {
			s := pjLiveCursor(nextSkip)
			next = &s
		}
	default:
		rows, err := a.d.Store.ProjCommitsBefore(ctx, pr.Row.ID, cur.store, limit+1)
		if err != nil {
			a.fail(w, r, "commits", err)
			return
		}
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		for _, c := range rows {
			out = append(out, probe.Commit{ID: c.ID, VCS: c.VCS, Author: c.Author, Time: c.Time, Msg: c.Msg,
				Plus: c.Plus, Minus: c.Minus, NFiles: c.NFiles})
		}
		if more {
			s := pjStoreCursor(rows[len(rows)-1])
			next = &s
			break
		}
		// The cache is used up. Older history may exist for a vcs whose cache is full.
		if a.d.VCS != nil && a.rootCheck(pr) == nil {
			counts, err := a.d.Store.ProjCommitCounts(ctx, pr.Row.ID)
			if err != nil {
				a.fail(w, r, "commit counts", err)
				return
			}
			full := false
			for _, v := range vcsList {
				if counts[v] >= pjCacheCap {
					full = true
				}
			}
			if !full {
				break
			}
			skip := map[string]int{"git": counts["git"], "fossil": counts["fossil"]}
			if len(out) >= limit {
				// The page is full and ends exactly where the cache ends. The next page goes live.
				s := pjLiveCursor(skip)
				next = &s
				break
			}
			items, nextSkip, more, err := a.liveCommits(ctx, pr, vcsList, skip, limit-len(out), "")
			if err != nil {
				// The cached part is still good. Log and end the list here.
				a.log.Warn("older commits failed", "project", pr.Row.ID, "err", err)
				break
			}
			out = append(out, items...)
			if more {
				s := pjLiveCursor(nextSkip)
				next = &s
			}
		}
	}

	views := make([]pjCommit, 0, len(out))
	for _, c := range out {
		views = append(views, a.commitView(c, refs, pr.Row.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"commits": views, "next": next})
}

// commitError maps a probe error from a list call.
func (a *ProjectAPI) commitError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, git.ErrBadPath), errors.Is(err, fossil.ErrBadPath):
		BadRequest(w, "bad path")
	default:
		a.fail(w, r, "commit history", err)
	}
}

// liveCommits asks the probes for commits, skipping skip[vcs] of each, and merges them newest
// first. It returns at most limit commits, the new skip counts, and whether more may follow.
func (a *ProjectAPI) liveCommits(ctx context.Context, pr pjProject, vcsList []string, skip map[string]int,
	limit int, path string) (items []probe.Commit, nextSkip map[string]int, more bool, err error) {
	nextSkip = map[string]int{"git": skip["git"], "fossil": skip["fossil"]}
	if a.d.VCS == nil || len(vcsList) == 0 || limit <= 0 {
		return []probe.Commit{}, nextSkip, false, nil
	}
	var all []probe.Commit
	var firstErr error
	failed := 0
	fullPage := false
	for _, v := range vcsList {
		list, e := a.d.VCS.Commits(ctx, v, pr.Probe, skip[v], limit, path)
		switch {
		case e == nil:
		case errors.Is(e, git.ErrBadPath), errors.Is(e, fossil.ErrBadPath):
			return nil, nextSkip, false, e
		case errors.Is(e, git.ErrNotRepo), errors.Is(e, fossil.ErrNotCheckout):
			continue
		default:
			a.log.Warn("commit history", "project", pr.Row.ID, "vcs", v, "err", e)
			failed++
			if firstErr == nil {
				firstErr = e
			}
			continue
		}
		if len(list) >= limit {
			fullPage = true
		}
		for i := range list {
			if list[i].VCS == "" {
				list[i].VCS = v
			}
		}
		all = append(all, list...)
	}
	if failed > 0 && failed == len(vcsList) {
		return nil, nextSkip, false, firstErr
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].Time.Equal(all[j].Time) {
			return all[i].Time.After(all[j].Time)
		}
		return all[i].ID > all[j].ID
	})
	if len(all) > limit {
		all = all[:limit]
		more = true
	} else if fullPage {
		more = true
	}
	for _, c := range all {
		nextSkip[c.VCS]++
	}
	if len(all) == 0 {
		return []probe.Commit{}, nextSkip, false, nil
	}
	return all, nextSkip, more, nil
}

type pjCommitFile struct {
	Path  string `json:"path"`
	Plus  int    `json:"plus"`
	Minus int    `json:"minus"`
}

type pjCommitDetail struct {
	Commit         pjCommit       `json:"commit"`
	Body           string         `json:"body"`
	Files          []pjCommitFile `json:"files"`
	Patch          string         `json:"patch"`
	PatchTruncated bool           `json:"patchTruncated"`
}

func (a *ProjectAPI) handleCommit(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.onDisk(w, r)
	if !ok {
		return
	}
	id := strings.ToLower(r.PathValue("id"))
	if !pjCommitIDRe.MatchString(id) {
		BadRequest(w, "commit id must be 6 to 64 hex characters")
		return
	}
	if a.d.VCS == nil {
		NotFound(w, "no history available for this project")
		return
	}
	ctx := r.Context()

	// The cache knows which vcs a commit belongs to. Without it, try each vcs of the project.
	order := pjVCSList(pr.Row.VCS)
	lookupID := id
	if c, found, err := a.d.Store.ProjCommitByPrefix(ctx, pr.Row.ID, id); err != nil {
		a.fail(w, r, "commit lookup", err)
		return
	} else if found {
		order = []string{c.VCS}
		lookupID = c.ID
	}

	var det probe.CommitDetail
	got := false
	for _, v := range order {
		d, err := a.d.VCS.Detail(ctx, v, pr.Probe, lookupID)
		if err == nil {
			det, got = d, true
			if det.VCS == "" {
				det.VCS = v
			}
			break
		}
		switch {
		case errors.Is(err, git.ErrBadRef), errors.Is(err, git.ErrNotFound), errors.Is(err, git.ErrNotRepo),
			errors.Is(err, fossil.ErrBadRef), errors.Is(err, fossil.ErrNotFound), errors.Is(err, fossil.ErrNotCheckout):
			// This vcs does not know the commit. Try the next one.
		default:
			a.fail(w, r, "commit detail", err)
			return
		}
	}
	if !got {
		NotFound(w, "no such commit")
		return
	}

	refs, err := a.d.Store.ProjTaskRefs(ctx, pr.Row.ID, "commit")
	if err != nil {
		a.fail(w, r, "task links", err)
		return
	}
	patch, cut := pjClipText(det.Patch, pjPatchCap)
	files := make([]pjCommitFile, 0, len(det.Files))
	for _, f := range det.Files {
		files = append(files, pjCommitFile{Path: f.Path, Plus: f.Plus, Minus: f.Minus})
	}
	view := a.commitView(det.Commit, refs, pr.Row.ID)
	if view.NFiles == 0 {
		view.NFiles = len(files)
	}
	WriteJSON(w, http.StatusOK, pjCommitDetail{
		Commit: view, Body: det.Body, Files: files, Patch: patch, PatchTruncated: cut || det.PatchTruncated,
	})
}

type pjDirtyReply struct {
	Files         []probe.DirtyFile `json:"files"`
	Diff          string            `json:"diff"`
	DiffTruncated bool              `json:"diffTruncated"`
	Unavailable   *string           `json:"unavailable,omitempty"` // why the diff is missing, when it is
	FetchedAt     string            `json:"fetchedAt"`
}

func (a *ProjectAPI) handleDirty(w http.ResponseWriter, r *http.Request) {
	pr, ok := a.lookup(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	files, err := a.d.Store.ProjDirtyFiles(ctx, pr.Row.ID)
	if err != nil {
		a.fail(w, r, "dirty list", err)
		return
	}
	reply := pjDirtyReply{Files: files, FetchedAt: a.iso(a.now())}
	if a.d.VCS != nil && a.rootCheck(pr) != nil {
		msg := "the project folder is not available"
		reply.Unavailable = &msg
	} else if a.d.VCS != nil {
		var parts []string
		for _, v := range pjVCSList(pr.Row.VCS) {
			diff, cut, err := a.d.VCS.DirtyDiff(ctx, v, pr.Probe)
			switch {
			case err == nil:
			case errors.Is(err, git.ErrNotRepo), errors.Is(err, fossil.ErrNotCheckout):
				continue
			default:
				// Keep the file list. Say why the diff is missing.
				a.log.Warn("dirty diff", "project", pr.Row.ID, "vcs", v, "err", err)
				msg := v + " diff could not be read"
				reply.Unavailable = &msg
				continue
			}
			if diff != "" {
				parts = append(parts, diff)
			}
			reply.DiffTruncated = reply.DiffTruncated || cut
		}
		diff := strings.Join(parts, "\n")
		diff, cut := pjClipText(diff, pjPatchCap)
		reply.Diff = diff
		reply.DiffTruncated = reply.DiffTruncated || cut
	}
	WriteJSON(w, http.StatusOK, reply)
}
