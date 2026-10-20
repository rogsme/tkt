package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
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
	cases := []struct {
		f    Facts
		p    AutoPolicy
		m    autoMemory
		want autoAct
	}{
		{Facts{ID: "ACM-1", Agent: "working", Stage: "start"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actWait}},
		{Facts{ID: "ACM-1", Agent: "blocked"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopNeedsYou}},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopPlan}},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, AutoPolicy{ApprovePlan: true}, autoMemory{}, autoAct{Kind: actApprove}},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, AutoPolicy{ApprovePlan: true}, autoMemory{Last: "approve:@"}, autoAct{Kind: actStop, Stop: StopNoProgress}},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "BLOCKED"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopNeedsYou}},
		{Facts{ID: "ACM-1"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopAgentMissing}},
		{Facts{ID: "ACM-1", Agent: "idle", Launch: "not_ready"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopNeedsYou}},
		{Facts{ID: "ACM-1", Launch: "launching"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actPoll}},
		{Facts{ID: "ACM-1", Agent: "unknown"}, AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopUnknown}},
		{with(open, func(f *Facts) { f.Checks = "pending" }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actPoll}},
		{with(open, func(f *Facts) { f.BotWait = time.Hour }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actPoll}},
		{open, AutoPolicy{}, autoMemory{}, autoAct{Kind: actRun, Stage: "review"}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "CLEAR", true }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actRun, Stage: "review"}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "FINDINGS", true }), AutoPolicy{MaxRounds: 2}, autoMemory{Rounds: 1}, autoAct{Kind: actRun, Stage: "address"}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "FINDINGS", true }), AutoPolicy{MaxRounds: 2}, autoMemory{Rounds: 2}, autoAct{Kind: actStop, Stop: StopRoundLimit}},
		{with(open, func(f *Facts) { f.Mergeable = "CONFLICTING" }), AutoPolicy{MaxRounds: 5}, autoMemory{}, autoAct{Kind: actRun, Stage: "sync"}},
		{with(open, func(f *Facts) { f.Checks = "fail" }), AutoPolicy{MaxRounds: 5}, autoMemory{}, autoAct{Kind: actRun, Stage: "ci"}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "READY_FOR_HUMAN", true }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopReadyForHuman}},
		{with(open, func(f *Facts) { f.LastStatus = "AWAITING_POST_APPROVAL" }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopNeedsYou}},
		{with(open, func(f *Facts) { f.PRState = "MERGED" }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopMerged}},
		{with(open, func(f *Facts) { f.PRState = "CLOSED" }), AutoPolicy{}, autoMemory{}, autoAct{Kind: actStop, Stop: StopClosed}},
		// review-pr ended BLOCKED_FOR_SYNC with nothing to sync: running it
		// again on the same head would change nothing.
		{with(open, func(f *Facts) { f.LastStatus, f.Behind = "BLOCKED_FOR_SYNC", 0 }), AutoPolicy{}, autoMemory{Last: "run:review@h1"}, autoAct{Kind: actStop, Stop: StopNoProgress}},
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
	looks   int
	// flip, when set, changes the facts on the confirming look.
	flip     func(*Facts)
	forgeErr int
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

func (w *simWorld) remember(m autoMemory, _ ...string) { w.mem = m }
func (w *simWorld) stopped(stop string, _ autoView)    { w.stops = append(w.stops, stop) }
func (w *simWorld) now() time.Time                     { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

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
			idle("AWAITING_APPROVAL"),    // the plan
			pushed("h1"),                 // work-ticket opens the pull request
			pushed("h2"),                 // address after the bot's findings
			verdict("CHANGES_REQUESTED"), // review-pr after the bot's CLEAR
			pushed("h3"),                 // address after review-pr
			verdict("READY_FOR_HUMAN"),   // review-pr after the bot stayed silent
		},
		polls: []func(*Facts){
			func(f *Facts) { f.Checks = "ok" }, // CI passes on h1
			verdict("FINDINGS"),                // the bot reviews h1
			func(f *Facts) { f.Checks = "ok" }, // CI passes on h2
			verdict("CLEAR"),                   // the bot clears h2
			func(f *Facts) { f.Checks = "ok" }, // CI passes on h3
			func(f *Facts) { f.Waited = 5 * time.Minute },
			func(f *Facts) { f.Waited = 15 * time.Minute }, // the bot stays silent
		},
	}
	res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{ApprovePlan: true, MaxRounds: 5}, autoMemory{}, simPoll, false, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"start", "approve", "run:address", "run:review", "run:address", "run:review"}
	if !slices.Equal(w.acts, want) {
		t.Errorf("acts: %v, want %v", w.acts, want)
	}
	if res.Stop != StopReadyForHuman || res.Rounds != 2 || len(res.Steps) != 6 || !slices.Equal(w.stops, []string{StopReadyForHuman}) {
		t.Errorf("result: %+v, stops %v", res, w.stops)
	}
	if w.mem.Last != "run:review@h3" {
		t.Errorf("memory: %+v", w.mem)
	}
}

func TestAutoLoopStops(t *testing.T) {
	plan := Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL", Behind: -1}
	t.Run("plan without approve-plan", func(t *testing.T) {
		w := &simWorld{f: plan, started: true, wanted: true}
		res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, false, false)
		if err != nil || res.Stop != StopPlan || len(w.acts) != 0 {
			t.Errorf("%+v %v %v", res, err, w.acts)
		}
	})
	t.Run("round limit", func(t *testing.T) {
		w := &simWorld{f: Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "ok", Head: "h1", Behind: -1, Verdict: "FINDINGS", AtHead: true, BotWait: time.Hour},
			started: true, wanted: true, turns: []func(*Facts){pushed("h2")}, polls: []func(*Facts){
				func(f *Facts) { f.Checks = "ok" }, verdict("FINDINGS"),
			}}
		res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{MaxRounds: 1}, autoMemory{}, simPoll, false, false)
		if err != nil || res.Stop != StopRoundLimit || !slices.Equal(w.acts, []string{"run:address"}) {
			t.Errorf("%+v %v %v", res, err, w.acts)
		}
	})
	t.Run("stopped", func(t *testing.T) {
		w := &simWorld{f: plan, started: true}
		res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, false, false)
		if err != nil || res.Stop != StopStopped {
			t.Errorf("%+v %v", res, err)
		}
	})
	t.Run("the decision changed before acting", func(t *testing.T) {
		w := &simWorld{f: plan, started: true, wanted: true, flip: func(f *Facts) { f.Agent = "blocked" }}
		res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{ApprovePlan: true}, autoMemory{}, simPoll, false, false)
		if err != nil || res.Stop != StopNeedsYou || len(w.acts) != 0 {
			t.Errorf("%+v %v %v", res, err, w.acts)
		}
	})
	t.Run("the forge recovers", func(t *testing.T) {
		w := &simWorld{f: plan, started: true, wanted: true, forgeErr: maxForgeFailures - 1, polls: make([]func(*Facts), maxForgeFailures)}
		for i := range w.polls {
			w.polls[i] = func(*Facts) {}
		}
		if res, err := autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, false, false); err != nil || res.Stop != StopPlan {
			t.Errorf("%+v %v", res, err)
		}
	})
	t.Run("the forge keeps failing", func(t *testing.T) {
		w := &simWorld{f: plan, started: true, wanted: true, forgeErr: maxForgeFailures, polls: make([]func(*Facts), maxForgeFailures)}
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
	if err != nil || res.Did == nil || res.Did.Action != actApprove || !slices.Equal(w.acts, []string{"approve"}) {
		t.Errorf("approve: %+v %v", res, err)
	}
	w = &simWorld{f: Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "pending", Behind: -1}, started: true}
	res, err = autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, true, false)
	if err != nil || res.Did != nil || res.Wait != ReasonCIPending || len(w.stops) != 0 {
		t.Errorf("wait: %+v %v", res, err)
	}
	w = &simWorld{f: Facts{ID: "ACM-1", Agent: "blocked", Behind: -1}, started: true}
	res, err = autoLoop(context.Background(), w, "ACM-1", AutoPolicy{}, autoMemory{}, simPoll, true, false)
	if err != nil || res.Stop != StopNeedsYou || len(w.stops) != 0 {
		t.Errorf("stop: %+v %v", res, err)
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
	go func() { done <- w.takeSlot() }()
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
	c.release()
	if _, err := lock.Try(common, "slot-0"); err != nil {
		t.Errorf("the slot stays held after release: %v", err)
	}
}
