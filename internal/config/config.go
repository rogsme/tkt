// Package config holds tkt's own settings: the presets that choose an agent
// and the stage lines tkt run sends.
package config

import (
	"slices"
	"sort"
	"strconv"
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
	// Source names config.toml, or the built-in presets without one, for
	// messages.
	Source string
	// PresetFiles lists the repository's files whose presets go before
	// Source's, in the order read.
	PresetFiles []string
	// Shared and Local are the repository's .tkt/tkt.toml and
	// .tkt/tkt.local.toml, when tkt read them.
	Shared  string
	Local   string
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
	Watch         Watch
	Suggest       Suggest
	Verify        Verify
	Fixes         Fixes
	Start         Start
	// Chores are the repository's scheduled runs, by name.
	Chores []Chore
	// Status lists the status pages tkt watch reads, by name.
	Status []StatusPage
}

// Suggest holds the [suggest] settings for tkt suggest.
type Suggest struct {
	// As is the preset suggest-tickets runs on, such as a cheap one; empty
	// means the default preset.
	As string
}

// Verify holds the [verify] settings for the verify stage.
type Verify struct {
	// As is the preset verify-pr runs on, such as one from another vendor
	// than the one that built the change; empty means the ticket's own.
	As string
}

// Fixes holds the [fixes] settings for the small fixes: a failed sync, a
// conflict with the base, and failing checks.
type Fixes struct {
	// As is the preset those sessions run on, such as a cheap one; empty
	// means the ticket's own.
	As string
}

// Start holds the [start] settings for tkt start.
type Start struct {
	// Mode is how tkt start drives a ticket when no flag says: auto, where
	// tkt auto takes over once you approve the plan, or manual.
	Mode string
}

// The modes tkt start takes: manual drives nothing; auto drives the ticket
// once you approve its plan; auto-plan also approves the plan; yolo also
// merges, with nobody watching.
const (
	ModeManual   = "manual"
	ModeAuto     = "auto"
	ModeAutoPlan = "auto-plan"
	ModeYolo     = "yolo"
)

// FixStages are the stages [fixes] as runs on.
var FixStages = []string{"ci", "resolve", "sync-fix"}

// Watch holds tkt watch's settings.
type Watch struct {
	// Interval is the time between two passes over the tickets.
	Interval time.Duration
	// DoneAfterMerge runs tkt done once a pull request closes without
	// merging. Despite its name, nothing makes tkt watch run tkt done after
	// a merge; the key stays so files that set it still load.
	DoneAfterMerge bool
	// Autostart lets every tkt command in a herdr pane start tkt watch for
	// its repository when none runs.
	Autostart bool
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
	DefaultWatchEvery  = 60 * time.Second
)

// ApproveLine is what tkt approve sends to the planning session. It is not a
// stage: tkt run starts a fresh session, and the plan lives in this one.
const ApproveLine = "The plan is approved. Use the work-ticket skill to execute it."

// MergeLine is what tkt merge sends a fresh session once the human
// verification steps passed on the head verified: finish-ticket's explicit
// word to merge, with every step it requires confirmed on a head it can
// check. When the pull request moved past that head by clean merges of the
// base, the line names its head too; finish-ticket counts the steps there
// once it checked the merges itself (rogs-skills 421cfc7).
func MergeLine(steps []int, verified, head string) string {
	line := "Use the finish-ticket skill to merge the PR. Human verification " + StepsText(steps) + " passed on head " + verified + "."
	if head != verified {
		line += " The pull request is at " + head + " now, after clean merges of the base."
	}
	return line
}

// EvidenceMergeLine is the merge line for steps the user confirmed from
// verify-pr's evidence rather than by running them: finish-ticket then
// checks that verify-pr's report for that head marks each one PASSED.
func EvidenceMergeLine(steps []int, verified, head string) string {
	line := MergeLine(steps, verified, head)
	at := "passed on head " + verified + "."
	return strings.Replace(line, at, "passed on head "+verified+": the user confirmed them from verify-pr's evidence.", 1)
}

// WaivedMergeLine adds the steps the user waived to a merge line, with why:
// finish-ticket takes a step waived with a reason in place of a pass.
func WaivedMergeLine(line string, waived []int, reason string) string {
	if len(waived) == 0 {
		return line
	}
	return line + " The user waived " + StepsText(waived) + ": " + strings.TrimRight(reason, ".") + "."
}

// StepsText names steps for people: step 2, steps 1 and 2, steps 1 to 3,
// steps 1, 2 and 4.
func StepsText(steps []int) string {
	n := make([]string, len(steps))
	run := len(steps) > 2
	for i, s := range steps {
		n[i] = strconv.Itoa(s)
		run = run && (i == 0 || s == steps[i-1]+1)
	}
	switch {
	case len(n) == 1:
		return "step " + n[0]
	case run:
		return "steps " + n[0] + " to " + n[len(n)-1]
	case len(n) > 1:
		return "steps " + strings.Join(n[:len(n)-1], ", ") + " and " + n[len(n)-1]
	}
	return "steps"
}

// SuggestLine is what tkt suggest sends a fresh session at the repository's
// root: suggest-tickets picks the next tickets to start, ones that can run
// side by side.
func SuggestLine(n int) string {
	if n == 1 {
		return "Use the suggest-tickets skill to suggest the next ticket to start."
	}
	return "Use the suggest-tickets skill to suggest the next " + strconv.Itoa(n) + " tickets to start, ones that can run side by side."
}

// SuggestRequestLine is what tkt suggest sends in place of SuggestLine for a
// request in your own words, such as five tickets tagged feedback:
// suggest-tickets reads the count and any constraint from the request.
func SuggestRequestLine(request string) string {
	return "Use the suggest-tickets skill for this request: " + request
}

// UnattendedLine follows the start line when tkt auto approves the plan
// itself. start-ticket then skips the browser presentation and asks every
// open question with the harness's question tool, which stops the loop.
const UnattendedLine = "This run is unattended: no person watches this session until it asks a question."

// AutonomousLine follows every line tkt sends a ticket started with tkt auto
// --yolo: the skills then follow workflow.md's Autonomous runs section in
// place of every question, since nobody answers one.
const AutonomousLine = "This run is autonomous: no person reads this session or answers a question. Follow Autonomous runs in docs/agents/workflow.md wherever you would ask."

// AutonomousMergeLine is the merge line of a yolo ticket: the user
// authorized the merge when they started it, and verify-pr passed every
// step on the head verified, which finish-ticket counts as each step
// waived.
func AutonomousMergeLine(verified, head string) string {
	line := "Use the finish-ticket skill to merge the PR. " + AutonomousLine + " verify-pr passed every human verification step on head " + verified + "."
	if head != verified {
		line += " The pull request is at " + head + " now, after clean merges of the base."
	}
	return line
}

// BuiltinStages are the stage lines tkt run sends. {id} becomes the ticket,
// {remote} and {base} the forge's remote and base branch, and {reason} why
// the last sync failed.
var BuiltinStages = map[string]string{
	"start":   "Use the start-ticket skill to start ticket {id}.",
	"address": "Use the address-pr-review skill to address the review on this branch's pull request.",
	// sync is address under its old name: address-pr-review syncs with the
	// base first on every run (rogs-skills 26cfa58). tkt run warns that it
	// goes away; tkt sync replaces it.
	"sync": "Use the address-pr-review skill to address the review on this branch's pull request.",
	// resolve is the session tkt sync starts when the base merge conflicts.
	// It leaves the review's findings to address, so a sync never fixes
	// what nobody asked it to.
	"resolve": "Fetch {remote} and merge {remote}/{base} into this branch. The merge conflicts: resolve each conflict from what both sides meant, keep it a merge, and change nothing the merge does not need. Stage only the paths you resolved, and ask the user about any hunk that decides product behavior. Run the focused checks for the code it touched, commit the merge, and push once, never with force. Then refresh the Review brief in the pull request's body for the new head and base, when it has one, and end with Status: READY_FOR_REVIEW.",
	// sync-fix is the session tkt sync starts when a sync fails for any
	// other reason, such as a pre-push hook that refuses the push. {reason}
	// says why, and where git's whole output is.
	"sync-fix": "tkt sync could not bring this branch up to date with {remote}/{base}: {reason} Fetch {remote} and merge {remote}/{base} into this branch, fix the cause of that failure, such as a check a hook runs or a setup step it needs, and change nothing else. Keep it a merge, never bypass a hook, and push once, never with force. Then refresh the Review brief in the pull request's body for the new head and base, when it has one, and end with Status: READY_FOR_REVIEW. When the fix needs a person's decision, end with Status: BLOCKED and a Reason: line instead.",
	"verify":   "Use the verify-pr skill to verify the current PR.",
	"ci":       "The checks on this branch's pull request fail at its head. Find the cause and fix it, then push once. When a failure is infrastructure, such as an API quota error, rerun the job instead of changing code. End with Status: READY_FOR_REVIEW, or with Status: BLOCKED and a Reason: line when the fix needs a person.",
	"review":   "Use the review-pr skill to review the current PR.",
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
		Board:  Board{StaleAfter: DefaultStaleAfter},
		Watch:  Watch{Interval: DefaultWatchEvery, DoneAfterMerge: true, Autostart: true},
		Start:  Start{Mode: ModeAuto}}
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

// StageLine returns a stage's line for a ticket: {id} becomes the ticket,
// and vars, as name and value pairs, fill other placeholders, such as
// "base", "main" for {base}.
func (c Config) StageLine(stage, id string, vars ...string) (string, error) {
	t, ok := c.Stages[stage]
	if !ok || t == "" {
		return "", apperr.New(apperr.UnknownStage, "unknown stage '%s' (stages: %s)", stage, strings.Join(c.StageNames(), " ")).
			WithHint("%s", suggestion(stage, c.StageNames()))
	}
	pairs := []string{"{id}", id}
	for i := 0; i+1 < len(vars); i += 2 {
		pairs = append(pairs, "{"+vars[i]+"}", vars[i+1])
	}
	return strings.NewReplacer(pairs...).Replace(t), nil
}

// TabLabel names a ticket's tab "ID (preset)", so the tab says what runs
// in it, the default preset too.
func (c Config) TabLabel(id, preset string) string {
	return id + " (" + preset + ")"
}
