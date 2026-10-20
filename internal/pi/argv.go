package pi

import (
	"path/filepath"
	"slices"
	"strings"
)

// Launch is what one tkt agent run asks of pi.
type Launch struct {
	// Model is a pi model pattern; empty keeps pi's default.
	Model string
	// Extensions are more extension files to load, as absolute paths.
	Extensions []string
	// Continue resumes the newest session in the directory.
	Continue bool
}

// Argv is the command line that starts pi with tkt's extension and system
// prompt and nothing else: none of the user's own pi extensions, skills,
// prompt templates or themes, no AGENTS.md or CLAUDE.md, no MCP servers and
// no built-in tools. extDir is where WriteExt put the extension.
func Argv(bin, extDir string, o Launch) []string {
	argv := []string{bin,
		"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes",
		"--no-context-files", "--no-mcp", "--no-builtin-tools",
		"-e", filepath.Join(extDir, "index.ts"),
		"--system-prompt", filepath.Join(extDir, "system.md"),
	}
	if o.Model != "" {
		argv = append(argv, "--model", o.Model)
	}
	for _, e := range o.Extensions {
		argv = append(argv, "-e", e)
	}
	if o.Continue {
		argv = append(argv, "-c")
	}
	return argv
}

// dropped are variables of the user's own pi that would point tkt's pi at
// the user's sessions or at another pi's package files.
var dropped = []string{"PI_CODING_AGENT_SESSION_DIR", "PI_PACKAGE_DIR"}

// EnvVars are the variables tkt sets for pi, as KEY=VALUE: pi keeps its
// settings, logins and sessions in agentDir, asks no server for updates or
// telemetry, and finds tkt at tktBin.
func EnvVars(agentDir, tktBin string) []string {
	return []string{
		"PI_CODING_AGENT_DIR=" + agentDir,
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
		"TKT_BIN=" + tktBin,
	}
}

// Env returns base with EnvVars in place of any values base had for them,
// and without the variables in dropped.
func Env(base []string, agentDir, tktBin string) []string {
	set := EnvVars(agentDir, tktBin)
	keys := slices.Clone(dropped)
	for _, kv := range set {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	out := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(keys, k) {
			out = append(out, kv)
		}
	}
	return append(out, set...)
}
