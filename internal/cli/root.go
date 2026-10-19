// Package cli wires tkt's commands to cobra and fang: flags, help, errors
// and output.
package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.com/rogs/tkt/internal/app"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/runner"
)

// IO is the process's streams and environment.
type IO struct {
	Stdin  *os.File
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

// globals holds the root flags and what every command shares.
type globals struct {
	io      IO
	dir     string
	verbose bool
	ui      *output.UI
}

// Main runs tkt with args and returns the exit status.
func Main(ctx context.Context, args []string, version string, sio IO) int {
	g := &globals{io: sio, ui: &output.UI{Err: sio.Stderr}}
	root := newRoot(g)
	root.SetArgs(args)
	root.SetIn(sio.Stdin)
	root.SetOut(sio.Stdout)
	root.SetErr(sio.Stderr)
	err := fang.Execute(ctx, root,
		fang.WithVersion(versionString(version)),
		fang.WithErrorHandler(g.handleError),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	)
	return apperr.Exit(classify(err))
}

func versionString(v string) string {
	if v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func newRoot(g *globals) *cobra.Command {
	root := &cobra.Command{
		Use:   "tkt",
		Short: "Run the mechanical steps of the ticket lifecycle from a herdr pane",
		Long: `tkt runs the mechanical steps around the rogs-skills ticket lifecycle: it creates
the ticket's worktree, opens a herdr tab named after the ticket, starts Claude Code
or OpenCode in it, and hands the agent its prompt. Each command is one step you
take; nothing chains stages.`,
		SilenceUsage:               true,
		SuggestionsMinimumDistance: 2,
	}
	root.PersistentFlags().StringVarP(&g.dir, "dir", "C", "", "run as if tkt started in DIR")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "print every external command to stderr")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return apperr.New(apperr.Usage, "%s", err.Error()).Wrap(err)
	})
	cobra.EnableCommandSorting = false
	root.AddGroup(
		&cobra.Group{ID: "lifecycle", Title: "Lifecycle"},
		&cobra.Group{ID: "inspect", Title: "Inspect"},
		&cobra.Group{ID: "setup", Title: "Setup"},
		&cobra.Group{ID: "topics", Title: "Help topics"},
	)
	root.SetHelpCommandGroupID("setup")
	root.SetCompletionCommandGroupID("setup")
	for _, c := range []*cobra.Command{startCmd(g), runCmd(g), resumeCmd(g), doneCmd(g)} {
		c.GroupID = "lifecycle"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{lsCmd(g)} {
		c.GroupID = "inspect"
		root.AddCommand(c)
	}
	for _, c := range topics() {
		c.GroupID = "topics"
		root.AddCommand(c)
	}
	return root
}

// app builds the shared state for one command.
func (g *globals) app() *app.App {
	cwd, _ := os.Getwd()
	dir := g.dir
	if dir == "" {
		dir = cwd
	}
	tty := g.io.Stdin != nil && term.IsTerminal(int(g.io.Stdin.Fd()))
	log := &runner.Log{Path: logPath(g.io.Getenv)}
	if g.verbose {
		log.Verbose = g.io.Stderr
	}
	var stdin io.Reader = g.io.Stdin
	if g.io.Stdin == nil {
		stdin = strings.NewReader("")
	}
	return &app.App{
		Stdin:    stdin,
		StdinTTY: tty,
		UI:       g.ui,
		Getenv:   g.io.Getenv,
		Cwd:      cwd,
		Dir:      dir,
		R:        runner.Logged{Inner: runner.Exec{Stdin: stdin, StdinTTY: tty, Stderr: g.io.Stderr}, Log: log},
		Now:      time.Now,
		Sleep:    time.Sleep,
	}
}

func logPath(getenv func(string) string) string {
	if d := getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "tkt", "tkt.log")
	}
	return filepath.Join(getenv("HOME"), ".local", "state", "tkt", "tkt.log")
}

// classify gives every error a code: cobra's own errors are usage errors and
// a cancelled context is an interruption.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var e *apperr.Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.Canceled) {
		return apperr.New(apperr.Interrupted, "interrupted").Wrap(err)
	}
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "flag needs an argument", "invalid argument", "accepts ", "requires ", "if any flags in the group"} {
		if strings.HasPrefix(msg, p) {
			return apperr.New(apperr.Usage, "%s", msg).Wrap(err)
		}
	}
	return apperr.As(err)
}

// handleError prints an error as "tkt: <message>" and its hint.
func (g *globals) handleError(w io.Writer, styles fang.Styles, err error) {
	e := apperr.As(classify(err))
	if f, ok := g.io.Stderr.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fang.DefaultErrorHandler(w, styles, e)
		if e.Hint != "" {
			io.WriteString(w, "hint: "+e.Hint+"\n\n")
		}
		return
	}
	io.WriteString(w, "tkt: "+e.Msg+"\n")
	if e.Hint != "" {
		io.WriteString(w, "tkt: hint: "+e.Hint+"\n")
	}
}

// usage returns a usage error.
func usage(format string, args ...any) error {
	return apperr.New(apperr.Usage, format, args...)
}
