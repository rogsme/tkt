package herdr

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Agent statuses herdr reports.
const (
	Idle    = "idle"
	Working = "working"
	Blocked = "blocked"
	Done    = "done"
)

// Agent is a coding agent herdr tracks in a pane.
type Agent struct {
	Name          *string `json:"name"`
	Kind          string  `json:"agent"`
	Status        string  `json:"agent_status"`
	PaneID        string  `json:"pane_id"`
	TabID         string  `json:"tab_id"`
	Cwd           string  `json:"cwd"`
	ForegroundCwd string  `json:"foreground_cwd"`
	Session       *struct {
		Value string `json:"value"`
	} `json:"agent_session"`
}

// NameOr returns the agent's name, or def when it has none.
func (a Agent) NameOr(def string) string {
	if a.Name != nil && *a.Name != "" {
		return *a.Name
	}
	return def
}

// SessionID returns the agent's harness session, or "".
func (a Agent) SessionID() string {
	if a.Session == nil {
		return ""
	}
	return a.Session.Value
}

// Pane is a terminal pane.
type Pane struct {
	PaneID        string          `json:"pane_id"`
	TabID         string          `json:"tab_id"`
	WorkspaceID   string          `json:"workspace_id"`
	Cwd           string          `json:"cwd"`
	ForegroundCwd string          `json:"foreground_cwd"`
	Agent         json.RawMessage `json:"agent"`
}

// HasAgent reports whether herdr lists an agent in the pane.
func (p Pane) HasAgent() bool {
	s := strings.TrimSpace(string(p.Agent))
	return s != "" && s != "null"
}

// Tab is a tab in a workspace.
type Tab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// Inside reports whether path is dir or below it.
func Inside(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// TicketTabs returns the tabs whose label is id, or id followed by a space,
// such as "ACM-12 (fast)".
func TicketTabs(tabs []Tab, id string) []Tab {
	var out []Tab
	for _, t := range tabs {
		if t.Label == id || strings.HasPrefix(t.Label, id+" ") {
			out = append(out, t)
		}
	}
	return out
}

// AgentsInPath returns the agents working in path or below it.
func AgentsInPath(agents []Agent, path string) []Agent {
	var out []Agent
	for _, a := range agents {
		if Inside(a.Cwd, path) || Inside(a.ForegroundCwd, path) {
			out = append(out, a)
		}
	}
	return out
}
