package config

import (
	"regexp"
	"sort"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
)

var modelLine = regexp.MustCompile("Model:[[:space:]]*`?([A-Za-z0-9._-]+)")

// ModelValues returns the distinct Model: values a prompt names, sorted.
func ModelValues(prompt string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range modelLine.FindAllStringSubmatch(prompt, -1) {
		v := strings.TrimRight(m[1], ".")
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// Pick chooses the preset for a prompt: the one named by as, otherwise the
// first preset that serves the prompt's Model: value, otherwise the default.
// With force, as may contradict the prompt.
func (c Config) Pick(as, prompt string, force bool) (Preset, error) {
	values := ModelValues(prompt)
	if as != "" {
		p, err := c.Preset(as)
		if err != nil {
			return p, err
		}
		for _, v := range values {
			if !p.ServesModel(v) && !force {
				return p, apperr.New(apperr.PresetModelMismatch,
					"the prompt says 'Model: %s', which preset '%s' does not serve; pass --force to use it anyway", v, as)
			}
		}
		return p, nil
	}
	if len(values) > 1 {
		return Preset{}, apperr.New(apperr.AmbiguousModel, "the prompt names several models (%s); pass --as", strings.Join(values, " "))
	}
	if len(values) == 0 {
		return c.Default(), nil
	}
	for _, p := range c.Presets {
		if p.ServesModel(values[0]) {
			return p, nil
		}
	}
	return Preset{}, apperr.New(apperr.NoPresetForModel,
		"no preset serves 'Model: %s'; add one to %s or pass --as", values[0], c.Source)
}

// suggestion returns "did you mean X?" for the candidate closest to name,
// or "" when none is close.
func suggestion(name string, candidates []string) string {
	best, bestD := "", 3
	for _, c := range candidates {
		if d := distance(name, c); d < bestD {
			best, bestD = c, d
		}
	}
	if best == "" {
		return ""
	}
	return "did you mean " + best + "?"
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
