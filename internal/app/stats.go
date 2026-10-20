package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/output"
)

// StatsOpts are tkt stats's arguments.
type StatsOpts struct {
	IDs      []string
	Since    time.Duration
	AllRepos bool
}

// StatsResult is how long tickets took, from the history.
type StatsResult struct {
	// Repo is the repository the tickets belong to, or "" for all.
	Repo    string           `json:"repo"`
	Tickets []history.Ticket `json:"tickets"`
	Summary history.Summary  `json:"summary"`
}

func (r *StatsResult) Text(w io.Writer) {
	if len(r.Tickets) == 0 {
		fmt.Fprintln(w, "no tickets in the history yet")
	} else {
		header := []string{"TICKET", "STARTED", "START TO MERGE", "WAITING ON YOU", "ROUNDS", "AUTO"}
		if r.Repo == "" {
			header = append([]string{"REPO"}, header...)
		}
		var rows [][]string
		for _, t := range r.Tickets {
			total := dur(t.Total)
			if t.InProgress {
				total += " so far"
			}
			auto := "-"
			if t.Auto {
				auto = "yes"
			}
			row := []string{t.Ticket, strings.SplitN(t.Started, "T", 2)[0], total, dur(t.WaitingOnYou), fmt.Sprint(t.Rounds), auto}
			if r.Repo == "" {
				row = append([]string{t.Repo}, row...)
			}
			rows = append(rows, row)
		}
		output.Table(w, header, rows)
	}
	s := r.Summary
	if s.Merged > 0 {
		fmt.Fprintf(w, "\n%d merged: start to merge median %s, p90 %s; waiting on you median %s, p90 %s\n",
			s.Merged, dur(s.MedianTotal), dur(s.P90Total), dur(s.MedianWait), dur(s.P90Wait))
	}
	if s.WithoutStart > 0 {
		fmt.Fprintf(w, "%d tickets have no recorded start and are left out\n", s.WithoutStart)
	}
}

func dur(seconds int64) string { return Ago(time.Duration(seconds) * time.Second) }

// Stats reports how long tickets took from start to merge, and how much of
// that they waited for a person, from the history tkt keeps.
func (a *App) Stats(ctx context.Context, o StatsOpts) (*StatsResult, error) {
	c, err := a.begin(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	f := history.Filter{}
	res := &StatsResult{}
	if !o.AllRepos {
		res.Repo = c.repoKey()
		f.Repo = res.Repo
	}
	for _, raw := range o.IDs {
		id, err := c.LC.NormalizeID(raw)
		if err != nil {
			return nil, err
		}
		f.IDs = append(f.IDs, id)
	}
	if o.Since > 0 {
		f.Since = a.Now().Add(-o.Since)
	}
	events, err := history.Read(history.Path(a.Getenv))
	if err != nil {
		return nil, apperr.New(apperr.Internal, "cannot read the history: %v", err)
	}
	res.Tickets, res.Summary = history.Timelines(events, f, WaitingOnYou, a.Now())
	if res.Tickets == nil {
		res.Tickets = []history.Ticket{}
	}
	return res, nil
}
