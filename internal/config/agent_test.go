package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gitlab.com/rogs/tkt/internal/apperr"
)

func TestAgentSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c, err := Load(dirWith(t, map[string]string{FileName: `[agent]
model = "acme/large"
extensions = ["/opt/acme/provider.ts", "~/.pi/agent/extensions/acme.ts"]
`}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.Model != "acme/large" {
		t.Errorf("model %q", c.Agent.Model)
	}
	want := []string{"/opt/acme/provider.ts", filepath.Join(home, ".pi/agent/extensions/acme.ts")}
	if !slices.Equal(c.Agent.Extensions, want) {
		t.Errorf("extensions %q, want %q", c.Agent.Extensions, want)
	}
	if got := c.PresetNames(); !slices.Equal(got, []string{"claude", "opencode"}) {
		t.Errorf("an [agent] table changed the presets: %v", got)
	}
}

func TestAgentSettingsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"relative extension": "[agent]\nextensions = [\"acme.ts\"]\n",
		"bare tilde":         "[agent]\nextensions = [\"~acme/x.ts\"]\n",
		"unknown key":        "[agent]\nmodle = \"acme/large\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(dirWith(t, map[string]string{FileName: body}))
			if code(err) != apperr.ConfigInvalid {
				t.Fatalf("Load = %v, want config_invalid", err)
			}
			if name == "unknown key" && !strings.Contains(err.Error(), "agent.modle") {
				t.Errorf("message %q does not name the key", err)
			}
		})
	}
}
