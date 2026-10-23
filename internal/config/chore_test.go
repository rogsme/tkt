package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
)

func TestChores(t *testing.T) {
	home, _ := os.UserHomeDir()
	src := `[presets.claude]
harness = "claude"

[presets.fast]
harness = "opencode"
model = "acme/fast"

[chores.deps]
repo = "~/code/acme/app"
schedule = "0 9 * * 1,4"
branch = "deps-{date}"
as = "fast"
prompt = """
Upgrade the dependencies.
"""

[chores.changelog]
repo = "/srv/acme/app/"
schedule = "@daily"
prompt = "Gather the changelog lines."
`
	c, err := Load(dirWith(t, map[string]string{FileName: src}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Chores) != 2 || c.Chores[0].Name != "changelog" || c.Chores[1].Name != "deps" {
		t.Fatalf("chores %+v", c.Chores)
	}
	deps, changelog := c.Chores[1], c.Chores[0]
	if deps.Repo != filepath.Join(home, "code/acme/app") || deps.As != "fast" || deps.Prompt != "Upgrade the dependencies." || deps.Schedule.String() != "0 9 * * 1,4" {
		t.Errorf("deps %+v", deps)
	}
	if changelog.Repo != "/srv/acme/app" || changelog.Branch != DefaultChoreBranch {
		t.Errorf("changelog %+v", changelog)
	}
	at := time.Date(2026, 10, 22, 9, 5, 0, 0, time.UTC)
	if got := deps.BranchFor(at); got != "deps-2026-10-22" {
		t.Errorf("deps branch %q", got)
	}
	if got := changelog.BranchFor(at); got != "changelog-2026-10-22" {
		t.Errorf("changelog branch %q", got)
	}
	if got := (Chore{Name: "x", Branch: "{name}-{date}-{time}"}).BranchFor(at); got != "x-2026-10-22-0905" {
		t.Errorf("{time} %q", got)
	}
}

func TestChoreErrors(t *testing.T) {
	base := "[chores.deps]\nrepo = \"/srv/app\"\nschedule = \"0 9 * * 1\"\nprompt = \"Upgrade.\"\n"
	for src, want := range map[string]string{
		"[chores.Deps]\nrepo = \"/srv/app\"\nschedule = \"0 9 * * 1\"\nprompt = \"Upgrade.\"\n":  "the name",
		"[chores.deps]\nschedule = \"0 9 * * 1\"\nprompt = \"Upgrade.\"\n":                       "repo names",
		"[chores.deps]\nrepo = \"app\"\nschedule = \"0 9 * * 1\"\nprompt = \"Upgrade.\"\n":       "not an absolute path",
		"[chores.deps]\nrepo = \"/srv/app\"\nprompt = \"Upgrade.\"\n":                            "schedule says",
		"[chores.deps]\nrepo = \"/srv/app\"\nschedule = \"0 25 * * 1\"\nprompt = \"Upgrade.\"\n": "hour",
		"[chores.deps]\nrepo = \"/srv/app\"\nschedule = \"0 9 * * 1\"\nprompt = \"  \"\n":        "prompt is",
		base + "as = \"fast\"\n":            "preset 'fast'",
		base + "branch = \"deps/{date}\"\n": "gives 'deps/2026-01-02'",
		base + "branch = \"deps-{day}\"\n":  "branch has {day}",
		base + "every = \"1h\"\n":           "unknown key",
		"[chores.deps]\nrepo = \"/srv/app\"\nschedule = \"0 9 * * 1\"\nprompt = \"!npm update\"\n":               "shell mode",
		"[chores.deps]\nrepo = \"/srv/app\"\nschedule = \"0 9 * * 1\"\nprompt = \"\"\"\n/upgrade\nnow\n\"\"\"\n": "takes one line",
	} {
		_, err := Load(dirWith(t, map[string]string{FileName: src}))
		if code(err) != apperr.ConfigInvalid || !strings.Contains(apperr.As(err).Msg, want) {
			t.Errorf("%q: %v, want an error about %s", src, err, want)
		}
	}
}
