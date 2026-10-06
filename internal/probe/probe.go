// Package probe holds the types every probe shares.
//
// Each probe lives in its own sub-package and exposes typed functions
// (Scan, Detail, ...) instead of one generic Scan, because the results differ.
// The indexer calls them directly. The shared types are here so the git and
// fossil probes, the store and the HTTP layer all speak the same language.
package probe

import "time"

// Project is the input to every probe. ID is the folder name, or "<group>.<name>"
// when two groups hold the same name (04 section 1). IDs never contain a slash,
// because they appear in URLs.
type Project struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	Path   string `json:"path"` // absolute, symlinks not resolved
	Hidden bool   `json:"hidden"`
}

// Probe is the minimum every probe package's main type offers.
type Probe interface {
	Name() string
}

// Commit is one git or fossil check-in. JSON names match 04-data-model.md section 6.
type Commit struct {
	ID     string       `json:"id"`
	VCS    string       `json:"vcs"` // "git" | "fossil"
	Author string       `json:"author"`
	Time   time.Time    `json:"ts"`
	Msg    string       `json:"msg"` // subject line
	Body   string       `json:"body,omitempty"`
	Plus   int          `json:"plus"`
	Minus  int          `json:"minus"`
	NFiles int          `json:"nfiles"`
	Branch string       `json:"branch,omitempty"`
	Files  []CommitFile `json:"files,omitempty"`
}

// CommitFile is one file touched by a commit (numstat row).
type CommitFile struct {
	Path  string `json:"path"`
	Plus  int    `json:"plus"`
	Minus int    `json:"minus"`
}

// CommitDetail is a commit plus its patch, fetched live (never cached).
type CommitDetail struct {
	Commit
	Patch          string `json:"patch"`
	PatchTruncated bool   `json:"patchTruncated,omitempty"`
}

// DirtyFile is one uncommitted path. State is the short porcelain code:
// "M" modified, "A" added, "D" deleted, "R" renamed, "?" untracked, "U" conflicted.
type DirtyFile struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

// Unavailable is returned (as a Result field, not an error) when a tool is missing
// or not logged in. The UI greys the part out and shows Reason.
type Unavailable struct {
	Reason string `json:"reason"`
}
