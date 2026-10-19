package harness

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ClaudeHome returns Claude Code's configuration directory.
func ClaudeHome(getenv func(string) string) string {
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(getenv("HOME"), ".claude")
}

// ClaudeProjectDir returns where Claude Code keeps the transcripts of
// sessions started in path.
func ClaudeProjectDir(getenv func(string) string, path string) string {
	return filepath.Join(ClaudeHome(getenv), "projects", ClaudeSlug(path))
}

// ClaudeSlug names a project directory the way Claude Code does: every UTF-16
// unit that is not an ASCII letter or digit becomes "-", and a name over 200
// units is cut and suffixed with a hash of the path.
func ClaudeSlug(path string) string {
	units := utf16.Encode([]rune(path))
	b := make([]byte, len(units))
	for i, u := range units {
		if u < 128 && (u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9') {
			b[i] = byte(u)
		} else {
			b[i] = '-'
		}
	}
	if len(b) <= 200 {
		return string(b)
	}
	var h int32
	for _, u := range units {
		h = h<<5 - h + int32(u)
	}
	abs := int64(h)
	if abs < 0 {
		abs = -abs
	}
	return string(b[:200]) + "-" + strconv.FormatInt(abs, 36)
}

// ClaudeNewest returns the most recently written session in a project
// directory.
func ClaudeNewest(dir string) (Session, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Session{}, false
	}
	var best Session
	found := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ms := info.ModTime().UnixMilli()
		if !found || ms > best.UpdatedMs {
			best = Session{ID: strings.TrimSuffix(name, ".jsonl"), UpdatedMs: ms}
			found = true
		}
	}
	return best, found
}

// ClaudeUserTurns counts the prompts a Claude Code transcript recorded. A
// missing transcript has none yet; ok is false when the file cannot be read.
// A last line still being written is skipped.
func ClaudeUserTurns(file string) (n int, ok bool) {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, true
	}
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	var pending error
	for sc.Scan() {
		if pending != nil {
			return 0, false
		}
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			pending = err
			continue
		}
		if rec.Type == "user" && isPrompt(rec.Message.Content) {
			n++
		}
	}
	return n, sc.Err() == nil
}

// isPrompt reports whether a user record holds typed text, as opposed to a
// tool result.
func isPrompt(content json.RawMessage) bool {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return true
	}
	var parts []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return false
	}
	for _, p := range parts {
		if p.Type == "text" {
			return true
		}
	}
	return false
}
