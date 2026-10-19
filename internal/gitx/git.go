// Package gitx runs the git queries tkt needs.
package gitx

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/runner"
)

// Git runs git through a Runner.
type Git struct {
	R runner.Runner
}

func (g Git) run(ctx context.Context, timeout time.Duration, args ...string) runner.Result {
	return g.R.Run(ctx, runner.Cmd{Argv: append([]string{"git"}, args...), Timeout: timeout})
}

func (g Git) out(ctx context.Context, args ...string) (string, bool) {
	r := g.run(ctx, 0, args...)
	return strings.TrimRight(r.Out(), "\n"), r.OK()
}

// Repo locates a repository: Root is the primary worktree and Common the
// shared git directory.
type Repo struct {
	Root   string
	Common string
}

// Resolve finds the repository that dir belongs to.
func (g Git) Resolve(ctx context.Context, dir string) (Repo, error) {
	if _, ok := g.out(ctx, "-C", dir, "rev-parse", "--git-dir"); !ok {
		return Repo{}, apperr.New(apperr.NotARepository, "%s is not inside a git repository", dir)
	}
	list, _ := g.out(ctx, "-C", dir, "worktree", "list", "--porcelain")
	first, _, _ := strings.Cut(list, "\n")
	root := strings.TrimPrefix(first, "worktree ")
	common, _ := g.out(ctx, "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if fi, err := os.Stat(root); root == first || err != nil || !fi.IsDir() {
		return Repo{}, apperr.New(apperr.NotARepository, "cannot find the primary worktree of %s", dir)
	}
	return Repo{Root: root, Common: common}, nil
}

// AbsGitDir returns the worktree's own git directory.
func (g Git) AbsGitDir(ctx context.Context, path string) (string, error) {
	d, ok := g.out(ctx, "-C", path, "rev-parse", "--absolute-git-dir")
	if !ok {
		return "", apperr.New(apperr.GitFailed, "git rev-parse --absolute-git-dir failed in %s", path)
	}
	return d, nil
}

// HasRef reports whether a full ref name exists.
func (g Git) HasRef(ctx context.Context, dir, ref string) bool {
	return g.run(ctx, 0, "-C", dir, "show-ref", "--verify", "--quiet", ref).OK()
}

// LastCommit returns "<short hash> <subject>" for a revision.
func (g Git) LastCommit(ctx context.Context, dir, rev string) string {
	s, _ := g.out(ctx, "-C", dir, "log", "-1", "--format=%h %s", rev)
	return s
}

// LsRemoteHeads lists the remote branch named branch. ok is false when the
// remote could not be reached.
func (g Git) LsRemoteHeads(ctx context.Context, dir, remote, branch string) (string, bool) {
	r := g.run(ctx, 20*time.Second, "-C", dir, "ls-remote", "--heads", remote, branch)
	return strings.TrimSpace(r.Out()), r.OK()
}

// Fetch runs git fetch --prune for the remote.
func (g Git) Fetch(ctx context.Context, dir, remote string) runner.Result {
	return g.run(ctx, 0, "-C", dir, "fetch", "--prune", "--quiet", remote)
}

// CurrentBranch returns the checked-out branch, or "".
func (g Git) CurrentBranch(ctx context.Context, path string) string {
	s, _ := g.out(ctx, "-C", path, "branch", "--show-current")
	return s
}

// Head returns the worktree's HEAD commit.
func (g Git) Head(ctx context.Context, path string) string {
	s, _ := g.out(ctx, "-C", path, "rev-parse", "HEAD")
	return s
}

// Clean reports whether the worktree has no changes.
func (g Git) Clean(ctx context.Context, path string) bool {
	s, ok := g.out(ctx, "-C", path, "status", "--porcelain=v1")
	return ok && s == ""
}

// Count returns git rev-list --count for a range, or -1.
func (g Git) Count(ctx context.Context, path, rng string) int {
	s, ok := g.out(ctx, "-C", path, "rev-list", "--count", rng)
	n, err := strconv.Atoi(s)
	if !ok || err != nil {
		return -1
	}
	return n
}

// IsAncestor reports whether commit is an ancestor of ref.
func (g Git) IsAncestor(ctx context.Context, dir, commit, ref string) bool {
	return g.run(ctx, 0, "-C", dir, "merge-base", "--is-ancestor", commit, ref).OK()
}

// Worktree is one entry of git worktree list.
type Worktree struct {
	Path   string
	Branch string
}

// Worktrees lists the repository's worktrees. It is fast enough for shell
// completion, unlike wt list.
func (g Git) Worktrees(ctx context.Context, dir string) []Worktree {
	s, _ := g.out(ctx, "-C", dir, "worktree", "list", "--porcelain")
	var out []Worktree
	var cur Worktree
	for _, line := range strings.Split(s+"\n", "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur = Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "branch refs/heads/"):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "" && cur.Path != "":
			out = append(out, cur)
			cur = Worktree{}
		}
	}
	return out
}
