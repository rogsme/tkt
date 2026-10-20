package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/lock"
)

func TestLsPretty(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	behind := 2
	r := &LsResult{Sources: Sources{Herdr: "ok", Forge: "ok"}, now: now, Rows: []Row{
		{Ticket: "ACM-12", Phase: PhasePlanning, PhaseSince: "2026-01-01T11:55:00Z", Agent: &RowAgent{Status: "working"},
			Next: Next{Kind: "wait", Text: "wait (agent working)"}, Auto: &RowAuto{Running: true}},
		{Ticket: "ACM-13", Phase: PhaseNeedsYou, PhaseSince: "2026-01-01T11:59:30Z", Agent: &RowAgent{Status: "blocked"},
			PR: &RowPR{Number: 4, State: "OPEN"}, Behind: &behind, Checks: &forge.Checks{State: "ok", Total: 5},
			Review: &forge.Verdict{Value: "FINDINGS"}, Next: Next{Kind: "human", Text: "answer the agent in its tab"},
			Auto: &RowAuto{Running: true, Paused: StopNeedsYou}},
		{Ticket: "ACM-14", Phase: PhaseQueued, PhaseSince: "2026-01-01T10:00:00Z", Next: Next{Kind: "wait", Text: "wait for a free auto slot"},
			Auto: &RowAuto{Running: true, Waiting: "slot"}},
	}}
	var b strings.Builder
	r.Pretty(&colorprofile.Writer{Forward: &b, Profile: colorprofile.NoTTY}, "", 200)
	want := `
  TICKET  PHASE      TIME  AGENT    PR             CHECKS  REVIEW          AUTO    NEXT
  ACM-12  planning   5m    working  -              -       -               on      wait (agent working)
  ACM-13  needs you  30s   blocked  #4 (2 behind)  ok 5    FINDINGS (old)  paused  answer the agent in its tab
  ACM-14  queued     2h    -        -              -       -               queued  wait for a free auto slot

`
	if got := b.String(); got != want {
		t.Errorf("Pretty:\n%s\nwant:\n%s", got, want)
	}
}

func TestQueuedTickets(t *testing.T) {
	common := t.TempDir()
	c := &Ctx{Repo: gitx.Repo{Common: common}}
	dir := queuedDir(common)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "ACM-9"), []byte("mode=plan\nsince=2026-01-01T00:00:00Z\n"), 0o644)
	if rows := c.queuedTickets(); len(rows) != 0 {
		t.Fatalf("a mark with no loop shows: %+v", rows)
	}
	held, err := lock.Try(common, "ACM-9.auto")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	rows := c.queuedTickets()
	if len(rows) != 1 || rows[0].Ticket != "ACM-9" || rows[0].Phase != PhaseQueued || !rows[0].Auto.ApprovePlan || rows[0].Next.Reason != ReasonAwaitingSlot {
		t.Errorf("rows: %+v", rows)
	}
}
