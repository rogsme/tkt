package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/state"
)

// DoneOpts are tkt done's arguments.
type DoneOpts struct {
	IDs []string
	All bool
	Yes bool
	Dry bool
}

// DoneTicket is what tkt done did with one ticket.
type DoneTicket struct {
	Ticket string `json:"ticket"`
	Path   string `json:"path"`
	// Outcome is removed, kept, missing or planned (in a dry run).
	Outcome       string   `json:"outcome"`
	BranchDeleted bool     `json:"branch_deleted"`
	ClosedTabs    []string `json:"closed_tabs"`
	Reasons       []string `json:"reasons"`
	// Started is when tkt first started the ticket and Merged when the
	// forge merged it; Elapsed is the time between, in seconds, or 0 when
	// either is unknown.
	Started string `json:"started"`
	Merged  string `json:"merged"`
	Elapsed int64  `json:"elapsed_s"`
	// Closed is the pull request closed without merging whose ticket
	// tkt done removed, or 0.
	Closed int `json:"closed_pr"`
	// Unblocked lists the tickets finish-ticket said the merge unblocked:
	// their blockers are all done now.
	Unblocked []string `json:"unblocked"`
}

// DoneResult is what tkt done did.
type DoneResult struct {
	Tickets []DoneTicket `json:"tickets"`
	DryRun  bool         `json:"dry_run"`
	Actions []Action     `json:"actions"`
	base    string
}

func (r *DoneResult) Text(w io.Writer) {
	for _, t := range r.Tickets {
		if t.Outcome != "removed" {
			continue
		}
		if t.Closed > 0 {
			fmt.Fprintf(w, "%s  pull request %s was closed without merging; move the ticket in the tracker yourself\n", t.Ticket, forge.Ref(t.Closed))
		}
		if t.BranchDeleted {
			fmt.Fprintf(w, "%s  removed %s and branch %s\n", t.Ticket, t.Path, t.Ticket)
		} else {
			fmt.Fprintf(w, "%s  removed %s; worktrunk kept branch %s (not integrated into %s)\n", t.Ticket, t.Path, t.Ticket, r.base)
		}
		if t.Elapsed > 0 {
			fmt.Fprintf(w, "%s  %s from start to merge\n", t.Ticket, Ago(time.Duration(t.Elapsed)*time.Second))
		}
		if len(t.Unblocked) > 0 {
			fmt.Fprintf(w, "%s  the merge unblocked %s; tkt start %s starts one\n", t.Ticket, idsText(t.Unblocked), t.Unblocked[0])
		}
	}
}

// Done closes the tabs and removes the worktree of each ticket whose pull
// request merged, or closed without merging.
func (a *App) Done(ctx context.Context, o DoneOpts) (*DoneResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	if err := c.LC.RequireGitHub("done"); err != nil {
		return nil, err
	}
	if err := c.needTools("gh"); err != nil {
		return nil, err
	}
	if err := c.needHerdr(); err != nil {
		return nil, err
	}
	f := c.LC.Forge
	if c.Dry {
		a.UI.Warn("dry run: not fetching, so merged state is as of the last fetch")
	} else {
		r := c.Git.Fetch(c.Ctx, c.Repo.Root, f.Remote)
		if !r.OK() {
			a.Sleep(time.Second)
			r = c.Git.Fetch(c.Ctx, c.Repo.Root, f.Remote)
		}
		if !r.OK() {
			a.UI.Warn("git fetch failed: %s", strings.TrimSpace(r.ErrText()))
		}
	}
	res := &DoneResult{DryRun: c.Dry, Tickets: []DoneTicket{}, base: f.Base}
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return nil, err
	}
	var tickets []ticketWorktree
	if o.All {
		tickets = c.workWorktrees(es)
		if len(tickets) == 0 {
			a.UI.Info("no ticket worktrees")
			return res, nil
		}
		if !o.Yes {
			if !a.StdinTTY {
				return nil, apperr.New(apperr.ConfirmationRequired, "tkt done --all needs --yes when stdin is not a terminal").
					WithHint("tkt done --all --yes")
			}
			ids := make([]string, len(tickets))
			for i, t := range tickets {
				ids[i] = t.ID
			}
			a.UI.Info("check and remove these ticket worktrees: %s", strings.Join(ids, " "))
			fmt.Fprint(a.UI.Err, "tkt: continue? [y/N] ")
			var answer string
			fmt.Fscanln(a.Stdin, &answer)
			if !strings.HasPrefix(strings.ToLower(answer), "y") {
				return nil, apperr.New(apperr.Cancelled, "stopped")
			}
		}
	} else {
		for _, raw := range o.IDs {
			id, err := c.workID(raw)
			if err != nil {
				return nil, err
			}
			t := ticketWorktree{ID: id}
			if e, ok := es.Find(id); ok {
				t.Path, t.Chore = e.Path, c.choreOf(e)
			}
			tickets = append(tickets, t)
		}
	}
	agents, err := c.H.Agents(c.Ctx)
	if err != nil {
		return nil, herdrErr(err)
	}
	panes, err := c.H.Panes(c.Ctx)
	if err != nil {
		return nil, herdrErr(err)
	}
	tabs, err := c.H.Tabs(c.Ctx)
	if err != nil {
		return nil, herdrErr(err)
	}
	gh := forge.GH{R: a.R, Repo: f.Host + "/" + f.Repo, Name: f.Repo}
	kept := []string{}
	for _, t := range tickets {
		c.ticket(t.ID)
		dt := DoneTicket{Ticket: t.ID, Path: t.Path, ClosedTabs: []string{}, Reasons: []string{}, Unblocked: []string{}}
		if t.Path == "" {
			a.UI.Warn("%s: no worktree", t.ID)
			dt.Outcome = "missing"
			res.Tickets = append(res.Tickets, dt)
			kept = append(kept, t.ID)
			continue
		}
		if err := c.lockTicket(t.ID); err != nil {
			return nil, err
		}
		reasons, closeTabs, pr := c.doneCheck(gh, t, herdr.AgentsInPath(agents, t.Path), panes, tabs)
		if len(reasons) > 0 {
			a.UI.Info("%s kept:", t.ID)
			for _, r := range reasons {
				a.UI.Raw("  - %s", r)
			}
			dt.Outcome, dt.Reasons = "kept", reasons
			res.Tickets = append(res.Tickets, dt)
			kept = append(kept, t.ID)
			continue
		}
		st := state.State{}
		if ts, err := c.readState(t.Path); err == nil {
			st = ts.st
		}
		// finish-ticket's report names the tickets the merge unblocked, and
		// its session holds the report until the tabs close. A chore's run
		// has no ticket, and no finish-ticket; nor does a closed pull request.
		if t.Chore == "" && pr.State != "CLOSED" {
			dt.Unblocked = c.sessionUnblocked(t.ID, t.Path, herdr.AgentsInPath(agents, t.Path), st)
		}
		for _, tab := range closeTabs {
			if c.Dry {
				c.Would([]string{"herdr", "tab", "close", tab})
			} else if err := c.H.TabClose(c.Ctx, tab); err != nil {
				return nil, herdrErr(err)
			}
			dt.ClosedTabs = append(dt.ClosedTabs, tab)
		}
		started, tmp := st["started"], st["tmpdir"]
		dt.Started, dt.Merged = started, pr.MergedAt
		if pr.State == "CLOSED" {
			dt.Closed = pr.Number
		}
		if s, err := time.Parse(time.RFC3339, started); err == nil {
			if m, err := time.Parse(time.RFC3339, pr.MergedAt); err == nil {
				dt.Elapsed = int64(m.Sub(s).Seconds())
			}
		}
		argv := c.WT.RemoveArgv(t.ID)
		if c.Dry {
			c.Would(argv)
			c.removeTmp(t.ID, tmp)
			dt.Outcome = "planned"
			res.Tickets = append(res.Tickets, dt)
			continue
		}
		if r := c.WT.Visible(c.Ctx, argv); !r.OK() {
			return nil, apperr.New(apperr.WtRemoveFailed, "wt remove %s failed (see above)", t.ID)
		}
		c.removeTmp(t.ID, tmp)
		// A chore's run that changed nothing merged nothing.
		switch {
		case pr.State == "CLOSED":
			c.record(history.Event{Ticket: t.ID, Event: history.Closed, PR: pr.Number, OpenedAt: pr.CreatedAt})
		case pr.Number > 0:
			c.record(history.Event{Ticket: t.ID, Event: history.Merged, PR: pr.Number, MergedAt: pr.MergedAt, OpenedAt: pr.CreatedAt})
		}
		c.record(history.Event{Ticket: t.ID, Event: history.Done, Unblocked: dt.Unblocked})
		c.noteUnblocked(t.ID, dt.Unblocked)
		// A ticket another merge named, and that merged since, is done.
		c.forgetUnblocked(t.ID)
		dt.Outcome = "removed"
		dt.BranchDeleted = !c.Git.HasRef(c.Ctx, c.Repo.Root, "refs/heads/"+t.ID)
		res.Tickets = append(res.Tickets, dt)
	}
	res.Actions = c.Plan
	if len(kept) > 0 && !o.All {
		// The reasons go in the message itself: a program reading --json
		// never sees the lines above.
		var why []string
		for _, dt := range res.Tickets {
			if slices.Contains(kept, dt.Ticket) {
				switch {
				case len(dt.Reasons) > 0:
					why = append(why, dt.Ticket+": "+strings.Join(dt.Reasons, "; "))
				case dt.Outcome == "missing":
					why = append(why, dt.Ticket+": no worktree")
				}
			}
		}
		return res, apperr.New(apperr.TicketsKept, "kept %s", strings.Join(why, "; ")).
			WithHint("fix the reasons, then rerun tkt done").
			With("tickets", res.Tickets)
	}
	return res, nil
}

// doneCheck lists why a ticket's worktree must stay, the tabs to close, and
// the merged pull request, or the one closed without merging: closing it
// rejected the work. A chore's run goes too when it never opened one and its
// branch holds nothing the base lacks: it ended with nothing to change.
func (c *Ctx) doneCheck(gh forge.GH, t ticketWorktree, agents []herdr.Agent, panes []herdr.Pane, tabs []herdr.Tab) (reasons, closeTabs []string, pr forge.PR) {
	id, path := t.ID, t.Path
	f := c.LC.Forge
	oid := ""
	prs, err := gh.ListHead(c.Ctx, id, false)
	if err != nil {
		reasons = append(reasons, err.Error())
	} else {
		var open, merged, closed []forge.PR
		for _, p := range gh.Mine(prs, id) {
			switch p.State {
			case "OPEN":
				open = append(open, p)
			case "MERGED":
				merged = append(merged, p)
			case "CLOSED":
				closed = append(closed, p)
			}
		}
		if len(open) > 0 {
			reasons = append(reasons, fmt.Sprintf("pull request %s is still open", forge.Ref(open[0].Number)))
		}
		switch len(merged) {
		case 0:
			switch {
			case len(closed) > 0 && len(open) == 0:
				// A pull request closed without merging drops the work, and
				// it keeps the work's commits: its head must be all the
				// branch holds, so nothing newer is lost.
				oid, pr = closed[0].HeadRefOid, closed[0]
			case t.Chore == "":
				reasons = append(reasons, fmt.Sprintf("no merged or closed pull request has %s as its head branch", id))
			}
		case 1:
		default:
			reasons = append(reasons, fmt.Sprintf("several merged pull requests have %s as their head branch", id))
		}
		if len(merged) > 0 {
			oid, pr = merged[0].HeadRefOid, merged[0]
		}
	}
	if !c.Git.Clean(c.Ctx, path) {
		reasons = append(reasons, "the worktree has uncommitted changes")
	}
	tip := c.Git.Head(c.Ctx, path)
	if tip != oid && !c.Git.IsAncestor(c.Ctx, c.Repo.Root, tip, "refs/remotes/"+f.Remote+"/"+f.Base) {
		reasons = append(reasons, fmt.Sprintf("the branch tip %s is neither the pull request's head nor in %s/%s", short(tip), f.Remote, f.Base))
	}
	for _, a := range agents {
		if a.Status == herdr.Working || a.Status == herdr.Blocked {
			reasons = append(reasons, "an agent in the worktree is working or waiting for an answer")
			break
		}
	}
	if herdr.Inside(c.Cwd, path) {
		reasons = append(reasons, "tkt runs inside the worktree; run it from another directory")
	}
	for _, t := range herdr.TicketTabs(tabs, id) {
		if t.TabID == c.Getenv("HERDR_TAB_ID") {
			reasons = append(reasons, "tkt runs in the ticket's own tab")
		}
		all := true
		for _, p := range panes {
			if p.TabID == t.TabID && !herdr.Inside(p.Cwd, path) {
				all = false
			}
		}
		if all {
			closeTabs = append(closeTabs, t.TabID)
		}
	}
	var foreign []string
	for _, p := range panes {
		if herdr.Inside(p.Cwd, path) && !slices.Contains(closeTabs, p.TabID) {
			foreign = append(foreign, p.PaneID)
		}
	}
	if len(foreign) > 0 {
		reasons = append(reasons, "panes outside the ticket's tabs work in the worktree: "+strings.Join(foreign, ", "))
	}
	if openCodeServesFrom(path) {
		reasons = append(reasons, "the OpenCode service runs from inside the worktree, and removing it would stop that service")
	}
	return reasons, closeTabs, pr
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// openCodeServesFrom reports whether an `opencode serve` process runs with
// its working directory inside path. Without /proc it reports false.
func openCodeServesFrom(path string) bool {
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, f := range procs {
		b, err := os.ReadFile(f)
		if err != nil || !strings.Contains(strings.ReplaceAll(string(b), "\x00", " "), "opencode serve") {
			continue
		}
		if cwd, err := os.Readlink(filepath.Join(filepath.Dir(f), "cwd")); err == nil && herdr.Inside(cwd, path) {
			return true
		}
	}
	return false
}
