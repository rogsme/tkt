package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/rogs/tkt/internal/apperr"
)

func project(t *testing.T, tracker, forge string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs/agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if tracker != "" {
		os.WriteFile(filepath.Join(dir, "issue-tracker.md"), []byte(tracker), 0o644)
	}
	if forge != "" {
		os.WriteFile(filepath.Join(dir, "forge.md"), []byte(forge), 0o644)
	}
	return root
}

const tracker = "<!-- rogs-skills config v1: issue-tracker -->\n# Issue tracker\n\n## Tracker\n\n- Kind: Example tracker\n- Ticket ids: `ACM-<number>`, matched by `^ACM-[1-9][0-9]*$`.\n"
const forge = "<!-- rogs-skills config v1: forge -->\n# Forge\n\n## Forge\n\n- Kind: GitHub\n- Host: github.com\n- Repository: acme/app\n- Remote: origin\n- Base branch: main; re-read with: `gh repo view`\n\n## Operations\n\n- Remote: elsewhere\n"

func code(err error) apperr.Code {
	if err == nil {
		return ""
	}
	return apperr.As(err).Code
}

func TestLoadReadsTrackerAndForge(t *testing.T) {
	c, err := Load(project(t, tracker, forge))
	if err != nil {
		t.Fatal(err)
	}
	if c.Pattern != "^ACM-[1-9][0-9]*$" || c.Prefix != "ACM" {
		t.Errorf("pattern %q prefix %q", c.Pattern, c.Prefix)
	}
	want := Forge{Kind: "GitHub", Host: "github.com", Repo: "acme/app", Remote: "origin", Base: "main"}
	if c.Forge != want {
		t.Errorf("forge %+v", c.Forge)
	}
}

func TestLoadRefusals(t *testing.T) {
	cases := []struct {
		name, tracker, forge string
		want                 apperr.Code
	}{
		{"missing forge", tracker, "", apperr.LifecycleConfigMissing},
		{"wrong marker", "# Issue tracker\n" + tracker, forge, apperr.LifecycleConfigMarker},
		{"older contract", "<!-- rogs-skills config v0: issue-tracker -->\n", forge, apperr.LifecycleConfigVersion},
		{"newer contract", tracker, "<!-- rogs-skills config v2: forge -->\n", apperr.LifecycleConfigVersion},
		{"no pattern", "<!-- rogs-skills config v1: issue-tracker -->\n- Ticket ids: ACM-1\n", forge, apperr.TicketPatternMissing},
		{"bad pattern", "<!-- rogs-skills config v1: issue-tracker -->\n- Ticket ids: `^ACM-(?<n>[0-9]+$`\n", forge, apperr.TicketPatternInvalid},
		{"no base", tracker, "<!-- rogs-skills config v1: forge -->\n## Forge\n\n- Remote: origin\n", apperr.ForgeConfigIncomplete},
	}
	for _, tc := range cases {
		_, err := Load(project(t, tc.tracker, tc.forge))
		if code(err) != tc.want {
			t.Errorf("%s: got %v (%s), want %s", tc.name, err, code(err), tc.want)
		}
	}
}

func TestNormalizeID(t *testing.T) {
	c, err := Load(project(t, tracker, forge))
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"acm-12": "ACM-12", "12": "ACM-12", "ACM-12": "ACM-12"} {
		if got, err := c.NormalizeID(in); got != want || err != nil {
			t.Errorf("NormalizeID(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := c.NormalizeID("ACM-0"); code(err) != apperr.InvalidTicketID {
		t.Errorf("ACM-0: %v", err)
	}
}

func TestFirstID(t *testing.T) {
	c, _ := Load(project(t, tracker, forge))
	cases := map[string]string{
		"See ACM-1700x, then **ACM-17** and ACM-18 (rider).": "ACM-17",
		"ACM-9's worktree": "ACM-9",
		"no ticket here":   "",
	}
	for in, want := range cases {
		if got := c.FirstID(in); got != want {
			t.Errorf("FirstID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequireGitHub(t *testing.T) {
	c, _ := Load(project(t, tracker, forge))
	if err := c.RequireGitHub("ls"); err != nil {
		t.Error(err)
	}
	c.Forge.Kind = "Gitea"
	if err := c.RequireGitHub("done"); code(err) != apperr.ForgeUnsupported {
		t.Errorf("Gitea: %v", err)
	}
}
