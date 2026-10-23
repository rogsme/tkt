package contract_test

import (
	"os"
	"path/filepath"
	"regexp"
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
	for _, s := range []string{contract.StatusAwaitingApproval, contract.StatusBlocked, contract.StatusSetupRequired,
		contract.StatusReadyForReview, contract.StatusBlockedForSync, contract.StatusHumanQAFailed, contract.StatusAwaitingPostApproval, contract.StatusDone} {
		if !strings.Contains(registry, "`Status: "+s+"`") {
			t.Errorf("Status: %s is not in the registry", s)
		}
	}
	for _, v := range []string{contract.VerdictChangesRequested, contract.VerdictReadyForHuman} {
		if !strings.Contains(registry, "`Verdict: "+v+"`") {
			t.Errorf("Verdict: %s is not in the registry", v)
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

func TestStageLinesNameRegisteredSkills(t *testing.T) {
	registry := read(t, "status-lines.md")
	for _, skill := range contract.Skills {
		if !strings.Contains(registry, skill) {
			t.Errorf("%s does not appear in the status-line registry", skill)
		}
	}
	named := regexp.MustCompile(`the ([a-z]+(?:-[a-z]+)+) skill`)
	lines := map[string]string{"approve": config.ApproveLine, "merge": config.MergeLine([]int{1})}
	for stage, line := range config.BuiltinStages {
		lines[stage] = line
	}
	for stage, line := range lines {
		for _, m := range named.FindAllStringSubmatch(line, -1) {
			if m[1] == "playwright-cli" || m[1] == contract.ResolveSkill {
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

func TestResolveStageNamesTheMergeConflictSkill(t *testing.T) {
	skill := read(t, "resolving-merge-conflicts.md")
	if !strings.Contains(skill, "\nname: "+contract.ResolveSkill+"\n") {
		t.Fatalf("the fixture is not the %s skill", contract.ResolveSkill)
	}
	// The resolve line relies on these: the skill takes a merge another
	// party started, stays a merge, and stops to ask about product behavior.
	for _, want := range []string{"another skill hands over a conflicted merge", "A workflow that syncs by merging never turns a merge into a rebase.", "stop and ask the user", "git commit --no-edit"} {
		if !strings.Contains(skill, want) {
			t.Errorf("%s no longer says %q", contract.ResolveSkill, want)
		}
	}
	if !strings.Contains(config.BuiltinStages["resolve"], "the "+contract.ResolveSkill+" skill") {
		t.Errorf("the resolve stage does not name the %s skill: %s", contract.ResolveSkill, config.BuiltinStages["resolve"])
	}
}

func TestCleanMergesKeepTheReviewBrief(t *testing.T) {
	rule := read(t, "review-pr-clean-merge.md")
	if !strings.HasPrefix(rule, contract.CleanMergeRule) {
		t.Fatalf("review-pr's clean-merge rule changed:\n%s", rule)
	}
	// tkt sync makes exactly this merge: its second parent is the base, and
	// it checks the merge's tree against git merge-tree before it pushes.
	for _, want := range []string{"second parent is an ancestor of the base", "`git merge-tree --write-tree <merge>^1 <merge>^2`", "check each one yourself"} {
		if !strings.Contains(rule, want) {
			t.Errorf("review-pr's clean-merge rule no longer says %q", want)
		}
	}
}

func TestMergeLineIsFinishTicketsExplicitWord(t *testing.T) {
	skill := read(t, "finish-ticket-authorization.md")
	line := config.MergeLine([]int{1, 2, 3})
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
