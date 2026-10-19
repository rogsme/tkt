package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/runner"
)

var statusRE = contract.StatusLineRE

// Outcome is how a launch or a prompt ended.
type Outcome int

const (
	// OK: the agent is ready, or took the prompt.
	OK Outcome = iota
	// NotReady: the agent stopped at a startup dialog.
	NotReady
	// Uncertain: tkt cannot tell whether the agent took the prompt; never
	// send it again then.
	Uncertain
	// Failed: see the error.
	Failed
)

// Launch starts an agent in an idle shell pane.
func (o Ops) Launch(ctx context.Context, pane, name, h string, args []string) (Outcome, error) {
	if o.Dry {
		o.Would(herdr.AgentStartArgv(name, h, pane, args))
		return OK, nil
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := o.H.AgentStart(ctx, name, h, pane, args)
		if err == nil {
			return OK, nil
		}
		switch herdr.CodeOf(err) {
		case "agent_not_ready":
			return NotReady, nil
		case "agent_pane_busy":
			if time.Now().Before(deadline) {
				o.sleep(250 * time.Millisecond)
				continue
			}
		default:
			return Failed, fmt.Errorf("herdr agent start: %s", codeOr(err))
		}
		break
	}
	// herdr also calls a pane busy when the shell keeps a zombie child in its
	// foreground process group, which fish sometimes does; typing works there.
	if !o.H.PaneIsShell(ctx, pane) {
		return Failed, fmt.Errorf("pane %s is not at a shell prompt", pane)
	}
	cmd, err := ShellWords(append([]string{h}, args...)...)
	if err != nil {
		return Failed, errors.New("cannot quote the launch command for the shell")
	}
	o.UI.Info("herdr reports pane %s busy; typing the command into its shell instead", pane)
	if err := o.H.PaneRun(ctx, pane, cmd); err != nil {
		return Failed, fmt.Errorf("herdr pane run: %s", herdr.CodeOf(err))
	}
	found := false
	for i := 0; i < 80 && !found; i++ {
		if _, found = o.H.AgentOnPane(ctx, pane); !found {
			o.sleep(250 * time.Millisecond)
		}
	}
	if !found {
		return Failed, fmt.Errorf("no agent appeared in pane %s", pane)
	}
	status, err := o.H.Wait(ctx, pane, []string{herdr.Idle, herdr.Blocked, herdr.Done}, 90000)
	if err != nil {
		return Failed, fmt.Errorf("the agent in pane %s did not become ready: %s", pane, herdr.CodeOf(err))
	}
	if err := o.H.Rename(ctx, pane, name); err != nil {
		o.UI.Warn("could not name the agent in pane %s '%s'", pane, name)
	}
	if status == herdr.Blocked {
		return NotReady, nil
	}
	return OK, nil
}

func codeOr(err error) string {
	if c := herdr.CodeOf(err); c != "" {
		return c
	}
	if e, ok := err.(*herdr.Error); ok {
		return runner.Head(e.Stderr, 200)
	}
	return err.Error()
}

// Send types text into an agent and waits until it starts working.
func (o Ops) Send(ctx context.Context, target, text string) (Outcome, error) {
	if o.Dry {
		o.Would(herdr.PromptArgv(target, text))
		return OK, nil
	}
	err := o.H.Prompt(ctx, target, text)
	if err == nil {
		return OK, nil
	}
	switch herdr.CodeOf(err) {
	case "agent_prompt_stalled", "timeout":
		return Uncertain, err
	}
	return Failed, err
}

// Deliver sends text to the agent in a pane. When herdr cannot confirm the
// agent took it, the session's own record decides: a prompt it never
// recorded, to an agent that is still idle, is sent once more.
func (o Ops) Deliver(ctx context.Context, pane, text, h, sid, path string) (Outcome, bool, error) {
	if o.Dry {
		out, err := o.Send(ctx, pane, text)
		return out, false, err
	}
	before, known := o.UserTurns(ctx, h, sid, path)
	out, err := o.Send(ctx, pane, text)
	if out != Uncertain || !known {
		return out, false, err
	}
	o.sleep(3 * time.Second)
	after, known := o.UserTurns(ctx, h, sid, path)
	if !known {
		return Uncertain, false, err
	}
	if after > before {
		return OK, false, nil
	}
	a, _ := o.H.AgentOnPane(ctx, pane)
	if a.Status != herdr.Idle && a.Status != herdr.Done {
		return Uncertain, false, err
	}
	o.UI.Info("the agent never recorded the prompt; sending it once more")
	out, err = o.Send(ctx, pane, text)
	return out, true, err
}

// Exit closes the idle agent in a pane and waits until herdr sees a free
// shell. It reports whether the pane is free.
func (o Ops) Exit(ctx context.Context, pane string) bool {
	if o.Dry {
		o.Would([]string{"herdr", "agent", "prompt", pane, "/exit"})
		return true
	}
	o.H.Type(ctx, pane, "/exit")
	for i := 0; i < 25 && !o.H.PaneIsShell(ctx, pane); i++ {
		o.sleep(200 * time.Millisecond)
	}
	// /exit only works on an empty input box; Ctrl-C clears it, then exits.
	for i := 0; i < 3 && !o.H.PaneIsShell(ctx, pane); i++ {
		o.H.SendKeys(ctx, pane, "ctrl+c")
		o.sleep(700 * time.Millisecond)
	}
	return o.WaitPaneFree(ctx, pane, 40)
}
