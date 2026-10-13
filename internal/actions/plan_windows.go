//go:build windows

package actions

import (
	"os/exec"
	"strings"
)

// Windows equivalents of the macOS "open" family. Nothing here goes through a shell:
// rundll32 and explorer take their arguments as plain argv entries.

// cmdMeta are characters cmd.exe would act on; the cmd fallback refuses them.
const cmdMeta = "&|<>^%\"()\r\n"

// terminalCommand runs argv in a new Windows Terminal tab inside dir; without wt.exe it
// falls back to a new cmd window. The "env VAR=value" prefix used on unix has no Windows
// equivalent here, so the variables are dropped (the child inherits drashta's environment).
func terminalCommand(dir string, argv ...string) Command {
	for len(argv) > 0 && argv[0] == "env" {
		argv = argv[1:]
		for len(argv) > 0 && strings.Contains(argv[0], "=") {
			argv = argv[1:]
		}
	}
	if _, err := exec.LookPath("wt"); err == nil {
		return Command{Name: "wt", Args: append([]string{"-d", dir, "--"}, argv...)}
	}
	args := []string{"/c", "start", "drashta", "/D", dir, "cmd", "/k"}
	for _, a := range argv {
		if strings.ContainsAny(a, cmdMeta) {
			a = "invalid"
		}
		args = append(args, a)
	}
	return Command{Name: "cmd", Args: args}
}

// openInTerminalApp opens a terminal in dir p. The configured name is a macOS app name and is
// ignored; wt is used when installed, else cmd.
func openInTerminalApp(term, p string) Command {
	if _, err := exec.LookPath("wt"); err == nil {
		return Command{Name: "wt", Args: []string{"-d", p}}
	}
	return Command{Name: "cmd", Args: []string{"/c", "start", "drashta", "/D", p, "cmd"}}
}

func previewCommand(p string) Command { return openDefaultCommand(p) }

func revealCommand(p string) Command {
	return Command{Name: "explorer", Args: []string{"/select," + p}}
}

func openDefaultCommand(p string) Command {
	return Command{Name: "rundll32", Args: []string{"url.dll,FileProtocolHandler", p}}
}

// browserCommand opens the URL in the default browser (the URL is already validated https).
func browserCommand(u string) Command { return openDefaultCommand(u) }
