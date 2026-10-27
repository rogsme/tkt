package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
)

// AcceptOpts are tkt accept's arguments: the ticket, and why its review's
// findings can stay as they are.
type AcceptOpts struct {
	ID     string
	Reason string
	Dry    bool
}

// AcceptResult is what tkt accept did. Action is commented, or none when an
// acceptance holds for the head already. Review is the verdict accepted,
// and URL the comment's.
type AcceptResult struct {
	Ticket  string   `json:"ticket"`
	PR      int      `json:"pr"`
	Head    string   `json:"head"`
	Review  string   `json:"review"`
	Reason  string   `json:"reason"`
	Action  string   `json:"action"`
	URL     string   `json:"url"`
	DryRun  bool     `json:"dry_run"`
	Actions []Action `json:"actions"`
}

func (r *AcceptResult) Text(w io.Writer) {
	switch {
	case r.Action == "none":
		fmt.Fprintf(w, "%s  %s has an acceptance for %s already\n", r.Ticket, forge.Ref(r.PR), short(r.Head))
	case r.URL != "":
		fmt.Fprintf(w, "%s  accepted the review on %s: %s\n", r.Ticket, forge.Ref(r.PR), r.URL)
		fmt.Fprintf(w, "next: tkt ls %s (the head goes on to its human verification steps)\n", r.Ticket)
	}
}

// acceptedComment is the comment that accepts the review on head. It holds
// no Verdict: line and no Human verification heading, so nothing reads it
// as a review summary.
func acceptedComment(v *forge.Verdict, head, reason string) string {
	return contract.AcceptedHeading + "\n\n" +
		"The user accepts this review's findings as they are. Verification goes on from the pull request's Human verification section.\n\n" +
		"Accepted head: `" + head + "`\n" +
		"Accepted review: " + v.Value + " on `" + short(v.Head) + "`, submitted " + v.SubmittedAt + "\n" +
		"Reason: " + reason + "\n"
}

// Accept comments on a ticket's pull request that you accept the review at
// its head as it is: finish-ticket and verify-pr take the comment in place
// of a clean review, with the procedure from the pull request's body, and
// tkt goes on to the human verification steps. Only a review with findings
// at the head can be accepted, and not while the agent works on it.
func (a *App) Accept(ctx context.Context, o AcceptOpts) (*AcceptResult, error) {
	reason := strings.Join(strings.Fields(o.Reason), " ")
	if reason == "" {
		return nil, apperr.New(apperr.Usage, "tkt accept needs --reason: why the review's findings can stay as they are")
	}
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	id, err := c.LC.NormalizeID(o.ID)
	if err != nil {
		return nil, err
	}
	c.ticket(id)
	if err := c.LC.RequireGitHub("accept"); err != nil {
		return nil, err
	}
	if err := c.needTools("gh"); err != nil {
		return nil, err
	}
	path, err := c.ticketPath(id)
	if err != nil {
		return nil, err
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	l, err := c.look(lookOpts{ids: []string{id}})
	if err != nil {
		return nil, err
	}
	if l.src.Forge != "ok" {
		if l.forgeErr != nil {
			return nil, l.forgeErr
		}
		return nil, apperr.New(apperr.ForgeQueryFailed, "tkt accept cannot read %s's pull request", id)
	}
	row, _ := c.row(c.Ctx, l, ticketWorktree{ID: id, Path: path})
	if row.PR == nil || row.PR.State != "OPEN" {
		return nil, apperr.New(apperr.NothingToAccept, "%s has no open pull request", id).WithHint("tkt ls %s", id)
	}
	res := &AcceptResult{Ticket: id, PR: row.PR.Number, Head: row.PR.Head, Reason: reason, DryRun: c.Dry, Actions: []Action{}}
	if row.Accepted.Holds() {
		res.Action = "none"
		return res, nil
	}
	v := row.Review
	if v == nil || !v.AtHead || v.Value != contract.VerdictFindings && v.Value != contract.VerdictChangesRequested {
		return nil, apperr.New(apperr.NothingToAccept, "no review has findings at %s's head %s", id, short(row.PR.Head)).WithHint("tkt ls %s", id)
	}
	if row.Agent != nil && row.Agent.Status == herdr.Working {
		// The agent may be fixing the findings, and its push would void
		// the acceptance.
		return nil, apperr.New(apperr.AgentBusy, "%s's agent is working; wait for it, or interrupt it in its tab, then accept", id).WithHint("tkt wait %s", id)
	}
	res.Review = v.Value
	gh := forge.GH{R: c.R, Repo: c.LC.Forge.Host + "/" + c.LC.Forge.Repo, Name: c.LC.Forge.Repo}
	body := acceptedComment(v, row.PR.Head, reason)
	if c.Dry {
		c.Would(gh.CommentArgv(res.PR, body))
		res.Action, res.Actions = "commented", c.Plan
		return res, nil
	}
	if res.URL, err = gh.Comment(c.Ctx, res.PR, body); err != nil {
		return nil, err
	}
	c.record(history.Event{Ticket: id, Event: history.Accepted, PR: res.PR, Note: reason})
	res.Action = "commented"
	return res, nil
}
