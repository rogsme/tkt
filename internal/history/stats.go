package history

import (
	"slices"
	"sort"
	"time"
)

// Ticket is one ticket's way from start to merge, built from its events.
// Times are as events write them; durations are in seconds.
type Ticket struct {
	Ticket   string `json:"ticket"`
	Repo     string `json:"repo"`
	Started  string `json:"started"`
	Approved string `json:"approved"`
	Opened   string `json:"pr_opened"`
	Ready    string `json:"ready_for_human"`
	Merged   string `json:"merged"`
	Done     string `json:"done"`
	PR       int    `json:"pr"`
	// Total runs from the start to the merge, or to now while the ticket
	// is in progress.
	Total      int64 `json:"total_s"`
	InProgress bool  `json:"in_progress"`
	// WaitingOnYou sums the phases where the ticket waited for a person.
	WaitingOnYou int64            `json:"waiting_on_you_s"`
	Phases       map[string]int64 `json:"phases_s"`
	// Rounds counts the address, sync and ci runs.
	Rounds int  `json:"rounds"`
	Auto   bool `json:"auto"`
}

// Summary describes the merged tickets of a Timelines call.
type Summary struct {
	Merged       int   `json:"merged"`
	InProgress   int   `json:"in_progress"`
	MedianTotal  int64 `json:"median_total_s"`
	P90Total     int64 `json:"p90_total_s"`
	MedianWait   int64 `json:"median_waiting_on_you_s"`
	P90Wait      int64 `json:"p90_waiting_on_you_s"`
	WithoutStart int   `json:"without_start"`
}

// Filter picks the events Timelines reads.
type Filter struct {
	// Repo keeps one repository's tickets; empty keeps all.
	Repo string
	// IDs keeps these tickets; empty keeps all.
	IDs []string
	// Since keeps tickets started at or after it; zero keeps all.
	Since time.Time
}

// Timelines builds each ticket's latest way from start to merge. A ticket
// whose history has no start, because tkt did not record one yet, counts in
// Summary.WithoutStart and nowhere else. waiting lists the phases that wait
// for a person.
func Timelines(events []Event, f Filter, waiting []string, now time.Time) ([]Ticket, Summary) {
	byTicket := map[string][]Event{}
	var order []string
	for _, e := range events {
		if f.Repo != "" && e.Repo != f.Repo || len(f.IDs) > 0 && !slices.Contains(f.IDs, e.Ticket) {
			continue
		}
		key := e.Repo + " " + e.Ticket
		if _, ok := byTicket[key]; !ok {
			order = append(order, key)
		}
		byTicket[key] = append(byTicket[key], e)
	}
	var out []Ticket
	var sum Summary
	for _, key := range order {
		evs := byTicket[key]
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].At < evs[j].At })
		t, ok := timeline(lastCycle(evs), waiting, now)
		if !ok {
			sum.WithoutStart++
			continue
		}
		if !f.Since.IsZero() {
			if s, _ := parse(t.Started); s.Before(f.Since) {
				continue
			}
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	var totals, waits []int64
	for _, t := range out {
		if t.InProgress {
			sum.InProgress++
			continue
		}
		sum.Merged++
		totals = append(totals, t.Total)
		waits = append(waits, t.WaitingOnYou)
	}
	sum.MedianTotal, sum.P90Total = percentile(totals, 50), percentile(totals, 90)
	sum.MedianWait, sum.P90Wait = percentile(waits, 50), percentile(waits, 90)
	return out, sum
}

// lastCycle keeps the events from the last start that follows a done, so a
// ticket started again after its removal counts afresh.
func lastCycle(evs []Event) []Event {
	start := 0
	open := false
	for i, e := range evs {
		switch e.Event {
		case Started:
			if !open {
				start, open = i, true
			}
		case Done:
			open = false
		}
	}
	return evs[start:]
}

func timeline(evs []Event, waiting []string, now time.Time) (Ticket, bool) {
	t := Ticket{Ticket: evs[0].Ticket, Repo: evs[0].Repo, Phases: map[string]int64{}}
	type mark struct {
		phase string
		at    time.Time
	}
	var marks []mark
	for _, e := range evs {
		switch e.Event {
		case Started:
			if t.Started == "" {
				t.Started = e.At
			}
		case Approved:
			if t.Approved == "" {
				t.Approved = e.At
			}
		case Stage:
			if e.Stage == "address" || e.Stage == "sync" || e.Stage == "ci" || e.Stage == "resolve" {
				t.Rounds++
			}
		case Auto:
			t.Auto = true
		case Phase:
			if at, ok := parse(e.At); ok {
				marks = append(marks, mark{e.Phase, at})
			}
			if e.Phase == "ready_for_human" {
				t.Ready = e.At
			}
			if e.Phase == "merged" && t.Merged == "" {
				t.Merged = e.At
			}
		case Merged:
			if e.MergedAt != "" {
				t.Merged = e.MergedAt
			} else if t.Merged == "" {
				t.Merged = e.At
			}
			t.Opened = e.OpenedAt
			t.PR = e.PR
		case Done:
			t.Done = e.At
		}
	}
	start, ok := parse(t.Started)
	if !ok {
		return t, false
	}
	end := now
	if m, ok := parse(t.Merged); ok {
		end = m
	} else {
		t.InProgress = true
	}
	t.Total = int64(end.Sub(start).Seconds())
	for i, m := range marks {
		if !m.at.Before(end) {
			break
		}
		next := end
		if i+1 < len(marks) && marks[i+1].at.Before(end) {
			next = marks[i+1].at
		}
		d := int64(next.Sub(m.at).Seconds())
		t.Phases[m.phase] += d
		if slices.Contains(waiting, m.phase) {
			t.WaitingOnYou += d
		}
	}
	return t, true
}

// percentile returns the nearest-rank percentile, or 0 for no values.
func percentile(vs []int64, p int) int64 {
	if len(vs) == 0 {
		return 0
	}
	s := slices.Clone(vs)
	slices.Sort(s)
	rank := (p*len(s) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	return s[rank-1]
}
