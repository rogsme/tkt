package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var waiting = []string{"awaiting_approval", "needs_you", "ready_for_human"}

func ev(at, ticket, event string, set ...func(*Event)) Event {
	e := Event{At: at, Repo: "git.example.com/acme/app", Ticket: ticket, Event: event}
	for _, s := range set {
		s(&e)
	}
	return e
}

func phase(p string) func(*Event) { return func(e *Event) { e.Phase = p } }

func TestAppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tkt", "history.jsonl")
	if evs, err := Read(path); err != nil || len(evs) != 0 {
		t.Fatalf("missing file: %v %v", evs, err)
	}
	for _, e := range []Event{ev("2026-01-01T10:00:00Z", "ACM-1", Started), ev("2026-01-01T10:01:00Z", "ACM-1", Phase, phase("planning"))} {
		if err := Append(path, e); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("not json\n")
	f.Close()
	evs, err := Read(path)
	if err != nil || len(evs) != 2 || evs[0].V != Version || evs[1].Phase != "planning" {
		t.Errorf("read: %+v %v", evs, err)
	}
}

func TestTimelines(t *testing.T) {
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	events := []Event{
		ev("2026-01-01T10:00:00Z", "ACM-1", Started),
		ev("2026-01-01T10:00:00Z", "ACM-1", Phase, phase("planning")),
		ev("2026-01-01T11:00:00Z", "ACM-1", Phase, phase("awaiting_approval")),
		ev("2026-01-01T13:00:00Z", "ACM-1", Approved),
		ev("2026-01-01T13:00:00Z", "ACM-1", Phase, phase("building")),
		ev("2026-01-01T15:00:00Z", "ACM-1", Stage, func(e *Event) { e.Stage = "address" }),
		ev("2026-01-01T16:00:00Z", "ACM-1", Phase, phase("ready_for_human")),
		ev("2026-01-02T10:00:00Z", "ACM-1", Phase, phase("merged")),
		ev("2026-01-02T12:00:00Z", "ACM-1", Merged, func(e *Event) { e.MergedAt = "2026-01-02T09:00:00Z"; e.PR = 7 }),
		ev("2026-01-02T12:00:00Z", "ACM-1", Done),
		// ACM-2 is in progress and ran under tkt auto.
		ev("2026-01-09T00:00:00Z", "ACM-2", Started),
		ev("2026-01-09T00:00:00Z", "ACM-2", Auto, func(e *Event) { e.Action = "start" }),
		// ACM-3 predates the history: no start.
		ev("2026-01-05T00:00:00Z", "ACM-3", Phase, phase("building")),
		// Another repository.
		{At: "2026-01-01T00:00:00Z", Repo: "git.example.com/acme/web", Ticket: "ACM-1", Event: Started},
	}
	tickets, sum := Timelines(events, Filter{Repo: "git.example.com/acme/app"}, waiting, now)
	if len(tickets) != 2 || tickets[0].Ticket != "ACM-2" || tickets[1].Ticket != "ACM-1" {
		t.Fatalf("tickets: %+v", tickets)
	}
	a := tickets[1]
	if a.Total != 23*3600 || a.InProgress || a.PR != 7 || a.Rounds != 1 || a.Auto {
		t.Errorf("ACM-1: %+v", a)
	}
	// Waiting: 2h for approval plus 17h ready for human until the merge.
	if a.WaitingOnYou != 19*3600 || a.Phases["building"] != 3*3600 || a.Phases["merged"] != 0 {
		t.Errorf("ACM-1 phases: wait %d %v", a.WaitingOnYou, a.Phases)
	}
	b := tickets[0]
	if !b.InProgress || b.Total != 24*3600 || !b.Auto {
		t.Errorf("ACM-2: %+v", b)
	}
	if sum.Merged != 1 || sum.InProgress != 1 || sum.WithoutStart != 1 || sum.MedianTotal != 23*3600 {
		t.Errorf("summary: %+v", sum)
	}

	if tickets, _ := Timelines(events, Filter{Since: now.Add(-48 * time.Hour)}, waiting, now); len(tickets) != 1 || tickets[0].Ticket != "ACM-2" {
		t.Errorf("since: %+v", tickets)
	}
	if tickets, _ := Timelines(events, Filter{IDs: []string{"ACM-1"}}, waiting, now); len(tickets) != 2 {
		t.Errorf("all repositories: %+v", tickets)
	}
}

func TestTimelinesCountTheLastCycle(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		ev("2026-01-01T00:00:00Z", "ACM-1", Started),
		ev("2026-01-02T00:00:00Z", "ACM-1", Merged),
		ev("2026-01-02T00:00:00Z", "ACM-1", Done),
		ev("2026-01-20T00:00:00Z", "ACM-1", Started),
		ev("2026-01-20T06:00:00Z", "ACM-1", Merged),
	}
	tickets, _ := Timelines(events, Filter{}, waiting, now)
	if len(tickets) != 1 || tickets[0].Started != "2026-01-20T00:00:00Z" || tickets[0].Total != 6*3600 {
		t.Errorf("cycle: %+v", tickets)
	}
}

func TestPercentile(t *testing.T) {
	vs := []int64{5, 1, 4, 2, 3, 6, 7, 8, 9, 10}
	if percentile(vs, 50) != 5 || percentile(vs, 90) != 9 || percentile(nil, 50) != 0 || percentile([]int64{4}, 90) != 4 {
		t.Errorf("percentiles: %d %d", percentile(vs, 50), percentile(vs, 90))
	}
}
