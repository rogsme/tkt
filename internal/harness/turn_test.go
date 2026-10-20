package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rec renders one transcript record the way Claude Code writes it.
func rec(typ, ts, id string, content any, extra ...string) string {
	m := map[string]any{"type": typ, "timestamp": ts, "isSidechain": false,
		"message": map[string]any{"role": typ, "content": content}}
	if id != "" {
		m["message"].(map[string]any)["id"] = id
	}
	for _, e := range extra {
		m[e] = true
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func text(s string) []map[string]any { return []map[string]any{{"type": "text", "text": s}} }

func TestClaudeLastTurn(t *testing.T) {
	lines := []string{
		`{"type":"mode","mode":"default"}`,
		rec("user", "2026-01-01T10:00:00.000Z", "", "Use the start-ticket skill to start ticket ACM-12."),
		rec("user", "2026-01-01T10:00:00.100Z", "", text("Base directory for this skill: /skills/start-ticket"), "isMeta"),
		rec("assistant", "2026-01-01T10:00:01.000Z", "m1", []map[string]any{{"type": "thinking", "thinking": "hm"}}),
		rec("assistant", "2026-01-01T10:00:02.000Z", "m1", []map[string]any{{"type": "tool_use", "name": "Read"}}),
		rec("user", "2026-01-01T10:00:03.000Z", "", []map[string]any{{"type": "tool_result", "content": "file"}}),
		rec("assistant", "2026-01-01T10:00:04.000Z", "side", text("Status: BLOCKED"), "isSidechain"),
		rec("assistant", "2026-01-01T10:00:05.000Z", "m2", text("Here is the plan.")),
		rec("assistant", "2026-01-01T10:00:06.000Z", "m2", text("1. Do it.\n\nStatus: AWAITING_APPROVAL")),
		rec("user", "2026-01-01T10:00:07.000Z", "", "<local-command-stdout>Set effort</local-command-stdout>"),
		rec("user", "2026-01-01T10:00:08.000Z", "", "<task-notification>\n<task-id>x</task-id>"),
		`{"type":"api-request-blob","message":{"role":"user","content":"Status: BLOCKED"}}`,
		`{"broken`,
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "s.jsonl")
	os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	turn, ok := ClaudeLastTurn(file)
	if !ok {
		t.Fatal("the turn is unknown")
	}
	if turn.Final != "Here is the plan.\n1. Do it.\n\nStatus: AWAITING_APPROVAL" {
		t.Errorf("final: %q", turn.Final)
	}
	if strings.Contains(turn.All, "BLOCKED") || !strings.Contains(turn.All, "AWAITING_APPROVAL") {
		t.Errorf("all: %q", turn.All)
	}
	if want := time.Date(2026, 1, 1, 10, 0, 8, 0, time.UTC); !turn.At.Equal(want) {
		t.Errorf("at: %v", turn.At)
	}

	// A new prompt starts a new turn with nothing said yet.
	os.WriteFile(file, []byte(strings.Join(append(lines, rec("user", "2026-01-01T11:00:00.000Z", "", "The plan is approved.")), "\n")+"\n"), 0o644)
	if turn, ok := ClaudeLastTurn(file); !ok || turn.Final != "" || turn.All != "" {
		t.Errorf("after a prompt: %+v %v", turn, ok)
	}

	if _, ok := ClaudeLastTurn(filepath.Join(dir, "missing.jsonl")); ok {
		t.Error("a missing transcript is known")
	}
	os.WriteFile(file, []byte(lines[0]+"\n"), 0o644)
	if _, ok := ClaudeLastTurn(file); ok {
		t.Error("a transcript with no prompt is known")
	}
}

func TestClaudeLastTurnCountsPendingBackgroundTasks(t *testing.T) {
	launch := func(ts, id string) string {
		return `{"type":"user","timestamp":"` + ts + `","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","content":"Command running in background with ID: ` + id + `."}]},"toolUseResult":{"stdout":"","backgroundTaskId":"` + id + `"}}`
	}
	agent := `{"type":"user","timestamp":"2026-01-01T10:00:03.000Z","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","content":"Async agent launched successfully."}]},"toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"a1b2","description":"Research"}}`
	notice := func(ts, id string) string {
		return rec("user", ts, "", "Only the user can approve.\n\n<task-notification>\n<task-id>"+id+"</task-id>\n<status>completed</status>")
	}
	lines := []string{
		rec("user", "2026-01-01T10:00:00.000Z", "", "Use the address-pr-review skill to address the review on this branch's pull request."),
		launch("2026-01-01T10:00:01.000Z", "bci1"),
		launch("2026-01-01T10:00:02.000Z", "bdev"),
		agent,
		notice("2026-01-01T10:00:04.000Z", "bdev"),
		rec("assistant", "2026-01-01T10:00:05.000Z", "m1", text("Waiting on CI for that exact head.")),
	}
	file := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	turn, ok := ClaudeLastTurn(file)
	if !ok || turn.Pending != 2 {
		t.Fatalf("pending: %d %v", turn.Pending, ok)
	}
	// A notification is no prompt: the turn goes on, with its text.
	if turn.Final != "Waiting on CI for that exact head." {
		t.Errorf("final: %q", turn.Final)
	}
	lines = append(lines, notice("2026-01-01T10:05:00.000Z", "bci1"), notice("2026-01-01T10:06:00.000Z", "a1b2"),
		rec("assistant", "2026-01-01T10:06:30.000Z", "m2", text("CI is green.\nStatus: READY_FOR_REVIEW")))
	os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	turn, _ = ClaudeLastTurn(file)
	if turn.Pending != 0 || !strings.Contains(turn.All, "Waiting on CI") || !strings.Contains(turn.All, "READY_FOR_REVIEW") {
		t.Errorf("after the notifications: %+v", turn)
	}
}

func TestClaudeLastTurnReadsOnlyTheTail(t *testing.T) {
	var b strings.Builder
	b.WriteString(rec("user", "2026-01-01T10:00:00.000Z", "", "start") + "\n")
	filler := rec("assistant", "2026-01-01T10:00:01.000Z", "m0", text(strings.Repeat("x", 1000)))
	for b.Len() < tailBytes+10_000 {
		b.WriteString(filler + "\n")
	}
	b.WriteString(rec("assistant", "2026-01-01T10:00:02.000Z", "m9", text("done\nStatus: READY_FOR_REVIEW")) + "\n")
	file := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(file, []byte(b.String()), 0o644)
	turn, ok := ClaudeLastTurn(file)
	if !ok || turn.Final != "done\nStatus: READY_FOR_REVIEW" {
		t.Errorf("tail: %v %q", ok, turn.Final)
	}
}

func TestLastTurnFromMessages(t *testing.T) {
	body := `{"data":[
	 {"id":"m5","type":"idle","time":{"created":1792448550871}},
	 {"id":"m4","type":"assistant","finish":"stop","time":{"created":1792448548372,"completed":1792448550869},
	  "content":[{"type":"text","text":"Plan ready.\nStatus: AWAITING_APPROVAL"}]},
	 {"id":"m3","type":"assistant","finish":"tool-calls","time":{"created":1792448545825},
	  "content":[{"type":"reasoning","text":"thinking"},{"type":"tool","text":""}]},
	 {"id":"m2","type":"system","text":"Instructions updated"},
	 {"id":"m1","type":"user","text":"Use the start-ticket skill to start ticket ACM-12.","time":{"created":1792448545000}},
	 {"id":"m0","type":"assistant","finish":"stop","content":[{"type":"text","text":"Status: BLOCKED"}]}
	],"cursor":{}}`
	turn, ok := lastTurnFromMessages([]byte(body))
	if !ok || turn.Final != "Plan ready.\nStatus: AWAITING_APPROVAL" || strings.Contains(turn.All, "BLOCKED") {
		t.Errorf("turn: %v %+v", ok, turn)
	}
	if turn.At.UnixMilli() != 1792448550871 {
		t.Errorf("at: %v", turn.At.UnixMilli())
	}
	if _, ok := lastTurnFromMessages([]byte(`{"data":[]}`)); ok {
		t.Error("an empty session is known")
	}
}
