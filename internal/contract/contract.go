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
	TrackerFile  = "docs/agents/issue-tracker.md"
	ForgeFile    = "docs/agents/forge.md"
	WorkflowFile = "docs/agents/workflow.md"
	TrackerID    = "issue-tracker"
	ForgeID      = "forge"
	WorkflowID   = "workflow"
)

// The rows of workflow.md's feature table that tkt reads. A row reads
// "| Name | on |"; a missing row means off. tkt runs verify-pr after a clean
// review only with agent verification on, and tkt auto --yolo needs all
// three on.
const (
	FeatureAgentVerification = "Agent verification"
	FeatureAutonomousRuns    = "Autonomous runs"
	FeaturePlanReview        = "Plan review"
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
	StatusAwaitingApproval = "AWAITING_APPROVAL"
	// StatusPlanApproved marks the moment an approval started the work
	// phase in start-ticket's session; the run goes on after it.
	StatusPlanApproved         = "PLAN_APPROVED"
	StatusBlocked              = "BLOCKED"
	StatusSetupRequired        = "SETUP_REQUIRED"
	StatusReadyForReview       = "READY_FOR_REVIEW"
	StatusBlockedForSync       = "BLOCKED_FOR_SYNC"
	StatusHumanQAFailed        = "HUMAN_QA_FAILED"
	StatusAwaitingPostApproval = "AWAITING_POST_APPROVAL"
	// StatusDone ends a finish-ticket run that merged the pull request,
	// saw post-merge CI pass and moved the ticket to done.
	StatusDone = "DONE"
	// StatusBlockedPostMerge ends one whose merge landed but whose
	// post-merge CI failed.
	StatusBlockedPostMerge = "BLOCKED_POST_MERGE"
	// verify-pr ends with one of these, after posting its report.
	StatusVerified               = "VERIFIED"
	StatusVerificationFailed     = "VERIFICATION_FAILED"
	StatusVerificationIncomplete = "VERIFICATION_INCOMPLETE"
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

// verify-pr's report, a comment-only review: the head it ran the human
// verification steps on, one line per step, and the outcome. It holds no
// Verdict: line, so nothing reads it as a review summary.
var (
	VerificationRE = regexp.MustCompile(`(?m)^Verification: (PASSED|FAILED|INCOMPLETE)[ \t]*$`)
	VerifiedHeadRE = regexp.MustCompile("(?m)^Verified head: `?([0-9a-f]{7,40})")
	VerifiedStepRE = regexp.MustCompile(`(?m)^(\d+)\. (PASSED|FAILED|NOT RUN): (.*)$`)
)

// The outcomes of a verify-pr run, and of each of its steps.
const (
	VerificationPassed     = "PASSED"
	VerificationFailed     = "FAILED"
	VerificationIncomplete = "INCOMPLETE"
	StepNotRun             = "NOT RUN"
)

// UnblockedRE finds the line finish-ticket's report gives right before
// Status: DONE: the tickets the merge unblocked, whose blockers are all
// done now, or none.
var UnblockedRE = regexp.MustCompile(`(?m)^Unblocked: (.*)$`)

// HumanVerificationRE finds the section that follows review-pr's
// READY_FOR_HUMAN summary and holds the numbered procedure a person runs.
var HumanVerificationRE = regexp.MustCompile(`(?m)^## Human verification[ \t]*$`)

// Skills names the lifecycle skills tkt's built-in stage lines, and the
// lines tkt approve, tkt merge and tkt suggest send, ask for.
var Skills = []string{"start-ticket", "work-ticket", "address-pr-review", "review-pr", "verify-pr", "finish-ticket", "suggest-tickets"}

// CleanMergeRule is how review-pr's rule about a review brief across clean
// base merges begins. tkt sync relies on it: it pushes such merges with no
// agent, and leaves the brief as it was.
const CleanMergeRule = "A brief pinned to an earlier head stays current when every commit after that head is a clean merge of the base"

// CarryOverRule is how finish-ticket's rule about human verification across
// clean base merges begins. tkt merge relies on it: the merge line names the
// head the steps passed on, and a merge order holds across the clean merges
// tkt sync makes.
const CarryOverRule = "A confirmation counts for the head it names"

// Autonomous is the word the lifecycle skills' "Autonomous runs" sections
// look for in their request: the run follows workflow.md's Autonomous runs
// section in place of every question, and finish-ticket merges on
// verify-pr's passing report when the ticket carries start-ticket's
// "## Autonomous run" comment.
const Autonomous = "autonomous"

// Unattended is the word start-ticket's "Unattended runs" section looks for
// in its request: the run skips the browser presentation, and every open
// question goes through the harness's question tool.
const Unattended = "unattended"
