//go:build !windows

package actions

import (
	"errors"
	"reflect"
	"testing"
)

// Project + editor and project + safari (added for the Project page, agent b-api-tracker).

func TestProjectEditor(t *testing.T) {
	f := newFix(t)
	got, err := f.cfg.Plan(Target{Type: TypeProject, ProjectRoot: f.root}, ActionEditor)
	if err != nil {
		t.Fatal(err)
	}
	want := Command{Name: "code", Args: []string{"-g", f.realDir}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// A template without {path}:{line} keeps its other items; {line} alone becomes 1.
	cfg := f.cfg
	cfg.Editor = []string{"zed", "--line={line}"}
	got, err = cfg.Plan(Target{Type: TypeProject, ProjectRoot: f.root}, ActionEditor)
	if err != nil {
		t.Fatal(err)
	}
	want = Command{Name: "zed", Args: []string{"--line=1", f.realDir}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// Folder targets still refuse the editor.
	if _, err := f.cfg.Plan(Target{Type: TypeFolder, ProjectRoot: f.root, Path: "src"}, ActionEditor); !errors.Is(err, ErrAction) {
		t.Fatalf("folder editor: %v", err)
	}
	// A project root outside the roots is refused.
	if _, err := f.cfg.Plan(Target{Type: TypeProject, ProjectRoot: t.TempDir()}, ActionEditor); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outside root: %v", err)
	}
}

func TestGitHubURL(t *testing.T) {
	good := map[string]string{
		"https://github.com/example/drashta":       "https://github.com/example/drashta",
		"https://github.com/example/drashta.git":   "https://github.com/example/drashta",
		"https://github.com/example/drashta/":      "https://github.com/example/drashta",
		"git@github.com:example/drashta.git":       "https://github.com/example/drashta",
		"ssh://git@github.com/example/drashta.git": "https://github.com/example/drashta",
		"https://token@github.com/o/r":             "https://github.com/o/r",
		"  https://github.com/o-x/r_y.z.git  ":     "https://github.com/o-x/r_y.z",
		"https://GitHub.com/Owner/Repo":            "https://github.com/Owner/Repo",
		"git://github.com/owner/repo.git":          "https://github.com/owner/repo",
		"https://github.com/owner/repo.name.git":   "https://github.com/owner/repo.name",
		"ssh://git@github.com/owner/repo":          "https://github.com/owner/repo",
		"https://github.com/owner/-repo-with-dash": "",
	}
	for in, want := range good {
		got, err := GitHubURL(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q: want error, got %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "file:///Users/x/repo", "/Users/x/repo.git", "../repo", "https://gitlab.com/o/r",
		"https://github.com.evil.com/o/r", "https://evil.com/github.com/o/r", "http://github.com/o/r",
		"https://github.com/o", "https://github.com/o/r/tree/main", "https://github.com/o/r?x=1",
		"https://github.com:8443/o/r", "git@gitlab.com:o/r.git", "https://github.com/../r",
		"https://github.com/-o/r", "javascript:alert(1)", "https://github.com/o/r#frag",
	}
	for _, in := range bad {
		if got, err := GitHubURL(in); !errors.Is(err, ErrForbidden) {
			t.Errorf("%q: want ErrForbidden, got %q, %v", in, got, err)
		}
	}
}

func TestProjectSafari(t *testing.T) {
	f := newFix(t)
	cases := []struct {
		name   string
		target Target
		want   []string
		err    error
	}{
		{"github https", Target{Type: TypeProject, ProjectRoot: f.root, URL: "https://github.com/o/r.git"},
			[]string{"-a", "Safari", "https://github.com/o/r"}, nil},
		{"github ssh", Target{Type: TypeProject, ProjectRoot: f.root, URL: "git@github.com:o/r.git"},
			[]string{"-a", "Safari", "https://github.com/o/r"}, nil},
		{"not github", Target{Type: TypeProject, ProjectRoot: f.root, URL: "https://gitlab.com/o/r"}, nil, ErrForbidden},
		{"file remote", Target{Type: TypeProject, ProjectRoot: f.root, URL: "file:///tmp/r.git"}, nil, ErrForbidden},
		{"no remote", Target{Type: TypeProject, ProjectRoot: f.root}, nil, ErrForbidden},
		{"outside roots", Target{Type: TypeProject, ProjectRoot: t.TempDir(), URL: "https://github.com/o/r"}, nil, ErrForbidden},
		{"folder", Target{Type: TypeFolder, ProjectRoot: f.root, Path: "src", URL: "https://github.com/o/r"}, nil, ErrAction},
	}
	for _, c := range cases {
		got, err := f.cfg.Plan(c.target, ActionSafari)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("%s: want %v, got %v (%v)", c.name, c.err, err, got)
			}
			continue
		}
		if err != nil || got.Name != "open" || !reflect.DeepEqual(got.Args, c.want) {
			t.Errorf("%s: got %v, %v", c.name, got, err)
		}
	}
}
