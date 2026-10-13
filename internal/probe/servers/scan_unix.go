//go:build !windows

package servers

import (
	"context"
	"strconv"
	"strings"

	"github.com/Prath2000/drashta/internal/runx"
)

func listenLsof(ctx context.Context) (string, error) {
	res, err := runx.Run(ctx, runx.Options{Timeout: scanTimeout}, "lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n")
	return lsofExit(res.Stdout, res.ExitCode, err)
}

// enrichRunx fills Cmd from ps and Cwd from lsof, best effort.
func enrichRunx(ctx context.Context, s *Server) {
	pid := strconv.Itoa(s.PID)
	o := runx.Options{Timeout: enrichTimeout}
	if res, err := runx.Run(ctx, o, "ps", "-o", "command=", "-p", pid); err == nil && res.ExitCode == 0 {
		s.Cmd = strings.TrimSpace(string(res.Stdout))
	}
	if res, err := runx.Run(ctx, o, "lsof", "-a", "-d", "cwd", "-p", pid, "-Fn"); err == nil && res.ExitCode == 0 {
		s.Cwd = parseCwd(string(res.Stdout))
	}
}
