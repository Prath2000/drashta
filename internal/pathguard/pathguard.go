// Package pathguard decides whether a path may be read or opened.
//
// Rule (03-architecture.md sections 7 and 8): the path is cleaned, symlinks are
// resolved with EvalSymlinks, and the result must be inside a known project
// root. Everything that reads a file for the page, and every native action,
// goes through here.
package pathguard

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrOutside means the path resolves outside every allowed root.
var ErrOutside = errors.New("pathguard: path is outside the allowed roots")

// Resolve joins rel onto root, resolves symlinks and checks the result stays
// inside root. rel may be empty (the root itself). Absolute rel is rejected.
// The path must exist, because EvalSymlinks needs it.
func Resolve(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", ErrOutside
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.Clean("/"+rel)))
	if err != nil {
		return "", err
	}
	if !Inside(realRoot, full) {
		return "", ErrOutside
	}
	return full, nil
}

// ResolveAny resolves an absolute path and requires it to be inside one of roots.
func ResolveAny(roots []string, abs string) (string, error) {
	if !filepath.IsAbs(abs) {
		return "", ErrOutside
	}
	full, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		return "", err
	}
	for _, r := range roots {
		realRoot, err := filepath.EvalSymlinks(r)
		if err != nil {
			continue
		}
		if Inside(realRoot, full) {
			return full, nil
		}
	}
	return "", ErrOutside
}

// Inside reports whether p is root itself or below it. Both must already be
// cleaned and symlink-free; it is a pure string check on path elements, so
// "/a/bc" is not inside "/a/b".
func Inside(root, p string) bool {
	root = filepath.Clean(root)
	p = filepath.Clean(p)
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}
