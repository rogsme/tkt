package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/prompt"
	"gitlab.com/rogs/tkt/internal/runner"
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
	// Mode is how a ticket's start goes on: manual, auto (tkt auto takes
	// over once you approve the plan), auto-plan or yolo; "" takes [start]
	// mode. Every mode but manual starts a tkt auto loop after the start.
	Mode string
	// Unattended adds config.UnattendedLine to the prompt, for a tkt auto
	// that approves the plan itself. Autonomous adds config.AutonomousLine
	// instead, for tkt auto --yolo, where nobody answers a question either.
	Unattended bool
	Autonomous bool
	// chore names the chore tkt chore run starts a run of, in the worktree
	// that Scratch names, with Prompt as its prompt. Unlike a scratch
	// worktree's, a run's agent gets its prompt, its tabs a TMPDIR, and its
	// start goes into the history.
	chore string
}

// StartResult is what tkt start did.
type StartResult struct {
	Ticket          string `json:"ticket"`
	Scratch         string `json:"scratch,omitempty"`
	Worktree        string `json:"worktree"`
	WorktreeCreated bool   `json:"worktree_created"`
	Tab             string `json:"tab"`
	Pane            string `json:"pane"`
	Agent           string `json:"agent"`
	Preset          string `json:"preset"`
	Harness         string `json:"harness"`
	Session         string `json:"session"`
	Stage           string `json:"stage"`
	Continued       bool   `json:"continued"`
	Resent          bool   `json:"resent"`
	// Mode is how the ticket goes on: manual, or the tkt auto mode whose
	// loop the start started (auto, auto-plan or yolo); "" for a worktree
	// with no ticket.
	Mode    string   `json:"mode"`
	DryRun  bool     `json:"dry_run"`
	Actions []Action `json:"actions"`
}

func (r *StartResult) Text(w io.Writer) {
	name := r.Ticket
	if r.Scratch != "" {
		name = r.Scratch
	}
	fmt.Fprintf(w, "%s  %s  tab %s  agent %s (%s, %s)\n", name, r.Worktree, r.Tab, r.Agent, r.Preset, r.Harness)
	switch r.Mode {
	case config.ModeAuto:
		fmt.Fprintf(w, "%s  tkt auto takes over once you approve the plan\n", name)
	case config.ModeAutoPlan:
		fmt.Fprintf(w, "%s  tkt auto approves the plan and drives it until it is reviewed clean\n", name)
	case config.ModeYolo:
		fmt.Fprintf(w, "%s  tkt auto carries it to the merge, with nobody watching\n", name)
	}
}

// Start creates or reuses a ticket's worktree, opens its tab, starts an agent
// and sends it the prompt. A scratch start has no ticket and sends nothing.
func (a *App) Start(ctx context.Context, o StartOpts) (*StartResult, error) {
	// named is a worktree with no ticket: a scratch one, or a chore's run.
	named := o.Scratch != ""
	scratch := named && o.chore == ""
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
	case named:
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
	mode := ""
	if !named {
		if mode, err = c.startMode(o.Mode); err != nil {
			return nil, err
		}
		switch mode {
		case config.ModeAutoPlan:
			o.Unattended = true
		case config.ModeYolo:
			if err := c.yoloAllowed(id); err != nil {
				return nil, err
			}
			o.Autonomous = true
		}
	}
	preset := c.Cfg.Default()
	if o.As != "" {
		if preset, err = c.Cfg.Preset(o.As); err != nil {
			return nil, err
		}
	}
	name := prompt.AgentName(id)
	again, stage, busy := "tkt start "+id, "start", "use tkt run or tkt resume, or close it"
	switch {
	case scratch:
		again, stage, busy = "tkt start --scratch "+id, "", "close it"
	case named:
		again, stage = "tkt chore run "+o.chore, stageChore
	case !o.Prompt.Set:
		text, _ = c.stageLine("start", id)
	}
	switch {
	case named:
	case o.Autonomous:
		text = withLine(text, config.AutonomousLine)
	case o.Unattended:
		text = withLine(text, config.UnattendedLine)
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
	if named {
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
	case len(inPath) > 0 && named:
		return nil, apperr.New(apperr.AgentRunning, "an agent already runs in %s (pane %s)", path, inPath[0].PaneID).
			WithHint("switch to pane %s", inPath[0].PaneID)
	case len(inPath) > 0:
		return nil, apperr.New(apperr.AgentRunning, "an agent already runs in %s (pane %s); use tkt run or tkt resume", path, inPath[0].PaneID).
			WithHint("tkt run %s STAGE, or tkt resume %s", id, id)
	default:
		own := ts
		if scratch {
			own = nil
		}
		if tab, pane, err = c.startTab(id, path, preset, busy, own); err != nil {
			return nil, err
		}
		c.save(ts, "ticket", id, "preset", preset.Name, "harness", h, "stage", stage, "tab", tab, "pane", pane, "agent", name, "status", "tab")
		if o.chore != "" {
			c.save(ts, "chore", o.chore)
		}
		if !scratch && st["started"] == "" {
			c.save(ts, "started", c.Now().UTC().Format(history.TimeFormat))
			c.record(history.Event{Ticket: id, Event: history.Started, Chore: o.chore})
			c.forgetUnblocked(id)
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
			if !named {
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
	if mode != "" {
		res.Mode = c.startAuto(id, mode, ts)
	}
	res.Actions = c.Plan
	return res, nil
}

// startMode is the mode a ticket's start takes: the flag's, else [start]
// mode. TKT_AUTO=off keeps every start manual, as the tests need.
func (c *Ctx) startMode(flag string) (string, error) {
	switch {
	case flag == config.ModeManual, flag == config.ModeAuto, flag == config.ModeAutoPlan, flag == config.ModeYolo:
		return flag, nil
	case flag != "":
		return "", apperr.New(apperr.Usage, "unknown start mode '%s'; it is manual, auto, auto-plan or yolo", flag)
	case c.Getenv("TKT_AUTO") == "off":
		return config.ModeManual, nil
	}
	return c.Cfg.Start.Mode, nil
}

// startAuto starts the tkt auto loop that drives a ticket tkt start just
// started, in a session of its own, so it outlives the start. In auto mode
// the loop waits for you to approve the plan, in the viewer or with tkt
// approve, and drives the ticket from there; it never approves a plan
// itself. It returns the mode the ticket goes on in: manual where tkt auto
// cannot run, as on a forge other than GitHub.
func (c *Ctx) startAuto(id, mode string, ts *ticketState) string {
	if mode == config.ModeManual {
		return mode
	}
	if c.LC.RequireGitHub("auto") != nil || !found("gh") {
		c.UI.Info("%s stays manual: tkt auto needs GitHub and gh", id)
		return config.ModeManual
	}
	self, err := os.Executable()
	if err != nil {
		c.UI.Warn("%s stays manual: %v", id, err)
		return config.ModeManual
	}
	argv := autoArgv(self, c.Repo.Root, id, mode)
	if c.Dry {
		c.Would(argv)
		return mode
	}
	if mode == config.ModeYolo {
		// A yolo loop that attaches to a started ticket needs its mark.
		c.save(ts, "yolo", "1")
	}
	sp, ok := c.R.(runner.Spawner)
	if !ok {
		return config.ModeManual
	}
	dir := filepath.Join(c.Repo.Common, "tkt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.UI.Warn("%s stays manual: %v", id, err)
		return config.ModeManual
	}
	if err := sp.Spawn(c.Ctx, runner.Cmd{Argv: argv}, autoEnv(os.Environ()), c.Repo.Root, filepath.Join(dir, "auto-"+id+".log")); err != nil {
		c.UI.Warn("%s stays manual: cannot start tkt auto: %v", id, err)
		return config.ModeManual
	}
	c.save(ts, "auto_spawned", c.Now().UTC().Format(history.TimeFormat))
	return mode
}

// respawnAuto starts again the tkt auto loop of a ticket marked for auto
// whose loop is gone, in the mode its state names, unless tkt started one
// within autoRespawn. It reports whether it started one.
func (c *Ctx) respawnAuto(id string, ts *ticketState) bool {
	if at, err := time.Parse(time.RFC3339, ts.st["auto_spawned"]); err == nil && c.Now().Sub(at) < autoRespawn {
		return false
	}
	mode := config.ModeAuto
	switch ts.st["auto"] {
	case autoPlan:
		mode = config.ModeAutoPlan
	case autoYolo:
		mode = config.ModeYolo
	}
	if c.startAuto(id, mode, ts) == config.ModeManual {
		return false
	}
	c.UI.Info("%s: its tkt auto loop was gone, so tkt watch started it again", id)
	c.record(history.Event{Ticket: id, Event: history.Watch, Action: actAuto})
	if c.Dry {
		return true
	}
	c.notify(id, notice{"tkt auto restarted", "its loop was gone, as after a reboot; tkt auto --stop " + id + " turns it off", "none"})
	return true
}

// autoEnv is the environment a tkt auto loop starts with: this one, in the
// same herdr workspace, where it opens the ticket's tabs, but out of the
// pane and tab tkt start ran in, and out of a ticket's own TMPDIR.
func autoEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "HERDR_PANE_ID="), strings.HasPrefix(kv, "HERDR_TAB_ID="):
		case strings.HasPrefix(kv, "TMPDIR="):
			out = append(out, "TMPDIR="+tmpRoot(strings.TrimPrefix(kv, "TMPDIR=")))
		default:
			out = append(out, kv)
		}
	}
	return out
}

// autoArgv is the tkt auto command for a mode.
func autoArgv(self, root, id, mode string) []string {
	argv := []string{self, "-C", root, "auto", id, "--notify"}
	switch mode {
	case config.ModeAutoPlan:
		argv = append(argv, "--auto-plan")
	case config.ModeYolo:
		argv = append(argv, "--yolo")
	}
	return argv
}

// startTab reuses the ticket's idle tab in this workspace, or opens one. busy
// is the advice for a tab that is in use; ts is the ticket's state, or nil
// for a scratch worktree.
func (c *Ctx) startTab(id, path string, preset config.Preset, busy string, ts *ticketState) (tab, pane string, err error) {
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
	return c.openTab(id, path, c.Cfg.TabLabel(id, preset.Name), ts)
}

// openTab opens a tab for the ticket, or reports it in a dry run. The tab's
// TMPDIR is the ticket's own directory; a scratch worktree, with no ts, gets
// none.
func (c *Ctx) openTab(id, path, label string, ts *ticketState) (tab, pane string, err error) {
	ws := c.Getenv("HERDR_WORKSPACE_ID")
	var env []string
	if ts != nil {
		if d := c.ticketTmp(id, ts); d != "" {
			env = append(env, "TMPDIR="+d)
		}
	}
	if c.Dry {
		c.Would(herdr.TabCreateArgv(ws, path, label, env...))
		return "<tab>", "<pane>", nil
	}
	tab, pane, err = c.H.TabCreate(c.Ctx, ws, path, label, env...)
	return tab, pane, herdrErr(err)
}

// openSplit splits the ticket's pane beside to its right, for a session
// that works next to the ticket's own agent, or reports it in a dry run. The
// split's TMPDIR is the ticket's own directory, as a tab's is.
func (c *Ctx) openSplit(id, path, beside string, ts *ticketState) (string, error) {
	var env []string
	if d := c.ticketTmp(id, ts); d != "" {
		env = append(env, "TMPDIR="+d)
	}
	if c.Dry {
		c.Would(herdr.PaneSplitArgv(beside, path, env...))
		return "<pane>", nil
	}
	pane, err := c.H.PaneSplit(c.Ctx, beside, path, env...)
	return pane, herdrErr(err)
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

// withLine adds a line to a prompt: after a space, or after a blank line
// when the prompt runs over several lines.
func withLine(text, line string) string {
	sep := " "
	if strings.Contains(text, "\n") {
		sep = "\n\n"
	}
	return strings.TrimRight(text, "\n") + sep + line
}
