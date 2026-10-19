package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/rogs/tkt/internal/config"
)

func TestClaudeArgs(t *testing.T) {
	if got := ClaudeArgs(New, "s1", ""); !slices.Equal(got, []string{"--dangerously-skip-permissions", "--session-id", "s1"}) {
		t.Errorf("new: %v", got)
	}
	if got := ClaudeArgs(Resume, "s1", "opus#high"); !slices.Equal(got, []string{"--resume", "s1", "--dangerously-skip-permissions", "--model", "opus", "--effort", "high"}) {
		t.Errorf("resume: %v", got)
	}
	if got := ClaudeArgs(New, "s1", "#low"); !slices.Equal(got[3:], []string{"--effort", "low"}) {
		t.Errorf("effort only: %v", got)
	}
	p := config.Preset{Name: "fast", Harness: config.OpenCode}
	if got := LaunchArgs(config.OpenCode, New, "ses_1", p, "/w/ACM-1"); !slices.Equal(got, []string{"-s", "ses_1", "/w/ACM-1"}) {
		t.Errorf("opencode: %v", got)
	}
}

func TestOpenCodeModelAndBody(t *testing.T) {
	m, err := OpenCodeModel("acme/fast#max")
	if err != nil || *m != (Model{ProviderID: "acme", ID: "fast", Variant: "max"}) {
		t.Fatalf("model: %+v, %v", m, err)
	}
	if _, err := OpenCodeModel("fast"); err == nil {
		t.Error("a model without a provider was accepted")
	}
	body := SessionBody("/w/<ACM-5>", "ACM-5", m)
	want := `{"title":"ACM-5","agent":"build","location":{"directory":"/w/<ACM-5>"},"model":{"providerID":"acme","id":"fast","variant":"max"}}`
	if body != want {
		t.Errorf("body:\n%s\nwant:\n%s", body, want)
	}
	if body := SessionBody("/w", "t", nil); strings.Contains(body, "model") {
		t.Errorf("default model in body: %s", body)
	}
}

func TestClaudeSlug(t *testing.T) {
	if got := ClaudeSlug("/home/dev/code/acme.app"); got != "-home-dev-code-acme-app" {
		t.Errorf("short: %s", got)
	}
	if got := ClaudeSlug("/a/\U0001F600"); got != "-a---" {
		t.Errorf("astral characters are two units: %s", got)
	}
	long := "/" + strings.Repeat("a", 250)
	got := ClaudeSlug(long)
	if !regexp.MustCompile(`^-a{199}-[0-9a-z]+$`).MatchString(got) {
		t.Errorf("long: %s", got)
	}
}

func TestClaudeNewestAndTurns(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "11111111-1111-1111-1111-111111111111.jsonl")
	recent := filepath.Join(dir, "22222222-2222-2222-2222-222222222222.jsonl")
	os.WriteFile(old, nil, 0o644)
	os.WriteFile(recent, []byte(`{"type":"user","message":{"content":"hi"}}
{"type":"user","message":{"content":[{"type":"tool_result"}]}}
{"type":"assistant","message":{"content":"ok"}}
{"type":"user","message":{"content":[{"type":"text","text":"more"}]}}
{"type":"user","mess`), 0o644)
	os.Chtimes(old, time.Now(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	os.Chtimes(recent, time.Now(), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	s, ok := ClaudeNewest(dir)
	if !ok || s.ID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("newest: %+v", s)
	}
	if n, ok := ClaudeUserTurns(recent); !ok || n != 2 {
		t.Errorf("turns: %d, %v", n, ok)
	}
	if n, ok := ClaudeUserTurns(filepath.Join(dir, "missing.jsonl")); !ok || n != 0 {
		t.Errorf("missing: %d, %v", n, ok)
	}
	os.WriteFile(old, []byte("not json\n{\"type\":\"user\",\"message\":{\"content\":\"x\"}}\n"), 0o644)
	if _, ok := ClaudeUserTurns(old); ok {
		t.Error("a broken line before the end was accepted")
	}
}

func TestURIEscape(t *testing.T) {
	if got := uriEscape("/w/a b+c~d"); got != "%2Fw%2Fa%20b%2Bc~d" {
		t.Errorf("got %s", got)
	}
}

func TestNewUUID(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(NewUUID()) {
		t.Error("not a v4 UUID")
	}
}
