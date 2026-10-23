package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

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

// SuggestOpts are tkt suggest's arguments: how many tickets to suggest, the
// preset suggest-tickets runs on, and whether to wait for its picks, at most
// Timeout.
type SuggestOpts struct {
	Count   int
	As      string
	Wait    bool
	Timeout time.Duration
	Dry     bool
}

// SuggestPick is a ticket suggest-tickets picked. Model is the Model: value
// its Model routing names, or "" without model routing, and Preset the
// first preset that serves it, for tkt start --as; "" means the default.
type SuggestPick struct {
	Ticket string `json:"ticket"`
	Model  string `json:"model"`
	Preset string `json:"preset"`
}

// SuggestResult is what tkt suggest did: the session it started in the
// suggest tab at the repository's root. With --wait, Status is the agent's
// state when the wait ended, and once it finished, LastStatus, Report (what
// it said) and Picks come from its last turn.
type SuggestResult struct {
	Count      int           `json:"count"`
	Tab        string        `json:"tab"`
	Pane       string        `json:"pane"`
	Preset     string        `json:"preset"`
	Harness    string        `json:"harness"`
	Session    string        `json:"session"`
	Status     string        `json:"status"`
	LastStatus string        `json:"last_status"`
	Report     string        `json:"report"`
	Picks      []SuggestPick `json:"picks"`
	DryRun     bool          `json:"dry_run"`
	Actions    []Action      `json:"actions"`
}

func (r *SuggestResult) Text(w io.Writer) {
	switch {
	case r.Status == herdr.Blocked:
		fmt.Fprintf(w, "suggest-tickets asks a question in pane %s; answer it there\n", r.Pane)
	case r.LastStatus != "" && len(r.Picks) == 0:
		fmt.Fprintf(w, "suggest-tickets picked nothing to start (Status: %s); tkt show is in pane %s\n", r.LastStatus, r.Pane)
	case len(r.Picks) > 0:
		for _, p := range r.Picks {
			fmt.Fprintf(w, "%s  %s\n", p.Ticket, startCommand(p))
		}
	default:
		fmt.Fprintf(w, "suggest-tickets picks %d in pane %s (%s, %s)\n", r.Count, r.Pane, r.Preset, r.Harness)
	}
}

// startCommand is the tkt start command for a pick, on the preset its
// model needs.
func startCommand(p SuggestPick) string {
	if p.Preset == "" {
		return "tkt start " + p.Ticket
	}
	return "tkt start " + p.Ticket + " --as " + p.Preset
}

// suggestLock is the lock one tkt suggest per repository holds.
const suggestLock = "suggest"

// suggestStatePath is the file where tkt suggest keeps its tab, pane and
// session, next to tkt watch's in the repository's git directory.
func suggestStatePath(common string) string { return filepath.Join(common, "tkt", "suggest") }

// Suggest has suggest-tickets pick the next tickets to start, ones that can
// run side by side: it starts a fresh session in the suggest tab at the
// repository's root, on the [suggest] preset, and sends the suggest line.
// suggest-tickets only reads. With Wait, it waits for the agent and reads
// the picks from its Suggested: line, each with the preset that serves the
// model its Model routing names.
func (a *App) Suggest(ctx context.Context, o SuggestOpts) (*SuggestResult, error) {
	if o.Count == 0 {
		o.Count = 3
	}
	if o.Count < 1 || o.Count > 10 {
		return nil, apperr.New(apperr.Usage, "tkt suggest picks 1 to 10 tickets, not %d", o.Count).WithHint("tkt suggest 3")
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
	res := &SuggestResult{Count: o.Count, Preset: preset.Name, Harness: h, Picks: []SuggestPick{}, DryRun: c.Dry, Actions: []Action{}}
	res.Tab, res.Pane, err = c.suggestPane(ts, preset)
	if err != nil {
		return nil, err
	}
	sid, err := c.newSession(c.Repo.Root, "suggest", preset)
	if err != nil {
		return nil, err
	}
	res.Session = sid
	c.save(ts, "tab", res.Tab, "pane", res.Pane, "preset", preset.Name, "harness", h, "session", sid, "status", "launching",
		"started", c.Now().UTC().Format(history.TimeFormat))
	// herdr names agents across every repository it serves.
	name := prompt.AgentName(suggestLock + "-" + filepath.Base(c.Repo.Root))
	switch out, err := c.Agent.Launch(c.Ctx, res.Pane, name, h, harness.LaunchArgs(h, harness.New, sid, preset, c.Repo.Root)); out {
	case agent.NotReady:
		c.save(ts, "status", "not_ready")
		return nil, c.screenErr(apperr.New(apperr.AgentNotReady,
			"the agent stopped at a startup dialog in pane %s; answer it, then send the suggest line yourself", res.Pane).
			WithHint("answer the dialog in pane %s, then send the suggest line there", res.Pane), res.Pane)
	case agent.Failed:
		return nil, apperr.New(apperr.AgentLaunchFailed, "%s", err.Error())
	}
	switch out, _, err := c.Agent.Deliver(c.Ctx, res.Pane, config.SuggestLine(o.Count), h, sid, c.Repo.Root); out {
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
	if !o.Wait || c.Dry {
		return res, nil
	}
	return res, c.waitSuggest(res, ts, o.Timeout)
}

// suggestPane finds the pane for suggest-tickets: the suggest tab's, with
// its idle agent gone, or a new suggest tab at the repository's root.
func (c *Ctx) suggestPane(ts *ticketState, preset config.Preset) (tab, pane string, err error) {
	pane = ts.st["pane"]
	if pane != "" && c.H.PaneExists(c.Ctx, pane) {
		p, err := c.H.PaneGet(c.Ctx, pane)
		if err != nil {
			return "", "", herdrErr(err)
		}
		switch old, ok := c.H.AgentOnPane(c.Ctx, pane); {
		case ok && (old.Status == herdr.Working || old.Status == herdr.Blocked):
			return "", "", apperr.New(apperr.AgentBusy, "suggest-tickets is %s in pane %s; let it finish, or answer it there", old.Status, pane).
				WithHint("check pane %s", pane).With("pane", pane).With("status", old.Status)
		case ok:
			if !c.Agent.Exit(c.Ctx, pane) {
				return "", "", apperr.New(apperr.AgentExitFailed, "the agent in pane %s did not exit; nothing was sent", pane).
					WithHint("check pane %s, close the agent there, then rerun tkt suggest", pane)
			}
		case !c.Dry && !c.H.PaneIsShell(c.Ctx, pane):
			return "", "", apperr.New(apperr.PaneBusy, "pane %s is not at a shell prompt", pane).
				WithHint("finish what runs in pane %s, then rerun tkt suggest", pane)
		}
		return p.TabID, pane, nil
	}
	label := suggestLock
	if preset.Name != c.Cfg.Default().Name {
		label += " (" + preset.Name + ")"
	}
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

// waitSuggest waits for suggest-tickets to finish or ask, and reads its
// picks once it finished.
func (c *Ctx) waitSuggest(res *SuggestResult, ts *ticketState, timeout time.Duration) error {
	wctx := c.Ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		wctx, cancel = context.WithTimeout(c.Ctx, timeout+5*time.Second)
		defer cancel()
	}
	status, err := c.H.Wait(wctx, res.Pane, []string{herdr.Idle, herdr.Blocked, herdr.Done}, timeout.Milliseconds())
	switch herdr.CodeOf(err) {
	case "":
		if err != nil {
			return herdrErr(err)
		}
	case "timeout":
		return apperr.New(apperr.WaitTimeout, "suggest-tickets did not finish within %s", timeout).
			WithHint("check pane %s", res.Pane).With("pane", res.Pane)
	case "agent_not_found":
		return apperr.New(apperr.AgentMissing, "the agent in pane %s is gone", res.Pane).WithHint("tkt suggest")
	default:
		return herdrErr(err)
	}
	res.Status = status
	if status == herdr.Blocked {
		return nil
	}
	ag, _ := c.H.AgentOnPane(c.Ctx, res.Pane)
	turn, _ := c.lastTurn(c.Ctx, &ag, ts.st, c.Repo.Root)
	res.LastStatus, res.Report = agent.LastStatus(turn.All), strings.TrimSpace(or(turn.Final, turn.All))
	res.Picks = c.suggested(turn.All)
	return nil
}

// suggested reads the picks from suggest-tickets' Suggested: line, such as
// "Suggested: ACM-14 (frontier), ACM-21 (acme-small-2)", each with the
// first preset other than the default that serves its Model: value.
func (c *Ctx) suggested(text string) []SuggestPick {
	out := []SuggestPick{}
	for _, s := range contract.Suggestions(text, c.LC.RE) {
		pick := SuggestPick{Ticket: s.Ticket, Model: s.Model}
		if pick.Model != "" {
			if pr, err := c.Cfg.Pick("", "Model: "+pick.Model, false); err == nil && pr.Name != c.Cfg.Default().Name {
				pick.Preset = pr.Name
			}
		}
		out = append(out, pick)
	}
	return out
}
