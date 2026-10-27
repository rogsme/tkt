package contract_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/lifecycle"
)

// The fixtures in testdata/ copy text the skills repository produces; see
// testdata/SOURCES.md.

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetupSeedsParse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs/agents")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "issue-tracker.md"), []byte(read(t, "issue-tracker.md")), 0o644)
	os.WriteFile(filepath.Join(dir, "forge.md"), []byte(read(t, "forge.md")), 0o644)
	c, err := lifecycle.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := lifecycle.Forge{Kind: "GitHub", Host: "github.com", Repo: "acme/app", Remote: "origin", Base: "main"}
	if c.Forge != want || c.Prefix != "ACM" {
		t.Errorf("forge %+v prefix %q", c.Forge, c.Prefix)
	}
	if id, err := c.NormalizeID("12"); err != nil || id != "ACM-12" {
		t.Errorf("NormalizeID: %q, %v", id, err)
	}
}

func TestReviewSummariesCarryVerdicts(t *testing.T) {
	head := strings.Repeat("a1", 20)
	for name, want := range map[string]string{
		"summary-changes-requested.txt": contract.VerdictChangesRequested,
		"summary-ready.txt":             contract.VerdictReadyForHuman,
	} {
		body := strings.ReplaceAll(read(t, name), "<full SHA>", head)
		p := forge.PR{HeadRefOid: head, Reviews: []forge.Review{{Body: body, SubmittedAt: "2026-01-01T00:00:00Z"}}}
		v := forge.LatestVerdict(p)
		if v == nil || v.Value != want || !v.AtHead {
			t.Errorf("%s: %+v", name, v)
		}
	}
	p := forge.PR{HeadRefOid: "97c7506ffff", Reviews: []forge.Review{{Body: read(t, "automated-review.txt")}}}
	if v := forge.LatestVerdict(p); v == nil || v.Value != contract.VerdictFindings || !v.AtHead {
		t.Errorf("automated review: %+v", v)
	}
}

func TestStatusLinesTktReadsAreRegistered(t *testing.T) {
	registry := read(t, "status-lines.md")
	for _, s := range []string{contract.StatusAwaitingApproval, contract.StatusPlanApproved, contract.StatusBlocked, contract.StatusSetupRequired,
		contract.StatusReadyForReview, contract.StatusBlockedForSync, contract.StatusHumanQAFailed, contract.StatusAwaitingPostApproval, contract.StatusDone,
		contract.StatusBlockedPostMerge, contract.StatusVerified, contract.StatusVerificationFailed, contract.StatusVerificationIncomplete} {
		if !strings.Contains(registry, "`Status: "+s+"`") {
			t.Errorf("Status: %s is not in the registry", s)
		}
	}
	for _, v := range []string{contract.VerdictChangesRequested, contract.VerdictReadyForHuman} {
		if !strings.Contains(registry, "`Verdict: "+v+"`") {
			t.Errorf("Verdict: %s is not in the registry", v)
		}
	}
	for _, v := range []string{contract.VerificationPassed, contract.VerificationFailed, contract.VerificationIncomplete} {
		if !strings.Contains(registry, "`Verification: "+v+"`") {
			t.Errorf("Verification: %s is not in the registry", v)
		}
	}
}

// verifyReport fills the report fixture for head with an outcome.
func verifyReport(t *testing.T, head, outcome string) string {
	t.Helper()
	return strings.NewReplacer("<full SHA>", head, "<PASSED | FAILED | INCOMPLETE>", outcome).Replace(read(t, "verify-pr-report.txt"))
}

func TestVerifyReportCarriesItsHeadAndSteps(t *testing.T) {
	head := strings.Repeat("d4", 20)
	for _, outcome := range []string{contract.VerificationPassed, contract.VerificationFailed, contract.VerificationIncomplete} {
		p := forge.PR{HeadRefOid: head, Reviews: []forge.Review{{Body: verifyReport(t, head, outcome), SubmittedAt: "2026-01-01T00:00:00Z"}}}
		v := forge.LatestVerification(p)
		if v == nil || v.Value != outcome || !v.AtHead || !v.Holds() {
			t.Fatalf("%s: %+v", outcome, v)
		}
		want := []string{contract.VerificationPassed, contract.VerificationFailed, contract.StepNotRun}
		if len(v.Steps) != 3 {
			t.Fatalf("steps: %+v", v.Steps)
		}
		for i, s := range v.Steps {
			if s.N != i+1 || s.Result != want[i] || s.Text == "" {
				t.Errorf("step %d: %+v", i+1, s)
			}
		}
		// Nothing reads the report as a review summary, or its steps as
		// the procedure a person runs.
		if vd := forge.LatestVerdict(p); vd != nil {
			t.Errorf("%s: the report reads as a verdict: %+v", outcome, vd)
		}
		if proc := forge.HumanVerification(p); proc != nil {
			t.Errorf("%s: the report reads as a procedure: %+v", outcome, proc)
		}
	}
	// A report on an older head does not hold by itself.
	p := forge.PR{HeadRefOid: strings.Repeat("e5", 20), Reviews: []forge.Review{{Body: verifyReport(t, head, contract.VerificationPassed)}}}
	if v := forge.LatestVerification(p); v == nil || v.AtHead || v.Holds() {
		t.Errorf("older head: %+v", v)
	}
}

func TestWorkflowFeatureRowsTurnFeaturesOn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs/agents")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "issue-tracker.md"), []byte(read(t, "issue-tracker.md")), 0o644)
	os.WriteFile(filepath.Join(dir, "forge.md"), []byte(read(t, "forge.md")), 0o644)
	features := []string{contract.FeatureAgentVerification, contract.FeatureAutonomousRuns, contract.FeaturePlanReview, contract.FeatureCIReview}
	// Without workflow.md, as in an older configuration, every feature is
	// off.
	if c, err := lifecycle.Load(root); err != nil || c.On(contract.FeatureAgentVerification) {
		t.Fatalf("no workflow.md: %v", err)
	}
	table := read(t, "workflow-features.md")
	for _, f := range features {
		if !strings.Contains(table, "| "+f+" | {{FILL:") {
			t.Errorf("workflow.md's feature table has no %s row", f)
		}
	}
	fill := regexp.MustCompile(`\{\{FILL:[^}]*\}\}`)
	for _, state := range []string{"on", "off"} {
		body := "<!-- rogs-skills config v1: workflow -->\n# Workflow\n\n" + fill.ReplaceAllString(table, state)
		os.WriteFile(filepath.Join(dir, "workflow.md"), []byte(body), 0o644)
		c, err := lifecycle.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range features {
			if c.On(f) != (state == "on") {
				t.Errorf("%s with every row %s: %v", f, state, c.On(f))
			}
		}
	}
}

func TestCIReviewMakesTheBotsClearCount(t *testing.T) {
	seed := read(t, "workflow-ci-review.md")
	for _, want := range []string{
		"## CI review",
		"`Automated review: CLEAR`",
		"counts as `review-pr`'s summary with `Verdict: READY_FOR_HUMAN` for that head",
		"the pull request body's `## Human verification` section",
		"A runner never starts `review-pr` here.",
	} {
		if !strings.Contains(seed, want) {
			t.Errorf("the CI review section no longer says %q", want)
		}
	}
	// The verdict line the section names is the one tkt reads.
	if v := contract.AutomatedReviewRE.FindStringSubmatch("**Automated review: CLEAR** · head `abc1234`"); v == nil || v[1] != contract.VerdictClear {
		t.Errorf("AutomatedReviewRE no longer reads the CI reviewer's CLEAR")
	}
	finish := read(t, "finish-ticket-ci-review.md")
	for _, want := range []string{"`Automated review: CLEAR` for that same head SHA is the final review summary", "require it to be an ancestor of the reviewed head"} {
		if !strings.Contains(finish, want) {
			t.Errorf("finish-ticket no longer says %q", want)
		}
	}
}

func TestStartTicketMarksTheWorkPhaseBegun(t *testing.T) {
	text := read(t, "start-ticket-plan-approved.md")
	for _, want := range []string{
		"with the viewer's Approve button",
		"print `Status: " + contract.StatusPlanApproved + "` on a line of its own before the work phase begins",
		"the run goes on after it",
		// An approval starts work-ticket, so its start marks the approval.
		"Approve revision N and start work-ticket?",
		"treat that event as the user's message approving the revision and starting the work phase",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("start-ticket no longer says %q:\n%s", want, text)
		}
	}
}

func TestStartsWork(t *testing.T) {
	for skill, want := range map[string]bool{"work-ticket": true, "rogs:work-ticket": true, "start-ticket": false, "work-ticket-x": false} {
		if got := contract.StartsWork(skill); got != want {
			t.Errorf("StartsWork(%q) = %v", skill, got)
		}
	}
}

func TestUnattendedLineSaysTheWordStartTicketReads(t *testing.T) {
	section := read(t, "start-ticket-unattended.md")
	if !strings.Contains(section, "An "+contract.Unattended+" run is one whose request says so.") {
		t.Errorf("start-ticket's section no longer keys on %q:\n%s", contract.Unattended, section)
	}
	if !strings.Contains(section, "question tool") {
		t.Errorf("start-ticket's section no longer asks through the question tool:\n%s", section)
	}
	if !strings.Contains(config.UnattendedLine, "This run is "+contract.Unattended) {
		t.Errorf("config.UnattendedLine %q does not say the run is %s", config.UnattendedLine, contract.Unattended)
	}
}

func TestAutonomousLineSaysTheWordsTheSkillsRead(t *testing.T) {
	request := `whose request says "This run is ` + contract.Autonomous + `"`
	for _, name := range []string{"start-ticket-autonomous.md", "finish-ticket-autonomous.md", "workflow-autonomous-runs.md"} {
		if !strings.Contains(read(t, name), request) {
			t.Errorf("%s no longer keys on %s", name, request)
		}
	}
	if !strings.HasPrefix(config.AutonomousLine, "This run is "+contract.Autonomous+":") {
		t.Errorf("config.AutonomousLine %q does not say the run is %s", config.AutonomousLine, contract.Autonomous)
	}
	// It names the section the skills follow in place of asking.
	if !strings.Contains(config.AutonomousLine, "Autonomous runs in "+contract.WorkflowFile) {
		t.Errorf("config.AutonomousLine %q does not name workflow.md's section", config.AutonomousLine)
	}
	// A yolo run needs these on, which tkt auto --yolo checks first.
	if !strings.Contains(read(t, "workflow-autonomous-runs.md"), "An autonomous run needs plan review and agent verification on") {
		t.Errorf("workflow.md's section no longer names the features a run needs")
	}
}

func TestAutonomousMergeLineIsTheAuthorizationFinishTicketTakes(t *testing.T) {
	start, finish := read(t, "start-ticket-autonomous.md"), read(t, "finish-ticket-autonomous.md")
	// start-ticket posts the comment that makes the ticket autonomous, and
	// finish-ticket merges on the request only when the comment exists.
	if !strings.Contains(start, "post an `## Autonomous run` comment on the lead") {
		t.Errorf("start-ticket no longer posts the Autonomous run comment")
	}
	for _, want := range []string{
		"The authorization is the request, and it holds only when the lead carries the `## Autonomous run` comment",
		"require a `verify-pr` report that says `Verification: PASSED` for the head the request names",
		"under the same carry-over rule a confirmation follows",
		"Each numbered step then counts as waived",
		"`Status: BLOCKED_FOR_SYNC`",
	} {
		if !strings.Contains(finish, want) {
			t.Errorf("finish-ticket's autonomous runs no longer say %q", want)
		}
	}
	verified, head := strings.Repeat("a1", 20), strings.Repeat("b2", 20)
	line := config.AutonomousMergeLine(verified, verified)
	for _, want := range []string{"Use the finish-ticket skill to merge the PR.", config.AutonomousLine, "verify-pr passed every human verification step on head " + verified + "."} {
		if !strings.Contains(line, want) {
			t.Errorf("the autonomous merge line %q does not say %q", line, want)
		}
	}
	if line := config.AutonomousMergeLine(verified, head); !strings.HasSuffix(line, "The pull request is at "+head+" now, after clean merges of the base.") {
		t.Errorf("the autonomous merge line %q does not name both heads", line)
	}
	// It never says the run is unattended, which would have start-ticket
	// ask its questions after all.
	if strings.Contains(line, contract.Unattended) || strings.Contains(config.AutonomousLine, contract.Unattended) {
		t.Errorf("the autonomous lines say %s", contract.Unattended)
	}
}

func TestStageLinesNameRegisteredSkills(t *testing.T) {
	registry := read(t, "status-lines.md")
	for _, skill := range contract.Skills {
		if !strings.Contains(registry, skill) {
			t.Errorf("%s does not appear in the status-line registry", skill)
		}
	}
	named := regexp.MustCompile(`the ([a-z]+(?:-[a-z]+)+) skill`)
	lines := map[string]string{"approve": config.ApproveLine, "merge": config.MergeLine([]int{1}, "aaa", "aaa"), "suggest": config.SuggestLine(3),
		"suggest request": config.SuggestRequestLine("five tickets tagged feedback")}
	for stage, line := range config.BuiltinStages {
		lines[stage] = line
	}
	for stage, line := range lines {
		for _, m := range named.FindAllStringSubmatch(line, -1) {
			if m[1] == "playwright-cli" {
				continue
			}
			found := false
			for _, s := range contract.Skills {
				found = found || s == m[1]
			}
			if !found {
				t.Errorf("stage %s names %s, which contract.Skills does not list", stage, m[1])
			}
		}
	}
}

func TestReadySummaryCarriesHumanVerification(t *testing.T) {
	head := strings.Repeat("b2", 20)
	body := strings.ReplaceAll(read(t, "summary-ready-verification.txt"), "<full SHA>", head)
	p := forge.PR{HeadRefOid: head, Reviews: []forge.Review{{Body: body, SubmittedAt: "2026-01-01T00:00:00Z"}}}
	proc := forge.HumanVerification(p)
	if proc == nil || len(proc.Steps) != 3 {
		t.Fatalf("procedure: %+v", proc)
	}
	if s := proc.Steps[0]; s.N != 1 || !strings.HasPrefix(s.Text, "Open http://localhost:3000/login") || !strings.Contains(s.Text, "Expected: the form shows") {
		t.Errorf("step 1: %+v", s)
	}
	if s := proc.Steps[2]; strings.Contains(s.Text, "Cleanup") {
		t.Errorf("step 3 took the cleanup paragraph: %q", s.Text)
	}
	if !strings.HasPrefix(proc.Section, "Setup:") || !strings.Contains(proc.Section, "Not verifiable here") {
		t.Errorf("section: %q", proc.Section)
	}
	// A verdict on an older head has no steps to run.
	p.HeadRefOid = strings.Repeat("c3", 20)
	if proc := forge.HumanVerification(p); proc != nil {
		t.Errorf("old head: %+v", proc)
	}
}

func TestCleanMergesKeepTheReviewBrief(t *testing.T) {
	rule := read(t, "review-pr-clean-merge.md")
	if !strings.HasPrefix(rule, contract.CleanMergeRule) {
		t.Fatalf("review-pr's clean-merge rule changed:\n%s", rule)
	}
	// tkt sync makes exactly this merge: its second parent is the base, and
	// it checks the merge's tree against git merge-tree before it pushes.
	for _, want := range []string{"second parent is an ancestor of the base", "`git merge-tree --write-tree <merge>^1 <merge>^2`", "`git rev-list --first-parent <that head>..<the current head>`", "check each one yourself"} {
		if !strings.Contains(rule, want) {
			t.Errorf("review-pr's clean-merge rule no longer says %q", want)
		}
	}
}

func TestMergeLineIsFinishTicketsExplicitWord(t *testing.T) {
	skill := read(t, "finish-ticket-authorization.md")
	head := strings.Repeat("a1", 20)
	line := config.MergeLine([]int{1, 2, 3}, head, head)
	// The skill runs only on the user's explicit word, which the line says.
	if !strings.Contains(skill, `Use only when the user explicitly says \"merge the PR\"`) {
		t.Fatalf("finish-ticket no longer runs on \"merge the PR\":\n%s", skill)
	}
	if !strings.Contains(line, "merge the PR") {
		t.Errorf("the merge line %q does not say merge the PR", line)
	}
	// It merges only with every numbered step confirmed as passed, so the
	// line names them all.
	if !strings.Contains(skill, "Every numbered human step explicitly confirmed as passed by the user") {
		t.Errorf("finish-ticket no longer asks for every step:\n%s", skill)
	}
	if !strings.Contains(line, "steps 1 to 3 passed") {
		t.Errorf("the merge line %q does not confirm the steps", line)
	}
	// It stops when the base moved after the review, which tkt merge
	// checks first.
	if !strings.Contains(skill, "When the base moved at all after the review, stop with `Status: "+contract.StatusBlockedForSync+"`") {
		t.Errorf("finish-ticket no longer stops on a moved base:\n%s", skill)
	}
}

func TestEvidenceLineIsAConfirmationFinishTicketTakes(t *testing.T) {
	skill := read(t, "finish-ticket-authorization.md")
	// The user may confirm steps from verify-pr's evidence; finish-ticket
	// then checks the report for that head, which tkt merge --evidence
	// checked first.
	for _, want := range []string{"The user may confirm steps from the evidence `verify-pr` posted instead of running them", "When the merge instruction says so, require a `verify-pr` report for the confirmed head that marks each of those steps `PASSED`"} {
		if !strings.Contains(skill, want) {
			t.Errorf("finish-ticket no longer says %q", want)
		}
	}
	head := strings.Repeat("a1", 20)
	line := config.EvidenceMergeLine([]int{1, 2}, head, head)
	for _, want := range []string{"merge the PR", "steps 1 and 2 passed on head " + head, "the user confirmed them from verify-pr's evidence"} {
		if !strings.Contains(line, want) {
			t.Errorf("the evidence line %q does not say %q", line, want)
		}
	}
}

func TestStepsCarryOverCleanMerges(t *testing.T) {
	skill := read(t, "finish-ticket-authorization.md")
	if !strings.Contains(skill, "\n"+contract.CarryOverRule) {
		t.Fatalf("finish-ticket's carry-over rule changed:\n%s", skill)
	}
	// A confirmation counts for the head it names, which the line does, and
	// carries over the clean merges tkt sync makes, which gitx.CleanMerges
	// checks before tkt merge names the pull request's head too.
	for _, want := range []string{"A confirmation given on an earlier head still counts when every commit after that head is a clean merge of the base", "voids the confirmations given before it"} {
		if !strings.Contains(skill, want) {
			t.Errorf("finish-ticket no longer says %q", want)
		}
	}
	verified, head := strings.Repeat("a1", 20), strings.Repeat("b2", 20)
	if line := config.MergeLine([]int{1}, verified, verified); !strings.Contains(line, "passed on head "+verified+".") {
		t.Errorf("the merge line %q does not name the head the steps passed on", line)
	}
	if line := config.MergeLine([]int{1}, verified, head); !strings.Contains(line, "passed on head "+verified+".") || !strings.Contains(line, head+" now, after clean merges of the base") {
		t.Errorf("the merge line %q does not name both heads", line)
	}
	// review-pr and finish-ticket define a clean merge in the same words.
	def := func(s string) string {
		i := strings.Index(s, "a merge commit whose second parent")
		j := strings.Index(s, "through each merge's second parent.")
		if i < 0 || j < i {
			return ""
		}
		return strings.ReplaceAll(s[i:j], "; check each one yourself", "")
	}
	if a, b := def(skill), def(read(t, "review-pr-clean-merge.md")); a == "" || a != b {
		t.Errorf("finish-ticket and review-pr define a clean merge differently:\n%s\n%s", a, b)
	}
}

func TestUnblockedLineNamesTheTicketsAMergeFreed(t *testing.T) {
	skill := read(t, "finish-ticket-unblocked.md")
	// The report names them on one line, right before the status line.
	i, j := strings.Index(skill, "`Unblocked: ACM-15, ACM-16`"), strings.Index(skill, "`Status: "+contract.StatusDone+"`")
	if i < 0 || j < i || !strings.Contains(skill, "`Unblocked: none`") {
		t.Fatalf("finish-ticket's report no longer ends with an Unblocked line:\n%s", skill)
	}
	if !strings.Contains(skill, "it is unblocked when every one of them is now `done`") {
		t.Errorf("finish-ticket no longer names only tickets whose blockers are all done")
	}
	c := lifecycle.Config{RE: regexp.MustCompile(`^ACM-[1-9][0-9]*$`)}
	for text, want := range map[string][]string{
		"Merged.\nUnblocked: ACM-15, ACM-16\nStatus: DONE": {"ACM-15", "ACM-16"},
		"Unblocked: none\nStatus: DONE":                    {},
		"Unblocked: `ACM-15` (2 blockers done)":            {"ACM-15"},
	} {
		m := contract.UnblockedRE.FindStringSubmatch(text)
		if m == nil {
			t.Errorf("%q: no Unblocked line", text)
			continue
		}
		if got := c.IDs(m[1]); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}
