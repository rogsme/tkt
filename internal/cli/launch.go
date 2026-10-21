package cli

import (
	"github.com/spf13/cobra"

	"gitlab.com/rogs/tkt/internal/app"
)

// launch is what decides whether bare tkt opens the agent.
type launch struct {
	args      []string
	json      bool
	stdinTTY  bool
	stdoutTTY bool
	herdrEnv  string
	// configured reports whether the repository has the lifecycle
	// configuration. It runs git, so it is asked last.
	configured func() bool
}

// shouldLaunch reports whether bare tkt opens the agent: a person typed
// tkt alone at a terminal in a herdr pane, inside a configured repository.
// Anywhere else, such as in a script or an agent's shell, it prints help.
func shouldLaunch(l launch) bool {
	return len(l.args) == 0 && !l.json && l.stdinTTY && l.stdoutTTY && l.herdrEnv == "1" && l.configured()
}

// runRoot runs bare tkt.
func (g *globals) runRoot(cmd *cobra.Command, args []string) error {
	a := g.app()
	if !shouldLaunch(launch{args: args, json: g.json, stdinTTY: a.StdinTTY, stdoutTTY: a.StdoutTTY,
		herdrEnv: g.io.Getenv("HERDR_ENV"), configured: func() bool { return a.Configured(cmd.Context()) }}) {
		return cmd.Help()
	}
	_, err := a.Front(cmd.Context(), app.FrontOpts{})
	return err
}
