package app

import (
	"cmp"
	"context"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/runner"
	"gitlab.com/rogs/tkt/internal/state"
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

// RowAuto is what tkt auto does with a ticket.
type RowAuto struct {
	// Wanted is set while tkt auto drives the ticket; an interrupted loop
	// leaves it, so a new one can take over.
	Wanted      bool `json:"wanted"`
	ApprovePlan bool `json:"approve_plan"`
	// Running says a tkt auto loop holds the ticket right now.
	Running bool `json:"running"`
	Rounds  int  `json:"rounds"`
	// Stop is why the last loop stopped.
	Stop string `json:"stop"`
	// Waiting is "slot" while the loop waits for a free slot.
	Waiting string `json:"waiting"`
}

// Row is one ticket in tkt ls. A null field is unknown or absent; Sources
// says which sources were read. Times are UTC, as 2006-01-02T15:04:05Z.
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
	// Stage is the stage tkt last gave the ticket's agent, and Launch how
	// its launch went (the state file's status).
	Stage  string   `json:"stage"`
	Launch string   `json:"launch"`
	Auto   *RowAuto `json:"auto"`
	// Phase is where the ticket stands, since PhaseSince.
	Phase      string `json:"phase"`
	PhaseSince string `json:"phase_since"`
	Started    string `json:"started"`
	// IdleSince is the agent's last activity while it is idle; Stale says it
	// has sat idle with no status line for longer than [board] stale_after.
	IdleSince string `json:"idle_since"`
	Stale     bool   `json:"stale"`
	Updated   string `json:"updated"`
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
	now     time.Time
}

func (r *LsResult) Text(w io.Writer) {
	if len(r.Rows) == 0 {
		return
	}
	header := []string{"TICKET", "TAB", "AGENT", "LAST STATUS", "PR", "CHECKS", "REVIEW", "BEHIND", "PHASE", "AGE", "NEXT"}
	var rows [][]string
	for _, row := range r.Rows {
		rows = append(rows, r.cells(row, r.now))
	}
	output.Table(w, header, rows)
}

func (r *LsResult) cells(row Row, now time.Time) []string {
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
	phase := or(row.Phase, "-")
	if row.Stale {
		phase += " (stale)"
	}
	age := "-"
	if t, err := time.Parse(time.RFC3339, row.PhaseSince); err == nil && !now.IsZero() {
		age = Ago(now.Sub(t))
	}
	return []string{row.Ticket, tab, ag, last, pr, checks, review, behind, phase, age, next}
}

// Ago renders a duration for people: 45s, 12m, 3h, 2d.
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d"
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Ls lists every ticket worktree with its agent, pull request, checks and
// review, and the next step. It also records each ticket's phase when it
// changed, which is what tkt stats and the time in phase come from.
func (a *App) Ls(ctx context.Context, o LsOpts) (*LsResult, error) {
	c, err := a.begin(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	var want []string
	for _, raw := range o.IDs {
		id, err := c.LC.NormalizeID(raw)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(want, id) {
			want = append(want, id)
		}
	}
	var (
		entries wt.Entries
		wtErr   error
		l       *look
		lookErr error
	)
	g := new(errgroup.Group)
	g.Go(func() error { entries, wtErr = c.WT.List(c.Ctx); return nil })
	g.Go(func() error { l, lookErr = c.look(lookOpts{ids: want, fetch: !o.NoFetch, tabs: true}); return nil })
	g.Wait()
	if wtErr != nil {
		return nil, wtErr
	}
	if lookErr != nil {
		return nil, lookErr
	}
	res := &LsResult{Sources: l.src, Rows: []Row{}, now: a.Now()}
	if res.Sources.Fetch == "failed" {
		a.UI.Warn("git fetch failed; showing the last fetched state")
	}
	if l.forgeErr != nil {
		a.UI.Warn("%s", l.forgeErr.Error())
	}

	tickets := c.ticketWorktrees(entries)
	var missing []string
	if len(want) > 0 {
		var picked []ticketWorktree
		for _, t := range tickets {
			if slices.Contains(want, t.ID) {
				picked = append(picked, t)
			}
		}
		for _, id := range want {
			if !slices.ContainsFunc(picked, func(t ticketWorktree) bool { return t.ID == id }) {
				missing = append(missing, id)
			}
		}
		tickets = picked
	}
	res.Rows = make([]Row, len(tickets))
	rows := new(errgroup.Group)
	rows.SetLimit(8)
	for i, t := range tickets {
		rows.Go(func() error {
			res.Rows[i], _ = c.row(c.Ctx, l, t)
			return nil
		})
	}
	rows.Wait()
	if len(tickets) == 0 && len(want) == 0 {
		a.UI.Info("no ticket worktrees")
	}
	if res.Sources.Herdr != "ok" && l.forgeErr == nil && a.Getenv("HERDR_ENV") != "1" {
		a.UI.Info("outside herdr: tab and agent columns show ?")
	}
	if len(missing) > 0 {
		return res, apperr.New(apperr.WorktreeMissing, "%s has no worktree", strings.Join(missing, ", ")).
			WithHint("tkt start %s", missing[0]).With("rows", res.Rows).With("missing", missing)
	}
	if l.forgeErr != nil {
		e := apperr.As(l.forgeErr)
		return res, apperr.New(e.Code, "%s", e.Msg).With("rows", res.Rows).Wrap(l.forgeErr)
	}
	return res, nil
}

// look is one reading of the sources tkt ls combines, shared by its rows.
type look struct {
	src       Sources
	agents    []herdr.Agent
	tabs      []herdr.Tab
	open, all []forge.PR
	gh        forge.GH
	baseRef   string
	hasBase   bool
	// forgeErr is a failed pull request query. When the open pull requests
	// failed, src.Forge is failed too; when only the full list failed, the
	// open ones still count.
	forgeErr error
}

// lookOpts says what to read. With one to three ids, pull requests are read
// by head branch, which no list limit can cut off. tabs is off for callers
// that never show them, which saves a herdr call per workspace.
type lookOpts struct {
	ids   []string
	fetch bool
	tabs  bool
}

// look reads herdr, the forge and, when asked, the remote, in parallel.
func (c *Ctx) look(o lookOpts) (*look, error) {
	f := c.LC.Forge
	l := &look{src: Sources{Herdr: "unavailable", Forge: "unsupported", Fetch: "skipped"}}
	haveHerdr := c.Getenv("HERDR_ENV") == "1" && found("herdr")
	l.gh = forge.GH{R: c.R, Repo: f.Host + "/" + f.Repo, Name: f.Repo}
	haveGH := f.Kind == "GitHub" && found("gh") && f.Host != "" && f.Repo != ""
	var (
		agentsErr, tabsErr error
		openErr, allErr    error
	)
	g := new(errgroup.Group)
	if o.fetch {
		g.Go(func() error {
			if r := c.Git.Fetch(c.Ctx, c.Repo.Root, f.Remote); r.OK() {
				l.src.Fetch = "ok"
			} else {
				l.src.Fetch = "failed"
			}
			return nil
		})
	}
	if haveHerdr {
		g.Go(func() error { l.agents, agentsErr = c.H.Agents(c.Ctx); return nil })
		if o.tabs {
			g.Go(func() error { l.tabs, tabsErr = c.H.Tabs(c.Ctx); return nil })
		}
	}
	switch {
	case haveGH && len(o.ids) > 0 && len(o.ids) < 4:
		var mu sync.Mutex
		for _, id := range o.ids {
			g.Go(func() error {
				prs, err := l.gh.ListHead(c.Ctx, id, true)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					openErr = err
				}
				for _, p := range prs {
					l.all = append(l.all, p)
					if p.State == "OPEN" {
						l.open = append(l.open, p)
					}
				}
				return nil
			})
		}
	case haveGH:
		g.Go(func() error { l.open, openErr = l.gh.ListOpen(c.Ctx); return nil })
		g.Go(func() error { l.all, allErr = l.gh.ListAll(c.Ctx); return nil })
	}
	g.Wait()
	if err := cmp.Or(agentsErr, tabsErr); err != nil {
		return nil, herdrErr(err)
	}
	if haveHerdr {
		l.src.Herdr = "ok"
	}
	if haveGH {
		l.src.Forge = "ok"
		if openErr != nil {
			l.src.Forge, l.forgeErr = "failed", openErr
		} else if allErr != nil {
			l.forgeErr = allErr
		}
	}
	l.baseRef = "refs/remotes/" + f.Remote + "/" + f.Base
	l.hasBase = c.Git.HasRef(c.Ctx, c.Repo.Root, l.baseRef)
	return l, nil
}

// row describes one ticket from a look, its state file and its agent's
// session, and records its phase when it changed. It returns the facts its
// next step came from, for tkt auto.
func (c *Ctx) row(ctx context.Context, l *look, t ticketWorktree) (Row, Facts) {
	rctx := runner.WithTicket(ctx, t.ID)
	row := Row{Ticket: t.ID, Path: t.Path}
	ts, err := c.stateAt(rctx, t.Path)
	if err != nil {
		ts = &ticketState{st: state.State{}}
	}
	st := ts.st
	row.Stage, row.Launch, row.Started, row.Updated = st["stage"], st["status"], st["started"], st["updated"]
	row.Auto = &RowAuto{Wanted: st["auto"] != "", ApprovePlan: st["auto"] == autoPlan, Rounds: atoi(st["rounds"]),
		Stop: st["auto_stop"], Waiting: st["auto_wait"], Running: c.autoRunning(t.ID)}
	if l.src.Herdr == "ok" {
		row.Tabs = herdr.TicketTabs(l.tabs, t.ID)
		if row.Tabs == nil {
			row.Tabs = []herdr.Tab{}
		}
		if in := herdr.AgentsInPath(l.agents, t.Path); len(in) > 0 {
			ag := in[0]
			if i := slices.IndexFunc(in, func(a herdr.Agent) bool { return a.PaneID == st["pane"] }); i >= 0 {
				ag = in[i]
			}
			row.Agent = &RowAgent{PaneID: ag.PaneID, Name: ag.NameOr(""), Harness: ag.Kind, Status: ag.Status}
			if ag.Status == herdr.Idle || ag.Status == herdr.Done {
				turn, _ := c.lastTurn(rctx, &ag, st, t.Path)
				row.LastStatus = agent.LastStatus(turn.All)
				if !turn.At.IsZero() {
					row.IdleSince = turn.At.UTC().Format(history.TimeFormat)
				}
			}
		}
	}
	if l.src.Forge == "ok" {
		if mine := l.gh.Mine(l.open, t.ID); len(mine) > 0 {
			p := mine[0]
			checks, verdict := forge.Summarize(p), forge.LatestVerdict(p)
			row.PR = &RowPR{Number: p.Number, State: "OPEN", Mergeable: p.Mergeable, Head: p.HeadRefOid}
			row.Checks, row.Review = &checks, verdict
		} else if p, ok := closedPR(l.gh.Mine(l.all, t.ID)); ok {
			row.PR = &RowPR{Number: p.Number, State: p.State}
		}
	}
	if l.hasBase {
		if n := c.Git.Count(rctx, t.Path, "HEAD.."+l.baseRef); n >= 0 {
			row.Behind = &n
		}
	}
	pf := state.Read(phasePath(ts))
	f := factsFor(row)
	f.BotWait = c.Cfg.Review.BotWait
	if pf["phase"] == PhaseWaitingReview && f.Head != "" && pf["head"] == f.Head {
		if since, err := time.Parse(time.RFC3339, pf["since"]); err == nil {
			f.Waited = c.Now().Sub(since)
		}
	}
	if l.src.Forge != "ok" {
		row.Next = Next{Kind: "unknown", Reason: ReasonUnknown, Text: "?"}
		row.Phase, row.PhaseSince = PhaseUnknown, ""
	} else {
		row.Next = NextStep(f)
		row.Phase = PhaseOf(f, row.Next)
		row.PhaseSince = pf["since"]
		if l.src.Herdr == "ok" {
			row.PhaseSince = c.observePhase(ts, t.ID, row.Phase, f.Head)
		}
	}
	if stale := c.Cfg.Board.StaleAfter; stale > 0 && row.Agent != nil && row.LastStatus == "" && row.IdleSince != "" {
		if at, err := time.Parse(time.RFC3339, row.IdleSince); err == nil && c.Now().Sub(at) > stale {
			row.Stale = true
		}
	}
	return row, f
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
