// Package contract holds what tkt reads from the rogs-skills lifecycle: the
// configuration contract's files and markers, the status lines agents end
// with, and the verdict lines review-pr posts. tkt reads no skill files at
// run time; testdata/ keeps verbatim copies of the upstream text, and
// testdata/SOURCES.md says where each one comes from.
package contract

import (
	"regexp"
	"strconv"
)

// Version is the configuration contract version tkt reads, as defined in
// gitlab.com/rogs/skills skills/lifecycle/setup-rogs/references/contract.md.
const Version = 1

// The lifecycle configuration files, relative to the primary worktree.
const (
	TrackerFile = "docs/agents/issue-tracker.md"
	ForgeFile   = "docs/agents/forge.md"
	TrackerID   = "issue-tracker"
	ForgeID     = "forge"
)

var markerRE = regexp.MustCompile(`^<!-- rogs-skills config v(\d+): (\S+) -->$`)

// ParseMarker reads a configuration file's first line.
func ParseMarker(line string) (version int, id string, ok bool) {
	m := markerRE.FindStringSubmatch(line)
	if m == nil {
		return 0, "", false
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", false
	}
	return v, m[2], true
}

// The status lines tkt branches on. The CLAUDE.md "Status lines" registry in
// the skills repository lists every line a lifecycle skill emits.
const (
	StatusAwaitingApproval     = "AWAITING_APPROVAL"
	StatusBlocked              = "BLOCKED"
	StatusSetupRequired        = "SETUP_REQUIRED"
	StatusReadyForReview       = "READY_FOR_REVIEW"
	StatusBlockedForSync       = "BLOCKED_FOR_SYNC"
	StatusHumanQAFailed        = "HUMAN_QA_FAILED"
	StatusAwaitingPostApproval = "AWAITING_POST_APPROVAL"
)

// StatusLineRE finds a status line on an agent's screen.
var StatusLineRE = regexp.MustCompile(`Status: ([A-Z_]+)`)

// Review summaries. review-pr ends its summary with a Verdict: line and
// names the head it reviewed; an automated reviewer on the forge writes one
// "Automated review:" line instead.
var (
	AutomatedReviewRE = regexp.MustCompile("Automated review: (CLEAR|FINDINGS|STALE_BASE)")
	AutomatedHeadRE   = regexp.MustCompile("head `([0-9a-f]{7,40})`")
	VerdictRE         = regexp.MustCompile(`(?m)^Verdict: (CHANGES_REQUESTED|READY_FOR_HUMAN)`)
	ReviewedHeadRE    = regexp.MustCompile("(?m)^Reviewed head: `?([0-9a-f]{7,40})")
)

// The verdicts, from either kind of review.
const (
	VerdictClear            = "CLEAR"
	VerdictFindings         = "FINDINGS"
	VerdictStaleBase        = "STALE_BASE"
	VerdictChangesRequested = "CHANGES_REQUESTED"
	VerdictReadyForHuman    = "READY_FOR_HUMAN"
)

// HumanVerificationRE finds the section that follows review-pr's
// READY_FOR_HUMAN summary and holds the numbered procedure a person runs.
var HumanVerificationRE = regexp.MustCompile(`(?m)^## Human verification[ \t]*$`)

// Skills names the lifecycle skills tkt's built-in stage lines, and the line
// tkt approve sends, ask for.
var Skills = []string{"start-ticket", "work-ticket", "address-pr-review", "review-pr"}

// CleanMergeRule is how review-pr's rule about a review brief across clean
// base merges begins. tkt sync relies on it: it pushes such merges with no
// agent, and leaves the brief as it was.
const CleanMergeRule = "A brief pinned to an earlier head stays current when every commit after that head is a clean merge of the base"

// ResolveSkill is the engineering skill the resolve stage line asks for. It
// resolves a merge that stopped on conflicts, keeps it a merge, and asks the
// user about a hunk that decides product behavior.
const ResolveSkill = "resolving-merge-conflicts"

// Unattended is the word start-ticket's "Unattended runs" section looks for
// in its request: the run skips the browser presentation, and every open
// question goes through the harness's question tool.
const Unattended = "unattended"
