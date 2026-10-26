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
		// A small fix in a split: its session works, waits on you, or
		// stopped with a status other than READY_FOR_REVIEW. One that ended
		// with READY_FOR_REVIEW changes nothing until tkt watch closes it.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "fail", Fix: "working"}, "wait (a fresh session fixes it beside the agent)", "wait", ReasonFixing},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "fail", Fix: "blocked"}, "answer the fix's session in its split", "human", ReasonFixBlocked},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "fail", Fix: "idle", FixStatus: "BLOCKED"}, "read why the fix stopped, in its split", "human", ReasonFixStopped},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "fail", Fix: "idle", FixStatus: "READY_FOR_REVIEW"}, "tkt run ACM-1 ci", "command", ReasonChecksFail},
		// The ticket's own agent working comes first.
		{Facts{PRState: "OPEN", Agent: "working", Fix: "working"}, "wait (agent working)", "wait", ReasonAgentWorking},
		// With CI review on, the bot's CLEAR is ready for a merge order, a
		// head without its review waits for it however long it takes, and
		// a silent bot never brings review-pr in.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, ReviewReady: true, CIReview: true, MergeSteps: "1", MergeHead: "h1"}, "tkt merge ACM-1 --passed 1 --head h1", "command", ReasonMergeDue},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", CIReview: true, MergeSteps: "1", MergeHead: "h0"}, "wait for the review bot", "wait", ReasonAwaitingBot},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", CIReview: true}, "wait for the review bot", "wait", ReasonAwaitingBot},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", CIReview: true, BotWait: time.Minute, Waited: time.Hour}, "the review bot has not reviewed this head; check its CI job", "human", ReasonBotSilent},
		// A sync that failed on the head goes to a fresh session.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "STALE_BASE", AtHead: true, SyncFail: "failed"}, "tkt sync ACM-1", "command", ReasonSyncFailed},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "pending", SyncFail: "failed"}, "wait for CI", "wait", ReasonCIPending},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE"}, "tkt run ACM-1 verify", "command", ReasonBotClear},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true}, "tkt run ACM-1 verify", "command", ReasonReviewClear},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: false, Mergeable: "MERGEABLE"}, "tkt run ACM-1 review", "command", ReasonNeedsReview},
		// With agent verification on, a clean head gets verify-pr, whose
		// report then decides: a failed step goes to address, a pass waits
		// for you to confirm the evidence, and steps it could not run wait
		// for you to run them. A report decides with the feature off too.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, AgentVerify: true}, "tkt run ACM-1 verify", "command", ReasonNeedsVerify},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, AgentVerify: true}, "tkt run ACM-1 verify", "command", ReasonNeedsVerify},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, AgentVerify: true, Verification: "FAILED", VerifyHeld: true}, "tkt run ACM-1 address", "command", ReasonVerifyFailed},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, AgentVerify: true, Verification: "PASSED", VerifyHeld: true},
			"verify-pr passed every step: confirm them from its evidence (tkt show ACM-1 --steps), then tkt merge ACM-1 --passed all --evidence", "human", ReasonVerified},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, Verification: "INCOMPLETE", VerifyHeld: true},
			"run the steps verify-pr could not (tkt show ACM-1 --steps), then tkt merge ACM-1 --passed all", "human", ReasonVerifyIncomplete},
		// A report that no longer holds, after a fix, decides nothing: the
		// new head needs its own review and its own run.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, AgentVerify: true, Verification: "FAILED"}, "tkt run ACM-1 verify", "command", ReasonNeedsVerify},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: false, AgentVerify: true, Verification: "FAILED"}, "tkt run ACM-1 review", "command", ReasonNeedsReview},
		// A yolo ticket gets review-pr's verdict before verify-pr, since
		// finish-ticket merges on it alone; then verify-pr; then the merge,
		// once its report passed every step. Steps it could not run need you.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Yolo: true}, "tkt run ACM-1 review", "command", ReasonNeedsReview},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, ReviewReady: true, Yolo: true}, "tkt run ACM-1 verify", "command", ReasonNeedsVerify},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, ReviewReady: true, Yolo: true, Verification: "PASSED", VerifyHeld: true}, "tkt merge ACM-1 --autonomous", "command", ReasonAutonomousMerge},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Yolo: true, Verification: "PASSED", VerifyHeld: true}, "tkt run ACM-1 review", "command", ReasonNeedsReview},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, ReviewReady: true, Yolo: true, Verification: "INCOMPLETE", VerifyHeld: true}, "run the steps verify-pr could not (tkt show ACM-1 --steps), then tkt merge ACM-1 --passed all", "human", ReasonVerifyIncomplete},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, ReviewReady: true, Head: "h2", MergeSteps: "1", MergeHead: "h1", MergeBy: "autonomous", Yolo: true}, "tkt merge ACM-1 --passed 1 --head h1 --autonomous", "command", ReasonMergeDue},
		// An order you confirmed from the evidence keeps saying so.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, ReviewReady: true, Head: "h1", MergeSteps: "1,2", MergeHead: "h1", MergeBy: "evidence"}, "tkt merge ACM-1 --passed 1,2 --head h1 --evidence", "command", ReasonMergeDue},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", BotWait: 15 * time.Minute, Waited: 5 * time.Minute}, "wait for the review bot", "wait", ReasonAwaitingBot},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", BotWait: 15 * time.Minute, Waited: 15 * time.Minute}, "the review bot has not reviewed this head; check its CI job, or run tkt run ACM-1 review", "human", ReasonBotSilent},
		{Facts{PRState: "OPEN", Agent: "idle", Background: true}, "wait (the agent waits on a background task)", "wait", ReasonAgentWaiting},
		// A merge order waits for CI, syncs a branch behind its base, and
		// is due once the checks pass at a head reviewed clean.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "pending", Verdict: "READY_FOR_HUMAN", AtHead: true, Head: "h1", MergeSteps: "1,2"}, "wait for CI", "wait", ReasonCIPending},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, Head: "h1", MergeSteps: "1,2", Behind: 1}, "tkt sync ACM-1", "command", ReasonBaseMoved},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, ReviewReady: true, Head: "h1", MergeSteps: "1,2", MergeHead: "h1"}, "tkt merge ACM-1 --passed 1,2 --head h1", "command", ReasonMergeDue},
		// finish-ticket merges on review-pr's READY_FOR_HUMAN alone, so a head
		// only the bot cleared gets that review first; one that has it is
		// due, whichever review came last.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "none", Verdict: "CLEAR", AtHead: true, Head: "h1", MergeSteps: "1", MergeHead: "h1"}, "tkt run ACM-1 review", "command", ReasonNeedsReview},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "none", Verdict: "CLEAR", AtHead: true, ReviewReady: true, Head: "h1", MergeSteps: "1", MergeHead: "h1"}, "tkt merge ACM-1 --passed 1 --head h1", "command", ReasonMergeDue},
		// An order that a clean sync carried to h2 names the head the steps
		// passed on, and waits for a review of the new head first.
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, ReviewReady: true, Head: "h2", MergeSteps: "1,2", MergeHead: "h1"}, "tkt merge ACM-1 --passed 1,2 --head h1", "command", ReasonMergeDue},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", Head: "h2", MergeSteps: "1,2", MergeHead: "h1"}, "tkt run ACM-1 review", "command", ReasonNeedsReview},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "fail", Verdict: "READY_FOR_HUMAN", AtHead: true, Head: "h1", MergeSteps: "1,2"}, "tkt run ACM-1 ci", "command", ReasonChecksFail},
		{Facts{PRState: "OPEN", Agent: "working", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true, Head: "h1", MergeSteps: "1,2"}, "wait (agent working)", "wait", ReasonAgentWorking},
		// A chore's run has no ticket: an end with nothing to change removes
		// it, and the review's findings and the merge are yours, since no
		// skill takes a pull request without a ticket. The bot still gets
		// its say first.
		{Facts{Chore: "deps", Agent: "idle", LastStatus: "DONE"}, "tkt done ACM-1", "command", ReasonNoChanges},
		// A run whose start stopped before its agent got the prompt goes on
		// with tkt chore run; one whose prompt may have gone in has a
		// session to resume.
		{Facts{Chore: "deps", Launch: "tab"}, "tkt chore run deps", "command", ReasonNoAgent},
		{Facts{Chore: "deps", Launch: "not_ready"}, "tkt chore run deps", "command", ReasonNoAgent},
		{Facts{Chore: "deps", Launch: "prompt_uncertain"}, "tkt resume ACM-1", "command", ReasonNoAgent},
		{Facts{Chore: "deps", Launch: "submitted"}, "tkt resume ACM-1", "command", ReasonNoAgent},
		{Facts{Chore: "deps", PRState: "CLOSED", Agent: "idle"}, "tkt done ACM-1", "command", ReasonClosed},
		{Facts{Chore: "deps", Agent: "idle", LastStatus: "READY_FOR_REVIEW"}, "check the agent in its tab", "human", ReasonCheckAgent},
		{Facts{Chore: "deps", PRState: "MERGED", Agent: "idle"}, "tkt done ACM-1", "command", ReasonMerged},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "STALE_BASE", AtHead: true}, "tkt sync ACM-1", "command", ReasonBaseMoved},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "fail"}, "tkt run ACM-1 ci", "command", ReasonChecksFail},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "FINDINGS", AtHead: true}, "read the review's findings, then have its agent fix them in its tab, or merge the pull request as it is", "human", ReasonChoreFindings},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, BotWait: time.Hour}, "review the pull request, and merge it yourself", "human", ReasonChoreReady},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "ok", BotWait: 15 * time.Minute, Waited: time.Minute}, "wait for the review bot", "wait", ReasonAwaitingBot},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "ok", BotWait: 15 * time.Minute, Waited: time.Hour}, "review the pull request, and merge it yourself", "human", ReasonChoreReady},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "ok"}, "review the pull request, and merge it yourself", "human", ReasonChoreReady},
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
		{Facts{PRState: "OPEN", Agent: "idle", Fix: "working"}, PhaseFixing},
		{Facts{PRState: "OPEN", Agent: "idle", Fix: "idle", FixStatus: "BLOCKED"}, PhaseNeedsYou},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", SyncFail: "failed"}, PhaseNeedsFix},
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
		{Facts{Chore: "deps", Agent: "idle", LastStatus: "DONE"}, PhaseFinished},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "FINDINGS", AtHead: true}, PhaseNeedsYou},
		{Facts{Chore: "deps", PRState: "OPEN", Agent: "idle", Checks: "ok"}, PhaseReadyForHuman},
		{Facts{Chore: "deps", Agent: "working", Stage: "chore"}, PhaseWorking},
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
