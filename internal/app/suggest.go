package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/prompt"
	"gitlab.com/rogs/tkt/internal/state"
)

// SuggestOpts are tkt suggest's arguments: what to ask suggest-tickets,
// either Request in your own words or Count tickets, and the preset it runs
// on. Show reads the last run's answer instead of starting one.
type SuggestOpts struct {
	Count   int
	Request string
	As      string
	Show    bool
	Dry     bool
}

// SuggestResult is what tkt suggest did: the session it started in the
// suggest tab at the repository's root, and the prompt it sent. The answer
// stays in that tab, for you to read and talk over with the agent. Count is
// 0 for a request. With --show, it is the last run's session, and Message
// is what its agent said since the last prompt, read the way tkt show
// reads a ticket's; Source and At are as in tkt show.
type SuggestResult struct {
	Count       int      `json:"count"`
	Prompt      string   `json:"prompt"`
	Tab         string   `json:"tab"`
	Pane        string   `json:"pane"`
	Preset      string   `json:"preset"`
	Harness     string   `json:"harness"`
	Session     string   `json:"session"`
	Started     string   `json:"started"`
	AgentStatus string   `json:"agent_status"`
	Source      string   `json:"source"`
	Message     string   `json:"message"`
	At          string   `json:"at"`
	DryRun      bool     `json:"dry_run"`
	Actions     []Action `json:"actions"`
	shown       bool
}

func (r *SuggestResult) Text(w io.Writer) {
	if r.shown {
		fmt.Fprintln(w, strings.TrimRight(r.Message, "\n"))
		return
	}
	if r.Count == 0 {
		fmt.Fprintf(w, "suggest-tickets has your request in pane %s (%s, %s); read its picks there\n", r.Pane, r.Preset, r.Harness)
		return
	}
	fmt.Fprintf(w, "suggest-tickets picks %d in pane %s (%s, %s); read its picks there\n", r.Count, r.Pane, r.Preset, r.Harness)
}

// suggestLock is the lock one tkt suggest per repository holds.
const suggestLock = "suggest"

// suggestStatePath is the file where tkt suggest keeps its tab, pane and
// session, next to tkt watch's in the repository's git directory.
func suggestStatePath(common string) string { return filepath.Join(common, "tkt", "suggest") }

// Suggest starts a fresh session in the suggest tab at the repository's
// root, on the [suggest] preset, and has suggest-tickets pick the next
// tickets to start: those your request asks for, or without one, Count
// tickets that can run side by side. tkt reads none of the answer: it is
// yours to weigh in the tab.
func (a *App) Suggest(ctx context.Context, o SuggestOpts) (*SuggestResult, error) {
	if o.Show {
		return a.suggestShow(ctx)
	}
	line := ""
	if r := strings.TrimSpace(o.Request); r != "" {
		line, o.Count = config.SuggestRequestLine(r), 0
	} else {
		if o.Count == 0 {
			o.Count = 3
		}
		if o.Count < 1 || o.Count > 10 {
			return nil, apperr.New(apperr.Usage, "tkt suggest picks 1 to 10 tickets, not %d", o.Count).WithHint("tkt suggest 3")
		}
		line = config.SuggestLine(o.Count)
	}
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	if err := c.needHerdr(); err != nil {
		return nil, err
	}
	preset := c.Cfg.Default()
	if as := or(o.As, c.Cfg.Suggest.As); as != "" {
		if preset, err = c.Cfg.Preset(as); err != nil {
			return nil, err
		}
	}
	h := preset.Harness
	if err := c.needTools(h); err != nil {
		return nil, err
	}
	if err := c.lockTicket(suggestLock); err != nil {
		return nil, err
	}
	ts := &ticketState{path: suggestStatePath(c.Repo.Common), st: state.Read(suggestStatePath(c.Repo.Common))}
	res := &SuggestResult{Count: o.Count, Prompt: line, Preset: preset.Name, Harness: h, DryRun: c.Dry, Actions: []Action{}}
	// herdr names agents across every repository it serves.
	name := prompt.AgentName(suggestLock + "-" + filepath.Base(c.Repo.Root))
	res.Tab, res.Pane, err = c.suggestPane(ts, preset, name)
	if err != nil {
		return nil, err
	}
	sid, err := c.newSession(c.Repo.Root, "suggest", preset)
	if err != nil {
		return nil, err
	}
	res.Session = sid
	c.save(ts, "tab", res.Tab, "pane", res.Pane, "preset", preset.Name, "harness", h, "session", sid, "status", "launching",
		"started", c.Now().UTC().Format(history.TimeFormat), "count", strconv.Itoa(o.Count), "prompt", line)
	switch out, err := c.Agent.Launch(c.Ctx, res.Pane, name, h, harness.LaunchArgs(h, harness.New, sid, preset, c.Repo.Root)); out {
	case agent.NotReady:
		c.save(ts, "status", "not_ready")
		return nil, c.screenErr(apperr.New(apperr.AgentNotReady,
			"the agent stopped at a startup dialog in pane %s; answer it, then send the suggest line yourself", res.Pane).
			WithHint("answer the dialog in pane %s, then send the suggest line there", res.Pane), res.Pane)
	case agent.Failed:
		return nil, apperr.New(apperr.AgentLaunchFailed, "%s", err.Error())
	}
	switch out, _, err := c.Agent.Deliver(c.Ctx, res.Pane, line, h, sid, c.Repo.Root); out {
	case agent.Uncertain:
		c.save(ts, "status", "prompt_uncertain")
		return nil, c.screenErr(apperr.New(apperr.PromptUncertain,
			"cannot confirm the agent received the suggest line; check pane %s before sending it again", res.Pane).
			WithHint("check pane %s before sending the suggest line again", res.Pane), res.Pane)
	case agent.Failed:
		return nil, sendErr("sending the suggest line failed", err)
	}
	c.save(ts, "status", "submitted")
	res.Actions = append(res.Actions, c.Plan...)
	return res, nil
}

// suggestShow reads what the last suggest run's agent said since its last
// prompt: from its session's record, or from its screen while it runs and
// tkt cannot read the record. It changes nothing, so a page can poll it.
func (a *App) suggestShow(ctx context.Context) (*SuggestResult, error) {
	c, err := a.begin(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	st := state.Read(suggestStatePath(c.Repo.Common))
	if st["session"] == "" {
		return nil, apperr.New(apperr.AgentMissing, "suggest-tickets has not run in this repository").WithHint("tkt suggest 3")
	}
	count, _ := strconv.Atoi(st["count"])
	res := &SuggestResult{Count: count, Prompt: st["prompt"], Tab: st["tab"], Pane: st["pane"], Preset: st["preset"], Harness: st["harness"],
		Session: st["session"], Started: st["started"], Actions: []Action{}, shown: true}
	agents, err := c.H.Agents(c.Ctx)
	if err != nil {
		return nil, herdrErr(err)
	}
	name := prompt.AgentName(suggestLock + "-" + filepath.Base(c.Repo.Root))
	var ag *herdr.Agent
	for i := range agents {
		if agents[i].NameOr("") == name {
			ag = &agents[i]
			res.Pane, res.AgentStatus = ag.PaneID, ag.Status
			if ag.Kind != "" {
				res.Harness = ag.Kind
			}
			if sid := ag.SessionID(); sid != "" {
				res.Session = sid
			}
			break
		}
	}
	turn, src := c.lastTurn(c.Ctx, ag, st, c.Repo.Root)
	if src == "" {
		return nil, apperr.New(apperr.AgentMissing, "no agent runs in the suggest tab and tkt cannot read its session").WithHint("tkt suggest 3")
	}
	res.Source, res.Message = src, turn.Final
	if src == "screen" {
		res.Message = turn.All
	}
	if !turn.At.IsZero() {
		res.At = turn.At.UTC().Format(history.TimeFormat)
	}
	return res, nil
}

// suggestPane finds the pane for suggest-tickets: the one its agent, found
// by name, runs in, or the suggest tab's while it is still that tab, at the
// repository's root, with a shell in it; else a new suggest tab there. herdr
// numbers panes again when its server restarts, so a kept pane id counts
// only after those checks. An idle agent there exits first.
func (c *Ctx) suggestPane(ts *ticketState, preset config.Preset, name string) (tab, pane string, err error) {
	agents, err := c.H.Agents(c.Ctx)
	if err != nil {
		return "", "", herdrErr(err)
	}
	var p herdr.Pane
	at := func(id string) (bool, error) {
		if id == "" || id == c.Getenv("HERDR_PANE_ID") || !c.H.PaneExists(c.Ctx, id) {
			return false, nil
		}
		if p, err = c.H.PaneGet(c.Ctx, id); err != nil {
			return false, herdrErr(err)
		}
		return p.Cwd == c.Repo.Root, nil
	}
	for _, ag := range agents {
		if ag.NameOr("") != name {
			continue
		}
		ok, err := at(ag.PaneID)
		switch {
		case err != nil:
			return "", "", err
		case !ok:
			return "", "", apperr.New(apperr.PaneBusy, "the agent %s runs in pane %s, outside %s; close it, then rerun tkt suggest", name, ag.PaneID, c.Repo.Root).
				WithHint("check pane %s", ag.PaneID)
		case ag.Status == herdr.Working || ag.Status == herdr.Blocked:
			return "", "", apperr.New(apperr.AgentBusy, "suggest-tickets is %s in pane %s; let it finish, or answer it there", ag.Status, ag.PaneID).
				WithHint("check pane %s", ag.PaneID).With("pane", ag.PaneID).With("status", ag.Status)
		case !c.Agent.Exit(c.Ctx, ag.PaneID):
			return "", "", apperr.New(apperr.AgentExitFailed, "the agent in pane %s did not exit; nothing was sent", ag.PaneID).
				WithHint("check pane %s, close the agent there, then rerun tkt suggest", ag.PaneID)
		}
		return p.TabID, ag.PaneID, nil
	}
	ok, err := at(ts.st["pane"])
	if err != nil {
		return "", "", err
	}
	if ok && p.TabID == ts.st["tab"] && (c.Dry || c.H.PaneIsShell(c.Ctx, p.PaneID)) {
		return p.TabID, p.PaneID, nil
	}
	label := c.Cfg.TabLabel(suggestLock, preset.Name)
	ws := c.Getenv("HERDR_WORKSPACE_ID")
	if c.Dry {
		c.Would(herdr.TabCreateArgv(ws, c.Repo.Root, label))
		return "<tab>", "<pane>", nil
	}
	tab, pane, err = c.H.TabCreate(c.Ctx, ws, c.Repo.Root, label)
	if err != nil {
		return "", "", herdrErr(err)
	}
	c.Agent.WaitPaneFree(c.Ctx, pane, 40)
	return tab, pane, nil
}
