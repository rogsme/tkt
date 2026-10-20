package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/prompt"
)

// SendOpts are tkt send's arguments.
type SendOpts struct {
	ID     string
	Prompt prompt.Source
	Pane   string
	// Wait queues the text: when the agent works, waits on a background
	// task or asks a question, tkt send waits for it to stop first.
	Wait  bool
	Force bool
	Dry   bool
}

// ApproveOpts are tkt approve's arguments.
type ApproveOpts struct {
	ID    string
	Pane  string
	Force bool
	Dry   bool
}

// SendResult is what tkt send or tkt approve typed into a session.
type SendResult struct {
	Ticket  string   `json:"ticket"`
	Pane    string   `json:"pane"`
	Agent   string   `json:"agent"`
	Harness string   `json:"harness"`
	Session string   `json:"session"`
	Resent  bool     `json:"resent"`
	DryRun  bool     `json:"dry_run"`
	Actions []Action `json:"actions"`
	// approve marks a result of tkt approve, for its text.
	approve bool
}

func (r *SendResult) Text(w io.Writer) {
	if r.approve {
		fmt.Fprintf(w, "%s  plan approved  pane %s; work-ticket runs in the same session\n", r.Ticket, r.Pane)
		return
	}
	fmt.Fprintf(w, "%s  sent  pane %s  agent %s\n", r.Ticket, r.Pane, r.Agent)
}

// Send types text into the ticket's running session, which keeps its whole
// conversation: to ask for changes to a plan, or to answer a question the
// agent asked in its last message.
func (a *App) Send(ctx context.Context, o SendOpts) (*SendResult, error) {
	text, err := prompt.Read(o.Prompt, a.Stdin, a.StdinTTY, a.UI)
	if err != nil {
		return nil, err
	}
	if err := prompt.Guard(text, o.Force); err != nil {
		return nil, err
	}
	c, id, path, ts, err := a.beginTicket(ctx, o.ID, o.Dry, o.Force)
	if err != nil {
		return nil, err
	}
	defer c.release()
	target, err := c.ticketAgent(id, path, o.Pane, ts.st)
	if err != nil {
		return nil, err
	}
	if o.Wait && !c.Dry {
		if target, err = c.waitToSend(id, path, o.Pane, ts, target); err != nil {
			return nil, err
		}
	}
	return c.sendTo(id, path, ts, target, text)
}

// waitToSend waits until the ticket's agent is free to read typed text: not
// working, not waiting on its own background task, and not asking a
// question in a dialog. It holds no lock while it waits, so tkt auto and
// the other commands go on.
func (c *Ctx) waitToSend(id, path, pane string, ts *ticketState, target herdr.Agent) (herdr.Agent, error) {
	told := false
	for {
		free := target.Status == herdr.Idle || target.Status == herdr.Done
		if free {
			turn, _ := c.lastTurn(c.Ctx, &target, ts.st, path)
			if turn.Pending == 0 || agent.LastStatus(turn.All) != "" || c.Now().Sub(turn.At) >= backgroundGrace {
				return target, nil
			}
		}
		if !told {
			c.UI.Info("%s's agent is busy; the text waits until it stops", id)
			told = true
		}
		if target.Status == herdr.Working {
			_, err := c.H.Wait(c.Ctx, target.PaneID, []string{herdr.Idle, herdr.Blocked, herdr.Done}, waitSlice.Milliseconds())
			if c.Ctx.Err() != nil {
				return target, c.Ctx.Err()
			}
			switch herdr.CodeOf(err) {
			case "", "timeout":
			default:
				return target, herdrErr(err)
			}
		} else {
			t := time.NewTimer(15 * time.Second)
			select {
			case <-c.Ctx.Done():
				t.Stop()
				return target, c.Ctx.Err()
			case <-t.C:
			}
		}
		var err error
		if target, err = c.ticketAgent(id, path, pane, ts.st); err != nil {
			return target, err
		}
	}
}

// Approve tells the planning session that its plan is approved, so the same
// session goes on with work-ticket. It refuses unless the agent's last
// status is AWAITING_APPROVAL.
func (a *App) Approve(ctx context.Context, o ApproveOpts) (*SendResult, error) {
	c, id, path, ts, err := a.beginTicket(ctx, o.ID, o.Dry, o.Force)
	if err != nil {
		return nil, err
	}
	defer c.release()
	return c.approve(id, path, ts, o.Pane)
}

// approve checks the plan and sends the approval; tkt auto calls it too.
func (c *Ctx) approve(id, path string, ts *ticketState, pane string) (*SendResult, error) {
	target, err := c.ticketAgent(id, path, pane, ts.st)
	if err != nil {
		return nil, err
	}
	if !c.Force {
		turn, _ := c.lastTurn(c.Ctx, &target, ts.st, path)
		if last := agent.LastStatus(turn.All); last != contract.StatusAwaitingApproval || target.Status == herdr.Working {
			if last == "" {
				last = "none"
			}
			return nil, apperr.New(apperr.NoPlanToApprove, "%s has no plan waiting for approval (the agent is %s, last status %s)", id, target.Status, last).
				WithHint("read the agent's last message with tkt show %s", id).With("last_status", last)
		}
	}
	res, err := c.sendTo(id, path, ts, target, config.ApproveLine)
	if err != nil {
		return nil, err
	}
	res.approve = true
	c.save(ts, "stage", "work", "rounds", "0")
	c.record(history.Event{Ticket: id, Event: history.Approved})
	return res, nil
}

// beginTicket starts a command on one ticket that has a worktree and runs in
// herdr.
func (a *App) beginTicket(ctx context.Context, raw string, dry, force bool) (*Ctx, string, string, *ticketState, error) {
	c, err := a.begin(ctx, dry, force)
	if err != nil {
		return nil, "", "", nil, err
	}
	fail := func(err error) (*Ctx, string, string, *ticketState, error) {
		c.release()
		return nil, "", "", nil, err
	}
	id, err := c.LC.NormalizeID(raw)
	if err != nil {
		return fail(err)
	}
	c.ticket(id)
	if err := c.needHerdr(); err != nil {
		return fail(err)
	}
	path, err := c.ticketPath(id)
	if err != nil {
		return fail(err)
	}
	ts, err := c.readState(path)
	if err != nil {
		return fail(err)
	}
	return c, id, path, ts, nil
}

// sendTo types text into a ticket's agent, which must be idle or done: a
// working agent is busy, and a blocked one waits for an answer in its own
// dialog, which typed text would not reach.
func (c *Ctx) sendTo(id, path string, ts *ticketState, target herdr.Agent, text string) (*SendResult, error) {
	switch target.Status {
	case herdr.Idle, herdr.Done:
	case herdr.Working:
		return nil, apperr.New(apperr.AgentBusy, "the agent in pane %s is working; wait for it first", target.PaneID).
			WithHint("tkt wait %s", id).With("pane", target.PaneID).With("status", target.Status)
	case herdr.Blocked:
		return nil, apperr.New(apperr.AgentBusy, "the agent in pane %s waits for an answer in its own dialog; answer it in its tab", target.PaneID).
			WithHint("answer the agent in pane %s", target.PaneID).With("pane", target.PaneID).With("status", target.Status)
	default:
		if !c.Force {
			return nil, apperr.New(apperr.AgentStateUnknown, "the agent in pane %s is in state '%s'; pass --force to send anyway", target.PaneID, target.Status).
				WithHint("check pane %s, or pass --force", target.PaneID)
		}
	}
	h, sid := ts.st["harness"], ts.st["session"]
	if target.Kind == config.Claude || target.Kind == config.OpenCode {
		h = target.Kind
	}
	if s := target.SessionID(); s != "" && h == config.Claude {
		sid = s
	}
	if h == config.OpenCode && sid != "" && c.OC.SessionActive(c.Ctx, sid) {
		return nil, apperr.New(apperr.AgentBusy, "OpenCode still works on session %s", sid).WithHint("tkt wait %s", id)
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	res := &SendResult{Ticket: id, Pane: target.PaneID, Agent: target.NameOr(""), Harness: h, Session: sid, DryRun: c.Dry}
	out, resent, err := c.Agent.Deliver(c.Ctx, target.PaneID, text, h, sid, path)
	res.Resent = resent
	switch out {
	case agent.Uncertain:
		return nil, c.screenErr(apperr.New(apperr.PromptUncertain,
			"cannot confirm the agent received the text; check pane %s before sending it again", target.PaneID).
			WithHint("check pane %s before sending it again", target.PaneID), target.PaneID)
	case agent.Failed:
		if herdr.CodeOf(err) == "agent_blocked" {
			return nil, apperr.New(apperr.AgentBusy, "the agent in pane %s waits for an answer; answer it in its tab", target.PaneID).
				WithHint("answer the agent in pane %s", target.PaneID)
		}
		return nil, sendErr("sending the text failed", err)
	}
	res.Actions = c.Plan
	return res, nil
}
