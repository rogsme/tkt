package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestDoctorPretty(t *testing.T) {
	r := &DoctorResult{Checks: []Check{
		{ID: "git", Status: CheckOK, Message: "git version 2.55.0"},
		{ID: "repository", Status: CheckOK, Message: "/home/acme/app"},
		{ID: "lifecycle", Status: CheckFail, Message: "/home/acme/app/docs/agents has no lifecycle configuration",
			Hint: "run /setup-rogs in this project"},
		{ID: "herdr_env", Status: CheckWarn, Message: "not inside a herdr pane"},
		{ID: "harness.claude", Status: CheckOK, Message: "2.1.303 (Claude Code)"},
		{ID: "gh", Status: CheckSkip, Message: "the forge is unknown"},
	}}
	var b strings.Builder
	r.Pretty(&colorprofile.Writer{Forward: &b, Profile: colorprofile.NoTTY}, "/home/acme", 60)
	want := `
  Tools
    ok    git         git version 2.55.0
    skip  gh          the forge is unknown

  Harnesses
    ok    claude      2.1.303 (Claude Code)

  Project
    ok    repository  ~/app
    fail  lifecycle   ~/app/docs/agents has no lifecycle
                      configuration
                      hint: run /setup-rogs in this project

  Environment
    warn  herdr pane  not inside a herdr pane

  3 ok, 1 warn, 1 fail, 1 skip  fix the failing checks before using tkt

`
	if got := b.String(); got != want {
		t.Errorf("Pretty:\n%s\nwant:\n%s", got, want)
	}
}

func TestWordWrap(t *testing.T) {
	got := wordWrap("pass -C DIR or ~/.local/state/tkt/tkt.log here", 12)
	want := []string{"pass -C DIR", "or", "~/.local/state/tkt/tkt.log", "here"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wordWrap = %q, want %q", got, want)
	}
}
