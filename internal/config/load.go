package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"gitlab.com/rogs/tkt/internal/apperr"
)

// FileName is tkt's settings file inside its configuration directory.
const FileName = "config.toml"

// A repository's own settings live in the ProjectDir directory at the root
// of its primary worktree: SharedFile, which the repository may commit, and
// LocalFile, which is yours and git must not track.
const (
	ProjectDir = ".tkt"
	SharedFile = "tkt.toml"
	LocalFile  = "tkt.local.toml"
)

// SharedPath and LocalPath are the repository's settings files in the
// primary worktree root.
func SharedPath(root string) string { return filepath.Join(root, ProjectDir, SharedFile) }
func LocalPath(root string) string  { return filepath.Join(root, ProjectDir, LocalFile) }

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

type agentTOML struct {
	Model      string   `toml:"model"`
	Extensions []string `toml:"extensions"`
}

type fileTOML struct {
	Presets map[string]presetTOML `toml:"presets"`
	Stages  map[string]string     `toml:"stages"`
	Auto    *struct {
		MaxRounds   *int   `toml:"max_rounds"`
		Poll        string `toml:"poll"`
		MaxParallel *int   `toml:"max_parallel"`
	} `toml:"auto"`
	Review *struct {
		BotWait string `toml:"bot_wait"`
	} `toml:"review"`
	Board *struct {
		StaleAfter string `toml:"stale_after"`
	} `toml:"board"`
	Agent agentTOML `toml:"agent"`
	Watch *struct {
		Interval       string `toml:"interval"`
		DoneAfterMerge *bool  `toml:"done_after_merge"`
		Autostart      *bool  `toml:"autostart"`
	} `toml:"watch"`
	Suggest *struct {
		As string `toml:"as"`
	} `toml:"suggest"`
	Verify *struct {
		As string `toml:"as"`
	} `toml:"verify"`
	Fixes *struct {
		As string `toml:"as"`
	} `toml:"fixes"`
	Start *struct {
		Mode string `toml:"mode"`
	} `toml:"start"`
	Chores map[string]choreTOML  `toml:"chores"`
	Status map[string]statusTOML `toml:"status"`
}

// Load reads the settings from dir, tkt's configuration directory, then
// those of the repository whose primary worktree is root: .tkt/tkt.toml,
// then .tkt/tkt.local.toml, each winning key by key over what came before.
// Without any of them, the built-in presets and stages apply; root is ""
// outside a repository. The old presets and stages files are refused with
// their content converted to TOML.
func Load(dir, root string) (Config, error) {
	c := builtin()
	path := filepath.Join(dir, FileName)
	src, err := os.ReadFile(path)
	legacy := legacyFiles(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist) && len(legacy) > 0:
		return c, legacyError(dir, legacy)
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, apperr.New(apperr.ConfigInvalid, "cannot read %s: %v", path, err)
	default:
		c.Source = path
		c.IgnoredLegacy = legacy
		if err := parseTOML(&c, path, string(src)); err != nil {
			return c, err
		}
	}
	if root == "" {
		return c, nil
	}
	for _, project := range []string{SharedPath(root), LocalPath(root)} {
		src, err = os.ReadFile(project)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return c, apperr.New(apperr.ConfigInvalid, "cannot read %s: %v", project, err)
		}
		if err := parseProject(&c, path, project, root, string(src)); err != nil {
			return c, err
		}
	}
	return c, nil
}

func decode(path, src string) (fileTOML, toml.MetaData, error) {
	var f fileTOML
	md, err := toml.Decode(src, &f)
	if err != nil {
		return f, md, apperr.New(apperr.ConfigInvalid, "%s: %v", path, err)
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
		}
		return f, md, apperr.New(apperr.ConfigInvalid, "%s: unknown key %s", path, strings.Join(keys, ", "))
	}
	return f, md, nil
}

func parseTOML(c *Config, path, src string) error {
	f, md, err := decode(path, src)
	if err != nil {
		return err
	}
	if len(f.Chores) > 0 {
		return choresMoved(path, f.Chores)
	}
	ps, err := readPresets(path, f, md)
	if err != nil {
		return err
	}
	if len(ps) > 0 {
		c.Presets = ps
	}
	if err := readAgent(c, path, f, md); err != nil {
		return err
	}
	return parseSettings(c, path, f)
}

// readPresets reads a file's [presets.NAME] tables, in the file's order.
func readPresets(path string, f fileTOML, md toml.MetaData) ([]Preset, error) {
	var out []Preset
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
			return nil, bad("the name may hold letters, digits, '.', '_' and '-', starting with a letter or digit")
		case p.Harness != Claude && p.Harness != OpenCode:
			return nil, bad("the harness must be claude or opencode")
		case p.Harness == OpenCode && p.Model != "" && !openCode.MatchString(p.Model):
			return nil, bad("an OpenCode model looks like provider/model or provider/model#variant")
		case md.IsDefined("presets", name, "serves") && len(p.Serves) == 0:
			return nil, bad("serves is empty; leave it out to serve any model")
		}
		serves := p.Serves
		if serves == nil {
			serves = []string{"*"}
		}
		out = append(out, Preset{Name: name, Harness: p.Harness, Model: p.Model, Serves: serves})
	}
	return out, nil
}

// readAgent reads the [agent] keys the file sets.
func readAgent(c *Config, path string, f fileTOML, md toml.MetaData) error {
	if md.IsDefined("agent", "model") {
		c.Agent.Model = f.Agent.Model
	}
	if !md.IsDefined("agent", "extensions") {
		return nil
	}
	c.Agent.Extensions = nil
	for _, p := range f.Agent.Extensions {
		abs, ok := absPath(p)
		if !ok {
			return apperr.New(apperr.ConfigInvalid, "%s: agent extension '%s' is not an absolute path", path, p).
				WithHint("write the extension's full path, such as ~/.pi/agent/extensions/acme.ts")
		}
		c.Agent.Extensions = append(c.Agent.Extensions, abs)
	}
	return nil
}

// parseProject reads one of a repository's settings files over the settings
// read before. Its presets go first, in its own order, and replace those of
// the same name, so its first preset is the default there. The shared file
// takes no [agent] table, whose extensions are code that runs on your
// machine, and no chores, since every clone that reads a chore runs it; an
// as key in it names a preset it defines, so it never depends on the presets
// each person's config.toml names.
func parseProject(c *Config, global, path, root, src string) error {
	f, md, err := decode(path, src)
	if err != nil {
		return err
	}
	local := filepath.Base(path) == LocalFile
	switch {
	case !local && md.IsDefined("agent"):
		return apperr.New(apperr.ConfigInvalid, "%s: [agent] belongs in %s or %s: its extensions are code that runs on your machine, and anyone can commit to the repository", path, LocalPath(root), global).
			WithHint("move [agent] to %s", LocalPath(root))
	case !local && md.IsDefined("chores"):
		return apperr.New(apperr.ConfigInvalid, "%s: [chores] belongs in %s: every clone that reads a chore runs it, so a chore the repository commits would run once for each person", path, LocalPath(root)).
			WithHint("move [chores] to %s", LocalPath(root))
	}
	ps, err := readPresets(path, f, md)
	if err != nil {
		return err
	}
	if !local {
		for _, k := range []struct{ key, as string }{{"suggest", asOf(f.Suggest)}, {"verify", asOf(f.Verify)}, {"fixes", asOf(f.Fixes)}} {
			if k.as != "" && !slices.ContainsFunc(ps, func(p Preset) bool { return p.Name == k.as }) {
				return apperr.New(apperr.ConfigInvalid, "%s: [%s] as names preset '%s', which this file does not define: each person's config.toml names its own presets, so the repository's file names only its own", path, k.key, k.as).
					WithHint("define [presets.%s] in %s, or set [%s] as in %s", k.as, path, k.key, LocalPath(root))
			}
		}
	}
	if len(ps) > 0 {
		c.Presets = append(ps, slices.DeleteFunc(c.Presets, func(old Preset) bool {
			return slices.ContainsFunc(ps, func(p Preset) bool { return p.Name == old.Name })
		})...)
		c.PresetFiles = append(c.PresetFiles, path)
	}
	if local {
		c.Local = path
		if err := readAgent(c, path, f, md); err != nil {
			return err
		}
	} else {
		c.Shared = path
	}
	if err := parseSettings(c, path, f); err != nil {
		return err
	}
	return parseChores(c, path, root, f.Chores)
}

// asOf is a [suggest] or [verify] table's as, or "" without the table.
func asOf(t *struct {
	As string `toml:"as"`
}) string {
	if t == nil {
		return ""
	}
	return t.As
}

// parseSettings reads the tables both files take: [stages], [auto],
// [review], [board], [watch], [suggest], [verify], [fixes], [start] and
// [status]. It sets only
// the keys the file names, so the file read last wins key by key.
func parseSettings(c *Config, path string, f fileTOML) error {
	for name, text := range f.Stages {
		if !stageName.MatchString(name) {
			return apperr.New(apperr.ConfigInvalid, "%s: stage '%s': the name may hold lowercase letters, digits, '_' and '-'", path, name)
		}
		if strings.TrimSpace(text) == "" {
			return apperr.New(apperr.ConfigInvalid, "%s: stage '%s' has no text", path, name)
		}
		c.Stages[name] = strings.TrimSpace(text)
		if !slices.Contains(c.OwnStages, name) {
			c.OwnStages = append(c.OwnStages, name)
		}
	}
	sort.Strings(c.OwnStages)
	bad := func(key, rule string) error {
		return apperr.New(apperr.ConfigInvalid, "%s: %s %s", path, key, rule)
	}
	duration := func(key, v string, dst *time.Duration, positive bool) error {
		if v == "" {
			return nil
		}
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			return bad(key, "must be a duration such as \"30s\", \"15m\" or \"2h\"")
		case positive && d <= 0:
			return bad(key, "must be more than 0")
		case d < 0:
			return bad(key, "must not be negative")
		}
		*dst = d
		return nil
	}
	if a := f.Auto; a != nil {
		if a.MaxRounds != nil {
			if *a.MaxRounds < 0 {
				return bad("[auto] max_rounds", "must not be negative")
			}
			c.Auto.MaxRounds = *a.MaxRounds
		}
		if a.MaxParallel != nil {
			if *a.MaxParallel < 0 {
				return bad("[auto] max_parallel", "must not be negative")
			}
			c.Auto.MaxParallel = *a.MaxParallel
		}
		if err := duration("[auto] poll", a.Poll, &c.Auto.Poll, true); err != nil {
			return err
		}
	}
	if r := f.Review; r != nil {
		if err := duration("[review] bot_wait", r.BotWait, &c.Review.BotWait, false); err != nil {
			return err
		}
	}
	if b := f.Board; b != nil {
		if err := duration("[board] stale_after", b.StaleAfter, &c.Board.StaleAfter, false); err != nil {
			return err
		}
	}
	if w := f.Watch; w != nil {
		if err := duration("[watch] interval", w.Interval, &c.Watch.Interval, true); err != nil {
			return err
		}
		if w.DoneAfterMerge != nil {
			c.Watch.DoneAfterMerge = *w.DoneAfterMerge
		}
		if w.Autostart != nil {
			c.Watch.Autostart = *w.Autostart
		}
	}
	if s := f.Start; s != nil && s.Mode != "" {
		if s.Mode != ModeAuto && s.Mode != ModeManual {
			return bad("[start] mode", fmt.Sprintf("is '%s'; it takes auto or manual", s.Mode))
		}
		c.Start.Mode = s.Mode
	}
	if s := f.Suggest; s != nil && s.As != "" {
		if !c.Has(s.As) {
			return bad("[suggest] as", fmt.Sprintf("names preset '%s', which the presets do not define", s.As))
		}
		c.Suggest.As = s.As
	}
	if v := f.Verify; v != nil && v.As != "" {
		if !c.Has(v.As) {
			return bad("[verify] as", fmt.Sprintf("names preset '%s', which the presets do not define", v.As))
		}
		c.Verify.As = v.As
	}
	if v := f.Fixes; v != nil && v.As != "" {
		if !c.Has(v.As) {
			return bad("[fixes] as", fmt.Sprintf("names preset '%s', which the presets do not define", v.As))
		}
		c.Fixes.As = v.As
	}
	return parseStatus(c, path, f.Status)
}

// absPath expands a leading ~/ to the home directory and reports whether
// the result is absolute: pi would resolve a relative path from the
// repository it runs in.
func absPath(p string) (string, bool) {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, false
		}
		p = filepath.Join(home, rest)
	}
	return p, filepath.IsAbs(p)
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
			"tkt no longer reads the old settings files (%s), and they do not parse: %v; write %s instead (tkt help config)",
			strings.Join(files, " and "), apperr.As(err).Msg, filepath.Join(dir, FileName)).WithHint("tkt help config")
	}
	text := RenderTOML(c)
	return apperr.New(apperr.ConfigLegacy,
		"tkt no longer reads the old settings files (%s); write this to %s, then delete them:\n\n%s",
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
