package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// project finds the project folder once per run.
//
// Order: the --project option; then the nearest folder above Cwd that has a .drashta folder
// (never Home itself, because ~/.drashta is drashta's own data); then, inside
// <ProjectsDir>/<group>/<name>, that folder. The last case is for a project that has no
// .drashta yet: the first write creates it.
func (a *app) project(p *parsed) (string, error) {
	if a.root != "" {
		return a.root, nil
	}
	root, err := a.findProject(p.str("project"))
	if err != nil {
		return "", err
	}
	a.root = root
	return root, nil
}

func (a *app) findProject(explicit string) (string, error) {
	if explicit != "" {
		dir := explicit
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(a.env.Cwd, dir)
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return "", fmt.Errorf("project folder %s: %w", explicit, err)
		}
		if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
			return "", fmt.Errorf("project folder %s is not a folder", explicit)
		}
		return real, nil
	}

	cwd, err := filepath.EvalSymlinks(a.env.Cwd)
	if err != nil {
		return "", fmt.Errorf("working folder %s: %w", a.env.Cwd, err)
	}
	home := ""
	if a.env.Home != "" {
		if h, err := filepath.EvalSymlinks(a.env.Home); err == nil {
			home = h
		}
	}
	for dir := cwd; ; {
		if dir == home {
			break
		}
		if fi, err := os.Stat(filepath.Join(dir, ".drashta")); err == nil && fi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	if a.env.ProjectsDir != "" {
		if pd, err := filepath.EvalSymlinks(a.env.ProjectsDir); err == nil && pathguard.Inside(pd, cwd) {
			rel, _ := filepath.Rel(pd, cwd)
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) >= 2 {
				return filepath.Join(pd, parts[0], parts[1]), nil
			}
		}
	}
	return "", fmt.Errorf("no project found: there is no .drashta folder above %s, and it is not inside a project folder (use -C <dir>)", a.env.Cwd)
}
