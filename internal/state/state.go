// Package state keeps what tkt knows about one ticket (preset, session, tab,
// pane, status) in the worktree's own git directory, so it disappears with
// the worktree. The format is "key=value" lines, sorted.
package state

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Path returns the state file inside a worktree's git directory.
func Path(gitDir string) string { return filepath.Join(gitDir, "tkt", "state") }

// State is one ticket's recorded state.
type State map[string]string

// Read loads the state file; a missing file is an empty state.
func Read(path string) State {
	s := State{}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok && k != "" {
			s[k] = v
		}
	}
	return s
}

// Set records key=value pairs in memory.
func (s State) Set(kv ...string) {
	for i := 0; i+1 < len(kv); i += 2 {
		s[kv[i]] = kv[i+1]
	}
}

// Save writes the state atomically, stamped with the time.
func (s State) Save(path string, now time.Time) error {
	s["updated"] = now.UTC().Format("2006-01-02T15:04:05Z")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + s[k] + "\n")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "state.*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
