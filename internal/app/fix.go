package app

import (
	"slices"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/prompt"
	"gitlab.com/rogs/tkt/internal/state"
)

// The ticket state's keys for the session that makes a small fix in a split
// beside the ticket's agent: its pane, agent, session and harness, the
// stage it runs, how its launch went, and when it started. The ticket's own
// pane, agent and session stay as they are.
const (
	keyFixPane    = "fix_pane"
	keyFixAgent   = "fix_agent"
	keyFixSession = "fix_session"
	keyFixHarness = "fix_harness"
	keyFixStage   = "fix_stage"
	keyFixStatus  = "fix_status"
	keyFixStarted = "fix_started"
)

// isFixStage says a stage is a small fix, which runs in a split beside the
// ticket's agent: a failed sync, a conflict with the base, or failing
// checks.
func isFixStage(stage string) bool { return slices.Contains(config.FixStages, stage) }

// fixIn starts a fresh session for a small fix in a split beside the
// ticket's agent, and sends it the stage line. The ticket's agent keeps its
// pane and its conversation, so the work after the fix goes on where it
// was. An earlier fix's split is used again once its agent is done. The
// caller holds the ticket's lock.
func (c *Ctx) fixIn(id, path, stage, text string, ts *ticketState, preset config.Preset, own string) (*RunResult, error) {
	st := ts.st
	h, name := preset.Harness, prompt.AgentName(id)+"-fix"
	res := &RunResult{Ticket: id, Stage: stage, Tab: st["tab"], Agent: name, Preset: preset.Name, Harness: h, DryRun: c.Dry}
	pane := st[keyFixPane]
	if pane != "" && !c.Dry && !c.H.PaneExists(c.Ctx, pane) {
		pane = ""
	}
	if pane != "" {
		if old, ok := c.H.AgentOnPane(c.Ctx, pane); ok {
			if old.Status != herdr.Idle && old.Status != herdr.Done {
				return nil, apperr.New(apperr.AgentBusy, "the fix in pane %s is %s; wait for it or answer it first", pane, old.Status).
					WithHint("check pane %s", pane).With("pane", pane).With("status", old.Status)
			}
			res.ReplacedSession = st[keyFixSession]
			if !c.Agent.Exit(c.Ctx, pane) {
				return nil, apperr.New(apperr.AgentExitFailed, "the agent in pane %s did not exit; nothing was sent", pane).
					WithHint("check pane %s, close the agent there, then rerun tkt run", pane)
			}
		}
	} else {
		var err error
		if pane, err = c.fixPane(id, path, ts, preset); err != nil {
			return nil, err
		}
	}
	res.Pane = pane
	sid, err := c.newSession(path, id+" "+stage, preset)
	if err != nil {
		return nil, err
	}
	res.Session = sid
	if !c.Cfg.Has(st["preset"]) {
		c.save(ts, "preset", own)
	}
	c.save(ts, "ticket", id, keyFixPane, pane, keyFixAgent, name, keyFixSession, sid, keyFixHarness, h, keyFixStage, stage, keyFixStatus, "launching",
		keyFixStarted, c.Now().UTC().Format(history.TimeFormat))
	out, err := c.Agent.Launch(c.Ctx, pane, name, h, harness.LaunchArgs(h, harness.New, sid, preset, path))
	switch out {
	case agent.NotReady:
		c.save(ts, keyFixStatus, "not_ready")
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
		c.save(ts, keyFixStatus, "prompt_uncertain")
		return nil, c.screenErr(apperr.New(apperr.PromptUncertain,
			"cannot confirm the agent received the stage line; check pane %s before sending it again", pane).
			WithHint("check pane %s before sending the stage line again", pane), pane)
	case agent.Failed:
		return nil, sendErr("sending the stage line failed", err)
	}
	c.save(ts, keyFixStatus, "submitted")
	c.record(history.Event{Ticket: id, Event: history.Stage, Stage: stage})
	res.Actions = c.Plan
	return res, nil
}

// fixPane opens the pane a fix runs in: a split beside the ticket's agent.
// With no pane of the ticket's left, it takes the one pickPane finds or
// opens, and splits it only when an agent runs there.
func (c *Ctx) fixPane(id, path string, ts *ticketState, preset config.Preset) (string, error) {
	beside := ts.st["pane"]
	if beside == "" || !c.Dry && !c.H.PaneExists(c.Ctx, beside) {
		_, p, err := c.pickPane(id, path, "", ts, preset)
		if err != nil {
			return "", err
		}
		if _, ok := c.H.AgentOnPane(c.Ctx, p); !ok {
			return p, nil
		}
		beside = p
	}
	pane, err := c.openSplit(id, path, beside, ts)
	if err != nil || c.Dry {
		return pane, err
	}
	c.Agent.WaitPaneFree(c.Ctx, pane, 40)
	return pane, nil
}

// fixState is the ticket state as lastTurn reads it for the fix's session,
// with what tkt's OpenCode plugin saw in it.
func fixState(st state.State) state.State {
	return state.State{"harness": st[keyFixHarness], "session": st[keyFixSession],
		keyOCSession: st[fixOC+keyOCSession], keyOCStatuses: st[fixOC+keyOCStatuses], keyOCAt: st[fixOC+keyOCAt]}
}

// fixAgent finds the fix's agent among the agents, by its pane.
func fixAgent(agents []herdr.Agent, st state.State) (herdr.Agent, bool) {
	pane := st[keyFixPane]
	if pane == "" {
		return herdr.Agent{}, false
	}
	i := slices.IndexFunc(agents, func(a herdr.Agent) bool { return a.PaneID == pane })
	if i < 0 {
		return herdr.Agent{}, false
	}
	return agents[i], true
}

// withoutFix leaves the fix's agent out of the agents in a worktree, so
// nothing takes it for the ticket's own.
func withoutFix(agents []herdr.Agent, st state.State) []herdr.Agent {
	pane := st[keyFixPane]
	if pane == "" {
		return agents
	}
	return slices.DeleteFunc(slices.Clone(agents), func(a herdr.Agent) bool { return a.PaneID == pane })
}

// reapFix closes a fix's split once its session ended with
// READY_FOR_REVIEW, and forgets a split that is gone. A fix that stopped
// otherwise, such as with BLOCKED, keeps its split, so you can read why.
// tkt watch and tkt auto call it; tkt ls only reads.
func (c *Ctx) reapFix(id, path string, ts *ticketState) {
	pane := ts.st[keyFixPane]
	if pane == "" || c.Dry {
		return
	}
	if !c.H.PaneExists(c.Ctx, pane) {
		c.clearFix(ts)
		return
	}
	ag, ok := c.H.AgentOnPane(c.Ctx, pane)
	if !ok || ag.Status != herdr.Idle && ag.Status != herdr.Done {
		return
	}
	turn, _ := c.lastTurn(c.Ctx, &ag, fixState(ts.st), path)
	if agent.LastStatus(turn.All) != contract.StatusReadyForReview {
		return
	}
	if err := c.H.PaneClose(c.Ctx, pane); err != nil {
		c.UI.Warn("%s: cannot close the fix's pane %s: %v", id, pane, err)
		return
	}
	c.record(history.Event{Ticket: id, Event: history.Stage, Stage: ts.st[keyFixStage], Stop: contract.StatusReadyForReview})
	c.clearFix(ts)
}

// clearFix forgets the fix's session.
func (c *Ctx) clearFix(ts *ticketState) {
	c.save(ts, keyFixPane, "", keyFixAgent, "", keyFixSession, "", keyFixHarness, "", keyFixStage, "", keyFixStatus, "", keyFixStarted, "")
}

// syncFixStage is the stage that takes over a failed sync of a kind: resolve
// for a conflict, sync-fix for anything else.
func syncFixStage(kind string) string {
	if kind == syncConflict {
		return "resolve"
	}
	return "sync-fix"
}
