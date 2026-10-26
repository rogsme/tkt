package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/lifecycle"
	"gitlab.com/rogs/tkt/internal/pi"
	"gitlab.com/rogs/tkt/internal/runner"
)

// FrontOpts are tkt agent's arguments.
type FrontOpts struct {
	// Model overrides the [agent] model.
	Model    string
	Continue bool
	// Session resumes a chat by its id, as pi's exit line names it.
	Session string
	// PiArgs are pi's own flags, passed through as they are.
	PiArgs []string
	Dry    bool
}

// FrontResult is what tkt agent runs. A real run replaces tkt with pi, so
// only a dry run returns it.
type FrontResult struct {
	Pi string `json:"pi"`
	// Version is empty when TKT_PI names a pi that does not print one.
	Version  string `json:"version"`
	Source   string `json:"source"`
	ExtDir   string `json:"ext_dir"`
	AgentDir string `json:"agent_dir"`
	// Dir is where pi runs: the repository's root, since pi keeps its
	// sessions by directory.
	Dir  string   `json:"dir"`
	Argv []string `json:"argv"`
	// Env holds the variables tkt sets for pi.
	Env     map[string]string `json:"env"`
	DryRun  bool              `json:"dry_run"`
	Actions []Action          `json:"actions"`
}

func (r *FrontResult) Text(w io.Writer) {
	v := r.Version
	if v == "" {
		v = "of unknown version"
	}
	from := "bundled with tkt"
	if r.Source == pi.SourceEnv {
		from = "from TKT_PI"
	}
	fmt.Fprintf(w, "would start pi %s %s in %s\n", v, from, r.Dir)
}

// Front opens the tkt agent: it replaces tkt with pi in the repository's
// root, with tkt's extension and pi settings of its own. It returns only
// from a dry run or on a failure.
func (a *App) Front(ctx context.Context, o FrontOpts) (*FrontResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	if err := c.needHerdr(); err != nil {
		return nil, err
	}
	if !c.Dry && (!a.StdinTTY || !a.StdoutTTY) {
		return nil, apperr.New(apperr.TerminalRequired, "tkt agent is a chat, so it needs a terminal on stdin and stdout").
			WithHint("run tkt agent in a herdr pane, or add --dry-run to see what it would run")
	}
	rt, err := pi.Resolve(a.Getenv, a.DataDir(), a.UI.Info)
	if err != nil {
		return nil, err
	}
	ext, err := pi.WriteExt(a.DataDir())
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, apperr.New(apperr.Internal, "cannot find the running tkt: %v", err).Wrap(err)
	}
	agentDir := filepath.Join(a.ConfigDir(), "pi")
	model := o.Model
	if model == "" {
		model = c.Cfg.Agent.Model
	}
	launch := pi.Launch{Model: model, Extensions: c.Cfg.Agent.Extensions, Continue: o.Continue, Session: o.Session, Args: o.PiArgs}
	if !themeSet(agentDir) {
		launch.Theme = pi.Theme
	}
	argv := pi.Argv(rt.Path, ext, launch)
	res := &FrontResult{Pi: rt.Path, Version: rt.Version, Source: rt.Source, ExtDir: ext, AgentDir: agentDir,
		Dir: c.Repo.Root, Argv: argv, Env: map[string]string{}, DryRun: c.Dry}
	if c.Dry && res.Version == "" {
		res.Version = c.piVersion(rt.Path)
	}
	host := pi.Host{AgentDir: agentDir, Bin: self, Version: a.Version, PiVersion: res.Version, WebFile: webFilePath(c.Repo.Common)}
	for _, kv := range pi.EnvVars(host) {
		k, v, _ := strings.Cut(kv, "=")
		res.Env[k] = v
	}
	if c.Dry {
		c.Would(argv)
		res.Actions = c.Plan
		return res, nil
	}

	if err := seedSettings(agentDir); err != nil {
		return nil, apperr.New(apperr.PiUnpackFailed, "cannot write pi's settings in %s: %v", agentDir, err).Wrap(err).
			WithHint("fix the permissions of %s, then retry", agentDir)
	}
	if err := os.Chdir(c.Repo.Root); err != nil {
		return nil, apperr.New(apperr.Internal, "cannot change to %s: %v", c.Repo.Root, err).Wrap(err)
	}
	rep, ok := a.R.(runner.Replacer)
	if !ok {
		return nil, apperr.New(apperr.Internal, "this tkt cannot replace itself with pi")
	}
	err = rep.Replace(c.Ctx, runner.Cmd{Argv: argv}, pi.Env(os.Environ(), host))
	return nil, apperr.New(apperr.AgentLaunchFailed, "cannot start %s: %v", rt.Path, err).Wrap(err).
		WithHint("run tkt doctor, or set TKT_PI to another pi binary")
}

// piVersion asks a pi that TKT_PI names for its version.
func (c *Ctx) piVersion(path string) string {
	r := c.R.Run(c.Ctx, runner.Cmd{Argv: []string{path, "--version"}, Timeout: 10 * time.Second})
	if !r.OK() {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(r.Out()), "\n")
	return line
}

// seedSettings writes pi's first settings: a quiet startup, since tkt's
// chat needs no banner listing pi's resources. Settings the user changed
// since stay as they are.
func seedSettings(agentDir string) error {
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(agentDir, "settings.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, "{\n  \"quietStartup\": true\n}\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// themeSet reports whether pi's settings name a theme, as /settings saves
// one. Until they do, tkt agent starts pi with tkt's themes.
func themeSet(agentDir string) bool {
	b, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		return false
	}
	var s struct {
		Theme string `json:"theme"`
	}
	return json.Unmarshal(b, &s) == nil && s.Theme != ""
}

// Configured reports whether Dir lies in a repository with the lifecycle
// configuration, where bare tkt opens the agent.
func (a *App) Configured(ctx context.Context) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	repo, err := gitx.Git{R: a.R}.Resolve(ctx, a.Dir)
	if err != nil {
		return false
	}
	_, err = lifecycle.Load(repo.Root)
	return err == nil
}

// piCheck reports the pi that tkt agent would run, without unpacking it.
func (a *App) piCheck(ctx context.Context) Check {
	v := a.Getenv("TKT_PI")
	if v == "" {
		if version, ok := pi.Bundled(); ok {
			return Check{ID: "pi", Status: CheckOK, Message: "pi " + version + ", bundled with tkt"}
		}
		return Check{ID: "pi", Status: CheckWarn, Message: "this tkt has no bundled pi, so tkt agent cannot start",
			Hint: "install a release build of tkt (install.sh), or set TKT_PI to a pi binary"}
	}
	path, err := exec.LookPath(v)
	if err != nil {
		return Check{ID: "pi", Status: CheckWarn, Message: fmt.Sprintf("TKT_PI names %s, which is not a program tkt can run", v),
			Hint: "set TKT_PI to a pi binary, or unset it to use the bundled pi"}
	}
	r := a.R.Run(ctx, runner.Cmd{Argv: []string{path, "--version"}, Timeout: 10 * time.Second})
	line, _, _ := strings.Cut(strings.TrimSpace(r.Out()+"\n"+string(r.Stderr)), "\n")
	if !r.OK() {
		return Check{ID: "pi", Status: CheckWarn, Message: fmt.Sprintf("%s --version failed: %s", path, runner.Head(line, 120)),
			Hint: "set TKT_PI to a pi binary, or unset it to use the bundled pi"}
	}
	return Check{ID: "pi", Status: CheckOK, Message: fmt.Sprintf("pi %s from TKT_PI (%s)", line, path)}
}
