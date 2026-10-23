// Package herdr drives the herdr terminal multiplexer through its CLI, which
// answers {"result": ...} on stdout or {"error": {"code": ...}} on stderr.
package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gitlab.com/rogs/tkt/internal/runner"
)

// Client runs herdr commands.
type Client struct {
	R runner.Runner
}

// Error is a failed herdr command.
type Error struct {
	Argv   []string
	Code   string
	Stderr string
}

func (e *Error) Error() string {
	return fmt.Sprintf("herdr %s: %s %s", strings.Join(e.Argv, " "), e.Code, runner.Head(e.Stderr, 200))
}

// ErrorCode returns the error code herdr printed on stderr, or "".
func ErrorCode(stderr []byte) string {
	var v struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(stderr, &v) != nil {
		return ""
	}
	return v.Error.Code
}

// CodeOf returns the herdr error code inside err, or "".
func CodeOf(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ""
}

func (c Client) call(ctx context.Context, quiet bool, args ...string) (json.RawMessage, error) {
	r := c.R.Run(ctx, runner.Cmd{Argv: append([]string{"herdr"}, args...), Quiet: quiet})
	if !r.OK() {
		return nil, &Error{Argv: args, Code: ErrorCode(r.Stderr), Stderr: strings.TrimSpace(r.ErrText())}
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(r.Stdout, &env); err != nil {
		return nil, &Error{Argv: args, Code: "bad_output", Stderr: runner.Head(r.Out(), 200)}
	}
	return env.Result, nil
}

func decode[T any](raw json.RawMessage, err error) (T, error) {
	var v T
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, &Error{Code: "bad_output", Stderr: err.Error()}
	}
	return v, nil
}

// Agents lists every agent.
func (c Client) Agents(ctx context.Context) ([]Agent, error) {
	v, err := decode[struct{ Agents []Agent }](c.call(ctx, false, "agent", "list"))
	return v.Agents, err
}

// Panes lists every pane.
func (c Client) Panes(ctx context.Context) ([]Pane, error) {
	v, err := decode[struct{ Panes []Pane }](c.call(ctx, false, "pane", "list"))
	return v.Panes, err
}

// Tabs lists the tabs of every workspace, in workspace order.
func (c Client) Tabs(ctx context.Context) ([]Tab, error) {
	ws, err := decode[struct {
		Workspaces []struct {
			ID string `json:"workspace_id"`
		}
	}](c.call(ctx, false, "workspace", "list"))
	if err != nil {
		return nil, err
	}
	var out []Tab
	for _, w := range ws.Workspaces {
		v, err := decode[struct{ Tabs []Tab }](c.call(ctx, false, "tab", "list", "--workspace", w.ID))
		if err != nil {
			return nil, err
		}
		out = append(out, v.Tabs...)
	}
	return out, nil
}

// PaneGet returns one pane. Older herdr releases put the fields at the top of
// the result instead of under "pane".
func (c Client) PaneGet(ctx context.Context, pane string) (Pane, error) {
	raw, err := c.call(ctx, false, "pane", "get", pane)
	nested, err := decode[struct {
		Pane *Pane `json:"pane"`
	}](raw, err)
	if err != nil {
		return Pane{}, err
	}
	if nested.Pane != nil {
		return *nested.Pane, nil
	}
	return decode[Pane](raw, nil)
}

// PaneExists reports whether herdr knows the pane.
func (c Client) PaneExists(ctx context.Context, pane string) bool {
	_, err := c.call(ctx, true, "pane", "get", pane)
	return err == nil
}

// PaneIsShell reports whether the pane's foreground process is its shell,
// that is, nothing runs at its prompt.
func (c Client) PaneIsShell(ctx context.Context, pane string) bool {
	v, err := decode[struct {
		Info struct {
			Foreground *int64 `json:"foreground_process_group_id"`
			Shell      *int64 `json:"shell_pid"`
		} `json:"process_info"`
	}](c.call(ctx, true, "pane", "process-info", "--pane", pane))
	return err == nil && v.Info.Foreground != nil && v.Info.Shell != nil && *v.Info.Foreground == *v.Info.Shell
}

// AgentOnPane returns the agent in a pane, if any.
func (c Client) AgentOnPane(ctx context.Context, pane string) (Agent, bool) {
	v, err := decode[struct{ Agents []Agent }](c.call(ctx, true, "agent", "list"))
	if err != nil {
		return Agent{}, false
	}
	for _, a := range v.Agents {
		if a.PaneID == pane {
			return a, true
		}
	}
	return Agent{}, false
}

// TabCreateArgv is the herdr command that opens a tab without focusing it.
// env holds KEY=VALUE pairs for the tab's shell, and so for the agent tkt
// starts at its prompt.
func TabCreateArgv(workspace, cwd, label string, env ...string) []string {
	argv := []string{"herdr", "tab", "create", "--workspace", workspace, "--cwd", cwd, "--label", label}
	for _, kv := range env {
		argv = append(argv, "--env", kv)
	}
	return append(argv, "--no-focus")
}

// TabCreate opens a tab and returns it with its first pane.
func (c Client) TabCreate(ctx context.Context, workspace, cwd, label string, env ...string) (tab, pane string, err error) {
	v, err := decode[struct {
		Tab struct {
			ID string `json:"tab_id"`
		} `json:"tab"`
		Root struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}](c.call(ctx, false, TabCreateArgv(workspace, cwd, label, env...)[1:]...))
	return v.Tab.ID, v.Root.ID, err
}

// WorkspaceCreateArgv is the herdr command that opens a workspace in cwd
// without focusing it.
func WorkspaceCreateArgv(cwd, label string) []string {
	return []string{"herdr", "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus"}
}

// WorkspaceCreate opens a workspace and returns its id.
func (c Client) WorkspaceCreate(ctx context.Context, cwd, label string) (string, error) {
	v, err := decode[struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
	}](c.call(ctx, false, WorkspaceCreateArgv(cwd, label)[1:]...))
	return v.Workspace.ID, err
}

// TabClose closes a tab.
func (c Client) TabClose(ctx context.Context, tab string) error {
	_, err := c.call(ctx, false, "tab", "close", tab)
	return err
}

// TabRename relabels a tab.
func (c Client) TabRename(ctx context.Context, tab, label string) error {
	_, err := c.call(ctx, false, "tab", "rename", tab, label)
	return err
}

// TabFocus switches to a tab.
func (c Client) TabFocus(ctx context.Context, tab string) error {
	_, err := c.call(ctx, false, "tab", "focus", tab)
	return err
}

// AgentStartArgv is the herdr command that starts an agent in a pane.
func AgentStartArgv(name, kind, pane string, args []string) []string {
	return append([]string{"herdr", "agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", "90000", "--"}, args...)
}

// AgentStart starts an agent and waits until it is ready.
func (c Client) AgentStart(ctx context.Context, name, kind, pane string, args []string) error {
	_, err := c.call(ctx, false, AgentStartArgv(name, kind, pane, args)[1:]...)
	return err
}

// PromptArgv is the herdr command that types text into an agent and waits
// until it starts working or asks for an answer.
func PromptArgv(target, text string) []string {
	return []string{"herdr", "agent", "prompt", target, text, "--wait", "--until", "working", "--until", "blocked", "--timeout", "60000"}
}

// Prompt sends text to an agent and waits for it to react.
func (c Client) Prompt(ctx context.Context, target, text string) error {
	_, err := c.call(ctx, false, PromptArgv(target, text)[1:]...)
	return err
}

// Type sends text to an agent without waiting.
func (c Client) Type(ctx context.Context, target, text string) error {
	_, err := c.call(ctx, false, "agent", "prompt", target, text)
	return err
}

// Wait blocks until the agent in a pane reaches one of the states, and
// returns the state. timeoutMs of 0 means no limit.
func (c Client) Wait(ctx context.Context, pane string, until []string, timeoutMs int64) (string, error) {
	args := []string{"agent", "wait", pane}
	for _, u := range until {
		args = append(args, "--until", u)
	}
	if timeoutMs > 0 {
		args = append(args, "--timeout", strconv.FormatInt(timeoutMs, 10))
	}
	v, err := decode[struct {
		Agent Agent `json:"agent"`
	}](c.call(ctx, false, args...))
	return v.Agent.Status, err
}

// Read returns the visible text of the agent's screen, its last lines.
func (c Client) Read(ctx context.Context, pane string, lines int) string {
	return c.read(ctx, pane, "visible", lines)
}

// ReadRecent returns the agent's recent output, which reaches further back
// than its screen.
func (c Client) ReadRecent(ctx context.Context, pane string, lines int) string {
	return c.read(ctx, pane, "recent", lines)
}

func (c Client) read(ctx context.Context, pane, source string, lines int) string {
	r := c.R.Run(ctx, runner.Cmd{Argv: []string{"herdr", "agent", "read", pane, "--source", source, "--lines", strconv.Itoa(lines)}})
	if !r.OK() {
		return ""
	}
	return r.Out()
}

// NotifyArgv is the herdr command that shows a notification. sound is none,
// done or request.
func NotifyArgv(title, body, sound string) []string {
	argv := []string{"herdr", "notification", "show", title}
	if body != "" {
		argv = append(argv, "--body", body)
	}
	if sound != "" {
		argv = append(argv, "--sound", sound)
	}
	return argv
}

// Notify shows a notification. Only the exit status counts: a notification
// has no result to read.
func (c Client) Notify(ctx context.Context, title, body, sound string) error {
	argv := NotifyArgv(title, body, sound)
	if r := c.R.Run(ctx, runner.Cmd{Argv: argv}); !r.OK() {
		return &Error{Argv: argv[1:], Code: ErrorCode(r.Stderr), Stderr: strings.TrimSpace(r.ErrText())}
	}
	return nil
}

// SendKeys presses keys in an agent's pane.
func (c Client) SendKeys(ctx context.Context, pane string, keys ...string) error {
	_, err := c.call(ctx, false, append([]string{"agent", "send-keys", pane}, keys...)...)
	return err
}

// Rename names the agent in a pane.
func (c Client) Rename(ctx context.Context, pane, name string) error {
	_, err := c.call(ctx, false, "agent", "rename", pane, name)
	return err
}

// PaneRun types a command into a pane's shell and presses Enter.
func (c Client) PaneRun(ctx context.Context, pane, command string) error {
	_, err := c.call(ctx, false, "pane", "run", pane, command)
	return err
}
