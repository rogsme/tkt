package app

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/history"
)

// HistoryOpts are tkt history's arguments.
type HistoryOpts struct {
	IDs []string
}

// HistoryResult is what happened to tickets: their events from the history
// tkt keeps, and their pull requests from the forge.
type HistoryResult struct {
	Repo string `json:"repo"`
	// Forge says whether tkt read the pull requests: ok, unsupported (not
	// GitHub, or no gh) or failed.
	Forge   string          `json:"forge"`
	Tickets []TicketHistory `json:"tickets"`
}

// TicketHistory is one ticket's events and pull requests.
type TicketHistory struct {
	Ticket string `json:"ticket"`
	// Timeline is the ticket's latest way from start to merge, as tkt stats
	// counts it, or null when the history holds no start.
	Timeline     *history.Ticket `json:"timeline"`
	Events       []HistoryEvent  `json:"events"`
	PullRequests []PRHistory     `json:"pull_requests"`
}

// HistoryEvent is one line of the history, without what the result already
// says: the repository and the ticket.
type HistoryEvent struct {
	At     string `json:"at"`
	Event  string `json:"event"`
	Phase  string `json:"phase,omitempty"`
	From   string `json:"from,omitempty"`
	Stage  string `json:"stage,omitempty"`
	Action string `json:"action,omitempty"`
	Stop   string `json:"stop,omitempty"`
	PR     int    `json:"pr,omitempty"`
	// Unblocked lists the tickets finish-ticket said the merge unblocked.
	Unblocked []string `json:"unblocked,omitempty"`
	// Chore names the chore whose run started, on its start.
	Chore string `json:"chore,omitempty"`
	// Note says why a step failed.
	Note string `json:"note,omitempty"`
}

// PRHistory is a pull request of the ticket's branch.
type PRHistory struct {
	Number       int          `json:"number"`
	Title        string       `json:"title"`
	URL          string       `json:"url"`
	State        string       `json:"state"`
	Opened       string       `json:"opened"`
	Merged       string       `json:"merged"`
	Additions    int          `json:"additions"`
	Deletions    int          `json:"deletions"`
	ChangedFiles int          `json:"changed_files"`
	Commits      []CommitNote `json:"commits"`
	Reviews      []ReviewNote `json:"reviews"`
}

// CommitNote is a commit of the pull request.
type CommitNote struct {
	At      string `json:"at"`
	Oid     string `json:"oid"`
	Subject string `json:"subject"`
}

// ReviewNote is a review with a body. Verdict and Head are empty when the
// review carries no verdict, such as a person's comment.
type ReviewNote struct {
	At      string `json:"at"`
	Author  string `json:"author"`
	Verdict string `json:"verdict"`
	Head    string `json:"head"`
	Body    string `json:"body"`
}

func (r *HistoryResult) Text(w io.Writer) {
	for i, t := range r.Tickets {
		if i > 0 {
			fmt.Fprintln(w)
		}
		t.text(w)
	}
}

func (t *TicketHistory) text(w io.Writer) {
	if len(t.Events) == 0 && len(t.PullRequests) == 0 {
		fmt.Fprintf(w, "%s: no history and no pull request\n", t.Ticket)
		return
	}
	head := t.Ticket
	if tl := t.Timeline; tl != nil {
		total := dur(tl.Total) + " start to merge"
		if tl.InProgress {
			total = dur(tl.Total) + " so far"
		}
		head += fmt.Sprintf("  %s, %s waiting on you, %d round%s", total, dur(tl.WaitingOnYou), tl.Rounds, plural(tl.Rounds))
		if tl.Auto {
			head += ", auto"
		}
	}
	fmt.Fprintln(w, head)
	for _, e := range t.Events {
		fmt.Fprintf(w, "  %s  %s\n", clock(e.At), e.line())
	}
	for _, p := range t.PullRequests {
		fmt.Fprintf(w, "  pull request %s %s  +%d -%d in %d files  %s\n", forge.Ref(p.Number), p.State, p.Additions, p.Deletions, p.ChangedFiles, p.Title)
		type entry struct{ at, text string }
		var entries []entry
		for _, c := range p.Commits {
			entries = append(entries, entry{c.At, fmt.Sprintf("commit %s  %s", c.Oid, c.Subject)})
		}
		for _, v := range p.Reviews {
			text := "review by " + v.Author
			if v.Verdict != "" {
				text = fmt.Sprintf("review %s at %s by %s", v.Verdict, v.Head, v.Author)
			}
			entries = append(entries, entry{v.At, text})
		}
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].at < entries[j].at })
		for _, e := range entries {
			fmt.Fprintf(w, "    %s  %s\n", clock(e.at), e.text)
		}
	}
}

// line says what an event means, in a few words.
func (e HistoryEvent) line() string {
	switch e.Event {
	case history.Started:
		if e.Chore != "" {
			return "started, a run of chore " + e.Chore
		}
	case history.Approved:
		return "plan approved"
	case history.Stage:
		return "ran " + e.Stage
	case history.Phase:
		return "phase " + e.Phase
	case history.Auto:
		words := []string{"auto"}
		for _, w := range []string{e.Action, e.Stage, e.Stop} {
			if w != "" {
				words = append(words, w)
			}
		}
		return strings.Join(words, " ")
	case history.Merged:
		return "merged " + forge.Ref(e.PR)
	case history.Hold:
		if e.Action == "off" {
			return "taken off hold"
		}
		return "put on hold"
	case history.Sync:
		if e.Action == "failed" {
			return "sync failed: " + e.Note
		}
		return "merged the base, with no agent"
	case history.Watch:
		switch {
		case e.Action == actPause:
			return "tkt watch waits for you: " + e.Stop
		case e.Stop == "failed" && e.Note != "":
			return "tkt watch could not " + strings.TrimSpace(e.Action+" "+e.Stage) + ": " + e.Note
		case e.Stop == "failed":
			return "tkt watch could not " + strings.TrimSpace(e.Action+" "+e.Stage)
		case e.Action == actMerge:
			return "tkt watch carried out the merge order"
		}
		return "tkt watch ran " + e.Stage
	case history.Merge:
		switch e.Action {
		case MergeOrdered:
			return "merge order left, for when finish-ticket can merge"
		case "dropped":
			return "merge order dropped by a new head"
		}
		return "merge order cancelled"
	case history.Done:
		if len(e.Unblocked) > 0 {
			return "removed; the merge unblocked " + idsText(e.Unblocked)
		}
	}
	return e.Event
}

// clock renders an event time for people, in local time.
func clock(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at
	}
	return t.Local().Format("2006-01-02 15:04")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// History reports what happened to tickets: every event in the history,
// the timeline tkt stats counts, and each pull request's commits and
// reviews, so a person or the tkt agent can say how a ticket went. A
// ticket's worktree may be gone; the history and the forge outlive it.
func (a *App) History(ctx context.Context, o HistoryOpts) (*HistoryResult, error) {
	c, err := a.begin(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	var ids []string
	for _, raw := range o.IDs {
		id, err := c.LC.NormalizeID(raw)
		if err != nil {
			// A chore's run is named by its branch, and its history outlives
			// its worktree.
			if c.LC.ScratchName(raw) != nil {
				return nil, err
			}
			id = raw
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	res := &HistoryResult{Repo: c.repoKey(), Forge: "unsupported", Tickets: []TicketHistory{}}
	events, err := history.Read(history.Path(a.Getenv))
	if err != nil {
		return nil, apperr.New(apperr.Internal, "cannot read the history: %v", err)
	}
	f := c.LC.Forge
	gh := forge.GH{R: c.R, Repo: f.Host + "/" + f.Repo, Name: f.Repo}
	if f.Kind == "GitHub" && found("gh") && f.Host != "" && f.Repo != "" {
		res.Forge = "ok"
	}
	for _, id := range ids {
		c.ticket(id)
		th := TicketHistory{Ticket: id, Events: []HistoryEvent{}, PullRequests: []PRHistory{}}
		for _, e := range events {
			if e.Repo == res.Repo && e.Ticket == id {
				th.Events = append(th.Events, HistoryEvent{At: e.At, Event: e.Event, Phase: e.Phase, From: e.From, Stage: e.Stage, Action: e.Action, Stop: e.Stop, PR: e.PR, Unblocked: e.Unblocked, Chore: e.Chore, Note: e.Note})
			}
		}
		sort.SliceStable(th.Events, func(i, j int) bool { return th.Events[i].At < th.Events[j].At })
		if tls, _ := history.Timelines(events, history.Filter{Repo: res.Repo, IDs: []string{id}, Chores: true}, WaitingOnYou, a.Now()); len(tls) > 0 {
			th.Timeline = &tls[0]
		}
		if res.Forge == "ok" {
			prs, err := gh.ListHistory(c.Ctx, id)
			if err != nil {
				a.UI.Warn("cannot read %s's pull requests: %v", id, err)
				res.Forge = "failed"
			}
			for _, p := range gh.Mine(prs, id) {
				th.PullRequests = append(th.PullRequests, prHistory(p))
			}
			sort.SliceStable(th.PullRequests, func(i, j int) bool { return th.PullRequests[i].Number < th.PullRequests[j].Number })
		}
		res.Tickets = append(res.Tickets, th)
	}
	return res, nil
}

func prHistory(p forge.PR) PRHistory {
	h := PRHistory{Number: p.Number, Title: p.Title, URL: p.URL, State: p.State, Opened: p.CreatedAt, Merged: p.MergedAt,
		Additions: p.Additions, Deletions: p.Deletions, ChangedFiles: p.ChangedFiles, Commits: []CommitNote{}, Reviews: []ReviewNote{}}
	for _, c := range p.Commits {
		h.Commits = append(h.Commits, CommitNote{At: c.CommittedDate, Oid: short(c.Oid), Subject: c.MessageHeadline})
	}
	// A review without a body holds only line comments, which tkt does not
	// read; the review bot repeats its findings in the body.
	for _, r := range p.Reviews {
		if strings.TrimSpace(r.Body) == "" {
			continue
		}
		n := ReviewNote{At: r.SubmittedAt, Body: strings.TrimSpace(r.Body)}
		if r.Author != nil {
			n.Author = r.Author.Login
		}
		if v, ok := forge.ReviewVerdict(r); ok {
			n.Verdict, n.Head = v.Value, short(v.Head)
		}
		h.Reviews = append(h.Reviews, n)
	}
	sort.SliceStable(h.Reviews, func(i, j int) bool { return h.Reviews[i].At < h.Reviews[j].At })
	return h
}
