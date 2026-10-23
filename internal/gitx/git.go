// Package gitx runs the git queries tkt needs.
package gitx

import (
	"context"
	"os"
	"regexp"
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

// Argv is the git command line that runs args in dir. Commands that change
// a repository build it once, so a dry run reports the same line a real run
// executes with Do.
func Argv(dir string, args ...string) []string {
	return append([]string{"git", "-C", dir}, args...)
}

// Do runs a command line that Argv built.
func (g Git) Do(ctx context.Context, timeout time.Duration, argv []string) runner.Result {
	return g.R.Run(ctx, runner.Cmd{Argv: argv, Timeout: timeout})
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
		return Repo{}, apperr.New(apperr.NotARepository, "%s is not inside a git repository", dir).
			WithHint("run tkt inside the project, or pass -C DIR")
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

// Unchanged reports whether the worktree has no change to a tracked file.
// Untracked files do not count: a merge leaves them alone, and refuses to
// overwrite one.
func (g Git) Unchanged(ctx context.Context, path string) bool {
	s, ok := g.out(ctx, "-C", path, "status", "--porcelain=v1", "--untracked-files=no")
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

// Rev returns the object a revision names, such as a commit for
// refs/remotes/origin/main or a tree for HEAD^{tree}, or "" when it names
// none.
func (g Git) Rev(ctx context.Context, dir, rev string) string {
	s, ok := g.out(ctx, "-C", dir, "rev-parse", "--quiet", "--verify", rev)
	if !ok {
		return ""
	}
	return s
}

// MergeInProgress reports whether the worktree is in the middle of a merge.
func (g Git) MergeInProgress(ctx context.Context, dir string) bool {
	return g.Rev(ctx, dir, "MERGE_HEAD") != ""
}

// Merge is what merging theirs into ours would give: the tree, and the
// files that conflict. A merge with no conflicting file is clean.
type Merge struct {
	Tree      string
	Conflicts []string
}

// Clean reports whether the merge has no conflict.
func (m Merge) Clean() bool { return len(m.Conflicts) == 0 }

var oidRE = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// MergeTree merges theirs into ours in memory, as git merge would, without
// touching the worktree, the index or any ref (git 2.38 or newer).
func (g Git) MergeTree(ctx context.Context, dir, ours, theirs string) (Merge, error) {
	r := g.run(ctx, time.Minute, "-C", dir, "merge-tree", "--write-tree", "--name-only", "--no-messages", ours, theirs)
	lines := strings.Split(strings.TrimRight(r.Out(), "\n"), "\n")
	// Exit 1 means a conflict, but also a bad revision; only a conflict
	// prints a tree first.
	if (r.Code != 0 && r.Code != 1) || !oidRE.MatchString(lines[0]) {
		return Merge{}, apperr.New(apperr.GitFailed, "git merge-tree %s %s failed in %s: %s", ours, theirs, dir, runner.Head(strings.TrimSpace(r.ErrText()), 200))
	}
	m := Merge{Tree: lines[0], Conflicts: []string{}}
	if r.Code == 1 {
		for _, l := range lines[1:] {
			if l != "" {
				m.Conflicts = append(m.Conflicts, l)
			}
		}
		if len(m.Conflicts) == 0 {
			m.Conflicts = []string{"?"}
		}
	}
	return m, nil
}

// maxCleanMerges bounds the commits CleanMerges reads: a branch synced that
// often since its steps ran is better verified again.
const maxCleanMerges = 20

// CleanMerges reports whether head is from, or reached from it by clean
// merges of the base alone: every commit on the branch after from is a
// merge commit whose second parent is an ancestor of base, and whose tree
// is the one git merge-tree computes for its two parents. The branch is
// the first-parent chain from head back to from; the base's own commits
// come in through second parents. tkt sync makes such merges, and human
// verification that passed on from holds at head across them
// (finish-ticket, rogs-skills 421cfc7 and 05d27d5). An error says git
// could not tell, which is not the same as a commit of another kind.
func (g Git) CleanMerges(ctx context.Context, dir, from, head, base string) (bool, error) {
	if from == head {
		return true, nil
	}
	fail := func(what string, r runner.Result) (bool, error) {
		return false, apperr.New(apperr.GitFailed, "git %s failed in %s: %s", what, dir, runner.Head(strings.TrimSpace(r.ErrText()), 200))
	}
	r := g.run(ctx, 0, "-C", dir, "rev-parse", "--quiet", "--verify", from+"^{commit}")
	if !r.OK() {
		// A head this repository does not have cannot reach this one.
		return false, nil
	}
	from = strings.TrimSpace(r.Out())
	r = g.run(ctx, 0, "-C", dir, "rev-list", "--first-parent", "--parents", "-n", strconv.Itoa(maxCleanMerges+1), from+".."+head)
	if !r.OK() {
		return fail("rev-list", r)
	}
	s := strings.TrimRight(r.Out(), "\n")
	lines := strings.Split(s, "\n")
	if s == "" || len(lines) > maxCleanMerges {
		return false, nil
	}
	// Newest first: each merge's first parent is the next line's commit,
	// and the oldest one's is from.
	for i, l := range lines {
		c := strings.Fields(l)
		next := from
		if i+1 < len(lines) {
			next, _, _ = strings.Cut(lines[i+1], " ")
		}
		if len(c) != 3 || c[1] != next {
			return false, nil
		}
		// merge-base --is-ancestor exits 1 for no, and more on an error.
		switch r := g.run(ctx, 0, "-C", dir, "merge-base", "--is-ancestor", c[2], base); r.Code {
		case 0:
		case 1:
			return false, nil
		default:
			return fail("merge-base", r)
		}
		m, err := g.MergeTree(ctx, dir, c[1], c[2])
		if err != nil {
			return false, err
		}
		tree := g.run(ctx, 0, "-C", dir, "rev-parse", "--verify", c[0]+"^{tree}")
		if !tree.OK() {
			return fail("rev-parse", tree)
		}
		if !m.Clean() || strings.TrimSpace(tree.Out()) != m.Tree {
			return false, nil
		}
	}
	return true, nil
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
