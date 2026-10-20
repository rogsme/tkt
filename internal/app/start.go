package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/prompt"
)

// StartOpts are tkt start's arguments.
type StartOpts struct {
	ID string
	// Scratch names a worktree with no ticket; its agent gets no prompt.
	Scratch string
	Prompt  prompt.Source
	As      string
	Focus   bool
	Force   bool
	Dry     bool
}

// StartResult is what tkt start did.
type StartResult struct {
	Ticket          string   `json:"ticket"`
	Scratch         string   `json:"scratch,omitempty"`
	Worktree        string   `json:"worktree"`
	WorktreeCreated bool     `json:"worktree_created"`
	Tab             string   `json:"tab"`
	Pane            string   `json:"pane"`
	Agent           string   `json:"agent"`
	Preset          string   `json:"preset"`
	Harness         string   `json:"harness"`
	Session         string   `json:"session"`
	Stage           string   `json:"stage"`
	Continued       bool     `json:"continued"`
	Resent          bool     `json:"resent"`
	DryRun          bool     `json:"dry_run"`
	Actions         []Action `json:"actions"`
}

func (r *StartResult) Text(w io.Writer) {
	name := r.Ticket
	if r.Scratch != "" {
		name = r.Scratch
	}
	fmt.Fprintf(w, "%s  %s  tab %s  agent %s (%s, %s)\n", name, r.Worktree, r.Tab, r.Agent, r.Preset, r.Harness)
}

// Start creates or reuses a ticket's worktree, opens its tab, starts an agent
// and sends it the prompt. A scratch start has no ticket and sends nothing.
func (a *App) Start(ctx context.Context, o StartOpts) (*StartResult, error) {
	scratch := o.Scratch != ""
	text := ""
	if o.Prompt.Set {
		var err error
		if text, err = prompt.Read(o.Prompt, a.Stdin, a.StdinTTY, a.UI); err != nil {
			return nil, err
		}
	}
	c, err := a.begin(ctx, o.Dry, o.Force)
	if err != nil {
		return nil, err
	}
	defer c.release()
	if o.Prompt.Set {
		if err := prompt.Guard(text, o.Force); err != nil {
			return nil, err
		}
	}
	from := ""
	if o.Prompt.Set {
		from = c.LC.FirstID(text)
	}
	var id string
	switch {
	case scratch:
		if err := c.LC.ScratchName(o.Scratch); err != nil {
			return nil, err
		}
		id = o.Scratch
	case o.ID != "":
		if id, err = c.LC.NormalizeID(o.ID); err != nil {
			return nil, err
		}
		if from != "" && from != id && !o.Force {
			return nil, apperr.New(apperr.TicketIDMismatch,
				"you asked for %s but the prompt's first ticket is %s; pass --force to start %s with it", id, from, id).
				WithHint("tkt start %s, or pass --force", from)
		}
	default:
		if from == "" {
			return nil, apperr.New(apperr.TicketIDMissing, "give a ticket id, or a prompt that names one").
				WithHint("tkt start ACM-123, or tkt start --scratch NAME for a worktree with no ticket")
		}
		id = from
		a.UI.Info("ticket %s (the first id in the prompt)", id)
	}
	c.ticket(id)
	preset, err := c.Cfg.Pick(o.As, text, o.Force)
	if err != nil {
		return nil, err
	}
	name := prompt.AgentName(id)
	again, stage, busy := "tkt start "+id, "start", "use tkt run or tkt resume, or close it"
	if scratch {
		again, stage, busy = "tkt start --scratch "+id, "", "close it"
	} else if !o.Prompt.Set {
		text, _ = c.Cfg.StageLine("start", id)
	}
	if err := c.needHerdr(); err != nil {
		return nil, err
	}
	if err := c.needTools(preset.Harness); err != nil {
		return nil, err
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	path, created, err := c.resolveOrCreateWorktree(id, again)
	if err != nil {
		return nil, err
	}
	res := &StartResult{Ticket: id, Worktree: path, WorktreeCreated: created, Stage: stage, DryRun: c.Dry}
	if scratch {
		res.Ticket, res.Scratch = "", id
	}
	ts := &ticketState{st: map[string]string{}}
	var inPath []herdr.Agent
	if !strings.HasPrefix(path, "<") {
		if ts, err = c.readState(path); err != nil {
			return nil, err
		}
		agents, err := c.H.Agents(c.Ctx)
		if err != nil {
			return nil, herdrErr(err)
		}
		inPath = herdr.AgentsInPath(agents, path)
	}
	st := ts.st
	h := preset.Harness
	var tab, pane string
	continuing := st["status"] == "not_ready" && len(inPath) == 1 && inPath[0].PaneID == st["pane"] &&
		(inPath[0].Status == herdr.Idle || inPath[0].Status == herdr.Done)
	switch {
	case continuing:
		a.UI.Info("continuing the start that stopped at the agent's startup dialog")
		tab, pane, name, h = st["tab"], st["pane"], st["agent"], st["harness"]
		preset.Name = st["preset"]
	case len(inPath) > 0 && scratch:
		return nil, apperr.New(apperr.AgentRunning, "an agent already runs in %s (pane %s)", path, inPath[0].PaneID).
			WithHint("switch to pane %s", inPath[0].PaneID)
	case len(inPath) > 0:
		return nil, apperr.New(apperr.AgentRunning, "an agent already runs in %s (pane %s); use tkt run or tkt resume", path, inPath[0].PaneID).
			WithHint("tkt run %s STAGE, or tkt resume %s", id, id)
	default:
		if tab, pane, err = c.startTab(id, path, preset, busy); err != nil {
			return nil, err
		}
		c.save(ts, "ticket", id, "preset", preset.Name, "harness", h, "stage", stage, "tab", tab, "pane", pane, "agent", name, "status", "tab")
		if !scratch && st["started"] == "" {
			c.save(ts, "started", c.Now().UTC().Format(history.TimeFormat))
			c.record(history.Event{Ticket: id, Event: history.Started})
		}
		if !c.Dry {
			c.Agent.WaitPaneFree(c.Ctx, pane, 40)
			p, err := c.H.PaneGet(c.Ctx, pane)
			if err != nil {
				return nil, herdrErr(err)
			}
			if p.Cwd != path {
				return nil, apperr.New(apperr.PaneOutsideWorktree, "the new pane opened in %s instead of %s", p.Cwd, path)
			}
		}
		sid, err := c.newSession(path, id, preset)
		if err != nil {
			return nil, err
		}
		c.save(ts, "session", sid, "status", "launching")
		out, err := c.Agent.Launch(c.Ctx, pane, name, h, harness.LaunchArgs(h, harness.New, sid, preset, path))
		switch out {
		case agent.NotReady:
			c.save(ts, "status", "not_ready")
			rerun := again
			if !scratch {
				rerun += " with the same prompt"
			}
			return nil, c.screenErr(apperr.New(apperr.AgentNotReady,
				"the agent stopped at a startup dialog in tab %s; answer it there, then rerun %s", tab, rerun).
				WithHint("answer the dialog in tab %s, then rerun %s", tab, rerun), pane)
		case agent.Failed:
			return nil, apperr.New(apperr.AgentLaunchFailed, "%s", err.Error())
		}
	}
	res.Tab, res.Pane, res.Agent, res.Preset, res.Harness, res.Continued = tab, pane, name, preset.Name, h, continuing
	res.Session = st["session"]
	if scratch {
		c.save(ts, "status", "launched")
	} else {
		out, resent, err := c.Agent.Deliver(c.Ctx, pane, text, h, st["session"], path)
		res.Resent = resent
		switch out {
		case agent.Uncertain:
			c.save(ts, "status", "prompt_uncertain")
			return nil, c.screenErr(apperr.New(apperr.PromptUncertain,
				"cannot confirm the agent received the prompt; check tab %s before sending it again", tab).
				WithHint("check tab %s before sending the prompt again", tab), pane)
		case agent.Failed:
			return nil, sendErr("sending the prompt failed", err)
		}
		c.save(ts, "status", "submitted")
	}
	if o.Focus && !c.Dry {
		if err := c.H.TabFocus(c.Ctx, tab); err != nil {
			return nil, herdrErr(err)
		}
	}
	res.Actions = c.Plan
	return res, nil
}

// startTab reuses the ticket's idle tab in this workspace, or opens one. busy
// is the advice for a tab that is in use.
func (c *Ctx) startTab(id, path string, preset config.Preset, busy string) (tab, pane string, err error) {
	tabs, err := c.H.Tabs(c.Ctx)
	if err != nil {
		return "", "", herdrErr(err)
	}
	ws := c.Getenv("HERDR_WORKSPACE_ID")
	var mine []herdr.Tab
	for _, t := range herdr.TicketTabs(tabs, id) {
		if t.WorkspaceID == ws {
			mine = append(mine, t)
		}
	}
	if len(mine) > 0 {
		t := mine[0]
		panes, err := c.H.Panes(c.Ctx)
		if err != nil {
			return "", "", herdrErr(err)
		}
		var own []herdr.Pane
		idle := true
		for _, p := range panes {
			if p.TabID == t.TabID {
				own = append(own, p)
				idle = idle && !p.HasAgent() && p.Cwd == path
			}
		}
		if !idle || len(own) == 0 {
			return "", "", apperr.New(apperr.TabInUse, "tab '%s' already belongs to %s; %s", t.Label, id, busy)
		}
		if !c.H.PaneIsShell(c.Ctx, own[0].PaneID) {
			return "", "", apperr.New(apperr.TabInUse, "tab '%s' is busy", t.Label)
		}
		c.UI.Info("reusing tab %s", t.TabID)
		return t.TabID, own[0].PaneID, nil
	}
	return c.openTab(path, c.Cfg.TabLabel(id, preset.Name))
}

// openTab opens a tab for the ticket, or reports it in a dry run.
func (c *Ctx) openTab(path, label string) (tab, pane string, err error) {
	ws := c.Getenv("HERDR_WORKSPACE_ID")
	if c.Dry {
		c.Would(herdr.TabCreateArgv(ws, path, label))
		return "<tab>", "<pane>", nil
	}
	tab, pane, err = c.H.TabCreate(c.Ctx, ws, path, label)
	return tab, pane, herdrErr(err)
}

// newSession picks the session id for a fresh agent: a UUID for Claude Code,
// a session created through the API for OpenCode.
func (c *Ctx) newSession(path, title string, p config.Preset) (string, error) {
	if p.Harness == config.Claude {
		return harness.NewUUID(), nil
	}
	m, err := harness.OpenCodeModel(p.Model)
	if err != nil {
		return "", err
	}
	body := harness.SessionBody(path, title, m)
	if c.Dry {
		c.Would(harness.CreateSessionArgv(body))
		return "ses_dryrun", nil
	}
	return c.OC.CreateSession(c.Ctx, body)
}

func sendErr(msg string, err error) error {
	code, detail := "", ""
	if e, ok := err.(*herdr.Error); ok {
		code, detail = e.Code, e.Stderr
	}
	return apperr.New(apperr.PromptSendFailed, "%s: %s %s", msg, code, detail).Wrap(err)
}
