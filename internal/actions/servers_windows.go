//go:build windows

package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/Prath2000/drashta/internal/probe/servers"
	"github.com/Prath2000/drashta/internal/runx"
)

// killPID ends the process tree with taskkill (no shell); /F after a graceful try is
// not possible for console apps, so a plain /T is tried first, then /F.
func killPID(pid int) error {
	if pid <= 4 {
		return fmt.Errorf("refusing to kill pid %d", pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := strconv.Itoa(pid)
	if _, err := runx.Run(ctx, runx.Options{}, "taskkill", "/PID", id, "/T"); err == nil {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if !pidAlive(pid) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if _, err := runx.Run(ctx, runx.Options{}, "taskkill", "/PID", id, "/T", "/F"); err != nil && pidAlive(pid) {
		return err
	}
	return nil
}

func pidAlive(pid int) bool {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// restart relaunches s.Cmd in s.Cwd detached, logging to logDir/<port>.log. The command is
// split into argv (no shell, so metacharacters have no meaning) and started directly.
func restart(s servers.Server, logDir string) error {
	if s.Cmd == "" || s.Cwd == "" {
		return errors.New("no command or working directory recorded")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(LogPath(logDir, s.Port), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	argv := splitArgs(s.Cmd)
	if len(argv) == 0 {
		return errors.New("empty command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = s.Cwd
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200 | 0x08000000} // NEW_PROCESS_GROUP | NO_WINDOW
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// splitArgs splits on spaces, honouring double quotes.
func splitArgs(s string) []string {
	var out []string
	var cur []rune
	inq, has := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inq, has = !inq, true
		case (r == ' ' || r == '\t') && !inq:
			if has {
				out = append(out, string(cur))
				cur, has = nil, false
			}
		default:
			cur, has = append(cur, r), true
		}
	}
	if has {
		out = append(out, string(cur))
	}
	return out
}
