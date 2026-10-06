package gh

import (
	"net/url"
	"strings"
	"unicode"
)

// Repo is a GitHub repository on github.com.
type Repo struct {
	Owner string
	Name  string
}

// String gives "owner/name".
func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Spec gives "github.com/owner/name". This is the form we pass to `gh -R`, so a
// user whose default gh host is not github.com still gets the right repo.
func (r Repo) Spec() string { return Host + "/" + r.Owner + "/" + r.Name }

// ParseRemote reads a git remote URL and returns the repo when it is on github.com.
//
// Accepted forms:
//
//	git@github.com:owner/repo.git         (scp style)
//	https://github.com/owner/repo(.git)   (also http, a trailing slash, user@ and a port)
//	ssh://git@github.com/owner/repo.git   (also git:// and git+ssh://)
//
// Anything else gives ok == false: other hosts, extra path parts such as
// /owner/repo/tree/main, empty owner or name, odd characters.
func ParseRemote(remote string) (Repo, bool) {
	s := strings.TrimSpace(remote)
	if s == "" || strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return Repo{}, false
	}
	var host, path string
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return Repo{}, false
		}
		switch strings.ToLower(u.Scheme) {
		case "https", "http", "ssh", "git", "git+ssh", "ssh+git":
		default:
			return Repo{}, false
		}
		host = u.Hostname()
		path = u.Path
	} else {
		// scp style: [user@]host:path
		hostPart, p, ok := strings.Cut(s, ":")
		if !ok || strings.Contains(hostPart, "/") {
			return Repo{}, false
		}
		if i := strings.LastIndexByte(hostPart, '@'); i >= 0 {
			hostPart = hostPart[i+1:]
		}
		host, path = hostPart, p
	}
	host = strings.ToLower(host)
	if host != Host && host != "www."+Host {
		return Repo{}, false
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return Repo{}, false
	}
	owner, name := parts[0], parts[1]
	if !validSegment(owner) || !validSegment(name) {
		return Repo{}, false
	}
	return Repo{Owner: owner, Name: name}, true
}

// validSegment allows letters, digits, '.', '_' and '-'. It refuses "." and "..",
// and a leading '-' so the value can never look like a command line flag.
func validSegment(s string) bool {
	if s == "" || s == "." || s == ".." || s[0] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// okBranch reports whether name is safe to pass after `-b`: not empty, no
// leading '-', no control characters, not the detached marker "HEAD".
func okBranch(name string) bool {
	if name == "" || name == "HEAD" || name[0] == '-' {
		return false
	}
	return strings.IndexFunc(name, unicode.IsControl) < 0
}
