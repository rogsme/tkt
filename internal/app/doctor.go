package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/lifecycle"
	"gitlab.com/rogs/tkt/internal/runner"
	"gitlab.com/rogs/tkt/internal/wt"
)

// Check statuses.
const (
	CheckOK   = "ok"
	CheckWarn = "warn"
	CheckFail = "fail"
	CheckSkip = "skip"
)

// Check is one thing tkt doctor checked.
type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Want    string `json:"want,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// DoctorResult lists the checks in a fixed order.
type DoctorResult struct {
	Checks []Check `json:"checks"`
}

func (r *DoctorResult) Text(w io.Writer) {
	for _, c := range r.Checks {
		fmt.Fprintf(w, "%-5s %-16s %s\n", c.Status, c.ID, c.Message)
		if c.Hint != "" && c.Status != CheckOK {
			fmt.Fprintf(w, "%-22s hint: %s\n", "", c.Hint)
		}
	}
}

// Minimum versions tkt supports.
const (
	minGit   = "2.31"
	minWT    = "0.68"
	minHerdr = "0.9.3"
)

var versionRE = regexp.MustCompile(`\d+(\.\d+)+`)

// versionAtLeast reports whether the first version number in text is at
// least min, and returns that number.
func versionAtLeast(text, min string) (string, bool) {
	v := versionRE.FindString(text)
	if v == "" {
		return "", false
	}
	a, b := strings.Split(v, "."), strings.Split(min, ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x, _ = strconv.Atoi(a[i])
		}
		if i < len(b) {
			y, _ = strconv.Atoi(b[i])
		}
		if x != y {
			return v, x > y
		}
	}
	return v, true
}

// Doctor checks what tkt needs and reports each check. Checks that need the
// repository are skipped outside one.
func (a *App) Doctor(ctx context.Context) (*DoctorResult, error) {
	git := gitx.Git{R: a.R}
	var (
		repo    gitx.Repo
		repoErr error
		lc      lifecycle.Config
		lcErr   error
	)
	if _, err := exec.LookPath("git"); err == nil {
		repo, repoErr = git.Resolve(ctx, a.Dir)
		if repoErr == nil {
			lc, lcErr = lifecycle.Load(repo.Root)
		}
	}
	cfg, cfgErr := config.Load(a.ConfigDir())

	var checks []func() Check
	add := func(f func() Check) { checks = append(checks, f) }
	add(func() Check { return a.toolCheck(ctx, "git", "git", minGit, true) })
	add(func() Check {
		switch {
		case repoErr != nil:
			e := apperr.As(repoErr)
			return Check{ID: "repository", Status: CheckFail, Message: e.Msg, Hint: e.Hint}
		case repo.Root == "":
			return Check{ID: "repository", Status: CheckSkip, Message: "git is missing"}
		}
		return Check{ID: "repository", Status: CheckOK, Message: repo.Root}
	})
	add(func() Check {
		switch {
		case repo.Root == "":
			return Check{ID: "lifecycle", Status: CheckSkip, Message: "not inside a repository"}
		case lcErr != nil:
			e := apperr.As(lcErr)
			return Check{ID: "lifecycle", Status: CheckFail, Message: e.Msg, Hint: e.Hint}
		}
		return Check{ID: "lifecycle", Status: CheckOK,
			Message: fmt.Sprintf("tickets match %s; forge %s %s, base %s/%s", lc.Pattern, or(lc.Forge.Kind, "unnamed"), lc.Forge.Repo, lc.Forge.Remote, lc.Forge.Base)}
	})
	add(func() Check {
		if cfgErr != nil {
			e := apperr.As(cfgErr)
			if e.Code == apperr.ConfigLegacy {
				return Check{ID: "config", Status: CheckFail, Message: "the presets and stages files use the old format",
					Hint: "run tkt ls to print the config.toml to write, then delete the old files"}
			}
			return Check{ID: "config", Status: CheckFail, Message: e.Msg, Hint: or(e.Hint, "tkt help config")}
		}
		msg := fmt.Sprintf("presets %s from %s", strings.Join(cfg.PresetNames(), ", "), cfg.Source)
		if len(cfg.IgnoredLegacy) > 0 {
			return Check{ID: "config", Status: CheckWarn, Message: msg + "; ignoring " + strings.Join(cfg.IgnoredLegacy, " and "),
				Hint: "delete " + strings.Join(cfg.IgnoredLegacy, " and ")}
		}
		return Check{ID: "config", Status: CheckOK, Message: msg}
	})
	add(func() Check { return a.toolCheck(ctx, "wt", "wt", minWT, true) })
	add(func() Check { return a.toolCheck(ctx, "herdr", "herdr", minHerdr, true) })
	add(func() Check {
		if a.Getenv("HERDR_ENV") == "1" && a.Getenv("HERDR_WORKSPACE_ID") != "" {
			return Check{ID: "herdr_env", Status: CheckOK, Message: "inside herdr workspace " + a.Getenv("HERDR_WORKSPACE_ID")}
		}
		return Check{ID: "herdr_env", Status: CheckWarn, Message: "not inside a herdr pane; start, run, resume, done and wait need one",
			Hint: "open a herdr pane and run tkt there"}
	})
	add(func() Check {
		switch {
		case repo.Root == "" || lcErr != nil:
			return Check{ID: "gh", Status: CheckSkip, Message: "the forge is unknown"}
		case lc.Forge.Kind != "GitHub":
			return Check{ID: "gh", Status: CheckSkip, Message: fmt.Sprintf("the forge is %s; tkt done and tkt ls support GitHub only", or(lc.Forge.Kind, "not set"))}
		}
		return a.toolCheck(ctx, "gh", "gh", "", true)
	})
	if cfgErr == nil {
		seen := map[string]bool{}
		for _, p := range cfg.Presets {
			if seen[p.Harness] {
				continue
			}
			seen[p.Harness] = true
			required := p.Harness == cfg.Default().Harness
			add(func() Check {
				c := a.toolCheck(ctx, p.Harness, "harness."+p.Harness, "", required)
				if c.Status == CheckWarn {
					c.Message += "; presets that use it fail"
				}
				return c
			})
		}
	}
	add(func() Check {
		if repo.Root == "" {
			return Check{ID: "worktrunk_hooks", Status: CheckSkip, Message: "not inside a repository"}
		}
		if (wt.Client{R: a.R, Root: repo.Root}).HasPostStartHooks(ctx) {
			return Check{ID: "worktrunk_hooks", Status: CheckWarn, Message: "worktrunk runs post-start hooks in the background, so an agent may start before they finish",
				Hint: "move the setup an agent needs to a pre-start hook"}
		}
		return Check{ID: "worktrunk_hooks", Status: CheckOK, Message: "no post-start hooks"}
	})
	add(func() Check {
		dir := filepath.Dir(LogPath(a.Getenv))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Check{ID: "log", Status: CheckWarn, Message: fmt.Sprintf("cannot create %s: %v", dir, err)}
		}
		f, err := os.CreateTemp(dir, ".doctor")
		if err != nil {
			return Check{ID: "log", Status: CheckWarn, Message: fmt.Sprintf("cannot write to %s: %v", dir, err)}
		}
		f.Close()
		os.Remove(f.Name())
		return Check{ID: "log", Status: CheckOK, Message: "logging commands to " + LogPath(a.Getenv)}
	})

	res := &DoctorResult{Checks: make([]Check, len(checks))}
	g := new(errgroup.Group)
	for i, f := range checks {
		g.Go(func() error { res.Checks[i] = f(); return nil })
	}
	g.Wait()
	var failed []string
	for _, c := range res.Checks {
		if c.Status == CheckFail {
			failed = append(failed, c.ID)
		}
	}
	if len(failed) > 0 {
		return res, apperr.New(apperr.ChecksFailed, "failing checks: %s", strings.Join(failed, ", ")).With("checks", res.Checks)
	}
	return res, nil
}

// toolCheck checks that a program is installed and, when min is set, new
// enough. A missing optional program is a warning.
func (a *App) toolCheck(ctx context.Context, tool, id, min string, required bool) Check {
	missing := CheckFail
	if !required {
		missing = CheckWarn
	}
	if _, err := exec.LookPath(tool); err != nil {
		return Check{ID: id, Status: missing, Message: tool + " is not installed", Want: min, Hint: "install " + tool}
	}
	r := a.R.Run(ctx, runner.Cmd{Argv: []string{tool, "--version"}, Timeout: 10 * time.Second})
	out := strings.TrimSpace(r.Out() + "\n" + string(r.Stderr))
	line, _, _ := strings.Cut(out, "\n")
	if !r.OK() {
		return Check{ID: id, Status: missing, Message: fmt.Sprintf("%s --version failed: %s", tool, runner.Head(line, 120)), Want: min}
	}
	if min == "" {
		return Check{ID: id, Status: CheckOK, Message: line}
	}
	v, ok := versionAtLeast(out, min)
	if !ok {
		return Check{ID: id, Status: CheckFail, Message: fmt.Sprintf("%s %s is older than %s", tool, or(v, "of unknown version"), min),
			Want: min, Hint: fmt.Sprintf("update %s to %s or newer", tool, min)}
	}
	return Check{ID: id, Status: CheckOK, Message: line, Want: min}
}

// LogPath is where tkt logs the commands it runs.
func LogPath(getenv func(string) string) string {
	if d := getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "tkt", "tkt.log")
	}
	return filepath.Join(getenv("HOME"), ".local", "state", "tkt", "tkt.log")
}
