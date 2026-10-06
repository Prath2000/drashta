package indexer

import (
	"path/filepath"
	"strings"

	"github.com/Prath2000/drashta/internal/probe/tree"
)

// router turns one watched path into jobs (03-architecture.md section 4, routing table).
// A router is an immutable snapshot: the indexer builds a new one when the
// project list changes, so routing never takes a lock.
type router struct {
	projectsDirs []string          // projects_dir, cleaned (and its symlink-resolved form)
	roots        map[string]bool   // group folder names
	byRel        map[string]string // "<group>/<name>" -> project id
	hidden       map[string]bool   // project id -> hidden
	skipNames    map[string]bool   // folder names never walked
	skipPaths    map[string]bool   // extra skip entries with a "/" (relative paths)
	profiles     []profileDir      // <profile>/projects folders
	fossilRepos  map[string]string // repository file -> project id (repo may live outside the project)
	worktrees    map[string]string // gitdir of a linked worktree (<main>/.git/worktrees/<n>) -> project id
}

type profileDir struct {
	dir  string // <profile>/projects, cleaned
	name string // profile folder name, for example ".claude-work"
}

// routed is what one event becomes.
type routed struct {
	reqs     []request
	sweep    bool   // a full sweep is needed (MustScan on projects_dir)
	project  string // file event inside a project: count it as activity
	rel      string // its path relative to the project
	activity bool
}

// The .git files that matter to the git probe.
var gitFiles = map[string]bool{"HEAD": true, "index": true, "packed-refs": true, "FETCH_HEAD": true}

func newRouter(projectsDir string, roots []string, extraSkip []string, profiles []string) *router {
	r := &router{
		roots:       map[string]bool{},
		byRel:       map[string]string{},
		hidden:      map[string]bool{},
		skipNames:   map[string]bool{},
		skipPaths:   map[string]bool{},
		fossilRepos: map[string]string{},
		worktrees:   map[string]string{},
	}
	r.projectsDirs = pathForms(projectsDir)
	for _, g := range roots {
		r.roots[g] = true
	}
	for _, n := range tree.DefaultSkipNames() {
		r.skipNames[n] = true
	}
	for _, s := range extraSkip {
		s = strings.Trim(filepath.ToSlash(s), "/")
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			r.skipPaths[s] = true
		} else {
			r.skipNames[s] = true
		}
	}
	for _, p := range profiles {
		for _, d := range pathForms(filepath.Join(p, "projects")) {
			r.profiles = append(r.profiles, profileDir{dir: d, name: filepath.Base(p)})
		}
	}
	return r
}

// pathForms returns the cleaned path, plus its symlink-resolved form and the
// form without the macOS "/private" prefix when those differ.
func pathForms(p string) []string {
	if p == "" {
		return nil
	}
	c := filepath.Clean(p)
	out := []string{c}
	add := func(s string) {
		for _, o := range out {
			if o == s {
				return
			}
		}
		out = append(out, s)
	}
	if real, err := filepath.EvalSymlinks(c); err == nil {
		add(filepath.Clean(real))
	}
	for _, s := range append([]string(nil), out...) {
		if strings.HasPrefix(s, "/private/") {
			add(strings.TrimPrefix(s, "/private"))
		}
	}
	return out
}

// relUnder returns path relative to base when path is base or inside it.
func relUnder(base, path string) (string, bool) {
	if path == base {
		return "", true
	}
	if strings.HasPrefix(path, base+string(filepath.Separator)) {
		return path[len(base)+1:], true
	}
	return "", false
}

func (r *router) underProjects(p string) (string, bool) {
	for _, cand := range []string{p, strings.TrimPrefix(p, "/private")} {
		for _, d := range r.projectsDirs {
			if rel, ok := relUnder(d, cand); ok {
				return rel, true
			}
		}
	}
	return "", false
}

func (r *router) underProfile(p string) (profileDir, string, bool) {
	for _, cand := range []string{p, strings.TrimPrefix(p, "/private")} {
		for _, d := range r.profiles {
			if rel, ok := relUnder(d.dir, cand); ok {
				return d, rel, true
			}
		}
	}
	return profileDir{}, "", false
}

// route decides what to do with one event.
func (r *router) route(ev WatchEvent) routed {
	p := filepath.Clean(ev.Path)
	mustScan := ev.Kind == MustScan

	if repoProject, ok := r.fossilRepos[p]; ok {
		return routed{reqs: []request{{key: jobKey{bucket: repoProject, kind: kindVCS}, force: true}}}
	}
	// A commit, `git add` or checkout in a linked worktree changes only
	// <main>/.git/worktrees/<n>/{HEAD,index}: that is the worktree project's VCS state (I6).
	if b := filepath.Base(p); b == "HEAD" || b == "index" {
		dir := filepath.Dir(p)
		for _, cand := range []string{dir, strings.TrimPrefix(dir, "/private")} {
			if id, ok := r.worktrees[cand]; ok {
				return routed{reqs: []request{{key: jobKey{bucket: id, kind: kindVCS}}}}
			}
		}
	}

	if rel, ok := r.underProjects(p); ok {
		return r.routeProjects(p, filepath.ToSlash(rel), mustScan)
	}
	if prof, rel, ok := r.underProfile(p); ok {
		return r.routeClaude(p, prof, filepath.ToSlash(rel), mustScan)
	}
	return routed{}
}

func (r *router) routeProjects(abs, rel string, mustScan bool) routed {
	projectsJob := request{key: jobKey{bucket: bucketProjects, kind: kindProjects}}
	if rel == "" {
		if mustScan {
			return routed{sweep: true, reqs: []request{projectsJob}}
		}
		return routed{reqs: []request{projectsJob}}
	}
	parts := strings.Split(rel, "/")
	if !r.roots[parts[0]] {
		return routed{} // not a group folder (or a plain file next to the groups)
	}
	if len(parts) <= 2 {
		// the group folder itself, or a direct child of it: the project list may have changed
		out := routed{reqs: []request{projectsJob}}
		if mustScan {
			if len(parts) == 1 {
				out.sweep = true
			} else if id, ok := r.byRel[parts[0]+"/"+parts[1]]; ok {
				out.reqs = append(out.reqs, request{key: jobKey{bucket: id, kind: kindFull}})
			}
		}
		return out
	}
	id, ok := r.byRel[parts[0]+"/"+parts[1]]
	if !ok {
		// a project we do not know yet: rediscover
		return routed{reqs: []request{projectsJob}}
	}
	if mustScan {
		return routed{reqs: []request{{key: jobKey{bucket: id, kind: kindFull}}}}
	}
	inner := parts[2:]
	innerRel := strings.Join(inner, "/")

	switch inner[0] {
	case ".git":
		if len(inner) == 2 && gitFiles[inner[1]] {
			return routed{reqs: []request{{key: jobKey{bucket: id, kind: kindVCS}}}}
		}
		if len(inner) >= 2 && inner[1] == "refs" {
			push := len(inner) >= 3 && inner[2] == "remotes"
			return routed{reqs: []request{{key: jobKey{bucket: id, kind: kindVCS}, push: push}}}
		}
		return routed{}
	case ".fslckout", "_FOSSIL_", ".fslckout-wal", ".fslckout-journal":
		if len(inner) == 1 {
			return routed{reqs: []request{{key: jobKey{bucket: id, kind: kindVCS}}}}
		}
	case ".drashta":
		return routed{reqs: []request{{key: jobKey{bucket: id, kind: kindAuthored}, path: abs}}}
	}
	if len(inner) == 1 && strings.HasSuffix(inner[0], ".fossil") {
		return routed{reqs: []request{{key: jobKey{bucket: id, kind: kindVCS}}}}
	}
	if r.skipped(inner) {
		return routed{}
	}
	if r.hidden[id] {
		return routed{} // hidden projects are not walked until they are shown again
	}
	return routed{
		reqs: []request{
			{key: jobKey{bucket: id, kind: kindTree}, path: innerRel},
			{key: jobKey{bucket: id, kind: kindVCS}, dirty: true},
		},
		project: id, rel: innerRel, activity: true,
	}
}

// skipped reports whether a project-relative path is inside a skip folder.
func (r *router) skipped(parts []string) bool {
	for i, part := range parts {
		if r.skipNames[part] {
			return true
		}
		if len(r.skipPaths) > 0 && r.skipPaths[strings.Join(parts[:i+1], "/")] {
			return true
		}
	}
	return false
}

func (r *router) routeClaude(abs string, prof profileDir, rel string, mustScan bool) routed {
	all := routed{reqs: []request{{key: jobKey{bucket: bucketClaude, kind: kindClaudeAll}}}}
	if mustScan {
		return all
	}
	if !strings.HasSuffix(rel, ".jsonl") {
		if rel == "" || !strings.Contains(rel, "/") {
			return all // the projects folder or a session folder appeared or went away
		}
		return routed{}
	}
	parts := strings.Split(rel, "/")
	var parent string
	switch {
	case len(parts) == 2: // <enc>/<id>.jsonl
		parent = abs
	case len(parts) == 4 && parts[2] == "subagents": // <enc>/<id>/subagents/<x>.jsonl
		parent = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(abs))), parts[1]+".jsonl")
	default:
		return routed{}
	}
	return routed{reqs: []request{{key: jobKey{bucket: bucketClaude, kind: kindClaude, sub: parent + "\x00" + prof.name}, path: abs}}}
}
