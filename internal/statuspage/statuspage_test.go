package statuspage

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const summary = `{
  "page": {"id": "x", "name": "GitHub", "url": "https://www.githubstatus.com"},
  "components": [
    {"name": "Git Operations", "status": "operational"},
    {"name": "Actions", "status": "partial_outage"},
    {"name": "Codespaces", "status": "major_outage"}
  ],
  "incidents": [{"name": "Disruption with some GitHub services", "status": "investigating"}],
  "status": {"indicator": "major", "description": "Partial System Outage"}
}`

func page(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "api", "v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "v2", "summary.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return "file://" + dir
}

func TestReadAndProblem(t *testing.T) {
	s, err := New().Read(context.Background(), page(t, summary)+"/")
	if err != nil {
		t.Fatal(err)
	}
	if s.Page.Name != "GitHub" {
		t.Errorf("page %+v", s.Page)
	}
	if p, missing := s.Problem(nil); p != "Partial System Outage: Disruption with some GitHub services" || missing != nil {
		t.Errorf("whole page: %q %v", p, missing)
	}
	if p, missing := s.Problem([]string{"Git Operations", "actions", "Pull Requests"}); p != "Actions partial outage" || !slices.Equal(missing, []string{"Pull Requests"}) {
		t.Errorf("components: %q %v", p, missing)
	}
	if p, _ := s.Problem([]string{"Git Operations"}); p != "" {
		t.Errorf("a component that works: %q", p)
	}
}

func TestAllClear(t *testing.T) {
	s, err := New().Read(context.Background(), page(t, `{"page": {"name": "Acme"}, "status": {"indicator": "none", "description": "All Systems Operational"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Problem(nil); p != "" {
		t.Errorf("problem %q", p)
	}
}

func TestReadRefusesOtherPages(t *testing.T) {
	if _, err := New().Read(context.Background(), page(t, `<html>`)); err == nil || !strings.Contains(err.Error(), "not a status page summary") {
		t.Errorf("html: %v", err)
	}
	if _, err := New().Read(context.Background(), page(t, `{"page": {}}`)); err == nil || !strings.Contains(err.Error(), "no status indicator") {
		t.Errorf("no indicator: %v", err)
	}
	if _, err := New().Read(context.Background(), "file://"+t.TempDir()); err == nil {
		t.Errorf("a missing page reads")
	}
}
