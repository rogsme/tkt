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
	ReasonAwaitingBot          = "awaiting_bot"
	ReasonNeedsReview          = "needs_review"
	ReasonUnknown              = "unknown"
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
// give the automated reviewer before review-pr runs instead.
type Facts struct {
	ID         string
	PRState    string
	Agent      string
	LastStatus string
	Checks     string
	Verdict    string
	AtHead     bool
	Mergeable  string
	Head       string
	Behind     int
	Stage      string
	Launch     string
	AutoWait   string
	BotWait    time.Duration
	Waited     time.Duration
}

// NextStep applies the rules in order and returns the first that matches.
func NextStep(f Facts) Next {
	id := f.ID
	switch {
	case f.PRState == "MERGED":
		return command(ReasonMerged, "tkt", "done", id)
	case f.PRState == "CLOSED":
		return human(ReasonClosed, "closed without merging")
	case f.Agent == herdr.Working:
		return Next{Kind: "wait", Reason: ReasonAgentWorking, Argv: []string{"tkt", "wait", id}, Text: "wait (agent working)"}
	case f.Agent == herdr.Blocked:
		return human(ReasonAgentBlocked, "answer the agent in its tab")
	case f.PRState == "":
		switch {
		case f.Agent == "":
			return command(ReasonNoAgent, "tkt", "resume", id)
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
	case f.AtHead && f.Verdict == contract.VerdictStaleBase, f.Mergeable == "CONFLICTING",
		f.LastStatus == contract.StatusBlockedForSync && f.Behind > 0:
		return command(ReasonBaseMoved, "tkt", "run", id, "sync")
	case f.LastStatus == contract.StatusHumanQAFailed:
		return command(ReasonQAFailed, "tkt", "run", id, "address")
	case f.AtHead && (f.Verdict == contract.VerdictFindings || f.Verdict == contract.VerdictChangesRequested):
		return command(ReasonFindings, "tkt", "run", id, "address")
	case f.Checks == "fail":
		return command(ReasonChecksFail, "tkt", "run", id, "ci")
	case f.AtHead && f.Verdict == contract.VerdictClear:
		return command(ReasonBotClear, "tkt", "run", id, "verify")
	case f.AtHead && f.Verdict == contract.VerdictReadyForHuman:
		return command(ReasonReviewClear, "tkt", "run", id, "verify")
	case f.Waited < f.BotWait:
		return Next{Kind: "wait", Reason: ReasonAwaitingBot, Text: "wait for review"}
	}
	return command(ReasonNeedsReview, "tkt", "run", id, "review")
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
	PhaseReadyForHuman    = "ready_for_human"
	PhaseMerged           = "merged"
	PhaseClosed           = "closed"
	PhaseNoAgent          = "no_agent"
	PhaseUnknown          = "unknown"
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
	case f.Agent != herdr.Working && (f.Launch == "tab" || f.Launch == "launching"):
		return PhaseStarting
	case f.Agent != herdr.Working && (f.Launch == "not_ready" || f.Launch == "prompt_uncertain"):
		return PhaseNeedsYou
	case f.Agent != herdr.Working && f.Agent != herdr.Blocked && f.AutoWait == "slot":
		return PhaseQueued
	}
	switch n.Reason {
	case ReasonAgentWorking:
		switch f.Stage {
		case "start":
			return PhasePlanning
		case "work":
			return PhaseBuilding
		case "review":
			return PhaseReviewing
		case "address", "sync", "ci":
			return PhaseFixing
		case "verify":
			return PhaseVerifying
		}
		return PhaseWorking
	case ReasonAgentBlocked, ReasonStatusBlocked, ReasonCheckAgent, ReasonReviewAwaitsApproval:
		return PhaseNeedsYou
	case ReasonNoAgent:
		return PhaseNoAgent
	case ReasonPlanAwaitsApproval:
		return PhaseAwaitingApproval
	case ReasonCIPending:
		return PhaseCI
	case ReasonAwaitingBot, ReasonNeedsReview, ReasonBotClear:
		return PhaseWaitingReview
	case ReasonBaseMoved, ReasonQAFailed, ReasonFindings, ReasonChecksFail:
		return PhaseNeedsFix
	case ReasonReviewClear:
		return PhaseReadyForHuman
	}
	return PhaseUnknown
}

// factsFor gathers a row's facts.
func factsFor(r Row) Facts {
	f := Facts{ID: r.Ticket, LastStatus: r.LastStatus, Behind: -1, Stage: r.Stage, Launch: r.Launch}
	if r.Agent != nil {
		f.Agent = r.Agent.Status
	}
	if r.PR != nil {
		f.PRState, f.Mergeable, f.Head = r.PR.State, r.PR.Mergeable, r.PR.Head
	}
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
		f.AutoWait = r.Auto.Waiting
	}
	return f
}
