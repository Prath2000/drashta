//go:build !windows

package actions

// terminalScript is the FIXED AppleScript used to run a command in Terminal.
// argv item 1 is the folder, the rest is the command argv. Every item is
// shell-quoted with "quoted form of", so no data is ever built into script
// source and no data is ever parsed by the shell as code.
var terminalScript = []string{
	"on run argv",
	"set cmd to \"cd \" & quoted form of (item 1 of argv) & \" &&\"",
	"repeat with i from 2 to count of argv",
	"set cmd to cmd & \" \" & quoted form of (item i of argv)",
	"end repeat",
	"tell application \"Terminal\"",
	"activate",
	"do script cmd",
	"end tell",
	"end run",
}

// terminalCommand wraps argv to run in a new Terminal window inside dir.
func terminalCommand(dir string, argv ...string) Command {
	args := make([]string, 0, 2*len(terminalScript)+2+len(argv))
	for _, line := range terminalScript {
		args = append(args, "-e", line)
	}
	args = append(args, "--", dir)
	args = append(args, argv...)
	return Command{Name: "osascript", Args: args}
}

// openInTerminalApp opens dir p in the named terminal app.
func openInTerminalApp(term, p string) Command {
	return Command{Name: "open", Args: []string{"-a", term, p}}
}

func previewCommand(p string) Command {
	return Command{Name: "open", Args: []string{"-a", "Preview", p}}
}

func revealCommand(p string) Command {
	return Command{Name: "open", Args: []string{"-R", p}}
}

// openDefaultCommand opens p with the app macOS picks ("--" so a path is never a flag).
func openDefaultCommand(p string) Command {
	return Command{Name: "open", Args: []string{"--", p}}
}

func browserCommand(u string) Command {
	return Command{Name: "open", Args: []string{"-a", "Safari", u}}
}
