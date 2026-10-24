package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
)

const globalSettings = `[presets.claude]
harness = "claude"

[presets.fast]
harness = "opencode"

[stages]
lint = "Run the linter on {id}."
docs = "Update the docs for {id}."

[review]
bot_wait = "10m"

[auto]
max_rounds = 4
max_parallel = 2

[status.github]
page = "https://www.githubstatus.com"
components = ["Actions"]

[agent]
model = "acme/large"
`

func TestProjectSettingsWinKeyByKey(t *testing.T) {
	project := `[stages]
lint = "Run just lint on {id}."
ci = "Rerun the checks."

[review]
bot_wait = "0s"

[auto]
max_rounds = 7

[suggest]
as = "fast"

[status.github]
page = "https://status.example.com"

[status.vercel]
page = "https://www.vercel-status.com"
`
	root := dirWith(t, map[string]string{ProjectFileName: project})
	c, err := Load(dirWith(t, map[string]string{FileName: globalSettings}), root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != filepath.Join(root, ProjectFileName) || !strings.HasSuffix(c.Source, FileName) {
		t.Errorf("project %q, source %q", c.Project, c.Source)
	}
	if c.Review.BotWait != 0 || c.Auto.MaxRounds != 7 || c.Auto.MaxParallel != 2 || c.Auto.Poll != DefaultPoll {
		t.Errorf("review %+v, auto %+v", c.Review, c.Auto)
	}
	if c.Stages["lint"] != "Run just lint on {id}." || c.Stages["docs"] != "Update the docs for {id}." || c.Stages["ci"] != "Rerun the checks." {
		t.Errorf("stages %v", c.Stages)
	}
	if !slices.Equal(c.OwnStages, []string{"ci", "docs", "lint"}) {
		t.Errorf("own stages %v", c.OwnStages)
	}
	if c.Suggest.As != "fast" || c.Agent.Model != "acme/large" || !slices.Equal(c.PresetNames(), []string{"claude", "fast"}) {
		t.Errorf("suggest %+v, agent %+v, presets %v", c.Suggest, c.Agent, c.PresetNames())
	}
	if len(c.Status) != 2 || c.Status[0].URL != "https://status.example.com" || c.Status[0].Components != nil || c.Status[1].Name != "vercel" {
		t.Errorf("status %+v", c.Status)
	}
}

func TestNoProjectFile(t *testing.T) {
	dir := dirWith(t, map[string]string{FileName: globalSettings})
	for _, root := range []string{"", t.TempDir()} {
		c, err := Load(dir, root)
		if err != nil {
			t.Fatal(err)
		}
		if c.Project != "" || c.Review.BotWait != 10*time.Minute || c.Stages["lint"] != "Run the linter on {id}." {
			t.Errorf("root %q: project %q, review %+v", root, c.Project, c.Review)
		}
	}
	c, err := Load(t.TempDir(), dirWith(t, map[string]string{ProjectFileName: "[watch]\nautostart = false\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Watch.Autostart || c.Source != "built-in presets" {
		t.Errorf("without config.toml: watch %+v, source %q", c.Watch, c.Source)
	}
}

func TestProjectFileRefusesPersonalTables(t *testing.T) {
	dir := dirWith(t, map[string]string{FileName: globalSettings})
	for src, want := range map[string]string{
		"[presets.big]\nharness = \"claude\"\n":  "[presets] belongs in " + filepath.Join(dir, FileName),
		"[agent]\nmodel = \"acme/small\"\n":      "[agent] belongs in " + filepath.Join(dir, FileName),
		"[review]\nbot_wiat = \"0s\"\n":          "unknown key review.bot_wiat",
		"[verify]\nas = \"big\"\n":               "names preset 'big'",
		"[stages]\nlint = \"\"\n":                "stage 'lint' has no text",
		"[chores.deps]\nschedule = \"@daily\"\n": "[chores] belongs in " + filepath.Join(dir, FileName),
	} {
		root := dirWith(t, map[string]string{ProjectFileName: src})
		_, err := Load(dir, root)
		e := apperr.As(err)
		if e == nil || e.Code != apperr.ConfigInvalid || !strings.Contains(e.Msg, filepath.Join(root, ProjectFileName)) || !strings.Contains(e.Msg, want) {
			t.Errorf("%q: %v, want an error about %s", src, err, want)
		}
	}
}
