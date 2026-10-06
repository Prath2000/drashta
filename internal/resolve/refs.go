package resolve

import (
	"fmt"
	"strings"

	"github.com/Prath2000/drashta/internal/authored"
)

// ParseRef parses a ref string with authored.ParseRef. It also accepts the type "doc",
// which the web client uses for documents: "doc:x.md" is read as "file:x.md".
func ParseRef(s string) (authored.Ref, error) {
	s = strings.TrimSpace(s)
	body := s
	if i := strings.IndexByte(s, '#'); i >= 0 {
		body = s[:i]
	}
	parts := strings.SplitN(body, ":", 3)
	switch {
	case len(parts) >= 2 && parts[0] == "doc":
		s = "file" + s[len("doc"):]
	case len(parts) == 3 && parts[1] == "doc":
		s = parts[0] + ":file" + s[len(parts[0])+len(":doc"):]
	}
	r, err := authored.ParseRef(s)
	if err != nil {
		return authored.Ref{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return r, nil
}

// canonType maps "image" to "file".
func canonType(t string) string {
	if t == authored.RefImage {
		return authored.RefFile
	}
	return t
}

// SameItem reports whether a and b point at the same item. Fragments are ignored.
// Commit and session ids match when one is a prefix of the other (short and full ids).
// Both refs should already carry their project (see Resolver.Qualify).
func SameItem(a, b authored.Ref) bool {
	if canonType(a.Type) != canonType(b.Type) {
		return false
	}
	if a.Type != authored.RefSession && a.Project != b.Project {
		return false
	}
	x, y := a.ID, b.ID
	switch a.Type {
	case authored.RefCommit, authored.RefSession, authored.RefServerKey:
		x, y = strings.ToLower(x), strings.ToLower(y)
		if len(x) > len(y) {
			x, y = y, x
		}
		return len(x) >= 6 && strings.HasPrefix(y, x)
	}
	return x == y
}
