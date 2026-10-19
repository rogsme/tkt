package app

import (
	"errors"
	"path/filepath"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/lock"
)

// resolveOrCreateWorktree returns the ticket's worktree, creating it through
// worktrunk when needed. In a dry run a worktree to create is the
// placeholder "<worktree of ID>". again is the command that reruns this
// start, for the hints.
func (c *Ctx) resolveOrCreateWorktree(id, again string) (path string, created bool, err error) {
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return "", false, err
	}
	remote, base, root := c.LC.Forge.Remote, c.LC.Forge.Base, c.Repo.Root
	create := true
	entry, ok := es.Find(id)
	if ok {
		path = entry.Path
		c.UI.Info("reusing worktree %s", path)
	} else if c.Git.HasRef(c.Ctx, root, "refs/heads/"+id) {
		if !c.Force {
			return "", false, apperr.New(apperr.BranchNoWorktree,
				"branch %s exists without a worktree (%s); it may be a finished ticket. Reuse it with --force, or delete it with: git branch -d %s",
				id, c.Git.LastCommit(c.Ctx, root, id), id).WithHint("%s --force", again)
		}
		create = false
	} else {
		heads, reached := c.Git.LsRemoteHeads(c.Ctx, root, remote, id)
		switch {
		case !reached:
			c.UI.Warn("cannot reach %s to look for an existing %s branch; creating it from %s", remote, id, base)
		case heads != "":
			c.UI.Info("branch %s exists on %s; checking it out", id, remote)
			create = false
		case c.Git.HasRef(c.Ctx, root, "refs/remotes/"+remote+"/"+id):
			return "", false, apperr.New(apperr.StaleRemoteRef,
				"refs/remotes/%s/%s is stale (the branch is gone from %s, so the ticket may be merged); run git fetch --prune %s first",
				remote, id, remote, remote).WithHint("git fetch --prune %s", remote)
		}
	}
	if path == "" {
		if c.WT.HasPostStartHooks(c.Ctx) {
			c.UI.Warn("worktrunk runs post-start hooks in the background, so the agent may start before they finish; move readiness work to pre-start")
		}
		argv := c.WT.SwitchArgv(id, create)
		if c.Dry {
			c.Would(argv)
			return "<worktree of " + id + ">", true, nil
		}
		f, err := lock.Wait(c.Ctx, c.Repo.Common, "wt", 300*time.Second)
		if errors.Is(err, lock.ErrHeld) {
			return "", false, apperr.New(apperr.WorktreeLockTimeout, "timed out waiting for another tkt start to finish creating its worktree")
		}
		if err != nil {
			return "", false, apperr.New(apperr.Internal, "cannot take the worktree lock: %v", err)
		}
		r := c.WT.Visible(c.Ctx, argv)
		f.Close()
		es, lerr := c.WT.List(c.Ctx)
		if lerr != nil {
			return "", false, lerr
		}
		e, found := es.Find(id)
		if !r.OK() {
			if found {
				return "", false, apperr.New(apperr.WtHookFailed,
					"wt created %s but a hook failed (see above). Fix it, run 'wt -C %s hook pre-start', then rerun %s",
					e.Path, e.Path, again).WithHint("wt -C %s hook pre-start", e.Path)
			}
			return "", false, apperr.New(apperr.WtSwitchFailed, "wt switch failed for %s (see above)", id)
		}
		if !found {
			return "", false, apperr.New(apperr.WtFailed, "wt reported success but lists no worktree for %s", id)
		}
		entry, path, created = e, e.Path, true
	}
	if filepath.Base(path) != id {
		return "", false, apperr.New(apperr.WorktreeLayout,
			"the worktree directory is %s, not %s; the lifecycle skills need branch = directory = ticket id (check worktree-path in your worktrunk config)",
			filepath.Base(path), id)
	}
	if c.Git.CurrentBranch(c.Ctx, path) != id || entry.Mismatch {
		return "", false, apperr.New(apperr.WorktreeBranchMismatch, "%s is not on branch %s", path, id)
	}
	return path, created, nil
}
