package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replacer records what the log held when Replace ran.
type replacer struct {
	Func
	log  string
	seen string
	env  []string
}

func (r *replacer) Replace(_ context.Context, _ Cmd, env []string) error {
	b, _ := os.ReadFile(r.log)
	r.seen, r.env = string(b), env
	return errors.New("exec failed")
}

// A command that replaces tkt is logged before it runs, since nothing runs
// after it.
func TestLoggedReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tkt.log")
	inner := &replacer{log: path}
	l := Logged{Inner: inner, Log: &Log{Path: path}}
	ctx := WithTicket(context.Background(), "ACM-12")
	err := l.Replace(ctx, Cmd{Argv: []string{"/opt/pi/pi", "--no-extensions"}}, []string{"A=1"})
	if err == nil || err.Error() != "exec failed" {
		t.Errorf("Replace = %v, want the inner error", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(inner.seen), " ACM-12 rc=exec 0ms /opt/pi/pi --no-extensions") {
		t.Errorf("the log held %q when Replace ran", inner.seen)
	}
	if len(inner.env) != 1 || inner.env[0] != "A=1" {
		t.Errorf("env %q", inner.env)
	}
}

func TestLoggedReplaceNeedsAReplacer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tkt.log")
	l := Logged{Inner: Func(func(context.Context, Cmd) Result { return Result{} }), Log: &Log{Path: path}}
	if err := l.Replace(context.Background(), Cmd{Argv: []string{"pi"}}, nil); err == nil {
		t.Error("Replace through a Runner without Replace succeeded")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("Replace logged a command it could not run")
	}
}
