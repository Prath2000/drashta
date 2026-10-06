package actions

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/Prath2000/drashta/internal/runx"
)

// Executor runs a planned Command.
type Executor interface {
	Run(Command) error
}

// ExecExecutor runs commands for real through runx (no shell, 10 s timeout).
type ExecExecutor struct{}

// Run executes c. A non-zero exit is an error that carries stderr.
func (ExecExecutor) Run(c Command) error {
	res, err := runx.Run(context.Background(), runx.Options{}, c.Name, c.Args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: exit %d: %s", c.Name, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// RecorderExecutor records commands instead of running them (tests).
// Err, when set, is returned from every Run.
type RecorderExecutor struct {
	mu   sync.Mutex
	Cmds []Command
	Err  error
}

// Run records c.
func (r *RecorderExecutor) Run(c Command) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Cmds = append(r.Cmds, c)
	return r.Err
}

// Do plans action on t with the default Config. Prefer Config.Do.
func Do(ctx context.Context, ex Executor, t Target, action string) (string, error) {
	return Config{}.Do(ctx, ex, t, action)
}

// Do plans action on t, runs it with ex and logs
// "action=<kind> ref=<ref> ok=<bool>". It returns the command line that ran.
func (c Config) Do(ctx context.Context, ex Executor, t Target, action string) (ran string, err error) {
	defer func() {
		slog.Info("action", "action", t.Type, "ref", t.Ref, "ok", err == nil)
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cmd, err := c.Plan(t, action)
	if err != nil {
		return "", err
	}
	if err := ex.Run(cmd); err != nil {
		return cmd.String(), err
	}
	return cmd.String(), nil
}
