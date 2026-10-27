package config

import (
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/cron"
)

// Chore is an agent run a repository's .tkt/tkt.local.toml schedules, such as a
// dependency upgrade. Each run is a branch, and a pull request unless PR is
// false, with no ticket; that repository's tkt watch starts it when its
// schedule comes round.
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
	// PR says its runs end in a pull request. A chore whose work lands
	// elsewhere, such as a tag and a release, sets pr = false.
	PR bool `json:"pr"`
}

// DefaultChoreBranch names a chore's runs when its table names none.
const DefaultChoreBranch = "{name}-{date}"

// BranchFor names the branch of a run that starts at t.
func (ch Chore) BranchFor(t time.Time) string {
	return strings.NewReplacer("{name}", ch.Name, "{date}", t.Format("2006-01-02"), "{time}", t.Format("1504")).Replace(ch.Branch)
}

// StatusPage is a service's status page that tkt watch reads, such as
// GitHub's: an incident there holds its agent runs until it ends.
type StatusPage struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// Components are the parts of the service that count, such as Actions;
	// none means the whole page does.
	Components []string `json:"components"`
}

type choreTOML struct {
	Repo     string `toml:"repo"`
	Schedule string `toml:"schedule"`
	Branch   string `toml:"branch"`
	As       string `toml:"as"`
	Prompt   string `toml:"prompt"`
	PR       *bool  `toml:"pr"`
}

type statusTOML struct {
	Page       string   `toml:"page"`
	Components []string `toml:"components"`
}

var (
	choreName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	// branchName is what a run's branch may hold: worktrunk names the
	// worktree's directory after it, and tkt finds the run by that name.
	branchName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	placeholder = regexp.MustCompile(`\{[^}]*\}`)
)

// parseChores reads the [chores.NAME] tables of the repository in root,
// sorted by name.
func parseChores(c *Config, path, root string, in map[string]choreTOML) error {
	for name, t := range in {
		bad := func(format string, args ...any) error {
			return apperr.New(apperr.ConfigInvalid, "%s: chore '%s': %s", path, name, fmt.Sprintf(format, args...))
		}
		if !choreName.MatchString(name) {
			return bad("the name may hold lowercase letters, digits, '_' and '-', starting with a letter or digit")
		}
		ch := Chore{Name: name, Repo: filepath.Clean(root), Branch: or(t.Branch, DefaultChoreBranch), As: t.As, Prompt: strings.TrimSpace(t.Prompt), PR: t.PR == nil || *t.PR}
		switch {
		case t.Repo != "":
			return bad("it runs in this repository, so it takes no repo; drop the key")
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

// choresMoved refuses the [chores.NAME] tables of config.toml, which older
// versions read with a repo key, and prints each repository's tables to
// move to its .tkt/tkt.local.toml.
func choresMoved(path string, in map[string]choreTOML) error {
	byRepo := map[string][]string{}
	for name, t := range in {
		byRepo[t.Repo] = append(byRepo[t.Repo], name)
	}
	repos := slices.Sorted(maps.Keys(byRepo))
	files := map[string]string{}
	var b strings.Builder
	for _, repo := range repos {
		names := byRepo[repo]
		sort.Strings(names)
		var t strings.Builder
		for i, name := range names {
			ch := in[name]
			if i > 0 {
				t.WriteString("\n")
			}
			fmt.Fprintf(&t, "[chores.%s]\n", tomlKey(name))
			for _, kv := range [][2]string{{"schedule", ch.Schedule}, {"branch", ch.Branch}, {"as", ch.As}, {"prompt", strings.TrimSpace(ch.Prompt)}} {
				if kv[1] != "" {
					fmt.Fprintf(&t, "%s = %s\n", kv[0], tomlString(kv[1]))
				}
			}
		}
		file := LocalPath(repo)
		files[file] = t.String()
		fmt.Fprintf(&b, "\n%s:\n\n%s", file, t.String())
	}
	return apperr.New(apperr.ConfigChoresMoved,
		"%s holds chores, and tkt reads them from each repository's %s now; move these tables there, then delete them from %s:\n%s",
		path, filepath.Join(ProjectDir, LocalFile), path, b.String()).
		WithHint("move each [chores.NAME] table to its repository's %s, then delete it from %s", filepath.Join(ProjectDir, LocalFile), path).
		With("tkt_local_toml", files)
}

// parseStatus reads the [status.NAME] tables, sorted by name. A page the
// file names again replaces the one read before.
func parseStatus(c *Config, path string, in map[string]statusTOML) error {
	for name, t := range in {
		bad := func(format string, args ...any) error {
			return apperr.New(apperr.ConfigInvalid, "%s: status page '%s': %s", path, name, fmt.Sprintf(format, args...))
		}
		u, err := url.Parse(t.Page)
		switch {
		case !presetName.MatchString(name):
			return bad("the name may hold letters, digits, '.', '_' and '-', starting with a letter or digit")
		case t.Page == "":
			return bad("page is the status page's address, such as \"https://www.githubstatus.com\"")
		case err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "file") || u.Host == "" && u.Scheme != "file":
			return bad("page '%s' is not an http or https address", t.Page)
		case slices.Contains(t.Components, ""):
			return bad("components holds an empty name")
		}
		c.Status = slices.DeleteFunc(c.Status, func(p StatusPage) bool { return p.Name == name })
		c.Status = append(c.Status, StatusPage{Name: name, URL: strings.TrimRight(t.Page, "/"), Components: t.Components})
	}
	sort.Slice(c.Status, func(i, j int) bool { return c.Status[i].Name < c.Status[j].Name })
	return nil
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
