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

// clean is CleanMerges for a test, which fails on its error.
func clean(t *testing.T, g Git, ctx context.Context, dir, from, head, base string) bool {
	t.Helper()
	ok, err := g.CleanMerges(ctx, dir, from, head, base)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestCleanMerges(t *testing.T) {
	dir := repo(t)
	g := Git{R: runner.Exec{}}
	ctx := context.Background()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// clean takes main twice with clean merges; side's merge of main
	// conflicts, and a resolution changes the tree.
	verified := g.Rev(ctx, dir, "clean")
	git("switch", "-q", "clean")
	git("merge", "-q", "--no-ff", "--no-edit", "main")
	git("switch", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "h"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "h")
	git("commit", "-q", "-m", "main adds h")
	git("switch", "-q", "clean")
	git("merge", "-q", "--no-ff", "--no-edit", "main")
	head := g.Rev(ctx, dir, "HEAD")
	if !clean(t, g, ctx, dir, verified, head, "main") || !clean(t, g, ctx, dir, verified[:7], head, "main") || !clean(t, g, ctx, dir, head, head, "main") {
		t.Error("two clean merges of main do not carry the steps")
	}
	// The second parent must be on the base.
	if clean(t, g, ctx, dir, verified, head, verified) {
		t.Error("a merge of a branch other than the base carried the steps")
	}
	// A commit that is not a merge ends the chain.
	if err := os.WriteFile(filepath.Join(dir, "g"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-am", "a fix")
	if clean(t, g, ctx, dir, verified, g.Rev(ctx, dir, "HEAD"), "main") {
		t.Error("a fix after the merges carried the steps")
	}
	// A merge whose tree differs from git merge-tree's carries a conflict
	// resolution, or more.
	git("reset", "-q", "--hard", head)
	git("commit", "-q", "--amend", "--no-edit", "--allow-empty")
	amended := g.Rev(ctx, dir, "HEAD")
	if !clean(t, g, ctx, dir, verified, amended, "main") {
		t.Error("the same merge, made again, no longer carries the steps")
	}
	if err := os.WriteFile(filepath.Join(dir, "g"), []byte("sneaked in\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-a", "--amend", "--no-edit")
	if clean(t, g, ctx, dir, verified, g.Rev(ctx, dir, "HEAD"), "main") {
		t.Error("a merge that changed a file carried the steps")
	}
	// Steps on a commit the head does not contain carry nowhere.
	if clean(t, g, ctx, dir, g.Rev(ctx, dir, "side"), head, "main") {
		t.Error("steps on another branch carried")
	}
}

func TestCleanMergesTellsAnErrorFromAnUncleanMerge(t *testing.T) {
	dir := repo(t)
	g := Git{R: runner.Exec{}}
	ctx := context.Background()
	// A base that does not exist is git failing, not a commit of another
	// kind, so a caller keeps a merge order it cannot judge.
	if out, err := exec.Command("git", "-C", dir, "switch", "-q", "clean").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "merge", "-q", "--no-ff", "--no-edit", "main").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	ok, err := g.CleanMerges(ctx, dir, g.Rev(ctx, dir, "clean^1"), g.Rev(ctx, dir, "HEAD"), "no-such-base")
	if ok || err == nil {
		t.Errorf("a missing base: %v, %v", ok, err)
	}
}
