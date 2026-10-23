package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/lock"
	"gitlab.com/rogs/tkt/internal/output"
)

func TestAutoStepTable(t *testing.T) {
	open := Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "ok", Head: "h1", Behind: -1}
	with := func(f Facts, edit func(*Facts)) Facts { edit(&f); return f }
	pause := func(why string) autoAct { return autoAct{Kind: actPause, Stop: why} }
	cases := []struct {
		f    Facts
		p    AutoPolicy
		m    autoMemory
		want autoAct
	}{
		{Facts{ID: "ACM-1", Agent: "working", Stage: "start"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actWait}},
		{Facts{ID: "ACM-1", Agent: "idle", Background: true}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actPoll}},
		{Facts{ID: "ACM-1", Agent: "blocked"}, AutoPolicy{}, autoMemory{}, pause(StopNeedsYou)},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, AutoPolicy{}, autoMemory{}, pause(StopPlan)},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, AutoPolicy{ApprovePlan: true}, autoMemory{}, autoAct{Kind: actApprove}},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, AutoPolicy{ApprovePlan: true}, autoMemory{Last: "approve:@"}, pause(StopNoProgress)},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "BLOCKED"}, AutoPolicy{}, autoMemory{}, pause(StopNeedsYou)},
		{Facts{ID: "ACM-1", Agent: "idle"}, AutoPolicy{}, autoMemory{}, pause(StopNeedsYou)},
		{Facts{ID: "ACM-1"}, AutoPolicy{}, autoMemory{}, pause(StopAgentMissing)},
		{Facts{ID: "ACM-1", Agent: "idle", Launch: "not_ready"}, AutoPolicy{}, autoMemory{}, pause(StopNeedsYou)},
		{Facts{ID: "ACM-1", Launch: "launching"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actPoll}},
		{Facts{ID: "ACM-1", Agent: "unknown"}, AutoPolicy{}, autoMemory{}, pause(StopUnknown)},
		{with(open, func(f *Facts) { f.Checks = "pending" }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actPoll}},
		{with(open, func(f *Facts) { f.BotWait = time.Hour }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actPoll}},
		// With a bot, tkt auto never reviews: the bot in CI does.
		{with(open, func(f *Facts) { f.BotWait, f.Waited = time.Hour, time.Hour }), AutoPolicy{}, autoMemory{}, pause(StopBotSilent)},
		{open, AutoPolicy{}, autoMemory{}, autoAct{Kind: actRun, Stage: "review"}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "CLEAR", true }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopReadyForHuman}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "FINDINGS", true }), AutoPolicy{MaxRounds: 2}, autoMemory{Rounds: 1}, autoAct{Kind: actRun, Stage: "address"}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "FINDINGS", true }), AutoPolicy{MaxRounds: 2}, autoMemory{Rounds: 2}, pause(StopRoundLimit)},
		// A moved base that merges clean is tkt sync's, with no agent and
		// no round, even past the round limit; a conflict needs resolve.
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "STALE_BASE", true }), AutoPolicy{MaxRounds: 5}, autoMemory{}, autoAct{Kind: actSync}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead, f.BaseMerge = "STALE_BASE", true, "clean" }), AutoPolicy{MaxRounds: 1}, autoMemory{Rounds: 1}, autoAct{Kind: actSync}},
		{with(open, func(f *Facts) { f.Mergeable, f.BaseMerge = "CONFLICTING", "clean" }), AutoPolicy{MaxRounds: 5}, autoMemory{}, autoAct{Kind: actSync}},
		{with(open, func(f *Facts) { f.Mergeable = "CONFLICTING" }), AutoPolicy{MaxRounds: 5}, autoMemory{}, autoAct{Kind: actRun, Stage: "resolve"}},
		{with(open, func(f *Facts) { f.BaseMerge = "conflict" }), AutoPolicy{MaxRounds: 5}, autoMemory{}, autoAct{Kind: actRun, Stage: "resolve"}},
		{with(open, func(f *Facts) { f.BaseMerge = "conflict" }), AutoPolicy{MaxRounds: 1}, autoMemory{Rounds: 1}, pause(StopRoundLimit)},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "STALE_BASE", true }), AutoPolicy{MaxRounds: 5}, autoMemory{Last: "sync:@h1"}, pause(StopNoProgress)},
		{with(open, func(f *Facts) { f.Checks = "fail" }), AutoPolicy{MaxRounds: 5}, autoMemory{}, autoAct{Kind: actRun, Stage: "ci"}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "READY_FOR_HUMAN", true }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopReadyForHuman}},
		// A merge order is yours: tkt auto leaves it to tkt watch.
		{with(open, func(f *Facts) { f.Verdict, f.AtHead, f.MergeSteps = "READY_FOR_HUMAN", true, "1" }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopReadyForHuman}},
		{with(open, func(f *Facts) { f.LastStatus = "AWAITING_POST_APPROVAL" }), AutoPolicy{}, autoMemory{}, pause(StopNeedsYou)},
		{with(open, func(f *Facts) { f.PRState = "MERGED" }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopMerged}},
		{with(open, func(f *Facts) { f.PRState = "CLOSED" }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopClosed}},
		// Right after the loop's own sync, the forge may still show the old
		// head: the loop waits for it instead of calling the sync useless.
		{with(open, func(f *Facts) { f.Verdict, f.AtHead, f.ForgeLag = "STALE_BASE", true, true }), AutoPolicy{MaxRounds: 5}, autoMemory{Last: "sync:@h1"}, autoAct{Kind: actPoll}},
		// A ticket on hold pauses the loop, whatever its next step.
		{with(open, func(f *Facts) { f.Hold, f.Checks = true, "fail" }), AutoPolicy{MaxRounds: 5}, autoMemory{}, pause(StopHold)},
		{Facts{ID: "ACM-1", Agent: "working", Stage: "work", Hold: true}, AutoPolicy{}, autoMemory{}, pause(StopHold)},
		// address answered every finding without a new head: running it
		// again on the same head would change nothing.
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "FINDINGS", true }), AutoPolicy{MaxRounds: 5}, autoMemory{Last: "run:address@h1"}, pause(StopNoProgress)},
	}
	for _, tc := range cases {
		if got := autoStep(NextStep(tc.f), tc.f, tc.p, tc.m); got != tc.want {
			t.Errorf("%+v %+v %+v: got %+v, want %+v", tc.f, tc.p, tc.m, got, tc.want)
		}
	}
}

// simWorld is a ticket that moves as a script says: each agent turn and
// each poll applies the next change.
type simWorld struct {
	f       Facts
	started bool
	wanted  bool
	turns   []func(*Facts)
	polls   []func(*Facts)
	acts    []string
	mem     autoMemory
	stops   []string
	pauses  []string
	looks   int
	// slot is whether the loop holds a slot; slots counts the takes.
	slot  bool
	slots int
	// flip, when set, changes the facts on the confirming look.
	flip     func(*Facts)
	forgeErr int
	// failSync makes that many syncs fail, as with changes in the worktree.
	failSync int
	resumes  int
}

const simPoll = 30 * time.Second

func (w *simWorld) look(_ context.Context, fetch bool) (autoView, error) {
	w.looks++
	if w.forgeErr > 0 {
		w.forgeErr--
		return autoView{}, errForge{errors.New("HTTP 502")}
	}
	if fetch && w.flip != nil {
		w.flip(&w.f)
		w.flip = nil
	}
	n := NextStep(w.f)
	return autoView{Facts: w.f, Next: n, Phase: PhaseOf(w.f, n), Pane: "w1:p10", Started: w.started, Wanted: w.wanted}, nil
}

func (w *simWorld) waitAgent(context.Context, string) error {
	if len(w.turns) == 0 {
		return errors.New("the script has no turn left")
	}
	w.turns[0](&w.f)
	w.turns = w.turns[1:]
	return nil
}

func (w *simWorld) sleep(_ context.Context, d time.Duration) error {
	if d == simPoll {
		if len(w.polls) == 0 {
			return errors.New("the script has no poll left")
		}
		w.polls[0](&w.f)
		w.polls = w.polls[1:]
	}
	return nil
}

func (w *simWorld) act(_ context.Context, a autoAct) error {
	if a.Kind == actSync {
		if w.failSync > 0 {
			w.failSync--
			return errStep{error: errors.New("ACM-1 has uncommitted changes"), stop: StopSyncFailed}
		}
		w.acts = append(w.acts, "sync")
		w.f.Head, w.f.Verdict, w.f.AtHead, w.f.Checks = "h2", "", false, "pending"
		return nil
	}
	name := a.Kind
	if a.Stage != "" {
		name += ":" + a.Stage
	}
	w.acts = append(w.acts, name)
	w.f.Agent = "working"
	switch a.Kind {
	case actStart:
		w.started, w.f.Stage = true, "start"
	case actApprove:
		w.f.Stage = "work"
	default:
		w.f.Stage = a.Stage
	}
	return nil
}

func (w *simWorld) remember(m autoMemory, _ ...string)      { w.mem = m }
func (w *simWorld) stopped(stop string, _ autoView)         { w.stops = append(w.stops, stop) }
func (w *simWorld) paused(why string, _ autoView, _ string) { w.pauses = append(w.pauses, why) }
func (w *simWorld) resumed()                                { w.resumes++ }
func (w *simWorld) dropSlot()                               { w.slot = false }
func (w *simWorld) now() time.Time                          { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func (w *simWorld) holdSlot(context.Context) (bool, error) {
	if !w.slot {
		w.slot = true
		w.slots++
	}
	return false, nil
}

func idle(status string) func(*Facts) {
	return func(f *Facts) { f.Agent, f.LastStatus = "idle", status }
}

// pushed is an agent turn that ends with a new head and pending checks.
func pushed(head string) func(*Facts) {
	return func(f *Facts) {
		f.Agent, f.LastStatus = "idle", "READY_FOR_REVIEW"
		f.PRState, f.Head, f.Checks, f.AtHead, f.Waited = "OPEN", head, "pending", false, 0
	}
}

func verdict(v string) func(*Facts) {
	return func(f *Facts) { f.Agent, f.Verdict, f.AtHead = "idle", v, true }
}

func TestAutoLoopDrivesToReadyForHuman(t *testing.T) {
	w := &simWorld{
		f:      Facts{ID: "ACM-1", Behind: -1, BotWait: 15 * time.Minute},
		wanted: true,
		turns: []func(*Facts){
			idle("AWAITING_APPROVAL"), // the plan
			pushed("h1"),              // work-ticket opens the pull request
			pushed("h2"),              // address after the bot's findings
		},
		polls: []func(*Facts){
			func(f *Facts) { f.Checks = "ok" }, // CI passes on h1
			verdict("FINDINGS"),                // the bot reviews h1
			func(f *Facts) { f.Checks = "ok" }, // CI passes on h2
			verdict("CLEAR"),                   // the bot clears h2
		},
	}
	res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{ApprovePlan: true, MaxRounds: 5}, autoMemory{}, simPoll, false, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"start", "approve", "run:address"}
	if !slices.Equal(w.acts, want) {
		t.Errorf("acts: %v, want %v", w.acts, want)
	}
	if res.Stop != StopReadyForHuman || res.Rounds != 1 || len(res.Steps) != 3 || !slices.Equal(w.stops, []string{StopReadyForHuman}) {
		t.Errorf("result: %+v, stops %v", res, w.stops)
	}
	// A slot counts an agent at work: one from the start through the plan's
	// approval, given up while the loop waits for CI and the bot, and one
	// more for address.
	if w.slots != 2 || w.slot {
		t.Errorf("slots taken %d, held at the end %v", w.slots, w.slot)
	}
}

func TestAutoLoopReviewsWithoutABot(t *testing.T) {
	w := &simWorld{
		f:       Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", LastStatus: "READY_FOR_REVIEW", Head: "h1", Checks: "ok", Behind: -1},
		started: true, wanted: true,
		turns: []func(*Facts){verdict("READY_FOR_HUMAN")},
	}
	res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{MaxRounds: 5}, autoMemory{}, simPoll, false, false)
	if err != nil || res.Stop != StopReadyForHuman || !slices.Equal(w.acts, []string{"run:review"}) {
		t.Errorf("%+v %v %v", res, err, w.acts)
	}
}

func TestAutoLoopPausesForYou(t *testing.T) {
	plan := Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL", Behind: -1, BotWait: time.Hour}
	t.Run("a question, then on by itself", func(t *testing.T) {
		w := &simWorld{f: Facts{ID: "ACM-1", Agent: "blocked", Stage: "start", Behind: -1}, started: true, wanted: true,
			polls: []func(*Facts){
				func(*Facts) {},                         // still asking
				func(f *Facts) { f.Agent = "working" },  // you answered in its tab
				func(f *Facts) { f.PRState = "MERGED" }, // merged
			},
			turns: []func(*Facts){idle("AWAITING_APPROVAL")}}
		res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{ApprovePlan: true}, autoMemory{}, simPoll, false, false)
		if err == nil || !slices.Equal(w.pauses, []string{StopNeedsYou}) {
			t.Fatalf("%+v %v pauses %v", res, err, w.pauses)
		}
		// After the answer the plan came, and auto approved it.
		if !slices.Equal(w.acts, []string{"approve"}) {
			t.Errorf("acts: %v", w.acts)
		}
	})
	t.Run("a plan without approve-plan", func(t *testing.T) {
		w := &simWorld{f: plan, started: true, wanted: true, polls: []func(*Facts){
			func(*Facts) {},
			func(f *Facts) { f.Agent, f.Stage = "working", "work" }, // you approved it
		}, turns: []func(*Facts){pushed("h1")}}
		_, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, false, false)
		if err == nil || !slices.Equal(w.pauses, []string{StopPlan}) || len(w.acts) != 0 {
			t.Errorf("%v pauses %v acts %v", err, w.pauses, w.acts)
		}
	})
	t.Run("round limit", func(t *testing.T) {
		w := &simWorld{f: Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "ok", Head: "h1", Behind: -1, Verdict: "FINDINGS", AtHead: true, BotWait: time.Hour},
			started: true, wanted: true, turns: []func(*Facts){pushed("h2")}, polls: []func(*Facts){
				func(f *Facts) { f.Checks = "ok" }, verdict("FINDINGS"),
			}}
		_, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{MaxRounds: 1}, autoMemory{}, simPoll, false, false)
		if err == nil || !slices.Equal(w.pauses, []string{StopRoundLimit}) || !slices.Equal(w.acts, []string{"run:address"}) || w.slot {
			t.Errorf("%v pauses %v acts %v slot %v", err, w.pauses, w.acts, w.slot)
		}
	})
	t.Run("the decision changed before acting", func(t *testing.T) {
		w := &simWorld{f: plan, started: true, wanted: true, flip: func(f *Facts) { f.Agent = "blocked" }}
		_, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{ApprovePlan: true}, autoMemory{}, simPoll, false, false)
		if err == nil || !slices.Equal(w.pauses, []string{StopNeedsYou}) || len(w.acts) != 0 {
			t.Errorf("%v pauses %v acts %v", err, w.pauses, w.acts)
		}
	})
}

func TestAutoLoopStops(t *testing.T) {
	merged := Facts{ID: "ACM-1", PRState: "MERGED", Agent: "idle", Behind: -1}
	t.Run("stopped", func(t *testing.T) {
		w := &simWorld{f: merged, started: true}
		res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, false, false)
		if err != nil || res.Stop != StopStopped {
			t.Errorf("%+v %v", res, err)
		}
	})
	t.Run("the forge recovers", func(t *testing.T) {
		w := &simWorld{f: merged, started: true, wanted: true, forgeErr: maxForgeFailures - 1, polls: make([]func(*Facts), maxForgeFailures)}
		for i := range w.polls {
			w.polls[i] = func(*Facts) {}
		}
		if res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, false, false); err != nil || res.Stop != StopMerged {
			t.Errorf("%+v %v", res, err)
		}
	})
	t.Run("the forge keeps failing", func(t *testing.T) {
		w := &simWorld{f: merged, started: true, wanted: true, forgeErr: maxForgeFailures, polls: make([]func(*Facts), maxForgeFailures)}
		for i := range w.polls {
			w.polls[i] = func(*Facts) {}
		}
		var fe errForge
		if _, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, false, false); !errors.As(err, &fe) {
			t.Errorf("error: %v", err)
		}
	})
}

func TestAutoLoopOnce(t *testing.T) {
	w := &simWorld{f: Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL", Behind: -1}, started: true}
	res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{ApprovePlan: true}, autoMemory{}, simPoll, true, false)
	if err != nil || res.Did == nil || res.Did.Action != actApprove || !slices.Equal(w.acts, []string{"approve"}) || w.slots != 0 {
		t.Errorf("approve: %+v %v", res, err)
	}
	w = &simWorld{f: Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "pending", Behind: -1}, started: true}
	res, err = autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, true, false)
	if err != nil || res.Did != nil || res.Wait != ReasonCIPending || len(w.stops) != 0 {
		t.Errorf("wait: %+v %v", res, err)
	}
	w = &simWorld{f: Facts{ID: "ACM-1", Agent: "blocked", Behind: -1}, started: true}
	res, err = autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, true, false)
	if err != nil || res.Stop != StopNeedsYou || len(w.stops) != 0 || len(w.pauses) != 0 {
		t.Errorf("pause: %+v %v", res, err)
	}
}

func TestAutoSlots(t *testing.T) {
	common := t.TempDir()
	held, err := lock.Try(common, "slot-0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := config.Config{Auto: config.Auto{MaxParallel: 1}}
	c := &Ctx{App: &App{UI: &output.UI{Err: &bytes.Buffer{}}, Now: time.Now}, Ctx: ctx, Repo: gitx.Repo{Common: common}, Cfg: cfg}
	w := &ctxWorld{c: c, id: "ACM-2"}
	done := make(chan error, 1)
	go func() {
		waited, err := w.holdSlot(ctx)
		if err == nil && !waited {
			err = errors.New("did not say it waited")
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("took a slot while the only one is held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	held.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never took the freed slot")
	}
	w.dropSlot()
	if _, err := lock.Try(common, "slot-0"); err != nil {
		t.Errorf("the slot stays held after dropSlot: %v", err)
	}
	// While it waited, a ticket with no worktree showed in queued/.
	if entries, _ := os.ReadDir(queuedDir(common)); len(entries) != 0 {
		t.Errorf("queued/ keeps %v after the slot came", entries)
	}
}

// A sync that cannot happen for now pauses the loop once, with no round
// spent, and the loop tries it again at each look until it works.
func TestAutoLoopRetriesASyncThatFails(t *testing.T) {
	w := &simWorld{
		f:       Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "ok", Head: "h1", Behind: 2, Verdict: "STALE_BASE", AtHead: true, BotWait: time.Hour},
		started: true, wanted: true, failSync: 2,
		polls: []func(*Facts){
			func(*Facts) {}, func(*Facts) {}, // two failed tries
			func(f *Facts) { f.Checks = "ok" },      // CI passes on the merge
			func(f *Facts) { f.PRState = "MERGED" }, // and it merges
		},
	}
	_, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{MaxRounds: 5}, autoMemory{}, simPoll, false, false)
	if err != nil && !strings.Contains(err.Error(), "the script has no poll left") {
		t.Fatal(err)
	}
	if !slices.Equal(w.pauses, []string{StopSyncFailed}) || !slices.Equal(w.acts, []string{"sync"}) || w.resumes != 1 || w.mem.Rounds != 0 {
		t.Errorf("pauses %v, acts %v, resumes %d, rounds %d", w.pauses, w.acts, w.resumes, w.mem.Rounds)
	}
	// One step that tkt auto --once cannot take reports why.
	w = &simWorld{f: Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "ok", Head: "h1", Behind: 2, Verdict: "STALE_BASE", AtHead: true}, started: true, failSync: 1}
	if _, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{MaxRounds: 5}, autoMemory{}, simPoll, true, false); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("--once: %v", err)
	}
}
