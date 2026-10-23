package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itchyny/gojq"
	"github.com/rogpeppe/go-internal/testscript"

	"gitlab.com/rogs/tkt/internal/cli"
	"gitlab.com/rogs/tkt/internal/fake"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/pi"
	"gitlab.com/rogs/tkt/internal/state"
)

// realGit is the git on PATH before testscript puts the fakes first.
var realGit, _ = exec.LookPath("git")

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"tkt":      tktMain,
		"herdr":    fake.Herdr,
		"wt":       fake.WT,
		"gh":       fake.GH,
		"opencode": fake.OpenCode,
		"claude":   fake.Claude,
		"git":      fake.Git,
		"pi":       fake.Pi,
		// The service managers and their helpers, for tkt watch --install.
		"systemctl":      fake.Systemctl,
		"systemd-escape": fake.SystemdEscape,
		"loginctl":       fake.Loginctl,
		"launchctl":      fake.Launchctl,
	})
}

// tktMain runs tkt and records its exit status for the rc command.
func tktMain() {
	code := cli.Main(context.Background(), os.Args[1:], "test", cli.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv})
	if f := os.Getenv("TKT_RC_FILE"); f != "" {
		os.WriteFile(f, []byte(strconv.Itoa(code)), 0o644)
	}
	os.Exit(code)
}

// TestScripts runs the black-box tests in testdata/script. Each script gets
// a repository with a bare origin, the Acme lifecycle configuration and a
// herdr session whose only pane sits in that repository; see
// testdata/README.md.
func TestScripts(t *testing.T) {
	if realGit == "" {
		t.Skip("git is not installed")
	}
	src, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/script",
		RequireExplicitExec: true,
		Setup: func(env *testscript.Env) error {
			env.Setenv("SRC", src)
			return setupSandbox(env, realGit)
		},
		Cmds: commands,
		// [bundled_pi] holds when the test binary carries pi, as a build
		// with the bundled_pi tag does.
		Condition: func(cond string) (bool, error) {
			if cond == "bundled_pi" {
				_, ok := pi.Bundled()
				return ok, nil
			}
			return false, fmt.Errorf("unknown condition %q", cond)
		},
	})
}

const trackerConfig = "<!-- rogs-skills config v1: issue-tracker -->\n# Issue tracker\n\n## Tracker\n\n- Kind: Example tracker\n- Ticket ids: `ACM-<number>`, matched by `^ACM-[1-9][0-9]*$`.\n"

const forgeConfig = "<!-- rogs-skills config v1: forge -->\n# Forge\n\n## Forge\n\n- Kind: GitHub\n- Host: github.com\n- Repository: acme/app\n- Remote: origin\n- Base branch: main; re-read with: `gh repo view`\n\n## Operations\n"

func setupSandbox(env *testscript.Env, realGit string) error {
	work := env.WorkDir
	home := filepath.Join(work, "home")
	repo := filepath.Join(work, "app")
	vars := map[string]string{
		"HOME":                home,
		"XDG_CONFIG_HOME":     filepath.Join(home, ".config"),
		"XDG_STATE_HOME":      filepath.Join(home, ".local", "state"),
		"FAKE_DIR":            filepath.Join(work, "fake"),
		"FAKE_LOG":            filepath.Join(work, "fake", "log"),
		"FAKE_WT_ROOT":        filepath.Join(work, "wt"),
		"REAL_GIT":            realGit,
		"REPO":                repo,
		"TKT_RC_FILE":         filepath.Join(work, "fake", "rc"),
		"GIT_AUTHOR_NAME":     "Acme",
		"GIT_AUTHOR_EMAIL":    "dev@example.com",
		"GIT_COMMITTER_NAME":  "Acme",
		"GIT_COMMITTER_EMAIL": "dev@example.com",
		"GIT_CONFIG_GLOBAL":   filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_NOSYSTEM": "1",
		"HERDR_ENV":           "1",
		"HERDR_WORKSPACE_ID":  "w1",
		"HERDR_TAB_ID":        "w1:t1",
		"HERDR_PANE_ID":       "w1:p1",
		"NO_COLOR":            "1",
		// Every command in a herdr pane would start a tkt watch that
		// outlives its script; the watch_ scripts turn it back on.
		"TKT_WATCH": "off",
		// Under -race every fake is a race-built copy of this test binary,
		// which by default sleeps a second as it exits.
		"GORACE": "atexit_sleep_ms=0",
	}
	for k, v := range vars {
		env.Setenv(k, v)
	}
	for _, d := range []string{home, vars["FAKE_DIR"], vars["FAKE_WT_ROOT"], filepath.Join(repo, "docs/agents")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	os.WriteFile(vars["FAKE_LOG"], nil, 0o644)
	os.WriteFile(filepath.Join(repo, "docs/agents/issue-tracker.md"), []byte(trackerConfig), 0o644)
	os.WriteFile(filepath.Join(repo, "docs/agents/forge.md"), []byte(forgeConfig), 0o644)
	gitEnv := os.Environ()
	for k, v := range vars {
		gitEnv = append(gitEnv, k+"="+v)
	}
	run := func(args ...string) error {
		cmd := exec.Command(realGit, args...)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return nil
	}
	origin := filepath.Join(work, "origin.git")
	for _, args := range [][]string{
		{"init", "-q", "--bare", "-b", "main", origin},
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "add", "-A"},
		{"-C", repo, "commit", "-q", "-m", "init"},
		{"-C", repo, "remote", "add", "origin", origin},
		{"-C", repo, "push", "-q", "-u", "origin", "main"},
	} {
		if err := run(args...); err != nil {
			return err
		}
	}
	herdr := map[string]any{
		"next":       10,
		"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "acme"}},
		"tabs":       []any{map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "main"}},
		"panes":      []any{map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": repo, "foreground_cwd": repo}},
		"agents":     []any{},
	}
	b, _ := json.Marshal(herdr)
	if err := os.WriteFile(filepath.Join(vars["FAKE_DIR"], "herdr.json"), b, 0o644); err != nil {
		return err
	}
	env.Cd = repo
	return nil
}

var commands = map[string]func(ts *testscript.TestScript, neg bool, args []string){
	"rc":          cmdRC,
	"logged":      cmdLogged,
	"resetlog":    cmdResetLog,
	"houserules":  cmdHouseRules,
	"jqedit":      cmdJQEdit,
	"jqeq":        cmdJQEq,
	"state":       cmdState,
	"statevar":    cmdStateVar,
	"waitstate":   cmdWaitState,
	"waitfile":    cmdWaitFile,
	"mkprompt":    cmdMkPrompt,
	"realgit":     cmdRealGit,
	"touchat":     cmdTouchAt,
	"envelope":    cmdEnvelope,
	"logcontains": cmdLogContains,
	"claudedir":   cmdClaudeDir,
	"gitvar":      cmdGitVar,
	"mkrelease":   cmdMkRelease,
	"unitvar":     cmdUnitVar,
}

// unitvar VAR PATH: sets VAR to PATH as systemd-escape --path names it.
func cmdUnitVar(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: unitvar VAR PATH")
	}
	ts.Setenv(args[0], fake.EscapePath(args[1]))
}

// rc N: the last tkt command exited with status N.
func cmdRC(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 1 {
		ts.Fatalf("usage: rc N")
	}
	got := strings.TrimSpace(ts.ReadFile(ts.Getenv("TKT_RC_FILE")))
	if (got == args[0]) == neg {
		ts.Fatalf("tkt exited %s, want %s%s", got, map[bool]string{true: "not ", false: ""}[neg], args[0])
	}
}

// logged WORDS...: a fake logged a command line containing the words, each
// quoted as the log quotes it.
func cmdLogged(ts *testscript.TestScript, neg bool, args []string) {
	want := fake.LogLine(args...)
	log := ts.ReadFile(ts.Getenv("FAKE_LOG"))
	found := strings.Contains(log, want)
	if found == neg {
		ts.Fatalf("log %s [%s]:\n%s", map[bool]string{true: "has", false: "lacks"}[neg], want, log)
	}
}

// resetlog: forget the commands and prompts so far.
func cmdResetLog(ts *testscript.TestScript, _ bool, _ []string) {
	ts.Check(os.WriteFile(ts.Getenv("FAKE_LOG"), nil, 0o644))
	os.Remove(filepath.Join(ts.Getenv("FAKE_DIR"), "prompts"))
}

// houserules: worktrees changed only through wt with its hooks, and agents
// started only through herdr.
func cmdHouseRules(ts *testscript.TestScript, _ bool, _ []string) {
	sc := bufio.NewScanner(strings.NewReader(ts.ReadFile(ts.Getenv("FAKE_LOG"))))
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "wt ") && (strings.Contains(l, "--no-hooks") || strings.Contains(l, "--clobber") || strings.Contains(l, " --yes")):
			ts.Fatalf("wt ran with a forbidden flag: %s", l)
		case strings.HasPrefix(l, "git ") && containsAny(l, "worktree add", "worktree remove", "worktree prune", "worktree move"):
			ts.Fatalf("tkt changed worktrees with git: %s", l)
		case strings.HasPrefix(l, "herdr worktree"):
			ts.Fatalf("tkt used herdr worktrees: %s", l)
		case strings.HasPrefix(l, "claude") && l != "claude --version":
			ts.Fatalf("tkt ran claude directly: %s", l)
		}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// jqVars are the variables a jq expression can read, as $NAME.
var jqVars = []string{"REPO", "WORK", "WT", "FAKE_WT_ROOT", "FAKE_DIR", "HOME", "SID", "H1", "H2", "H3", "H4", "H5"}

func jqRun(ts *testscript.TestScript, file, expr string) []any {
	q, err := gojq.Parse(expr)
	ts.Check(err)
	names := make([]string, len(jqVars))
	values := make([]any, len(jqVars))
	for i, v := range jqVars {
		names[i], values[i] = "$"+v, ts.Getenv(v)
	}
	code, err := gojq.Compile(q, gojq.WithVariables(names))
	ts.Check(err)
	var input any
	ts.Check(json.Unmarshal([]byte(ts.ReadFile(file)), &input))
	var out []any
	iter := code.Run(input, values...)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			ts.Fatalf("jq %s: %v", expr, err)
		}
		out = append(out, v)
	}
	return out
}

// jqedit FILE EXPR: replace a JSON file with EXPR applied to it.
func cmdJQEdit(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: jqedit FILE EXPR")
	}
	file := ts.MkAbs(args[0])
	out := jqRun(ts, file, args[1])
	if len(out) != 1 {
		ts.Fatalf("jqedit: %d results", len(out))
	}
	b, _ := json.MarshalIndent(out[0], "", "  ")
	ts.Check(os.WriteFile(file, b, 0o644))
}

// jqeq FILE EXPR WANT: EXPR applied to a JSON file gives WANT, compact.
func cmdJQEq(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 3 {
		ts.Fatalf("usage: jqeq FILE EXPR WANT")
	}
	out := jqRun(ts, ts.MkAbs(args[0]), args[1])
	var parts []string
	for _, v := range out {
		if s, ok := v.(string); ok {
			parts = append(parts, s)
			continue
		}
		b, _ := json.Marshal(v)
		parts = append(parts, string(b))
	}
	got := strings.Join(parts, "\n")
	if (got == args[2]) == neg {
		ts.Fatalf("jq %s = %s, want %s%s", args[1], got, map[bool]string{true: "not ", false: ""}[neg], args[2])
	}
}

func readState(ts *testscript.TestScript, dir string) state.State {
	cmd := exec.Command(ts.Getenv("REAL_GIT"), "-C", ts.MkAbs(dir), "rev-parse", "--absolute-git-dir")
	out, err := cmd.Output()
	ts.Check(err)
	return state.Read(state.Path(strings.TrimSpace(string(out))))
}

// state DIR KEY WANT: the ticket state in worktree DIR records KEY=WANT.
func cmdState(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 3 {
		ts.Fatalf("usage: state DIR KEY WANT")
	}
	got := readState(ts, args[0])[args[1]]
	if (got == args[2]) == neg {
		ts.Fatalf("state %s = %q, want %s%q", args[1], got, map[bool]string{true: "not ", false: ""}[neg], args[2])
	}
}

// waitstate DIR KEY WANT: wait up to 30 seconds for the ticket state to
// record KEY=WANT, for a tkt auto loop running in the background.
func cmdWaitState(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 3 || neg {
		ts.Fatalf("usage: waitstate DIR KEY WANT")
	}
	got := ""
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if got = readState(ts, args[0])[args[1]]; got == args[2] {
			return
		}
	}
	ts.Fatalf("state %s = %q after 30s, want %q", args[1], got, args[2])
}

// waitfile FILE: wait up to 30 seconds for FILE to exist, for a tkt watch
// started in the background.
func cmdWaitFile(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 1 || neg {
		ts.Fatalf("usage: waitfile FILE")
	}
	f := ts.MkAbs(args[0])
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(f); err == nil {
			return
		}
	}
	ts.Fatalf("%s did not appear in 30s", f)
}

// statevar DIR KEY VAR: set VAR to the state's KEY.
func cmdStateVar(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) != 3 {
		ts.Fatalf("usage: statevar DIR KEY VAR")
	}
	ts.Setenv(args[2], readState(ts, args[0])[args[1]])
}

// mkprompt FILE ID [LINE...]: write a start-ticket prompt for ID.
func cmdMkPrompt(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) < 2 {
		ts.Fatalf("usage: mkprompt FILE ID [LINE...]")
	}
	extra := strings.Join(args[2:], "\n")
	text := fmt.Sprintf("Load the start-ticket skill and follow it.\n\nWork in the %s worktree.\n%s\nStatus: AWAITING_APPROVAL\n", args[1], extra)
	ts.Check(os.WriteFile(ts.MkAbs(args[0]), []byte(text), 0o644))
}

// realgit ARGS...: run the real git, unlogged, for test setup.
func cmdRealGit(ts *testscript.TestScript, neg bool, args []string) {
	cmd := exec.Command(ts.Getenv("REAL_GIT"), args...)
	cmd.Dir = ts.MkAbs(".")
	env := []string{}
	for _, k := range []string{"HOME", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "PATH"} {
		env = append(env, k+"="+ts.Getenv(k))
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if (err != nil) != neg {
		ts.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	fmt.Fprint(ts.Stdout(), string(out))
}

// touchat FILE DATE: create FILE with the modification time DATE
// (2006-01-02).
func cmdTouchAt(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: touchat FILE DATE")
	}
	t, err := time.Parse("2006-01-02", args[1])
	ts.Check(err)
	f := ts.MkAbs(args[0])
	ts.Check(os.MkdirAll(filepath.Dir(f), 0o755))
	if _, err := os.Stat(f); err != nil {
		ts.Check(os.WriteFile(f, nil, 0o644))
	}
	ts.Check(os.Chtimes(f, t, t))
}

// envelope [EXPR WANT]: stdout is exactly one JSON document with schema, ok
// and command, and EXPR applied to it gives WANT.
func cmdEnvelope(ts *testscript.TestScript, neg bool, args []string) {
	out := ts.ReadFile("stdout")
	dec := json.NewDecoder(strings.NewReader(out))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		ts.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if dec.More() {
		ts.Fatalf("stdout holds more than one JSON document:\n%s", out)
	}
	for _, k := range []string{"schema", "ok", "command"} {
		if _, ok := doc[k]; !ok {
			ts.Fatalf("the envelope lacks %q:\n%s", k, out)
		}
	}
	if len(args) == 0 {
		return
	}
	if len(args) != 2 {
		ts.Fatalf("usage: envelope [EXPR WANT]")
	}
	f := filepath.Join(ts.Getenv("WORK"), ".envelope.json")
	ts.Check(os.WriteFile(f, []byte(out), 0o644))
	cmdJQEq(ts, neg, []string{f, args[0], args[1]})
}

// logcontains TEXT: the log holds TEXT as it is, quoting included.
func cmdLogContains(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 1 {
		ts.Fatalf("usage: logcontains TEXT")
	}
	log := ts.ReadFile(ts.Getenv("FAKE_LOG"))
	if strings.Contains(log, args[0]) == neg {
		ts.Fatalf("log %s [%s]:\n%s", map[bool]string{true: "has", false: "lacks"}[neg], args[0], log)
	}
}

// claudedir VAR PATH: set VAR to Claude Code's project directory for PATH.
func cmdClaudeDir(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: claudedir VAR PATH")
	}
	ts.Setenv(args[0], harness.ClaudeProjectDir(ts.Getenv, args[1]))
}

// gitvar VAR DIR REV: set VAR to the commit REV names in DIR.
func cmdGitVar(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) != 3 {
		ts.Fatalf("usage: gitvar VAR DIR REV")
	}
	out, err := exec.Command(ts.Getenv("REAL_GIT"), "-C", ts.MkAbs(args[1]), "rev-parse", args[2]).Output()
	ts.Check(err)
	ts.Setenv(args[0], strings.TrimSpace(string(out)))
}

// mkrelease TAG: publish a fake release TAG under $WORK/rel, as install.sh
// and tkt update read it: a binary for each system that logs its call and
// prints "tkt version TAG", and SHA256SUMS.
func cmdMkRelease(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) != 1 {
		ts.Fatalf("usage: mkrelease TAG")
	}
	dir := filepath.Join(ts.Getenv("WORK"), "rel", args[0])
	ts.Check(os.MkdirAll(dir, 0o755))
	bin := fmt.Sprintf("#!/bin/sh\necho \"new-tkt $*\" >> \"$FAKE_LOG\"\necho \"tkt version %s\"\n", args[0])
	sum := sha256.Sum256([]byte(bin))
	var sums strings.Builder
	for _, a := range []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"} {
		ts.Check(os.WriteFile(filepath.Join(dir, "tkt_"+a), []byte(bin), 0o644))
		fmt.Fprintf(&sums, "%s  tkt_%s\n", hex.EncodeToString(sum[:]), a)
	}
	ts.Check(os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644))
}
