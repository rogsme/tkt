package app

import (
	"cmp"
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/runner"
	"gitlab.com/rogs/tkt/internal/wt"
)

// LsOpts are tkt ls's arguments.
type LsOpts struct {
	IDs     []string
	NoFetch bool
}

// RowAgent is the agent working in a ticket's worktree.
type RowAgent struct {
	PaneID  string `json:"pane_id"`
	Name    string `json:"name"`
	Harness string `json:"harness"`
	Status  string `json:"status"`
}

// RowPR is a ticket's pull request.
type RowPR struct {
	Number    int    `json:"number"`
	State     string `json:"state"`
	Mergeable string `json:"mergeable,omitempty"`
	Head      string `json:"head,omitempty"`
}

// Row is one ticket in tkt ls. A null field is unknown or absent; Sources
// says which sources were read.
type Row struct {
	Ticket     string         `json:"ticket"`
	Path       string         `json:"path"`
	Tabs       []herdr.Tab    `json:"tabs"`
	Agent      *RowAgent      `json:"agent"`
	LastStatus string         `json:"last_status"`
	PR         *RowPR         `json:"pr"`
	Checks     *forge.Checks  `json:"checks"`
	Review     *forge.Verdict `json:"review"`
	Behind     *int           `json:"behind"`
	Next       Next           `json:"next"`
}

// Sources says what tkt ls could read: ok, unavailable (not installed or
// not inside herdr), unsupported (forge), skipped (fetch) or failed.
type Sources struct {
	Herdr string `json:"herdr"`
	Forge string `json:"forge"`
	Fetch string `json:"fetch"`
}

// LsResult is tkt ls's table.
type LsResult struct {
	Sources Sources `json:"sources"`
	Rows    []Row   `json:"rows"`
}

func (r *LsResult) Text(w io.Writer) {
	if len(r.Rows) == 0 {
		return
	}
	header := []string{"TICKET", "TAB", "AGENT", "LAST STATUS", "PR", "CHECKS", "REVIEW", "BEHIND", "NEXT"}
	var rows [][]string
	for _, row := range r.Rows {
		rows = append(rows, r.cells(row))
	}
	output.Table(w, header, rows)
}

func (r *LsResult) cells(row Row) []string {
	herdrOK := r.Sources.Herdr == "ok"
	forgeOK := r.Sources.Forge == "ok"
	tab, ag, last := "?", "?", "-"
	if herdrOK {
		labels := make([]string, len(row.Tabs))
		for i, t := range row.Tabs {
			labels[i] = t.Label
		}
		tab = or(strings.Join(labels, ", "), "-")
		ag = "-"
		if row.Agent != nil {
			ag = row.Agent.Status
		}
	}
	if row.LastStatus != "" {
		last = row.LastStatus
	}
	pr, checks, review, next := "?", "-", "-", "?"
	if forgeOK {
		pr = "-"
		next = row.Next.Text
	}
	if row.PR != nil {
		pr = forge.Ref(row.PR.Number)
		if row.PR.State != "OPEN" {
			pr += " " + strings.ToLower(row.PR.State)
		}
	}
	if row.Checks != nil {
		checks = row.Checks.Text()
	}
	if row.Review != nil {
		review = row.Review.Value
		if !row.Review.AtHead {
			review += " (old head)"
		}
	}
	behind := "-"
	if row.Behind != nil {
		behind = strconv.Itoa(*row.Behind)
	}
	return []string{row.Ticket, tab, ag, last, pr, checks, review, behind, next}
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Ls lists every ticket worktree with its agent, pull request, checks and
// review, and the next step.
func (a *App) Ls(ctx context.Context, o LsOpts) (*LsResult, error) {
	c, err := a.begin(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	f := c.LC.Forge
	res := &LsResult{Sources: Sources{Herdr: "unavailable", Forge: "unsupported", Fetch: "skipped"}, Rows: []Row{}}
	haveHerdr := a.Getenv("HERDR_ENV") == "1" && found("herdr")
	gh := forge.GH{R: a.R, Repo: f.Host + "/" + f.Repo, Name: f.Repo}
	haveGH := f.Kind == "GitHub" && found("gh") && f.Host != "" && f.Repo != ""

	var (
		entries         wt.Entries
		wtErr           error
		agentsErr       error
		tabsErr         error
		agents          []herdr.Agent
		tabs            []herdr.Tab
		open, all       []forge.PR
		openErr, allErr error
	)
	g := new(errgroup.Group)
	g.Go(func() error { entries, wtErr = c.WT.List(c.Ctx); return nil })
	if !o.NoFetch {
		g.Go(func() error {
			if r := c.Git.Fetch(c.Ctx, c.Repo.Root, f.Remote); r.OK() {
				res.Sources.Fetch = "ok"
			} else {
				res.Sources.Fetch = "failed"
			}
			return nil
		})
	}
	if haveHerdr {
		g.Go(func() error { agents, agentsErr = c.H.Agents(c.Ctx); return nil })
		g.Go(func() error { tabs, tabsErr = c.H.Tabs(c.Ctx); return nil })
	}
	if haveGH {
		g.Go(func() error { open, openErr = gh.ListOpen(c.Ctx); return nil })
		g.Go(func() error { all, allErr = gh.ListAll(c.Ctx); return nil })
	}
	g.Wait()
	if wtErr != nil {
		return nil, wtErr
	}
	if err := cmp.Or(agentsErr, tabsErr); err != nil {
		return nil, herdrErr(err)
	}
	if res.Sources.Fetch == "failed" {
		a.UI.Warn("git fetch failed; showing the last fetched state")
	}
	if haveHerdr {
		res.Sources.Herdr = "ok"
	}
	var forgeErr error
	if haveGH {
		res.Sources.Forge = "ok"
		if openErr != nil {
			a.UI.Warn("%s", openErr.Error())
			res.Sources.Forge, forgeErr = "failed", openErr
		} else if allErr != nil {
			a.UI.Warn("%s", allErr.Error())
			forgeErr = allErr
		}
	}

	tickets := c.ticketWorktrees(entries)
	baseRef := "refs/remotes/" + f.Remote + "/" + f.Base
	hasBase := c.Git.HasRef(c.Ctx, c.Repo.Root, baseRef)
	res.Rows = make([]Row, len(tickets))
	rows := new(errgroup.Group)
	rows.SetLimit(8)
	for i, t := range tickets {
		rows.Go(func() error {
			rctx := runner.WithTicket(c.Ctx, t.ID)
			row := Row{Ticket: t.ID, Path: t.Path}
			if res.Sources.Herdr == "ok" {
				row.Tabs = herdr.TicketTabs(tabs, t.ID)
				if row.Tabs == nil {
					row.Tabs = []herdr.Tab{}
				}
				if in := herdr.AgentsInPath(agents, t.Path); len(in) > 0 {
					ag := in[0]
					row.Agent = &RowAgent{PaneID: ag.PaneID, Name: ag.NameOr(""), Harness: ag.Kind, Status: ag.Status}
					if ag.Status == herdr.Idle || ag.Status == herdr.Done {
						row.LastStatus = agent.LastStatus(c.H.Read(rctx, ag.PaneID, 40))
					}
				}
			}
			if res.Sources.Forge == "ok" {
				if mine := gh.Mine(open, t.ID); len(mine) > 0 {
					p := mine[0]
					checks, verdict := forge.Summarize(p), forge.LatestVerdict(p)
					row.PR = &RowPR{Number: p.Number, State: "OPEN", Mergeable: p.Mergeable, Head: p.HeadRefOid}
					row.Checks, row.Review = &checks, verdict
				} else if p, ok := closedPR(gh.Mine(all, t.ID)); ok {
					row.PR = &RowPR{Number: p.Number, State: p.State}
				}
			}
			if hasBase {
				if n := c.Git.Count(rctx, t.Path, "HEAD.."+baseRef); n >= 0 {
					row.Behind = &n
				}
			}
			if res.Sources.Forge == "ok" {
				row.Next = NextStep(factsFor(row))
			} else {
				row.Next = Next{Kind: "unknown", Text: "?"}
			}
			res.Rows[i] = row
			return nil
		})
	}
	rows.Wait()
	if len(tickets) == 0 {
		a.UI.Info("no ticket worktrees")
	}
	if !haveHerdr && forgeErr == nil && a.Getenv("HERDR_ENV") != "1" {
		a.UI.Info("outside herdr: tab and agent columns show ?")
	}
	if forgeErr != nil {
		e := apperr.As(forgeErr)
		return res, apperr.New(e.Code, "%s", e.Msg).With("rows", res.Rows).Wrap(forgeErr)
	}
	return res, nil
}

// closedPR picks a ticket's merged pull request, else its latest one.
func closedPR(prs []forge.PR) (forge.PR, bool) {
	for _, p := range prs {
		if p.State == "MERGED" {
			return p, true
		}
	}
	if len(prs) > 0 {
		return prs[0], true
	}
	return forge.PR{}, false
}

func found(tool string) bool {
	_, err := exec.LookPath(tool)
	return err == nil
}
