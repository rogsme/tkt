package config

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
)

// Load reads the settings from dir, the tkt configuration directory.
func Load(dir string) (Config, error) {
	c := builtin()
	presets := filepath.Join(dir, "presets")
	if src, err := os.ReadFile(presets); err == nil {
		c.Source = presets
		c.Presets = nil
		if err := parsePresets(&c, string(src)); err != nil {
			return c, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return c, apperr.New(apperr.ConfigInvalid, "cannot read %s: %v", presets, err)
	}
	stages := filepath.Join(dir, "stages")
	if src, err := os.ReadFile(stages); err == nil {
		if err := parseStages(&c, stages, string(src)); err != nil {
			return c, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return c, apperr.New(apperr.ConfigInvalid, "cannot read %s: %v", stages, err)
	}
	return c, nil
}

// parsePresets reads "name harness model serves" lines; "-" is the default
// model and an empty serves column serves any model.
func parsePresets(c *Config, src string) error {
	f := c.Source
	for _, line := range lines(src) {
		fields := strings.Fields(line)
		name, harness, model, serves := fields[0], field(fields, 1), field(fields, 2), field(fields, 3)
		if harness != Claude && harness != OpenCode {
			return apperr.New(apperr.ConfigInvalid, "%s: preset '%s': the harness must be claude or opencode", f, name)
		}
		if model == "" {
			return apperr.New(apperr.ConfigInvalid, "%s: preset '%s': missing the model column (use - for the default)", f, name)
		}
		if len(fields) > 4 {
			return apperr.New(apperr.ConfigInvalid, "%s: preset '%s': too many columns", f, name)
		}
		if c.Has(name) {
			return apperr.New(apperr.ConfigInvalid, "%s: preset '%s' is defined twice", f, name)
		}
		if model == "-" {
			model = ""
		}
		if serves == "" {
			serves = "*"
		}
		c.Presets = append(c.Presets, Preset{Name: name, Harness: harness, Model: model, Serves: strings.Split(serves, ",")})
	}
	if len(c.Presets) == 0 {
		return apperr.New(apperr.ConfigInvalid, "%s: no presets", f)
	}
	return nil
}

// parseStages reads "name text" lines.
func parseStages(c *Config, f, src string) error {
	for _, line := range lines(src) {
		line = strings.TrimLeft(line, " \t")
		name, text := line, ""
		if i := strings.IndexAny(line, " \t"); i >= 0 {
			name, text = line[:i], strings.TrimSpace(line[i:])
		}
		if text == "" {
			return apperr.New(apperr.ConfigInvalid, "%s: stage '%s' has no text", f, name)
		}
		c.Stages[name] = text
	}
	return nil
}

// lines returns the lines that are neither blank nor comments.
func lines(src string) []string {
	var out []string
	s := bufio.NewScanner(strings.NewReader(src))
	for s.Scan() {
		l := s.Text()
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func field(f []string, i int) string {
	if i < len(f) {
		return f[i]
	}
	return ""
}
