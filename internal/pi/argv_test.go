package pi

import (
	"slices"
	"testing"
)

func TestArgv(t *testing.T) {
	isolated := []string{"/opt/pi/pi",
		"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes",
		"--no-context-files", "--no-mcp", "--no-builtin-tools",
		"-e", "/data/ext/abc/index.ts", "--system-prompt", "/data/ext/abc/system.md",
		"--theme", "/data/ext/abc/tkt-dark.json", "--theme", "/data/ext/abc/tkt-light.json"}
	if got := Argv("/opt/pi/pi", "/data/ext/abc", Launch{}); !slices.Equal(got, isolated) {
		t.Errorf("Argv = %q", got)
	}
	got := Argv("/opt/pi/pi", "/data/ext/abc", Launch{Model: "acme/large", Extensions: []string{"/a.ts", "/b.ts"}, Continue: true, Theme: Theme})
	want := append(slices.Clone(isolated), "--use-theme", "tkt-light/tkt-dark", "--model", "acme/large", "-e", "/a.ts", "-e", "/b.ts", "-c")
	if !slices.Equal(got, want) {
		t.Errorf("Argv = %q\nwant %q", got, want)
	}
	got = Argv("/opt/pi/pi", "/data/ext/abc", Launch{Session: "01a1", Args: []string{"--thinking", "high"}})
	want = append(slices.Clone(isolated), "--session", "01a1", "--thinking", "high")
	if !slices.Equal(got, want) {
		t.Errorf("Argv = %q\nwant %q", got, want)
	}
}

func TestEnv(t *testing.T) {
	base := []string{"HOME=/home/acme", "PI_TELEMETRY=1", "PI_CODING_AGENT_DIR=/home/acme/.pi/agent",
		"PI_CODING_AGENT_SESSION_DIR=/s", "PI_PACKAGE_DIR=/p", "ANTHROPIC_API_KEY=k", "TKT_BIN=/old", "TKT_VERSION=v0.1.0"}
	got := Env(base, Host{AgentDir: "/home/acme/.config/tkt/pi", Bin: "/usr/bin/tkt", Version: "v0.6.0", PiVersion: "1.3.0"})
	want := []string{"HOME=/home/acme", "ANTHROPIC_API_KEY=k",
		"PI_CODING_AGENT_DIR=/home/acme/.config/tkt/pi", "PI_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0", "TKT_BIN=/usr/bin/tkt",
		"TKT_VERSION=v0.6.0", "TKT_PI_VERSION=1.3.0"}
	if !slices.Equal(got, want) {
		t.Errorf("Env = %q\nwant %q", got, want)
	}
}
