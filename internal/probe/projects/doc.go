// Package projects is the project discovery probe. A project is every folder
// directly inside projects_dir/<root> for each root in the config
// (03-architecture.md section 3, projects).
//
// Exported API:
//
//	func Discover(cfg *config.Config) ([]probe.Project, error)
//	    Every project folder, in config root order and then by folder name.
//	    ID is the folder name (URL safe, no slash). When the same name exists in
//	    two or more groups, every one of them gets "<group>.<name>" instead. That
//	    is decided from the whole list, so it is stable between runs and does not
//	    depend on scan order or on the hide list. Group always holds the group.
//	    Path is absolute and symlinks are not resolved.
//	    Hidden is set from cfg.Hidden (a name like "venv", "tools/venv" or the clash id).
//	    Hidden projects are still returned. Plain files at group level are ignored.
//	    A missing root folder is skipped without an error. Another read error
//	    (for example permission denied) is returned joined, together with the
//	    projects that were found, so check the list even when err != nil.
//
//	type Info struct
//	    Stack       []string   "go" | "node" | "rust" | "python" | "vite", fixed order, never nil
//	    Description string     first prose paragraph of the README, 300 characters at most, "" if none
//	    ReadmePath  string     README file name relative to the project folder, "" if none
//
//	func Describe(path string) Info
//	    Reads the project folder (not deeper): marker files for the stack
//	    (go.mod, package.json, Cargo.toml, pyproject.toml, vite.config.*) and the
//	    best README (README.md, then .markdown, no extension, .txt, other).
//	    A README that is a link leaving the project is not read. A folder that
//	    does not exist gives an empty Info.
//
// The description skips headings, badges, HTML, code blocks, lists, tables,
// comments and front matter. Links are reduced to their text.
package projects
