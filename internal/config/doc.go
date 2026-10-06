// Package config reads and writes ~/.drashta/config.toml.
//
// The file is created with defaults when it is missing (mode 0600, folder 0700).
// A key that is missing keeps its default. Two defaults come from the disk when the key
// is absent: roots (DiscoverRoots over projects_dir) and claude_profiles (~/.claude plus
// every existing ~/.claude-*). A key that is present always wins, also "roots = []". drashta never rewrites the file except
// through SetHidden. The editor is set only by editing the file by hand; Load
// checks it (ValidateEditor) and falls back to DefaultEditor with a warning.
//
// Exported API:
//
//	type Config struct                          settings; fields match the TOML keys
//	    ProjectsDir string                      projects_dir, "~" already expanded
//	    Roots, Hidden, Skip []string            roots, hidden, skip
//	    Editor []string                         editor argv template
//	    Terminal string                         terminal app name
//	    ClaudeProfiles []string                 claude_profiles, "~" already expanded
//	    GHEnabled bool                          gh_enabled
//	    DesktopScript string                    desktop_script (plash-mode), "~" already expanded; only config.toml sets it
//	    Chitralipi string                       chitralipi (DWG reader program), "~" already expanded; only config.toml sets it
//	    Warnings []string                       problems Load fixed itself (log them once)
//
//	func Default() *Config                      built-in settings ("~" not expanded); roots and claude_profiles nil (Load fills them), hidden and desktop_script empty
//	func Load(path string) (*Config, error)     read config.toml; create it when missing; expand "~"
//	func DiscoverRoots(projectsDir string) []string
//	                                            group folders found on disk: visible folders that are not projects but hold folders
//
//	func DataDir() string                       $DRASHTA_HOME, else ~/.drashta (not created)
//	func DefaultPath() string                   DataDir()/config.toml
//	func (c *Config) RootPaths() []string       ProjectsDir/<root> for every root, in order
//	func (c *Config) IsHidden(group, name string) bool
//	                                            true if Hidden has "name", "group/name" or "group.name"
//	func (c *Config) SetHidden(path string, names []string) error
//	                                            rewrite only the hidden line in the file at
//	                                            path (atomic) and update c.Hidden
//	func ValidateEditor(argv []string) error    editor rules: bare name or clean absolute path,
//	                                            no shell/interpreter, only {path} {line}
//	var DefaultEditor = ["code","-g","{path}:{line}"]
//	func (c *Config) HiddenList() []string      copy of Hidden (lock held)
//	func (c *Config) EditorList() []string      copy of Editor (lock held)
//	const DataDirEnv = "DRASHTA_HOME"
//
// Thread safety: a Config holds an RWMutex. SetHidden is the only writer and
// takes it. While it may run, read the hide list only through IsHidden and
// HiddenList (never c.Hidden). The other fields never change after Load. Do not copy a Config by value (go vet checks).
package config
