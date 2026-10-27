package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/gitx"
)

// claudeHookCommand is the command tkt's Claude Code hooks run. It names
// the tkt on PATH, so a settings file you commit works for everyone, and
// the agents in herdr's panes have the PATH tkt watch --install gave herdr.
const claudeHookCommand = "tkt hook event --claude"

// claudeHookEvents are the Claude Code hook events tkt's hooks use, with
// the matcher each needs: a new prompt starts a turn, a stop ends one, and
// a Skill call starts a skill. None rewrites a tool call, so they never
// cancel another hook's rewrite, such as RTK's.
var claudeHookEvents = []struct{ event, matcher string }{
	{"UserPromptSubmit", ""},
	{"Stop", ""},
	{"PreToolUse", "Skill"},
}

// ClaudeHookOpts are tkt hook claude's arguments. Shared writes the
// settings file you commit instead of the local one.
type ClaudeHookOpts struct {
	Install bool
	Shared  bool
	Dry     bool
}

// ClaudeHookResult is tkt hook claude's answer: the settings file, whether
// --install changed it, and the hooks it holds or would hold.
type ClaudeHookResult struct {
	Settings  string         `json:"settings"`
	Installed bool           `json:"installed"`
	Hooks     map[string]any `json:"hooks"`
	DryRun    bool           `json:"dry_run"`
	Actions   []Action       `json:"actions"`
}

func (r *ClaudeHookResult) Text(w io.Writer) {
	if r.Settings != "" {
		fmt.Fprintln(w, r.Settings)
		return
	}
	b, _ := json.MarshalIndent(map[string]any{"hooks": r.Hooks}, "", "  ")
	fmt.Fprintln(w, string(b))
}

// ClaudeHook prints tkt's Claude Code hooks, or with --install adds them to
// the repository's .claude/settings.local.json, or settings.json with
// --shared, keeping everything else in the file.
func (a *App) ClaudeHook(ctx context.Context, o ClaudeHookOpts) (*ClaudeHookResult, error) {
	res := &ClaudeHookResult{Hooks: map[string]any{}, DryRun: o.Dry, Actions: []Action{}}
	for _, e := range claudeHookEvents {
		res.Hooks[e.event] = []any{claudeHookEntry(e.matcher)}
	}
	if !o.Install {
		return res, nil
	}
	repo, err := gitx.Git{R: a.R}.Resolve(ctx, a.Dir)
	if err != nil {
		return nil, err
	}
	name := "settings.local.json"
	if o.Shared {
		name = "settings.json"
	}
	path := filepath.Join(repo.Root, ".claude", name)
	res.Settings = path
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &settings); err != nil {
			return nil, apperr.New(apperr.Usage, "%s is not valid JSON: %v", path, err).
				WithHint("fix the file, then run tkt hook claude --install again")
		}
	}
	if !addClaudeHooks(settings) {
		a.UI.Info("%s holds tkt's hooks already", path)
		return res, nil
	}
	if o.Dry {
		c := &Ctx{App: a, Ctx: ctx, Dry: true}
		c.Would([]string{"tee", path})
		res.Actions = c.Plan
		return res, nil
	}
	b, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, apperr.New(apperr.Internal, "cannot create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return nil, apperr.New(apperr.Internal, "cannot write %s: %v", path, err)
	}
	res.Installed = true
	if !o.Shared {
		a.UI.Info("a ticket's worktree gets %s only when worktrunk copies ignored files into it, as wt step copy-ignored does", name)
	}
	a.UI.Info("Claude Code reads the hooks when a session starts; tkt resume ID restarts a ticket's agent")
	return res, nil
}

func claudeHookEntry(matcher string) map[string]any {
	e := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": claudeHookCommand, "timeout": 10}}}
	if matcher != "" {
		e["matcher"] = matcher
	}
	return e
}

// addClaudeHooks adds the hooks settings lacks, and reports whether it
// added any.
func addClaudeHooks(settings map[string]any) bool {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	added := false
	for _, e := range claudeHookEvents {
		if hasClaudeHook(hooks, e.event, e.matcher) {
			continue
		}
		list, _ := hooks[e.event].([]any)
		hooks[e.event] = append(list, claudeHookEntry(e.matcher))
		added = true
	}
	settings["hooks"] = hooks
	return added
}

// hasClaudeHook reports whether hooks runs tkt hook event --claude on event
// for every tool the matcher names.
func hasClaudeHook(hooks map[string]any, event, matcher string) bool {
	list, _ := hooks[event].([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		m, _ := entry["matcher"].(string)
		if matcher != "" && m != "" && m != "*" && !slices.Contains(strings.Split(m, "|"), matcher) {
			continue
		}
		cmds, _ := entry["hooks"].([]any)
		for _, c := range cmds {
			h, _ := c.(map[string]any)
			if cmd, _ := h["command"].(string); strings.Contains(cmd, "tkt hook event") && strings.Contains(cmd, "--claude") {
				return true
			}
		}
	}
	return false
}

// claudeSettingsFiles lists the settings files Claude Code reads for a
// session in the repository: the project's shared and local ones, then
// yours.
func claudeSettingsFiles(root string, getenv func(string) string) []string {
	var files []string
	if root != "" {
		files = append(files, filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, ".claude", "settings.local.json"))
	}
	return append(files, filepath.Join(getenv("HOME"), ".claude", "settings.json"))
}

// claudeHooksCheck is tkt doctor's view of tkt's Claude Code hooks, for a
// configuration that runs Claude Code. The hooks may sit in any of the
// settings files, split across them as Claude Code merges them.
func (a *App) claudeHooksCheck(cfg config.Config, cfgErr error, root string) Check {
	const id = "claude_hooks"
	if cfgErr != nil || !slices.ContainsFunc(cfg.Presets, func(p config.Preset) bool { return p.Harness == config.Claude }) {
		return Check{ID: id, Status: CheckSkip, Message: "no preset runs Claude Code"}
	}
	var missing, where []string
	files := claudeSettingsFiles(root, a.Getenv)
	parsed := map[string]map[string]any{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s map[string]any
		if err := json.Unmarshal(b, &s); err != nil {
			return Check{ID: id, Status: CheckWarn, Message: f + " is not valid JSON, so Claude Code ignores it", Hint: "fix the file, then run tkt doctor again"}
		}
		parsed[f] = s
	}
	for _, e := range claudeHookEvents {
		found := false
		for _, f := range files {
			hooks, _ := parsed[f]["hooks"].(map[string]any)
			if hasClaudeHook(hooks, e.event, e.matcher) {
				found = true
				if !slices.Contains(where, f) {
					where = append(where, f)
				}
			}
		}
		if !found {
			missing = append(missing, e.event)
		}
	}
	if len(missing) > 0 {
		return Check{ID: id, Status: CheckWarn,
			Message: "tkt reads Claude Code agents' status lines only from their transcripts, since the settings lack tkt's " + strings.Join(missing, ", ") + " hooks",
			Hint:    "tkt hook claude --install, or --shared to write the settings file you commit"}
	}
	msg := "tkt's hooks are in " + strings.Join(where, " and ")
	if len(where) == 1 && strings.HasSuffix(where[0], "settings.local.json") {
		msg += "; a ticket's worktree gets that file only when worktrunk copies ignored files into it"
	}
	return Check{ID: id, Status: CheckOK, Message: msg}
}
