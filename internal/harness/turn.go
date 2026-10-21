package harness

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// Turn is what an agent said since the last prompt a person or tkt typed.
type Turn struct {
	// Final is the text of the agent's last message: a plan, a report.
	Final string
	// All is every text the agent wrote in the turn, for its status lines.
	All string
	// At is the session's last activity.
	At time.Time
	// Pending counts the background tasks the session started and has not
	// heard back from. Claude Code ends a turn while a CI watcher or a
	// subagent runs, and goes on when the task's notification arrives.
	Pending int
}

var (
	backgroundRE = regexp.MustCompile(`"backgroundTaskId":"([^"]+)"`)
	asyncAgentRE = regexp.MustCompile(`"isAsync":true[^{}]*?"agentId":"([^"]+)"`)
	taskDoneRE   = regexp.MustCompile(`(?:<|\\u003c)task-id(?:>|\\u003e)([^<\\]+)(?:<|\\u003c)/task-id`)
)

// tailBytes bounds how much of a transcript ClaudeLastTurn reads; a turn
// that writes more than this loses its start, never its end.
const tailBytes = 2 << 20

// ClaudeLastTurn reads the last turn of a Claude Code transcript. known is
// false when the file is missing or unreadable, or holds no turn at all.
func ClaudeLastTurn(file string) (t Turn, known bool) {
	f, err := os.Open(file)
	if err != nil {
		return Turn{}, false
	}
	defer f.Close()
	cut := false
	if info, err := f.Stat(); err == nil && info.Size() > tailBytes {
		if _, err := f.Seek(info.Size()-tailBytes, io.SeekStart); err != nil {
			return Turn{}, false
		}
		cut = true
	}
	r := bufio.NewReaderSize(f, 64*1024)
	if cut {
		// The first line is a partial record.
		if _, err := r.ReadString('\n'); err != nil {
			return Turn{}, false
		}
	}
	var (
		all             []string
		lastID, lastTxt string
		prompt          bool
		started         = map[string]bool{}
		finished        = map[string]bool{}
	)
	for {
		line, err := r.ReadBytes('\n')
		for _, m := range backgroundRE.FindAllSubmatch(line, -1) {
			started[string(m[1])] = true
		}
		for _, m := range asyncAgentRE.FindAllSubmatch(line, -1) {
			started[string(m[1])] = true
		}
		for _, m := range taskDoneRE.FindAllSubmatch(line, -1) {
			finished[string(m[1])] = true
		}
		if len(strings.TrimSpace(string(line))) > 0 {
			var rec struct {
				Type      string `json:"type"`
				Sidechain bool   `json:"isSidechain"`
				Meta      bool   `json:"isMeta"`
				Timestamp string `json:"timestamp"`
				Message   struct {
					ID      string          `json:"id"`
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &rec) == nil && (rec.Type == "user" || rec.Type == "assistant") && !rec.Sidechain && !rec.Meta {
				if at, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil && at.After(t.At) {
					t.At = at
				}
				switch {
				case rec.Type == "user" && isPrompt(rec.Message.Content) && !isNotice(rec.Message.Content):
					all, lastID, lastTxt, prompt = nil, "", "", true
				case rec.Type == "assistant":
					if text := texts(rec.Message.Content); text != "" {
						all = append(all, text)
						if rec.Message.ID == lastID && lastID != "" {
							lastTxt += "\n" + text
						} else {
							lastID, lastTxt = rec.Message.ID, text
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	for id := range started {
		if !finished[id] {
			t.Pending++
		}
	}
	if !prompt && !(cut && len(all) > 0) {
		return Turn{At: t.At, Pending: t.Pending}, false
	}
	t.Final, t.All = lastTxt, strings.Join(all, "\n")
	return t, true
}

// isNotice reports whether a user record is something Claude Code wrote
// itself, such as a slash command's output or a background task's notice,
// rather than a prompt.
func isNotice(content json.RawMessage) bool {
	var s string
	if json.Unmarshal(content, &s) != nil {
		return false
	}
	for _, p := range []string{"<command-name>", "<local-command-", "<task-notification>", "<system-reminder>"} {
		if strings.HasPrefix(strings.TrimSpace(s), p) {
			return true
		}
	}
	// A task's notification can follow a preamble that says it is no
	// prompt; the agent's turn goes on after it.
	return strings.Contains(s, "<task-notification>")
}

// texts joins the text blocks of a message's content.
func texts(content json.RawMessage) string {
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		var s string
		json.Unmarshal(content, &s)
		return s
	}
	var out []string
	for _, p := range parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// lastTurnFromMessages reads OpenCode's message list, newest first, back to
// the last prompt.
func lastTurnFromMessages(b []byte) (Turn, bool) {
	var v struct {
		Data []struct {
			Type    string `json:"type"`
			Finish  string `json:"finish"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Time struct {
				Created   int64 `json:"created"`
				Completed int64 `json:"completed"`
			} `json:"time"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &v) != nil {
		return Turn{}, false
	}
	var t Turn
	var all []string
	prompt := false
	for _, m := range v.Data {
		at := max(m.Time.Created, m.Time.Completed)
		if ms := time.UnixMilli(at); at > 0 && ms.After(t.At) {
			t.At = ms
		}
		if m.Type == "user" {
			prompt = true
			break
		}
		if m.Type != "assistant" {
			continue
		}
		var parts []string
		for _, p := range m.Content {
			if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
				parts = append(parts, p.Text)
			}
		}
		if len(parts) == 0 {
			continue
		}
		text := strings.Join(parts, "\n")
		all = append([]string{text}, all...)
		if t.Final == "" {
			t.Final = text
		}
	}
	if !prompt && len(all) == 0 {
		return t, false
	}
	t.All = strings.Join(all, "\n")
	return t, true
}
