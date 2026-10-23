package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/lock"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/runner"
	"gitlab.com/rogs/tkt/internal/service"
	"gitlab.com/rogs/tkt/internal/state"
)

// WatchOpts are tkt watch's arguments. Install and Uninstall add or remove
// the services that run it; Yes answers yes to starting them at boot.
type WatchOpts struct {
	Once      bool
	Stop      bool
	Dry       bool
	Install   bool
	Uninstall bool
	Yes       bool
}

// WatchResult is what tkt watch did in its last pass; with --stop, whether
// it stopped one; with --install or --uninstall, the service it set up or
// removed.
type WatchResult struct {
	Repo    string         `json:"repo"`
	Passes  int            `json:"passes"`
	Tickets []WatchTicket  `json:"tickets"`
	Stopped bool           `json:"stopped"`
	Service *ServiceResult `json:"service"`
	DryRun  bool           `json:"dry_run"`
	Actions []Action       `json:"actions"`
}

// WatchTicket is what one pass did with a ticket. Action is done, sync or
// run (with Stage), or "" when it left the ticket alone. Reason is the
// ticket's next step that the action answers, or why the pass left it:
// hold, auto (a tkt auto loop drives it), locked (another tkt command works
// on it), forge_lag (the forge still shows an older head), agent_settling
// (its agent stopped minutes ago, and a person may still use the session),
// slot (every [auto] max_parallel slot is taken), worktree_gone (its
// directory or state is gone), round_limit or no_progress (it waits for
// you), or its next step's reason.
type WatchTicket struct {
	Ticket string `json:"ticket"`
	Action string `json:"action"`
	Stage  string `json:"stage,omitempty"`
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

func (r *WatchResult) Text(w io.Writer) {
	if r.Service != nil {
		fmt.Fprint(w, r.Service.text(r.Repo, r.DryRun))
		return
	}
	if r.Stopped {
		fmt.Fprintf(w, "stopped tkt watch for %s\n", r.Repo)
		return
	}
	var rows [][]string
	for _, t := range r.Tickets {
		step := or(t.Action, "-")
		if t.Stage != "" {
			step += " " + t.Stage
		}
		why := t.Reason
		if t.Error != "" {
			why += ": " + t.Error
		}
		rows = append(rows, []string{t.Ticket, step, why})
	}
	if len(rows) > 0 {
		output.Table(w, []string{"TICKET", "STEP", "WHY"}, rows)
	}
}

// WatchStatus says whether tkt watch runs for the repository, and how its
// last pass went: LastPass is when it ended, and Error why it failed.
type WatchStatus struct {
	Running  bool   `json:"running"`
	LastPass string `json:"last_pass"`
	Error    string `json:"error"`
}

// The reasons a pass leaves a ticket alone, beyond its next step's.
const (
	WatchHold     = "hold"
	WatchAuto     = "auto"
	WatchLocked   = "locked"
	WatchForgeLag = "forge_lag"
	WatchSettling = "agent_settling"
	WatchSlot     = "slot"
	WatchLaunch   = "launch"
	WatchGone     = "worktree_gone"
)

// watchSettle is how long an agent sits idle before tkt watch replaces it
// with a fresh session: a person may still be reading or typing there.
const watchSettle = 5 * time.Minute

// tkt watch's own actions: actDone runs tkt done, and actMerge carries out a
// merge order with tkt merge.
const (
	actDone  = "done"
	actMerge = "merge"
)

// WatchPolicy is what tkt watch may do.
type WatchPolicy struct {
	MaxRounds      int
	DoneAfterMerge bool
}

// watchMemory is what tkt watch keeps about a ticket between passes: the
// fix runs since the plan's approval, which tkt auto counts too, and its
// last step with the head it took it on.
type watchMemory struct {
	Rounds int
	Last   string
}

// watchAct is a pass's decision for one ticket: a step to take, or why it
// leaves the ticket alone. Pause marks a reason that waits for a person.
type watchAct struct {
	Kind, Stage, Why string
	Pause            bool
}

func (a watchAct) key(head string) string { return a.Kind + ":" + a.Stage + "@" + head }

// watchStep decides what tkt watch does for one ticket, from the rule tkt
// ls applies, as autoStep does for tkt auto. watch takes only the upkeep
// nobody needs to decide on: it removes a ticket once finish-ticket ends
// with Status: DONE, syncs a moved base, fixes failing checks, once per head
// and up to [auto] max_rounds, and carries out the merge order you left with
// tkt merge. The plan, reviews and findings stay with you and with tkt auto,
// and a ticket on hold, or one a tkt auto loop drives, is left alone.
func watchStep(n Next, f Facts, p WatchPolicy, m watchMemory) watchAct {
	leave := func(why string) watchAct { return watchAct{Why: why} }
	pause := func(why string) watchAct { return watchAct{Why: why, Pause: true} }
	act := func(kind, stage string) watchAct {
		a := watchAct{Kind: kind, Stage: stage, Why: n.Reason}
		if a.key(f.Head) == m.Last {
			return pause(StopNoProgress)
		}
		return a
	}
	fix := func(stage string) watchAct {
		switch {
		case m.Rounds >= p.MaxRounds:
			return pause(StopRoundLimit)
		case f.Agent != "" && f.Idle > 0 && f.Idle < watchSettle:
			return leave(WatchSettling)
		}
		return act(actRun, stage)
	}
	switch {
	case f.Hold:
		return leave(WatchHold)
	case f.AutoRunning:
		return leave(WatchAuto)
	case f.Agent != herdr.Working && (f.Launch == "tab" || f.Launch == "launching" || f.Launch == "not_ready" || f.Launch == "prompt_uncertain"):
		return leave(WatchLaunch)
	case f.ForgeLag:
		// What the forge says is about an older head, even that a step made
		// no progress.
		return leave(WatchForgeLag)
	}
	switch n.Reason {
	case ReasonMerged:
		if p.DoneAfterMerge && f.LastStatus == contract.StatusDone {
			return act(actDone, "")
		}
	case ReasonBaseMoved:
		if f.BaseMerge == "conflict" || f.BaseMerge == "" && f.Mergeable == "CONFLICTING" {
			return fix("resolve")
		}
		return act(actSync, "")
	case ReasonChecksFail:
		return fix("ci")
	case ReasonMergeDue:
		// You left the order, so no settling: the session goes for the merge.
		return act(actMerge, "")
	}
	return leave(n.Reason)
}

// watchArgv is the tkt command a step amounts to, for dry runs. A merge is
// the ticket's next step, which carries the order's steps and head.
func watchArgv(a watchAct, id string, n Next) []string {
	switch a.Kind {
	case actDone:
		return []string{"tkt", "done", id}
	case actSync:
		return []string{"tkt", "sync", id, "--no-agent"}
	case actMerge:
		return n.Argv
	}
	return []string{"tkt", "run", id, a.Stage}
}

// watchPauseText says why tkt watch leaves a ticket for you, from the
// pause and its last step.
func watchPauseText(id, why, last string) string {
	if why == StopRoundLimit {
		return "tkt watch reached [auto] max_rounds fix runs since the plan's approval; read the review, then decide"
	}
	switch kind, _, _ := strings.Cut(last, "@"); kind {
	case actRun + ":ci":
		return "tkt watch ran ci on this head, and the checks still fail; read what the agent said in its tab"
	case actRun + ":resolve":
		return "tkt watch ran resolve on this head, and the base still conflicts; read what the agent said in its tab"
	case actSync + ":":
		return "tkt watch could not merge the base on this head; tkt sync " + id + " says why"
	case actDone + ":":
		return "tkt watch could not remove the merged ticket; tkt done " + id + " says why"
	case actMerge + ":":
		return "tkt watch sent finish-ticket the merge line on this head, and the pull request is still open; read what the agent said in its tab"
	}
	return "tkt watch took a step on this head that changed nothing"
}

// Watch keeps a repository's tickets moving: every [watch] interval it
// fetches, reads every ticket with a fixed number of forge calls, and takes
// at most one upkeep step per ticket (watchStep). With Once it takes one
// pass; with Dry it says what each ticket would get. With Stop it stops the
// tkt watch that runs for the repository.
func (a *App) Watch(ctx context.Context, o WatchOpts) (*WatchResult, error) {
	if o.Dry && !o.Once && !o.Stop && !o.Install && !o.Uninstall {
		return nil, apperr.New(apperr.Usage, "--dry-run needs --once: a dry run takes one pass").WithHint("tkt watch --once --dry-run")
	}
	if o.Yes && !o.Install {
		return nil, apperr.New(apperr.Usage, "--yes answers --install's question about starting at boot").WithHint("tkt watch --install --yes")
	}
	a.watching = true
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	switch {
	case o.Stop:
		return c.stopWatch()
	case o.Install:
		return c.installWatch(o)
	case o.Uninstall:
		return c.uninstallWatch()
	}
	if err := c.LC.RequireGitHub("watch"); err != nil {
		return nil, err
	}
	if err := c.needTools("gh", "herdr"); err != nil {
		return nil, err
	}
	w := &watcher{c: c, slots: map[string]*os.File{}, started: c.Now().UTC().Format(history.TimeFormat)}
	defer w.dropSlots(nil)
	// The first SIGTERM or Ctrl-C lets the pass finish the step it took. A
	// second one stops tkt at once, as it would without fang.
	go func() {
		<-ctx.Done()
		signal.Reset(os.Interrupt, syscall.SIGTERM)
	}()
	if !c.Dry {
		f, err := lock.Try(c.Repo.Common, watchLock)
		if errors.Is(err, lock.ErrHeld) {
			pid := state.Read(watchStatePath(c.Repo.Common))["pid"]
			return nil, apperr.New(apperr.WatchRunning, "tkt watch already runs for %s (pid %s)", c.Repo.Root, or(pid, "?")).
				WithHint("tkt watch --stop stops it")
		}
		if err != nil {
			return nil, apperr.New(apperr.Internal, "cannot take the watch lock: %v", err)
		}
		c.locks = append(c.locks, f)
		w.writeStatus(nil)
	}
	res := &WatchResult{Repo: c.Repo.Root, Tickets: []WatchTicket{}, DryRun: c.Dry, Actions: []Action{}}
	lastErr := ""
	self := binaryStamp()
	for {
		if !o.Once {
			a.UI.ForgetWarnings()
		}
		tickets, err := w.pass(ctx)
		w.passes++
		res.Passes = w.passes
		if ctx.Err() == nil {
			w.writeStatus(err)
		}
		if o.Once {
			res.Tickets, res.Actions = tickets, append([]Action{}, w.plan...)
			if tickets == nil {
				res.Tickets = []WatchTicket{}
			}
			return res, err
		}
		switch {
		case err == nil:
			res.Tickets, lastErr = tickets, ""
		case ctx.Err() == nil && apperr.As(err).Msg != lastErr:
			// One warning for each new failure, such as herdr going down,
			// not one a minute.
			lastErr = apperr.As(err).Msg
			a.UI.Warn("%s; tkt watch tries again every %s", lastErr, c.Cfg.Watch.Interval)
		}
		t := time.NewTimer(c.Cfg.Watch.Interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return res, nil
		case <-t.C:
		}
		if _, err := os.Stat(c.Repo.Common); err != nil {
			a.UI.Warn("%s is gone, so tkt watch stops", c.Repo.Root)
			return res, nil
		}
		// After tkt update, the next tkt command starts a watch that runs
		// the new tkt.
		if self != "" && binaryStamp() != self {
			a.UI.Info("tkt was replaced, so this tkt watch stops; the next tkt command starts the new one")
			return res, nil
		}
	}
}

// binaryStamp names the running tkt binary's size and time, or "" when
// tkt cannot tell.
func binaryStamp() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	fi, err := os.Stat(self)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d %d", fi.Size(), fi.ModTime().UnixNano())
}

// watchLock is the lock one tkt watch per repository holds.
const watchLock = "watch"

// watchStatePath is the file where tkt watch says how its passes go.
func watchStatePath(common string) string { return filepath.Join(common, "tkt", "watch") }

// watchRunning reports whether a tkt watch holds the repository's lock.
func (c *Ctx) watchRunning() bool {
	f, err := lock.Try(c.Repo.Common, watchLock)
	if errors.Is(err, lock.ErrHeld) {
		return true
	}
	if err == nil {
		f.Close()
	}
	return false
}

func (c *Ctx) watchStatus() WatchStatus {
	st := state.Read(watchStatePath(c.Repo.Common))
	return WatchStatus{Running: c.watchRunning(), LastPass: st["pass"], Error: st["error"]}
}

// watcher is one tkt watch run: the slots its agents hold, what its dry runs
// would do, and the herdr workspace its pass opens tabs in.
type watcher struct {
	c       *Ctx
	slots   map[string]*os.File
	started string
	passes  int
	plan    []Action
	ws      string
}

func (w *watcher) writeStatus(err error) {
	c := w.c
	if c.Dry {
		return
	}
	msg := ""
	if err != nil {
		msg = oneLine(apperr.As(err).Msg)
	}
	now := c.Now()
	st := state.State{"pid": strconv.Itoa(os.Getpid()), "started": w.started, "pass": now.UTC().Format(history.TimeFormat), "passes": strconv.Itoa(w.passes), "error": msg}
	if w.passes == 0 {
		st["pass"] = ""
	}
	if err := st.Save(watchStatePath(c.Repo.Common), now); err != nil {
		c.UI.Warn("cannot write %s: %v", watchStatePath(c.Repo.Common), err)
	}
}

// app returns the App a pass runs its commands with. Inside a herdr pane
// it is tkt watch's own; outside one, as under a service, it is a copy that
// sees HERDR_ENV and the workspace whose panes work in the repository, so
// every command watch runs opens its tabs there.
func (w *watcher) app() (*App, error) {
	a := w.c.App
	if a.Getenv("HERDR_ENV") == "1" && a.Getenv("HERDR_WORKSPACE_ID") != "" {
		w.ws = a.Getenv("HERDR_WORKSPACE_ID")
		return a, nil
	}
	ws, err := w.c.findWorkspace()
	if err != nil {
		return nil, err
	}
	w.ws = ws
	cp := *a
	cp.Getenv = func(k string) string {
		switch k {
		case "HERDR_ENV":
			return "1"
		case "HERDR_WORKSPACE_ID":
			return w.ws
		case "HERDR_PANE_ID", "HERDR_TAB_ID":
			return ""
		}
		return a.Getenv(k)
	}
	return &cp, nil
}

// openWorkspace opens a herdr workspace in the repository when none has a
// pane there, before the pass takes a step: every step works in herdr, and
// outside a pane, as after a reboot, nothing else gives tkt watch one. The
// next pass finds it from its pane.
func (w *watcher) openWorkspace(c *Ctx) error {
	if w.ws != "" {
		return nil
	}
	label := filepath.Base(c.Repo.Root)
	if c.Dry {
		c.Would(herdr.WorkspaceCreateArgv(c.Repo.Root, label))
		w.ws = "<workspace>"
		return nil
	}
	ws, err := c.H.WorkspaceCreate(c.Ctx, c.Repo.Root, label)
	if err != nil {
		return herdrErr(err)
	}
	c.UI.Info("no herdr workspace had a pane in %s, so tkt watch opened workspace %s there", c.Repo.Root, ws)
	w.ws = ws
	return nil
}

// findWorkspace finds the herdr workspace that holds the repository's
// panes: the one with the most panes in its primary worktree or a ticket
// worktree, or "" when none has one. herdr numbers workspaces per server, so
// nothing can store one.
func (c *Ctx) findWorkspace() (string, error) {
	panes, err := c.H.Panes(c.Ctx)
	if err != nil {
		return "", herdrErr(err)
	}
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return "", err
	}
	dirs := []string{c.Repo.Root}
	for _, e := range es {
		dirs = append(dirs, e.Path)
	}
	count, best := map[string]int{}, ""
	for _, p := range panes {
		for _, d := range dirs {
			if herdr.Inside(p.Cwd, d) {
				count[p.WorkspaceID]++
				if best == "" || count[p.WorkspaceID] > count[best] {
					best = p.WorkspaceID
				}
				break
			}
		}
	}
	return best, nil
}

// pass reads every ticket once and takes each one's step. It returns nil
// tickets when it could not read them.
func (w *watcher) pass(ctx context.Context) ([]WatchTicket, error) {
	a, err := w.app()
	if err != nil {
		return nil, err
	}
	c, err := a.begin(ctx, w.c.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	defer func() { w.plan = append(w.plan, c.Plan...) }()
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return nil, err
	}
	l, err := c.look(lookOpts{fetch: !c.Dry, probe: true})
	if err != nil {
		return nil, err
	}
	if l.src.Forge != "ok" {
		if l.forgeErr != nil {
			return nil, l.forgeErr
		}
		return nil, apperr.New(apperr.ForgeQueryFailed, "tkt watch cannot read the pull requests")
	}
	if l.src.Herdr != "ok" {
		return nil, apperr.New(apperr.HerdrFailed, "tkt watch cannot reach herdr")
	}
	out := []WatchTicket{}
	seen := map[string]bool{}
	for _, t := range c.ticketWorktrees(es) {
		if ctx.Err() != nil {
			break
		}
		seen[t.ID] = true
		// wt still lists a worktree whose directory someone removed.
		if _, err := os.Stat(t.Path); err != nil {
			out = append(out, WatchTicket{Ticket: t.ID, Reason: WatchGone})
			continue
		}
		row, f := c.row(ctx, l, t)
		// A stop during the reads leaves facts that may be wrong.
		if ctx.Err() != nil {
			break
		}
		w.freeSlot(t.ID, row)
		out = append(out, w.step(ctx, a, c, t, row, f))
	}
	w.dropSlots(seen)
	return out, nil
}

// step decides and takes one ticket's step.
func (w *watcher) step(ctx context.Context, a *App, c *Ctx, t ticketWorktree, row Row, f Facts) WatchTicket {
	res := WatchTicket{Ticket: t.ID}
	// Without the state, watch knows neither a hold nor its own last step.
	ts, err := c.stateAt(ctx, t.Path)
	if err != nil {
		res.Reason = WatchGone
		return res
	}
	f.Hold = f.Hold || ts.st["hold"] != ""
	// The steps of a merge order ran on its head; a new one drops it.
	if o := ts.st["merge_head"]; o != "" && f.Head != "" && o != f.Head && !f.ForgeLag && !c.Dry {
		c.dropOrder(ts, t.ID, "dropped")
		c.notify(t.ID, notice{"merge order dropped", "the head moved after you verified it; check the new head once it is reviewed", "request"})
	}
	mem := watchMemory{Rounds: atoi(ts.st["rounds"]), Last: ts.st["watch_last"]}
	p := WatchPolicy{MaxRounds: c.Cfg.Auto.MaxRounds, DoneAfterMerge: c.Cfg.Watch.DoneAfterMerge}
	act := watchStep(row.Next, f, p, mem)
	// A fact the rule list cannot see: another tkt command works on the
	// ticket.
	if act.Kind != "" && (c.ticketLocked(t.ID) || c.autoRunning(t.ID)) {
		act = watchAct{Why: WatchLocked}
	}
	res.Action, res.Stage, res.Reason = act.Kind, act.Stage, act.Why
	if act.Pause {
		w.pause(c, ts, t.ID, f.Head, act.Why, mem.Last)
		return res
	}
	if ts.st["watch_pause"] != "" && !c.Dry {
		c.save(ts, "watch_pause", "", "watch_head", "")
	}
	if act.Kind == "" {
		return res
	}
	if (act.Kind == actRun || act.Kind == actMerge) && !w.takeSlot(t.ID) {
		res.Action, res.Stage, res.Reason = "", "", WatchSlot
		return res
	}
	if c.Dry {
		w.openWorkspace(c)
		c.Would(watchArgv(act, t.ID, row.Next))
		return res
	}
	// A step that started finishes even when tkt watch is asked to stop; a
	// stop before it starts leaves the ticket for the next watch.
	if ctx.Err() != nil {
		res.Action, res.Stage, res.Reason = "", "", act.Why
		return res
	}
	if err := w.openWorkspace(c); err != nil {
		res.Action, res.Stage, res.Error = "", "", apperr.As(err).Msg
		return res
	}
	// Remember the step before taking it, so one that fails is not taken
	// again on the same head.
	kv := []string{"watch_last", act.key(f.Head)}
	if act.Kind == actRun {
		kv = append(kv, "rounds", strconv.Itoa(mem.Rounds+1))
	}
	c.save(ts, kv...)
	if err := w.do(ctx, a, act, t.ID, ts); err != nil {
		if errors.Is(err, errOrderWaits) {
			c.save(ts, "watch_last", mem.Last)
			res.Action, res.Reason = "", ReasonCIPending
			return res
		}
		res.Error = apperr.As(err).Msg
		// A sync or a merge that failed sent nothing, such as one while the
		// network is down, so the next pass tries it again. One whose outcome
		// is uncertain waits for you: the merge line may have gone in.
		if (act.Kind == actSync || act.Kind == actMerge) && apperr.Lookup(apperr.As(err).Code).Exit != apperr.ExitUncertain {
			c.save(ts, "watch_last", mem.Last)
		}
		w.failed(c, ts, t.ID, act, f.Head, res.Error)
		return res
	}
	if ts.st["watch_fail"] != "" {
		c.save(ts, "watch_fail", "")
	}
	switch act.Kind {
	case actRun:
		c.UI.Info("%s: ran %s (%s)", t.ID, act.Stage, act.Why)
		c.record(history.Event{Ticket: t.ID, Event: history.Watch, Action: actRun, Stage: act.Stage})
		c.notify(t.ID, notice{"tkt watch runs " + act.Stage, watchRunText(act.Stage), "none"})
	case actSync:
		c.UI.Info("%s: synced with the base, with no agent", t.ID)
	case actDone:
		c.UI.Info("%s: removed it after finish-ticket's Status: DONE", t.ID)
		c.notify(t.ID, notice{"removed", "finish-ticket ended with Status: DONE, so tkt watch removed its tabs and worktree", "done"})
	case actMerge:
		c.UI.Info("%s: the checks passed, so finish-ticket merges it", t.ID)
		c.record(history.Event{Ticket: t.ID, Event: history.Watch, Action: actMerge})
		c.notify(t.ID, notice{"merging", "the checks passed, so a fresh session runs finish-ticket with your merge order", "none"})
	}
	return res
}

// errOrderWaits says the checks at a merge order's head run again, so the
// order waits for them.
var errOrderWaits = errors.New("the merge order waits for the checks")

func watchRunText(stage string) string {
	if stage == "resolve" {
		return "the base merge conflicts; a fresh session resolves it"
	}
	return "the checks fail at the head; a fresh session fixes them"
}

// do takes a step. It finishes the step even when tkt watch is asked to
// stop meanwhile, so it never leaves a merge half done.
func (w *watcher) do(ctx context.Context, a *App, act watchAct, id string, ts *ticketState) error {
	ctx = context.WithoutCancel(ctx)
	switch act.Kind {
	case actDone:
		_, err := a.Done(ctx, DoneOpts{IDs: []string{id}})
		return err
	case actMerge:
		r, err := a.Merge(ctx, MergeOpts{ID: id, Passed: ts.st["merge_steps"], Head: ts.st["merge_head"]})
		if err == nil && r.Action != MergeMerging {
			return errOrderWaits
		}
		return err
	case actSync:
		_, err := a.Sync(ctx, SyncOpts{ID: id, NoAgent: true})
		// The base moved again since the look, and now conflicts: the next
		// pass sees the conflict and runs resolve.
		if err != nil && apperr.As(err).Code == apperr.SyncConflict {
			return nil
		}
		return err
	}
	_, err := a.Run(ctx, RunOpts{ID: id, Stage: act.Stage})
	return err
}

// failed reports a step that failed: once for each step, head and reason,
// since a sync that keeps failing is tried again every pass.
func (w *watcher) failed(c *Ctx, ts *ticketState, id string, act watchAct, head, msg string) {
	key := act.key(head) + " " + msg
	if ts.st["watch_fail"] == key {
		return
	}
	c.save(ts, "watch_fail", key)
	what := act.Kind
	if act.Stage != "" {
		what += " " + act.Stage
	}
	c.UI.Warn("%s: tkt watch could not %s: %s", id, what, msg)
	c.record(history.Event{Ticket: id, Event: history.Watch, Action: act.Kind, Stage: act.Stage, Stop: "failed"})
	c.notify(id, notice{"tkt watch could not " + what, msg, "request"})
}

// pause records that tkt watch leaves a ticket for you, and notifies once
// for each reason and head. A pause right after a step that failed says
// nothing new, since that failure was reported already.
func (w *watcher) pause(c *Ctx, ts *ticketState, id, head, why, last string) {
	if c.Dry || ts.path == "" || ts.st["watch_pause"] == why && ts.st["watch_head"] == head {
		return
	}
	c.save(ts, "watch_pause", why, "watch_head", head)
	c.record(history.Event{Ticket: id, Event: history.Watch, Action: actPause, Stop: why})
	if !strings.HasPrefix(ts.st["watch_fail"], last+" ") {
		c.notify(id, notice{"needs you", watchPauseText(id, why, last), "request"})
	}
}

// ticketLocked reports whether another tkt command holds the ticket.
func (c *Ctx) ticketLocked(id string) bool {
	f, err := lock.Try(c.Repo.Common, id)
	if err != nil {
		return true
	}
	f.Close()
	return false
}

// takeSlot takes one of the [auto] max_parallel slots tkt auto's agents use,
// for an agent tkt watch sets to work. It holds the slot until the agent
// stops working.
func (w *watcher) takeSlot(id string) bool {
	c := w.c
	n := c.Cfg.Auto.MaxParallel
	if w.slots[id] != nil || n <= 0 || c.Dry {
		return true
	}
	for k := range n {
		if f, err := lock.Try(c.Repo.Common, "slot-"+strconv.Itoa(k)); err == nil {
			w.slots[id] = f
			return true
		}
	}
	return false
}

// freeSlot gives back a ticket's slot once its agent stopped working.
func (w *watcher) freeSlot(id string, row Row) {
	f := w.slots[id]
	if f == nil || row.Agent != nil && row.Agent.Status == herdr.Working || row.Launch == "tab" || row.Launch == "launching" {
		return
	}
	f.Close()
	delete(w.slots, id)
}

// dropSlots gives back the slots of the tickets not in keep, such as one
// whose worktree is gone; nil drops them all.
func (w *watcher) dropSlots(keep map[string]bool) {
	for id, f := range w.slots {
		if !keep[id] {
			f.Close()
			delete(w.slots, id)
		}
	}
}

// stopWatch stops the tkt watch that runs for the repository and waits
// until it has finished the step it was taking. A watch its service runs
// stops through the service, which would start it again otherwise.
func (c *Ctx) stopWatch() (*WatchResult, error) {
	res := &WatchResult{Repo: c.Repo.Root, Tickets: []WatchTicket{}, DryRun: c.Dry, Actions: []Action{}}
	if s, err := c.watchService(c.Ctx, c.Repo.Root); err == nil && c.active(c.Ctx, s, s.unit) {
		argv := service.SystemctlArgv("stop", s.unit)
		if s.manager == service.Launchd {
			argv = service.LaunchctlArgv("bootout", s.domain+"/"+s.unit)
		}
		if err := c.serviceRun(argv); err != nil {
			return nil, err
		}
		c.UI.Info("stopped %s; the next tkt command in a herdr pane, or the next boot, starts it again", s.unit)
		res.Stopped, res.Actions = !c.Dry, append([]Action{}, c.Plan...)
		return res, nil
	}
	if !c.watchRunning() {
		c.UI.Info("no tkt watch runs for %s", c.Repo.Root)
		return res, nil
	}
	stopped, err := c.signalWatch()
	if err != nil {
		return nil, err
	}
	res.Stopped, res.Actions = stopped, append([]Action{}, c.Plan...)
	return res, nil
}

// signalWatch sends the running tkt watch SIGTERM and waits until it has
// finished the step it was taking. It reports whether it stopped one.
func (c *Ctx) signalWatch() (bool, error) {
	pid := atoi(state.Read(watchStatePath(c.Repo.Common))["pid"])
	if pid <= 0 {
		return false, apperr.New(apperr.Internal, "cannot tell which process runs tkt watch for %s", c.Repo.Root)
	}
	if c.Dry {
		c.Would([]string{"kill", "-TERM", strconv.Itoa(pid)})
		return false, nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return false, apperr.New(apperr.Internal, "cannot stop tkt watch (pid %d): %v", pid, err)
	}
	for deadline := time.Now().Add(30 * time.Second); c.watchRunning(); c.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			return false, apperr.New(apperr.WatchRunning, "tkt watch (pid %d) did not stop within 30 seconds; it finishes the step it took first", pid).
				WithHint("kill %d stops it at once", pid)
		}
	}
	return true, nil
}

// ensureWatch starts tkt watch for the repository when none runs, so its
// tickets keep moving after this command ends. It runs only inside a herdr
// pane, where everything tkt watch needs is there; [watch] autostart =
// false, or TKT_WATCH=off, turns it off. The watch runs in a session of its
// own, so it outlives the command and the pane, and writes its lines to
// watch.log in the repository's tkt directory.
func (c *Ctx) ensureWatch() {
	if c.Getenv("HERDR_ENV") != "1" || !c.Cfg.Watch.Autostart || c.Getenv("TKT_WATCH") == "off" ||
		c.LC.RequireGitHub("watch") != nil || !found("gh") || !found("herdr") || c.watchRunning() {
		return
	}
	sp, ok := c.R.(runner.Spawner)
	if !ok {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Join(c.Repo.Common, "tkt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// A watch that exits at once would start again with every command, such
	// as the board's tkt ls every few seconds.
	mark := filepath.Join(dir, "watch.started")
	if fi, err := os.Stat(mark); err == nil && c.Now().Sub(fi.ModTime()) < time.Minute {
		return
	}
	if os.WriteFile(mark, nil, 0o644) == nil {
		os.Chtimes(mark, c.Now(), c.Now())
	}
	// A service that tkt watch --install set up runs the repository's watch.
	if c.startWatchService() {
		return
	}
	log := filepath.Join(dir, "watch.log")
	if fi, err := os.Stat(log); err == nil && fi.Size() > 1<<20 {
		os.Rename(log, log+".1")
	}
	argv := []string{self, "-C", c.Repo.Root, "watch"}
	if err := sp.Spawn(c.Ctx, runner.Cmd{Argv: argv}, watchEnv(os.Environ()), c.Repo.Root, log); err != nil {
		c.UI.Warn("cannot start tkt watch: %v", err)
		return
	}
	c.UI.Info("started tkt watch for %s, which keeps its tickets moving (tkt help watch); it writes to %s", c.Repo.Root, log)
}

// watchEnv is the environment tkt watch starts with: this one, out of a
// ticket's own TMPDIR, which tkt done removes, and without the pane, tab and
// workspace tkt runs in. The pane and tab are not the watch's, and a
// workspace id holds only until herdr restarts, so the watch finds the
// workspace again at every pass, as it does outside a pane.
func watchEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "HERDR_ENV="), strings.HasPrefix(kv, "HERDR_WORKSPACE_ID="),
			strings.HasPrefix(kv, "HERDR_PANE_ID="), strings.HasPrefix(kv, "HERDR_TAB_ID="):
		case strings.HasPrefix(kv, "TMPDIR="):
			out = append(out, "TMPDIR="+tmpRoot(strings.TrimPrefix(kv, "TMPDIR=")))
		default:
			out = append(out, kv)
		}
	}
	return out
}
