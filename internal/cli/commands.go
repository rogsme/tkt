package cli

import (
	"io"

	"github.com/spf13/cobra"

	"gitlab.com/rogs/tkt/internal/app"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/prompt"
)

type texter interface{ Text(w io.Writer) }

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
	if !isNil {
		res.Text(cmd.OutOrStdout())
	}
	return err
}

func promptFlag(cmd *cobra.Command, text *string) {
	cmd.Flags().StringVar(text, "prompt", "", "send TEXT instead of the stage line; - reads it from stdin")
}

func promptSource(cmd *cobra.Command, text string) prompt.Source {
	return prompt.Source{Text: text, Set: cmd.Flags().Changed("prompt")}
}

func startCmd(g *globals) *cobra.Command {
	var o app.StartOpts
	var text string
	cmd := &cobra.Command{
		Use:   "start [ID]",
		Short: "Create the worktree and tab, start an agent and send it the prompt",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usage("start takes one ticket id")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.ID = args[0]
			}
			o.Prompt = promptSource(cmd, text)
			res, err := g.app().Start(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	promptFlag(cmd, &text)
	cmd.Flags().StringVar(&o.As, "as", "", "run on PRESET instead of the one the prompt's Model: line picks")
	cmd.Flags().BoolVar(&o.Focus, "focus", false, "switch to the ticket's tab afterwards")
	cmd.Flags().BoolVar(&o.Force, "force", false, "override the safety refusals")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func runCmd(g *globals) *cobra.Command {
	var o app.RunOpts
	var text string
	cmd := &cobra.Command{
		Use:   "run ID [STAGE]",
		Short: "Replace the ticket's idle agent with a fresh session and send a stage line",
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return usage("usage: tkt run ID STAGE")
			case len(args) > 2:
				return usage("run takes a ticket id and a stage")
			case len(args) == 1 && !cmd.Flags().Changed("prompt"):
				return usage("usage: tkt run ID STAGE (or --prompt TEXT)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ID = args[0]
			if len(args) == 2 {
				o.Stage = args[1]
			}
			o.Prompt = promptSource(cmd, text)
			res, err := g.app().Run(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	promptFlag(cmd, &text)
	cmd.Flags().StringVar(&o.As, "as", "", "run on PRESET instead of the ticket's")
	cmd.Flags().StringVar(&o.Pane, "pane", "", "use PANE when several agents run in the worktree")
	cmd.Flags().BoolVar(&o.Force, "force", false, "replace an agent that is planning or in an unknown state")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func resumeCmd(g *globals) *cobra.Command {
	var o app.ResumeOpts
	cmd := &cobra.Command{
		Use:   "resume ID",
		Short: "Relaunch the ticket's newest session when no agent runs for it",
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
	cmd.Flags().BoolVar(&o.Focus, "focus", false, "switch to the ticket's tab afterwards")
	cmd.Flags().BoolVar(&o.Dry, "dry-run", false, "print the changes instead of making them")
	return cmd
}

func doneCmd(g *globals) *cobra.Command {
	var o app.DoneOpts
	cmd := &cobra.Command{
		Use:   "done [ID...]",
		Short: "Close a merged ticket's tabs and remove its worktree",
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
		Use:   "ls",
		Short: "List ticket worktrees with their state and the next step",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := g.app().Ls(cmd.Context(), o)
			return show(g, cmd, res, res == nil, err)
		},
	}
	cmd.Flags().BoolVar(&o.NoFetch, "no-fetch", false, "skip the git fetch that refreshes the commits-behind count")
	return cmd
}
