package app

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/herdr"
)

// WaitStates are the agent states tkt wait can wait for.
var WaitStates = []string{herdr.Idle, herdr.Working, herdr.Blocked, herdr.Done}

// WaitOpts are tkt wait's arguments.
type WaitOpts struct {
	ID string
	// Until lists the states that end the wait; empty means idle, blocked
	// or done, that is, the agent needs you or has finished.
	Until   []string
	Timeout time.Duration
	Pane    string
	// Notify shows a herdr notification when the wait ends.
	Notify bool
}

// WaitResult is the agent's state when the wait ended.
type WaitResult struct {
	Ticket     string `json:"ticket"`
	Pane       string `json:"pane"`
	Agent      string `json:"agent"`
	Status     string `json:"status"`
	LastStatus string `json:"last_status"`
	WaitedMs   int64  `json:"waited_ms"`
}

func (r *WaitResult) Text(w io.Writer) {
	last := r.LastStatus
	if last == "" {
		last = "-"
	}
	fmt.Fprintf(w, "%s  %s  pane %s  last status %s\n", r.Ticket, r.Status, r.Pane, last)
}

// Wait blocks until the ticket's agent reaches one of the states.
func (a *App) Wait(ctx context.Context, o WaitOpts) (*WaitResult, error) {
	until := o.Until
	if len(until) == 0 {
		until = []string{herdr.Idle, herdr.Blocked, herdr.Done}
	}
	for _, u := range until {
		if !slices.Contains(WaitStates, u) {
			return nil, apperr.New(apperr.Usage, "--until takes %s, not %q", strings.Join(WaitStates, ", "), u)
		}
	}
	c, err := a.begin(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	id, err := c.workID(o.ID)
	if err != nil {
		return nil, err
	}
	c.ticket(id)
	if err := c.needHerdr(); err != nil {
		return nil, err
	}
	path, err := c.ticketPath(id)
	if err != nil {
		return nil, err
	}
	ts, err := c.readState(path)
	if err != nil {
		return nil, err
	}
	target, err := c.ticketAgent(id, path, o.Pane, ts.st)
	if err != nil {
		return nil, err
	}
	wctx := c.Ctx
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		wctx, cancel = context.WithTimeout(c.Ctx, o.Timeout+5*time.Second)
		defer cancel()
	}
	start := c.Now()
	status, err := c.H.Wait(wctx, target.PaneID, until, o.Timeout.Milliseconds())
	res := &WaitResult{Ticket: id, Pane: target.PaneID, Agent: target.NameOr(""), WaitedMs: c.Now().Sub(start).Milliseconds()}
	switch herdr.CodeOf(err) {
	case "":
		if err != nil {
			return nil, herdrErr(err)
		}
	case "timeout":
		return nil, apperr.New(apperr.WaitTimeout, "the agent in pane %s did not become %s within %s", target.PaneID, strings.Join(until, " or "), o.Timeout).
			WithHint("tkt wait %s again, or check pane %s", id, target.PaneID).With("pane", target.PaneID)
	case "agent_not_found":
		return nil, apperr.New(apperr.AgentMissing, "the agent in pane %s is gone", target.PaneID).WithHint("tkt resume %s", id)
	default:
		return nil, herdrErr(err)
	}
	res.Status = status
	if status == herdr.Idle || status == herdr.Done {
		target.Status = status
		turn, _ := c.lastTurn(c.Ctx, &target, ts.st, path)
		res.LastStatus = agent.LastStatus(turn.All)
	}
	if o.Notify {
		c.notify(id, waitNotice(res))
	}
	return res, nil
}

// waitNotice says why a wait ended, for its notification.
func waitNotice(r *WaitResult) notice {
	if r.Status == herdr.Blocked {
		return notice{"needs you", "the agent asks a question in its tab", "request"}
	}
	if r.LastStatus != "" {
		return notice{"finished", "Status: " + r.LastStatus, "done"}
	}
	return notice{"finished", "the agent is " + r.Status, "done"}
}

// notice is a herdr notification about a ticket.
type notice struct{ title, body, sound string }

// notify shows a notification about a ticket. A failed notification is a
// warning: the result already says what happened.
func (c *Ctx) notify(id string, n notice) {
	if c.Dry {
		c.Would(herdr.NotifyArgv(id+": "+n.title, n.body, n.sound))
		return
	}
	if err := c.H.Notify(c.Ctx, id+": "+n.title, n.body, n.sound); err != nil {
		c.UI.Warn("could not show a notification: %v", err)
	}
}
