package config

import (
	"os"
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

// repoWith makes a primary worktree whose .tkt directory holds the shared
// and local settings files given, leaving out an empty one.
func repoWith(t *testing.T, shared, local string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ProjectDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{SharedPath(root): shared, LocalPath(root): local} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

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
	root := repoWith(t, project, "")
	c, err := Load(dirWith(t, map[string]string{FileName: globalSettings}), root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Shared != SharedPath(root) || c.Local != "" || !strings.HasSuffix(c.Source, FileName) {
		t.Errorf("shared %q, local %q, source %q", c.Shared, c.Local, c.Source)
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

func TestLocalSettingsWinOverShared(t *testing.T) {
	shared := "[stages]\nlint = \"Run just lint on {id}.\"\n\n[auto]\nmax_rounds = 7\n"
	local := "[stages]\nlint = \"Run my linter on {id}.\"\n\n[chores.deps]\nschedule = \"@daily\"\nprompt = \"Upgrade.\"\n"
	root := repoWith(t, shared, local)
	c, err := Load(dirWith(t, map[string]string{FileName: globalSettings}), root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Shared != SharedPath(root) || c.Local != LocalPath(root) {
		t.Errorf("shared %q, local %q", c.Shared, c.Local)
	}
	if c.Stages["lint"] != "Run my linter on {id}." || c.Auto.MaxRounds != 7 || c.Auto.MaxParallel != 2 {
		t.Errorf("lint %q, auto %+v", c.Stages["lint"], c.Auto)
	}
	if len(c.Chores) != 1 || c.Chores[0].Repo != root {
		t.Errorf("chores %+v", c.Chores)
	}
	c, err = Load(t.TempDir(), repoWith(t, "", "[review]\nbot_wait = \"0s\"\n"))
	if err != nil || c.Shared != "" || c.Local == "" || c.Review.BotWait != 0 {
		t.Errorf("local alone: %v, %+v", err, c.Review)
	}
}

func TestNoProjectFile(t *testing.T) {
	dir := dirWith(t, map[string]string{FileName: globalSettings})
	for _, root := range []string{"", t.TempDir()} {
		c, err := Load(dir, root)
		if err != nil {
			t.Fatal(err)
		}
		if c.Shared != "" || c.Local != "" || c.Review.BotWait != 10*time.Minute || c.Stages["lint"] != "Run the linter on {id}." {
			t.Errorf("root %q: shared %q, local %q, review %+v", root, c.Shared, c.Local, c.Review)
		}
	}
	c, err := Load(t.TempDir(), repoWith(t, "[watch]\nautostart = false\n", ""))
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
		"[presets.big]\nharness = \"claude\"\n": "[presets] belongs in " + filepath.Join(dir, FileName),
		"[agent]\nmodel = \"acme/small\"\n":     "[agent] belongs in " + filepath.Join(dir, FileName),
		"[review]\nbot_wiat = \"0s\"\n":         "unknown key review.bot_wiat",
		"[verify]\nas = \"big\"\n":              "names preset 'big'",
		"[stages]\nlint = \"\"\n":               "stage 'lint' has no text",
	} {
		for _, local := range []bool{false, true} {
			root := repoWith(t, src, "")
			path := SharedPath(root)
			if local {
				root = repoWith(t, "", src)
				path = LocalPath(root)
			}
			_, err := Load(dir, root)
			e := apperr.As(err)
			if e == nil || e.Code != apperr.ConfigInvalid || !strings.Contains(e.Msg, path) || !strings.Contains(e.Msg, want) {
				t.Errorf("%s %q: %v, want an error about %s", path, src, err, want)
			}
		}
	}
}

func TestSharedFileRefusesChores(t *testing.T) {
	root := repoWith(t, "[chores.deps]\nschedule = \"@daily\"\nprompt = \"Upgrade.\"\n", "")
	_, err := Load(t.TempDir(), root)
	e := apperr.As(err)
	if e == nil || e.Code != apperr.ConfigInvalid || !strings.Contains(e.Msg, SharedPath(root)+": [chores] belongs in "+LocalPath(root)) {
		t.Errorf("Load = %v", err)
	}
}
