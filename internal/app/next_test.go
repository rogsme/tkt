package app

import (
	"testing"
	"time"
)

func TestNextStepTable(t *testing.T) {
	cases := []struct {
		f      Facts
		want   string
		kind   string
		reason string
	}{
		{Facts{PRState: "MERGED", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE"}, "tkt done ACM-1", "command", ReasonMerged},
		{Facts{PRState: "CLOSED"}, "closed without merging", "human", ReasonClosed},
		{Facts{PRState: "OPEN", Agent: "working", Checks: "ok", Mergeable: "MERGEABLE"}, "wait (agent working)", "wait", ReasonAgentWorking},
		{Facts{PRState: "OPEN", Agent: "blocked", Checks: "ok", Mergeable: "MERGEABLE"}, "answer the agent in its tab", "human", ReasonAgentBlocked},
		{Facts{}, "tkt resume ACM-1", "command", ReasonNoAgent},
		{Facts{Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, "read the plan with tkt show ACM-1, then tkt approve ACM-1", "human", ReasonPlanAwaitsApproval},
		{Facts{Agent: "idle", LastStatus: "BLOCKED"}, "read the agent's reason in its tab", "human", ReasonStatusBlocked},
		{Facts{Agent: "idle", LastStatus: "SETUP_REQUIRED"}, "read the agent's reason in its tab", "human", ReasonStatusBlocked},
		{Facts{Agent: "idle"}, "check the agent in its tab", "human", ReasonCheckAgent},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", LastStatus: "BLOCKED"}, "read the agent's reason in its tab", "human", ReasonStatusBlocked},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", LastStatus: "AWAITING_POST_APPROVAL"}, "approve or discard the review draft in its tab", "human", ReasonReviewAwaitsApproval},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "pending", Verdict: "FINDINGS", AtHead: true, Mergeable: "MERGEABLE"}, "wait for CI", "wait", ReasonCIPending},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "STALE_BASE", AtHead: true, Mergeable: "MERGEABLE"}, "tkt sync ACM-1", "command", ReasonBaseMoved},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "CONFLICTING"}, "tkt sync ACM-1", "command", ReasonBaseMoved},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", LastStatus: "BLOCKED_FOR_SYNC", Behind: 2}, "tkt sync ACM-1", "command", ReasonBaseMoved},
		// git merge-tree finds a conflict before the forge reports one.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE", BaseMerge: "conflict", Behind: 3}, "tkt sync ACM-1", "command", ReasonBaseMoved},
		// A base that merges clean is no reason to sync on its own.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE", BaseMerge: "clean", Behind: 3}, "tkt run ACM-1 verify", "command", ReasonBotClear},
		// review-pr also says BLOCKED_FOR_SYNC while CI is not green; with
		// nothing behind, the review is what is missing.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", LastStatus: "BLOCKED_FOR_SYNC", Behind: 0}, "tkt run ACM-1 review", "command", ReasonNeedsReview},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", LastStatus: "HUMAN_QA_FAILED", Verdict: "READY_FOR_HUMAN", AtHead: true}, "tkt run ACM-1 address", "command", ReasonQAFailed},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "FINDINGS", AtHead: true, Mergeable: "MERGEABLE"}, "tkt run ACM-1 address", "command", ReasonFindings},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CHANGES_REQUESTED", AtHead: true}, "tkt run ACM-1 address", "command", ReasonFindings},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "fail", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE"}, "tkt run ACM-1 ci", "command", ReasonChecksFail},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE"}, "tkt run ACM-1 verify", "command", ReasonBotClear},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true}, "tkt run ACM-1 verify", "command", ReasonReviewClear},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: false, Mergeable: "MERGEABLE"}, "tkt run ACM-1 review", "command", ReasonNeedsReview},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", BotWait: 15 * time.Minute, Waited: 5 * time.Minute}, "wait for the review bot", "wait", ReasonAwaitingBot},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", BotWait: 15 * time.Minute, Waited: 15 * time.Minute}, "the review bot has not reviewed this head; check its CI job, or run tkt run ACM-1 review", "human", ReasonBotSilent},
		{Facts{PRState: "OPEN", Agent: "idle", Background: true}, "wait (the agent waits on a background task)", "wait", ReasonAgentWaiting},
	}
	for _, tc := range cases {
		tc.f.ID = "ACM-1"
		got := NextStep(tc.f)
		if got.Text != tc.want || got.Kind != tc.kind || got.Reason != tc.reason {
			t.Errorf("%+v: got %q (%s, %s), want %q (%s, %s)", tc.f, got.Text, got.Kind, got.Reason, tc.want, tc.kind, tc.reason)
		}
		if got.Kind == "command" && len(got.Argv) == 0 {
			t.Errorf("%q has no argv", got.Text)
		}
	}
	if got := NextStep(Facts{ID: "ACM-1", PRState: "OPEN", Agent: "working"}); len(got.Argv) != 3 || got.Argv[1] != "wait" {
		t.Errorf("a working agent's next step is not tkt wait: %+v", got)
	}
}

func TestPhaseOf(t *testing.T) {
	cases := []struct {
		f    Facts
		want string
	}{
		{Facts{PRState: "MERGED"}, PhaseMerged},
		{Facts{PRState: "CLOSED"}, PhaseClosed},
		{Facts{Launch: "launching"}, PhaseStarting},
		{Facts{Launch: "tab", Agent: "idle"}, PhaseStarting},
		{Facts{Launch: "not_ready", Agent: "idle"}, PhaseNeedsYou},
		{Facts{Launch: "prompt_uncertain", Agent: "idle"}, PhaseNeedsYou},
		{Facts{AutoWait: "slot"}, PhaseQueued},
		{Facts{AutoWait: "slot", Agent: "working", Stage: "start", Launch: "submitted"}, PhasePlanning},
		{Facts{Agent: "working", Stage: "start", Launch: "launching"}, PhasePlanning},
		{Facts{Agent: "working", Stage: "work"}, PhaseBuilding},
		{Facts{Agent: "working", Stage: "review"}, PhaseReviewing},
		{Facts{Agent: "working", Stage: "address"}, PhaseFixing},
		{Facts{Agent: "working", Stage: "verify"}, PhaseVerifying},
		{Facts{Agent: "working", Stage: "custom"}, PhaseWorking},
		{Facts{Agent: "blocked"}, PhaseNeedsYou},
		{Facts{Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, PhaseAwaitingApproval},
		{Facts{Agent: "idle"}, PhaseNeedsYou},
		{Facts{}, PhaseNoAgent},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "pending"}, PhaseCI},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", BotWait: time.Hour}, PhaseWaitingReview},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true}, PhaseReadyForHuman},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", BotWait: time.Hour, Waited: time.Hour}, PhaseNeedsYou},
		{Facts{PRState: "OPEN", Agent: "idle", Background: true, Stage: "work"}, PhaseBuilding},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "FINDINGS", AtHead: true}, PhaseNeedsFix},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true}, PhaseReadyForHuman},
	}
	for _, tc := range cases {
		tc.f.ID = "ACM-1"
		if tc.f.Behind == 0 {
			tc.f.Behind = -1
		}
		if got := PhaseOf(tc.f, NextStep(tc.f)); got != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.f, got, tc.want)
		}
	}
}
