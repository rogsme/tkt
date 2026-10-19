// Package wt drives worktrunk, which creates and removes every worktree tkt
// uses, with its hooks intact.
package wt

import (
	"context"
	"encoding/json"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/runner"
)

// Client runs wt for one repository.
type Client struct {
	R    runner.Runner
	Root string
}

// Entry is one worktree in wt list.
type Entry struct {
	Branch   string `json:"branch"`
	Path     string `json:"path"`
	Mismatch bool   `json:"branch_mismatch"`
}

// Entries is the output of wt list, in its order.
type Entries []Entry

// Find returns the entry for a branch.
func (es Entries) Find(branch string) (Entry, bool) {
	for _, e := range es {
		if e.Branch == branch {
			return e, true
		}
	}
	return Entry{}, false
}

// List runs wt list and reads either JSON schema: 1 is an array of entries,
// 2 an object with items. Entries without a branch or a path are skipped.
func (c Client) List(ctx context.Context) (Entries, error) {
	r := c.R.Run(ctx, runner.Cmd{Argv: []string{"wt", "-C", c.Root, "list", "--format", "json"}})
	if !r.OK() {
		return nil, apperr.New(apperr.WtFailed, "wt list failed: %s", strings.TrimSpace(r.ErrText()))
	}
	es, err := parseList(r.Stdout)
	if err != nil {
		return nil, apperr.New(apperr.WtFailed, "cannot read wt list output").Wrap(err)
	}
	return es, nil
}

type rawEntry struct {
	Branch   *string `json:"branch"`
	Path     *string `json:"path"`
	Worktree *struct {
		Path           *string `json:"path"`
		BranchMismatch bool    `json:"branch_mismatch"`
	} `json:"worktree"`
}

func parseList(b []byte) (Entries, error) {
	var raw []rawEntry
	trimmed := strings.TrimSpace(string(b))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(b, &raw); err != nil {
			return nil, err
		}
		var out Entries
		for _, e := range raw {
			if e.Branch != nil && e.Path != nil {
				out = append(out, Entry{Branch: *e.Branch, Path: *e.Path})
			}
		}
		return out, nil
	}
	var v2 struct {
		Schema int        `json:"schema"`
		Items  []rawEntry `json:"items"`
	}
	if err := json.Unmarshal(b, &v2); err != nil {
		return nil, err
	}
	if v2.Schema != 2 {
		return nil, apperr.New(apperr.WtFailed, "unknown wt list output (schema %d)", v2.Schema)
	}
	var out Entries
	for _, e := range v2.Items {
		if e.Branch != nil && e.Worktree != nil && e.Worktree.Path != nil {
			out = append(out, Entry{Branch: *e.Branch, Path: *e.Worktree.Path, Mismatch: e.Worktree.BranchMismatch})
		}
	}
	return out, nil
}

// SwitchArgv is the command that creates or checks out a ticket's worktree.
func (c Client) SwitchArgv(branch string, create bool) []string {
	argv := []string{"wt", "-C", c.Root, "switch"}
	if create {
		argv = append(argv, "--create")
	}
	return append(argv, branch, "--no-cd", "--format", "json")
}

// RemoveArgv is the command that removes a ticket's worktree.
func (c Client) RemoveArgv(branch string) []string {
	return []string{"wt", "-C", c.Root, "remove", branch, "--foreground", "--reap", "--format", "json"}
}

// Visible runs a wt command with its progress on the user's terminal.
func (c Client) Visible(ctx context.Context, argv []string) runner.Result {
	return c.R.Run(ctx, runner.Cmd{Argv: argv, Visible: true})
}

// HasPostStartHooks reports whether worktrunk runs post-start hooks, which
// run in the background and may not finish before the agent starts.
func (c Client) HasPostStartHooks(ctx context.Context) bool {
	r := c.R.Run(ctx, runner.Cmd{Argv: []string{"wt", "-C", c.Root, "hook", "show"}})
	return strings.Contains(r.Out(), "post-start")
}
