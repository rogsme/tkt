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
	line := fmt.Sprintf("%s rc=%d %dms %s", TicketFrom(ctx), r.Code, r.Dur.Milliseconds(), Display(c.Argv))
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Path != "" {
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

// Display renders argv for logs and dry runs: each argument shell-quoted,
// long ones replaced by their size and hash.
func Display(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = LogArg(a)
	}
	return strings.Join(parts, " ")
}
