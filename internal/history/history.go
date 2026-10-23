// Package history keeps what happens to tickets in a JSON Lines file that
// outlives their worktrees, so tkt stats can say how long tickets take from
// start to merge. Each line is one event; tkt only ever appends.
package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Version is the event format's version.
const Version = 1

// The events tkt records.
const (
	Started  = "started"
	Approved = "approved"
	Stage    = "stage"
	Phase    = "phase"
	Auto     = "auto"
	Merged   = "merged"
	Done     = "done"
	// Hold is a ticket put on hold (Action on) or taken off it (off).
	Hold = "hold"
	// Sync is a base merge tkt made and pushed itself (Action merged).
	Sync = "sync"
	// Watch is a step tkt watch took (Action run, with Stage), a pause for
	// a person (Action pause, with Stop), or a step that failed (Stop
	// failed).
	Watch = "watch"
)

// Event is one line of the history.
type Event struct {
	V      int    `json:"v"`
	At     string `json:"at"`
	Repo   string `json:"repo"`
	Ticket string `json:"ticket"`
	Event  string `json:"event"`
	// Phase is the phase a ticket entered; From the one it left.
	Phase string `json:"phase,omitempty"`
	From  string `json:"from,omitempty"`
	// Stage is the stage tkt run started.
	Stage string `json:"stage,omitempty"`
	// Action and Stop describe what tkt auto did, or why it stopped.
	Action string `json:"action,omitempty"`
	Stop   string `json:"stop,omitempty"`
	PR     int    `json:"pr,omitempty"`
	// MergedAt is when the forge merged the pull request.
	MergedAt string `json:"merged_at,omitempty"`
	// OpenedAt is when the pull request was opened.
	OpenedAt string `json:"opened_at,omitempty"`
}

// Path returns the history file: next to tkt's command log.
func Path(getenv func(string) string) string {
	dir := getenv("XDG_STATE_HOME")
	if dir == "" {
		dir = filepath.Join(getenv("HOME"), ".local", "state")
	}
	return filepath.Join(dir, "tkt", "history.jsonl")
}

// TimeFormat is how events write times.
const TimeFormat = "2006-01-02T15:04:05Z"

// Append writes one event. A single short write to a file opened for
// appending lands whole, so concurrent tkt commands never interleave lines.
func Append(path string, e Event) error {
	e.V = Version
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Read returns every event in the file, skipping lines it cannot parse. A
// missing file has no events.
func Read(path string) ([]Event, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Ticket != "" && e.Event != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

func parse(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}
