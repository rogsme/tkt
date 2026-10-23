package app

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/runner"
)

// mergedRepo makes a repository whose ticket branch has a merge of main on
// top of its own commit, as tkt sync makes it, and returns the directory,
// the commit before the merge, and the merge.
func mergedRepo(t *testing.T) (dir, head, merge string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir = t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "Acme", "GIT_AUTHOR_EMAIL": "dev@example.com", "GIT_COMMITTER_NAME": "Acme", "GIT_COMMITTER_EMAIL": "dev@example.com"} {
		t.Setenv(k, v)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "start")
	git("switch", "-q", "-c", "ACM-12")
	git("commit", "-q", "--allow-empty", "-m", "ACM-12 work")
	head = git("rev-parse", "HEAD")
	git("switch", "-q", "main")
	git("commit", "-q", "--allow-empty", "-m", "another ticket")
	git("switch", "-q", "ACM-12")
	git("merge", "-q", "--no-ff", "--no-edit", "main")
	return dir, head, git("rev-parse", "HEAD")
}

// tkt sync takes its merge back off the branch when the push fails, but
// never a commit someone made on top of it meanwhile.
func TestSyncUndoesOnlyItsOwnMerge(t *testing.T) {
	dir, head, merge := mergedRepo(t)
	c := &Ctx{App: &App{UI: &output.UI{Err: io.Discard}}, Ctx: context.Background(), Git: gitx.Git{R: runner.Exec{}}}
	err := c.undo(dir, merge, head, apperr.New(apperr.GitFailed, "git push to origin failed"))
	if !strings.HasSuffix(err.Error(), "; tkt undid its merge") || c.Git.Head(c.Ctx, dir) != head {
		t.Fatalf("undo with the merge on top: %v, HEAD %s", err, c.Git.Head(c.Ctx, dir))
	}

	dir, head, merge = mergedRepo(t)
	if out, err := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", "the agent's commit").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	later := c.Git.Head(c.Ctx, dir)
	err = c.undo(dir, merge, head, apperr.New(apperr.GitFailed, "git push to origin failed"))
	if !strings.Contains(err.Error(), "stays in "+dir) || c.Git.Head(c.Ctx, dir) != later {
		t.Fatalf("undo under a later commit: %v, HEAD %s, want %s", err, c.Git.Head(c.Ctx, dir), later)
	}
	if e := apperr.As(err); !strings.Contains(e.Hint, "reset --keep "+head[:7]) {
		t.Errorf("hint: %q", e.Hint)
	}
	// An interrupted sync still undoes its merge.
	dir, head, merge = mergedRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Ctx = ctx
	if err := c.undo(dir, merge, head, apperr.New(apperr.GitFailed, "interrupted")); !strings.HasSuffix(err.Error(), "tkt undid its merge") {
		t.Errorf("undo after Ctrl-C: %v", err)
	}
}
