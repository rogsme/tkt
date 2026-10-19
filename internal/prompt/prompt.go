// Package prompt reads, checks and names the text tkt hands an agent.
package prompt

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
)

// MaxBytes caps a prompt: herdr receives it as one argument, and Linux limits
// one argument to 128 KiB.
const MaxBytes = 131000

// Source says where a prompt comes from: Text ("-" for stdin) or File.
type Source struct {
	Text string
	File string
	Set  bool
}

// Notifier receives the notes Read prints for a person at a terminal.
type Notifier interface {
	Info(format string, args ...any)
	Warn(format string, args ...any)
}

// Read returns the prompt text. Text from stdin or a file loses its trailing
// newlines, as shell command substitution would.
func Read(src Source, stdin io.Reader, stdinTTY bool, ui Notifier) (string, error) {
	switch {
	case src.File != "":
		b, err := os.ReadFile(src.File)
		if err != nil {
			return "", apperr.New(apperr.PromptFileUnreadable, "cannot read the prompt file: %v", err)
		}
		return strings.TrimRight(string(b), "\n"), nil
	case src.Text == "-":
		if stdinTTY {
			ui.Info("reading the prompt from the terminal; finish with Ctrl-D")
		}
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", apperr.New(apperr.PromptFileUnreadable, "cannot read the prompt from stdin: %v", err)
		}
		if stdinTTY && hasLongLine(b) {
			ui.Warn("a line of 4095 bytes or more was typed into the terminal and may be cut; pipe the prompt instead")
		}
		return strings.TrimRight(string(b), "\n"), nil
	default:
		return src.Text, nil
	}
}

func hasLongLine(b []byte) bool {
	s := bufio.NewScanner(bytes.NewReader(b))
	s.Buffer(make([]byte, 0, 64*1024), MaxBytes*2)
	for s.Scan() {
		if len(s.Bytes()) >= 4095 {
			return true
		}
	}
	return false
}

// Guard refuses a prompt the agent would misread. force lifts the refusals
// that protect against a mistake, never the size cap.
func Guard(p string, force bool) error {
	if len(p) > MaxBytes {
		return apperr.New(apperr.PromptTooLarge,
			"the prompt is %d bytes; the limit is %d, because one argument must stay under 128 KiB", len(p), MaxBytes)
	}
	if strings.ContainsRune(p, 0) {
		return apperr.New(apperr.PromptInvalid, "the prompt holds a NUL byte, which no argument can carry")
	}
	if strings.TrimSpace(p) == "" {
		return apperr.New(apperr.PromptEmpty, "the prompt is empty")
	}
	if force {
		return nil
	}
	if strings.HasPrefix(p, "!") {
		return apperr.New(apperr.PromptShellEscape,
			"the prompt starts with '!', which switches the agent to shell mode; pass --force to send it anyway")
	}
	if strings.HasPrefix(p, "/") && strings.Contains(p, "\n") {
		return apperr.New(apperr.PromptSlashCommand,
			"the prompt starts with '/' and has several lines, so the agent would read its first line as a command; pass --force to send it anyway")
	}
	return nil
}

var nameUnsafe = regexp.MustCompile(`[^a-z0-9_-]`)

// AgentName names the herdr agent for a ticket: lowercase, at most 32
// characters, starting with a letter.
func AgentName(id string) string {
	n := nameUnsafe.ReplaceAllString(strings.ToLower(id), "-")
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "t-" + n
	}
	if len(n) > 32 {
		n = n[:32]
	}
	return n
}
