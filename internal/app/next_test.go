package app

import "testing"

func TestNextStepTable(t *testing.T) {
	cases := []struct {
		f    Facts
		want string
		kind string
	}{
		{Facts{PRState: "MERGED", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE"}, "tkt done ACM-1", "command"},
		{Facts{PRState: "CLOSED"}, "closed without merging", "human"},
		{Facts{PRState: "OPEN", Agent: "working", Checks: "ok", Mergeable: "MERGEABLE"}, "wait (agent working)", "wait"},
		{Facts{PRState: "OPEN", Agent: "blocked", Checks: "ok", Mergeable: "MERGEABLE"}, "answer the agent in its tab", "human"},
		{Facts{}, "tkt resume ACM-1", "command"},
		{Facts{Agent: "idle", LastStatus: "AWAITING_APPROVAL"}, "approve the plan in its tab", "human"},
		{Facts{Agent: "idle", LastStatus: "BLOCKED"}, "read the agent's reason in its tab", "human"},
		{Facts{Agent: "idle"}, "check the agent in its tab", "human"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "pending", Verdict: "FINDINGS", AtHead: true, Mergeable: "MERGEABLE"}, "wait for CI", "wait"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "STALE_BASE", AtHead: true, Mergeable: "MERGEABLE"}, "tkt run ACM-1 sync", "command"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "CONFLICTING"}, "tkt run ACM-1 sync", "command"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "FINDINGS", AtHead: true, Mergeable: "MERGEABLE"}, "tkt run ACM-1 address", "command"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CHANGES_REQUESTED", AtHead: true}, "tkt run ACM-1 address", "command"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "fail", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE"}, "tkt run ACM-1 ci", "command"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: true, Mergeable: "MERGEABLE"}, "tkt run ACM-1 verify", "command"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "READY_FOR_HUMAN", AtHead: true}, "tkt run ACM-1 verify", "command"},
		{Facts{PRState: "OPEN", Agent: "idle", Checks: "ok", Verdict: "CLEAR", AtHead: false, Mergeable: "MERGEABLE"}, "wait for review", "wait"},
	}
	for _, tc := range cases {
		tc.f.ID = "ACM-1"
		got := NextStep(tc.f)
		if got.Text != tc.want || got.Kind != tc.kind {
			t.Errorf("%+v: got %q (%s), want %q (%s)", tc.f, got.Text, got.Kind, tc.want, tc.kind)
		}
		if got.Kind == "command" && len(got.Argv) == 0 {
			t.Errorf("%q has no argv", got.Text)
		}
	}
	if got := NextStep(Facts{ID: "ACM-1", PRState: "OPEN", Agent: "working"}); len(got.Argv) != 3 || got.Argv[1] != "wait" {
		t.Errorf("a working agent's next step is not tkt wait: %+v", got)
	}
}
