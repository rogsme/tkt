package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/cron"
)

// Chore is an agent run you schedule in a repository, such as a dependency
// upgrade. Each run is a branch and a pull request with no ticket; that
// repository's tkt watch starts it when its schedule comes round.
type Chore struct {
	Name string `json:"name"`
	// Repo is the repository's primary worktree, as an absolute path.
	Repo     string        `json:"repo"`
	Schedule cron.Schedule `json:"-"`
	// Branch names each run's branch: {name}, {date} and {time} become the
	// chore's name and the run's local date and time.
	Branch string `json:"branch"`
	// As is the preset its agents run on, or "" for the default.
	As     string `json:"as"`
	Prompt string `json:"prompt"`
}

// RunsIn reports whether the chore runs in the repository whose primary
// worktree is root, through any symbolic links.
func (ch Chore) RunsIn(root string) bool {
	a, b := ch.Repo, root
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// DefaultChoreBranch names a chore's runs when its table names none.
const DefaultChoreBranch = "{name}-{date}"

// BranchFor names the branch of a run that starts at t.
func (ch Chore) BranchFor(t time.Time) string {
	return strings.NewReplacer("{name}", ch.Name, "{date}", t.Format("2006-01-02"), "{time}", t.Format("1504")).Replace(ch.Branch)
}

type choreTOML struct {
	Repo     string `toml:"repo"`
	Schedule string `toml:"schedule"`
	Branch   string `toml:"branch"`
	As       string `toml:"as"`
	Prompt   string `toml:"prompt"`
}

var (
	choreName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	// branchName is what a run's branch may hold: worktrunk names the
	// worktree's directory after it, and tkt finds the run by that name.
	branchName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	placeholder = regexp.MustCompile(`\{[^}]*\}`)
)

// parseChores reads the [chores.NAME] tables, sorted by name.
func parseChores(c *Config, path string, in map[string]choreTOML) error {
	for name, t := range in {
		bad := func(format string, args ...any) error {
			return apperr.New(apperr.ConfigInvalid, "%s: chore '%s': %s", path, name, fmt.Sprintf(format, args...))
		}
		if !choreName.MatchString(name) {
			return bad("the name may hold lowercase letters, digits, '_' and '-', starting with a letter or digit")
		}
		ch := Chore{Name: name, Branch: or(t.Branch, DefaultChoreBranch), As: t.As, Prompt: strings.TrimSpace(t.Prompt)}
		switch {
		case t.Repo == "":
			return bad("repo names the repository it runs in, such as \"~/code/acme/app\"")
		case t.Schedule == "":
			return bad("schedule says when it runs, in cron's five fields, such as \"0 9 * * 1,4\"")
		case ch.Prompt == "":
			return bad("prompt is what its agent gets")
		case strings.HasPrefix(ch.Prompt, "!"):
			return bad("prompt starts with '!', which switches the agent to shell mode")
		case strings.HasPrefix(ch.Prompt, "/") && strings.Contains(ch.Prompt, "\n"):
			return bad("prompt runs a slash command, so it takes one line: the agent reads the lines after a command as its arguments")
		case ch.As != "" && !c.Has(ch.As):
			return bad("as names preset '%s', which the presets do not define", ch.As)
		}
		repo, ok := absPath(t.Repo)
		if !ok {
			return bad("repo '%s' is not an absolute path", t.Repo)
		}
		ch.Repo = filepath.Clean(repo)
		s, err := cron.Parse(t.Schedule)
		if err != nil {
			return bad("schedule %v", err)
		}
		ch.Schedule = s
		for _, p := range placeholder.FindAllString(ch.Branch, -1) {
			if p != "{name}" && p != "{date}" && p != "{time}" {
				return bad("branch has %s; it takes {name}, {date} and {time}", p)
			}
		}
		if sample := ch.BranchFor(time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)); !branchName.MatchString(sample) {
			return bad("branch '%s' gives '%s', and a run's branch may hold letters, digits, '-' and '_' only, starting with a letter or digit", ch.Branch, sample)
		}
		c.Chores = append(c.Chores, ch)
	}
	sort.Slice(c.Chores, func(i, j int) bool { return c.Chores[i].Name < c.Chores[j].Name })
	return nil
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
