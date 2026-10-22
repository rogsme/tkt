// Package app runs tkt's commands. Each command returns a typed result and an
// error; the cli package renders them as text or JSON.
package app

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/lifecycle"
	"gitlab.com/rogs/tkt/internal/lock"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/runner"
	"gitlab.com/rogs/tkt/internal/state"
	"gitlab.com/rogs/tkt/internal/wt"
)

// App holds what every command shares: the process's streams, environment
// and the runner for external commands.
type App struct {
	Stdin    io.Reader
	StdinTTY bool
	UI       *output.UI
	Getenv   func(string) string
	// Cwd is the real working directory; Dir is where tkt looks for the
	// repository (-C, or Cwd).
	Cwd   string
	Dir   string
	R     runner.Runner
	Now   func() time.Time
	Sleep func(time.Duration)
	// Version is the running tkt's version, such as v0.2.0, or dev.
	Version string
	// StdoutTTY is set when stdout is a terminal, which tkt agent needs.
	StdoutTTY bool
}

// ConfigDir is tkt's own configuration directory.
func (a *App) ConfigDir() string {
	if d := a.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "tkt")
	}
	return filepath.Join(a.Getenv("HOME"), ".config", "tkt")
}

// DataDir is where tkt unpacks its bundled pi and extension.
func (a *App) DataDir() string {
	if d := a.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "tkt")
	}
	return filepath.Join(a.Getenv("HOME"), ".local", "share", "tkt")
}

// Action is one change a dry run would make.
type Action struct {
	Argv    []string `json:"argv"`
	Display string   `json:"display"`
}

// Ctx is one command's run in one repository.
type Ctx struct {
	*App
	Ctx   context.Context
	Dry   bool
	Force bool
	Repo  gitx.Repo
	LC    lifecycle.Config
	Cfg   config.Config
	Git   gitx.Git
	WT    wt.Client
	H     herdr.Client
	OC    harness.OpenCode
	Agent agent.Ops
	Plan  []Action
	locks []*os.File
}

// begin checks the tools every repository command needs and loads the
// repository, its lifecycle configuration and tkt's settings.
func (a *App) begin(ctx context.Context, dry, force bool) (*Ctx, error) {
	c := &Ctx{App: a, Ctx: ctx, Dry: dry, Force: force}
	if err := c.needTools("git", "wt"); err != nil {
		return nil, err
	}
	c.Git = gitx.Git{R: a.R}
	repo, err := c.Git.Resolve(ctx, a.Dir)
	if err != nil {
		return nil, err
	}
	c.Repo = repo
	if c.LC, err = lifecycle.Load(repo.Root); err != nil {
		return nil, err
	}
	if c.Cfg, err = config.Load(a.ConfigDir()); err != nil {
		return nil, err
	}
	c.WT = wt.Client{R: a.R, Root: repo.Root}
	c.H = herdr.Client{R: a.R}
	c.OC = harness.OpenCode{R: a.R}
	c.Agent = agent.Ops{H: c.H, OC: c.OC, UI: a.UI, Getenv: a.Getenv, Dry: dry, Would: c.Would, Sleep: a.Sleep}
	return c, nil
}

// release drops the locks the command took. A command that hands a ticket
// to another one, as tkt sync hands a conflict to tkt run, releases early.
func (c *Ctx) release() {
	for _, f := range c.locks {
		f.Close()
	}
	c.locks = nil
}

// ticket marks the commands that follow as working on id, for the log.
func (c *Ctx) ticket(id string) {
	c.Ctx = runner.WithTicket(c.Ctx, id)
}

// Would reports a change a dry run skips.
func (c *Ctx) Would(argv []string) {
	d := runner.Display(argv)
	c.UI.Info("dry run: %s", d)
	c.Plan = append(c.Plan, Action{Argv: argv, Display: d})
}

func (c *Ctx) needTools(tools ...string) error {
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			return apperr.New(apperr.ToolMissing, "%s is not installed", t).WithHint("install %s, then run tkt doctor", t)
		}
	}
	return nil
}

func (c *Ctx) needHerdr() error {
	if c.Getenv("HERDR_ENV") != "1" || c.Getenv("HERDR_WORKSPACE_ID") == "" {
		return apperr.New(apperr.HerdrEnvMissing, "run tkt from a herdr pane (HERDR_ENV is not set)").
			WithHint("open a herdr pane and run tkt there")
	}
	return c.needTools("herdr")
}

func (c *Ctx) lockTicket(id string) error {
	if c.Dry {
		return nil
	}
	f, err := lock.Try(c.Repo.Common, id)
	if errors.Is(err, lock.ErrHeld) {
		return apperr.New(apperr.TicketLocked, "another tkt command is working on %s", id).
			WithHint("wait for the other tkt command to finish, then retry")
	}
	if err != nil {
		return apperr.New(apperr.Internal, "cannot lock %s: %v", id, err)
	}
	c.locks = append(c.locks, f)
	return nil
}

// herdrErr turns a failed herdr call into tkt's error.
func herdrErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*apperr.Error); ok {
		return err
	}
	e := apperr.New(apperr.HerdrFailed, "%s", err.Error()).Wrap(err)
	if code := herdr.CodeOf(err); code != "" {
		e.With("herdr_code", code)
	}
	return e
}

// ticketState is one worktree's state file.
type ticketState struct {
	path string
	st   state.State
}

func (c *Ctx) readState(worktree string) (*ticketState, error) {
	dir, err := c.Git.AbsGitDir(c.Ctx, worktree)
	if err != nil {
		return nil, err
	}
	p := state.Path(dir)
	return &ticketState{path: p, st: state.Read(p)}, nil
}

// save records key=value pairs, and writes them unless this is a dry run.
// It writes only these keys, so what another tkt wrote since ts was read
// stays, and ts then holds the file as written.
func (c *Ctx) save(ts *ticketState, kv ...string) {
	ts.st.Set(kv...)
	if c.Dry || ts.path == "" {
		return
	}
	st, err := state.Update(ts.path, c.Now(), kv...)
	if err != nil {
		c.UI.Warn("cannot write %s: %v", ts.path, err)
		return
	}
	// Callers hold ts.st itself, so it is updated in place.
	for k, v := range st {
		ts.st[k] = v
	}
}

// ticketPath returns the ticket's worktree.
func (c *Ctx) ticketPath(id string) (string, error) {
	es, err := c.WT.List(c.Ctx)
	if err != nil {
		return "", err
	}
	e, ok := es.Find(id)
	if !ok {
		return "", apperr.New(apperr.WorktreeMissing, "%s has no worktree; run tkt start %s", id, id).
			WithHint("tkt start %s", id)
	}
	return e.Path, nil
}

// ticketWorktree is a worktree that belongs to a ticket.
type ticketWorktree struct {
	ID   string
	Path string
}

func (c *Ctx) ticketWorktrees(es wt.Entries) []ticketWorktree {
	var out []ticketWorktree
	for _, e := range es {
		if c.LC.IsTicketWorktree(e.Branch, e.Path) {
			out = append(out, ticketWorktree{ID: e.Branch, Path: e.Path})
		}
	}
	return out
}

// screenErr attaches the agent's screen to an uncertain-outcome error.
func (c *Ctx) screenErr(e *apperr.Error, pane string) error {
	return e.With("screen", c.Agent.ShowScreen(c.Ctx, pane))
}
