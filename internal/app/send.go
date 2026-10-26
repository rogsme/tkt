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
	Wait bool
	// Steer types the text in while the agent works: Claude Code and
	// OpenCode read it at their next step. With Wait, only a question in
	// the agent's own dialog holds the text back.
	Steer bool
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
	Ticket  string `json:"ticket"`
	Pane    string `json:"pane"`
	Agent   string `json:"agent"`
	Harness string `json:"harness"`
	Session string `json:"session"`
	Resent  bool   `json:"resent"`
	// Steered says the agent was working, so it reads the text at its next
	// step rather than as a new turn.
	Steered bool     `json:"steered"`
	DryRun  bool     `json:"dry_run"`
	Actions []Action `json:"actions"`
	// approve marks a result of tkt approve, for its text.
	approve bool
}

func (r *SendResult) Text(w io.Writer) {
	switch {
	case r.approve:
		fmt.Fprintf(w, "%s  plan approved  pane %s; work-ticket runs in the same session\n", r.Ticket, r.Pane)
	case r.Steered:
		fmt.Fprintf(w, "%s  sent while the agent works  pane %s  agent %s\n", r.Ticket, r.Pane, r.Agent)
	default:
		fmt.Fprintf(w, "%s  sent  pane %s  agent %s\n", r.Ticket, r.Pane, r.Agent)
	}
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
		if target, err = c.waitToSend(id, path, o.Pane, ts, target, o.Steer); err != nil {
			return nil, err
		}
	}
	return c.sendTo(id, path, ts, target, text, o.Steer)
}

// waitToSend waits until the ticket's agent is free to read typed text: not
// working, not waiting on its own background task, and not asking a
// question in a dialog. With steer, only the dialog counts. It holds no
// lock while it waits, so tkt auto and the other commands go on.
func (c *Ctx) waitToSend(id, path, pane string, ts *ticketState, target herdr.Agent, steer bool) (herdr.Agent, error) {
	told := false
	for {
		free := target.Status == herdr.Idle || target.Status == herdr.Done
		if steer && (free || target.Status == herdr.Working) {
			return target, nil
		}
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
	if chore := ts.st["chore"]; chore != "" {
		return nil, apperr.New(apperr.StageNeedsTicket, "%s is a run of chore %s, with no ticket and no plan, and the approval starts work-ticket, which needs a ticket", id, chore).
			WithHint("tkt send %s TEXT tells its agent what to do", id)
	}
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
	line := config.ApproveLine
	if ts.st["yolo"] != "" {
		line = withLine(line, config.AutonomousLine)
	}
	res, err := c.sendTo(id, path, ts, target, line, false)
	if err != nil {
		return nil, err
	}
	res.approve = true
	c.save(ts, "stage", "work", "rounds", "0")
	c.record(history.Event{Ticket: id, Event: history.Approved})
	return res, nil
}

// planApproved moves a ticket to the work stage when its session says the
// plan was approved there, as start-ticket does with Status: PLAN_APPROVED
// once you approve in the plan viewer, so no tkt approve runs. The stage
// change itself keeps it from happening twice.
func (c *Ctx) planApproved(id string, ts *ticketState) {
	if ts.st["stage"] != "start" {
		return
	}
	c.save(ts, "stage", "work", "rounds", "0")
	c.record(history.Event{Ticket: id, Event: history.Approved, Action: "session"})
	c.UI.Info("%s: the plan was approved in its session, so it is building now", id)
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
	id, err := c.workID(raw)
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

// sendTo types text into a ticket's agent, which must be idle or done, or,
// with steer, working: the harness then reads it at its next step. A
// blocked agent waits for an answer in its own dialog, which typed text
// would not reach.
func (c *Ctx) sendTo(id, path string, ts *ticketState, target herdr.Agent, text string, steer bool) (*SendResult, error) {
	switch target.Status {
	case herdr.Idle, herdr.Done:
	case herdr.Working:
		if !steer {
			return nil, apperr.New(apperr.AgentBusy, "the agent in pane %s is working; pass --steer to type the text in now, or wait for it first", target.PaneID).
				WithHint("tkt wait %s", id).With("pane", target.PaneID).With("status", target.Status)
		}
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
	working := target.Status == herdr.Working
	if !working && h == config.OpenCode && sid != "" && c.OC.SessionActive(c.Ctx, sid) {
		if !steer {
			return nil, apperr.New(apperr.AgentBusy, "OpenCode still works on session %s; pass --steer to type the text in now", sid).WithHint("tkt wait %s", id)
		}
		working = true
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	res := &SendResult{Ticket: id, Pane: target.PaneID, Agent: target.NameOr(""), Harness: h, Session: sid, Steered: working, DryRun: c.Dry}
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
