package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// Exec runs commands on the operating system.
type Exec struct {
	// Stdin and StdinTTY serve visible commands: they read the terminal only
	// when tkt itself runs interactively.
	Stdin    io.Reader
	StdinTTY bool
	// Stderr receives a visible command's stderr.
	Stderr io.Writer
}

func (e Exec) Run(ctx context.Context, c Cmd) Result {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	// Like timeout(1): ask the command to stop, then stop waiting for any
	// child that keeps its output pipes open.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if c.Visible {
		cmd.Stderr = e.Stderr
		if e.StdinTTY {
			cmd.Stdin = e.Stdin
		}
	} else {
		cmd.Stderr = &stderr
	}
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Dur: time.Since(start)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		res.Code, res.Err = -1, ctx.Err()
	case errors.As(err, &exitErr) && exitErr.ExitCode() >= 0:
		res.Code = exitErr.ExitCode()
	default:
		res.Code, res.Err = -1, err
	}
	return res
}

// Replace runs c in place of tkt with env as its environment. Only a failure
// returns.
func (e Exec) Replace(_ context.Context, c Cmd, env []string) error {
	path, err := exec.LookPath(c.Argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, c.Argv, env)
}
