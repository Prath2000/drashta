package projects

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Prath2000/drashta/internal/config"
	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/probe"
)

// Discover lists every project: each folder directly inside projects_dir/<root>
// for every root in the config.
//
//   - Plain files at group level are ignored. A symlink that points at a folder
//     counts as a folder (Path is not resolved).
//   - ID is the folder name (04-data-model.md section 1), so it is safe in a URL.
//     If the same name exists in two or more groups, ALL of them get the id
//     "<group>.<name>" instead. This is decided from the full list, so it does
//     not depend on scan order, and hidden projects count too (hiding a project
//     never changes any id). Project.Group always holds the group.
//   - Hidden is true when the name, "<group>/<name>" or the clash id is on the
//     hide list. Hidden projects are still returned.
//   - Order: config root order, then folder name. The same root listed twice is
//     read once.
//   - A root folder that does not exist is skipped without an error. Any other
//     read error is collected: the projects found are still returned, together
//     with the joined error.
func Discover(cfg *config.Config) ([]probe.Project, error) {
	var out []probe.Project
	var errs []error
	seen := map[string]bool{}
	for _, group := range cfg.Roots {
		if group == "" || seen[group] {
			continue
		}
		seen[group] = true

		groupDir := filepath.Join(cfg.ProjectsDir, group)
		des, err := os.ReadDir(groupDir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			if len(des) == 0 {
				continue
			}
		}
		for _, de := range des {
			if !isDir(groupDir, de) {
				continue
			}
			name := de.Name()
			out = append(out, probe.Project{
				ID:     name,
				Name:   name,
				Group:  group,
				Path:   filepath.Join(groupDir, name),
				Hidden: cfg.IsHidden(group, name),
			})
		}
	}

	// Second pass: a name that appears in more than one group becomes "<group>.<name>" everywhere.
	count := map[string]int{}
	for _, p := range out {
		count[p.Name]++
	}
	for i := range out {
		if count[out[i].Name] > 1 {
			out[i].ID = out[i].Group + "." + out[i].Name
		}
	}
	return out, errors.Join(errs...)
}

// isDir reports whether a directory item is a folder, following one symlink.
func isDir(parent string, de os.DirEntry) bool {
	if de.IsDir() {
		return true
	}
	if de.Type()&os.ModeSymlink == 0 {
		return false
	}
	st, err := os.Stat(filepath.Join(parent, de.Name()))
	return err == nil && st.IsDir()
}

// Info is what Describe learns about one project folder.
type Info struct {
	// Stack lists detected technologies, in a fixed order, never nil:
	// "go", "node", "rust", "python", "vite".
	Stack []string `json:"stack"`
	// Description is the first plain paragraph of the README, 300 characters at most. "" if none.
	Description string `json:"description"`
	// ReadmePath is the README file name relative to the project folder (for example "README.md"). "" if none.
	ReadmePath string `json:"readmePath"`
}

// stackMarkers maps a marker file to a stack name. Order is the output order.
var stackMarkers = []struct{ file, name string }{
	{"go.mod", "go"},
	{"package.json", "node"},
	{"Cargo.toml", "rust"},
	{"pyproject.toml", "python"},
}

// Describe reads a project folder and returns its stack and a short description.
// A folder that does not exist gives an empty Info (no error): the page just
// shows nothing.
func Describe(path string) Info {
	info := Info{Stack: []string{}}
	des, err := os.ReadDir(path)
	if err != nil && len(des) == 0 {
		return info
	}

	files := map[string]bool{}
	hasVite := false
	var readmes []string
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		files[name] = true
		if strings.HasPrefix(name, "vite.config.") {
			hasVite = true
		}
		if strings.HasPrefix(strings.ToUpper(name), "README") {
			readmes = append(readmes, name)
		}
	}
	for _, m := range stackMarkers {
		if files[m.file] {
			info.Stack = append(info.Stack, m.name)
		}
	}
	if hasVite {
		info.Stack = append(info.Stack, "vite")
	}

	if name := pickReadme(readmes); name != "" {
		// A README that is a link out of the project is not read.
		if abs, err := pathguard.Resolve(path, name); err == nil {
			if text, ok := readHead(abs); ok {
				info.ReadmePath = name
				info.Description = firstParagraph(text)
			}
		}
	}
	return info
}

// pickReadme chooses the best README name: .md first, then .markdown, no
// extension, .txt, anything else. Ties go to the shorter name, then A-Z.
func pickReadme(names []string) string {
	rank := func(n string) int {
		switch strings.ToLower(filepath.Ext(n)) {
		case ".md":
			return 0
		case ".markdown":
			return 1
		case "":
			return 2
		case ".txt":
			return 3
		}
		return 4
	}
	sort.Slice(names, func(i, j int) bool {
		ri, rj := rank(names[i]), rank(names[j])
		if ri != rj {
			return ri < rj
		}
		if len(names[i]) != len(names[j]) {
			return len(names[i]) < len(names[j])
		}
		return names[i] < names[j]
	})
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// readmeReadLimit is how much of a README is read to find the description.
const readmeReadLimit = 64 << 10

// readHead reads at most 64 KB of a regular file.
func readHead(abs string) (string, bool) {
	f, err := os.Open(abs)
	if err != nil {
		return "", false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(f, readmeReadLimit))
	if err != nil {
		return "", false
	}
	if !utf8.Valid(b) {
		b = []byte(strings.ToValidUTF8(string(b), "�"))
	}
	return string(b), true
}

// maxDescription is the longest description, counted in characters.
const maxDescription = 300

var (
	atxHeadingRe = regexp.MustCompile(`^\s{0,3}#{1,6}(\s|$)`)
	setextRe     = regexp.MustCompile(`^\s{0,3}(=+|-+)\s*$`)
	ruleRe       = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	fenceRe      = regexp.MustCompile("^\\s{0,3}(```|~~~)")
	listItemRe   = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s`)
	refDefRe     = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*\S+`)
	imageRe      = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	linkRe       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
	emphasisRe   = regexp.MustCompile("(\\*\\*|__|`)")
	spaceRe      = regexp.MustCompile(`\s+`)
)

// firstParagraph returns the first paragraph of README text that is real prose:
// not a heading, code block, list, table, badge line, HTML or front matter.
// Links are reduced to their text and emphasis marks are removed.
func firstParagraph(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	i := 0
	// Skip YAML front matter.
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if t := strings.TrimSpace(lines[j]); t == "---" || t == "..." {
				i = j + 1
				break
			}
		}
	}

	var block []string
	inFence := false
	inComment := false
	flush := func() string {
		defer func() { block = nil }()
		if len(block) == 0 {
			return ""
		}
		return cleanBlock(block)
	}

	for ; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if inComment {
			if strings.Contains(line, "-->") {
				inComment = false
			}
			continue
		}
		if inFence {
			if fenceRe.MatchString(line) {
				inFence = false
			}
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "<!--"):
			if !strings.Contains(trimmed, "-->") {
				inComment = true
			}
			if d := flush(); d != "" {
				return limit(d)
			}
			continue
		case fenceRe.MatchString(line):
			if d := flush(); d != "" {
				return limit(d)
			}
			inFence = true
			continue
		case trimmed == "":
			if d := flush(); d != "" {
				return limit(d)
			}
			continue
		case len(block) > 0 && setextRe.MatchString(line):
			block = nil // the lines above were a heading
			continue
		case atxHeadingRe.MatchString(line), ruleRe.MatchString(line):
			// A heading or a rule ends the paragraph above it.
			if d := flush(); d != "" {
				return limit(d)
			}
			continue
		}
		block = append(block, line)
	}
	return limit(flush())
}

// cleanBlock turns the lines of one block into plain text, or "" if the block
// is not prose.
func cleanBlock(block []string) string {
	first := strings.TrimSpace(block[0])
	switch {
	case strings.HasPrefix(first, "<"), // html, such as <p align="center"> or <img>
		strings.HasPrefix(first, "|"), // table
		listItemRe.MatchString(block[0]),
		refDefRe.MatchString(block[0]),
		strings.HasPrefix(block[0], "    "), strings.HasPrefix(block[0], "\t"): // indented code
		return ""
	}
	parts := make([]string, 0, len(block))
	for _, l := range block {
		l = strings.TrimSpace(l)
		l = strings.TrimLeft(l, "> ")
		parts = append(parts, l)
	}
	s := strings.Join(parts, " ")
	s = imageRe.ReplaceAllString(s, "")
	s = linkRe.ReplaceAllString(s, "$1")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = emphasisRe.ReplaceAllString(s, "")
	s = spaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	// A badge row is left with only punctuation or nothing at all.
	if !hasLetterOrDigit(s) {
		return ""
	}
	return s
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127 {
			return true
		}
	}
	return false
}

// limit cuts s to 300 characters. A cut ends at a word boundary when one is
// near the end, and adds an ellipsis (counted in the 300).
func limit(s string) string {
	r := []rune(s)
	if len(r) <= maxDescription {
		return s
	}
	cut := r[:maxDescription-1]
	// Prefer a word boundary if it is not too far back.
	for i := len(cut) - 1; i > maxDescription*2/3; i-- {
		if cut[i] == ' ' {
			cut = cut[:i]
			break
		}
	}
	out := strings.TrimRight(string(cut), " ,;:.-")
	return out + "…"
}

// EmptyNotice explains why drashta shows no projects, or returns "" when there is at
// least one. It is for the first run: a missing folder, no roots, or empty roots must
// not leave the user with a blank screen. configPath is only used in the text.
func EmptyNotice(cfg *config.Config, configPath string) string {
	list, _ := Discover(cfg)
	if len(list) > 0 {
		return ""
	}
	fix := "set projects_dir or roots in " + configPath
	if _, err := os.Stat(cfg.ProjectsDir); err != nil {
		return "no projects found: " + cfg.ProjectsDir + " does not exist; " + fix
	}
	if len(cfg.Roots) == 0 {
		return "no projects found under " + cfg.ProjectsDir + "; drashta expects projects_dir/<group>/<project>; " + fix
	}
	return "no projects found under " + cfg.ProjectsDir + " (groups: " + strings.Join(cfg.Roots, ", ") + "); " + fix
}
