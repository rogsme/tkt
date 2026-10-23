// Package runner runs external commands. Every command tkt runs goes through
// a Runner, so tests can substitute one and every call is logged.
package runner

import (
	"context"
	"time"
)

// Cmd is one external command.
type Cmd struct {
	Argv []string
	// Timeout bounds the run; zero means none.
	Timeout time.Duration
	// Visible passes the command's stderr through to the user and gives it
	// the terminal as stdin when tkt runs interactively, so the user sees
	// progress and prompts. Its stdout is still captured.
	Visible bool
	// Quiet leaves the command out of the log: for polls that repeat many
	// times a second.
	Quiet bool
	// StdoutFile captures stdout through a temporary file instead of a
	// pipe, for a command that exits before a pipe drains: opencode api
	// cuts its output at 256 KiB on a pipe.
	StdoutFile bool
}

// Result is what a command did. Code is -1 when it could not start or ran
// out of time.
type Result struct {
	Stdout []byte
	Stderr []byte
	Code   int
	Dur    time.Duration
	Err    error
}

// OK reports whether the command exited 0.
func (r Result) OK() bool { return r.Code == 0 }

// Out returns stdout as a string.
func (r Result) Out() string { return string(r.Stdout) }

// ErrText returns stderr, or the start error when there is no stderr.
func (r Result) ErrText() string {
	if len(r.Stderr) == 0 && r.Err != nil {
		return r.Err.Error()
	}
	return string(r.Stderr)
}

// Runner runs commands.
type Runner interface {
	Run(ctx context.Context, c Cmd) Result
}

// Replacer replaces tkt's process with a command, for the one command that
// hands the terminal over for good. Replace returns only when it fails. It
// is not part of Runner, so a test's Runner need not provide it.
type Replacer interface {
	Replace(ctx context.Context, c Cmd, env []string) error
}

// Spawner starts a command that outlives tkt: in a session of its own, with
// no terminal, its output appended to a log file. Like Replacer, it is not
// part of Runner, so a test's Runner need not provide it.
type Spawner interface {
	Spawn(ctx context.Context, c Cmd, env []string, dir, log string) error
}

// Func adapts a function to a Runner, for tests.
type Func func(ctx context.Context, c Cmd) Result

func (f Func) Run(ctx context.Context, c Cmd) Result { return f(ctx, c) }

type ticketKey struct{}

// WithTicket records the ticket a command works on, for the log.
func WithTicket(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ticketKey{}, id)
}

// TicketFrom returns the ticket recorded in ctx, or "-".
func TicketFrom(ctx context.Context) string {
	if id, ok := ctx.Value(ticketKey{}).(string); ok && id != "" {
		return id
	}
	return "-"
}

// Head truncates s to n bytes, for error messages.
func Head(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
