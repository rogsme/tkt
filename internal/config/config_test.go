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

const settings = `# Presets in order: the first is the default.
[presets.claude]
harness = "claude"
serves = ["frontier"]

[presets.big]
harness = "opencode"
model = "acme/big#high"
serves = ["frontier"]

[presets.fast]
harness = "opencode"
model = "acme/fast#max"
serves = ["small"]
`

const legacyPresets = `# name  harness   model              serves
claude  claude    -                  frontier
big     opencode  acme/big#high      frontier
fast     opencode  acme/fast#max      small
`

func dirWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func code(err error) apperr.Code {
	if err == nil {
		return ""
	}
	return apperr.As(err).Code
}

func TestBuiltinPresets(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := c.PresetNames(); !slices.Equal(got, []string{"claude", "opencode"}) {
		t.Errorf("presets %v", got)
	}
	if p, _ := c.Preset("opencode"); p.Harness != OpenCode {
		t.Errorf("opencode harness %q", p.Harness)
	}
}

func TestUserPresetsKeepHashInModels(t *testing.T) {
	c, err := Load(dirWith(t, map[string]string{"config.toml": settings}))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.PresetNames(); !slices.Equal(got, []string{"claude", "big", "fast"}) {
		t.Errorf("presets %v", got)
	}
	fast, _ := c.Preset("fast")
	if fast.Model != "acme/fast#max" || !slices.Equal(fast.Serves, []string{"small"}) {
		t.Errorf("fast %+v", fast)
	}
	if claude, _ := c.Preset("claude"); claude.Model != "" {
		t.Errorf("claude model %q", claude.Model)
	}
}

func TestSettingsErrors(t *testing.T) {
	for _, src := range []string{
		"[presets.odd]\nharness = \"vim\"\n",
		"[presets.x]\nharness = \"claude\"\nmodle = \"opus\"\n",
		"[presets.x]\nharness = \"opencode\"\nmodel = \"fast\"\n",
		"[presets.x]\nharness = \"claude\"\nserves = []\n",
		"[presets.\"-x\"]\nharness = \"claude\"\n",
		"[stages]\nlint = \"  \"\n",
		"[stages]\nLint = \"Run it.\"\n",
		"[presets.x\nharness = \"claude\"\n",
		"color = true\n",
	} {
		_, err := Load(dirWith(t, map[string]string{"config.toml": src}))
		if code(err) != apperr.ConfigInvalid {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestPresetOrderFollowsTheFile(t *testing.T) {
	src := "[presets.zeta]\nharness = \"opencode\"\n\n[presets.alpha]\nharness = \"claude\"\n"
	c, err := Load(dirWith(t, map[string]string{"config.toml": src}))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.PresetNames(); !slices.Equal(got, []string{"zeta", "alpha"}) {
		t.Errorf("order %v", got)
	}
	if p := c.Default(); p.Name != "zeta" || !slices.Equal(p.Serves, []string{"*"}) {
		t.Errorf("default %+v", p)
	}
}

func TestStagesOnlyKeepBuiltinPresets(t *testing.T) {
	c, err := Load(dirWith(t, map[string]string{"config.toml": "[stages]\nlint = \"\"\"\nRun the linter\non {id}.\n\"\"\"\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.PresetNames(); !slices.Equal(got, []string{"claude", "opencode"}) {
		t.Errorf("presets %v", got)
	}
	if got, _ := c.StageLine("lint", "ACM-1"); got != "Run the linter\non ACM-1." {
		t.Errorf("lint: %q", got)
	}
}

func TestLegacyFilesAreConverted(t *testing.T) {
	dir := dirWith(t, map[string]string{"presets": legacyPresets, "stages": "lint Run \"the\" linter on {id}.\n"})
	_, err := Load(dir)
	e := apperr.As(err)
	if e.Code != apperr.ConfigLegacy {
		t.Fatalf("got %v", err)
	}
	text, _ := e.Details["config_toml"].(string)
	if !strings.Contains(e.Msg, text) || !strings.Contains(text, "[presets.fast]") {
		t.Errorf("message:\n%s", e.Msg)
	}
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(text), 0o644)
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("the converted file does not load: %v\n%s", err, text)
	}
	if got := c.PresetNames(); !slices.Equal(got, []string{"claude", "big", "fast"}) {
		t.Errorf("presets %v", got)
	}
	if fast, _ := c.Preset("fast"); fast.Model != "acme/fast#max" || !slices.Equal(fast.Serves, []string{"small"}) {
		t.Errorf("fast %+v", fast)
	}
	if got, _ := c.StageLine("lint", "ACM-1"); got != `Run "the" linter on ACM-1.` {
		t.Errorf("lint %q", got)
	}
	if len(c.IgnoredLegacy) != 2 {
		t.Errorf("ignored %v", c.IgnoredLegacy)
	}
}

func TestPickFollowsModelLine(t *testing.T) {
	c, _ := Load(dirWith(t, map[string]string{"config.toml": settings}))
	cases := []struct{ as, prompt, want string }{
		{"", "- Model: `small` (acme/fast)", "fast"},
		{"", "Model: frontier.", "claude"},
		{"", "no model", "claude"},
		{"big", "Model: frontier", "big"},
	}
	for _, tc := range cases {
		p, err := c.Pick(tc.as, tc.prompt, false)
		if err != nil || p.Name != tc.want {
			t.Errorf("Pick(%q, %q) = %s, %v; want %s", tc.as, tc.prompt, p.Name, err, tc.want)
		}
	}
}

func TestPickRefusesConflicts(t *testing.T) {
	c, _ := Load(dirWith(t, map[string]string{"config.toml": settings}))
	if _, err := c.Pick("claude", "Model: small", false); code(err) != apperr.PresetModelMismatch {
		t.Errorf("--as against the Model line: %v", err)
	}
	if _, err := c.Pick("", "Model: small\nModel: frontier", false); code(err) != apperr.AmbiguousModel {
		t.Errorf("two Model values: %v", err)
	}
	if _, err := c.Pick("", "Model: huge", false); code(err) != apperr.NoPresetForModel {
		t.Errorf("nothing serves the value: %v", err)
	}
	if _, err := c.Pick("nope", "", false); code(err) != apperr.UnknownPreset {
		t.Errorf("unknown preset: %v", err)
	}
	if p, err := c.Pick("claude", "Model: small", true); err != nil || p.Name != "claude" {
		t.Errorf("--force: %s, %v", p.Name, err)
	}
}

func TestModelValues(t *testing.T) {
	if got := ModelValues("Model: frontier.\n- `Model: frontier`, not small"); !slices.Equal(got, []string{"frontier"}) {
		t.Errorf("got %v", got)
	}
	if got := ModelValues("nothing"); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestStageLinesAndOverrides(t *testing.T) {
	c, _ := Load(t.TempDir())
	if got, _ := c.StageLine("start", "ACM-7"); got != "Use the start-ticket skill to start ticket ACM-7." {
		t.Errorf("start: %q", got)
	}
	c, err := Load(dirWith(t, map[string]string{"config.toml": "[stages]\naddress = \"Address ticket {id} please.\"\nlint = \"Run the linter.\"\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c.StageLine("address", "ACM-7"); got != "Address ticket ACM-7 please." {
		t.Errorf("address: %q", got)
	}
	if got, _ := c.StageLine("lint", "ACM-7"); got != "Run the linter." {
		t.Errorf("lint: %q", got)
	}
	_, err = c.StageLine("adress", "ACM-7")
	if code(err) != apperr.UnknownStage || apperr.As(err).Hint != "did you mean address?" {
		t.Errorf("unknown stage: %v (hint %q)", err, apperr.As(err).Hint)
	}
}

func TestTabLabel(t *testing.T) {
	c, _ := Load(dirWith(t, map[string]string{"config.toml": settings}))
	if got := c.TabLabel("ACM-1", "claude"); got != "ACM-1 (claude)" {
		t.Errorf("default preset: %q", got)
	}
	if got := c.TabLabel("ACM-1", "fast"); got != "ACM-1 (fast)" {
		t.Errorf("other preset: %q", got)
	}
}

func TestAutoReviewAndBoardSettings(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Auto != (Auto{MaxRounds: 5, Poll: 30 * time.Second, MaxParallel: 3}) || c.Review.BotWait != 15*time.Minute || c.Board.StaleAfter != 2*time.Hour {
		t.Errorf("defaults: %+v %+v %+v", c.Auto, c.Review, c.Board)
	}
	c, err = Load(dirWith(t, map[string]string{FileName: "[auto]\nmax_rounds = 0\npoll = \"1m\"\nmax_parallel = 0\n\n[review]\nbot_wait = \"0s\"\n\n[board]\nstale_after = \"30m\"\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Auto != (Auto{MaxRounds: 0, Poll: time.Minute, MaxParallel: 0}) || c.Review.BotWait != 0 || c.Board.StaleAfter != 30*time.Minute {
		t.Errorf("set: %+v %+v %+v", c.Auto, c.Review, c.Board)
	}
	for _, bad := range []string{
		"[auto]\nmax_rounds = -1\n",
		"[auto]\npoll = \"0s\"\n",
		"[auto]\npoll = \"soon\"\n",
		"[auto]\nmax_parallel = -2\n",
		"[review]\nbot_wait = \"-5m\"\n",
		"[review]\nbot_wait = 15\n",
		"[board]\nstale = \"2h\"\n",
	} {
		if _, err := Load(dirWith(t, map[string]string{FileName: bad})); code(err) != apperr.ConfigInvalid {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestWatchSettings(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Watch != (Watch{Interval: time.Minute, DoneAfterMerge: true, Autostart: true}) {
		t.Errorf("defaults: %+v", c.Watch)
	}
	c, err = Load(dirWith(t, map[string]string{FileName: "[watch]\ninterval = \"5m\"\ndone_after_merge = false\nautostart = false\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Watch != (Watch{Interval: 5 * time.Minute}) {
		t.Errorf("set: %+v", c.Watch)
	}
	for _, bad := range []string{"[watch]\ninterval = \"0s\"\n", "[watch]\ninterval = \"often\"\n", "[watch]\nautostart = \"yes\"\n", "[watch]\nevery = \"1m\"\n"} {
		if _, err := Load(dirWith(t, map[string]string{FileName: bad})); code(err) != apperr.ConfigInvalid {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSuggestSettings(t *testing.T) {
	c, err := Load(dirWith(t, map[string]string{FileName: "[presets.claude]\nharness = \"claude\"\n\n[presets.cheap]\nharness = \"claude\"\nmodel = \"haiku\"\n\n[suggest]\nas = \"cheap\"\n"}))
	if err != nil || c.Suggest.As != "cheap" {
		t.Fatalf("[suggest] as: %+v, %v", c.Suggest, err)
	}
	// A preset the file does not define is a mistake, caught at load.
	if _, err := Load(dirWith(t, map[string]string{FileName: "[suggest]\nas = \"fast\"\n"})); code(err) != apperr.ConfigInvalid {
		t.Errorf("an unknown [suggest] preset: %v", err)
	}
	if got := SuggestLine(1); got != "Use the suggest-tickets skill to suggest the next ticket to start." {
		t.Errorf("SuggestLine(1) = %q", got)
	}
	if got := SuggestRequestLine("five tickets tagged feedback"); got != "Use the suggest-tickets skill for this request: five tickets tagged feedback" {
		t.Errorf("SuggestRequestLine = %q", got)
	}
}

func TestMergeLineNamesEveryStep(t *testing.T) {
	for _, tc := range []struct {
		steps []int
		want  string
	}{
		{[]int{1}, "step 1"},
		{[]int{1, 2}, "steps 1 and 2"},
		{[]int{1, 2, 3}, "steps 1 to 3"},
		{[]int{1, 2, 4}, "steps 1, 2 and 4"},
	} {
		if got := StepsText(tc.steps); got != tc.want {
			t.Errorf("StepsText(%v) = %q, want %q", tc.steps, got, tc.want)
		}
	}
	if got, want := MergeLine([]int{1, 2, 3}, "aaa", "aaa"), "Use the finish-ticket skill to merge the PR. Human verification steps 1 to 3 passed on head aaa."; got != want {
		t.Errorf("MergeLine = %q, want %q", got, want)
	}
	if got, want := MergeLine([]int{1}, "aaa", "bbb"), "Use the finish-ticket skill to merge the PR. Human verification step 1 passed on head aaa. The pull request is at bbb now, after clean merges of the base."; got != want {
		t.Errorf("MergeLine after a sync = %q, want %q", got, want)
	}
}
