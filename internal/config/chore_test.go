package config

import (
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
)

func TestChores(t *testing.T) {
	global := `[presets.claude]
harness = "claude"

[presets.fast]
harness = "opencode"
model = "acme/fast"
`
	project := `[chores.deps]
schedule = "0 9 * * 1,4"
branch = "deps-{date}"
as = "fast"
prompt = """
Upgrade the dependencies.
"""

[chores.changelog]
schedule = "@daily"
pr = false
prompt = "Gather the changelog lines."
`
	root := repoWith(t, "", project)
	c, err := Load(dirWith(t, map[string]string{FileName: global}), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Chores) != 2 || c.Chores[0].Name != "changelog" || c.Chores[1].Name != "deps" {
		t.Fatalf("chores %+v", c.Chores)
	}
	deps, changelog := c.Chores[1], c.Chores[0]
	if deps.Repo != root || deps.As != "fast" || deps.Prompt != "Upgrade the dependencies." || deps.Schedule.String() != "0 9 * * 1,4" {
		t.Errorf("deps %+v", deps)
	}
	if !deps.PR || changelog.PR {
		t.Errorf("pr: deps %v, changelog %v; want true, false", deps.PR, changelog.PR)
	}
	if changelog.Repo != root || changelog.Branch != DefaultChoreBranch {
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
	base := "[chores.deps]\nschedule = \"0 9 * * 1\"\nprompt = \"Upgrade.\"\n"
	for src, want := range map[string]string{
		"[chores.Deps]\nschedule = \"0 9 * * 1\"\nprompt = \"Upgrade.\"\n":                  "the name",
		base + "repo = \"/srv/app\"\n":                                                      "takes no repo",
		"[chores.deps]\nprompt = \"Upgrade.\"\n":                                            "schedule says",
		"[chores.deps]\nschedule = \"0 25 * * 1\"\nprompt = \"Upgrade.\"\n":                 "hour",
		"[chores.deps]\nschedule = \"0 9 * * 1\"\nprompt = \"  \"\n":                        "prompt is",
		base + "as = \"fast\"\n":                                                            "preset 'fast'",
		base + "branch = \"deps/{date}\"\n":                                                 "gives 'deps/2026-01-02'",
		base + "branch = \"deps-{day}\"\n":                                                  "branch has {day}",
		base + "every = \"1h\"\n":                                                           "unknown key",
		"[chores.deps]\nschedule = \"0 9 * * 1\"\nprompt = \"!npm update\"\n":               "shell mode",
		"[chores.deps]\nschedule = \"0 9 * * 1\"\nprompt = \"\"\"\n/upgrade\nnow\n\"\"\"\n": "takes one line",
	} {
		root := repoWith(t, "", src)
		_, err := Load(t.TempDir(), root)
		if code(err) != apperr.ConfigInvalid || !strings.Contains(apperr.As(err).Msg, want) {
			t.Errorf("%q: %v, want an error about %s", src, err, want)
		}
	}
}

func TestGlobalChoresPrintTheTablesToMove(t *testing.T) {
	global := `[presets.fast]
harness = "opencode"

[chores.deps]
repo = "~/code/acme/app"
schedule = "0 9 * * 1,4"
branch = "deps-{date}"
as = "fast"
prompt = """
Upgrade the "dependencies".
Note the breaking changes.
"""

[chores.changelog]
repo = "~/code/acme/app"
schedule = "@daily"
prompt = "Gather the changelog lines."

[chores.lint]
repo = "/srv/acme/api"
schedule = "0 3 * * *"
prompt = "Fix the lint warnings."
`
	dir := dirWith(t, map[string]string{FileName: global})
	_, err := Load(dir, "")
	e := apperr.As(err)
	if e == nil || e.Code != apperr.ConfigChoresMoved {
		t.Fatalf("Load = %v, want config_chores_moved", err)
	}
	files, _ := e.Details["tkt_local_toml"].(map[string]string)
	if len(files) != 2 {
		t.Fatalf("tkt_local_toml %v", e.Details["tkt_local_toml"])
	}
	for file, want := range map[string][]string{
		"~/code/acme/app/.tkt/tkt.local.toml": {"changelog", "deps"},
		"/srv/acme/api/.tkt/tkt.local.toml":   {"lint"},
	} {
		text, ok := files[file]
		if !ok || !strings.Contains(e.Msg, file+":\n\n"+text) {
			t.Errorf("%s: missing from %q", file, e.Msg)
			continue
		}
		root := repoWith(t, "", text)
		c, err := Load(dirWith(t, map[string]string{FileName: "[presets.fast]\nharness = \"opencode\"\n"}), root)
		if err != nil {
			t.Fatalf("%s does not load: %v\n%s", file, err, text)
		}
		var names []string
		for _, ch := range c.Chores {
			names = append(names, ch.Name)
		}
		if !slices.Equal(names, want) {
			t.Errorf("%s: chores %v, want %v", file, names, want)
		}
		if file == "~/code/acme/app/.tkt/tkt.local.toml" {
			if d := c.Chores[1]; d.As != "fast" || d.Branch != "deps-{date}" || d.Prompt != "Upgrade the \"dependencies\".\nNote the breaking changes." {
				t.Errorf("deps %+v", d)
			}
		}
	}
}

func TestStatusPages(t *testing.T) {
	src := "[status.github]\npage = \"https://www.githubstatus.com/\"\ncomponents = [\"Actions\", \"Pull Requests\"]\n\n[status.anthropic]\npage = \"https://status.claude.com\"\n"
	c, err := Load(dirWith(t, map[string]string{FileName: src}), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Status) != 2 || c.Status[0].Name != "anthropic" || c.Status[1].URL != "https://www.githubstatus.com" || !slices.Equal(c.Status[1].Components, []string{"Actions", "Pull Requests"}) {
		t.Errorf("status %+v", c.Status)
	}
	for _, bad := range []string{
		"[status.github]\n",
		"[status.github]\npage = \"www.githubstatus.com\"\n",
		"[status.github]\npage = \"ftp://example.com\"\n",
		"[status.github]\npage = \"https://www.githubstatus.com\"\ncomponents = [\"\"]\n",
		"[status.github]\npage = \"https://www.githubstatus.com\"\nservices = [\"Actions\"]\n",
	} {
		if _, err := Load(dirWith(t, map[string]string{FileName: bad}), ""); code(err) != apperr.ConfigInvalid {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
