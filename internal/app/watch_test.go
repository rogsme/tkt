package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/runner"
	"gitlab.com/rogs/tkt/internal/wt"
)

func TestWatchStepTable(t *testing.T) {
	open := Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "ok", Head: "h1", Behind: -1, BotWait: time.Hour}
	with := func(f Facts, edit func(*Facts)) Facts { edit(&f); return f }
	failing := with(open, func(f *Facts) { f.Checks = "fail" })
	p := WatchPolicy{MaxRounds: 2, DoneAfterMerge: true}
	leave := func(why string) watchAct { return watchAct{Why: why} }
	pause := func(why string) watchAct { return watchAct{Why: why, Pause: true} }
	cases := []struct {
		f    Facts
		p    WatchPolicy
		m    watchMemory
		want watchAct
	}{
		// finish-ticket's Status: DONE after the merge removes the ticket; a
		// merge without it, such as one in the forge's page, waits for you.
		{with(open, func(f *Facts) { f.PRState, f.LastStatus = "MERGED", "DONE" }), p, watchMemory{}, watchAct{Kind: actDone, Why: ReasonMerged}},
		{with(open, func(f *Facts) { f.PRState = "MERGED" }), p, watchMemory{}, leave(ReasonMerged)},
		{with(open, func(f *Facts) { f.PRState, f.LastStatus = "MERGED", "DONE" }), WatchPolicy{MaxRounds: 2}, watchMemory{}, leave(ReasonMerged)},
		// A moved base: clean merges with no agent, even past the round
		// limit; a conflict runs resolve, which counts a round.
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "STALE_BASE", true }), p, watchMemory{}, watchAct{Kind: actSync, Why: ReasonBaseMoved}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead, f.BaseMerge = "STALE_BASE", true, "clean" }), p, watchMemory{Rounds: 2}, watchAct{Kind: actSync, Why: ReasonBaseMoved}},
		{with(open, func(f *Facts) { f.BaseMerge = "conflict" }), p, watchMemory{}, watchAct{Kind: actRun, Stage: "resolve", Why: ReasonBaseMoved}},
		{with(open, func(f *Facts) { f.BaseMerge = "conflict" }), p, watchMemory{Rounds: 2}, pause(StopRoundLimit)},
		// Failing checks get ci once per head, up to the round limit.
		{failing, p, watchMemory{}, watchAct{Kind: actRun, Stage: "ci", Why: ReasonChecksFail}},
		{failing, p, watchMemory{Last: "run:ci@h1", Rounds: 1}, pause(StopNoProgress)},
		{with(failing, func(f *Facts) { f.Head = "h2" }), p, watchMemory{Last: "run:ci@h1", Rounds: 1}, watchAct{Kind: actRun, Stage: "ci", Why: ReasonChecksFail}},
		{failing, p, watchMemory{Rounds: 2}, pause(StopRoundLimit)},
		// An agent that stopped minutes ago may still have a person in its
		// session; a sync, which needs no agent, goes ahead.
		{with(failing, func(f *Facts) { f.Idle = time.Minute }), p, watchMemory{}, leave(WatchSettling)},
		{with(failing, func(f *Facts) { f.Idle = time.Hour }), p, watchMemory{}, watchAct{Kind: actRun, Stage: "ci", Why: ReasonChecksFail}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead, f.Idle = "STALE_BASE", true, time.Minute }), p, watchMemory{}, watchAct{Kind: actSync, Why: ReasonBaseMoved}},
		// The forge still shows the head before watch's own sync: nothing
		// it says counts yet, not even that the sync changed nothing.
		{with(open, func(f *Facts) { f.Verdict, f.AtHead, f.ForgeLag = "STALE_BASE", true, true }), p, watchMemory{Last: "sync:@h1"}, leave(WatchForgeLag)},
		// What watch leaves alone.
		{with(failing, func(f *Facts) { f.Hold = true }), p, watchMemory{}, leave(WatchHold)},
		{with(failing, func(f *Facts) { f.AutoRunning = true }), p, watchMemory{}, leave(WatchAuto)},
		{with(failing, func(f *Facts) { f.Launch = "launching" }), p, watchMemory{}, leave(WatchLaunch)},
		{with(failing, func(f *Facts) { f.Agent = "working" }), p, watchMemory{}, leave(ReasonAgentWorking)},
		{with(failing, func(f *Facts) { f.Agent = "blocked" }), p, watchMemory{}, leave(ReasonAgentBlocked)},
		// Findings come before the checks in the rule list, and they are
		// address's, which only you or tkt auto run.
		{with(failing, func(f *Facts) { f.Verdict, f.AtHead = "FINDINGS", true }), p, watchMemory{}, leave(ReasonFindings)},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead = "CLEAR", true }), p, watchMemory{}, leave(ReasonBotClear)},
		{Facts{ID: "ACM-1", Agent: "idle", LastStatus: "AWAITING_APPROVAL", Behind: -1}, p, watchMemory{}, leave(ReasonPlanAwaitsApproval)},
		// A merge order you left goes ahead without settling, once per head;
		// on a branch behind its base, the sync comes first.
		{with(open, func(f *Facts) {
			f.Verdict, f.AtHead, f.MergeSteps, f.Idle = "READY_FOR_HUMAN", true, "1,2", time.Minute
		}), p, watchMemory{}, watchAct{Kind: actMerge, Why: ReasonMergeDue}},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead, f.MergeSteps = "READY_FOR_HUMAN", true, "1,2" }), p, watchMemory{Last: "merge:@h1"}, pause(StopNoProgress)},
		{with(open, func(f *Facts) { f.Verdict, f.AtHead, f.MergeSteps, f.Behind = "READY_FOR_HUMAN", true, "1,2", 2 }), p, watchMemory{}, watchAct{Kind: actSync, Why: ReasonBaseMoved}},
		{with(open, func(f *Facts) {
			f.Verdict, f.AtHead, f.MergeSteps, f.Checks = "READY_FOR_HUMAN", true, "1,2", "pending"
		}), p, watchMemory{}, leave(ReasonCIPending)},
		// A clean sync carried the order to a head no review has seen:
		// review-pr reviews it once, past the round limit, which counts only
		// fixes. Without an order, reviews stay yours.
		{with(open, func(f *Facts) { f.BotWait, f.MergeSteps, f.MergeHead = 0, "1,2", "h0" }), p, watchMemory{Rounds: 2}, watchAct{Kind: actRun, Stage: "review", Why: ReasonNeedsReview}},
		{with(open, func(f *Facts) { f.BotWait, f.MergeSteps, f.MergeHead, f.Idle = 0, "1,2", "h0", time.Minute }), p, watchMemory{}, leave(WatchSettling)},
		{with(open, func(f *Facts) { f.BotWait, f.MergeSteps, f.MergeHead = 0, "1,2", "h0" }), p, watchMemory{Last: "run:review@h1"}, pause(StopNoProgress)},
		{with(open, func(f *Facts) { f.BotWait = 0 }), p, watchMemory{}, leave(ReasonNeedsReview)},
	}
	for _, tc := range cases {
		if got := watchStep(NextStep(tc.f), tc.f, tc.p, tc.m); got != tc.want {
			t.Errorf("%+v %+v %+v: got %+v, want %+v", tc.f, tc.p, tc.m, got, tc.want)
		}
	}
}

func TestWatchPausesNeedYou(t *testing.T) {
	f := Facts{ID: "ACM-1", PRState: "OPEN", Agent: "idle", Checks: "fail", Head: "h1", Behind: -1, WatchPause: StopNoProgress}
	if got := PhaseOf(f, NextStep(f)); got != PhaseNeedsYou {
		t.Errorf("a ticket watch paused on is %s", got)
	}
	if got := watchPauseText("ACM-1", StopNoProgress, "run:ci@h1"); got != "tkt watch ran ci on this head, and the checks still fail; read what the agent said in its tab" {
		t.Errorf("pause text: %q", got)
	}
	if got := watchPauseText("ACM-1", StopNoProgress, "run:review@h1"); !strings.Contains(got, "ran review on this head for your merge order") {
		t.Errorf("pause text after a review: %q", got)
	}
}

func TestWatchEnvLeavesTheTicketBehind(t *testing.T) {
	got := watchEnv([]string{"HOME=/home/dev", "HERDR_ENV=1", "HERDR_PANE_ID=w1:p12", "HERDR_TAB_ID=w1:t12", "HERDR_WORKSPACE_ID=w1", "TMPDIR=/tmp/tkt-acme-app-ACM-12-123"})
	want := []string{"HOME=/home/dev", "TMPDIR=/tmp"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A workspace tkt watch opened stays its workspace while herdr has it, even
// before herdr reports a pane of it in the repository, so a pass never opens
// a second one.
func TestFindWorkspaceKeepsTheOneItOpened(t *testing.T) {
	panes := `{"result":{"panes":[{"pane_id":"w7:p1","workspace_id":"w7","cwd":"/home/acme"},{"pane_id":"w2:p1","workspace_id":"w2","cwd":"/srv/other"}]}}`
	r := runner.Func(func(_ context.Context, cmd runner.Cmd) runner.Result {
		switch cmd.Argv[0] {
		case "herdr":
			return runner.Result{Stdout: []byte(panes)}
		case "wt":
			return runner.Result{Stdout: []byte(`[{"branch":"main","path":"/r"}]`)}
		}
		return runner.Result{Code: 1}
	})
	c := &Ctx{App: &App{R: r}, Ctx: context.Background(), Repo: gitx.Repo{Root: "/r"}, H: herdr.Client{R: r}, WT: wt.Client{R: r, Root: "/r"}}
	for opened, want := range map[string]string{"": "", "w7": "w7", "w9": ""} {
		if ws, err := c.findWorkspace(opened); err != nil || ws != want {
			t.Errorf("findWorkspace(%q) = %q, %v; want %q", opened, ws, err, want)
		}
	}
}

func TestLeaveTicketTmp(t *testing.T) {
	for in, want := range map[string]string{
		"/tmp/tkt-acme-app-ACM-12-123":  "/tmp",
		"/var/tmp/tkt-acme-app-ACM-1-9": "/var/tmp",
		"/tmp":                          "",
		"":                              "",
	} {
		got := ""
		LeaveTicketTmp(func(string) string { return in }, func(_, v string) error { got = v; return nil })
		if got != want {
			t.Errorf("TMPDIR=%q became %q, want %q", in, got, want)
		}
	}
}
