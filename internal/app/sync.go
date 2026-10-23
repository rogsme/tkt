package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/runner"
)

// SyncOpts are tkt sync's arguments. NoAgent stops at a conflict instead of
// starting a session to resolve it; As is the preset for that session.
type SyncOpts struct {
	ID      string
	As      string
	NoAgent bool
	Dry     bool
}

// The outcomes of tkt sync.
const (
	SyncUpToDate  = "up_to_date"
	SyncMerged    = "merged"
	SyncResolving = "resolving"
	SyncConflict  = "conflict"
)

// SyncResult is what tkt sync did. Outcome is up_to_date, merged (a clean
// merge tkt pushed itself), resolving (the merge conflicts, and a fresh
// session resolves it, in Run) or conflict (it conflicts, and --no-agent
// kept tkt from starting one). Base is the base commit merged, Head the
// branch's head before, and NewHead the merge tkt pushed.
type SyncResult struct {
	Ticket    string     `json:"ticket"`
	Outcome   string     `json:"outcome"`
	Base      string     `json:"base"`
	Head      string     `json:"head"`
	NewHead   string     `json:"new_head"`
	Conflicts []string   `json:"conflicts"`
	Run       *RunResult `json:"run"`
	DryRun    bool       `json:"dry_run"`
	Actions   []Action   `json:"actions"`
	base      string
}

func (r *SyncResult) Text(w io.Writer) {
	switch r.Outcome {
	case SyncUpToDate:
		fmt.Fprintf(w, "%s  up to date with %s\n", r.Ticket, r.base)
	case SyncMerged:
		fmt.Fprintf(w, "%s  merged %s (%s) and pushed %s\n", r.Ticket, r.base, short(r.Base), or(short(r.NewHead), "the merge"))
	case SyncResolving:
		fmt.Fprintf(w, "%s  merging %s %s; a fresh session resolves it in pane %s\n", r.Ticket, r.base, r.conflictText(), r.Run.Pane)
	default:
		fmt.Fprintf(w, "%s  merging %s %s\n", r.Ticket, r.base, r.conflictText())
	}
}

// conflictText says how the merge failed: the files that conflict, or that
// it stopped, as when a hook refuses the merge commit.
func (r *SyncResult) conflictText() string {
	if len(r.Conflicts) == 0 {
		return "stopped"
	}
	return "conflicts in " + strings.Join(r.Conflicts, ", ")
}

// Sync brings a ticket's branch up to date with its base. It merges the base
// in memory first, with git merge-tree: when that is clean, tkt makes the
// merge commit and pushes it, with no agent, in seconds; when it conflicts,
// a fresh session runs the resolve stage, which only merges and resolves.
// It never rebases and never force-pushes.
func (a *App) Sync(ctx context.Context, o SyncOpts) (*SyncResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	id, err := c.workID(o.ID)
	if err != nil {
		return nil, err
	}
	c.ticket(id)
	if err := c.needHerdr(); err != nil {
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
	res, err := c.syncBranch(id, path)
	if err != nil {
		return nil, err
	}
	res.Actions = c.Plan
	if res.Outcome != SyncConflict {
		return res, nil
	}
	if o.NoAgent {
		return res, apperr.New(apperr.SyncConflict, "merging %s into %s %s", res.base, id, res.conflictText()).
			WithHint("tkt sync %s hands it to a fresh session, or tkt run %s resolve", id, id).
			With("conflicts", res.Conflicts)
	}
	// tkt run takes the ticket's lock itself.
	c.release()
	run, err := a.Run(ctx, RunOpts{ID: id, Stage: "resolve", As: o.As, Dry: o.Dry})
	if err != nil {
		return nil, err
	}
	res.Outcome, res.Run = SyncResolving, run
	res.Actions = append(res.Actions, run.Actions...)
	return res, nil
}

// syncBranch merges the base into a ticket's branch when the merge is
// clean, and pushes it. It returns the outcome conflict, and changes
// nothing, when the merge conflicts. The caller holds the ticket's lock.
func (c *Ctx) syncBranch(id, path string) (*SyncResult, error) {
	f := c.LC.Forge
	res := &SyncResult{Ticket: id, Conflicts: []string{}, DryRun: c.Dry, Actions: []Action{}, base: f.Remote + "/" + f.Base}
	agents, err := c.H.Agents(c.Ctx)
	if err != nil {
		return nil, herdrErr(err)
	}
	for _, ag := range herdr.AgentsInPath(agents, path) {
		if ag.Status == herdr.Working || ag.Status == herdr.Blocked {
			return nil, apperr.New(apperr.AgentBusy, "the agent in pane %s is %s; tkt sync changes the branch only while no agent works on it", ag.PaneID, ag.Status).
				WithHint("tkt wait %s, then tkt sync %s", id, id).With("pane", ag.PaneID).With("status", ag.Status)
		}
	}
	if c.Git.CurrentBranch(c.Ctx, path) != id {
		return nil, apperr.New(apperr.WorktreeBranchMismatch, "%s is not on branch %s", path, id)
	}
	if !c.Git.Unchanged(c.Ctx, path) || c.Git.MergeInProgress(c.Ctx, path) {
		return nil, apperr.New(apperr.WorktreeDirty, "%s has uncommitted changes to tracked files or a merge in progress; tkt sync merges only into a clean worktree", path).
			WithHint("commit or clean up the changes in %s, then tkt sync %s", path, id)
	}
	if c.Dry {
		c.UI.Warn("dry run: not fetching, so the base is as of the last fetch")
	} else if r := c.Git.Fetch(c.Ctx, c.Repo.Root, f.Remote); !r.OK() {
		return nil, apperr.New(apperr.GitFailed, "git fetch %s failed: %s", f.Remote, oneLine(r.ErrText())).
			WithHint("check that %s is reachable, then tkt sync %s", f.Remote, id)
	}
	remote := c.Git.Rev(c.Ctx, path, "refs/remotes/"+f.Remote+"/"+id+"^{commit}")
	if remote == "" {
		return nil, apperr.New(apperr.BranchNotPushed, "%s is not on %s yet; work-ticket merges the base itself before its first push", id, f.Remote).
			WithHint("tkt ls %s", id)
	}
	base := c.Git.Rev(c.Ctx, path, "refs/remotes/"+f.Remote+"/"+f.Base+"^{commit}")
	if base == "" {
		return nil, apperr.New(apperr.GitFailed, "%s has no %s after the fetch", c.Repo.Root, res.base)
	}
	head := c.Git.Head(c.Ctx, path)
	res.Head, res.Base = head, base
	switch {
	case head == remote:
	case c.Git.IsAncestor(c.Ctx, path, head, remote):
		// Someone pushed to the branch since this worktree last moved.
		ff := gitx.Argv(path, "merge", "--ff-only", "--quiet", remote)
		if c.Dry {
			c.Would(ff)
		} else if r := c.Git.Do(c.Ctx, time.Minute, ff); !r.OK() {
			return nil, apperr.New(apperr.GitFailed, "git merge --ff-only %s failed in %s: %s", short(remote), path, oneLine(r.ErrText()))
		}
		head, res.Head = remote, remote
	default:
		return nil, apperr.New(apperr.UnpushedCommits, "%s has commits that are not on %s/%s; tkt sync pushes only its own merge", id, f.Remote, id).
			WithHint("push them or let the ticket's agent finish, then tkt sync %s", id)
	}
	if c.Git.IsAncestor(c.Ctx, path, base, head) {
		res.Outcome = SyncUpToDate
		return res, nil
	}
	m, err := c.Git.MergeTree(c.Ctx, path, head, base)
	if err != nil {
		return nil, err
	}
	if !m.Clean() {
		res.Outcome, res.Conflicts = SyncConflict, m.Conflicts
		return res, nil
	}
	msg := fmt.Sprintf("Merge remote-tracking branch '%s' into %s", res.base, id)
	merge := gitx.Argv(path, "merge", "--no-ff", "--no-edit", "-m", msg, base)
	if c.Dry {
		c.Would(merge)
		c.Would(gitx.Argv(path, "push", "--quiet", f.Remote, "<merge>:refs/heads/"+id))
		res.Outcome = SyncMerged
		return res, nil
	}
	if r := c.Git.Do(c.Ctx, 5*time.Minute, merge); !r.OK() {
		// git merge-tree said clean, so something here stopped the merge,
		// such as a hook or an untracked file in its way. The session
		// that resolves conflicts gets it.
		c.abortMerge(path)
		c.UI.Warn("git merge stopped in %s: %s", path, oneLine(r.ErrText()+"\n"+r.Out()))
		res.Outcome = SyncConflict
		return res, nil
	}
	next := c.Git.Head(c.Ctx, path)
	// The commit must be tkt's merge: the head and the base as its parents,
	// and the tree git merge-tree computed. Otherwise the branch moved
	// meanwhile, or a hook changed the commit.
	if c.Git.Rev(c.Ctx, path, next+"^1") != head || c.Git.Rev(c.Ctx, path, next+"^2") != base {
		return nil, apperr.New(apperr.GitFailed, "%s moved while tkt sync merged the base, so tkt left it as it is", id).
			WithHint("check git -C %s log, then tkt sync %s", path, id)
	}
	if tree := c.Git.Rev(c.Ctx, path, next+"^{tree}"); tree != m.Tree {
		return nil, c.undo(path, next, head, apperr.New(apperr.GitFailed, "the merge commit's tree %s differs from the %s git merge-tree computed", short(tree), short(m.Tree)))
	}
	// Push the merge tkt checked, not whatever HEAD is by now.
	push := gitx.Argv(path, "push", "--quiet", f.Remote, next+":refs/heads/"+id)
	if r := c.Git.Do(c.Ctx, 10*time.Minute, push); !r.OK() {
		return nil, c.undo(path, next, head, apperr.New(apperr.GitFailed, "git push to %s failed: %s", f.Remote, oneLine(r.ErrText())).
			WithHint("tkt sync %s tries again", id))
	}
	res.Outcome, res.NewHead = SyncMerged, next
	c.record(history.Event{Ticket: id, Event: history.Sync, Action: SyncMerged})
	return res, nil
}

// undo takes tkt's merge back off the branch after a later step failed, and
// says in e whether it could. It moves the branch only while the merge is
// still its head, so a commit someone made on top of it since stays, merge
// and all. It runs even when tkt was interrupted, so Ctrl-C during a push
// leaves no merge behind.
func (c *Ctx) undo(path, merge, head string, e *apperr.Error) error {
	ctx := context.WithoutCancel(c.Ctx)
	if c.Git.Head(ctx, path) == merge {
		if r := c.Git.Do(ctx, time.Minute, gitx.Argv(path, "reset", "--keep", "--quiet", head)); r.OK() {
			e.Msg += "; tkt undid its merge"
			return e
		}
	}
	e.Msg += fmt.Sprintf("; tkt's merge %s stays in %s, unpushed", short(merge), path)
	return e.WithHint("git -C %s reset --keep %s drops it, when nothing came after it", path, short(head))
}

// abortMerge ends a merge that stopped, even when tkt was interrupted.
func (c *Ctx) abortMerge(path string) {
	ctx := context.WithoutCancel(c.Ctx)
	if !c.Git.MergeInProgress(ctx, path) {
		return
	}
	if r := c.Git.Do(ctx, time.Minute, gitx.Argv(path, "merge", "--abort")); !r.OK() {
		c.UI.Warn("cannot abort the merge in %s: %s; run git -C %s merge --abort", path, oneLine(r.ErrText()), path)
	}
}

// oneLine folds a command's output into one line for a message: its lines
// trimmed and joined with "; ", cut at 300 bytes.
func oneLine(s string) string {
	var parts []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	return runner.Head(strings.Join(parts, "; "), 300)
}
