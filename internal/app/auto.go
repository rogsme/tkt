package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
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
	MaxRounds   int
	Poll        time.Duration
	Notify      bool
	Once        bool
	Stop        bool
	Dry         bool
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
// --once took no step; Did is the step --once took, and Wait what it waits
// for instead.
type AutoResult struct {
	Ticket  string     `json:"ticket"`
	Stop    string     `json:"stop"`
	Did     *AutoStep  `json:"did"`
	Wait    string     `json:"wait"`
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
	}
	return "ran " + s.Stage
}

// The ways tkt auto stops.
const (
	StopReadyForHuman = "ready_for_human"
	StopMerged        = "merged"
	StopClosed        = "closed"
	StopStopped       = "stopped"
	StopNeedsYou      = "needs_you"
	StopPlan          = "plan_awaits_approval"
	StopAgentMissing  = "agent_missing"
	StopRoundLimit    = "round_limit"
	StopNoProgress    = "no_progress"
	StopUnknown       = "unknown"
)

// stopHint says what a person does after a stop.
func stopHint(stop, id string) string {
	switch stop {
	case StopReadyForHuman:
		return "run the human verification steps (tkt show " + id + " --steps), merge, then tkt done " + id
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
	case StopRoundLimit:
		return "read the review, then tkt run " + id + " address, or raise [auto] max_rounds"
	case StopNoProgress:
		return "the last step changed nothing; check the ticket's tab, then tkt auto " + id
	}
	return "check the agent in its tab, then tkt auto " + id
}

// The actions autoStep picks.
const (
	actWait    = "wait_agent"
	actPoll    = "poll"
	actStart   = "start"
	actApprove = "approve"
	actRun     = "run"
	actStop    = "stop"
)

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

type autoAct struct {
	Kind, Stage, Stop string
}

func (a autoAct) key(head string) string {
	return a.Kind + ":" + a.Stage + "@" + head
}

// autoStep decides tkt auto's next action from the rule tkt ls applies. It
// differs from tkt ls on purpose: it never runs done, resume or verify, it
// asks review-pr for a final verdict after the automated reviewer's CLEAR,
// and it stops where a person decides.
func autoStep(n Next, f Facts, p AutoPolicy, m autoMemory) autoAct {
	stop := func(why string) autoAct { return autoAct{Kind: actStop, Stop: why} }
	act := func(kind, stage string) autoAct {
		a := autoAct{Kind: kind, Stage: stage}
		if a.key(f.Head) == m.Last {
			return stop(StopNoProgress)
		}
		return a
	}
	fix := func(stage string) autoAct {
		if m.Rounds >= p.MaxRounds {
			return stop(StopRoundLimit)
		}
		return act(actRun, stage)
	}
	switch {
	case f.Agent != herdr.Working && (f.Launch == "not_ready" || f.Launch == "prompt_uncertain"):
		return stop(StopNeedsYou)
	case f.Agent != herdr.Working && (f.Launch == "tab" || f.Launch == "launching"):
		return autoAct{Kind: actPoll}
	case f.Agent != "" && f.Agent != herdr.Idle && f.Agent != herdr.Done && f.Agent != herdr.Working && f.Agent != herdr.Blocked:
		return stop(StopUnknown)
	}
	switch n.Reason {
	case ReasonMerged:
		return stop(StopMerged)
	case ReasonClosed:
		return stop(StopClosed)
	case ReasonAgentWorking:
		return autoAct{Kind: actWait}
	case ReasonAgentBlocked, ReasonStatusBlocked, ReasonCheckAgent, ReasonReviewAwaitsApproval:
		return stop(StopNeedsYou)
	case ReasonNoAgent:
		return stop(StopAgentMissing)
	case ReasonPlanAwaitsApproval:
		if p.ApprovePlan {
			return act(actApprove, "")
		}
		return stop(StopPlan)
	case ReasonCIPending, ReasonAwaitingBot:
		return autoAct{Kind: actPoll}
	case ReasonBaseMoved:
		return fix("sync")
	case ReasonQAFailed, ReasonFindings:
		return fix("address")
	case ReasonChecksFail:
		return fix("ci")
	case ReasonNeedsReview, ReasonBotClear:
		return act(actRun, "review")
	case ReasonReviewClear:
		return stop(StopReadyForHuman)
	}
	return stop(StopUnknown)
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
}

// autoWorld is what the loop needs from outside, so tests can script it.
type autoWorld interface {
	look(ctx context.Context, fetch bool) (autoView, error)
	waitAgent(ctx context.Context, pane string) error
	sleep(ctx context.Context, d time.Duration) error
	act(ctx context.Context, a autoAct) error
	remember(m autoMemory, kv ...string)
	stopped(stop string, v autoView)
	now() time.Time
}

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
		switch a.Kind {
		case actStop:
			res.Stop = a.Stop
			if !once {
				w.stopped(res.Stop, v)
			}
			return res, nil
		case actWait, actPoll:
			if once {
				res.Wait = v.Next.Reason
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
			if autoStep(again.Next, again.Facts, p, mem) != a {
				continue
			}
			v = again
		}
		mem.Last = a.key(v.Facts.Head)
		if a.Kind == actRun && a.Stage != "review" {
			mem.Rounds++
		}
		w.remember(mem)
		step := AutoStep{At: w.now().UTC().Format(history.TimeFormat), Action: a.Kind, Stage: a.Stage, Reason: v.Next.Reason, Head: v.Facts.Head}
		if err := w.act(ctx, a); err != nil {
			return nil, err
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
// --approve-plan, waits for CI and the review bot, and runs review, address,
// sync and ci in fresh sessions. It never merges.
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
			c.save(ts, "auto", "", "auto_stop", StopStopped, "auto_wait", "")
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
	w := &ctxWorld{c: c, id: id, path: path, mode: mode, notify: o.Notify, once: o.Once}
	var mem autoMemory
	if ts != nil {
		mem = autoMemory{Rounds: atoi(ts.st["rounds"]), Last: ts.st["auto_last"]}
		if !o.Once {
			c.save(ts, "auto", mode, "auto_stop", "")
		}
	}
	if !o.Once {
		if err := w.takeSlot(); err != nil {
			return nil, err
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
	case "", StopReadyForHuman, StopMerged, StopStopped:
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
	notify  bool
	once    bool
	actions []Action
}

func (w *ctxWorld) now() time.Time { return w.c.Now() }

func (w *ctxWorld) look(ctx context.Context, fetch bool) (autoView, error) {
	c := w.c
	if w.path == "" {
		es, err := c.WT.List(c.Ctx)
		if err != nil {
			return autoView{}, err
		}
		e, ok := es.Find(w.id)
		if !ok {
			return autoView{Wanted: true}, nil
		}
		w.path = e.Path
	}
	l, err := c.look(lookOpts{ids: []string{w.id}, fetch: fetch})
	if err != nil {
		return autoView{}, err
	}
	if l.forgeErr != nil {
		return autoView{}, errForge{l.forgeErr}
	}
	row, f := c.row(ctx, l, ticketWorktree{ID: w.id, Path: w.path})
	v := autoView{Facts: f, Next: row.Next, Phase: row.Phase, Started: true, Wanted: true}
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
		res, err := c.App.Start(ctx, StartOpts{ID: w.id, Dry: c.Dry, Unattended: w.mode == autoPlan})
		if err != nil {
			return err
		}
		actions = res.Actions
		if !c.Dry {
			w.path = res.Worktree
			if ts, err := c.readState(w.path); err == nil && !w.once {
				c.save(ts, "auto", w.mode, "auto_stop", "")
			}
		}
	case actApprove:
		res, err := c.App.Approve(ctx, ApproveOpts{ID: w.id, Dry: c.Dry})
		if err != nil {
			return err
		}
		actions = res.Actions
	case actRun:
		res, err := c.App.Run(ctx, RunOpts{ID: w.id, Stage: a.Stage, Dry: c.Dry})
		if err != nil {
			return err
		}
		actions = res.Actions
	}
	w.actions = append(w.actions, actions...)
	c.record(history.Event{Ticket: w.id, Event: history.Auto, Action: a.Kind, Stage: a.Stage})
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
			c.save(ts, "auto", "", "auto_stop", stop, "auto_wait", "")
		}
	}
	c.record(history.Event{Ticket: w.id, Event: history.Auto, Stop: stop})
	if w.notify {
		c.notify(w.id, autoNotice(stop, w.id))
	}
}

func autoNotice(stop, id string) notice {
	switch stop {
	case StopReadyForHuman:
		return notice{"ready for you", "the review says READY_FOR_HUMAN; run the human verification steps", "done"}
	case StopMerged, StopStopped:
		return notice{stop, stopHint(stop, id), "done"}
	}
	return notice{"needs you", stop + ": " + stopHint(stop, id), "request"}
}

// takeSlot waits for one of [auto] max_parallel slots, so only that many
// loops run at once in a repository. The slot is held until tkt exits.
func (w *ctxWorld) takeSlot() error {
	c := w.c
	n := c.Cfg.Auto.MaxParallel
	if n <= 0 || c.Dry {
		return nil
	}
	waiting := false
	for {
		for k := range n {
			f, err := lock.Try(c.Repo.Common, "slot-"+strconv.Itoa(k))
			if err == nil {
				c.locks = append(c.locks, f)
				if waiting {
					w.remember(w.memoryNow(), "auto_wait", "")
				}
				return nil
			}
			if !errors.Is(err, lock.ErrHeld) {
				return apperr.New(apperr.Internal, "cannot take an auto slot: %v", err)
			}
		}
		if !waiting {
			waiting = true
			c.UI.Info("%d tkt auto loops already run; %s waits for one to stop ([auto] max_parallel)", n, w.id)
			w.remember(w.memoryNow(), "auto_wait", "slot")
		}
		if err := w.sleep(c.Ctx, 2*time.Second); err != nil {
			return err
		}
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
