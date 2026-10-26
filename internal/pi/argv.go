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
	// Session resumes the session with this id, or a unique part of it.
	Session string
	// Theme is the theme pi starts with; empty keeps the one pi's settings
	// name.
	Theme string
	// Args are more of pi's own flags, passed as they are, last.
	Args []string
}

// Themes are the theme files beside the extension: tkt-dark and tkt-light,
// from one palette. Theme picks between them by the terminal's background.
var Themes = []string{"tkt-dark.json", "tkt-light.json"}

// Theme is the theme setting for tkt's own themes.
const Theme = "tkt-light/tkt-dark"

// Argv is the command line that starts pi with tkt's extension, system
// prompt and themes and nothing else: none of the user's own pi extensions,
// skills, prompt templates or themes, no AGENTS.md or CLAUDE.md, no MCP
// servers and no built-in tools. extDir is where WriteExt put the
// extension.
func Argv(bin, extDir string, o Launch) []string {
	argv := []string{bin,
		"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes",
		"--no-context-files", "--no-mcp", "--no-builtin-tools",
		"-e", filepath.Join(extDir, "index.ts"),
		"--system-prompt", filepath.Join(extDir, "system.md"),
	}
	for _, t := range Themes {
		argv = append(argv, "--theme", filepath.Join(extDir, t))
	}
	if o.Theme != "" {
		argv = append(argv, "--use-theme", o.Theme)
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
	if o.Session != "" {
		argv = append(argv, "--session", o.Session)
	}
	return append(argv, o.Args...)
}

// dropped are variables of the user's own pi that would point tkt's pi at
// the user's sessions or at another pi's package files.
var dropped = []string{"PI_CODING_AGENT_SESSION_DIR", "PI_PACKAGE_DIR"}

// Host is the tkt that starts pi: the directory for pi's settings, logins
// and sessions, the tkt binary, the versions of tkt and of pi, and the file
// where the chat that serves the repository's web page names it.
type Host struct {
	AgentDir  string
	Bin       string
	Version   string
	PiVersion string
	WebFile   string
}

// EnvVars are the variables tkt sets for pi, as KEY=VALUE: pi keeps its
// settings, logins and sessions in AgentDir, asks no server for updates or
// telemetry, and finds tkt at Bin. The versions tell the extension what it
// runs on, which the binary at Bin no longer says once tkt is replaced.
// TKT_WEB_FILE is where the extension names the web page it serves, for
// tkt web to find.
func EnvVars(h Host) []string {
	return []string{
		"PI_CODING_AGENT_DIR=" + h.AgentDir,
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
		"TKT_BIN=" + h.Bin,
		"TKT_VERSION=" + h.Version,
		"TKT_PI_VERSION=" + h.PiVersion,
		"TKT_WEB_FILE=" + h.WebFile,
	}
}

// Env returns base with EnvVars in place of any values base had for them,
// and without the variables in dropped.
func Env(base []string, h Host) []string {
	set := EnvVars(h)
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
