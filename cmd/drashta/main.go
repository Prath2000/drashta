// Command drashta is the local command center for ~/projects.
//
//	drashta [-port 4455] [-dev]        run the server (the launchd service runs this)
//	drashta -reindex                   drop the cache tables, rescan everything, exit
//	drashta reindex [project]          same; with a project only that project's rows
//	drashta -version                   print the version and exit
//	drashta task|note|snap ...         tracker commands (no server needed); see "drashta task -h"
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Prath2000/drashta/internal/app"
	"github.com/Prath2000/drashta/internal/cli"
	"github.com/Prath2000/drashta/internal/httpapi"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "task", "note", "snap", "help":
			// Tracker commands talk to the .drashta files directly; no server is started.
			return cli.Run(args, stdout, stderr, cli.DefaultEnv())
		}
	}

	fs := flag.NewFlagSet("drashta", flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := fs.Int("port", app.DefaultPort, "port to listen on (127.0.0.1 only)")
	dev := fs.Bool("dev", false, "dev mode: serve no page (Vite does) and allow Origin localhost:5173")
	reindex := fs.Bool("reindex", false, "drop the cache tables, rescan everything, then exit")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: drashta [-port N] [-dev] | -reindex | reindex [project] | -version | task|note|snap ...")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "drashta", version)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := app.Options{Port: *port, Dev: *dev, LogStderr: isTerminal(os.Stderr)}

	rest := fs.Args()
	if *reindex || (len(rest) > 0 && rest[0] == "reindex") {
		project := ""
		if len(rest) > 1 {
			project = rest[1]
		}
		if len(rest) > 2 {
			fmt.Fprintln(stderr, "usage: drashta reindex [project]")
			return 2
		}
		if err := app.Reindex(ctx, opts, project, stdout); err != nil {
			fmt.Fprintln(stderr, "drashta:", err)
			return 1
		}
		return 0
	}
	if len(rest) > 0 {
		fmt.Fprintf(stderr, "drashta: unknown command %q\n", rest[0])
		fs.Usage()
		return 2
	}

	err := app.Run(ctx, opts)
	switch {
	case errors.Is(err, app.ErrAlreadyRunning):
		fmt.Fprintf(stderr, "drashta: already running on port %d\n", *port)
		return 0
	case errors.Is(err, syscall.EADDRINUSE), errors.Is(err, httpapi.ErrPortBusy):
		fmt.Fprintf(stderr, "drashta: port %d is busy; use -port\n", *port)
		return 1
	case err != nil:
		fmt.Fprintln(stderr, "drashta:", err)
		return 1
	}
	return 0
}

// isTerminal reports whether f is a character device (a terminal), so log lines are
// also shown there. Under launchd stderr is the log file, so they are not doubled.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
