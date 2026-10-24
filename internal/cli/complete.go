package cli

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/lifecycle"
	"gitlab.com/rogs/tkt/internal/runner"
)

// Completion runs on every Tab press: it reads git and herdr directly, never
// worktrunk, logs nothing and prints no errors. herdr calls give up after two
// seconds, because its socket can hang; local git calls cannot.

type completer = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)

const noFiles = cobra.ShellCompDirectiveNoFileComp

func (g *globals) herdrContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(cmd.Context(), 2*time.Second)
}

func (g *globals) dirForCompletion() string {
	if g.dir != "" {
		return g.dir
	}
	d, _ := os.Getwd()
	return d
}

// ticketPaths maps each ticket worktree's id to its path.
func (g *globals) ticketPaths(cmd *cobra.Command) map[string]string {
	ctx := cmd.Context()
	git := gitx.Git{R: runner.Exec{}}
	repo, err := git.Resolve(ctx, g.dirForCompletion())
	if err != nil {
		return nil
	}
	lc, err := lifecycle.Load(repo.Root)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, w := range git.Worktrees(ctx, repo.Root) {
		if lc.IsTicketWorktree(w.Branch, w.Path) {
			out[w.Branch] = w.Path
		}
	}
	return out
}

// completeTickets offers ticket ids for the first argument, or for every
// argument when many is set, leaving out ids already given.
func (g *globals) completeTickets(many bool) completer {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 && !many {
			return nil, noFiles
		}
		var ids []string
		for id := range g.ticketPaths(cmd) {
			if !slices.Contains(args, id) {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		return ids, noFiles
	}
}

// completeRun offers ticket ids, then stage names described by their line.
func (g *globals) completeRun(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return g.completeTickets(false)(cmd, args, toComplete)
	}
	if len(args) > 1 {
		return nil, noFiles
	}
	cfg, err := config.Load(g.app().ConfigDir(), g.rootForCompletion(cmd))
	if err != nil {
		return nil, noFiles
	}
	var out []string
	for _, name := range cfg.StageNames() {
		out = append(out, name+"\t"+runner.Head(cfg.Stages[name], 60))
	}
	return out, noFiles
}

func (g *globals) completePresets(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	cfg, err := config.Load(g.app().ConfigDir(), "")
	if err != nil {
		return nil, noFiles
	}
	var out []string
	for _, p := range cfg.Presets {
		out = append(out, p.Name+"\t"+p.Harness+", "+or(p.Model, "default model"))
	}
	return out, noFiles
}

// completeChores offers the chores of the repository tkt runs in, with
// their schedule.
func (g *globals) completeChores(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, noFiles
	}
	root := g.rootForCompletion(cmd)
	if root == "" {
		return nil, noFiles
	}
	cfg, err := config.Load(g.app().ConfigDir(), root)
	if err != nil {
		return nil, noFiles
	}
	var out []string
	for _, ch := range cfg.Chores {
		out = append(out, ch.Name+"\t"+ch.Schedule.String())
	}
	return out, noFiles
}

// rootForCompletion is the primary worktree of the repository tkt runs in,
// whose .tkt files add stages and chores, or "" outside one.
func (g *globals) rootForCompletion(cmd *cobra.Command) string {
	repo, err := gitx.Git{R: runner.Exec{}}.Resolve(cmd.Context(), g.dirForCompletion())
	if err != nil {
		return ""
	}
	return repo.Root
}

// completePanes offers the herdr panes inside the ticket's worktree, named
// by their agent.
func (g *globals) completePanes(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return nil, noFiles
	}
	path := g.ticketPaths(cmd)[strings.ToUpper(args[0])]
	ctx, cancel := g.herdrContext(cmd)
	defer cancel()
	h := herdr.Client{R: runner.Exec{}}
	panes, err := h.Panes(ctx)
	if err != nil {
		return nil, noFiles
	}
	agents, _ := h.Agents(ctx)
	var out []string
	for _, p := range panes {
		if path != "" && !herdr.Inside(p.Cwd, path) {
			continue
		}
		desc := "shell"
		for _, a := range agents {
			if a.PaneID == p.PaneID {
				desc = a.Kind + ", " + a.Status
			}
		}
		out = append(out, p.PaneID+"\t"+desc)
	}
	return out, noFiles
}

// completeSessions offers the ticket's Claude Code sessions, newest first.
func (g *globals) completeSessions(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return nil, noFiles
	}
	path := g.ticketPaths(cmd)[strings.ToUpper(args[0])]
	if path == "" {
		return nil, noFiles
	}
	entries, err := os.ReadDir(harness.ClaudeProjectDir(g.io.Getenv, path))
	if err != nil {
		return nil, noFiles
	}
	type session struct {
		id string
		at time.Time
	}
	var ss []session
	for _, e := range entries {
		if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".jsonl") {
			ss = append(ss, session{strings.TrimSuffix(e.Name(), ".jsonl"), info.ModTime()})
		}
	}
	slices.SortFunc(ss, func(a, b session) int { return b.at.Compare(a.at) })
	var out []string
	for _, s := range ss {
		out = append(out, s.id+"\tClaude Code, "+s.at.Format("2006-01-02 15:04"))
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

func fixed(values ...string) completer {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, noFiles
	}
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
