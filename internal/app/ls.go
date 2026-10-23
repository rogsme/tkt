package app

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
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
	// Background counts the tasks an idle agent waits on, such as a CI
	// watcher; it goes on when they finish.
	Background int `json:"background"`
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
	// Paused says why a running loop waits for you, such as needs_you, and
	// Why names the rule behind it, such as agent_blocked. Both are empty
	// while the loop drives the ticket.
	Paused string `json:"paused"`
	Why    string `json:"why"`
}

// RowWatch is what tkt watch did with a ticket. Last is its last step and
// the head it took it on, such as run:ci@HEAD. Paused says why it leaves the
// ticket for you, round_limit or no_progress, while tkt watch runs and the
// head is the one it paused on.
type RowWatch struct {
	Last   string `json:"last"`
	Paused string `json:"paused"`
}

// ReasonAwaitingSlot is the next step of a ticket whose tkt auto loop waits
// for a free slot before it starts the ticket.
const ReasonAwaitingSlot = "awaiting_slot"

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
	// BaseMerge says how merging the base into a pull request that is
	// behind would go, clean or conflict, when tkt auto or tkt watch tried
	// it with git merge-tree; tkt ls leaves it "".
	BaseMerge string `json:"base_merge"`
	Next      Next   `json:"next"`
	// Stage is the stage tkt last gave the ticket's agent, and Launch how
	// its launch went (the state file's status).
	Stage  string   `json:"stage"`
	Launch string   `json:"launch"`
	Auto   *RowAuto `json:"auto"`
	// Hold says the ticket is on hold: tkt watch and tkt auto leave it
	// alone (tkt hold).
	Hold  bool      `json:"hold"`
	Watch *RowWatch `json:"watch"`
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

// LsResult is tkt ls's table, and whether tkt watch keeps the tickets
// moving.
type LsResult struct {
	Sources Sources     `json:"sources"`
	Watch   WatchStatus `json:"watch"`
	Rows    []Row       `json:"rows"`
	now     time.Time
}

// lsHeader names the columns of tkt ls. TIME is how long the ticket has been
// in its phase.
var lsHeader = []string{"TICKET", "PHASE", "TIME", "AGENT", "PR", "CHECKS", "REVIEW", "AUTO", "NEXT"}

func (r *LsResult) Text(w io.Writer) {
	if len(r.Rows) == 0 {
		return
	}
	var rows [][]string
	for _, row := range r.Rows {
		rows = append(rows, r.cells(row, r.now))
	}
	output.Table(w, lsHeader, rows)
}

// Pretty is tkt ls on a terminal: the same columns, with the phase colored
// by who acts next and the empty cells faint.
func (r *LsResult) Pretty(w io.Writer, _ string, width int) {
	if len(r.Rows) == 0 {
		return
	}
	var (
		head  = lipgloss.NewStyle().Bold(true).Faint(true)
		faint = lipgloss.NewStyle().Faint(true)
		bold  = lipgloss.NewStyle().Bold(true)
		you   = lipgloss.NewStyle().Foreground(lipgloss.Yellow).Bold(true)
		ready = lipgloss.NewStyle().Foreground(lipgloss.Green).Bold(true)
		busy  = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
		bad   = lipgloss.NewStyle().Foreground(lipgloss.Red)
	)
	cells := make([][]string, len(r.Rows))
	widths := make([]int, len(lsHeader))
	for i, h := range lsHeader {
		widths[i] = len(h)
	}
	for i, row := range r.Rows {
		cells[i] = r.cells(row, r.now)
		cells[i][1] = strings.ReplaceAll(cells[i][1], "_", " ")
		for j, c := range cells[i] {
			widths[j] = max(widths[j], len(c))
		}
	}
	last := len(lsHeader) - 1
	used := 2
	for _, n := range widths[:last] {
		used += n + 2
	}
	pad := func(s string, n int) string { return s + strings.Repeat(" ", max(0, n-len(s))) }
	fmt.Fprintln(w)
	line := "  "
	for i, h := range lsHeader[:last] {
		line += head.Render(pad(h, widths[i])) + "  "
	}
	fmt.Fprintln(w, line+head.Render(lsHeader[last]))
	for i, row := range r.Rows {
		line := "  "
		for j, c := range cells[i][:last] {
			s := pad(c, widths[j])
			switch {
			case j == 0:
				s = bold.Render(s)
			case j == 1:
				s = phaseStyle(row.Phase, you, ready, busy, faint).Render(s)
			case j == 2 && row.Stale:
				s = bad.Render(s)
			case c == "-":
				s = faint.Render(s)
			case j == 5 && row.Checks != nil && row.Checks.State == "fail":
				s = bad.Render(s)
			case j == 7 && (c == "paused" || c == "stalled"):
				s = you.Render(s)
			}
			line += s + "  "
		}
		next := cells[i][last]
		if width > used+10 && len(next) > width-used {
			next = next[:width-used-3] + "..."
		}
		if row.Next.Kind == "wait" || row.Next.Kind == "unknown" {
			next = faint.Render(next)
		}
		fmt.Fprintln(w, line+next)
	}
	fmt.Fprintln(w)
}

// phaseStyle colors a phase by who acts next: you, nobody (it is done or
// ready), or an agent.
func phaseStyle(phase string, you, ready, busy, faint lipgloss.Style) lipgloss.Style {
	switch phase {
	case PhaseAwaitingApproval, PhaseNeedsYou, PhaseNoAgent, PhaseUnknown:
		return you
	case PhaseReadyForHuman:
		return ready
	case PhaseMerged, PhaseClosed, PhaseQueued:
		return faint
	}
	return busy
}

func (r *LsResult) cells(row Row, now time.Time) []string {
	herdrOK := r.Sources.Herdr == "ok"
	forgeOK := r.Sources.Forge == "ok"
	ag := "?"
	if herdrOK {
		ag = "-"
		if row.Agent != nil {
			ag = row.Agent.Status
			if row.Agent.Background > 0 {
				ag = "waiting"
			}
		}
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
		} else if row.Behind != nil && *row.Behind > 0 {
			pr += " (" + strconv.Itoa(*row.Behind) + " behind)"
		}
	}
	if row.Checks != nil {
		checks = row.Checks.Text()
	}
	if row.Review != nil {
		review = row.Review.Value
		if !row.Review.AtHead {
			review += " (old)"
		}
	}
	phase := or(row.Phase, "-")
	if row.Stale {
		phase += " (stale)"
	}
	age := "-"
	if t, err := time.Parse(time.RFC3339, row.PhaseSince); err == nil && !now.IsZero() {
		age = Ago(now.Sub(t))
	}
	return []string{row.Ticket, phase, age, ag, pr, checks, review, autoCell(row.Auto, row.Hold), next}
}

// autoCell says what tkt auto does with a ticket: drives it, waits for you
// or for a slot, nothing because its loop is gone, or nothing because the
// ticket is on hold.
func autoCell(a *RowAuto, hold bool) string {
	switch {
	case hold:
		return "hold"
	case a == nil:
		return "-"
	case a.Running && a.Waiting == "slot":
		return "queued"
	case a.Running && a.Paused != "":
		return "paused"
	case a.Running:
		return "on"
	case a.Wanted:
		return "stalled"
	}
	return "-"
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
	res := &LsResult{Sources: l.src, Watch: c.watchStatus(), Rows: []Row{}, now: a.Now()}
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
	for _, q := range c.queuedTickets() {
		if (len(want) == 0 || slices.Contains(want, q.Ticket)) && !slices.ContainsFunc(tickets, func(t ticketWorktree) bool { return t.ID == q.Ticket }) {
			res.Rows = append(res.Rows, q)
			missing = slices.DeleteFunc(missing, func(id string) bool { return id == q.Ticket })
		}
	}
	if len(res.Rows) == 0 && len(want) == 0 {
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
	probe     bool
	// forgeErr is a failed pull request query. When the open pull requests
	// failed, src.Forge is failed too; when only the full list failed, the
	// open ones still count.
	forgeErr error
}

// lookOpts says what to read. With one to three ids, pull requests are read
// by head branch, which no list limit can cut off. tabs is off for callers
// that never show them, which saves a herdr call per workspace. probe tries
// the base merge of each open pull request that is behind, for the callers
// that act on it.
type lookOpts struct {
	ids   []string
	fetch bool
	tabs  bool
	probe bool
}

// look reads herdr, the forge and, when asked, the remote, in parallel.
func (c *Ctx) look(o lookOpts) (*look, error) {
	f := c.LC.Forge
	l := &look{src: Sources{Herdr: "unavailable", Forge: "unsupported", Fetch: "skipped"}, probe: o.probe}
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
	row.Hold = st["hold"] != ""
	row.Auto = &RowAuto{Wanted: st["auto"] != "", ApprovePlan: st["auto"] == autoPlan, Rounds: atoi(st["rounds"]),
		Stop: st["auto_stop"], Waiting: st["auto_wait"], Running: c.autoRunning(t.ID), Paused: st["auto_pause"], Why: st["auto_why"]}
	if !row.Auto.Running {
		row.Auto.Paused, row.Auto.Why = "", ""
	}
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
				if turn.Pending > 0 && row.LastStatus == "" && c.Now().Sub(turn.At) < backgroundGrace {
					row.Agent.Background = turn.Pending
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
	if l.probe && row.PR != nil && row.PR.State == "OPEN" && row.Behind != nil && *row.Behind > 0 {
		row.BaseMerge = c.probeBaseMerge(rctx, t.Path, row.PR.Head, l.baseRef)
	}
	row.Watch = &RowWatch{Last: st["watch_last"]}
	if p := st["watch_pause"]; p != "" && row.PR != nil && st["watch_head"] == row.PR.Head && c.watchRunning() {
		row.Watch.Paused = p
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

// probeBaseMerge tries merging the base into the pull request's head, or
// into the worktree's HEAD when the head is not here, without touching the
// worktree. It returns clean, conflict, or "" when git cannot tell.
func (c *Ctx) probeBaseMerge(ctx context.Context, path, head, base string) string {
	if head == "" || c.Git.Rev(ctx, path, head+"^{commit}") == "" {
		head = "HEAD"
	}
	m, err := c.Git.MergeTree(ctx, path, head, base)
	switch {
	case err != nil:
		return ""
	case m.Clean():
		return "clean"
	}
	return "conflict"
}

// queuedTickets lists the tickets whose tkt auto loop waits for a slot
// before the ticket has a worktree. A mark whose loop is gone counts for
// nothing.
func (c *Ctx) queuedTickets() []Row {
	dir := queuedDir(c.Repo.Common)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Row
	for _, e := range entries {
		id := e.Name()
		if e.IsDir() || !c.autoRunning(id) {
			continue
		}
		st := state.Read(filepath.Join(dir, id))
		out = append(out, Row{Ticket: id, Tabs: []herdr.Tab{}, Phase: PhaseQueued, PhaseSince: st["since"], Stage: "start",
			Next: Next{Kind: "wait", Reason: ReasonAwaitingSlot, Text: "wait for a free auto slot ([auto] max_parallel)"},
			Auto: &RowAuto{Wanted: true, ApprovePlan: st["mode"] == autoPlan, Running: true, Waiting: "slot"}})
	}
	return out
}

// backgroundGrace bounds how long an idle agent with unfinished background
// tasks counts as busy. A dev server it left running never reports back.
const backgroundGrace = time.Hour

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
