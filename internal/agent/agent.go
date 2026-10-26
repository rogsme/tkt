// Package agent starts, prompts and stops the agents in herdr panes.
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/output"
)

// Ops runs the agent flows. In a dry run it only reports, through Would,
// the commands that change something.
type Ops struct {
	H      herdr.Client
	OC     harness.OpenCode
	UI     *output.UI
	Getenv func(string) string
	Dry    bool
	Would  func(argv []string)
	Sleep  func(time.Duration)
}

func (o Ops) sleep(d time.Duration) {
	if o.Sleep != nil {
		o.Sleep(d)
		return
	}
	time.Sleep(d)
}

// PaneFree reports whether a pane sits at its shell with no agent.
func (o Ops) PaneFree(ctx context.Context, pane string) bool {
	if !o.H.PaneIsShell(ctx, pane) {
		return false
	}
	_, has := o.H.AgentOnPane(ctx, pane)
	return !has
}

// WaitPaneFree polls PaneFree up to tries times, a quarter second apart.
func (o Ops) WaitPaneFree(ctx context.Context, pane string, tries int) bool {
	for i := 0; i < tries; i++ {
		if o.PaneFree(ctx, pane) {
			return true
		}
		o.sleep(250 * time.Millisecond)
	}
	return false
}

// Screen returns the last non-blank lines on the agent's screen.
func (o Ops) Screen(ctx context.Context, pane string) []string {
	var lines []string
	for _, l := range strings.Split(o.H.Read(ctx, pane, 25), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	return lines
}

// ShowScreen prints the agent's screen to stderr and returns its lines.
func (o Ops) ShowScreen(ctx context.Context, pane string) []string {
	lines := o.Screen(ctx, pane)
	o.UI.Info("the agent's screen:")
	for _, l := range lines {
		o.UI.Raw("%s", l)
	}
	return lines
}

// LastTurn reads a session's last turn from its own record: the Claude Code
// transcript, or the OpenCode API. ok is false when that record is unknown.
func (o Ops) LastTurn(ctx context.Context, h, sid, path string) (harness.Turn, bool) {
	if sid == "" {
		return harness.Turn{}, false
	}
	if h == config.Claude {
		return harness.ClaudeLastTurn(harness.ClaudeProjectDir(o.Getenv, path) + "/" + sid + ".jsonl")
	}
	return o.OC.LastTurn(ctx, sid)
}

// LastStatus returns the value of the last status line in text, or "".
// PLAN_APPROVED marks a step inside a run, not its end: after it, the run
// has said nothing final yet, whatever status came before.
func LastStatus(text string) string {
	m := statusRE.FindAllStringSubmatch(text, -1)
	if len(m) == 0 || m[len(m)-1][1] == contract.StatusPlanApproved {
		return ""
	}
	return m[len(m)-1][1]
}

// HasStatus says text holds the status line Status: s.
func HasStatus(text, s string) bool {
	for _, m := range statusRE.FindAllStringSubmatch(text, -1) {
		if m[1] == s {
			return true
		}
	}
	return false
}

// ShellWords quotes words for the user's interactive shell. Single quotes
// mean the same in fish, bash and zsh while the word holds no quote,
// backslash or newline, so such words are refused.
func ShellWords(words ...string) (string, error) {
	quoted := make([]string, len(words))
	for i, w := range words {
		if strings.ContainsAny(w, "'\\\n") {
			return "", fmt.Errorf("cannot quote %q for the shell", w)
		}
		quoted[i] = "'" + w + "'"
	}
	return strings.Join(quoted, " "), nil
}

// UserTurns counts the prompts a session recorded; ok is false when the
// count is unknown.
func (o Ops) UserTurns(ctx context.Context, h, sid, path string) (int, bool) {
	if h == config.Claude {
		return harness.ClaudeUserTurns(harness.ClaudeProjectDir(o.Getenv, path) + "/" + sid + ".jsonl")
	}
	return o.OC.UserTurns(ctx, sid)
}
