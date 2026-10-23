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
	"gitlab.com/rogs/tkt/internal/prompt"
)

// ResumeOpts are tkt resume's arguments.
type ResumeOpts struct {
	ID      string
	Session string
	As      string
	Focus   bool
	Dry     bool
}

// ResumeResult is what tkt resume did.
type ResumeResult struct {
	Ticket         string   `json:"ticket"`
	AlreadyRunning bool     `json:"already_running"`
	Status         string   `json:"status,omitempty"`
	Harness        string   `json:"harness"`
	Session        string   `json:"session"`
	Tab            string   `json:"tab"`
	Pane           string   `json:"pane"`
	Agent          string   `json:"agent"`
	Preset         string   `json:"preset"`
	DryRun         bool     `json:"dry_run"`
	Actions        []Action `json:"actions"`
}

func (r *ResumeResult) Text(w io.Writer) {
	if r.AlreadyRunning {
		return
	}
	fmt.Fprintf(w, "%s  resumed %s session %s in pane %s\n", r.Ticket, r.Harness, r.Session, r.Pane)
}

// Resume relaunches the ticket's newest session when no agent runs for it,
// and sends nothing.
func (a *App) Resume(ctx context.Context, o ResumeOpts) (*ResumeResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
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
	st := ts.st
	agents, err := c.H.Agents(c.Ctx)
	if err != nil {
		return nil, herdrErr(err)
	}
	res := &ResumeResult{Ticket: id, DryRun: c.Dry}
	if running := herdr.AgentsInPath(agents, path); len(running) > 0 {
		r := running[0]
		a.UI.Info("%s already has an agent in pane %s (%s, %s)", id, r.PaneID, r.Kind, r.Status)
		if o.Focus && !c.Dry {
			if err := c.H.TabFocus(c.Ctx, r.TabID); err != nil {
				return nil, herdrErr(err)
			}
		}
		res.AlreadyRunning, res.Pane, res.Tab, res.Harness, res.Status = true, r.PaneID, r.TabID, r.Kind, r.Status
		res.Agent, res.Session = r.NameOr(""), r.SessionID()
		return res, nil
	}
	var preset config.Preset
	switch {
	case o.As != "":
		if preset, err = c.Cfg.Preset(o.As); err != nil {
			return nil, err
		}
	case c.Cfg.Has(st["preset"]):
		preset, _ = c.Cfg.Preset(st["preset"])
	}
	want := ""
	if o.As != "" {
		want = preset.Harness
	}
	sid, h := o.Session, ""
	switch {
	case sid != "":
		h = config.Claude
		if strings.HasPrefix(sid, "ses") {
			h = config.OpenCode
		}
	case st["session"] != "" && st["harness"] != "" && (want == "" || st["harness"] == want):
		sid, h = st["session"], st["harness"]
	default:
		cl, cok := harness.ClaudeNewest(harness.ClaudeProjectDir(a.Getenv, path))
		oc, ook := c.OC.Newest(c.Ctx, path)
		if want == config.Claude {
			ook = false
		}
		if want == config.OpenCode {
			cok = false
		}
		switch {
		case cok && (!ook || cl.UpdatedMs >= oc.UpdatedMs):
			sid, h = cl.ID, config.Claude
		case ook:
			sid, h = oc.ID, config.OpenCode
		}
	}
	if sid == "" {
		return nil, apperr.New(apperr.SessionMissing, "found no session for %s; start one with tkt run %s STAGE", id, id).
			WithHint("tkt run %s STAGE", id)
	}
	if preset.Name == "" || preset.Harness != h {
		p, ok := c.Cfg.FirstForHarness(h)
		if !ok {
			return nil, apperr.New(apperr.NoPresetForHarness, "no preset uses %s; add one to %s", h, c.Cfg.Source)
		}
		preset = p
	}
	if err := c.needTools(h); err != nil {
		return nil, err
	}
	if h == config.Claude {
		for _, ag := range agents {
			if ag.SessionID() == sid {
				return nil, apperr.New(apperr.SessionInUse, "session %s already runs in pane %s", sid, ag.PaneID).
					WithHint("switch to pane %s", ag.PaneID)
			}
		}
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	tab, pane, err := c.pickPane(id, path, "", ts, preset)
	if err != nil {
		return nil, err
	}
	if !c.Dry && !c.Agent.PaneFree(c.Ctx, pane) {
		return nil, apperr.New(apperr.PaneBusy, "pane %s is busy", pane).WithHint("finish what runs in pane %s", pane)
	}
	name := prompt.AgentName(id)
	out, err := c.Agent.Launch(c.Ctx, pane, name, h, harness.LaunchArgs(h, harness.Resume, sid, preset, path))
	switch out {
	case agent.NotReady:
		return nil, c.screenErr(apperr.New(apperr.AgentNotReady, "the agent stopped at a startup dialog in pane %s; answer it there", pane).
			WithHint("answer the dialog in pane %s", pane), pane)
	case agent.Failed:
		return nil, apperr.New(apperr.AgentLaunchFailed, "%s", err.Error())
	}
	c.save(ts, "ticket", id, "preset", preset.Name, "harness", h, "tab", tab, "pane", pane, "agent", name, "session", sid, "status", "resumed")
	if o.Focus && !c.Dry {
		if err := c.H.TabFocus(c.Ctx, tab); err != nil {
			return nil, herdrErr(err)
		}
	}
	res.Harness, res.Session, res.Tab, res.Pane, res.Agent, res.Preset, res.Actions = h, sid, tab, pane, name, preset.Name, c.Plan
	return res, nil
}
