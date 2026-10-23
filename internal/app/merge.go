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
	"gitlab.com/rogs/tkt/internal/herdr"
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
	// order marks tkt watch carrying out the ticket's merge order: it
	// merges only while that order stands.
	order bool
}

// MergeResult is what tkt merge did. Action is merging (a fresh session got
// finish-ticket's merge line, Run), ordered (the merge waits, on what Waits
// says, and tkt watch sends the line once it can), cancelled (an order was
// dropped), or none (no order to drop, or to carry out). Head is the pull
// request's head, and Verified the head the steps passed on: the same, or
// one that clean merges of the base left behind. Sent is when finish-ticket
// last got the merge line for the ticket, if ever.
type MergeResult struct {
	Ticket   string `json:"ticket"`
	PR       int    `json:"pr"`
	Head     string `json:"head"`
	Verified string `json:"verified"`
	Steps    []int  `json:"steps"`
	Action   string `json:"action"`
	// Waits is what an order waits for: sync (tkt watch merges the base
	// into the branch first), review (a review of the head a sync made),
	// checks (the checks at the head), or turn (the merge of After goes
	// first); "" when nothing does.
	Waits   string     `json:"waits"`
	After   string     `json:"after"`
	Sent    string     `json:"sent"`
	Run     *RunResult `json:"run"`
	DryRun  bool       `json:"dry_run"`
	Actions []Action   `json:"actions"`
	base    string
}

// The actions tkt merge takes.
const (
	MergeMerging   = "merging"
	MergeOrdered   = "ordered"
	MergeCancelled = "cancelled"
	MergeNone      = "none"
)

// What a merge order waits for.
const (
	WaitSync   = "sync"
	WaitReview = "review"
	WaitChecks = "checks"
	WaitTurn   = "turn"
)

func (r *MergeResult) Text(w io.Writer) {
	switch {
	case r.Action == MergeMerging:
		sent := "sent"
		if r.DryRun {
			sent = "would send"
		}
		passed := config.StepsText(r.Steps) + " passed"
		if r.Verified != r.Head {
			passed += " on " + short(r.Verified) + " and clean merges of the base since"
		}
		fmt.Fprintf(w, "%s  %s finish-ticket the merge line for %s at %s, with %s, in pane %s\n", r.Ticket, sent, forge.Ref(r.PR), short(r.Head), passed, r.Run.Pane)
	case r.Action == MergeOrdered && r.DryRun:
		fmt.Fprintf(w, "%s  would leave a merge order for %s, which merges once %s\n", r.Ticket, forge.Ref(r.PR), r.waitText())
	case r.Action == MergeOrdered:
		fmt.Fprintf(w, "%s  merges %s once %s; tkt watch sends finish-ticket the merge line then\n", r.Ticket, forge.Ref(r.PR), r.waitText())
	case r.Action == MergeCancelled && r.Sent != "":
		fmt.Fprintf(w, "%s  dropped the merge order for %s; finish-ticket got the merge line at %s, and stopping it is up to you, in the ticket's tab\n", r.Ticket, short(r.Head), r.Sent)
	case r.Action == MergeCancelled:
		fmt.Fprintf(w, "%s  dropped the merge order for %s\n", r.Ticket, short(r.Head))
	case r.Sent != "":
		fmt.Fprintf(w, "%s  no merge order: finish-ticket got the merge line at %s; to stop it, tell its agent in the ticket's tab\n", r.Ticket, r.Sent)
	default:
		fmt.Fprintf(w, "%s  no merge order\n", r.Ticket)
	}
}

// waitText says what an order waits for, for people.
func (r *MergeResult) waitText() string {
	switch r.Waits {
	case WaitSync:
		return "tkt watch merged " + r.base + " into it, and a review cleared the new head"
	case WaitReview:
		return "a review clears its head " + short(r.Head)
	case WaitTurn:
		return r.After + " merged first"
	}
	return "its checks pass at " + short(r.Head)
}

// Merge hands a ticket whose human verification passed to finish-ticket:
// it sends the merge line, naming every step as passed and the head they
// passed on, to a fresh session in the ticket's tab. The steps hold across
// clean merges of the base, which tkt sync makes (finish-ticket, rogs-skills
// 421cfc7). When finish-ticket could not merge yet, Merge leaves a merge
// order instead, which tkt watch carries out: on a branch behind its base,
// tkt watch syncs it and a review clears the new head first; it waits for
// running checks, and for another ticket's merge, since one goes at a time.
// The order stands until the pull request merges, so a finish-ticket that
// stops because the base moved goes again after the next sync, and a new
// head of any other kind drops it. Before any session starts, Merge refuses
// steps that did not all pass, a head the steps cannot carry to, one no
// review cleared, a base merge that conflicts and failing checks;
// finish-ticket checks the rest. It holds the ticket's lock until the line
// is sent, so a --cancel either drops the order first or finds the line
// sent.
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
	base := c.LC.Forge.Remote + "/" + c.LC.Forge.Base
	res := &MergeResult{Ticket: id, Steps: []int{}, Sent: ts.st["merge_sent"], DryRun: c.Dry, Actions: []Action{}, base: base}
	res.Head, res.Verified = ts.st["merge_head"], ts.st["merge_head"]
	switch {
	case o.Cancel && res.Head == "":
		res.Action = MergeNone
		return res, nil
	case o.Cancel:
		res.Steps = parseStepList(ts.st["merge_steps"])
		c.dropOrder(ts, id, MergeCancelled)
		res.Action = MergeCancelled
		return res, nil
	case o.order && (res.Head != o.Head || ts.st["merge_steps"] != o.Passed || ts.st["merge_as"] != o.As):
		// Cancelled, or replaced, since tkt watch read it.
		res.Action = MergeNone
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
	switch {
	case row.PR != nil && row.PR.State == "MERGED":
		return nil, apperr.New(apperr.PullRequestMerged, "%s's pull request %s is merged already", id, forge.Ref(row.PR.Number)).WithHint("tkt done %s", id)
	case row.PR == nil || row.PR.State != "OPEN":
		return nil, apperr.New(apperr.NoVerification, "%s has no open pull request", id).WithHint("tkt ls %s", id)
	}
	res.PR, res.Head, res.Verified = row.PR.Number, row.PR.Head, row.PR.Head
	if o.Head != "" && !strings.HasPrefix(res.Head, o.Head) {
		v := c.Git.Rev(c.Ctx, path, o.Head+"^{commit}")
		if v == "" || !c.Git.CleanMerges(c.Ctx, path, v, res.Head, l.baseRef) {
			return nil, apperr.New(apperr.MergeHeadMoved, "%s's pull request is at %s now, and more than clean merges of the base came after %s, where the steps ran; run them again on the new head", id, short(res.Head), o.Head).
				WithHint("tkt show %s --steps", id).With("head", res.Head)
		}
		res.Verified = v
	}
	// The review must be at the head finish-ticket merges. After a sync, the
	// order waits for one, which tkt watch starts.
	cleared := f.AtHead && (f.Verdict == contract.VerdictReadyForHuman || f.Verdict == contract.VerdictClear)
	if !cleared && (f.AtHead || res.Verified == res.Head) {
		return nil, apperr.New(apperr.NoVerification, "no review clears %s's head %s: finish-ticket merges only a head reviewed clean", id, short(res.Head)).
			WithHint("tkt ls %s", id)
	}
	if f.Verdict == contract.VerdictClear {
		c.UI.Warn("only the review bot cleared %s's head; finish-ticket asks for a review that says %s at the head, and stops with Status: %s without one", id, contract.VerdictReadyForHuman, contract.StatusBlocked)
	}
	// The steps are the procedure's at the head once a review cleared it,
	// and before that, the one they ran on.
	at := res.Head
	if !cleared {
		at = res.Verified
	}
	got, err := c.passedSteps(id, at, o.Passed)
	if err != nil {
		return nil, err
	}
	res.Steps = got
	switch {
	case f.Behind > 0:
		// finish-ticket merges only a head reviewed against the current
		// base. The steps carry over a clean merge, never over the
		// resolution of a conflict.
		if c.probeBaseMerge(c.Ctx, path, res.Head, l.baseRef) == "conflict" {
			return nil, apperr.New(apperr.MergeBaseConflicts, "%s conflicts with %s, and human verification never carries over the resolution of a conflict", id, base).
				WithHint("tkt sync %s, then verify the new head once it is reviewed", id)
		}
		res.Waits = WaitSync
	case f.Checks == "fail":
		return nil, apperr.New(apperr.MergeChecksFail, "the checks fail at %s's head %s", id, short(res.Head)).WithHint("tkt run %s ci", id)
	case !cleared:
		res.Waits = WaitReview
	case f.Checks == "pending":
		res.Waits = WaitChecks
	default:
		if other := c.mergeInFlight(l.agents, id); other != "" {
			res.Waits, res.After = WaitTurn, other
		}
	}
	if res.Waits != "" {
		res.Action = MergeOrdered
		if o.order {
			return res, nil
		}
		// A new order is a new word from you: tkt watch forgets that it
		// sent one on this head already.
		kv := []string{"merge_head", res.Verified, "merge_steps", stepList(got), "merge_as", o.As, "merge_at", c.Now().UTC().Format(history.TimeFormat)}
		if strings.HasPrefix(ts.st["watch_last"], actMerge+":") {
			kv = append(kv, "watch_last", "", "watch_pause", "", "watch_head", "")
		}
		c.save(ts, kv...)
		c.record(history.Event{Ticket: id, Event: history.Merge, Action: MergeOrdered, PR: res.PR})
		res.Actions = append(res.Actions, c.Plan...)
		if !c.Dry && !c.watchRunning() {
			how := "the next tkt command in a herdr pane starts one"
			if !c.Cfg.Watch.Autostart || c.Getenv("TKT_WATCH") == "off" {
				how = "start one with tkt watch, or tkt watch --install"
			}
			c.UI.Warn("no tkt watch runs for %s, so nothing carries the order out yet; %s", c.Repo.Root, how)
		}
		return res, nil
	}
	// The lock stays held, so the order cannot change while the line goes
	// out, and a --cancel after it finds the line sent.
	run, err := c.run(RunOpts{ID: id, Stage: "merge", As: o.As, Dry: o.Dry, line: config.MergeLine(got, res.Verified, res.Head)})
	if err != nil {
		return nil, err
	}
	// The order stands until the pull request merges: when finish-ticket
	// stops because the base moved meanwhile, tkt watch syncs the branch
	// and sends the line again on the new head. watch_last keeps it from
	// sending the line twice on one head.
	sent := c.Now().UTC().Format(history.TimeFormat)
	c.save(ts, "merge_head", res.Verified, "merge_steps", stepList(got), "merge_as", o.As, "merge_at", or(ts.st["merge_at"], sent),
		"merge_sent", sent, "watch_last", watchAct{Kind: actMerge}.key(res.Head))
	res.Action, res.Run, res.Sent = MergeMerging, run, sent
	res.Actions = append(res.Actions, run.Actions...)
	return res, nil
}

// passedSteps reads --passed against the human verification procedure at
// head: every step, since finish-ticket merges only when each one passed.
func (c *Ctx) passedSteps(id, head, passed string) ([]int, error) {
	proc := &ShowResult{}
	if err := c.stepsAt(id, head, proc); err != nil {
		return nil, err
	}
	var want []int
	for _, s := range proc.Steps {
		if !slices.Contains(want, s.N) {
			want = append(want, s.N)
		}
	}
	slices.Sort(want)
	got, err := parsePassed(passed, want)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(got, want) {
		return nil, apperr.New(apperr.StepsIncomplete, "--passed names %s, but the pull request's procedure has %s: finish-ticket merges only when every step passed", config.StepsText(got), config.StepsText(want)).
			WithHint("tkt show %s --steps", id).With("steps", want)
	}
	return got, nil
}

// mergeInFlight names a ticket other than except whose merge line went out
// and whose pull request is still open while its finish-ticket runs, or
// "". One merge goes at a time: a second finish-ticket that checks the base
// before the first one merges would merge an integration nobody reviewed.
// A pull request that merged frees the turn while its finish-ticket waits
// for the checks after the merge; one tkt cannot read keeps it.
func (c *Ctx) mergeInFlight(agents []herdr.Agent, except string) string {
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return ""
	}
	f := c.LC.Forge
	gh := forge.GH{R: c.R, Repo: f.Host + "/" + f.Repo, Name: f.Repo}
	for _, t := range c.ticketWorktrees(es) {
		if t.ID == except {
			continue
		}
		ts, err := c.readState(t.Path)
		if err != nil || ts.st["stage"] != "merge" {
			continue
		}
		busy := ts.st["status"] == "tab" || ts.st["status"] == "launching"
		for _, ag := range herdr.AgentsInPath(agents, t.Path) {
			busy = busy || ag.Status == herdr.Working || ag.Status == herdr.Blocked
		}
		if !busy {
			continue
		}
		prs, err := gh.ListHead(c.Ctx, t.ID, false)
		if err != nil {
			return t.ID
		}
		for _, p := range gh.Mine(prs, t.ID) {
			if p.State == "OPEN" {
				return t.ID
			}
		}
	}
	return ""
}

// dropOrder forgets a ticket's merge order and records why: cancelled by
// tkt merge --cancel, or dropped by a new head.
func (c *Ctx) dropOrder(ts *ticketState, id, why string) {
	c.save(ts, "merge_head", "", "merge_steps", "", "merge_as", "", "merge_at", "", "merge_sent", "")
	c.record(history.Event{Ticket: id, Event: history.Merge, Action: why})
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
