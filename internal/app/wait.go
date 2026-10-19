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
	id, err := c.LC.NormalizeID(o.ID)
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
	agents, err := c.H.Agents(c.Ctx)
	if err != nil {
		return nil, herdrErr(err)
	}
	inPath := herdr.AgentsInPath(agents, path)
	var target herdr.Agent
	switch {
	case o.Pane != "":
		i := slices.IndexFunc(inPath, func(ag herdr.Agent) bool { return ag.PaneID == o.Pane })
		if i < 0 {
			return nil, apperr.New(apperr.AgentMissing, "no agent of %s runs in pane %s", id, o.Pane).WithHint("tkt ls %s", id)
		}
		target = inPath[i]
	case len(inPath) == 0:
		return nil, apperr.New(apperr.AgentMissing, "no agent runs for %s", id).WithHint("tkt resume %s", id)
	case len(inPath) == 1:
		target = inPath[0]
	default:
		i := slices.IndexFunc(inPath, func(ag herdr.Agent) bool { return ag.PaneID == ts.st["pane"] })
		if i < 0 {
			panes := make([]string, len(inPath))
			for j, ag := range inPath {
				panes[j] = ag.PaneID
			}
			return nil, apperr.New(apperr.AmbiguousPane, "%d agents run in %s; pick one with --pane (%s)", len(inPath), path, strings.Join(panes, ", ")).
				WithHint("pass --pane %s", panes[0]).With("panes", panes)
		}
		target = inPath[i]
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
		res.LastStatus = agent.LastStatus(c.H.Read(c.Ctx, target.PaneID, 40))
	}
	return res, nil
}
