package autolink

import (
	"sort"
	"strings"

	"github.com/Prath2000/drashta/internal/authored"
)

// rkey is a ref reduced to what decides "is this the same thing". proj is "" when
// the ref points into the same project as the task that owns the link.
type rkey struct {
	proj string
	typ  string
	id   string
}

// parseKey reads a ref string written in a task file. ok is false for text that is not a ref.
func parseKey(taskProject, ref string) (rkey, bool) {
	r, err := authored.ParseRef(ref)
	if err != nil {
		return rkey{}, false
	}
	k := rkey{proj: r.Project, typ: r.Type, id: r.ID}
	if k.proj == taskProject {
		k.proj = ""
	}
	if k.typ == authored.RefImage {
		k.typ = authored.RefFile
	}
	switch k.typ {
	case authored.RefCommit, authored.RefSession, authored.RefServerKey:
		k.id = strings.ToLower(k.id)
	}
	return k, true
}

// same says whether two keys name the same thing. Commits and sessions may be
// written short (a prefix of the full id), so a prefix counts as the same.
func (a rkey) same(b rkey) bool {
	if a.proj != b.proj || a.typ != b.typ {
		return false
	}
	if a.id == b.id {
		return true
	}
	switch a.typ {
	case authored.RefCommit, authored.RefSession:
		short, long := a.id, b.id
		if len(short) > len(long) {
			short, long = long, short
		}
		return len(short) >= 6 && strings.HasPrefix(long, short)
	}
	return false
}

// String writes the key as a canonical ref. src is the project of the thing the
// ref points at; when it differs from the task's project it is written in front.
func (a rkey) String() string {
	if a.proj != "" {
		return a.proj + ":" + a.typ + ":" + a.id
	}
	return a.typ + ":" + a.id
}

// idSet answers "which full id does this short id belong to?" for commits.
type idSet struct{ sorted []string }

func newIDSet(ids []string) idSet {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return idSet{sorted: out}
}

// resolve returns the longest known id that starts with id (the smallest, when several
// are equally long), or id itself (lower case) when none is known.
func (s idSet) resolve(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	best := id
	for i := sort.SearchStrings(s.sorted, id); i < len(s.sorted) && strings.HasPrefix(s.sorted[i], id); i++ {
		if len(s.sorted[i]) > len(best) {
			best = s.sorted[i]
		}
	}
	return best
}

// validCommitID checks the shape the ref grammar needs: 6 to 64 hex characters.
func validCommitID(id string) bool {
	if len(id) < 6 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
