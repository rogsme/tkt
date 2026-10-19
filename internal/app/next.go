package app

import (
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/herdr"
)

// Next is the step tkt ls suggests for a ticket. Kind is command (run Argv),
// wait (Argv, when set, waits for you), human (do Text yourself) or unknown.
type Next struct {
	Kind string   `json:"kind"`
	Argv []string `json:"argv"`
	Text string   `json:"text"`
}

func command(argv ...string) Next {
	text := ""
	for i, a := range argv {
		if i > 0 {
			text += " "
		}
		text += a
	}
	return Next{Kind: "command", Argv: argv, Text: text}
}

// Facts are what NextStep decides from. PRState is "" without a pull
// request; Agent is the agent's status, "" without one; Checks is ok, fail,
// pending or none; Verdict is "" without a review.
type Facts struct {
	ID         string
	PRState    string
	Agent      string
	LastStatus string
	Checks     string
	Verdict    string
	AtHead     bool
	Mergeable  string
}

// NextStep applies the rules in order and returns the first that matches.
func NextStep(f Facts) Next {
	id := f.ID
	switch {
	case f.PRState == "MERGED":
		return command("tkt", "done", id)
	case f.PRState == "CLOSED":
		return Next{Kind: "human", Text: "closed without merging"}
	case f.Agent == herdr.Working:
		return Next{Kind: "wait", Argv: []string{"tkt", "wait", id}, Text: "wait (agent working)"}
	case f.Agent == herdr.Blocked:
		return Next{Kind: "human", Text: "answer the agent in its tab"}
	case f.PRState == "":
		switch {
		case f.Agent == "":
			return command("tkt", "resume", id)
		case f.LastStatus == contract.StatusAwaitingApproval:
			return Next{Kind: "human", Text: "approve the plan in its tab"}
		case f.LastStatus == contract.StatusBlocked:
			return Next{Kind: "human", Text: "read the agent's reason in its tab"}
		}
		return Next{Kind: "human", Text: "check the agent in its tab"}
	case f.Checks == "pending":
		return Next{Kind: "wait", Text: "wait for CI"}
	case f.AtHead && f.Verdict == contract.VerdictStaleBase, f.Mergeable == "CONFLICTING":
		return command("tkt", "run", id, "sync")
	case f.AtHead && (f.Verdict == contract.VerdictFindings || f.Verdict == contract.VerdictChangesRequested):
		return command("tkt", "run", id, "address")
	case f.Checks == "fail":
		return command("tkt", "run", id, "ci")
	case f.AtHead && (f.Verdict == contract.VerdictClear || f.Verdict == contract.VerdictReadyForHuman):
		return command("tkt", "run", id, "verify")
	}
	return Next{Kind: "wait", Text: "wait for review"}
}

// factsFor gathers a row's facts.
func factsFor(r Row) Facts {
	f := Facts{ID: r.Ticket, LastStatus: r.LastStatus}
	if r.Agent != nil {
		f.Agent = r.Agent.Status
	}
	if r.PR != nil {
		f.PRState, f.Mergeable = r.PR.State, r.PR.Mergeable
	}
	if r.Checks != nil {
		f.Checks = r.Checks.State
	}
	if r.Review != nil {
		f.Verdict, f.AtHead = r.Review.Value, r.Review.AtHead
	}
	return f
}
