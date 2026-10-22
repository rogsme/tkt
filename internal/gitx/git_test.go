package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"gitlab.com/rogs/tkt/internal/runner"
)

// repo makes a repository whose main and side branches both change f from
// the same start, and whose clean branch changes another file.
func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "Acme", "GIT_AUTHOR_EMAIL": "dev@example.com", "GIT_COMMITTER_NAME": "Acme", "GIT_COMMITTER_EMAIL": "dev@example.com"} {
		t.Setenv(k, v)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("f", "one\n")
	git("add", "f")
	git("commit", "-q", "-m", "start")
	git("branch", "side")
	git("branch", "clean")
	write("f", "main\n")
	git("commit", "-q", "-am", "main changes f")
	git("switch", "-q", "side")
	write("f", "side\n")
	git("commit", "-q", "-am", "side changes f")
	git("switch", "-q", "clean")
	write("g", "new\n")
	git("add", "g")
	git("commit", "-q", "-m", "clean adds g")
	return dir
}

func TestMergeTree(t *testing.T) {
	dir := repo(t)
	g := Git{R: runner.Exec{}}
	ctx := context.Background()
	m, err := g.MergeTree(ctx, dir, "clean", "main")
	if err != nil || !m.Clean() || !oidRE.MatchString(m.Tree) {
		t.Fatalf("clean merge: %+v, %v", m, err)
	}
	m, err = g.MergeTree(ctx, dir, "side", "main")
	if err != nil || m.Clean() || !slices.Equal(m.Conflicts, []string{"f"}) {
		t.Fatalf("conflicting merge: %+v, %v", m, err)
	}
	// A bad revision also exits 1, which must not read as a conflict.
	if m, err := g.MergeTree(ctx, dir, "side", "no-such-branch"); err == nil {
		t.Fatalf("a bad revision gave %+v", m)
	}
	// The probe touches no branch and leaves the worktree as it was.
	if g.Rev(ctx, dir, "side") == g.Rev(ctx, dir, "main") || !g.Clean(ctx, dir) || g.MergeInProgress(ctx, dir) {
		t.Error("git merge-tree changed the repository")
	}
}
