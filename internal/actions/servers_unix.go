//go:build !windows

package actions

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/Prath2000/drashta/internal/probe/servers"
)

// killPID sends SIGTERM, waits up to 3s for the process to exit, then SIGKILLs it.
func killPID(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to kill pid %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return nil
		}
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// restart relaunches s.Cmd in s.Cwd as a detached session, logging to logDir/<port>.log.
// This is the only place drashta uses a shell: it replays a command line that
// drashta itself recorded from ps.
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
	defer logFile.Close() // the child keeps its own descriptor
	cmd := exec.Command("/bin/sh", "-c", s.Cmd)
	cmd.Dir = s.Cwd
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the child so it never lingers as a zombie
	return nil
}
