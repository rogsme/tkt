package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/rogs/tkt/internal/apperr"
)

type notes struct{ info, warn []string }

func (n *notes) Info(f string, a ...any) { n.info = append(n.info, fmt.Sprintf(f, a...)) }
func (n *notes) Warn(f string, a ...any) { n.warn = append(n.warn, fmt.Sprintf(f, a...)) }

func code(err error) apperr.Code {
	if err == nil {
		return ""
	}
	return apperr.As(err).Code
}

func TestGuard(t *testing.T) {
	big := strings.Repeat("a", MaxBytes+1)
	refused := map[string]apperr.Code{
		big:             apperr.PromptTooLarge,
		"!rm -rf build": apperr.PromptShellEscape,
		"/review\nmore": apperr.PromptSlashCommand,
		"   ":           apperr.PromptEmpty,
		"a\x00b":        apperr.PromptInvalid,
		"":              apperr.PromptEmpty,
	}
	for p, want := range refused {
		if got := code(Guard(p, false)); got != want {
			t.Errorf("Guard(%.20q) = %s, want %s", p, got, want)
		}
	}
	for _, p := range []string{"/exit", "- a list item", "# Start ticket: ACM-1"} {
		if err := Guard(p, false); err != nil {
			t.Errorf("Guard(%q): %v", p, err)
		}
	}
	if err := Guard("!echo hi", true); err != nil {
		t.Errorf("--force: %v", err)
	}
	if code(Guard(big, true)) != apperr.PromptTooLarge {
		t.Error("--force lifted the size cap")
	}
}

func TestAgentName(t *testing.T) {
	cases := map[string]string{
		"ACM-12":                               "acm-12",
		"12-X":                                 "t-12-x",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ-123456789": "abcdefghijklmnopqrstuvwxyz-12345",
	}
	for in, want := range cases {
		if got := AgentName(in); got != want {
			t.Errorf("AgentName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadStripsTrailingNewlines(t *testing.T) {
	n := &notes{}
	got, err := Read(Source{Text: "-", Set: true}, strings.NewReader("line one\nline two\n\n"), false, n)
	if err != nil || got != "line one\nline two" {
		t.Errorf("stdin: %q, %v", got, err)
	}
	if len(n.info) != 0 {
		t.Errorf("printed %v without a terminal", n.info)
	}
	path := filepath.Join(t.TempDir(), "p.txt")
	os.WriteFile(path, []byte("from a file\n"), 0o644)
	if got, _ := Read(Source{File: path, Set: true}, nil, false, n); got != "from a file" {
		t.Errorf("file: %q", got)
	}
	if _, err := Read(Source{File: path + ".missing", Set: true}, nil, false, n); code(err) != apperr.PromptFileUnreadable {
		t.Errorf("missing file: %v", err)
	}
}

func TestReadWarnsAboutLongTerminalLines(t *testing.T) {
	n := &notes{}
	Read(Source{Text: "-"}, strings.NewReader(strings.Repeat("a", 4095)), true, n)
	if len(n.info) != 1 || len(n.warn) != 1 {
		t.Errorf("info %v warn %v", n.info, n.warn)
	}
}
