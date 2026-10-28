package app

import (
	"time"

	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/herdr"
)

// Next is the step tkt ls suggests for a ticket. Kind is command (run Argv),
// wait (Argv, when set, waits for you), human (do Text yourself) or unknown.
// Reason names the rule that matched; programs branch on it, and its values
// never change meaning.
type Next struct {
	Kind   string   `json:"kind"`
	Reason string   `json:"reason"`
	Argv   []string `json:"argv"`
	Text   string   `json:"text"`
}

// The reasons NextStep gives.
const (
	ReasonMerged               = "merged"
	ReasonClosed               = "closed"
	ReasonAgentWorking         = "agent_working"
	ReasonAgentWaiting         = "agent_waiting"
	ReasonAgentBlocked         = "agent_blocked"
	ReasonNoAgent              = "no_agent"
	ReasonPlanAwaitsApproval   = "plan_awaits_approval"
	ReasonStatusBlocked        = "status_blocked"
	ReasonCheckAgent           = "check_agent"
	ReasonReviewAwaitsApproval = "review_awaits_approval"
	ReasonCIPending            = "ci_pending"
	ReasonBaseMoved            = "base_moved"
	ReasonQAFailed             = "qa_failed"
	ReasonFindings             = "findings"
	ReasonChecksFail           = "checks_fail"
	ReasonBotClear             = "bot_clear"
	ReasonReviewClear          = "review_clear"
	// ReasonReviewAccepted is a head whose review you accepted as it is
	// (tkt accept): it goes on to the steps as a clean one does.
	ReasonReviewAccepted = "review_accepted"
	ReasonMergeDue       = "merge_due"
	ReasonAwaitingBot    = "awaiting_bot"
	ReasonBotSilent      = "bot_silent"
	ReasonNeedsReview    = "needs_review"
	ReasonUnknown        = "unknown"
	// The reasons verify-pr gives: a clean head waits for it, where
	// workflow.md turns agent verification on; its report found a step
	// that fails; or the report holds, every step passed or some it could
	// not run, and you confirm the steps.
	ReasonNeedsVerify      = "needs_verify"
	ReasonVerifyFailed     = "verify_failed"
	ReasonVerified         = "verified"
	ReasonVerifyIncomplete = "verify_incomplete"
	// ReasonAutonomousMerge is a yolo ticket's: review-pr cleared the head
	// and verify-pr passed every step, so finish-ticket merges it.
	ReasonAutonomousMerge = "autonomous_merge"
	// The reasons only a chore's run gets: it ended with nothing to change,
	// the review found something, or its pull request waits for your merge.
	// No skill reviews, fixes or merges a pull request without a ticket.
	ReasonNoChanges     = "no_changes"
	ReasonChoreFindings = "chore_findings"
	ReasonChoreReady    = "chore_ready"
	// The reasons a small fix in a split gives: its session works, it waits
	// on you, or it ended with a status line other than READY_FOR_REVIEW.
	// ReasonSyncFailed is a sync that failed on the head, which a fresh
	// session in a split fixes.
	ReasonFixing     = "fixing"
	ReasonFixBlocked = "fix_blocked"
	ReasonFixStopped = "fix_stopped"
	ReasonSyncFailed = "sync_failed_fix"
)

func command(reason string, argv ...string) Next {
	text := ""
	for i, a := range argv {
		if i > 0 {
			text += " "
		}
		text += a
	}
	return Next{Kind: "command", Reason: reason, Argv: argv, Text: text}
}

func human(reason, text string) Next {
	return Next{Kind: "human", Reason: reason, Text: text}
}

// Facts are what NextStep decides from. PRState is "" without a pull
// request; Agent is the agent's status, "" without one; Checks is ok, fail,
// pending or none; Verdict is "" without a review. Behind is -1 when unknown.
// Waited is how long the head has waited for a review; BotWait is how long to
// give the automated reviewer, and 0 means the repository has none, so
// review-pr reviews instead. Background says an idle agent waits on its
// own background tasks, such as a CI watcher, and goes on by itself. Hold
// says the ticket is on hold, which the next step ignores: it stops only
// tkt watch and tkt auto. BaseMerge says how merging the base into the head would go,
// clean or conflict, when tkt tried it with git merge-tree; "" when it did
// not. AutoRunning says a tkt auto loop drives the ticket; WatchPause says
// why tkt watch left it for you, round_limit or no_progress. ForgeLag says
// the forge still shows an older head than the branch has, as right after
// a push, and Idle how long the agent has sat idle; tkt auto and tkt watch
// read both, from a look with probe. MergeSteps holds the steps of a merge
// order that holds at the head, such as 1,2,3, or "" without one; MergeHead
// is the head they passed on, which clean merges of the base may have left
// behind, MergeAs the preset the order names, and MergeBy who confirmed
// the steps: "" for you, evidence for you from verify-pr's evidence.
// ReviewReady says review-pr's READY_FOR_HUMAN summary is at the head, which
// finish-ticket merges on; the review bot's CLEAR is not one. Accepted says
// you accepted a review as it is at the head, or at one only clean merges
// of the base separate from it: its findings count as answered, and it
// stands in for that summary. Chore names
// the chore whose run this is, or is "" for a ticket. AgentVerify says
// workflow.md turns agent verification on. Verification is the outcome of
// verify-pr's newest report, and VerifyHeld says it counts for the head:
// it is about the head, or about one only clean merges of the base
// separate from it. Yolo says tkt auto --yolo carries the ticket to the
// merge with nobody watching. Fix is the status of the agent fixing the
// ticket in a split, "" without one, and FixStatus the status line it
// ended with. SyncFail is the kind of the sync that failed on the head:
// conflict, failed, or "". CIReview says workflow.md turns CI review on: a
// reviewer in CI stands in for review-pr, its CLEAR at the head counts as
// ReviewReady, and tkt never starts review-pr.
type Facts struct {
	ID           string
	Chore        string
	PRState      string
	Agent        string
	Background   bool
	Hold         bool
	AutoRunning  bool
	WatchPause   string
	ForgeLag     bool
	Idle         time.Duration
	LastStatus   string
	Checks       string
	Verdict      string
	AtHead       bool
	Mergeable    string
	BaseMerge    string
	Head         string
	Behind       int
	Stage        string
	Launch       string
	AutoWait     string
	AutoPause    string
	MergeSteps   string
	MergeHead    string
	MergeAs      string
	MergeBy      string
	ReviewReady  bool
	AgentVerify  bool
	Verification string
	VerifyHeld   bool
	Yolo         bool
	BotWait      time.Duration
	Waited       time.Duration
	Fix          string
	FixStatus    string
	SyncFail     string
	CIReview     bool
	Accepted     bool
}

// NextStep applies the rules in order and returns the first that matches.
func NextStep(f Facts) Next {
	id := f.ID
	switch {
	case f.PRState == "MERGED":
		return command(ReasonMerged, "tkt", "done", id)
	case f.PRState == "CLOSED":
		// Closing a pull request without merging rejects the work, and
		// drops a chore's run.
		return command(ReasonClosed, "tkt", "done", id)
	case f.Agent == herdr.Working:
		return Next{Kind: "wait", Reason: ReasonAgentWorking, Argv: []string{"tkt", "wait", id}, Text: "wait (agent working)"}
	case f.Background:
		return Next{Kind: "wait", Reason: ReasonAgentWaiting, Text: "wait (the agent waits on a background task)"}
	case f.Agent == herdr.Blocked:
		return human(ReasonAgentBlocked, "answer the agent in its tab")
	case f.Fix == herdr.Working:
		return Next{Kind: "wait", Reason: ReasonFixing, Text: "wait (a fresh session fixes it beside the agent)"}
	case f.Fix == herdr.Blocked:
		return human(ReasonFixBlocked, "answer the fix's session in its split")
	case f.Fix != "" && f.FixStatus != "" && f.FixStatus != contract.StatusReadyForReview:
		return human(ReasonFixStopped, "read why the fix stopped, in its split")
	case f.PRState == "":
		switch {
		case f.Agent == "" && f.Chore != "" && unstarted(f.Launch):
			// The run's start stopped before its agent got the prompt, so no
			// session holds anything to resume: tkt chore run goes on with it.
			return command(ReasonNoAgent, "tkt", "chore", "run", f.Chore)
		case f.Agent == "":
			return command(ReasonNoAgent, "tkt", "resume", id)
		case f.Chore != "" && f.LastStatus == contract.StatusDone:
			return command(ReasonNoChanges, "tkt", "done", id)
		case f.LastStatus == contract.StatusAwaitingApproval:
			return human(ReasonPlanAwaitsApproval, "read the plan with tkt show "+id+", then tkt approve "+id)
		case f.LastStatus == contract.StatusBlocked, f.LastStatus == contract.StatusSetupRequired:
			return human(ReasonStatusBlocked, "read the agent's reason in its tab")
		}
		return human(ReasonCheckAgent, "check the agent in its tab")
	case f.LastStatus == contract.StatusBlocked, f.LastStatus == contract.StatusSetupRequired:
		return human(ReasonStatusBlocked, "read the agent's reason in its tab")
	case f.LastStatus == contract.StatusAwaitingPostApproval:
		return human(ReasonReviewAwaitsApproval, "approve or discard the review draft in its tab")
	case f.Checks == "pending":
		return Next{Kind: "wait", Reason: ReasonCIPending, Text: "wait for CI"}
	case f.SyncFail != "":
		// The sync failed on this head; tkt sync hands it to a fresh
		// session.
		return command(ReasonSyncFailed, "tkt", "sync", id)
	case f.AtHead && f.Verdict == contract.VerdictStaleBase, f.Mergeable == "CONFLICTING", f.BaseMerge == "conflict",
		f.LastStatus == contract.StatusBlockedForSync && f.Behind > 0, f.MergeSteps != "" && f.Behind > 0:
		return command(ReasonBaseMoved, "tkt", "sync", id)
	case f.LastStatus == contract.StatusHumanQAFailed:
		return command(ReasonQAFailed, "tkt", "run", id, "address")
	case f.VerifyHeld && f.Verification == contract.VerificationFailed && f.Chore == "":
		// The report is feedback, which address-pr-review answers.
		return command(ReasonVerifyFailed, "tkt", "run", id, "address")
	case f.AtHead && (f.Verdict == contract.VerdictFindings || f.Verdict == contract.VerdictChangesRequested) && !f.Accepted:
		if f.Chore != "" {
			return human(ReasonChoreFindings, "read the review's findings, then have its agent fix them in its tab, or merge the pull request as it is")
		}
		return command(ReasonFindings, "tkt", "run", id, "address")
	case f.Checks == "fail":
		return command(ReasonChecksFail, "tkt", "run", id, "ci")
	case f.Chore != "" && (f.AtHead && (f.Verdict == contract.VerdictClear || f.Verdict == contract.VerdictReadyForHuman) || f.BotWait == 0 || f.Waited >= f.BotWait):
		// finish-ticket merges only a ticket's pull request, so a chore's is
		// yours, once the review bot has had its say.
		return human(ReasonChoreReady, "review the pull request, and merge it yourself")
	case f.Chore != "":
		return Next{Kind: "wait", Reason: ReasonAwaitingBot, Text: "wait for the review bot"}
	case f.MergeSteps != "" && (f.ReviewReady || f.Accepted):
		argv := []string{"tkt", "merge", id, "--passed", f.MergeSteps, "--head", f.MergeHead}
		switch f.MergeBy {
		case mergeByEvidence:
			argv = append(argv, "--evidence")
		case mergeByAutonomous:
			argv = append(argv, "--autonomous")
		}
		if f.MergeAs != "" {
			argv = append(argv, "--as", f.MergeAs)
		}
		return command(ReasonMergeDue, argv...)
	case f.Accepted:
		// Your acceptance stands in for the review, so no review waits.
		return cleared(f)
	case f.MergeSteps != "" && f.AtHead && f.Verdict == contract.VerdictClear && !f.CIReview:
		// finish-ticket merges on review-pr's READY_FOR_HUMAN alone.
		return command(ReasonNeedsReview, "tkt", "run", id, "review")
	case f.AtHead && (f.Verdict == contract.VerdictClear || f.Verdict == contract.VerdictReadyForHuman):
		return cleared(f)
	case f.CIReview && (f.BotWait == 0 || f.Waited < f.BotWait):
		// The reviewer in CI reviews every head; nothing else does.
		return Next{Kind: "wait", Reason: ReasonAwaitingBot, Text: "wait for the review bot"}
	case f.BotWait == 0:
		return command(ReasonNeedsReview, "tkt", "run", id, "review")
	case f.Waited < f.BotWait:
		return Next{Kind: "wait", Reason: ReasonAwaitingBot, Text: "wait for the review bot"}
	case f.CIReview:
		return human(ReasonBotSilent, "the review bot has not reviewed this head; check its CI job")
	}
	return human(ReasonBotSilent, "the review bot has not reviewed this head; check its CI job, or run tkt run "+id+" review")
}

// cleared is the next step of a head a review cleared: verify-pr's report
// when one holds, verify-pr itself where workflow.md turns agent
// verification on, and otherwise the steps, which tkt run verify can run
// for you.
func cleared(f Facts) Next {
	id := f.ID
	switch {
	case !f.ReviewReady && !f.Accepted && (f.Yolo || f.Verdict == contract.VerdictClear):
		// finish-ticket merges on review-pr's READY_FOR_HUMAN alone, so a
		// head only the bot cleared gets that review before anyone runs its
		// steps: a finding there would undo them. With CI review on, the
		// bot's CLEAR is ReviewReady.
		return command(ReasonNeedsReview, "tkt", "run", id, "review")
	case f.Yolo && f.VerifyHeld && f.Verification == contract.VerificationPassed:
		return command(ReasonAutonomousMerge, "tkt", "merge", id, "--autonomous")
	case f.VerifyHeld && f.Verification == contract.VerificationPassed:
		return human(ReasonVerified, "verify-pr passed every step: confirm them from its evidence (tkt show "+id+" --steps), then tkt merge "+id+" --passed all --evidence")
	case f.VerifyHeld && f.Verification == contract.VerificationIncomplete:
		return human(ReasonVerifyIncomplete, "run the steps verify-pr could not (tkt show "+id+" --steps), then tkt merge "+id+" --passed all")
	case f.AgentVerify || f.Yolo:
		return command(ReasonNeedsVerify, "tkt", "run", id, "verify")
	case f.Accepted:
		return command(ReasonReviewAccepted, "tkt", "run", id, "verify")
	case f.Verdict == contract.VerdictClear:
		return command(ReasonBotClear, "tkt", "run", id, "verify")
	}
	return command(ReasonReviewClear, "tkt", "run", id, "verify")
}

// The phases PhaseOf names: the board's columns, and the history's steps.
const (
	PhaseQueued           = "queued"
	PhaseStarting         = "starting"
	PhasePlanning         = "planning"
	PhaseAwaitingApproval = "awaiting_approval"
	PhaseBuilding         = "building"
	PhaseCI               = "ci"
	PhaseWaitingReview    = "waiting_review"
	PhaseReviewing        = "reviewing"
	PhaseNeedsFix         = "needs_fix"
	PhaseFixing           = "fixing"
	PhaseVerifying        = "verifying"
	PhaseWorking          = "working"
	PhaseNeedsYou         = "needs_you"
	// PhaseBlocked is a ticket that waits on something outside it: its
	// agent stopped with Status: BLOCKED and a Reason: line, such as a
	// person's answer, or you put it on hold.
	PhaseBlocked       = "blocked"
	PhaseReadyForHuman = "ready_for_human"
	PhaseMerging       = "merging"
	PhaseMerged        = "merged"
	PhaseClosed        = "closed"
	PhaseNoAgent       = "no_agent"
	PhaseUnknown       = "unknown"
	// PhaseFinished is a chore's run that ended with nothing to change.
	PhaseFinished = "finished"
)

// WaitingOnYou lists the phases where a ticket waits for a person.
var WaitingOnYou = []string{PhaseAwaitingApproval, PhaseNeedsYou, PhaseReadyForHuman}

// PhaseOf names where a ticket stands, from its facts and next step. A
// working agent's phase comes from the stage it was given.
func PhaseOf(f Facts, n Next) string {
	switch {
	case f.PRState == "MERGED":
		return PhaseMerged
	case f.PRState == "CLOSED":
		return PhaseClosed
	case f.Hold && f.Agent != herdr.Working:
		return PhaseBlocked
	case f.Agent != herdr.Working && (f.Launch == "tab" || f.Launch == "launching"):
		return PhaseStarting
	case f.Agent != herdr.Working && (f.Launch == "not_ready" || f.Launch == "prompt_uncertain"):
		return PhaseNeedsYou
	case f.Agent != herdr.Working && f.Agent != herdr.Blocked && f.AutoWait == "slot":
		return PhaseQueued
	case f.Agent != herdr.Working && !f.Background &&
		(f.AutoPause == StopRoundLimit || f.AutoPause == StopNoProgress || f.AutoPause == StopUnknown || f.AutoPause == StopSyncFailed || f.WatchPause != ""):
		return PhaseNeedsYou
	}
	switch n.Reason {
	case ReasonAgentWorking, ReasonAgentWaiting:
		switch f.Stage {
		case "start":
			return PhasePlanning
		case "work":
			return PhaseBuilding
		case "review":
			return PhaseReviewing
		case "address", "sync", "ci", "resolve":
			return PhaseFixing
		case "verify":
			return PhaseVerifying
		case "merge":
			return PhaseMerging
		}
		return PhaseWorking
	case ReasonFixing:
		return PhaseFixing
	case ReasonStatusBlocked:
		if f.LastStatus == contract.StatusBlocked {
			return PhaseBlocked
		}
		return PhaseNeedsYou
	case ReasonAgentBlocked, ReasonCheckAgent, ReasonReviewAwaitsApproval, ReasonBotSilent, ReasonChoreFindings, ReasonFixBlocked, ReasonFixStopped:
		return PhaseNeedsYou
	case ReasonNoChanges:
		return PhaseFinished
	case ReasonChoreReady:
		return PhaseReadyForHuman
	case ReasonNoAgent:
		return PhaseNoAgent
	case ReasonPlanAwaitsApproval:
		return PhaseAwaitingApproval
	case ReasonCIPending:
		return PhaseCI
	case ReasonAwaitingBot, ReasonNeedsReview:
		return PhaseWaitingReview
	case ReasonBaseMoved, ReasonQAFailed, ReasonFindings, ReasonChecksFail, ReasonVerifyFailed, ReasonSyncFailed:
		return PhaseNeedsFix
	case ReasonReviewClear, ReasonBotClear, ReasonReviewAccepted, ReasonNeedsVerify, ReasonVerified, ReasonVerifyIncomplete:
		return PhaseReadyForHuman
	case ReasonMergeDue, ReasonAutonomousMerge:
		return PhaseMerging
	}
	return PhaseUnknown
}

// factsFor gathers a row's facts.
func factsFor(r Row) Facts {
	f := Facts{ID: r.Ticket, Chore: r.Chore, LastStatus: r.LastStatus, Behind: -1, Stage: r.Stage, Launch: r.Launch, Hold: r.Hold}
	if r.Agent != nil {
		f.Agent, f.Background = r.Agent.Status, r.Agent.Background > 0
	}
	if r.PR != nil {
		f.PRState, f.Mergeable, f.Head = r.PR.State, r.PR.Mergeable, r.PR.Head
	}
	f.BaseMerge, f.ReviewReady = r.BaseMerge, r.reviewReady
	if r.Checks != nil {
		f.Checks = r.Checks.State
	}
	if r.Review != nil {
		f.Verdict, f.AtHead = r.Review.Value, r.Review.AtHead
	}
	if r.Behind != nil {
		f.Behind = *r.Behind
	}
	if r.Auto != nil {
		f.AutoWait, f.AutoPause, f.AutoRunning, f.Yolo = r.Auto.Waiting, r.Auto.Paused, r.Auto.Running, r.Auto.Yolo
	}
	if r.Watch != nil {
		f.WatchPause = r.Watch.Paused
	}
	if r.Merge != nil {
		f.MergeSteps, f.MergeHead, f.MergeAs, f.MergeBy = stepList(r.Merge.Steps), r.Merge.Head, r.Merge.As, r.Merge.By
	}
	if v := r.Verification; v != nil {
		f.Verification, f.VerifyHeld = v.Value, v.Holds()
	}
	if r.Fix != nil {
		f.Fix, f.FixStatus = r.Fix.Status, r.Fix.LastStatus
	}
	f.CIReview, f.Accepted = r.CIReview, r.Accepted.Holds()
	if r.Sync != nil && r.PR != nil && r.PR.State == "OPEN" {
		f.SyncFail = r.Sync.Kind
	}
	return f
}
