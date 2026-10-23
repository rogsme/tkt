package cli

import (
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.com/rogs/tkt/internal/app"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/prompt"
)

type texter interface{ Text(w io.Writer) }

// prettier is a result with a richer view for a person at a terminal.
type prettier interface {
	Pretty(w io.Writer, home string, width int)
}

// show prints a command's result: the JSON envelope in JSON mode, otherwise
// its text. A command can return a result and an error: tkt ls prints its
// table even when the forge failed. In JSON mode the error envelope carries
// that partial result in its details instead.
func show[T texter](g *globals, cmd *cobra.Command, res T, isNil bool, err error) error {
	if g.json {
		if err != nil {
			return err
		}
		return output.Emit(cmd.OutOrStdout(), output.Envelope{
			Schema: output.Schema, OK: true, Command: g.commandName(), Result: res, Warnings: g.ui.Warnings(),
		}, isTerminal(cmd.OutOrStdout()))
	}
	if isNil {
		return err
	}
	out := cmd.OutOrStdout()
	if p, ok := any(res).(prettier); ok && prettyTerminal(out) {
		p.Pretty(colorprofile.NewWriter(out, os.Environ()), g.io.Getenv("HOME"), terminalWidth(out))
		// The view ends with a summary of the failing checks, so the error
		// box would repeat it.
		var e *apperr.Error
		g.errShown = errors.As(err, &e) && e.Code == apperr.ChecksFailed
		return err
	}
	res.Text(out)
	return err
}

// prettyTerminal decides whether a result gets its terminal view. Tests
// replace it, since they have no terminal.
var prettyTerminal = isTerminal

// terminalWidth returns w's width in columns, or 0 when it is unknown.
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	width, _, _ := term.GetSize(int(f.Fd()))
	return width
}

type promptFlags struct{ text, file string }

func (p *promptFlags) add(cmd *cobra.Command, what string) {
	cmd.Flags().StringVar(&p.text, "prompt", "", "send TEXT instead of "+what+"; - reads it from stdin")
	cmd.Flags().StringVar(&p.file, "prompt-file", "", "send the text in the file PATH instead of "+what)
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	cmd.MarkFlagFilename("prompt-file")
}

func (p *promptFlags) set(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("prompt") || cmd.Flags().Changed("prompt-file")
}

func (p *promptFlags) source(cmd *cobra.Command) prompt.Source {
	return prompt.Source{Text: p.text, File: p.file, Set: p.set(cmd)}
}

func startCmd(g *globals) *cobra.Command {
	var o app.StartOpts
	var pf promptFlags
	cmd := &cobra.Command{
		Use:   "start [ID | --scratch NAME]",
		Short: "Create the worktree and tab, start an agent and send it the prompt",
		Long:  helpText("start"),
		Example: `  xclip -selection clipboard -o | tkt start --prompt -
  tkt start ACM-12
  tkt start ACM-12 --prompt-file plan-request.md --as fast
  tkt start ACM-12 --dry-run
  tkt start --scratch spike-login --as fast`,
		ValidArgsFunction: g.completeTickets(false),
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) > 1:
				return usage("start takes one ticket id")
			case len(args) == 1 && cmd.Flags().Changed("scratch"):
				return usage("give a ticket id or --scratch NAME, not both")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.ID = args[0]
			}
			o.Prompt = pf.source(cmd)
			res, err := g.app().Start(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	pf.add(cmd, "the start stage line")
	cmd.Flags().StringVar(&o.Scratch, "scratch", "", "start an agent in worktree NAME with no ticket, and send it nothing")
	cmd.MarkFlagsMutuallyExclusive("scratch", "prompt")
	cmd.MarkFlagsMutuallyExclusive("scratch", "prompt-file")
	cmd.Flags().StringVar(&o.As, "as", "", "run on PRESET instead of the one the prompt's Model: line picks")
	cmd.RegisterFlagCompletionFunc("as", g.completePresets)
	cmd.Flags().BoolVar(&o.Focus, "focus", false, "switch to the ticket's tab afterwards")
	cmd.Flags().BoolVar(&o.Force, "force", false, "override the safety refusals")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func runCmd(g *globals) *cobra.Command {
	var o app.RunOpts
	var pf promptFlags
	cmd := &cobra.Command{
		Use:   "run ID [STAGE]",
		Short: "Replace the ticket's idle agent with a fresh session and send a stage line",
		Long:  helpText("run"),
		Example: `  tkt run ACM-12 review
  tkt run ACM-12 review --as big
  tkt run ACM-12 address
  tkt run ACM-12 --prompt "Check the flaky login test again."`,
		ValidArgsFunction: g.completeRun,
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return usage("usage: tkt run ID STAGE")
			case len(args) > 2:
				return usage("run takes a ticket id and a stage")
			case len(args) == 1 && !pf.set(cmd):
				return usage("usage: tkt run ID STAGE (or --prompt TEXT, or --prompt-file PATH)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			if len(args) == 2 {
				o.Stage = args[1]
			}
			o.Prompt = pf.source(cmd)
			res, err := g.app().Run(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	pf.add(cmd, "the stage line")
	cmd.Flags().StringVar(&o.As, "as", "", "run on PRESET instead of the ticket's")
	cmd.Flags().StringVar(&o.Pane, "pane", "", "use PANE when several agents run in the worktree")
	cmd.RegisterFlagCompletionFunc("as", g.completePresets)
	cmd.RegisterFlagCompletionFunc("pane", g.completePanes)
	cmd.Flags().BoolVar(&o.Force, "force", false, "replace an agent that is planning or in an unknown state")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func syncCmd(g *globals) *cobra.Command {
	var o app.SyncOpts
	cmd := &cobra.Command{
		Use:   "sync ID",
		Short: "Merge the base into the ticket's branch: itself when clean, through a fresh session on a conflict",
		Long:  helpText("sync"),
		Example: `  tkt sync ACM-12
  tkt sync ACM-12 --dry-run
  tkt sync ACM-12 --no-agent --json`,
		SuggestFor:        []string{"update", "rebase"},
		ValidArgsFunction: g.completeTickets(false),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("usage: tkt sync ID")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			res, err := g.app().Sync(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().StringVar(&o.As, "as", "", "run the session that resolves a conflict on PRESET instead of the ticket's")
	cmd.RegisterFlagCompletionFunc("as", g.completePresets)
	cmd.Flags().BoolVar(&o.NoAgent, "no-agent", false, "stop at a conflict instead of starting a session to resolve it")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func mergeCmd(g *globals) *cobra.Command {
	var o app.MergeOpts
	cmd := &cobra.Command{
		Use:   "merge ID",
		Short: "Have finish-ticket merge a verified ticket, now or once its checks pass",
		Long:  helpText("merge"),
		Example: `  tkt merge ACM-12 --passed 1-3
  tkt merge ACM-12 --passed all --head a1b2c3d
  tkt merge ACM-12 --cancel`,
		SuggestFor:        []string{"ship", "finish"},
		ValidArgsFunction: g.completeTickets(false),
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) != 1:
				return usage("usage: tkt merge ID --passed STEPS")
			case !cmd.Flags().Changed("passed") && !o.Cancel:
				return usage("tkt merge needs --passed: the human verification steps that passed, such as 1-3, or all")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			res, err := g.app().Merge(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().StringVar(&o.Passed, "passed", "", "the human verification steps that passed: numbers and ranges such as 1-3 or 1,2,4, or all")
	cmd.Flags().StringVar(&o.Head, "head", "", "the head the steps ran on; tkt merge refuses once the pull request moved past it")
	cmd.Flags().StringVar(&o.As, "as", "", "run finish-ticket on PRESET instead of the ticket's")
	cmd.RegisterFlagCompletionFunc("as", g.completePresets)
	cmd.Flags().BoolVar(&o.Cancel, "cancel", false, "drop the ticket's merge order")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	cmd.MarkFlagsMutuallyExclusive("passed", "cancel")
	return cmd
}

func suggestCmd(g *globals) *cobra.Command {
	var o app.SuggestOpts
	cmd := &cobra.Command{
		Use:   "suggest [COUNT]",
		Short: "Have suggest-tickets pick the next tickets to start, ones that can run side by side",
		Long:  helpText("suggest"),
		Example: `  tkt suggest
  tkt suggest 2
  tkt suggest --as fast --json`,
		SuggestFor: []string{"next", "pick", "dispatch"},
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) > 1:
				return usage("usage: tkt suggest [COUNT]")
			case len(args) == 1:
				n, err := strconv.Atoi(args[0])
				if err != nil {
					return usage("COUNT is a number of tickets, such as 3, not %q", args[0])
				}
				o.Count = n
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := g.app().Suggest(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().StringVar(&o.As, "as", "", "run suggest-tickets on PRESET instead of the [suggest] one")
	cmd.RegisterFlagCompletionFunc("as", g.completePresets)
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func hookCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Hooks tkt puts into the agents' harnesses",
		Long:  helpText("hook"),
		Args:  cobra.NoArgs,
	}
	var o app.HookOpts
	tmp := &cobra.Command{
		Use:   "tmp",
		Short: "Give OpenCode agents their ticket's TMPDIR, and move the /tmp paths they type into it",
		Long:  helpText("hook"),
		Example: `  tkt hook tmp --install
  echo '{"directories": ["'$PWD'"], "command": "cat > /tmp/pr-body.md"}' | tkt hook tmp`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := g.app().Hook(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	tmp.Flags().BoolVar(&o.Install, "install", false, "write tkt's plugin into OpenCode's global plugins directory")
	tmp.Flags().BoolVar(&o.Dry, "dry-run", false, "with --install, print the change instead of making it")
	cmd.AddCommand(tmp)
	return cmd
}

func approveCmd(g *globals) *cobra.Command {
	var o app.ApproveOpts
	cmd := &cobra.Command{
		Use:   "approve ID",
		Short: "Approve the plan in the ticket's own session, so work-ticket begins",
		Long:  helpText("approve"),
		Example: `  tkt show ACM-12
  tkt approve ACM-12
  tkt approve ACM-12 --dry-run`,
		ValidArgsFunction: g.completeTickets(false),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("usage: tkt approve ID")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			res, err := g.app().Approve(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().StringVar(&o.Pane, "pane", "", "approve the plan of the agent in PANE when several run in the worktree")
	cmd.RegisterFlagCompletionFunc("pane", g.completePanes)
	cmd.Flags().BoolVar(&o.Force, "force", false, "send the approval even when the last status is not AWAITING_APPROVAL")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func sendCmd(g *globals) *cobra.Command {
	var o app.SendOpts
	var pf promptFlags
	cmd := &cobra.Command{
		Use:   "send ID [TEXT]",
		Short: "Type text into the ticket's running session, which keeps its conversation",
		Long:  helpText("send"),
		Example: `  tkt send ACM-12 "Keep the old endpoint and drop step 4 of the plan."
  tkt send ACM-12 --steer "Stop: the endpoint stays. Drop step 4."
  tkt send ACM-12 --prompt-file answer.md`,
		ValidArgsFunction: g.completeTickets(false),
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return usage("usage: tkt send ID TEXT")
			case len(args) > 2:
				return usage("send takes a ticket id and one TEXT; quote the text")
			case len(args) == 2 && pf.set(cmd):
				return usage("give TEXT, --prompt or --prompt-file, not several")
			case len(args) == 1 && !pf.set(cmd):
				return usage("usage: tkt send ID TEXT (or --prompt TEXT, or --prompt-file PATH)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			o.Prompt = pf.source(cmd)
			if len(args) == 2 {
				o.Prompt = prompt.Source{Text: args[1], Set: true}
			}
			res, err := g.app().Send(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	pf.add(cmd, "TEXT")
	cmd.Flags().BoolVar(&o.Wait, "wait", false, "when the agent is busy, wait for it to stop, then send")
	cmd.Flags().BoolVar(&o.Steer, "steer", false, "type the text in while the agent works; with --wait, wait only for a question in its dialog")
	cmd.Flags().StringVar(&o.Pane, "pane", "", "send to the agent in PANE when several run in the worktree")
	cmd.RegisterFlagCompletionFunc("pane", g.completePanes)
	cmd.Flags().BoolVar(&o.Force, "force", false, "override the safety refusals")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func autoCmd(g *globals) *cobra.Command {
	var o app.AutoOpts
	cmd := &cobra.Command{
		Use:   "auto ID",
		Short: "Drive the ticket until it needs you: approve, review, fix, until READY_FOR_HUMAN",
		Long:  helpText("auto"),
		Example: `  tkt auto ACM-12 --approve-plan --notify
  tkt approve ACM-12 && tkt auto ACM-12
  tkt auto ACM-12 --once
  tkt auto ACM-12 --once --dry-run
  tkt auto ACM-12 --stop`,
		ValidArgsFunction: g.completeTickets(false),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("usage: tkt auto ID")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			res, err := g.app().Auto(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().BoolVar(&o.ApprovePlan, "approve-plan", false, "approve the plan in your place when start-ticket ends with AWAITING_APPROVAL")
	cmd.Flags().StringVar(&o.As, "as", "", "start the ticket's agents on PRESET")
	cmd.RegisterFlagCompletionFunc("as", g.completePresets)
	cmd.Flags().IntVar(&o.MaxRounds, "max-rounds", -1, "pause for you after N address and ci runs since the plan's approval (default [auto] max_rounds)")
	cmd.Flags().DurationVar(&o.Poll, "poll", 0, "read the forge every DURATION while waiting for CI or a review (default [auto] poll)")
	cmd.Flags().BoolVar(&o.Notify, "notify", false, "show a herdr notification when the loop needs you or stops")
	cmd.Flags().BoolVar(&o.Once, "once", false, "take at most one step, then exit 0 with what it did or why not")
	cmd.Flags().BoolVar(&o.Stop, "stop", false, "stop the loop that drives the ticket, at its next look")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "with --once, print the step instead of taking it")
	cmd.MarkFlagsMutuallyExclusive("stop", "once")
	cmd.MarkFlagsMutuallyExclusive("stop", "approve-plan")
	return cmd
}

func watchCmd(g *globals) *cobra.Command {
	var o app.WatchOpts
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Keep the repository's tickets moving: sync moved bases, fix failing checks, remove finished tickets",
		Long:  helpText("watch"),
		Example: `  tkt watch
  tkt watch --once --dry-run
  tkt watch --stop
  tkt watch --install
  tkt watch --uninstall`,
		SuggestFor: []string{"daemon", "serve"},
		Args:       cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := g.app().Watch(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().BoolVar(&o.Once, "once", false, "take one pass over the tickets, then exit")
	cmd.Flags().BoolVar(&o.Stop, "stop", false, "stop the tkt watch that runs for this repository")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "with --once, say what each ticket would get instead of doing it; with --install or --uninstall, what would change")
	cmd.Flags().BoolVar(&o.Install, "install", false, "run tkt watch for this repository, and herdr's server, as services that start at boot")
	cmd.Flags().BoolVar(&o.Uninstall, "uninstall", false, "stop running tkt watch for this repository as a service")
	cmd.Flags().BoolVar(&o.Yes, "yes", false, "with --install, start the services at boot without asking (loginctl enable-linger)")
	cmd.MarkFlagsMutuallyExclusive("once", "stop", "install", "uninstall")
	return cmd
}

func choresCmd(g *globals) *cobra.Command {
	var o app.ChoresOpts
	return &cobra.Command{
		Use:   "chores",
		Short: "List the repository's chores, with their next run",
		Long:  helpText("chores"),
		Example: `  tkt chores
  tkt chores --json`,
		SuggestFor: []string{"schedule", "cron", "jobs"},
		Args:       cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := g.app().Chores(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
}

func choreCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chore",
		Short: "Run one of the repository's chores now",
		Long:  helpText("chores"),
		Args:  cobra.NoArgs,
	}
	var o app.ChoreRunOpts
	run := &cobra.Command{
		Use:   "run NAME",
		Short: "Start a run of a chore now: a branch, a tab and an agent with the chore's prompt",
		Long:  helpText("chores"),
		Example: `  tkt chore run deps
  tkt chore run deps --dry-run`,
		ValidArgsFunction: g.completeChores,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("usage: tkt chore run NAME")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Name = args[0]
			res, err := g.app().ChoreRun(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	run.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	cmd.AddCommand(run)
	return cmd
}

func holdCmd(g *globals) *cobra.Command {
	var o app.HoldOpts
	cmd := &cobra.Command{
		Use:   "hold ID",
		Short: "Put a ticket on hold, so tkt watch and tkt auto leave it alone",
		Long:  helpText("hold"),
		Example: `  tkt hold ACM-9
  tkt hold ACM-9 --off`,
		SuggestFor:        []string{"park", "pause"},
		ValidArgsFunction: g.completeTickets(false),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("usage: tkt hold ID")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			res, err := g.app().Hold(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().BoolVar(&o.Off, "off", false, "take the ticket off hold")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func resumeCmd(g *globals) *cobra.Command {
	var o app.ResumeOpts
	cmd := &cobra.Command{
		Use:   "resume ID",
		Short: "Relaunch the ticket's newest session when no agent runs for it",
		Long:  helpText("resume"),
		Example: `  tkt resume ACM-12
  tkt resume ACM-12 --focus
  tkt resume ACM-12 --session 22222222-2222-2222-2222-222222222222`,
		SuggestFor:        []string{"continue", "attach"},
		ValidArgsFunction: g.completeTickets(false),
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return usage("usage: tkt resume ID")
			case len(args) > 1:
				return usage("resume takes one ticket id")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			res, err := g.app().Resume(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().StringVar(&o.Session, "session", "", "resume ID instead of the newest session")
	cmd.Flags().StringVar(&o.As, "as", "", "resume on PRESET, and with it its harness")
	cmd.RegisterFlagCompletionFunc("as", g.completePresets)
	cmd.RegisterFlagCompletionFunc("session", g.completeSessions)
	cmd.Flags().BoolVar(&o.Focus, "focus", false, "switch to the ticket's tab afterwards")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func doneCmd(g *globals) *cobra.Command {
	var o app.DoneOpts
	cmd := &cobra.Command{
		Use:   "done [ID...]",
		Short: "Close a merged ticket's tabs and remove its worktree",
		Long:  helpText("done"),
		Example: `  tkt done ACM-12
  tkt done ACM-12 --dry-run
  tkt done --all --yes`,
		SuggestFor:        []string{"finish", "remove", "rm", "clean"},
		ValidArgsFunction: g.completeTickets(true),
		Args: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			switch {
			case !all && len(args) == 0:
				return usage("usage: tkt done ID... or tkt done --all")
			case all && len(args) > 0:
				return usage("give ticket ids or --all, not both")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.IDs = args
			res, err := g.app().Done(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().BoolVar(&o.All, "all", false, "check every ticket worktree")
	cmd.Flags().BoolVar(&o.Yes, "yes", false, "skip the confirmation that --all asks for")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func lsCmd(g *globals) *cobra.Command {
	var o app.LsOpts
	cmd := &cobra.Command{
		Use:   "ls [ID...]",
		Short: "List ticket worktrees with their state and the next step",
		Long:  helpText("ls"),
		Example: `  tkt ls
  tkt ls ACM-12 --json
  tkt ls --json | jq -r '.result.rows[] | select(.next.kind == "command") | .next.text'`,
		SuggestFor:        []string{"list", "status"},
		ValidArgsFunction: g.completeTickets(true),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.IDs = args
			res, err := g.app().Ls(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().BoolVar(&o.NoFetch, "no-fetch", false, "skip the git fetch that refreshes the commits-behind count")
	return cmd
}

func waitCmd(g *globals) *cobra.Command {
	var o app.WaitOpts
	cmd := &cobra.Command{
		Use:   "wait ID",
		Short: "Block until the ticket's agent needs you or has finished",
		Long:  helpText("wait"),
		Example: `  tkt wait ACM-12
  tkt wait ACM-12 --timeout 30m --json
  tkt wait ACM-12 --until blocked`,
		ValidArgsFunction: g.completeTickets(false),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("usage: tkt wait ID")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			res, err := g.app().Wait(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().StringSliceVar(&o.Until, "until", nil, "stop at STATE: idle, working, blocked or done; repeat it or separate with commas (default idle, blocked, done)")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", 0, "give up after DURATION, such as 30m (default: wait without limit)")
	cmd.Flags().StringVar(&o.Pane, "pane", "", "wait for the agent in PANE when several run in the worktree")
	cmd.Flags().BoolVar(&o.Notify, "notify", false, "show a herdr notification when the wait ends")
	cmd.RegisterFlagCompletionFunc("pane", g.completePanes)
	cmd.RegisterFlagCompletionFunc("until", fixed(app.WaitStates...))
	return cmd
}

func showCmd(g *globals) *cobra.Command {
	var o app.ShowOpts
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "Print the agent's last message, its live screen, or the verification steps",
		Long:  helpText("show"),
		Example: `  tkt show ACM-12
  tkt show ACM-12 --screen
  tkt show ACM-12 --steps --json`,
		SuggestFor:        []string{"plan", "peek", "cat"},
		ValidArgsFunction: g.completeTickets(false),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("usage: tkt show ID")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			res, err := g.app().Show(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().BoolVar(&o.Screen, "screen", false, "print the agent's live screen instead, such as a question it asks")
	cmd.Flags().BoolVar(&o.Steps, "steps", false, "print the human verification steps of the review at the pull request's head")
	cmd.MarkFlagsMutuallyExclusive("screen", "steps")
	cmd.Flags().StringVar(&o.Pane, "pane", "", "show the agent in PANE when several run in the worktree")
	cmd.RegisterFlagCompletionFunc("pane", g.completePanes)
	return cmd
}

func statsCmd(g *globals) *cobra.Command {
	var o app.StatsOpts
	cmd := &cobra.Command{
		Use:   "stats [ID...]",
		Short: "Show how long tickets took from start to merge, and how long they waited for you",
		Long:  helpText("stats"),
		Example: `  tkt stats
  tkt stats --since 720h
  tkt stats ACM-12 --json`,
		SuggestFor:        []string{"metrics"},
		ValidArgsFunction: g.completeTickets(true),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.IDs = args
			res, err := g.app().Stats(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().DurationVar(&o.Since, "since", 0, "only tickets started within DURATION, such as 720h for 30 days")
	cmd.Flags().BoolVar(&o.AllRepos, "all-repos", false, "include the tickets of every repository in the history")
	return cmd
}

func historyCmd(g *globals) *cobra.Command {
	var o app.HistoryOpts
	return &cobra.Command{
		Use:   "history ID...",
		Short: "Show what happened to tickets: their events, commits and reviews",
		Long:  helpText("history"),
		Example: `  tkt history ACM-12
  tkt history ACM-12 ACM-13 --json`,
		SuggestFor:        []string{"log", "timeline", "recap"},
		ValidArgsFunction: g.completeTickets(true),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usage("usage: tkt history ID...")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.IDs = args
			res, err := g.app().History(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
}

func doctorCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Check the tools, versions and configuration tkt needs",
		Long:    helpText("doctor"),
		Example: "  tkt doctor\n  tkt doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := g.app().Doctor(cmd.Context())
			return show(g, cmd, res, res == nil, err)
		},
	}
}

func updateCmd(g *globals) *cobra.Command {
	var o app.UpdateOpts
	cmd := &cobra.Command{
		Use:   "update [VERSION]",
		Short: "Replace tkt with the latest release, or with VERSION",
		Long:  helpText("update"),
		Example: `  tkt update
  tkt update --check
  tkt update v0.3.0-rc.1`,
		SuggestFor: []string{"upgrade", "self-update"},
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usage("update takes one version")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.Version = args[0]
			}
			res, err := g.app().Update(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().BoolVar(&o.Check, "check", false, "report whether a release would replace tkt, and change nothing")
	cmd.Flags().BoolVar(&o.Force, "force", false, "replace a development build, or reinstall the same version")
	return cmd
}

func agentCmd(g *globals) *cobra.Command {
	var o app.FrontOpts
	cmd := &cobra.Command{
		Use:   "agent [-- PI_FLAGS...]",
		Short: "Open the tkt agent, a chat that drives your tickets",
		Long:  helpText("agent"),
		Example: `  tkt agent
  tkt agent --continue
  tkt agent --session 01a15a4d
  tkt agent --model acme/large
  tkt agent -- --thinking high
  tkt agent --dry-run --json`,
		SuggestFor: []string{"chat", "pi"},
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && cmd.ArgsLenAtDash() != 0 {
				return usage("tkt agent takes no arguments; put pi's own flags after --, such as tkt agent -- --thinking high")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.json && !o.Dry {
				return usage("tkt agent is a chat, so it prints no JSON; add --dry-run to see what it would run")
			}
			o.PiArgs = args
			res, err := g.app().Front(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().StringVar(&o.Model, "model", "", "use pi model PATTERN instead of the [agent] model, such as acme/large")
	cmd.Flags().BoolVarP(&o.Continue, "continue", "c", false, "continue the last chat in this repository")
	cmd.Flags().StringVar(&o.Session, "session", "", "resume the chat with session ID, or a unique part of it, as the line pi prints on exit names it")
	cmd.MarkFlagsMutuallyExclusive("continue", "session")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print how tkt would start pi instead of starting it")
	return cmd
}
