// Package lifecycle reads the project configuration that /setup-rogs writes:
// the tracker's ticket id pattern and the forge's fields.
package lifecycle

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/contract"
)

// Forge holds the "## Forge" fields tkt uses.
type Forge struct {
	Kind   string `json:"kind"`
	Host   string `json:"host"`
	Repo   string `json:"repository"`
	Remote string `json:"remote"`
	Base   string `json:"base"`
}

// Config is one project's lifecycle configuration.
type Config struct {
	TrackerFile string
	ForgeFile   string
	// Pattern is the ticket id regular expression as written; RE is its
	// compiled form and Prefix the key before the first dash, if any.
	Pattern string
	RE      *regexp.Regexp
	Prefix  string
	Forge   Forge
}

var (
	patternRE = regexp.MustCompile("`(\\^[^`]*\\$)`")
	prefixRE  = regexp.MustCompile(`^\^([A-Za-z][A-Za-z0-9]*)-`)
)

// Load reads the configuration under the primary worktree root.
func Load(root string) (Config, error) {
	c := Config{
		TrackerFile: filepath.Join(root, contract.TrackerFile),
		ForgeFile:   filepath.Join(root, contract.ForgeFile),
	}
	tracker, terr := readLines(c.TrackerFile)
	forge, ferr := readLines(c.ForgeFile)
	if terr != nil || ferr != nil {
		return c, apperr.New(apperr.LifecycleConfigMissing,
			"%s has no lifecycle configuration; run /setup-rogs", filepath.Join(root, "docs/agents")).
			WithHint("run /setup-rogs in this project")
	}
	if err := checkMarker(c.TrackerFile, tracker, contract.TrackerID); err != nil {
		return c, err
	}
	if err := checkMarker(c.ForgeFile, forge, contract.ForgeID); err != nil {
		return c, err
	}
	for _, line := range tracker {
		if strings.HasPrefix(line, "- Ticket ids:") {
			if m := patternRE.FindStringSubmatch(line); m != nil {
				c.Pattern = m[1]
			}
			break
		}
	}
	if c.Pattern == "" {
		return c, apperr.New(apperr.TicketPatternMissing,
			"%s: no ticket id pattern (a backticked ^...$) on the '- Ticket ids:' line", c.TrackerFile)
	}
	re, err := regexp.Compile(c.Pattern)
	if err != nil {
		return c, apperr.New(apperr.TicketPatternInvalid,
			"%s: the ticket id pattern %s does not compile: %v", c.TrackerFile, c.Pattern, err)
	}
	c.RE = re
	if m := prefixRE.FindStringSubmatch(c.Pattern); m != nil {
		c.Prefix = m[1]
	}
	c.Forge = Forge{
		Kind:   forgeField(forge, "Kind"),
		Host:   forgeField(forge, "Host"),
		Repo:   forgeField(forge, "Repository"),
		Remote: forgeField(forge, "Remote"),
		Base:   forgeField(forge, "Base branch"),
	}
	if c.Forge.Remote == "" || c.Forge.Base == "" {
		return c, apperr.New(apperr.ForgeConfigIncomplete,
			"%s: '## Forge' needs '- Remote:' and '- Base branch:' lines", c.ForgeFile)
	}
	return c, nil
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	return lines, s.Err()
}

func checkMarker(path string, lines []string, id string) error {
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	}
	v, got, ok := contract.ParseMarker(first)
	switch {
	case !ok || got != id:
		return apperr.New(apperr.LifecycleConfigMarker,
			"%s is not a rogs-skills config v%d file; run /setup-rogs", path, contract.Version).
			WithHint("run /setup-rogs in this project")
	case v < contract.Version:
		return apperr.New(apperr.LifecycleConfigVersion,
			"%s is a rogs-skills config v%d file and tkt reads v%d; run /setup-rogs", path, v, contract.Version).
			WithHint("run /setup-rogs in this project")
	case v > contract.Version:
		return apperr.New(apperr.LifecycleConfigVersion,
			"%s is a rogs-skills config v%d file and tkt reads v%d; update tkt", path, v, contract.Version).
			WithHint("update tkt")
	}
	return nil
}

// forgeField returns the first "- Name: value" line inside "## Forge", cut at
// the first ";", without backticks or trailing dots and spaces.
func forgeField(lines []string, name string) string {
	in := false
	prefix := "- " + name + ":"
	for _, line := range lines {
		if line == "## Forge" {
			in = true
			continue
		}
		if in && strings.HasPrefix(line, "## ") {
			break
		}
		if in && strings.HasPrefix(line, prefix) {
			v := strings.TrimLeft(strings.TrimPrefix(line, prefix), " ")
			v, _, _ = strings.Cut(v, ";")
			v = strings.ReplaceAll(v, "`", "")
			return strings.TrimRight(v, " \t.")
		}
	}
	return ""
}

// RequireGitHub refuses a forge other than GitHub, for commands that read
// pull requests through gh.
func (c Config) RequireGitHub(cmd string) error {
	if c.Forge.Kind != "GitHub" {
		kind := c.Forge.Kind
		if kind == "" {
			kind = "not set"
		}
		return apperr.New(apperr.ForgeUnsupported,
			"tkt %s supports GitHub forges only; this repository's forge is %s", cmd, kind)
	}
	if c.Forge.Host == "" || c.Forge.Repo == "" {
		return apperr.New(apperr.ForgeConfigIncomplete,
			"%s: '## Forge' needs '- Host:' and '- Repository:' lines", c.ForgeFile)
	}
	return nil
}
