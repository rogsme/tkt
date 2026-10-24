package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"golang.org/x/sync/errgroup"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/lifecycle"
	"gitlab.com/rogs/tkt/internal/lock"
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

// doctorGroups sorts the checks under headings in the terminal view. A check
// missing here lands in the last group.
var doctorGroups = []struct {
	title string
	ids   []string
}{
	{"Tools", []string{"git", "wt", "herdr", "gh", "pi"}},
	{"Harnesses", []string{"opencode_plugin"}},
	{"Project", []string{"repository", "lifecycle", "worktrunk_hooks", "chores"}},
	{"Environment", []string{"config", "herdr_env", "services", "log"}},
}

// doctorLabels names checks whose id reads poorly in the terminal view.
var doctorLabels = map[string]string{
	"wt":              "worktrunk",
	"herdr_env":       "herdr pane",
	"worktrunk_hooks": "worktrunk hooks",
	"opencode_plugin": "opencode plugin",
}

func doctorGroup(id string) int {
	if strings.HasPrefix(id, "harness.") {
		return 1
	}
	for i, g := range doctorGroups {
		if slices.Contains(g.ids, id) {
			return i
		}
	}
	return len(doctorGroups) - 1
}

func doctorLabel(id string) string {
	if l, ok := doctorLabels[id]; ok {
		return l
	}
	return strings.TrimPrefix(id, "harness.")
}

// Pretty writes the checks for a person at a terminal: grouped, colored,
// with home shortened to ~ and messages wrapped to width. Text stays the
// stable format for programs.
func (r *DoctorResult) Pretty(w io.Writer, home string, width int) {
	var (
		heading = lipgloss.NewStyle().Bold(true)
		faint   = lipgloss.NewStyle().Faint(true)
		status  = map[string]lipgloss.Style{
			CheckOK:   lipgloss.NewStyle().Foreground(lipgloss.Green),
			CheckWarn: lipgloss.NewStyle().Foreground(lipgloss.Yellow).Bold(true),
			CheckFail: lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true),
			CheckSkip: faint,
		}
	)
	short := func(s string) string {
		if home == "" || home == "/" {
			return s
		}
		return strings.ReplaceAll(s, home+"/", "~/")
	}
	labelW := 0
	for _, c := range r.Checks {
		labelW = max(labelW, len(doctorLabel(c.ID)))
	}
	indent := 4 + 6 + labelW + 2
	pad := strings.Repeat(" ", indent)
	wrap := func(s string) []string {
		if width-indent < 30 {
			return []string{s}
		}
		return wordWrap(s, width-indent)
	}

	groups := make([][]Check, len(doctorGroups))
	counts := map[string]int{}
	for _, c := range r.Checks {
		i := doctorGroup(c.ID)
		groups[i] = append(groups[i], c)
		counts[c.Status]++
	}
	fmt.Fprintln(w)
	for i, g := range doctorGroups {
		if len(groups[i]) == 0 {
			continue
		}
		fmt.Fprintf(w, "  %s\n", heading.Render(g.title))
		for _, c := range groups[i] {
			msg := wrap(short(c.Message))
			fmt.Fprintf(w, "    %s%-*s  %s\n", status[c.Status].Render(fmt.Sprintf("%-6s", c.Status)), labelW, doctorLabel(c.ID), msg[0])
			for _, l := range msg[1:] {
				fmt.Fprintf(w, "%s%s\n", pad, l)
			}
			if c.Hint != "" && c.Status != CheckOK {
				for _, l := range wrap("hint: " + short(c.Hint)) {
					fmt.Fprintf(w, "%s%s\n", pad, faint.Render(l))
				}
			}
		}
		fmt.Fprintln(w)
	}

	var parts []string
	for _, s := range []string{CheckOK, CheckWarn, CheckFail, CheckSkip} {
		if n := counts[s]; n > 0 {
			parts = append(parts, status[s].Render(fmt.Sprintf("%d %s", n, s)))
		}
	}
	verdict := "tkt is ready"
	switch {
	case counts[CheckFail] > 0:
		verdict = status[CheckFail].Render("fix the failing checks before using tkt")
	case counts[CheckWarn] > 0:
		verdict = "tkt is ready, with warnings"
	}
	fmt.Fprintf(w, "  %s  %s\n\n", strings.Join(parts, faint.Render(", ")), verdict)
}

// wordWrap breaks s at spaces into lines of at most width bytes. A word
// longer than width gets a line of its own, so paths and flags stay whole.
func wordWrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	return append(lines, line)
}

// Minimum versions tkt supports. git 2.38 brought git merge-tree
// --write-tree, which tkt sync merges the base with first.
const (
	minGit   = "2.38"
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
	cfg, cfgErr := config.Load(a.ConfigDir(), repo.Root)

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
		if cfg.Project != "" {
			msg += "; repository settings from " + cfg.Project
		}
		if len(cfg.IgnoredLegacy) > 0 {
			return Check{ID: "config", Status: CheckWarn, Message: msg + "; ignoring " + strings.Join(cfg.IgnoredLegacy, " and "),
				Hint: "delete " + strings.Join(cfg.IgnoredLegacy, " and ")}
		}
		return Check{ID: "config", Status: CheckOK, Message: msg}
	})
	add(func() Check { return a.toolCheck(ctx, "wt", "wt", minWT, true) })
	add(func() Check { return a.toolCheck(ctx, "herdr", "herdr", minHerdr, true) })
	add(func() Check { return a.piCheck(ctx) })
	add(func() Check {
		if a.Getenv("HERDR_ENV") == "1" && a.Getenv("HERDR_WORKSPACE_ID") != "" {
			return Check{ID: "herdr_env", Status: CheckOK, Message: "inside herdr workspace " + a.Getenv("HERDR_WORKSPACE_ID")}
		}
		return Check{ID: "herdr_env", Status: CheckWarn, Message: "not inside a herdr pane; start, approve, send, auto, run, resume, done, show and wait need one",
			Hint: "open a herdr pane and run tkt there"}
	})
	add(func() Check {
		var harnesses []string
		if cfgErr == nil {
			for _, p := range cfg.Presets {
				if !slices.Contains(harnesses, p.Harness) {
					harnesses = append(harnesses, p.Harness)
				}
			}
		}
		return a.serviceCheck(ctx, repo.Root, harnesses)
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
	add(func() Check { return a.pluginCheck(cfg, cfgErr) })
	add(func() Check { return a.choresCheck(cfg, cfgErr, repo) })
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

// choresCheck checks that each chore's repository exists, and that a tkt
// watch runs for this repository to start its chores: a chore whose watch
// never runs never runs either, and says nothing.
func (a *App) choresCheck(cfg config.Config, cfgErr error, repo gitx.Repo) Check {
	switch {
	case cfgErr != nil:
		return Check{ID: "chores", Status: CheckSkip, Message: "the settings do not load"}
	case len(cfg.Chores) == 0:
		return Check{ID: "chores", Status: CheckSkip, Message: "config.toml sets no chores"}
	}
	var here, elsewhere []string
	for _, ch := range cfg.Chores {
		if fi, err := os.Stat(filepath.Join(ch.Repo, ".git")); err != nil || fi == nil {
			return Check{ID: "chores", Status: CheckWarn, Message: fmt.Sprintf("chore %s runs in %s, which is not a git repository, so it never runs", ch.Name, ch.Repo),
				Hint: "fix the chore's repo in " + filepath.Join(a.ConfigDir(), config.FileName)}
		}
		if repo.Root != "" && ch.RunsIn(repo.Root) {
			here = append(here, fmt.Sprintf("%s (next %s)", ch.Name, ch.Schedule.Next(a.Now().Local()).Format("Mon 01-02 15:04")))
		} else {
			elsewhere = append(elsewhere, ch.Name)
		}
	}
	msg := ""
	if len(here) > 0 {
		msg = "here: " + strings.Join(here, ", ")
		if f, err := lock.Try(repo.Common, watchLock); err == nil {
			f.Close()
			return Check{ID: "chores", Status: CheckWarn, Message: "no tkt watch runs for this repository, so its chores never start: " + strings.Join(here, ", "),
				Hint: "tkt watch --install keeps one running; a tkt command in a herdr pane starts one"}
		}
	}
	if len(elsewhere) > 0 {
		msg = strings.TrimPrefix(msg+"; other repositories: "+strings.Join(elsewhere, ", "), "; ")
	}
	return Check{ID: "chores", Status: CheckOK, Message: msg}
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
