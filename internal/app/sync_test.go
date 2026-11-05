package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/lifecycle"
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

// syncRepo makes a repository whose ticket branch ACM-12 has its own commit,
// pushed as refs/remotes/origin/ACM-12, behind a main that moved on, as
// refs/remotes/origin/main. It returns a Ctx for it and its git.
func syncRepo(t *testing.T) (*Ctx, string, func(...string) string) {
	t.Helper()
	dir, _, _ := mergedRepo(t)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// Start again from the ticket's own commit, with a file on each side.
	git("reset", "-q", "--hard", "HEAD^1")
	os.WriteFile(filepath.Join(dir, "app.txt"), []byte("ticket\n"), 0o644)
	git("add", "app.txt")
	git("commit", "-q", "-m", "ACM-12 app")
	git("update-ref", "refs/remotes/origin/ACM-12", "HEAD")
	git("switch", "-q", "main")
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644)
	git("add", "base.txt")
	git("commit", "-q", "-m", "base moved")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-q", "ACM-12")
	c := &Ctx{App: &App{UI: &output.UI{Err: io.Discard}}, Ctx: context.Background(), Git: gitx.Git{R: runner.Exec{}}}
	c.LC.Forge = lifecycle.Forge{Remote: "origin", Base: "main", Repo: "acme/app"}
	return c, dir, git
}

// tkt sync drops a merge an earlier sync left unpushed only when it can make
// the same merge again: a clean merge of the base onto the branch's head on
// the remote, which nobody changed.
func TestStrandedMerge(t *testing.T) {
	c, dir, git := syncRepo(t)
	remote := git("rev-parse", "refs/remotes/origin/ACM-12")
	git("merge", "-q", "--no-ff", "--no-edit", "refs/remotes/origin/main")
	if !c.strandedMerge(dir, git("rev-parse", "HEAD"), remote) {
		t.Errorf("tkt's own clean merge is not stranded")
	}
	// An older base commit counts too: the base moved again since.
	git("switch", "-q", "main")
	git("commit", "-q", "--allow-empty", "-m", "base moved again")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-q", "ACM-12")
	if !c.strandedMerge(dir, git("rev-parse", "HEAD"), remote) {
		t.Errorf("a merge of an older base commit is not stranded")
	}

	// A change in the merge commit stays.
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("changed in the merge\n"), 0o644)
	git("commit", "-q", "-a", "--amend", "--no-edit")
	if c.strandedMerge(dir, git("rev-parse", "HEAD"), remote) {
		t.Errorf("a merge with a change of its own counts as stranded")
	}

	// So does a commit on top of the merge.
	git("reset", "-q", "--hard", remote)
	git("merge", "-q", "--no-ff", "--no-edit", "refs/remotes/origin/main")
	git("commit", "-q", "--allow-empty", "-m", "the agent's commit")
	if c.strandedMerge(dir, git("rev-parse", "HEAD"), remote) {
		t.Errorf("a commit on top of the merge counts as stranded")
	}

	// And a merge of another branch than the base.
	git("reset", "-q", "--hard", remote)
	git("switch", "-q", "-c", "side", "main~2")
	os.WriteFile(filepath.Join(dir, "side.txt"), []byte("side\n"), 0o644)
	git("add", "side.txt")
	git("commit", "-q", "-m", "side work")
	git("switch", "-q", "ACM-12")
	git("merge", "-q", "--no-ff", "--no-edit", "side")
	if c.strandedMerge(dir, git("rev-parse", "HEAD"), remote) {
		t.Errorf("a merge of another branch counts as stranded")
	}

	// And a merge onto anything but the branch's head on the remote.
	git("reset", "-q", "--hard", remote)
	git("commit", "-q", "--allow-empty", "-m", "unpushed work")
	git("merge", "-q", "--no-ff", "--no-edit", "refs/remotes/origin/main")
	if c.strandedMerge(dir, git("rev-parse", "HEAD"), remote) {
		t.Errorf("a merge onto unpushed work counts as stranded")
	}
}

// A conflict someone resolved by hand is never dropped.
func TestStrandedMergeKeepsAResolvedConflict(t *testing.T) {
	c, dir, git := syncRepo(t)
	git("switch", "-q", "main")
	os.WriteFile(filepath.Join(dir, "app.txt"), []byte("base\n"), 0o644)
	git("add", "app.txt")
	git("commit", "-q", "-m", "base edits app.txt")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-q", "ACM-12")
	remote := git("rev-parse", "HEAD")
	if out, err := exec.Command("git", "-C", dir, "merge", "-q", "--no-ff", "--no-edit", "refs/remotes/origin/main").CombinedOutput(); err == nil {
		t.Fatalf("the merge did not conflict: %s", out)
	}
	os.WriteFile(filepath.Join(dir, "app.txt"), []byte("both\n"), 0o644)
	git("add", "app.txt")
	git("commit", "-q", "--no-edit")
	if c.strandedMerge(dir, git("rev-parse", "HEAD"), remote) {
		t.Errorf("a resolved conflict counts as stranded")
	}
}

func TestMemFree(t *testing.T) {
	avail, total, ok := memFree("MemTotal:       16384000 kB\nMemFree:         1000 kB\nMemAvailable:    4096000 kB\nBuffers: 1 kB\n")
	if !ok || avail != 4096000<<10 || total != 16384000<<10 {
		t.Errorf("memFree = %d, %d, %v", avail, total, ok)
	}
	if _, _, ok := memFree("MemTotal: 1 kB\n"); ok {
		t.Errorf("a file without MemAvailable reads as known")
	}
}

// slotCtx is a Ctx with [sync] pushes slots in a state directory of its own
// and a meminfo file that says free of the total GB is free.
func slotCtx(t *testing.T, state, repo string, pushes int, free, total uint64) (*Ctx, string) {
	t.Helper()
	mem := filepath.Join(t.TempDir(), "meminfo")
	writeMem(t, mem, free, total)
	env := map[string]string{"XDG_STATE_HOME": state, "TKT_MEMINFO": mem}
	c := &Ctx{App: &App{UI: &output.UI{Err: io.Discard}, Getenv: func(k string) string { return env[k] }}, Ctx: context.Background()}
	c.LC.Forge.Repo = repo
	c.Cfg.Sync = config.Sync{Pushes: pushes, MinFree: config.Mem{Percent: 20}}
	return c, mem
}

func writeMem(t *testing.T, path string, free, total uint64) {
	t.Helper()
	text := fmt.Sprintf("MemTotal: %d kB\nMemAvailable: %d kB\n", total<<20, free<<20)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Only [sync] pushes syncs on the machine merge and push at once, whatever
// their repository, and each once [sync] min_free of memory is free. tkt
// watch and tkt auto get SyncPushBusy at once; a person's tkt sync waits.
func TestPushSlots(t *testing.T) {
	state := t.TempDir()
	c, mem := slotCtx(t, state, "acme/app", 2, 8, 16)
	r1, err := c.takePush("ACM-1", false)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := c.takePush("ACM-2", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.takePush("ACM-3", false)
	e := apperr.As(err)
	if e.Code != apperr.SyncPushBusy {
		t.Fatalf("a third push: %v", err)
	}
	if w, _ := e.Details["push_wait"].(*PushWait); w == nil || w.On != PushOnSlots || !slices.Equal(w.Pushes, []string{"ACM-1", "ACM-2"}) {
		t.Errorf("push_wait: %#v", e.Details["push_wait"])
	}
	if !strings.Contains(e.Msg, "ACM-3 waits for a push slot: ACM-1 and ACM-2 push") {
		t.Errorf("message: %s", e.Msg)
	}
	if w := c.pushWait("ACM-3"); w == nil || len(w.Pushes) != 2 {
		t.Errorf("tkt ls for ACM-3: %#v", w)
	}
	if w := c.pushWait("ACM-1"); w != nil {
		t.Errorf("ACM-1 pushes itself, but waits for %#v", w)
	}
	// Another repository's sync shares the machine's slots.
	other, _ := slotCtx(t, state, "acme/web", 2, 8, 16)
	if w := other.pushWait("ACM-9"); w == nil || !slices.Equal(w.Pushes, []string{"acme/app ACM-1", "acme/app ACM-2"}) {
		t.Errorf("another repository's wait: %#v", w)
	}

	// A sync a person runs waits for its turn, and takes it.
	pushPoll = 10 * time.Millisecond
	t.Cleanup(func() { pushPoll = time.Second })
	got := make(chan error)
	go func() {
		r, err := c.takePush("ACM-3", true)
		if err == nil {
			defer r()
		}
		got <- err
	}()
	time.Sleep(50 * time.Millisecond)
	r1()
	if err := <-got; err != nil {
		t.Fatalf("the waiting sync: %v", err)
	}
	r2()

	// With too little memory free, a push waits for it.
	writeMem(t, mem, 2, 16)
	_, err = c.takePush("ACM-4", false)
	if w, _ := apperr.As(err).Details["push_wait"].(*PushWait); w == nil || w.On != PushOnMemory || w.Need != 16<<30/100*20 {
		t.Fatalf("low memory: %v, %#v", err, apperr.As(err).Details["push_wait"])
	}
	if w := c.pushWait("ACM-4"); w == nil || w.On != PushOnMemory {
		t.Errorf("tkt ls on low memory: %#v", w)
	}
	// A person's sync gives up after pushWaitFor.
	pushWaitFor = 30 * time.Millisecond
	t.Cleanup(func() { pushWaitFor = 30 * time.Minute })
	if _, err := c.takePush("ACM-4", true); apperr.As(err).Code != apperr.SyncPushBusy {
		t.Errorf("a wait that ran out: %v", err)
	}
	writeMem(t, mem, 8, 16)
	r, err := c.takePush("ACM-4", false)
	if err != nil {
		t.Fatalf("with memory free again: %v", err)
	}
	r()
}
