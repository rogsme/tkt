package runner

import (
	"context"
	"testing"
)

// StdoutFile captures the whole output, past what one pipe buffer holds.
func TestExecStdoutFile(t *testing.T) {
	for _, file := range []bool{false, true} {
		r := Exec{}.Run(context.Background(), Cmd{Argv: []string{"sh", "-c", "head -c 300000 /dev/zero; echo err >&2"}, StdoutFile: file})
		if !r.OK() || len(r.Stdout) != 300000 || string(r.Stderr) != "err\n" {
			t.Errorf("StdoutFile %v: code %d, %d bytes of stdout, stderr %q", file, r.Code, len(r.Stdout), r.Stderr)
		}
	}
}
