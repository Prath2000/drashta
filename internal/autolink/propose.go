package autolink

import (
	"sort"
	"strings"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/probe"
)

// ProposeClose looks for a commit that says `Fixes T-12` or `Closes T-12` (also
// fix, fixed, close, closed, resolve, resolves, resolved, with an optional colon
// or `task:` in between) about this task. It returns a proposal to move the task
// to done, or nil.
//
// The proposal is only a suggestion for the Inbox; the user confirms (04 section 3).
// There is no proposal when the task is already done, when the user rejected that
// commit (it is in task.Unlinks), or when the commit names the task with a project
// name in front (`ledger:T-12` is about another project).
// With several matching commits the newest one is used.
//
// ProjectID of the result is empty: Proposals fills it.
func ProposeClose(task TaskInfo, commits []probe.Commit) *Proposal {
	return proposeClose(task, "", commits)
}

func proposeClose(task TaskInfo, project string, commits []probe.Commit) *Proposal {
	id, ok := normTaskID(task.ID)
	if !ok || strings.EqualFold(strings.TrimSpace(task.Status), "done") {
		return nil
	}
	var unlinks []rkey
	for _, u := range task.Unlinks {
		if k, ok := parseKey(project, u); ok {
			unlinks = append(unlinks, k)
		}
	}
	var best *probe.Commit
	for i := range commits {
		c := &commits[i]
		cid := strings.ToLower(c.ID)
		if !validCommitID(cid) || !closesTask(c.Msg+"\n"+c.Body, id, project) {
			continue
		}
		key := rkey{typ: authored.RefCommit, id: cid}
		rejected := false
		for _, u := range unlinks {
			if u.same(key) {
				rejected = true
				break
			}
		}
		if rejected {
			continue
		}
		if best == nil || newer(*c, *best) {
			best = c
		}
	}
	if best == nil {
		return nil
	}
	return &Proposal{
		Kind: KindCloseTask, TaskID: id,
		TaskRef: authored.RefTask + ":" + id, CommitRef: authored.RefCommit + ":" + strings.ToLower(best.ID),
	}
}

// closesTask reports whether text has a closing word right before a mention of id.
// A mention with a project name in front counts only when the name is own (or a trailer word).
func closesTask(text, id, own string) bool {
	for _, m := range scanMentions(text) {
		if m.ID != id {
			continue
		}
		if m.Qual != "" && !isTrailerWord(m.Qual) && (own == "" || m.Qual != own) {
			continue
		}
		if closesBefore(text, m) {
			return true
		}
	}
	return false
}

func newer(a, b probe.Commit) bool {
	if !a.Time.Equal(b.Time) {
		return a.Time.After(b.Time)
	}
	return strings.ToLower(a.ID) < strings.ToLower(b.ID)
}

// Proposals returns a close proposal for every task of in.Tasks that has one, with
// ProjectID set. Order: by task number.
func Proposals(in Input) []Proposal {
	var out []Proposal
	seen := map[string]bool{}
	for _, t := range in.Tasks {
		id, ok := normTaskID(t.ID)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		if p := proposeClose(t, in.ProjectID, in.Commits); p != nil {
			p.ProjectID = in.ProjectID
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return taskLess(out[i].TaskID, out[j].TaskID) })
	return out
}

// SessionCommits pairs each session with the commits it probably produced: commits
// made from the session's first turn until SessionWindow (2 hours) after its last
// turn that contain a file the session edited (04 section 3, "Session output").
// A commit needs Files for this; a commit without Files never matches.
// Edited paths outside the project ("~/..." or absolute) are ignored.
// Order: by session id, then commit id.
func SessionCommits(sessions []SessionInfo, commits []probe.Commit) []SessionCommit {
	var out []SessionCommit
	seen := map[SessionCommit]bool{}
	for _, s := range sessions {
		if s.ID == "" {
			continue
		}
		edited := map[string]bool{}
		for _, e := range s.Edited {
			e = strings.TrimPrefix(e, "./")
			if e == "" || strings.HasPrefix(e, "/") || strings.HasPrefix(e, "~") {
				continue
			}
			edited[e] = true
		}
		if len(edited) == 0 || (s.Start.IsZero() && s.End.IsZero()) {
			continue
		}
		start, end := s.Start, s.End
		if start.IsZero() {
			start = end
		}
		if end.IsZero() || end.Before(start) {
			end = start
		}
		deadline := end.Add(SessionWindow)
		for _, c := range commits {
			if c.ID == "" || c.Time.Before(start) || c.Time.After(deadline) {
				continue
			}
			if !touches(c, edited) {
				continue
			}
			sc := SessionCommit{SessionID: strings.ToLower(s.ID), CommitID: strings.ToLower(c.ID)}
			if !seen[sc] {
				seen[sc] = true
				out = append(out, sc)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SessionID != out[j].SessionID {
			return out[i].SessionID < out[j].SessionID
		}
		return out[i].CommitID < out[j].CommitID
	})
	return out
}

func touches(c probe.Commit, edited map[string]bool) bool {
	for _, f := range c.Files {
		if edited[f.Path] {
			return true
		}
	}
	return false
}
