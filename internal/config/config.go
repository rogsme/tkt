// Package config holds tkt's own settings: the presets that choose an agent
// and the stage lines tkt run sends.
package config

import (
	"slices"
	"sort"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
)

// Harnesses tkt can start.
const (
	Claude   = "claude"
	OpenCode = "opencode"
)

// Preset is one way to run an agent.
type Preset struct {
	Name    string `json:"name"`
	Harness string `json:"harness"`
	// Model is empty for the harness's default model. A Claude model reads
	// model#effort; an OpenCode model reads provider/model#variant.
	Model string `json:"model,omitempty"`
	// Serves lists the prompt Model: values the preset serves; "*" serves any.
	Serves []string `json:"serves"`
}

// ServesModel reports whether the preset serves a prompt's Model: value.
func (p Preset) ServesModel(v string) bool {
	return slices.Contains(p.Serves, "*") || slices.Contains(p.Serves, v)
}

// Config is the loaded settings. Presets keep their file order: the first
// is the default, and tkt tries them in order.
type Config struct {
	// Source names where the settings came from, for messages.
	Source  string
	Presets []Preset
	Stages  map[string]string
	// OwnStages lists the stages the settings define, sorted.
	OwnStages []string
	// IgnoredLegacy lists old-format files that config.toml overrides.
	IgnoredLegacy []string
	Auto          Auto
	Review        Review
	Board         Board
	Agent         Agent
}

// Agent holds the [agent] settings for tkt agent.
type Agent struct {
	// Model is a pi model pattern; empty keeps pi's default.
	Model string
	// Extensions are more pi extension files to load, as absolute paths.
	Extensions []string
}

// Auto holds tkt auto's settings.
type Auto struct {
	// MaxRounds caps the address, sync and ci runs since the plan's approval.
	MaxRounds int
	// Poll is how often a loop reads the forge while it waits for CI or a
	// review.
	Poll time.Duration
	// MaxParallel caps the auto loops that run at once in a repository; 0
	// means no cap.
	MaxParallel int
}

// Review holds how tkt treats reviews.
type Review struct {
	// BotWait is how long a new head waits for the forge's automated
	// reviewer before review-pr runs instead; 0 means there is no bot.
	BotWait time.Duration
}

// Board holds what tkt ls marks for people.
type Board struct {
	// StaleAfter marks an agent that sat idle with no status line this long;
	// 0 turns the mark off.
	StaleAfter time.Duration
}

// The defaults for the settings above.
const (
	DefaultMaxRounds   = 5
	DefaultPoll        = 30 * time.Second
	DefaultMaxParallel = 3
	DefaultBotWait     = 15 * time.Minute
	DefaultStaleAfter  = 2 * time.Hour
)

// ApproveLine is what tkt approve sends to the planning session. It is not a
// stage: tkt run starts a fresh session, and the plan lives in this one.
const ApproveLine = "The plan is approved. Use the work-ticket skill to execute it."

// BuiltinStages are the stage lines tkt run sends; {id} becomes the ticket.
var BuiltinStages = map[string]string{
	"start":   "Use the start-ticket skill to start ticket {id}.",
	"address": "Use the address-pr-review skill to address the review on this branch's pull request.",
	"sync":    "Use the address-pr-review skill to sync the PR with the base.",
	"verify":  "Run every step of the Human verification section of this branch's pull request with the playwright-cli skill. Report each step as passed or failed with what you saw. Change no code and post nothing.",
	"ci":      "The checks on this branch's pull request fail at its head. Find the cause and fix it, then push once. When a failure is infrastructure, such as an API quota error, rerun the job instead of changing code.",
	"review":  "Use the review-pr skill to review the current PR.",
}

// BuiltinPresets apply when the settings define none.
func BuiltinPresets() []Preset {
	return []Preset{
		{Name: Claude, Harness: Claude, Serves: []string{"*"}},
		{Name: OpenCode, Harness: OpenCode, Serves: []string{"*"}},
	}
}

func builtin() Config {
	c := Config{Source: "built-in presets", Presets: BuiltinPresets(), Stages: map[string]string{},
		Auto:   Auto{MaxRounds: DefaultMaxRounds, Poll: DefaultPoll, MaxParallel: DefaultMaxParallel},
		Review: Review{BotWait: DefaultBotWait},
		Board:  Board{StaleAfter: DefaultStaleAfter}}
	for k, v := range BuiltinStages {
		c.Stages[k] = v
	}
	return c
}

// Preset returns the named preset.
func (c Config) Preset(name string) (Preset, error) {
	for _, p := range c.Presets {
		if p.Name == name {
			return p, nil
		}
	}
	return Preset{}, apperr.New(apperr.UnknownPreset, "unknown preset '%s' (presets: %s)", name, strings.Join(c.PresetNames(), " ")).
		WithHint("%s", suggestion(name, c.PresetNames()))
}

// Has reports whether a preset of that name exists.
func (c Config) Has(name string) bool {
	_, err := c.Preset(name)
	return err == nil
}

// Default returns the first preset.
func (c Config) Default() Preset { return c.Presets[0] }

// FirstForHarness returns the first preset that uses harness.
func (c Config) FirstForHarness(harness string) (Preset, bool) {
	for _, p := range c.Presets {
		if p.Harness == harness {
			return p, true
		}
	}
	return Preset{}, false
}

// PresetNames lists the presets in order.
func (c Config) PresetNames() []string {
	names := make([]string, len(c.Presets))
	for i, p := range c.Presets {
		names[i] = p.Name
	}
	return names
}

// StageNames lists the stages, sorted.
func (c Config) StageNames() []string {
	names := make([]string, 0, len(c.Stages))
	for k := range c.Stages {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// StageLine returns a stage's line for a ticket.
func (c Config) StageLine(stage, id string) (string, error) {
	t, ok := c.Stages[stage]
	if !ok || t == "" {
		return "", apperr.New(apperr.UnknownStage, "unknown stage '%s' (stages: %s)", stage, strings.Join(c.StageNames(), " ")).
			WithHint("%s", suggestion(stage, c.StageNames()))
	}
	return strings.ReplaceAll(t, "{id}", id), nil
}

// TabLabel names a ticket's tab: the bare id for the default preset,
// otherwise "ID (preset)".
func (c Config) TabLabel(id, preset string) string {
	if preset == c.Default().Name {
		return id
	}
	return id + " (" + preset + ")"
}
