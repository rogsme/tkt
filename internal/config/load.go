package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"gitlab.com/rogs/tkt/internal/apperr"
)

// FileName is tkt's settings file inside its configuration directory.
const FileName = "config.toml"

var (
	presetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	stageName  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	openCode   = regexp.MustCompile(`^[^/#\s]+/[^#\s]+(#[^#\s]+)?$`)
)

type presetTOML struct {
	Harness string   `toml:"harness"`
	Model   string   `toml:"model"`
	Serves  []string `toml:"serves"`
}

type fileTOML struct {
	Presets map[string]presetTOML `toml:"presets"`
	Stages  map[string]string     `toml:"stages"`
}

// Load reads the settings from dir, tkt's configuration directory. Without
// a config.toml, the built-in presets and stages apply. The old presets and
// stages files are refused with their content converted to TOML.
func Load(dir string) (Config, error) {
	c := builtin()
	path := filepath.Join(dir, FileName)
	src, err := os.ReadFile(path)
	legacy := legacyFiles(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist) && len(legacy) > 0:
		return c, legacyError(dir, legacy)
	case errors.Is(err, fs.ErrNotExist):
		return c, nil
	case err != nil:
		return c, apperr.New(apperr.ConfigInvalid, "cannot read %s: %v", path, err)
	}
	c.Source = path
	c.IgnoredLegacy = legacy
	if err := parseTOML(&c, path, string(src)); err != nil {
		return c, err
	}
	return c, nil
}

func parseTOML(c *Config, path, src string) error {
	var f fileTOML
	md, err := toml.Decode(src, &f)
	if err != nil {
		return apperr.New(apperr.ConfigInvalid, "%s: %v", path, err)
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
		}
		return apperr.New(apperr.ConfigInvalid, "%s: unknown key %s", path, strings.Join(keys, ", "))
	}
	if len(f.Presets) > 0 {
		c.Presets = nil
		for _, k := range md.Keys() {
			if len(k) != 2 || k[0] != "presets" {
				continue
			}
			name := k[1]
			p := f.Presets[name]
			bad := func(format string, args ...any) error {
				return apperr.New(apperr.ConfigInvalid, "%s: preset '%s': %s", path, name, fmt.Sprintf(format, args...))
			}
			switch {
			case !presetName.MatchString(name):
				return bad("the name may hold letters, digits, '.', '_' and '-', starting with a letter or digit")
			case p.Harness != Claude && p.Harness != OpenCode:
				return bad("the harness must be claude or opencode")
			case p.Harness == OpenCode && p.Model != "" && !openCode.MatchString(p.Model):
				return bad("an OpenCode model looks like provider/model or provider/model#variant")
			case md.IsDefined("presets", name, "serves") && len(p.Serves) == 0:
				return bad("serves is empty; leave it out to serve any model")
			}
			serves := p.Serves
			if serves == nil {
				serves = []string{"*"}
			}
			c.Presets = append(c.Presets, Preset{Name: name, Harness: p.Harness, Model: p.Model, Serves: serves})
		}
	}
	for name, text := range f.Stages {
		if !stageName.MatchString(name) {
			return apperr.New(apperr.ConfigInvalid, "%s: stage '%s': the name may hold lowercase letters, digits, '_' and '-'", path, name)
		}
		if strings.TrimSpace(text) == "" {
			return apperr.New(apperr.ConfigInvalid, "%s: stage '%s' has no text", path, name)
		}
		c.Stages[name] = strings.TrimSpace(text)
		c.OwnStages = append(c.OwnStages, name)
	}
	sort.Strings(c.OwnStages)
	return nil
}

// legacyFiles lists the old-format files present in dir.
func legacyFiles(dir string) []string {
	var out []string
	for _, n := range []string{"presets", "stages"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}

func legacyError(dir string, files []string) error {
	c, err := loadLegacy(dir)
	if err != nil {
		return apperr.New(apperr.ConfigLegacy,
			"%s use the old format, and they do not parse: %v; write %s instead (tkt help config)",
			strings.Join(files, " and "), apperr.As(err).Msg, filepath.Join(dir, FileName)).WithHint("tkt help config")
	}
	text := RenderTOML(c)
	return apperr.New(apperr.ConfigLegacy,
		"%s use the old format; write this to %s, then delete them:\n\n%s",
		strings.Join(files, " and "), filepath.Join(dir, FileName), text).
		WithHint("write %s, then delete %s", filepath.Join(dir, FileName), strings.Join(files, " and ")).
		With("config_toml", text)
}

// RenderTOML writes settings as config.toml text: every preset, in order,
// and the stages the user defined.
func RenderTOML(c Config) string {
	var b strings.Builder
	b.WriteString("# tkt settings. The first preset is the default, and tkt tries the presets in\n# this order when a prompt names a model.\n")
	for _, p := range c.Presets {
		fmt.Fprintf(&b, "\n[presets.%s]\nharness = %s\n", tomlKey(p.Name), tomlString(p.Harness))
		if p.Model != "" {
			fmt.Fprintf(&b, "model = %s\n", tomlString(p.Model))
		}
		if len(p.Serves) > 0 && !(len(p.Serves) == 1 && p.Serves[0] == "*") {
			q := make([]string, len(p.Serves))
			for i, s := range p.Serves {
				q[i] = tomlString(s)
			}
			fmt.Fprintf(&b, "serves = [%s]\n", strings.Join(q, ", "))
		}
	}
	if len(c.OwnStages) > 0 {
		b.WriteString("\n[stages]\n")
		for _, name := range c.OwnStages {
			fmt.Fprintf(&b, "%s = %s\n", tomlKey(name), tomlString(c.Stages[name]))
		}
	}
	return b.String()
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return tomlString(k)
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
