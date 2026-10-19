// Package harness knows how to start and find sessions of the two agent
// harnesses tkt runs: Claude Code and OpenCode.
package harness

import (
	"crypto/rand"
	"fmt"
	"strings"

	"gitlab.com/rogs/tkt/internal/config"
)

// Phase says whether a launch starts a new session or resumes one.
type Phase int

const (
	New Phase = iota
	Resume
)

// Session is a harness session found on disk or through an API.
type Session struct {
	ID string
	// UpdatedMs is the last update, in milliseconds since the epoch.
	UpdatedMs int64
}

// LaunchArgs returns the harness arguments that start or resume sid.
func LaunchArgs(harness string, phase Phase, sid string, p config.Preset, path string) []string {
	if harness == config.Claude {
		return ClaudeArgs(phase, sid, p.Model)
	}
	return []string{"-s", sid, path}
}

// ClaudeArgs starts Claude Code with its permission prompts off, on a session
// id chosen up front. A model reads model#effort.
func ClaudeArgs(phase Phase, sid, model string) []string {
	var a []string
	if phase == Resume {
		a = []string{"--resume", sid, "--dangerously-skip-permissions"}
	} else {
		a = []string{"--dangerously-skip-permissions", "--session-id", sid}
	}
	if model == "" {
		return a
	}
	m, effort, _ := strings.Cut(model, "#")
	if m != "" {
		a = append(a, "--model", m)
	}
	if effort != "" {
		a = append(a, "--effort", effort)
	}
	return a
}

// NewUUID returns a random version 4 UUID.
func NewUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
