package agent

import "testing"

func TestLastStatus(t *testing.T) {
	cases := []struct{ text, want string }{
		{"", ""},
		{"Status: AWAITING_APPROVAL", "AWAITING_APPROVAL"},
		{"Status: AWAITING_APPROVAL\nlater\nStatus: READY_FOR_REVIEW", "READY_FOR_REVIEW"},
		// The approval starts the work phase: nothing final came after it.
		{"Status: AWAITING_APPROVAL\nYou approved revision 3.\nStatus: PLAN_APPROVED\nworking", ""},
		{"Status: PLAN_APPROVED\nStatus: READY_FOR_REVIEW", "READY_FOR_REVIEW"},
	}
	for _, c := range cases {
		if got := LastStatus(c.text); got != c.want {
			t.Errorf("LastStatus(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	if got := Reason("Steps 4 to 9 wait.\n\nStatus: BLOCKED\nReason: Albee adds the DNS records.\n"); got != "Albee adds the DNS records." {
		t.Errorf("Reason = %q", got)
	}
	if !HasStatus("x\nStatus: PLAN_APPROVED\ny", "PLAN_APPROVED") || HasStatus("Status: AWAITING_APPROVAL", "PLAN_APPROVED") {
		t.Error("HasStatus")
	}
}
