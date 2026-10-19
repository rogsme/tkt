package cli

import (
	"bytes"
	"io"
	"testing"

	"github.com/spf13/cobra"

	"gitlab.com/rogs/tkt/internal/apperr"
)

type prettyResult struct{}

func (prettyResult) Text(w io.Writer)                    { io.WriteString(w, "plain\n") }
func (prettyResult) Pretty(w io.Writer, _ string, _ int) { io.WriteString(w, "pretty\n") }

// show picks the terminal view when stdout is a terminal, with or without
// an error, and stays quiet about checks the view already reported.
func TestShowPretty(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tty      bool
		err      error
		want     string
		errShown bool
	}{
		{"plain", false, nil, "plain\n", false},
		{"terminal", true, nil, "pretty\n", false},
		{"terminal with failing checks", true, apperr.New(apperr.ChecksFailed, "failing checks: git"), "pretty\n", true},
		{"terminal with another error", true, apperr.New(apperr.Internal, "boom"), "pretty\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prettyTerminal = func(io.Writer) bool { return tc.tty }
			t.Cleanup(func() { prettyTerminal = isTerminal })
			g := testRoot()
			var b bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&b)
			if err := show(g, cmd, prettyResult{}, false, tc.err); err != tc.err {
				t.Errorf("show returned %v, want %v", err, tc.err)
			}
			if b.String() != tc.want {
				t.Errorf("printed %q, want %q", b.String(), tc.want)
			}
			if g.errShown != tc.errShown {
				t.Errorf("errShown = %v, want %v", g.errShown, tc.errShown)
			}
		})
	}
}
