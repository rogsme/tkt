package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/lock"
	"gitlab.com/rogs/tkt/internal/runner"
)

// DropOpts are tkt drop's arguments.
type DropOpts struct {
	ID  string
	Yes bool
	Dry bool
}

// DropResult is what tkt drop removed, or what it would remove in a dry run.
type DropResult struct {
	Ticket string `json:"ticket"`
	// Chore names the chore whose run this is, or "".
	Chore string `json:"chore"`
	Path  string `json:"path"`
	// Outcome is dropped, or planned in a dry run.
	Outcome string `json:"outcome"`
	// Head is the branch's last commit: git branch ID HEAD brings the
	// branch back until git gc prunes it.
	Head string `json:"head"`
	// Commits counts the branch's commits that the base lacks, and
	// Unpushed those that no branch on the remote holds.
	Commits  int `json:"commits"`
	Unpushed int `json:"unpushed"`
	// Changes are the worktree's uncommitted changes, as git status --short
	// prints them. Nothing brings them back.
	Changes []string `json:"changes"`
	// Tabs are the ticket's tabs, which close with their agents; Working
	// says an agent in the worktree was working or waiting for an answer.
	Tabs    []string `json:"tabs"`
	Working bool     `json:"working"`
	// Auto says a tkt auto loop drove the ticket.
	Auto bool `json:"auto"`
	// PR is the open pull request tkt drop closes, or 0. RemoteBranch says
	// it deletes the branch from the remote.
	PR           int  `json:"pr"`
	RemoteBranch bool `json:"remote_branch"`
	// Kept says what tkt drop leaves on the forge, and why, or "".
	Kept string `json:"kept"`
	// Lines say what the drop removes, for a person to confirm.
	Lines   []string `json:"lines"`
	DryRun  bool     `json:"dry_run"`
	Actions []Action `json:"actions"`
	remote  string
}

func (r *DropResult) Text(w io.Writer) {
	if r.DryRun {
		fmt.Fprintf(w, "%s  tkt drop %s would remove:\n", r.Ticket, r.Ticket)
		for _, l := range r.Lines {
			fmt.Fprintf(w, "  - %s\n", l)
		}
		return
	}
	what := "removed " + r.Path + " and branch " + r.Ticket
	if len(r.Tabs) > 0 {
		what = "closed " + strings.Join(r.Tabs, ", ") + ", " + what
	}
	fmt.Fprintf(w, "%s  dropped: %s\n", r.Ticket, what)
	switch {
	case r.PR > 0 && r.RemoteBranch:
		fmt.Fprintf(w, "%s  closed pull request %s and deleted %s/%s\n", r.Ticket, forge.Ref(r.PR), r.remote, r.Ticket)
	case r.RemoteBranch:
		fmt.Fprintf(w, "%s  deleted %s/%s\n", r.Ticket, r.remote, r.Ticket)
	}
	if r.Kept != "" {
		fmt.Fprintf(w, "%s  kept: %s\n", r.Ticket, r.Kept)
	}
	if r.Head != "" {
		fmt.Fprintf(w, "%s  %s\n", r.Ticket, recoverLine(r.Ticket, r.Head))
	}
	if r.Chore == "" {
		fmt.Fprintf(w, "%s  tkt never writes the tracker; move %s back there yourself\n", r.Ticket, r.Ticket)
	}
}

func recoverLine(id, head string) string {
	return fmt.Sprintf("branch %s was at %s; git branch %s %s brings it back until git gc prunes it", id, short(head), id, short(head))
}

// droppedLine is the history's line for a drop.
func droppedLine(head string, pr int) string {
	line := "dropped with tkt drop"
	if head != "" {
		line += "; the branch was at " + short(head)
	}
	if pr > 0 {
		line += "; closed " + forge.Ref(pr) + " without merging"
	}
	return line
}

// lines say what the drop removes, for the question tkt drop asks and the
// one the chat and the web page ask.
func (r *DropResult) lines() []string {
	var out []string
	switch len(r.Tabs) {
	case 0:
	case 1:
		out = append(out, "tab "+r.Tabs[0]+" and the agent in it")
	default:
		out = append(out, "tabs "+strings.Join(r.Tabs, ", ")+" and the agents in them")
	}
	if r.Working {
		out = append(out, "an agent that is working or waiting for an answer, which stops mid-turn")
	}
	if r.Auto {
		out = append(out, "the tkt auto loop that drives it")
	}
	wt := "worktree " + r.Path
	switch n := len(r.Changes); {
	case n == 1:
		wt += ", with 1 uncommitted change, lost for good: " + changeNames(r.Changes)
	case n > 1:
		wt += fmt.Sprintf(", with %d uncommitted changes, lost for good: %s", n, changeNames(r.Changes))
	default:
		wt += ", with no uncommitted changes"
	}
	out = append(out, wt)
	br := "branch " + r.Ticket
	if r.Head != "" {
		br += " at " + short(r.Head)
	}
	switch {
	case r.Commits > 0 && r.Unpushed > 0:
		br += fmt.Sprintf(", with %d commit%s the base lacks, %d never pushed", r.Commits, plural(r.Commits), r.Unpushed)
	case r.Commits > 0:
		br += fmt.Sprintf(", with %d commit%s the base lacks", r.Commits, plural(r.Commits))
	case r.Commits == 0:
		br += ", with no commits the base lacks"
	}
	out = append(out, br)
	if r.PR > 0 {
		out = append(out, "pull request "+forge.Ref(r.PR)+", closed without merging")
	}
	if r.RemoteBranch {
		out = append(out, "branch "+r.Ticket+" on "+r.remote)
	}
	if r.Kept != "" {
		out = append(out, "it keeps: "+r.Kept)
	}
	if r.Chore == "" {
		out = append(out, "the tracker stays as it is: move "+r.Ticket+" back yourself")
	}
	return out
}

// changeNames names the first few changed paths.
func changeNames(changes []string) string {
	const most = 3
	var names []string
	for _, c := range changes[:min(len(changes), most)] {
		names = append(names, strings.TrimSpace(c[min(len(c), 2):]))
	}
	s := strings.Join(names, ", ")
	if len(changes) > most {
		s += fmt.Sprintf(" and %d more", len(changes)-most)
	}
	return s
}

// Drop throws away a ticket started by mistake, merged or not: its tabs and
// agents, its tkt auto loop, its worktree with the changes in it, its
// branch, its temporary directory, and, when the work there is yours, its
// open pull request and its branch on the remote. It never writes the
// tracker.
func (a *App) Drop(ctx context.Context, o DropOpts) (*DropResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	if err := c.needHerdr(); err != nil {
		return nil, err
	}
	id, err := c.workID(o.ID)
	if err != nil {
		return nil, err
	}
	c.ticket(id)
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return nil, err
	}
	e, ok := es.Find(id)
	if !ok {
		return nil, apperr.New(apperr.WorktreeMissing, "%s has no worktree, so tkt drop has nothing to remove", id).
			WithHint("git branch -D %s deletes a branch left behind", id)
	}
	if e.Path == c.Repo.Root {
		return nil, apperr.New(apperr.DropRefused, "%s is the repository's primary worktree", e.Path)
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	ts, err := c.readState(e.Path)
	if err != nil {
		return nil, err
	}
	st := ts.st
	f := c.LC.Forge
	res := &DropResult{Ticket: id, Chore: c.choreOf(e), Path: e.Path, Changes: []string{}, Tabs: []string{}, DryRun: c.Dry, Actions: []Action{}, remote: f.Remote}
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
	reasons, closeTabs := c.worktreeHeld(id, e.Path, panes, tabs)
	if len(reasons) > 0 {
		return nil, apperr.New(apperr.DropRefused, "cannot drop %s: %s", id, strings.Join(reasons, "; ")).
			WithHint("run tkt drop %s from another tab, outside the worktree, once only the ticket's tabs work there", id)
	}
	res.Tabs = append(res.Tabs, closeTabs...)
	for _, ag := range herdr.AgentsInPath(agents, e.Path) {
		if ag.Status == herdr.Working || ag.Status == herdr.Blocked {
			res.Working = true
		}
	}
	running := c.autoRunning(id)
	res.Auto = running || st["auto"] != ""
	res.Head = c.Git.Head(c.Ctx, e.Path)
	res.Commits = c.Git.Count(c.Ctx, e.Path, "refs/remotes/"+f.Remote+"/"+f.Base+"..HEAD")
	res.Unpushed = c.Git.Unpushed(c.Ctx, e.Path, f.Remote)
	if changes, ok := c.Git.Changes(c.Ctx, e.Path); ok {
		res.Changes = append(res.Changes, changes...)
	}
	gh := forge.GH{R: a.R, Repo: f.Host + "/" + f.Repo, Name: f.Repo}
	if err := c.dropForge(gh, id, res); err != nil {
		return nil, err
	}
	res.Lines = res.lines()
	if !o.Yes && !c.Dry {
		if err := c.confirmDrop(res); err != nil {
			return nil, err
		}
	}
	// What runs outside the tab goes first: OpenCode's turn, then the
	// forge. A failure there stops the drop before anything local goes, so
	// running tkt drop again starts over.
	for _, s := range [][2]string{{st["harness"], st["session"]}, {st[keyFixHarness], st[keyFixSession]}} {
		if s[0] != config.OpenCode || s[1] == "" || !c.OC.SessionActive(c.Ctx, s[1]) {
			continue
		}
		if c.Dry {
			c.Would(harness.InterruptArgv(s[1]))
		} else if err := c.OC.Interrupt(c.Ctx, s[1]); err != nil {
			return nil, hinted(err, "stop the agent in its tab, then run tkt drop %s again; nothing was removed", id)
		}
	}
	if res.PR > 0 {
		if c.Dry {
			c.Would(gh.CloseArgv(res.PR))
		} else if err := gh.Close(c.Ctx, res.PR); err != nil {
			return nil, hinted(err, "tkt drop %s tries again; nothing local was removed", id)
		}
	}
	if res.RemoteBranch {
		push := gitx.Argv(c.Repo.Root, "push", "--quiet", f.Remote, "--delete", id)
		if c.Dry {
			c.Would(push)
		} else if r := c.Git.Do(c.Ctx, 2*time.Minute, push); !r.OK() {
			return nil, apperr.New(apperr.GitFailed, "git push --delete %s failed: %s", id, runner.Head(strings.TrimSpace(r.ErrText()), 200)).
				WithHint("tkt drop %s tries again; nothing local was removed", id)
		}
	}
	if st["auto"] != "" {
		// As tkt auto --stop does: the loop ends at its next look.
		c.save(ts, "auto", "", "auto_stop", StopStopped, "auto_wait", "", "auto_pause", "", "yolo", "")
	}
	for _, tab := range res.Tabs {
		if c.Dry {
			c.Would([]string{"herdr", "tab", "close", tab})
		} else if err := c.H.TabClose(c.Ctx, tab); err != nil {
			return nil, herdrErr(err)
		}
	}
	if running && !c.Dry {
		// The loop sees the stop at its next look; one that sleeps until
		// then ends anyway once it finds the worktree gone.
		l, err := lock.Wait(c.Ctx, c.Repo.Common, id+".auto", c.Cfg.Auto.Poll+5*time.Second)
		switch {
		case err == nil:
			l.Close()
		case errors.Is(err, lock.ErrHeld):
			a.UI.Warn("%s: the tkt auto loop has not stopped yet; it ends when it finds the worktree gone", id)
		}
	}
	argv := c.WT.RemoveForceArgv(id)
	if c.Dry {
		c.Would(argv)
		c.removeTmp(id, st["tmpdir"])
		res.Outcome = "planned"
		res.Actions = c.Plan
		return res, nil
	}
	if r := c.WT.Visible(c.Ctx, argv); !r.OK() {
		return nil, apperr.New(apperr.WtRemoveFailed, "wt remove %s failed (see above)", id).
			WithHint("tkt drop %s tries again", id)
	}
	c.removeTmp(id, st["tmpdir"])
	c.record(history.Event{Ticket: id, Event: history.Dropped, Head: res.Head, PR: res.PR})
	res.Outcome = "dropped"
	res.Actions = c.Plan
	return res, nil
}

// hinted adds a hint to one of tkt's errors.
func hinted(err error, format string, args ...any) error {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.WithHint(format, args...)
	}
	return err
}

// dropForge finds what tkt drop removes on the forge: the branch on the
// remote and its open pull request, but only when every commit the branch
// holds beyond the base is yours. A branch someone else pushed first, which
// tkt start then checked out, stays with its pull request. It refuses a
// ticket whose pull request merged: tkt done removes that one.
func (c *Ctx) dropForge(gh forge.GH, id string, res *DropResult) error {
	f, root := c.LC.Forge, c.Repo.Root
	remote := f.Remote + "/" + id
	heads, reached := c.Git.LsRemoteHeads(c.Ctx, root, f.Remote, id)
	if !reached {
		res.Kept = fmt.Sprintf("tkt cannot reach %s, so it leaves %s and any pull request there alone", f.Remote, remote)
		return nil
	}
	tip := ""
	for _, l := range strings.Split(heads, "\n") {
		if sha, ref, ok := strings.Cut(l, "\t"); ok && ref == "refs/heads/"+id {
			tip = sha
		}
	}
	github := c.LC.RequireGitHub("drop") == nil
	var open []forge.PR
	if github {
		if _, err := exec.LookPath("gh"); err != nil {
			if tip != "" {
				res.Kept = fmt.Sprintf("gh is not installed, so tkt cannot tell whether a pull request uses %s; it leaves the branch alone", remote)
			}
			return nil
		}
		prs, err := gh.ListHead(c.Ctx, id, false)
		if err != nil {
			if tip != "" {
				res.Kept = fmt.Sprintf("tkt could not read the pull requests (%v), so it leaves %s and any pull request alone", err, remote)
			}
			return nil
		}
		for _, p := range gh.Mine(prs, id) {
			switch p.State {
			case "MERGED":
				return apperr.New(apperr.PullRequestMerged, "%s's pull request %s is merged; tkt drop throws away work that never merged", id, forge.Ref(p.Number)).
					WithHint("tkt done %s", id)
			case "OPEN":
				open = append(open, p)
			}
		}
	}
	if tip == "" {
		if len(open) > 0 {
			res.Kept = fmt.Sprintf("pull request %s, whose branch %s is gone", forge.Ref(open[0].Number), remote)
		}
		return nil
	}
	if !github {
		res.Kept = fmt.Sprintf("%s and any pull request on it: tkt reads pull requests through gh, which supports GitHub only, so close it and delete the branch on %s yourself", remote, f.Host)
		return nil
	}
	theirs, err := c.othersCommits(id, tip)
	what := remote
	if len(open) > 0 {
		what += " and pull request " + forge.Ref(open[0].Number)
	}
	switch {
	case err != nil:
		res.Kept = fmt.Sprintf("%s, since %v", what, err)
		return nil
	case len(theirs) > 0:
		res.Kept = fmt.Sprintf("%s, since the branch holds commits by %s; it may be someone else's", what, strings.Join(theirs, ", "))
		return nil
	}
	res.RemoteBranch = true
	if len(open) > 0 {
		res.PR = open[0].Number
	}
	return nil
}

// othersCommits fetches the remote branch whose tip is tip, and lists the
// authors other than you of its commits beyond the base.
func (c *Ctx) othersCommits(id, tip string) ([]string, error) {
	f, root := c.LC.Forge, c.Repo.Root
	me := c.Git.UserEmail(c.Ctx, root)
	if me == "" {
		return nil, fmt.Errorf("git has no user.email here, so tkt cannot tell your commits from others'")
	}
	argv := gitx.Argv(root, "fetch", "--quiet", f.Remote, f.Base, "+refs/heads/"+id+":refs/remotes/"+f.Remote+"/"+id)
	if r := c.Git.Do(c.Ctx, 2*time.Minute, argv); !r.OK() {
		return nil, fmt.Errorf("git fetch %s %s failed: %s", f.Remote, id, runner.Head(strings.TrimSpace(r.ErrText()), 200))
	}
	authors, ok := c.Git.Authors(c.Ctx, root, "refs/remotes/"+f.Remote+"/"+f.Base+".."+tip)
	if !ok {
		return nil, fmt.Errorf("git cannot list the commits on %s/%s", f.Remote, id)
	}
	var theirs []string
	for _, a := range authors {
		if !strings.EqualFold(a, me) && !slices.Contains(theirs, a) {
			theirs = append(theirs, a)
		}
	}
	return theirs, nil
}

// confirmDrop asks you to type the ticket's id, after the list of what goes.
func (c *Ctx) confirmDrop(res *DropResult) error {
	id := res.Ticket
	if !c.StdinTTY {
		return apperr.New(apperr.ConfirmationRequired, "tkt drop throws work away, so it needs --yes when stdin is not a terminal").
			WithHint("tkt drop %s --dry-run lists what goes; tkt drop %s --yes drops it", id, id)
	}
	c.UI.Info("tkt drop %s removes:", id)
	for _, l := range res.Lines {
		c.UI.Raw("  - %s", l)
	}
	fmt.Fprintf(c.UI.Err, "tkt: type %s to drop it: ", id)
	var answer string
	fmt.Fscanln(c.Stdin, &answer)
	if !strings.EqualFold(strings.TrimSpace(answer), id) {
		return apperr.New(apperr.Cancelled, "stopped: nothing was dropped")
	}
	return nil
}
