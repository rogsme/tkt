// Package cli wires tkt's commands to cobra and fang: flags, help, errors
// and output.
package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime/debug"
	"strconv"
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
	json    bool
	ui      *output.UI
	root    *cobra.Command
	args    []string
	version string
	// errShown is set when the result already told a person about the
	// error, so the error handler stays quiet on a terminal.
	errShown bool
}

// Main runs tkt with args and returns the exit status.
func Main(ctx context.Context, args []string, version string, sio IO) int {
	g := &globals{io: sio, ui: &output.UI{Err: sio.Stderr}, args: args, version: version}
	envJSON, err := formatFromEnv(sio.Getenv)
	if err != nil {
		g.printError(err)
		return apperr.Exit(err)
	}
	root := newRoot(g, envJSON)
	g.root = root
	g.json = jsonFromArgs(args, envJSON)
	root.SetArgs(args)
	root.SetIn(sio.Stdin)
	root.SetOut(sio.Stdout)
	root.SetErr(sio.Stderr)
	err = fang.Execute(ctx, root,
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

// formatFromEnv reads TKT_FORMAT, which lets a harness ask for JSON once.
func formatFromEnv(getenv func(string) string) (bool, error) {
	switch v := getenv("TKT_FORMAT"); v {
	case "", "text":
		return false, nil
	case "json":
		return true, nil
	default:
		return false, apperr.New(apperr.Usage, "TKT_FORMAT must be json or text, not %q", v)
	}
}

// jsonFromArgs finds --json before cobra parses the arguments, so an error
// in parsing them is reported in the right format too.
func jsonFromArgs(args []string, def bool) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--json" {
			def = true
		} else if v, ok := strings.CutPrefix(a, "--json="); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				def = b
			}
		}
	}
	return def
}

func newRoot(g *globals, envJSON bool) *cobra.Command {
	root := &cobra.Command{
		Use:   "tkt",
		Short: "Run the mechanical steps of the ticket lifecycle from a herdr pane",
		Long: `tkt runs the mechanical steps around the rogs-skills ticket lifecycle: it creates
the ticket's worktree, opens a herdr tab named after the ticket, starts Claude Code
or OpenCode in it, and hands the agent its prompt. Each command is one step you
take. tkt watch, which tkt starts by itself, takes the upkeep steps for every
ticket: it syncs a moved base, fixes failing checks and removes finished
tickets. tkt auto chains all the steps for one ticket when you ask it to, and
stops where a person decides. Neither merges a pull request on its own: once
your human verification passed, tkt merge hands the ticket to finish-ticket,
now or, through tkt watch, once its checks pass.

Start with tkt help lifecycle. Programs that drive tkt want --json (tkt help
json), tkt schema and tkt wait. tkt help exit-codes lists every error code, and
tkt help config describes the settings file.

tkt agent opens a chat that runs these steps with you. In a herdr pane, inside
a repository with the lifecycle configuration, tkt alone opens it too.`,
		SilenceUsage:               true,
		SuggestionsMinimumDistance: 2,
		RunE:                       g.runRoot,
	}
	root.PersistentFlags().StringVarP(&g.dir, "dir", "C", "", "run as if tkt started in DIR")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "print every external command to stderr")
	root.PersistentFlags().BoolVar(&g.json, "json", envJSON, "print one JSON document on stdout (also TKT_FORMAT=json); see tkt help json")
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
	for _, c := range []*cobra.Command{agentCmd(g), suggestCmd(g), startCmd(g), approveCmd(g), sendCmd(g), autoCmd(g), watchCmd(g), holdCmd(g), runCmd(g), syncCmd(g), mergeCmd(g), resumeCmd(g), doneCmd(g)} {
		c.GroupID = "lifecycle"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{lsCmd(g), showCmd(g), waitCmd(g), statsCmd(g), historyCmd(g)} {
		c.GroupID = "inspect"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{doctorCmd(g), hookCmd(g), schemaCmd(g), updateCmd(g)} {
		c.GroupID = "setup"
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
	log := &runner.Log{Path: app.LogPath(g.io.Getenv)}
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
		Version:  versionString(g.version),
		// tkt agent hands the terminal to pi, so it checks both ends.
		StdoutTTY: isTerminal(g.io.Stdout),
	}
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

// handleError reports an error: as the JSON envelope in JSON mode, styled
// on a terminal, otherwise as "tkt: <message>" and its hint.
func (g *globals) handleError(w io.Writer, styles fang.Styles, err error) {
	e := apperr.As(classify(err))
	if e.Code == apperr.Usage && e.Hint == "" {
		if name := g.commandName(); name == "tkt" {
			e.Hint = "tkt --help"
		} else {
			e.Hint = "tkt " + name + " --help"
		}
	}
	if g.json {
		output.Emit(g.io.Stdout, output.Envelope{
			Schema:  output.Schema,
			Command: g.commandName(),
			Error: &output.ErrBody{Code: string(e.Code), Exit: apperr.Lookup(e.Code).Exit,
				Message: e.Msg, Hint: e.Hint, Details: e.Details},
			Warnings: g.ui.Warnings(),
		}, isTerminal(g.io.Stdout))
		return
	}
	if isTerminal(g.io.Stderr) {
		if g.errShown {
			return
		}
		fang.DefaultErrorHandler(w, styles, e)
		if e.Hint != "" {
			io.WriteString(w, "hint: "+e.Hint+"\n\n")
		}
		return
	}
	g.printError(e)
}

// printError prints "tkt: <message>" and its hint on stderr.
func (g *globals) printError(err error) {
	e := apperr.As(err)
	io.WriteString(g.io.Stderr, "tkt: "+e.Msg+"\n")
	if e.Hint != "" {
		io.WriteString(g.io.Stderr, "tkt: hint: "+e.Hint+"\n")
	}
}

// commandName names the command the arguments select, such as "start", or
// "tkt" when they select none.
func (g *globals) commandName() string {
	if g.root == nil {
		return "tkt"
	}
	cmd, _, err := g.root.Find(g.args)
	if err != nil || cmd == g.root {
		return "tkt"
	}
	return strings.TrimPrefix(cmd.CommandPath(), "tkt ")
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// usage returns a usage error.
func usage(format string, args ...any) error {
	return apperr.New(apperr.Usage, format, args...)
}
