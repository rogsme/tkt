package app

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/history"
)

// MergeOpts are tkt merge's arguments. Passed names the human verification
// steps that passed, such as 1-3, 1,2,4 or all; Head is the head they ran
// on. Cancel drops the ticket's merge order instead.
type MergeOpts struct {
	ID     string
	Passed string
	Head   string
	As     string
	Cancel bool
	Dry    bool
}

// MergeResult is what tkt merge did. Action is merging (a fresh session got
// finish-ticket's merge line, Run), ordered (the checks at Head had not
// passed yet, so tkt watch sends the line once they do), or cancelled.
type MergeResult struct {
	Ticket  string     `json:"ticket"`
	PR      int        `json:"pr"`
	Head    string     `json:"head"`
	Steps   []int      `json:"steps"`
	Action  string     `json:"action"`
	Run     *RunResult `json:"run"`
	DryRun  bool       `json:"dry_run"`
	Actions []Action   `json:"actions"`
}

// The actions tkt merge takes.
const (
	MergeMerging   = "merging"
	MergeOrdered   = "ordered"
	MergeCancelled = "cancelled"
)

func (r *MergeResult) Text(w io.Writer) {
	switch {
	case r.Action == MergeMerging:
		sent := "sent"
		if r.DryRun {
			sent = "would send"
		}
		fmt.Fprintf(w, "%s  %s finish-ticket the merge line for %s at %s, with %s passed, in pane %s\n", r.Ticket, sent, forge.Ref(r.PR), short(r.Head), config.StepsText(r.Steps), r.Run.Pane)
	case r.Action == MergeOrdered && r.DryRun:
		fmt.Fprintf(w, "%s  would leave a merge order for %s at %s, whose checks still run\n", r.Ticket, forge.Ref(r.PR), short(r.Head))
	case r.Action == MergeOrdered:
		fmt.Fprintf(w, "%s  merges %s once its checks pass at %s; tkt watch sends finish-ticket the merge line then\n", r.Ticket, forge.Ref(r.PR), short(r.Head))
	case r.Action == MergeCancelled:
		fmt.Fprintf(w, "%s  no merge order\n", r.Ticket)
	}
}

// Merge hands a ticket whose human verification passed to finish-ticket:
// it sends the merge line, naming every step as passed, to a fresh session
// in the ticket's tab. While the checks at the head still run, it leaves a
// merge order instead, which tkt watch carries out once they pass; a new
// head drops the order, since the steps ran on the old one. It refuses
// what finish-ticket would refuse: a head the steps did not run on, one
// with no clean review, a branch behind its base, failing checks, or steps
// that did not all pass.
func (a *App) Merge(ctx context.Context, o MergeOpts) (*MergeResult, error) {
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
	if err := c.LC.RequireGitHub("merge"); err != nil {
		return nil, err
	}
	if err := c.needTools("gh"); err != nil {
		return nil, err
	}
	if o.As != "" {
		if _, err := c.Cfg.Preset(o.As); err != nil {
			return nil, err
		}
	}
	path, err := c.ticketPath(id)
	if err != nil {
		return nil, err
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	ts, err := c.readState(path)
	if err != nil {
		return nil, err
	}
	res := &MergeResult{Ticket: id, Steps: []int{}, DryRun: c.Dry, Actions: []Action{}}
	if o.Cancel {
		res.Head, res.Steps = ts.st["merge_head"], parseStepList(ts.st["merge_steps"])
		if res.Head != "" {
			c.dropOrder(ts, id, "cancelled")
		}
		res.Action = MergeCancelled
		return res, nil
	}
	// Read the ticket as tkt ls does, after a fetch, so the base it compares
	// with is the remote's.
	l, err := c.look(lookOpts{ids: []string{id}, fetch: !c.Dry})
	if err != nil {
		return nil, err
	}
	if l.src.Forge != "ok" {
		if l.forgeErr != nil {
			return nil, l.forgeErr
		}
		return nil, apperr.New(apperr.ForgeQueryFailed, "tkt merge cannot read %s's pull request", id)
	}
	row, f := c.row(c.Ctx, l, ticketWorktree{ID: id, Path: path})
	if row.PR == nil || row.PR.State != "OPEN" {
		e := apperr.New(apperr.NoVerification, "%s has no open pull request", id).WithHint("tkt ls %s", id)
		if row.PR != nil && row.PR.State == "MERGED" {
			e = apperr.New(apperr.NoVerification, "%s's pull request %s is merged already", id, forge.Ref(row.PR.Number)).WithHint("tkt done %s", id)
		}
		return nil, e
	}
	res.PR, res.Head = row.PR.Number, row.PR.Head
	if o.Head != "" && !strings.HasPrefix(res.Head, o.Head) {
		return nil, apperr.New(apperr.MergeHeadMoved, "%s's pull request is at %s now, not at %s where the steps ran; run them again on the new head", id, short(res.Head), o.Head).
			WithHint("tkt show %s --steps", id).With("head", res.Head)
	}
	if !f.AtHead || f.Verdict != contract.VerdictReadyForHuman && f.Verdict != contract.VerdictClear {
		return nil, apperr.New(apperr.NoVerification, "no review clears %s's head %s: finish-ticket merges only a head reviewed clean", id, short(res.Head)).
			WithHint("tkt ls %s", id)
	}
	proc, err := c.verification(id)
	if err != nil {
		return nil, err
	}
	want := make([]int, len(proc.Steps))
	for i, s := range proc.Steps {
		want[i] = s.N
	}
	got, err := parsePassed(o.Passed, want)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(got, want) {
		return nil, apperr.New(apperr.StepsIncomplete, "--passed names %s, but the pull request's procedure has %s: finish-ticket merges only when every step passed", config.StepsText(got), config.StepsText(want)).
			WithHint("tkt show %s --steps", id).With("steps", want)
	}
	res.Steps = got
	if f.Behind > 0 {
		base := c.LC.Forge.Remote + "/" + c.LC.Forge.Base
		return nil, apperr.New(apperr.MergeBehindBase, "%s is %d commit%s behind %s, and finish-ticket merges only a head reviewed against the current base", id, f.Behind, plural(f.Behind), base).
			WithHint("tkt sync %s, then verify the new head once it is reviewed", id)
	}
	switch f.Checks {
	case "fail":
		return nil, apperr.New(apperr.MergeChecksFail, "the checks fail at %s's head %s", id, short(res.Head)).WithHint("tkt run %s ci", id)
	case "pending":
		c.save(ts, "merge_head", res.Head, "merge_steps", stepList(got), "merge_at", c.Now().UTC().Format(history.TimeFormat))
		c.record(history.Event{Ticket: id, Event: history.Merge, Action: MergeOrdered, PR: res.PR})
		res.Action, res.Actions = MergeOrdered, append(res.Actions, c.Plan...)
		if !c.Dry && !c.watchRunning() {
			c.UI.Warn("no tkt watch runs for %s, so nothing carries the order out yet; tkt watch, or any tkt command in a herdr pane, starts one", c.Repo.Root)
		}
		return res, nil
	}
	// tkt run takes the ticket's lock itself.
	c.release()
	run, err := a.Run(ctx, RunOpts{ID: id, Stage: "merge", As: o.As, Dry: o.Dry, line: config.MergeLine(got)})
	if err != nil {
		return nil, err
	}
	if ts.st["merge_head"] != "" {
		c.save(ts, "merge_head", "", "merge_steps", "", "merge_at", "")
	}
	res.Action, res.Run = MergeMerging, run
	res.Actions = append(res.Actions, run.Actions...)
	return res, nil
}

// dropOrder forgets a ticket's merge order and records why: cancelled by
// tkt merge --cancel, or dropped by a new head.
func (c *Ctx) dropOrder(ts *ticketState, id, why string) {
	c.save(ts, "merge_head", "", "merge_steps", "", "merge_at", "")
	c.record(history.Event{Ticket: id, Event: history.Merge, Action: why})
}

// verification reads the human verification procedure for the open pull
// request's head: review-pr's, or the pull request body's after the review
// bot's CLEAR.
func (c *Ctx) verification(id string) (*forge.Procedure, error) {
	res := &ShowResult{}
	if err := c.showSteps(id, res); err != nil {
		return nil, err
	}
	return &forge.Procedure{Section: res.Procedure, Steps: res.Steps}, nil
}

// parsePassed reads --passed: all, or step numbers and ranges such as 1-3
// or 1,2,4, against the procedure's steps.
func parsePassed(s string, want []int) ([]int, error) {
	if strings.TrimSpace(s) == "all" {
		return want, nil
	}
	var got []int
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		a, errA := strconv.Atoi(strings.TrimSpace(lo))
		b, errB := a, error(nil)
		if isRange {
			b, errB = strconv.Atoi(strings.TrimSpace(hi))
		}
		if errA != nil || errB != nil || a < 1 || b < a || b-a > 100 {
			return nil, apperr.New(apperr.Usage, "--passed takes step numbers and ranges, such as 1-3 or 1,2,4, or all; not %q", s).
				WithHint("tkt merge ID --passed all")
		}
		for n := a; n <= b; n++ {
			if !slices.Contains(got, n) {
				got = append(got, n)
			}
		}
	}
	slices.Sort(got)
	return got, nil
}

// stepList writes steps as a merge order keeps them, such as 1,2,3.
func stepList(steps []int) string {
	s := make([]string, len(steps))
	for i, n := range steps {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

// parseStepList reads a merge order's steps.
func parseStepList(s string) []int {
	out := []int{}
	for part := range strings.SplitSeq(s, ",") {
		if n, err := strconv.Atoi(part); err == nil {
			out = append(out, n)
		}
	}
	return out
}
