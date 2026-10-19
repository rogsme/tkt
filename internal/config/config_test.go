package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gitlab.com/rogs/tkt/internal/apperr"
)

const presets = `# name  harness   model              serves
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
	c, err := Load(dirWith(t, map[string]string{"presets": presets}))
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

func TestPresetErrors(t *testing.T) {
	for _, src := range []string{"odd vim - *\n", "x claude\n", "x claude - * extra\n", "x claude - *\nx claude - *\n", "# none\n"} {
		_, err := Load(dirWith(t, map[string]string{"presets": src}))
		if code(err) != apperr.ConfigInvalid {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestPickFollowsModelLine(t *testing.T) {
	c, _ := Load(dirWith(t, map[string]string{"presets": presets}))
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
	c, _ := Load(dirWith(t, map[string]string{"presets": presets}))
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
	c, err := Load(dirWith(t, map[string]string{"stages": "# mine\naddress  Address ticket {id} please.\nlint Run the linter.\n"}))
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
	c, _ := Load(dirWith(t, map[string]string{"presets": presets}))
	if got := c.TabLabel("ACM-1", "claude"); got != "ACM-1" {
		t.Errorf("default preset: %q", got)
	}
	if got := c.TabLabel("ACM-1", "fast"); got != "ACM-1 (fast)" {
		t.Errorf("other preset: %q", got)
	}
}
