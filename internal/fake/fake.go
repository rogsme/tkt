// Package fake holds the stand-ins for herdr, worktrunk, gh, OpenCode, pi,
// Claude Code and git that tkt's black-box tests put on PATH. Each one
// appends its command line to $FAKE_LOG, so tests can assert what tkt ran.
// Only the tests import it.
package fake

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.com/rogs/tkt/internal/runner"
)

// LogLine renders a command line the way the log holds it: each word
// shell-quoted like bash printf %q.
func LogLine(words ...string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = runner.BashQuote(w)
	}
	return strings.Join(q, " ")
}

func logCall(name string) {
	f, err := os.OpenFile(os.Getenv("FAKE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, LogLine(append([]string{name}, os.Args[1:]...)...))
}

func die(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}

func printJSON(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

// gitPath returns the real git, found before the tests put the fakes on
// PATH. It refuses to return this fake, which would call itself forever.
func gitPath() string {
	real := os.Getenv("REAL_GIT")
	self, _ := os.Executable()
	a, _ := filepath.EvalSymlinks(real)
	b, _ := filepath.EvalSymlinks(self)
	if real == "" || a == b {
		die(127, "fake: REAL_GIT must name the real git, not %q", real)
	}
	return real
}

// realGit runs the real git.
func realGit(args ...string) ([]byte, error) {
	cmd := exec.Command(gitPath(), args...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// Git logs the call and runs the real git.
func Git() {
	logCall("git")
	cmd := exec.Command(gitPath(), os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		}
		die(127, "fake git: %v", err)
	}
	os.Exit(0)
}

// Claude fails: tkt launches Claude Code through herdr, never directly.
// Only --version answers, for tkt doctor.
func Claude() {
	logCall("claude")
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("2.1.303 (Claude Code)")
		os.Exit(0)
	}
	os.Exit(99)
}

// GH answers `gh pr list` from the JSON file in $FAKE_GH_PRS.
func GH() {
	logCall("gh")
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("gh version 2.100.0 (2026-09-04)")
		os.Exit(0)
	}
	if os.Getenv("FAKE_GH_FAIL") == "1" {
		die(1, "HTTP 502")
	}
	if len(args) < 2 || args[0] != "pr" || args[1] != "list" {
		die(2, "fake gh: unsupported: %s", strings.Join(args, " "))
	}
	head, state := "", "open"
	for i := 2; i+1 < len(args); i++ {
		switch args[i] {
		case "--head":
			head = args[i+1]
		case "--state":
			state = args[i+1]
		}
	}
	// Turns mode swaps the pull requests in $FAKE_DIR/prs.json as the agent
	// works; otherwise $FAKE_GH_PRS names them.
	b, err := os.ReadFile(filepath.Join(os.Getenv("FAKE_DIR"), "prs.json"))
	if err != nil {
		b, err = os.ReadFile(os.Getenv("FAKE_GH_PRS"))
	}
	if err != nil {
		os.Exit(0)
	}
	var prs []map[string]any
	if json.Unmarshal(b, &prs) != nil {
		os.Exit(0)
	}
	out := []map[string]any{}
	for _, p := range prs {
		s, _ := p["state"].(string)
		if (head == "" || p["headRefName"] == head) && (state == "all" || strings.EqualFold(s, state)) {
			out = append(out, p)
		}
	}
	printJSON(out)
	os.Exit(0)
}

// OpenCode answers the session API calls tkt makes.
func OpenCode() {
	logCall("opencode")
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "service" {
		os.Exit(0)
	}
	if len(args) > 0 && args[0] == "--version" {
		fmt.Println("1.20.0")
		os.Exit(0)
	}
	if len(args) < 3 || args[0] != "api" {
		die(2, "fake opencode: unsupported: %s", strings.Join(args, " "))
	}
	dir := os.Getenv("FAKE_DIR")
	switch {
	case args[1] == "POST" && args[2] == "/api/session":
		n := counter(dir+"/oc.count") + 1
		var body map[string]any
		if len(args) > 4 {
			json.Unmarshal([]byte(args[4]), &body)
		}
		if body == nil {
			body = map[string]any{}
		}
		body["id"] = fmt.Sprintf("ses_fake%d", n)
		printJSON(map[string]any{"data": body})
	case args[1] == "GET" && args[2] == "/api/session/active":
		if a := os.Getenv("FAKE_OC_ACTIVE"); a != "" {
			fmt.Println(a)
		} else {
			fmt.Println(`{"data":{}}`)
		}
	case args[1] == "GET" && strings.HasPrefix(args[2], "/api/session/") && strings.Contains(args[2], "/message"):
		if b, err := os.ReadFile(os.Getenv("FAKE_OC_MESSAGES")); err == nil {
			os.Stdout.Write(b)
		} else {
			fmt.Println(`{"data":[]}`)
		}
	case args[1] == "GET" && strings.HasPrefix(args[2], "/api/session"):
		if b, err := os.ReadFile(os.Getenv("FAKE_OC_SESSIONS")); err == nil {
			os.Stdout.Write(b)
		} else {
			fmt.Println(`{"data":[]}`)
		}
	default:
		die(2, "fake opencode: unsupported: %s %s", args[1], args[2])
	}
	os.Exit(0)
}

// counter increments the number kept in file and returns its old value.
func counter(file string) int {
	n := 0
	if b, err := os.ReadFile(file); err == nil {
		fmt.Sscan(string(b), &n)
	}
	os.WriteFile(file, []byte(fmt.Sprint(n+1)), 0o644)
	return n
}
