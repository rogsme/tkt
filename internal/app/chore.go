package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/lock"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/prompt"
	"gitlab.com/rogs/tkt/internal/state"
	"gitlab.com/rogs/tkt/internal/wt"
)

// stageChore is the stage a chore's run starts with: its agent gets the
// chore's prompt and choreRule.
const stageChore = "chore"

// choreRule follows a chore's prompt. A run has no ticket, so no lifecycle
// skill takes it: the rule says where the work goes and how the run ends,
// the two things tkt reads from it.
func choreRule(run, remote, base string) string {
	return "This is a scheduled chore with no ticket, on branch " + run + " in this worktree. Commit what you change on this branch, push it to " + remote +
		", and open one pull request from it to " + base + ". End with Status: READY_FOR_REVIEW once the pull request is open, or with Status: DONE, and nothing committed or pushed, when there is nothing to change. " +
		config.UnattendedLine
}

// choresPath is the file where tkt watch keeps, for each chore, the last slot
// it took or skipped, or the moment it first saw the chore.
func choresPath(common string) string { return filepath.Join(common, "tkt", "chores") }

// chore finds this repository's chore by name.
func (c *Ctx) chore(name string) (config.Chore, error) {
	var names []string
	for _, ch := range c.Cfg.Chores {
		if ch.Name == name {
			return ch, nil
		}
		names = append(names, ch.Name)
	}
	if len(names) == 0 {
		return config.Chore{}, apperr.New(apperr.UnknownChore, "no chore runs in %s; a [chores.NAME] table in %s adds one", c.Repo.Root, config.LocalPath(c.Repo.Root)).
			WithHint("tkt help config")
	}
	return config.Chore{}, apperr.New(apperr.UnknownChore, "no chore named '%s' runs in %s (chores: %s)", name, c.Repo.Root, strings.Join(names, " ")).
		WithHint("tkt chores")
}

// choreSlots says where a chore stands on its schedule: due is the last slot
// at or before now when it came after last, the slot tkt watch handled most
// recently, and zero otherwise; next is the first slot after now.
func choreSlots(ch config.Chore, last, now time.Time) (due, next time.Time) {
	now = now.Local()
	if p := ch.Schedule.Prev(now); !p.IsZero() && p.After(last) {
		due = p
	}
	return due, ch.Schedule.Next(now)
}

// choreStamps reads the last slot of each chore from choresPath. A chore's
// key is slot.NAME: the file's own updated key could be a chore's name.
func choreStamps(common string) map[string]time.Time {
	out := map[string]time.Time{}
	for k, v := range state.Read(choresPath(common)) {
		name, ok := strings.CutPrefix(k, "slot.")
		if t, err := time.Parse(time.RFC3339, v); ok && err == nil {
			out[name] = t
		}
	}
	return out
}

// stampChore records the chore's last slot.
func (c *Ctx) stampChore(name string, at time.Time) {
	if c.Dry {
		return
	}
	if _, err := state.Update(choresPath(c.Repo.Common), c.Now(), "slot."+name, at.UTC().Format(history.TimeFormat)); err != nil {
		c.UI.Warn("cannot write %s: %v", choresPath(c.Repo.Common), err)
	}
}

// choreRun is a run of a chore that has a worktree, and whether it is still
// open: its agent works on it, or its pull request waits for a merge. A run
// that merged, closed, or ended with nothing to change is not. Fresh says
// its start stopped before its agent got the prompt, such as at a startup
// dialog or on a pre-start hook that failed: tkt chore run goes on with it.
type choreRun struct {
	ID, Chore   string
	Open, Fresh bool
}

// openRun returns the chore's run that is still open, if any.
func openRun(runs []choreRun, chore string) (choreRun, bool) {
	for _, r := range runs {
		if r.Chore == chore && r.Open {
			return r, true
		}
	}
	return choreRun{}, false
}

// runOf judges a chore run's row. While the forge cannot be read, a run
// counts as open, so no second one starts beside it.
func runOf(row Row, forgeOK bool) choreRun {
	r := choreRun{ID: row.Ticket, Chore: row.Chore}
	r.Open = !forgeOK || row.PR == nil && row.Next.Reason != ReasonNoChanges || row.PR != nil && row.PR.State == "OPEN"
	busy := row.Agent != nil && (row.Agent.Status == herdr.Working || row.Agent.Status == herdr.Blocked)
	r.Fresh = r.Open && row.PR == nil && !busy && unstarted(row.Launch)
	return r
}

// unstarted reports whether a launch status says the agent never got its
// prompt: tkt chore run made the worktree, or opened the tab, or launched an
// agent that stopped at a startup dialog.
func unstarted(launch string) bool {
	return launch == "" || launch == "tab" || launch == "launching" || launch == "not_ready"
}

// claimRun marks the run's worktree as the chore's after tkt start failed
// once worktrunk had made it, such as on a pre-start hook that failed, and
// reports whether the run has a worktree. The run then shows in tkt ls,
// holds the next slot back instead of having a second worktree made beside
// it, and tkt chore run goes on with it.
func (c *Ctx) claimRun(run, chore string) bool {
	if c.Dry {
		return false
	}
	ctx := context.WithoutCancel(c.Ctx)
	es, err := c.WT.List(ctx)
	if err != nil {
		return false
	}
	e, ok := es.Find(run)
	if !ok || filepath.Base(e.Path) != run {
		return false
	}
	ts, err := c.stateAt(ctx, e.Path)
	if err != nil {
		return false
	}
	if ts.st["chore"] == "" {
		c.save(ts, "chore", chore, "ticket", run, "stage", stageChore)
	}
	return true
}

// choreRuns reads the chore runs that have a worktree.
func (c *Ctx) choreRuns(ctx context.Context, es wt.Entries) ([]choreRun, error) {
	var mine []ticketWorktree
	for _, t := range c.workWorktrees(es) {
		if t.Chore != "" {
			mine = append(mine, t)
		}
	}
	if len(mine) == 0 {
		return nil, nil
	}
	l, err := c.look(lookOpts{})
	if err != nil {
		return nil, err
	}
	var out []choreRun
	for _, t := range mine {
		row, _ := c.row(ctx, l, t)
		out = append(out, runOf(row, l.src.Forge == "ok"))
	}
	return out, nil
}

// runName picks the branch of a chore's run that starts at now: the one its
// branch setting names, or, when a branch, a worktree, the remote or any pull
// request has that name already, the first of it with -2 to -9 after it that
// is free. A run always starts afresh, never on an older run's branch: tkt
// finds a run's pull request by its branch, so a name an older pull request
// had, merged and deleted since, would bring that one back.
func (c *Ctx) runName(ch config.Chore, es wt.Entries, now time.Time) (string, error) {
	f := c.LC.Forge
	gh := forge.GH{R: c.R, Repo: f.Host + "/" + f.Repo, Name: f.Repo}
	haveGH := f.Kind == "GitHub" && f.Host != "" && f.Repo != "" && found("gh")
	base := ch.BranchFor(now.Local())
	for i := 1; i <= 9; i++ {
		name := base
		if i > 1 {
			name += "-" + strconv.Itoa(i)
		}
		if err := c.LC.ScratchName(name); err != nil {
			return "", apperr.New(apperr.ConfigInvalid, "chore '%s' names its run %s, which this repository cannot take: %s", ch.Name, name, apperr.As(err).Msg).
				WithHint("change the chore's branch setting in %s", config.LocalPath(c.Repo.Root))
		}
		root := c.Repo.Root
		if _, ok := es.Find(name); ok || c.Git.HasRef(c.Ctx, root, "refs/heads/"+name) || c.Git.HasRef(c.Ctx, root, "refs/remotes/"+f.Remote+"/"+name) {
			continue
		}
		if heads, reached := c.Git.LsRemoteHeads(c.Ctx, root, f.Remote, name); reached && heads != "" {
			continue
		}
		if haveGH {
			prs, err := gh.ListHead(c.Ctx, name, false)
			if err != nil {
				return "", err
			}
			if len(gh.Mine(prs, name)) > 0 {
				continue
			}
		}
		return name, nil
	}
	return "", apperr.New(apperr.ChoreRunning, "chore '%s' has runs named %s to %s-9 already", ch.Name, base, base).
		WithHint("tkt done the runs that merged, or delete their branches")
}

// ChoreRunOpts are tkt chore run's arguments.
type ChoreRunOpts struct {
	Name string
	Dry  bool
}

// ChoreRunResult is the run tkt chore run started: its branch and worktree,
// and its agent's tab, pane and session.
type ChoreRunResult struct {
	Chore    string   `json:"chore"`
	Run      string   `json:"run"`
	Worktree string   `json:"worktree"`
	Tab      string   `json:"tab"`
	Pane     string   `json:"pane"`
	Agent    string   `json:"agent"`
	Preset   string   `json:"preset"`
	Harness  string   `json:"harness"`
	Session  string   `json:"session"`
	DryRun   bool     `json:"dry_run"`
	Actions  []Action `json:"actions"`
}

func (r *ChoreRunResult) Text(w io.Writer) {
	fmt.Fprintf(w, "%s  %s  %s  tab %s  agent %s (%s, %s)\n", r.Chore, r.Run, r.Worktree, r.Tab, r.Agent, r.Preset, r.Harness)
}

// ChoreRun starts a run of a chore now: a branch from the fetched base, its
// worktree and tab, and an agent with the chore's prompt and the rule that
// says how a run ends. It refuses while the chore's last run is open, as
// tkt watch skips a slot then, unless that run's start stopped before its
// agent got the prompt: then it goes on with that run.
func (a *App) ChoreRun(ctx context.Context, o ChoreRunOpts) (*ChoreRunResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	ch, err := c.chore(o.Name)
	if err != nil {
		return nil, err
	}
	if err := c.needHerdr(); err != nil {
		return nil, err
	}
	// Two runs of one chore never start at once, from tkt watch and from
	// you; the run's own lock comes with tkt start. No branch has a dot,
	// so the chore's lock is never a run's.
	if !c.Dry {
		f, err := lock.Try(c.Repo.Common, "chore."+ch.Name)
		if err != nil {
			return nil, apperr.New(apperr.TicketLocked, "another tkt command starts a run of chore '%s'", ch.Name).
				WithHint("wait for it to finish, then tkt chores")
		}
		c.locks = append(c.locks, f)
	}
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return nil, err
	}
	runs, err := c.choreRuns(c.Ctx, es)
	if err != nil {
		return nil, err
	}
	run := ""
	switch r, open := openRun(runs, ch.Name); {
	case open && !r.Fresh:
		return nil, apperr.New(apperr.ChoreRunning, "chore '%s' has a run still open, %s: a chore runs again once its last run merged, or ended with nothing to change", ch.Name, r.ID).
			WithHint("tkt ls %s", r.ID).With("run", r.ID)
	case open:
		run = r.ID
		a.UI.Info("going on with %s, whose start stopped before its agent got the prompt", run)
	default:
		if run, err = c.runName(ch, es, c.Now()); err != nil {
			return nil, err
		}
	}
	f := c.LC.Forge
	// A command reads the lines after it as its arguments, so a prompt that
	// runs one goes alone, and the command says how the run ends.
	text := ch.Prompt
	if !strings.HasPrefix(text, "/") {
		text += "\n\n" + choreRule(run, f.Remote, f.Base)
	}
	sr, err := a.Start(ctx, StartOpts{Scratch: run, Prompt: prompt.Source{Text: text, Set: true}, As: ch.As, Dry: o.Dry, chore: ch.Name})
	if err != nil {
		if c.claimRun(run, ch.Name) {
			err = apperr.As(err).With("run", run)
		}
		return nil, err
	}
	return &ChoreRunResult{Chore: ch.Name, Run: run, Worktree: sr.Worktree, Tab: sr.Tab, Pane: sr.Pane, Agent: sr.Agent, Preset: sr.Preset,
		Harness: sr.Harness, Session: sr.Session, DryRun: sr.DryRun, Actions: append(append([]Action{}, c.Plan...), sr.Actions...)}, nil
}

// ChoresOpts are tkt chores's arguments.
type ChoresOpts struct{}

// ChoreInfo is one chore of the repository: its settings, the last slot tkt
// watch took or skipped (Last, "" before it first saw the chore), whether a
// slot came since that it has not taken yet (Due), the next slot, and its
// runs that have a worktree, with the one still open, which holds the next
// run back. Times are UTC.
type ChoreInfo struct {
	Name     string   `json:"name"`
	Schedule string   `json:"schedule"`
	Branch   string   `json:"branch"`
	As       string   `json:"as"`
	Last     string   `json:"last"`
	Due      bool     `json:"due"`
	Next     string   `json:"next"`
	Runs     []string `json:"runs"`
	Open     string   `json:"open"`
}

// ChoresResult lists the repository's chores and whether tkt watch runs to
// start them.
type ChoresResult struct {
	Repo   string      `json:"repo"`
	Watch  WatchStatus `json:"watch"`
	Chores []ChoreInfo `json:"chores"`
}

func (r *ChoresResult) Text(w io.Writer) {
	if len(r.Chores) == 0 {
		fmt.Fprintf(w, "no chore runs in %s\n", r.Repo)
	} else {
		var rows [][]string
		for _, ch := range r.Chores {
			last := localTime(ch.Last)
			if ch.Due {
				last += " (due)"
			}
			run := or(ch.Open, "-")
			if ch.Open != "" {
				run += " (open)"
			}
			rows = append(rows, []string{ch.Name, ch.Schedule, localTime(ch.Next), last, run})
		}
		output.Table(w, []string{"CHORE", "SCHEDULE", "NEXT", "LAST", "RUN"}, rows)
		if !r.Watch.Running {
			fmt.Fprintln(w, "tkt watch does not run for this repository, so no chore runs on schedule; tkt watch --install keeps one running")
		}
	}
}

// localTime renders a UTC time in local time, or "-".
func localTime(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return "-"
	}
	return t.Local().Format("Mon 2006-01-02 15:04")
}

// Chores lists the repository's chores with their next slot and their runs.
func (a *App) Chores(ctx context.Context, _ ChoresOpts) (*ChoresResult, error) {
	c, err := a.begin(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	res := &ChoresResult{Repo: c.Repo.Root, Watch: c.watchStatus(), Chores: []ChoreInfo{}}
	if len(c.Cfg.Chores) == 0 {
		return res, nil
	}
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return nil, err
	}
	runs, err := c.choreRuns(c.Ctx, es)
	if err != nil {
		return nil, err
	}
	stamps := choreStamps(c.Repo.Common)
	now := c.Now()
	for _, ch := range c.Cfg.Chores {
		info := ChoreInfo{Name: ch.Name, Schedule: ch.Schedule.String(), Branch: ch.Branch, As: ch.As, Runs: []string{}}
		if r, open := openRun(runs, ch.Name); open {
			info.Open = r.ID
		}
		last, seen := stamps[ch.Name]
		due, next := choreSlots(ch, last, now)
		if seen {
			info.Last, info.Due = last.UTC().Format(history.TimeFormat), !due.IsZero()
		}
		if !next.IsZero() {
			info.Next = next.UTC().Format(history.TimeFormat)
		}
		for _, r := range runs {
			if r.Chore == ch.Name && !slices.Contains(info.Runs, r.ID) {
				info.Runs = append(info.Runs, r.ID)
			}
		}
		res.Chores = append(res.Chores, info)
	}
	return res, nil
}
