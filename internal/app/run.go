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

// RunOpts are tkt run's arguments.
type RunOpts struct {
	ID     string
	Stage  string
	Prompt prompt.Source
	As     string
	Pane   string
	Force  bool
	Dry    bool
}

// RunResult is what tkt run did.
type RunResult struct {
	Ticket          string   `json:"ticket"`
	Stage           string   `json:"stage"`
	Tab             string   `json:"tab"`
	Pane            string   `json:"pane"`
	Agent           string   `json:"agent"`
	Preset          string   `json:"preset"`
	Harness         string   `json:"harness"`
	Session         string   `json:"session"`
	ReplacedSession string   `json:"replaced_session"`
	Resent          bool     `json:"resent"`
	DryRun          bool     `json:"dry_run"`
	Actions         []Action `json:"actions"`
}

func (r *RunResult) Text(w io.Writer) {
	fmt.Fprintf(w, "%s  %s  pane %s  agent %s (%s, %s)\n", r.Ticket, r.Stage, r.Pane, r.Agent, r.Preset, r.Harness)
}

// Run replaces the ticket's idle agent with a fresh session and sends it a
// stage line or a prompt.
func (a *App) Run(ctx context.Context, o RunOpts) (*RunResult, error) {
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
	id, err := c.LC.NormalizeID(o.ID)
	if err != nil {
		return nil, err
	}
	c.ticket(id)
	if !o.Prompt.Set {
		if text, err = c.Cfg.StageLine(o.Stage, id); err != nil {
			return nil, err
		}
	}
	if err := prompt.Guard(text, o.Force); err != nil {
		return nil, err
	}
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
	st := ts.st
	preset := c.Cfg.Default()
	switch {
	case o.As != "":
		if preset, err = c.Cfg.Preset(o.As); err != nil {
			return nil, err
		}
	case c.Cfg.Has(st["preset"]):
		preset, _ = c.Cfg.Preset(st["preset"])
	}
	h, name := preset.Harness, prompt.AgentName(id)
	if err := c.needTools(h); err != nil {
		return nil, err
	}
	stage := o.Stage
	if stage == "" || o.Prompt.Set {
		stage = "custom"
	}
	if st["stage"] == "start" && !o.Force && c.Git.Count(c.Ctx, path, "refs/remotes/"+c.LC.Forge.Remote+"/"+c.LC.Forge.Base+"..HEAD") <= 0 && c.Git.Clean(c.Ctx, path) {
		return nil, apperr.New(apperr.TicketInPlanning,
			"%s is still in planning (no commits yet); its agent may be waiting for plan approval. Pass --force to replace it", id).
			WithHint("read the plan with tkt show %s, then tkt approve %s, or pass --force", id, id)
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	tab, pane, err := c.pickPane(id, path, o.Pane, st, preset)
	if err != nil {
		return nil, err
	}
	res := &RunResult{Ticket: id, Stage: stage, Tab: tab, Pane: pane, Agent: name, Preset: preset.Name, Harness: h, DryRun: c.Dry}
	if old, ok := c.H.AgentOnPane(c.Ctx, pane); ok {
		switch old.Status {
		case herdr.Idle, herdr.Done:
		case herdr.Working, herdr.Blocked:
			return nil, apperr.New(apperr.AgentBusy, "the agent in pane %s is %s; wait for it or answer it first", pane, old.Status).
				WithHint("tkt wait %s", id).With("pane", pane).With("status", old.Status)
		default:
			if !o.Force {
				return nil, apperr.New(apperr.AgentStateUnknown, "the agent in pane %s is in state '%s'; pass --force to replace it", pane, old.Status).
					WithHint("check pane %s, or pass --force", pane)
			}
		}
		if st["harness"] == config.OpenCode && st["session"] != "" && c.OC.SessionActive(c.Ctx, st["session"]) {
			return nil, apperr.New(apperr.AgentBusy, "OpenCode still works on session %s", st["session"]).WithHint("tkt wait %s", id)
		}
		res.ReplacedSession = st["session"]
		if !c.Agent.Exit(c.Ctx, pane) {
			return nil, apperr.New(apperr.AgentExitFailed, "the agent in pane %s did not exit; nothing was sent", pane).
				WithHint("check pane %s, close the agent there, then rerun tkt run", pane)
		}
	} else if !c.Dry && !c.H.PaneIsShell(c.Ctx, pane) {
		return nil, apperr.New(apperr.PaneBusy, "pane %s is not at a shell prompt", pane).
			WithHint("finish what runs in pane %s, or pass --pane", pane)
	}
	sid, err := c.newSession(path, id+" "+stage, preset)
	if err != nil {
		return nil, err
	}
	res.Session = sid
	c.save(ts, "ticket", id, "preset", preset.Name, "harness", h, "stage", stage, "tab", tab, "pane", pane, "agent", name, "session", sid, "status", "launching")
	out, err := c.Agent.Launch(c.Ctx, pane, name, h, harness.LaunchArgs(h, harness.New, sid, preset, path))
	switch out {
	case agent.NotReady:
		c.save(ts, "status", "not_ready")
		return nil, c.screenErr(apperr.New(apperr.AgentNotReady,
			"the agent stopped at a startup dialog in pane %s; answer it, then send the stage line yourself", pane).
			WithHint("answer the dialog in pane %s, then send the stage line there", pane), pane)
	case agent.Failed:
		return nil, apperr.New(apperr.AgentLaunchFailed, "%s", err.Error())
	}
	out, resent, err := c.Agent.Deliver(c.Ctx, pane, text, h, sid, path)
	res.Resent = resent
	switch out {
	case agent.Uncertain:
		c.save(ts, "status", "prompt_uncertain")
		return nil, c.screenErr(apperr.New(apperr.PromptUncertain,
			"cannot confirm the agent received the stage line; check pane %s before sending it again", pane).
			WithHint("check pane %s before sending the stage line again", pane), pane)
	case agent.Failed:
		return nil, sendErr("sending the stage line failed", err)
	}
	c.save(ts, "status", "submitted")
	c.record(history.Event{Ticket: id, Event: history.Stage, Stage: stage})
	res.Actions = c.Plan
	return res, nil
}

// pickPane finds the pane for a ticket's agent: --pane, the recorded pane,
// the agent running in the worktree, then the ticket tab's first pane. It
// opens a tab when there is none.
func (c *Ctx) pickPane(id, path, want string, st map[string]string, preset config.Preset) (tab, pane string, err error) {
	agents, err := c.H.Agents(c.Ctx)
	if err != nil {
		return "", "", herdrErr(err)
	}
	inPath := herdr.AgentsInPath(agents, path)
	switch {
	case want != "":
		pane = want
	case st["pane"] != "" && c.H.PaneExists(c.Ctx, st["pane"]):
		pane = st["pane"]
	case len(inPath) > 1:
		ids := make([]string, len(inPath))
		for i, a := range inPath {
			ids[i] = a.PaneID
		}
		return "", "", apperr.New(apperr.AmbiguousPane, "%d agents run in %s; pick one with --pane (%s)", len(inPath), path, strings.Join(ids, ", ")).
			WithHint("pass --pane %s", ids[0]).With("panes", ids)
	case len(inPath) == 1:
		pane = inPath[0].PaneID
	}
	if pane == "" {
		tabs, err := c.H.Tabs(c.Ctx)
		if err != nil {
			return "", "", herdrErr(err)
		}
		mine := herdr.TicketTabs(tabs, id)
		if len(mine) > 1 {
			return "", "", apperr.New(apperr.AmbiguousPane, "%d tabs belong to %s; pick a pane with --pane", len(mine), id).WithHint("pass --pane PANE")
		}
		if len(mine) == 1 {
			panes, err := c.H.Panes(c.Ctx)
			if err != nil {
				return "", "", herdrErr(err)
			}
			for _, p := range panes {
				if p.TabID == mine[0].TabID {
					pane = p.PaneID
					break
				}
			}
		}
	}
	if pane != "" {
		p, err := c.H.PaneGet(c.Ctx, pane)
		if err != nil {
			return "", "", herdrErr(err)
		}
		if !herdr.Inside(p.Cwd, path) {
			return "", "", apperr.New(apperr.PaneOutsideWorktree, "pane %s is in %s, not in %s", pane, p.Cwd, path)
		}
		if pane == c.Getenv("HERDR_PANE_ID") {
			return "", "", apperr.New(apperr.PaneIsSelf, "pane %s is the one running tkt", pane)
		}
		return p.TabID, pane, nil
	}
	tab, pane, err = c.openTab(path, c.Cfg.TabLabel(id, preset.Name))
	if err != nil || c.Dry {
		return tab, pane, err
	}
	c.Agent.WaitPaneFree(c.Ctx, pane, 40)
	return tab, pane, nil
}
