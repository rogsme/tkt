package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Log appends one line per command to a file:
//
//	2026-01-02T03:04:05Z ACM-12 rc=0 12ms herdr agent list
//
// and, when Verbose is set, echoes the line without its timestamp.
type Log struct {
	Path    string
	Verbose io.Writer

	mu sync.Mutex
}

// Record writes the line for one finished command.
func (l *Log) Record(ctx context.Context, c Cmd, r Result) {
	l.write(fmt.Sprintf("%s rc=%d %dms %s", TicketFrom(ctx), r.Code, r.Dur.Milliseconds(), Display(c.Argv)))
}

// RecordExec writes the line for a command that replaces tkt, before it
// runs: no line could follow it. rc=exec stands for the status.
func (l *Log) RecordExec(ctx context.Context, c Cmd) {
	l.write(fmt.Sprintf("%s rc=exec 0ms %s", TicketFrom(ctx), Display(c.Argv)))
}

// RecordSpawn writes the line for a command tkt starts in the background,
// before it starts. rc=spawn stands for the status, which tkt never sees.
func (l *Log) RecordSpawn(ctx context.Context, c Cmd) {
	l.write(fmt.Sprintf("%s rc=spawn 0ms %s", TicketFrom(ctx), Display(c.Argv)))
}

// maxLog is the size past which the log starts over; the older lines stay
// in one file beside it, ending in .1. A tkt watch logs every minute, for
// days.
const maxLog = 10 << 20

func (l *Log) write(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Path != "" {
		if fi, err := os.Stat(l.Path); err == nil && fi.Size() > maxLog {
			os.Rename(l.Path, l.Path+".1")
		}
		if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err == nil {
			if f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), line)
				f.Close()
			}
		}
	}
	if l.Verbose != nil {
		fmt.Fprintf(l.Verbose, "tkt: + %s\n", line)
	}
}

// Logged runs commands through Inner and records each one in Log.
type Logged struct {
	Inner Runner
	Log   *Log
}

func (l Logged) Run(ctx context.Context, c Cmd) Result {
	r := l.Inner.Run(ctx, c)
	if !c.Quiet && l.Log != nil {
		l.Log.Record(ctx, c, r)
	}
	return r
}

// Replace logs c, then replaces tkt with it through Inner.
func (l Logged) Replace(ctx context.Context, c Cmd, env []string) error {
	r, ok := l.Inner.(Replacer)
	if !ok {
		return fmt.Errorf("the runner cannot replace tkt with %s", c.Argv[0])
	}
	if l.Log != nil {
		l.Log.RecordExec(ctx, c)
	}
	return r.Replace(ctx, c, env)
}

// Spawn logs c, then starts it in the background through Inner.
func (l Logged) Spawn(ctx context.Context, c Cmd, env []string, dir, log string) error {
	s, ok := l.Inner.(Spawner)
	if !ok {
		return fmt.Errorf("the runner cannot start %s in the background", c.Argv[0])
	}
	if l.Log != nil {
		l.Log.RecordSpawn(ctx, c)
	}
	return s.Spawn(ctx, c, env, dir, log)
}

// Display renders argv for logs and dry runs: each argument shell-quoted,
// long ones replaced by their size and hash.
func Display(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = LogArg(a)
	}
	return strings.Join(parts, " ")
}
