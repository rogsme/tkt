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
		if t.BranchDeleted {
			fmt.Fprintf(w, "%s  removed %s and branch %s\n", t.Ticket, t.Path, t.Ticket)
		} else {
			fmt.Fprintf(w, "%s  removed %s; worktrunk kept branch %s (not integrated into %s)\n", t.Ticket, t.Path, t.Ticket, r.base)
		}
		if t.Elapsed > 0 {
			fmt.Fprintf(w, "%s  %s from start to merge\n", t.Ticket, Ago(time.Duration(t.Elapsed)*time.Second))
		}
	}
}

// Done closes the tabs and removes the worktree of each merged ticket.
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
		tickets = c.ticketWorktrees(es)
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
			id, err := c.LC.NormalizeID(raw)
			if err != nil {
				return nil, err
			}
			path := ""
			if e, ok := es.Find(id); ok {
				path = e.Path
			}
			tickets = append(tickets, ticketWorktree{ID: id, Path: path})
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
		dt := DoneTicket{Ticket: t.ID, Path: t.Path, ClosedTabs: []string{}, Reasons: []string{}}
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
		reasons, closeTabs, merged := c.doneCheck(gh, t.ID, t.Path, herdr.AgentsInPath(agents, t.Path), panes, tabs)
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
		for _, tab := range closeTabs {
			if c.Dry {
				c.Would([]string{"herdr", "tab", "close", tab})
			} else if err := c.H.TabClose(c.Ctx, tab); err != nil {
				return nil, herdrErr(err)
			}
			dt.ClosedTabs = append(dt.ClosedTabs, tab)
		}
		started := ""
		if ts, err := c.readState(t.Path); err == nil {
			started = ts.st["started"]
		}
		dt.Started, dt.Merged = started, merged.MergedAt
		if s, err := time.Parse(time.RFC3339, started); err == nil {
			if m, err := time.Parse(time.RFC3339, merged.MergedAt); err == nil {
				dt.Elapsed = int64(m.Sub(s).Seconds())
			}
		}
		argv := c.WT.RemoveArgv(t.ID)
		if c.Dry {
			c.Would(argv)
			dt.Outcome = "planned"
			res.Tickets = append(res.Tickets, dt)
			continue
		}
		if r := c.WT.Visible(c.Ctx, argv); !r.OK() {
			return nil, apperr.New(apperr.WtRemoveFailed, "wt remove %s failed (see above)", t.ID)
		}
		c.record(history.Event{Ticket: t.ID, Event: history.Merged, PR: merged.Number, MergedAt: merged.MergedAt, OpenedAt: merged.CreatedAt})
		c.record(history.Event{Ticket: t.ID, Event: history.Done})
		dt.Outcome = "removed"
		dt.BranchDeleted = !c.Git.HasRef(c.Ctx, c.Repo.Root, "refs/heads/"+t.ID)
		res.Tickets = append(res.Tickets, dt)
	}
	res.Actions = c.Plan
	if len(kept) > 0 && !o.All {
		return res, apperr.New(apperr.TicketsKept, "kept %s; see the reasons above", strings.Join(kept, ", ")).
			WithHint("fix the reasons, then rerun tkt done").
			With("tickets", res.Tickets)
	}
	return res, nil
}

// doneCheck lists why a ticket's worktree must stay, the tabs to close, and
// the merged pull request.
func (c *Ctx) doneCheck(gh forge.GH, id, path string, agents []herdr.Agent, panes []herdr.Pane, tabs []herdr.Tab) (reasons, closeTabs []string, pr forge.PR) {
	f := c.LC.Forge
	oid := ""
	prs, err := gh.ListHead(c.Ctx, id, false)
	if err != nil {
		reasons = append(reasons, err.Error())
	} else {
		var open, merged []forge.PR
		for _, p := range gh.Mine(prs, id) {
			switch p.State {
			case "OPEN":
				open = append(open, p)
			case "MERGED":
				merged = append(merged, p)
			}
		}
		if len(open) > 0 {
			reasons = append(reasons, fmt.Sprintf("pull request %s is still open", forge.Ref(open[0].Number)))
		}
		switch len(merged) {
		case 0:
			reasons = append(reasons, fmt.Sprintf("no merged pull request has %s as its head branch", id))
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
		reasons = append(reasons, fmt.Sprintf("the branch tip %s is neither the merged head nor in %s/%s", short(tip), f.Remote, f.Base))
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
