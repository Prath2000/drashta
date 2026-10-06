package autolink

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Prath2000/drashta/internal/authored"
)

// taskState is one task plus its manual and rejected refs, parsed once.
type taskState struct {
	proj    string
	info    TaskInfo
	manual  []rkey
	unlinks []rkey
}

// index holds the lookups Links needs.
type index struct {
	own     string
	tasks   map[string]map[string]*taskState // project -> task id -> state
	commits idSet
}

func newIndex(in Input) *index {
	ix := &index{own: in.ProjectID, tasks: map[string]map[string]*taskState{}}
	add := func(proj string, list []TaskInfo) {
		m := ix.tasks[proj]
		if m == nil {
			m = map[string]*taskState{}
			ix.tasks[proj] = m
		}
		for _, t := range list {
			id, ok := normTaskID(t.ID)
			if !ok {
				continue
			}
			if _, dup := m[id]; dup {
				continue
			}
			st := &taskState{proj: proj, info: t}
			st.info.ID = id
			for _, r := range t.Manual {
				if k, ok := parseKey(proj, r); ok {
					st.manual = append(st.manual, k)
				}
			}
			for _, r := range t.Unlinks {
				if k, ok := parseKey(proj, r); ok {
					st.unlinks = append(st.unlinks, k)
				}
			}
			m[id] = st
		}
	}
	add(in.ProjectID, in.Tasks)
	others := append([]ProjectTasks(nil), in.Others...)
	sort.SliceStable(others, func(i, j int) bool { return others[i].ProjectID < others[j].ProjectID })
	for _, o := range others {
		if o.ProjectID == "" || o.ProjectID == in.ProjectID || ix.tasks[o.ProjectID] != nil {
			continue
		}
		add(o.ProjectID, o.Tasks)
	}

	var ids []string
	for _, c := range in.Commits {
		ids = append(ids, c.ID)
	}
	for _, b := range in.Branches {
		ids = append(ids, b.Commits...)
	}
	ix.commits = newIDSet(ids)
	return ix
}

// target decides which project a mention belongs to: the text's own project when it
// has no qualifier (or only a trailer word), or another known project for `ledger:T-12`.
func (ix *index) target(m mention) (string, bool) {
	q := m.Qual
	switch {
	case q == "", q == ix.own, isTrailerWord(q):
		return ix.own, true
	case ix.tasks[q] != nil:
		return q, true
	}
	return "", false
}

func (ix *index) task(proj, id string) *taskState {
	if m := ix.tasks[proj]; m != nil {
		return m[id]
	}
	return nil
}

// cand is a link on its way to the result.
type cand struct {
	taskProj string
	taskID   string
	key      rkey
	reason   string
	prio     int
}

type candKey struct {
	taskProj, taskID string
	key              rkey
}

// collector keeps the best reason for each (task, ref).
type collector struct{ m map[candKey]cand }

// add keeps the candidate with the lowest prio; a tie goes to the smaller reason text,
// so the result never depends on the order of the input.
func (c *collector) add(x cand) {
	k := candKey{x.taskProj, x.taskID, x.key}
	old, ok := c.m[k]
	if !ok || x.prio < old.prio || (x.prio == old.prio && x.reason < old.reason) {
		c.m[k] = x
	}
}

// Priorities when several signals give the same link (lower wins).
const (
	prioCommitMessage = 1
	prioSessionEdit   = 1
	prioSnapshot      = 2
	prioSessionText   = 2
	prioBranch        = 3
	prioSessionOutput = 4
)

// sourceKey makes the key of a thing in project src for a task in project taskProj.
func sourceKey(taskProj, src, typ, id string) rkey {
	k := rkey{typ: typ, id: id}
	if taskProj != src {
		k.proj = src
	}
	return k
}

// Links derives the auto links of one project (04 section 3). It is a pure function:
// the same input gives the same output, in the same order, whatever the order of the input slices.
//
// Rules:
//   - commit message: `\b[Tt]-\d+\b` in the subject or body, including trailers such as
//     `Task: T-12` and `Refs: T-12, T-14`; `ledger:T-12` links the other project's task.
//   - branch name: every commit of Input.Branches[i].Commits links to the tasks named in the branch name.
//   - session text: a user turn (or the title) says `T-12` or `task:T-12`.
//   - session edit: the session wrote `.drashta/tasks/T-12.md` (or `.drashta/snapshots/T-12/`) or ran
//     the drashta program with T-12 in the command.
//   - snapshot: the snapshot's commit links to its task.
//   - session output: a commit made while a session ran (up to 2 hours after) that contains a file the
//     session edited links to every task the session is linked to (auto or manual).
//
// A link is dropped when the task already has it as a manual link (a short commit id matches the full
// one), or when the ref is in the task's Unlinks. Only tasks that exist in Input (or in Others) get links.
func Links(in Input) []Link {
	ix := newIndex(in)
	col := &collector{m: map[candKey]cand{}}

	// 1. Commit messages.
	for _, c := range in.Commits {
		id := strings.ToLower(c.ID)
		if !validCommitID(id) {
			continue
		}
		text := c.Msg + "\n" + c.Body
		for _, m := range scanMentions(text) {
			proj, ok := ix.target(m)
			if !ok || ix.task(proj, m.ID) == nil {
				continue
			}
			col.add(cand{proj, m.ID, sourceKey(proj, in.ProjectID, authored.RefCommit, id), ReasonCommitMessage, prioCommitMessage})
		}
	}

	// 2. Branch names.
	for _, b := range in.Branches {
		if b.Default {
			continue
		}
		for _, m := range branchMentions(b.Name) {
			if ix.task(ix.own, m.ID) == nil {
				continue
			}
			for _, cid := range b.Commits {
				full := ix.commits.resolve(cid)
				if !validCommitID(full) {
					continue
				}
				col.add(cand{ix.own, m.ID, rkey{typ: authored.RefCommit, id: full}, ReasonBranch, prioBranch})
			}
		}
	}

	// 3. Snapshots.
	for _, s := range in.Snapshots {
		id, ok := normTaskID(s.TaskID)
		if !ok || ix.task(ix.own, id) == nil || !validCommitID(strings.ToLower(s.Commit)) {
			continue
		}
		full := ix.commits.resolve(s.Commit)
		col.add(cand{ix.own, id, rkey{typ: authored.RefCommit, id: full}, ReasonSnapshot, prioSnapshot})
	}

	// 4. Sessions: text and edits.
	sessions := ownSessions(in)
	for _, s := range sessions {
		sid := strings.ToLower(s.ID)
		texts := make([]string, 0, len(s.UserTexts)+1)
		texts = append(texts, s.Title)
		texts = append(texts, s.UserTexts...)
		for _, text := range texts {
			if text == "" {
				continue
			}
			for _, m := range scanMentions(text) {
				if !m.Upper && !m.TaskPrefix {
					continue // plain lower case "t-1" in chat is too often something else
				}
				proj, ok := ix.target(m)
				if !ok || ix.task(proj, m.ID) == nil {
					continue
				}
				col.add(cand{proj, m.ID, sourceKey(proj, in.ProjectID, authored.RefSession, sid), ReasonSessionText, prioSessionText})
			}
		}
		for _, e := range s.Edited {
			if id, ok := editedTaskID(e); ok && ix.task(ix.own, id) != nil {
				col.add(cand{ix.own, id, rkey{typ: authored.RefSession, id: sid}, ReasonSessionEdit, prioSessionEdit})
			}
		}
		for _, cmd := range s.Commands {
			if !isDrashtaCommand(cmd) {
				continue
			}
			for _, m := range scanMentions(cmd) {
				proj, ok := ix.target(m)
				if !ok || ix.task(proj, m.ID) == nil {
					continue
				}
				col.add(cand{proj, m.ID, sourceKey(proj, in.ProjectID, authored.RefSession, sid), ReasonSessionEdit, prioSessionEdit})
			}
		}
	}

	// 5. Session output: commits made by a session that is linked to a task.
	// A session is linked when it survives the manual / unlink check below, or when the user linked it by hand.
	linked := ix.linkedSessions(col, sessions)
	for _, sc := range SessionCommits(sessions, in.Commits) {
		for _, tid := range linked[strings.ToLower(sc.SessionID)] {
			col.add(cand{ix.own, tid, rkey{typ: authored.RefCommit, id: strings.ToLower(sc.CommitID)}, ReasonSessionOutput, prioSessionOutput})
		}
	}

	return ix.finish(col)
}

// ownSessions keeps the sessions whose own project is this project. A session about
// this project that started elsewhere has another primary project, and a "T-12"
// in it more likely means a task of that project.
func ownSessions(in Input) []SessionInfo {
	var out []SessionInfo
	for _, s := range in.Sessions {
		if s.ID == "" || (s.ProjectID != "" && s.ProjectID != in.ProjectID) {
			continue
		}
		if _, err := authored.ParseRef("session:" + s.ID); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out
}

// linkedSessions returns, for each session id, the sorted task ids of this project the
// session is linked to: by the session rules above (minus unlinks) or by hand.
func (ix *index) linkedSessions(col *collector, sessions []SessionInfo) map[string][]string {
	known := map[string]string{} // lower-case session id -> itself
	for _, s := range sessions {
		known[strings.ToLower(s.ID)] = strings.ToLower(s.ID)
	}
	set := map[string]map[string]bool{}
	put := func(sid, tid string) {
		if set[sid] == nil {
			set[sid] = map[string]bool{}
		}
		set[sid][tid] = true
	}
	for _, c := range col.m {
		if c.taskProj != ix.own || c.key.typ != authored.RefSession || c.key.proj != "" {
			continue
		}
		st := ix.task(ix.own, c.taskID)
		if st == nil || st.rejects(c.key) {
			continue
		}
		put(c.key.id, c.taskID)
	}
	for _, st := range ix.tasks[ix.own] {
		for _, k := range st.manual {
			if k.typ != authored.RefSession || k.proj != "" || st.unlinked(k) {
				continue
			}
			for sid := range known {
				if (rkey{typ: k.typ, id: sid}).same(k) {
					put(sid, st.info.ID)
				}
			}
		}
	}
	out := map[string][]string{}
	for sid, tasks := range set {
		for t := range tasks {
			out[sid] = append(out[sid], t)
		}
		sort.Slice(out[sid], func(i, j int) bool { return taskLess(out[sid][i], out[sid][j]) })
	}
	return out
}

// unlinked says whether the user rejected the key.
func (st *taskState) unlinked(k rkey) bool {
	for _, u := range st.unlinks {
		if u.same(k) {
			return true
		}
	}
	return false
}

// rejects says whether an auto link to the key must be dropped: the task has it
// as a manual link already, or the user rejected it.
func (st *taskState) rejects(k rkey) bool {
	for _, m := range st.manual {
		if m.same(k) {
			return true
		}
	}
	return st.unlinked(k)
}

// finish drops manual and rejected links, sorts, and builds the Link values.
func (ix *index) finish(col *collector) []Link {
	var keep []cand
	for _, c := range col.m {
		st := ix.task(c.taskProj, c.taskID)
		if st == nil || st.rejects(c.key) {
			continue
		}
		keep = append(keep, c)
	}
	sort.Slice(keep, func(i, j int) bool {
		a, b := keep[i], keep[j]
		if a.taskProj != b.taskProj {
			return a.taskProj < b.taskProj
		}
		if a.taskID != b.taskID {
			return taskLess(a.taskID, b.taskID)
		}
		if a.key.typ != b.key.typ {
			return typeRank(a.key.typ) < typeRank(b.key.typ)
		}
		if a.key.proj != b.key.proj {
			return a.key.proj < b.key.proj
		}
		return a.key.id < b.key.id
	})
	out := make([]Link, 0, len(keep))
	for _, c := range keep {
		out = append(out, Link{
			ProjectID: c.taskProj, TaskID: c.taskID, Ref: c.key.String(),
			Reason: c.reason, Source: Source, From: ix.own,
		})
	}
	return out
}

func typeRank(t string) int {
	switch t {
	case authored.RefCommit:
		return 0
	case authored.RefSession:
		return 1
	}
	return 2
}

// taskLess orders "T-2" before "T-10".
func taskLess(a, b string) bool {
	na, ea := strconv.Atoi(strings.TrimPrefix(a, "T-"))
	nb, eb := strconv.Atoi(strings.TrimPrefix(b, "T-"))
	if ea == nil && eb == nil && na != nb {
		return na < nb
	}
	return a < b
}
