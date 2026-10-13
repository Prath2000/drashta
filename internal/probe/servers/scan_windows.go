//go:build windows

package servers

import (
	"context"
	"strconv"
	"strings"

	"github.com/Prath2000/drashta/internal/runx"
)

// listenLsof lists listening TCP sockets with `netstat -ano -p tcp` and maps
// PIDs to process names with `tasklist`. It returns lsof-shaped text so the
// shared parser keeps working. The name is best effort.
func listenLsof(ctx context.Context) (string, error) {
	res, err := runx.Run(ctx, runx.Options{Timeout: scanTimeout}, "netstat", "-ano", "-p", "tcp")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", errExit("netstat", res.ExitCode)
	}
	rows := parseNetstat(string(res.Stdout))
	var names map[int]string
	if len(rows) > 0 {
		if tl, err := runx.Run(ctx, runx.Options{Timeout: scanTimeout}, "tasklist", "/FO", "CSV", "/NH"); err == nil && tl.ExitCode == 0 {
			names = parseTasklist(string(tl.Stdout))
		}
	}
	return lsofLike(rows, names), nil
}

// enrichRunx fills Cmd from the process command line (PowerShell CIM). Cwd is
// not available on Windows and stays empty.
func enrichRunx(ctx context.Context, s *Server) {
	o := runx.Options{Timeout: enrichTimeout}
	script := "(Get-CimInstance Win32_Process -Filter 'ProcessId=" + strconv.Itoa(s.PID) + "').CommandLine"
	res, err := runx.Run(ctx, o, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err == nil && res.ExitCode == 0 {
		s.Cmd = strings.TrimSpace(string(res.Stdout))
	}
}
