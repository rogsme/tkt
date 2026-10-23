package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/lock"
)

// AutoOpts are tkt auto's arguments. MaxRounds below 0 and Poll of 0 take
// the settings' values.
type AutoOpts struct {
	ID          string
	ApprovePlan bool
	// As is the preset for the agents the loop starts.
	As        string
	MaxRounds int
	Poll      time.Duration
	Notify    bool
	Once      bool
	Stop      bool
	Dry       bool
}

// AutoStep is one thing tkt auto did: started the ticket, approved its
// plan, or ran a stage.
type AutoStep struct {
	At     string `json:"at"`
	Action string `json:"action"`
	Stage  string `json:"stage,omitempty"`
	Reason string `json:"reason"`
	Head   string `json:"head,omitempty"`
}

// AutoResult is how tkt auto ended. Stop says why a loop stopped, or why
// --once took no step, such as a pause for a person; Did is the step --once
// took, and Wait what it waits for instead. Hold is what the status pages
// report while --once waits for their incident to end.
type AutoResult struct {
	Ticket  string     `json:"ticket"`
	Stop    string     `json:"stop"`
	Did     *AutoStep  `json:"did"`
	Wait    string     `json:"wait"`
	Hold    string     `json:"hold,omitempty"`
	Next    Next       `json:"next"`
	Phase   string     `json:"phase"`
	Rounds  int        `json:"rounds"`
	Steps   []AutoStep `json:"steps"`
	DryRun  bool       `json:"dry_run"`
	Actions []Action   `json:"actions"`
}

func (r *AutoResult) Text(w io.Writer) {
	switch {
	case r.Did != nil:
		fmt.Fprintf(w, "%s  %s\n", r.Ticket, stepText(*r.Did))
	case r.Wait == WaitIncident:
		fmt.Fprintf(w, "%s  waiting: %s; tkt auto sets no agent to work until it ends\n", r.Ticket, r.Hold)
	case r.Wait != "":
		fmt.Fprintf(w, "%s  waiting: %s\n", r.Ticket, r.Next.Text)
	default:
		fmt.Fprintf(w, "%s  %s  rounds %d  %s\n", r.Ticket, r.Stop, r.Rounds, stopHint(r.Stop, r.Ticket))
	}
}

func stepText(s AutoStep) string {
	switch s.Action {
	case actStart:
		return "started"
	case actApprove:
		return "approved the plan"
	case actSync:
		return "merged the base"
	}
	return "ran " + s.Stage
}

// The ways tkt auto stops, and the reasons it pauses for a person. A loop
// ends only at ready_for_human, merge_ordered, merged, closed or stopped. For the others it
// notifies once, waits, and goes on when the ticket moves, such as after you
// answer the agent's question in its tab.
const (
	StopReadyForHuman = "ready_for_human"
	// StopMergeOrdered ends a loop whose ticket has a merge order that is
	// due: tkt watch carries it out, since tkt auto never merges.
	StopMergeOrdered = "merge_ordered"
	StopMerged       = "merged"
	StopClosed       = "closed"
	StopStopped      = "stopped"
	StopNeedsYou     = "needs_you"
	StopPlan         = "plan_awaits_approval"
	StopAgentMissing = "agent_missing"
	StopBotSilent    = "bot_silent"
	StopRoundLimit   = "round_limit"
	StopNoProgress   = "no_progress"
	StopUnknown      = "unknown"
	// StopHold pauses a loop while the ticket is on hold. You put it there,
	// so the loop does not notify.
	StopHold = "hold"
	// StopSyncFailed pauses a loop whose sync cannot happen for now, such as
	// while the worktree holds changes. The loop tries again at each look.
	StopSyncFailed = "sync_failed"
)

// stopHint says what a person does after a stop.
func stopHint(stop, id string) string {
	switch stop {
	case StopReadyForHuman:
		return "run the human verification steps (tkt show " + id + " --steps), merge, then tkt done " + id
	case StopMergeOrdered:
		return "tkt watch sends finish-ticket the merge line your order holds"
	case StopMerged:
		return "tkt done " + id
	case StopClosed:
		return "the pull request was closed without merging"
	case StopStopped:
		return "tkt auto " + id + " starts it again"
	case StopNeedsYou:
		return "answer the agent in its tab (tkt show " + id + " --screen), then tkt auto " + id
	case StopPlan:
		return "read the plan with tkt show " + id + ", then tkt approve " + id + " and tkt auto " + id
	case StopAgentMissing:
		return "tkt resume " + id + ", then tkt auto " + id
	case StopBotSilent:
		return "check the review bot's CI job, or run tkt run " + id + " review"
	case StopRoundLimit:
		return "read the review, then tkt run " + id + " address, or raise [auto] max_rounds"
	case StopNoProgress:
		return "the last step changed nothing; check the ticket's tab, then tkt auto " + id
	case StopHold:
		return "tkt hold " + id + " --off lets tkt auto and tkt watch drive it again"
	case StopSyncFailed:
		return "tkt sync " + id + " says why it cannot merge the base; tkt auto goes on once it can"
	}
	return "check the agent in its tab, then tkt auto " + id
}

// pauseText says why a loop waits for you, from its pause and the rule
// that matched.
func pauseText(stop, reason string) string {
	switch {
	case reason == ReasonAgentBlocked:
		return "the agent asks a question; answer it in its tab"
	case reason == ReasonStatusBlocked:
		return "the agent stopped with Status: BLOCKED; read its reason in its tab"
	case reason == ReasonReviewAwaitsApproval:
		return "review-pr waits for you to post or drop its draft"
	case stop == StopPlan:
		return "the plan is ready; read it, then tkt approve"
	case stop == StopAgentMissing:
		return "no agent runs in the worktree; tkt resume brings it back"
	case stop == StopBotSilent:
		return "the review bot has not reviewed this head; check its CI job"
	case stop == StopRoundLimit:
		return "it reached [auto] max_rounds fix runs since the plan; read the review"
	case stop == StopNoProgress:
		return "the last step changed nothing; check the ticket's tab"
	case stop == StopHold:
		return "the ticket is on hold; tkt hold --off lets the loop go on"
	case stop == StopSyncFailed:
		return "tkt sync cannot merge the base"
	case reason == ReasonCheckAgent:
		return "the agent stopped without a status line; check its tab"
	}
	return "the agent needs you; check its tab"
}

// The actions autoStep picks.
const (
	actWait    = "wait_agent"
	actPoll    = "poll"
	actStart   = "start"
	actApprove = "approve"
	actRun     = "run"
	actSync    = "sync"
	actStop    = "stop"
	actPause   = "pause"
)

// WaitIncident is what tkt auto waits for while a status page reports an
// incident: --once says so in wait, and tkt ls in auto.waiting.
const WaitIncident = "incident"

// AutoPolicy is what one tkt auto run may do.
type AutoPolicy struct {
	ApprovePlan bool
	MaxRounds   int
}

// autoMemory is what tkt auto keeps between steps: the fix runs since the
// plan's approval, and the last action with the head it acted on.
type autoMemory struct {
	Rounds int
	Last   string
}

// autoAct is a step. Why says what a poll waits for when the rule list
// does not, such as an incident.
type autoAct struct {
	Kind, Stage, Stop, Why string
}

func (a autoAct) key(head string) string {
	return a.Kind + ":" + a.Stage + "@" + head
}

// held turns a step that sets an agent to work into a wait while a status
// page reports an incident, as tkt watch holds its own: a fix for checks
// that fail because CI is down, or a run on a model that does not answer,
// only spends a round. A sync needs no agent and goes on.
func held(a autoAct, hold string) autoAct {
	if hold != "" && (a.Kind == actStart || a.Kind == actApprove || a.Kind == actRun) {
		return autoAct{Kind: actPoll, Why: WaitIncident}
	}
	return a
}

// autoStep decides tkt auto's next action from the rule tkt ls applies. It
// differs from tkt ls on purpose: it never runs done, resume or verify; the
// review bot's CLEAR ends the loop like review-pr's READY_FOR_HUMAN, since
// the bot in CI is the reviewer; review-pr runs only where the repository
// has no bot ([review] bot_wait = "0s"); and where a person decides, or the
// ticket is on hold, it pauses.
func autoStep(n Next, f Facts, p AutoPolicy, m autoMemory) autoAct {
	stop := func(why string) autoAct { return autoAct{Kind: actStop, Stop: why} }
	pause := func(why string) autoAct { return autoAct{Kind: actPause, Stop: why} }
	act := func(kind, stage string) autoAct {
		a := autoAct{Kind: kind, Stage: stage}
		if a.key(f.Head) == m.Last {
			return pause(StopNoProgress)
		}
		return a
	}
	fix := func(stage string) autoAct {
		if m.Rounds >= p.MaxRounds {
			return pause(StopRoundLimit)
		}
		return act(actRun, stage)
	}
	switch {
	case f.Hold:
		return pause(StopHold)
	case f.Agent != herdr.Working && (f.Launch == "not_ready" || f.Launch == "prompt_uncertain"):
		return pause(StopNeedsYou)
	case f.Agent != herdr.Working && (f.Launch == "tab" || f.Launch == "launching"):
		return autoAct{Kind: actPoll}
	case f.Agent != "" && f.Agent != herdr.Idle && f.Agent != herdr.Done && f.Agent != herdr.Working && f.Agent != herdr.Blocked:
		return pause(StopUnknown)
	case f.ForgeLag && f.Agent != herdr.Working:
		// The forge has not caught up with a push yet, such as the loop's
		// own sync, so its facts are about an older head.
		return autoAct{Kind: actPoll}
	}
	switch n.Reason {
	case ReasonMerged:
		return stop(StopMerged)
	case ReasonClosed:
		return stop(StopClosed)
	case ReasonAgentWorking:
		return autoAct{Kind: actWait}
	case ReasonAgentWaiting, ReasonCIPending, ReasonAwaitingBot:
		return autoAct{Kind: actPoll}
	case ReasonAgentBlocked, ReasonStatusBlocked, ReasonCheckAgent, ReasonReviewAwaitsApproval:
		return pause(StopNeedsYou)
	case ReasonNoAgent:
		return pause(StopAgentMissing)
	case ReasonPlanAwaitsApproval:
		if p.ApprovePlan {
			return act(actApprove, "")
		}
		return pause(StopPlan)
	case ReasonBotSilent:
		return pause(StopBotSilent)
	case ReasonBaseMoved:
		// A clean merge needs no agent: tkt sync makes it, and it spends no
		// fix round. A conflict needs the session that resolves it.
		if f.BaseMerge == "conflict" || f.BaseMerge == "" && f.Mergeable == "CONFLICTING" {
			return fix("resolve")
		}
		return act(actSync, "")
	case ReasonQAFailed, ReasonFindings:
		return fix("address")
	case ReasonChecksFail:
		return fix("ci")
	case ReasonNeedsReview:
		return act(actRun, "review")
	case ReasonReviewClear, ReasonBotClear:
		return stop(StopReadyForHuman)
	case ReasonMergeDue:
		// The merge order is yours, and tkt watch carries it out once the
		// loop is gone: tkt auto never merges.
		return stop(StopMergeOrdered)
	}
	return pause(StopUnknown)
}

// autoView is one look at the ticket for tkt auto.
type autoView struct {
	Facts   Facts
	Next    Next
	Phase   string
	Pane    string
	Started bool
	// Wanted is false once tkt auto --stop cleared the ticket's auto key.
	Wanted bool
	// Hold is what the status pages report while they hold agent runs
	// back, as the running tkt watch last read them, or "".
	Hold string
}

// autoWorld is what the loop needs from outside, so tests can script it.
type autoWorld interface {
	look(ctx context.Context, fetch bool) (autoView, error)
	waitAgent(ctx context.Context, pane string) error
	sleep(ctx context.Context, d time.Duration) error
	act(ctx context.Context, a autoAct) error
	remember(m autoMemory, kv ...string)
	stopped(stop string, v autoView)
	// paused records that the loop waits for a person, and why, with a
	// note such as a sync's error, and notifies once.
	paused(why string, v autoView, note string)
	resumed()
	// holdSlot takes one of [auto] max_parallel slots before the loop sets
	// an agent to work, and says whether it had to wait for one; dropSlot
	// frees it once the agent stops working.
	holdSlot(ctx context.Context) (waited bool, err error)
	dropSlot()
	// waitIncident records that the loop waits for an incident to end
	// before it sets an agent to work, or, with on false, that it no
	// longer does.
	waitIncident(on bool, hold string)
	now() time.Time
}

// errStep is a step that cannot happen for now, such as a sync while the
// worktree holds changes. The loop takes it again at its next look; stop,
// when set, is the pause it shows meanwhile, and the error says why.
type errStep struct {
	error
	stop string
}

func (e errStep) Unwrap() error { return e.error }

// errForge marks a failed forge read, which the loop retries.
type errForge struct{ error }

func (e errForge) Unwrap() error { return e.error }

// maxForgeFailures is how many forge reads in a row may fail before the loop
// gives up.
const maxForgeFailures = 5

// autoLoop drives the ticket until it stops, or takes one step with once.
func autoLoop(ctx context.Context, w autoWorld, id string, p AutoPolicy, mem autoMemory, poll time.Duration, once, dry bool) (*AutoResult, error) {
	res := &AutoResult{Ticket: id, Steps: []AutoStep{}, DryRun: dry}
	fails := 0
	// pausedOn is the pause the loop notified about; retry is a step that
	// failed and that the loop takes again; incident says it waits for one
	// to end.
	pausedOn, retry, incident := "", "", false
	defer w.dropSlot()
	for {
		v, err := w.look(ctx, false)
		var fe errForge
		if errors.As(err, &fe) && !once && fails+1 < maxForgeFailures {
			fails++
			if err := w.sleep(ctx, poll); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		fails = 0
		res.Next, res.Phase, res.Rounds = v.Next, v.Phase, mem.Rounds
		if !v.Wanted && !once {
			res.Stop = StopStopped
			w.stopped(res.Stop, v)
			return res, nil
		}
		a := autoAct{Kind: actStart}
		if v.Started {
			a = autoStep(v.Next, v.Facts, p, mem)
		}
		a = held(a, v.Hold)
		if on := a.Why == WaitIncident; on != incident && !once {
			incident = on
			w.waitIncident(on, v.Hold)
		}
		// A slot counts agents at work: the loop keeps it while its agent
		// works or starts, and gives it up while it waits for CI, the bot
		// or a person.
		busy := a.Kind == actWait || a.Kind == actPoll && (v.Next.Reason == ReasonAgentWaiting || v.Phase == PhaseStarting)
		if !busy && a.Kind != actStart && a.Kind != actApprove && a.Kind != actRun {
			w.dropSlot()
		}
		if a.Kind != actPause && pausedOn != "" && a.key(v.Facts.Head) != retry {
			w.resumed()
			pausedOn, retry = "", ""
		}
		switch a.Kind {
		case actStop:
			res.Stop = a.Stop
			if !once {
				w.stopped(res.Stop, v)
			}
			return res, nil
		case actPause:
			if once {
				res.Stop = a.Stop
				return res, nil
			}
			if key := a.Stop + "|" + v.Next.Reason + "|" + v.Facts.Head + "|" + v.Facts.LastStatus; key != pausedOn {
				w.paused(a.Stop, v, "")
				pausedOn = key
			}
			if err := w.sleep(ctx, poll); err != nil {
				return nil, err
			}
			continue
		case actWait, actPoll:
			if once {
				res.Wait = or(a.Why, v.Next.Reason)
				if a.Why == WaitIncident {
					res.Hold = v.Hold
				}
				return res, nil
			}
			if a.Kind == actWait {
				err = w.waitAgent(ctx, v.Pane)
			} else {
				err = w.sleep(ctx, poll)
			}
			if err != nil {
				return nil, err
			}
			continue
		}
		// GitHub lags behind a push, and herdr does not track turns: let
		// both settle, look again, and act only on the same decision.
		if a.Kind != actStart && !dry {
			if err := w.sleep(ctx, min(5*time.Second, poll)); err != nil {
				return nil, err
			}
			again, err := w.look(ctx, true)
			if err != nil {
				return nil, err
			}
			if held(autoStep(again.Next, again.Facts, p, mem), again.Hold) != a {
				continue
			}
			v = again
		}
		if !once && !dry && a.Kind != actSync {
			waited, err := w.holdSlot(ctx)
			if err != nil {
				return nil, err
			}
			if waited {
				continue
			}
		}
		prev := mem
		mem.Last = a.key(v.Facts.Head)
		if a.Kind == actRun && a.Stage != "review" {
			mem.Rounds++
		}
		w.remember(mem)
		step := AutoStep{At: w.now().UTC().Format(history.TimeFormat), Action: a.Kind, Stage: a.Stage, Reason: v.Next.Reason, Head: v.Facts.Head}
		if err := w.act(ctx, a); err != nil {
			var se errStep
			if once || !errors.As(err, &se) {
				return nil, err
			}
			// The step did not happen, so the loop takes it again at its
			// next look, and while it keeps failing, asks for you once.
			mem = prev
			w.remember(mem)
			retry = a.key(v.Facts.Head)
			if key := se.stop + "|" + retry + "|" + se.Error(); se.stop != "" && key != pausedOn {
				w.paused(se.stop, v, se.Error())
				pausedOn = key
			}
			if err := w.sleep(ctx, poll); err != nil {
				return nil, err
			}
			continue
		}
		if retry != "" {
			if pausedOn != "" {
				w.resumed()
			}
			pausedOn, retry = "", ""
		}
		res.Steps = append(res.Steps, step)
		res.Rounds = mem.Rounds
		if once {
			res.Did = &step
			return res, nil
		}
	}
}

// Auto drives a ticket through the lifecycle until it needs a person: it
// starts the ticket when it has no worktree, approves the plan with
// --approve-plan, waits for CI and the review bot, merges a moved base
// itself when the merge is clean, and runs review, address, resolve and ci
// in fresh sessions. It never merges the pull request.
func (a *App) Auto(ctx context.Context, o AutoOpts) (*AutoResult, error) {
	if o.Dry && !o.Once {
		return nil, apperr.New(apperr.Usage, "--dry-run needs --once: a dry run takes one step").WithHint("tkt auto %s --once --dry-run", o.ID)
	}
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	id, err := c.LC.NormalizeID(o.ID)
	if err != nil {
		return nil, err
	}
	c.ticket(id)
	if err := c.LC.RequireGitHub("auto"); err != nil {
		return nil, err
	}
	if err := c.needTools("gh"); err != nil {
		return nil, err
	}
	if err := c.needHerdr(); err != nil {
		return nil, err
	}
	if o.As != "" {
		if _, err := c.Cfg.Preset(o.As); err != nil {
			return nil, err
		}
	}
	path := ""
	if es, err := c.WT.List(c.Ctx); err != nil {
		return nil, err
	} else if e, ok := es.Find(id); ok {
		path = e.Path
	}
	var ts *ticketState
	if path != "" {
		if ts, err = c.readState(path); err != nil {
			return nil, err
		}
	}
	if o.Stop {
		res := &AutoResult{Ticket: id, Stop: StopStopped, Steps: []AutoStep{}}
		if ts != nil {
			c.save(ts, "auto", "", "auto_stop", StopStopped, "auto_wait", "", "auto_pause", "")
		}
		return res, nil
	}
	f, err := lock.Wait(c.Ctx, c.Repo.Common, id+".auto", 2*time.Second)
	switch {
	case errors.Is(err, lock.ErrHeld):
		return nil, apperr.New(apperr.AutoRunning, "a tkt auto loop already drives %s", id).
			WithHint("tkt auto %s --stop stops it", id)
	case err != nil:
		return nil, err
	}
	c.locks = append(c.locks, f)
	p := AutoPolicy{ApprovePlan: o.ApprovePlan, MaxRounds: c.Cfg.Auto.MaxRounds}
	if o.MaxRounds >= 0 {
		p.MaxRounds = o.MaxRounds
	}
	poll := c.Cfg.Auto.Poll
	if o.Poll > 0 {
		poll = o.Poll
	}
	mode := autoOn
	if o.ApprovePlan {
		mode = autoPlan
	}
	w := &ctxWorld{c: c, id: id, path: path, mode: mode, as: o.As, notify: o.Notify, once: o.Once}
	defer w.unqueue()
	var mem autoMemory
	if ts != nil {
		if ts.st["hold"] != "" && !o.Once {
			a.UI.Info("%s is on hold; the loop waits until tkt hold %s --off", id, id)
		}
		mem = autoMemory{Rounds: atoi(ts.st["rounds"]), Last: ts.st["auto_last"]}
		if !o.Once {
			c.save(ts, "auto", mode, "auto_stop", "", "auto_pause", "")
			w.labelTab(true)
		}
	}
	res, err := autoLoop(c.Ctx, w, id, p, mem, poll, o.Once, c.Dry)
	if err != nil {
		if c.Ctx.Err() != nil {
			return nil, c.Ctx.Err()
		}
		return nil, err
	}
	res.Actions = append(append([]Action{}, c.Plan...), w.actions...)
	switch res.Stop {
	case "", StopReadyForHuman, StopMergeOrdered, StopMerged, StopStopped:
		return res, nil
	}
	if o.Once {
		return res, nil
	}
	return res, apperr.New(apperr.AutoStopped, "tkt auto stopped %s: %s", id, res.Stop).
		WithHint("%s", stopHint(res.Stop, id)).
		With("stop", res.Stop).With("next", res.Next).With("phase", res.Phase).With("rounds", res.Rounds).With("steps", res.Steps)
}

// ctxWorld is the real world tkt auto runs in.
type ctxWorld struct {
	c       *Ctx
	id      string
	path    string
	mode    string
	as      string
	notify  bool
	once    bool
	actions []Action
	slot    *os.File
	queued  string
}

func (w *ctxWorld) now() time.Time { return w.c.Now() }

func (w *ctxWorld) look(ctx context.Context, fetch bool) (autoView, error) {
	c := w.c
	hold := holdText(c.incidents())
	if w.path == "" {
		es, err := c.WT.List(c.Ctx)
		if err != nil {
			return autoView{}, err
		}
		e, ok := es.Find(w.id)
		if !ok {
			return autoView{Wanted: true, Hold: hold}, nil
		}
		w.path = e.Path
	}
	l, err := c.look(lookOpts{ids: []string{w.id}, fetch: fetch, probe: true})
	if err != nil {
		return autoView{}, err
	}
	if l.forgeErr != nil {
		return autoView{}, errForge{l.forgeErr}
	}
	row, f := c.row(ctx, l, ticketWorktree{ID: w.id, Path: w.path})
	v := autoView{Facts: f, Next: row.Next, Phase: row.Phase, Started: true, Wanted: true, Hold: hold}
	if row.Agent != nil {
		v.Pane = row.Agent.PaneID
	}
	if ts, err := c.readState(w.path); err == nil && !w.once {
		v.Wanted = ts.st["auto"] != ""
	}
	return v, nil
}

// waitSlice bounds one herdr wait, so the loop also notices a pull request
// merged or an agent replaced while the agent works.
const waitSlice = 10 * time.Minute

func (w *ctxWorld) waitAgent(ctx context.Context, pane string) error {
	_, err := w.c.H.Wait(ctx, pane, []string{herdr.Idle, herdr.Blocked, herdr.Done}, waitSlice.Milliseconds())
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch herdr.CodeOf(err) {
	case "", "timeout", "agent_not_found":
		return nil
	}
	return herdrErr(err)
}

func (w *ctxWorld) sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (w *ctxWorld) act(ctx context.Context, a autoAct) error {
	c := w.c
	var actions []Action
	switch a.Kind {
	case actStart:
		res, err := c.App.Start(ctx, StartOpts{ID: w.id, As: w.as, Dry: c.Dry, Unattended: w.mode == autoPlan})
		if err != nil {
			return err
		}
		actions = res.Actions
		if !c.Dry {
			w.path = res.Worktree
			w.unqueue()
			if ts, err := c.readState(w.path); err == nil && !w.once {
				c.save(ts, "auto", w.mode, "auto_stop", "", "auto_pause", "")
			}
		}
	case actApprove:
		res, err := c.App.Approve(ctx, ApproveOpts{ID: w.id, Dry: c.Dry})
		if err != nil {
			return err
		}
		actions = res.Actions
	case actRun:
		res, err := c.App.Run(ctx, RunOpts{ID: w.id, Stage: a.Stage, As: w.as, Dry: c.Dry})
		if err != nil {
			return err
		}
		actions = res.Actions
	case actSync:
		res, err := c.App.Sync(ctx, SyncOpts{ID: w.id, NoAgent: true, Dry: c.Dry})
		if err != nil {
			switch apperr.As(err).Code {
			case apperr.SyncConflict:
				// The base moved again since the look, and now conflicts:
				// the next look sees the conflict and runs resolve.
			case apperr.TicketLocked, apperr.AgentBusy:
				// Another tkt command, or an agent, works on it right now.
				return errStep{error: err}
			default:
				return errStep{error: err, stop: StopSyncFailed}
			}
		}
		actions = res.Actions
	}
	w.actions = append(w.actions, actions...)
	c.record(history.Event{Ticket: w.id, Event: history.Auto, Action: a.Kind, Stage: a.Stage})
	if !w.once {
		w.labelTab(true)
	}
	return nil
}

func (w *ctxWorld) remember(m autoMemory, kv ...string) {
	if w.path == "" {
		return
	}
	if ts, err := w.c.readState(w.path); err == nil {
		w.c.save(ts, append([]string{"rounds", strconv.Itoa(m.Rounds), "auto_last", m.Last}, kv...)...)
	}
}

// stopped records why the loop stopped and clears the ticket's auto key, so
// nothing restarts it before a person looks. An interrupted loop never gets
// here and leaves the key for the next one.
func (w *ctxWorld) stopped(stop string, v autoView) {
	c := w.c
	if w.path != "" {
		if ts, err := c.readState(w.path); err == nil {
			c.save(ts, "auto", "", "auto_stop", stop, "auto_wait", "", "auto_pause", "")
		}
	}
	w.labelTab(false)
	c.record(history.Event{Ticket: w.id, Event: history.Auto, Stop: stop})
	if w.notify {
		c.notify(w.id, autoNotice(stop, w.id))
	}
}

// paused records that the loop waits for a person, and why, so tkt ls and
// the board show it, and notifies once.
func (w *ctxWorld) paused(why string, v autoView, note string) {
	c := w.c
	w.remember(w.memoryNow(), "auto_pause", why, "auto_why", v.Next.Reason, "auto_note", note)
	c.record(history.Event{Ticket: w.id, Event: history.Auto, Action: actPause, Stop: why})
	if w.notify && why != StopHold {
		body := pauseText(why, v.Next.Reason)
		if note != "" {
			body += ": " + note
		}
		c.notify(w.id, notice{"needs you", body, "request"})
	}
}

func (w *ctxWorld) resumed() {
	w.remember(w.memoryNow(), "auto_pause", "", "auto_why", "", "auto_note", "")
}

// autoTabLabel marks a tab label for a ticket in auto: ACM-12 (auto), or
// ACM-12 (fast, auto) for a preset's label.
func autoTabLabel(label string) string {
	if strings.HasSuffix(label, ")") {
		return strings.TrimSuffix(label, ")") + ", auto)"
	}
	return label + " (auto)"
}

// labelTab marks the ticket's tab while tkt auto drives it, and takes the
// mark off when the loop ends. A failed rename only costs the mark.
func (w *ctxWorld) labelTab(on bool) {
	c := w.c
	if w.path == "" || c.Dry || w.once {
		return
	}
	ts, err := c.readState(w.path)
	if err != nil || ts.st["tab"] == "" {
		return
	}
	preset := c.Cfg.Default().Name
	if c.Cfg.Has(ts.st["preset"]) {
		preset = ts.st["preset"]
	}
	want := c.Cfg.TabLabel(w.id, preset)
	if on {
		want = autoTabLabel(want)
	}
	tabs, err := c.H.Tabs(c.Ctx)
	if err != nil {
		return
	}
	for _, t := range tabs {
		if t.TabID == ts.st["tab"] && t.Label != want {
			if err := c.H.TabRename(c.Ctx, t.TabID, want); err != nil {
				c.UI.Warn("cannot rename tab %s: %v", t.TabID, err)
			}
		}
	}
}

func autoNotice(stop, id string) notice {
	switch stop {
	case StopReadyForHuman:
		return notice{"ready for you", "the review says READY_FOR_HUMAN; run the human verification steps", "done"}
	case StopMergeOrdered:
		return notice{"merge order due", "the checks passed, so tkt watch sends finish-ticket your merge line", "none"}
	case StopMerged, StopStopped:
		return notice{stop, stopHint(stop, id), "done"}
	}
	return notice{"needs you", stop + ": " + stopHint(stop, id), "request"}
}

// holdSlot waits for one of [auto] max_parallel slots, so only that many
// tkt auto agents work at once in a repository. A ticket with no worktree
// yet leaves a mark in queued/, so tkt ls can show it while it waits.
func (w *ctxWorld) holdSlot(ctx context.Context) (bool, error) {
	c := w.c
	n := c.Cfg.Auto.MaxParallel
	if w.slot != nil || n <= 0 || c.Dry || w.once {
		return false, nil
	}
	waiting := false
	for {
		for k := range n {
			f, err := lock.Try(c.Repo.Common, "slot-"+strconv.Itoa(k))
			if err == nil {
				w.slot = f
				if waiting {
					w.remember(w.memoryNow(), "auto_wait", "")
					w.unqueue()
				}
				return waiting, nil
			}
			if !errors.Is(err, lock.ErrHeld) {
				return false, apperr.New(apperr.Internal, "cannot take an auto slot: %v", err)
			}
		}
		if !waiting {
			waiting = true
			c.UI.Info("%d tkt auto agents already work; %s waits for one to stop ([auto] max_parallel)", n, w.id)
			w.remember(w.memoryNow(), "auto_wait", "slot")
			w.queue("slot")
		}
		if err := w.sleep(ctx, 2*time.Second); err != nil {
			return false, err
		}
	}
}

func (w *ctxWorld) waitIncident(on bool, hold string) {
	if w.c.Dry {
		return
	}
	if on {
		w.c.UI.Info("%s; tkt auto sets no agent to work on %s until it ends", hold, w.id)
		w.remember(w.memoryNow(), "auto_wait", WaitIncident)
		w.queue(WaitIncident)
		return
	}
	w.remember(w.memoryNow(), "auto_wait", "")
	w.unqueue()
}

func (w *ctxWorld) dropSlot() {
	if w.slot != nil {
		w.slot.Close()
		w.slot = nil
	}
}

// queuedDir holds a file per ticket whose loop waits for a slot before the
// ticket has a worktree.
func queuedDir(common string) string { return filepath.Join(common, "tkt", "queued") }

// queue marks a ticket with no worktree yet as waiting, for a slot or for
// an incident to end, so tkt ls can show it.
func (w *ctxWorld) queue(wait string) {
	if w.path != "" {
		return
	}
	dir := queuedDir(w.c.Repo.Common)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	p := filepath.Join(dir, w.id)
	body := "mode=" + w.mode + "\nsince=" + w.c.Now().UTC().Format(history.TimeFormat) + "\nwait=" + wait + "\n"
	if os.WriteFile(p, []byte(body), 0o644) == nil {
		w.queued = p
	}
}

func (w *ctxWorld) unqueue() {
	if w.queued != "" {
		os.Remove(w.queued)
		w.queued = ""
	}
}

// memoryNow reads the memory back from the state file, so a write of other
// keys keeps it.
func (w *ctxWorld) memoryNow() autoMemory {
	if w.path == "" {
		return autoMemory{}
	}
	ts, err := w.c.readState(w.path)
	if err != nil {
		return autoMemory{}
	}
	return autoMemory{Rounds: atoi(ts.st["rounds"]), Last: ts.st["auto_last"]}
}
