package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/statuspage"
)

// Incident is a status page that reports a problem with its service, as
// tkt watch last read it. While one does, tkt watch and tkt auto start no
// agent: a fix for checks that fail because CI is down, or a run on a model
// that does not answer, only spends a round. Page is the [status.NAME] the page has in
// config.toml, Service what the page calls its service, Since when tkt first
// read the problem, and Seen when it last did, in UTC.
type Incident struct {
	Page    string `json:"page"`
	Service string `json:"service"`
	Problem string `json:"problem"`
	URL     string `json:"url"`
	Since   string `json:"since"`
	Seen    string `json:"seen"`
	// Ignored says a person ran tkt incidents --ignore while it lasts, so it
	// holds nothing back.
	Ignored bool `json:"ignored,omitempty"`
}

// statusEvery is how often tkt watch reads the status pages, which ask to be
// polled no more often than every few minutes.
const statusEvery = 5 * time.Minute

// statusFor is how long tkt ls trusts tkt watch's last read: a watch that
// missed two reads stopped reading.
const statusFor = 3 * statusEvery

// statusPath is the file where tkt watch keeps its last read of the status
// pages, for tkt ls and the board.
func statusPath(common string) string { return filepath.Join(common, "tkt", "status") }

type statusFile struct {
	Checked   string     `json:"checked"`
	Incidents []Incident `json:"incidents"`
}

// incidents returns what tkt watch last read from the status pages, while
// that read is recent and a watch runs, each marked when it is ignored.
func (c *Ctx) incidents() []Incident {
	incs := lastIncidents(c.Repo.Common, c.Now())
	if len(incs) == 0 || !c.watchRunning() {
		return []Incident{}
	}
	return markIgnored(c.Repo.Common, incs)
}

// ignorePath is the file where tkt incidents --ignore keeps the incidents a
// person let agents start through.
func ignorePath(common string) string { return filepath.Join(common, "tkt", "status-ignored") }

// waiver is one ignored incident. It names the incident by its page and the
// time tkt first read it, which tkt watch keeps while the incident lasts:
// the waiver ends with the incident, and the next one holds agents again.
type waiver struct {
	Page  string `json:"page"`
	Since string `json:"since"`
	At    string `json:"at"`
}

func (v waiver) covers(i Incident) bool { return v.Page == i.Page && v.Since == i.Since }

func readWaivers(common string) []waiver {
	b, err := os.ReadFile(ignorePath(common))
	var ws []waiver
	if err != nil || json.Unmarshal(b, &ws) != nil {
		return nil
	}
	return ws
}

func writeWaivers(common string, ws []waiver) error {
	path := ignorePath(common)
	if len(ws) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	b, _ := json.Marshal(ws)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// markIgnored returns a copy of incs with each incident a waiver covers
// marked Ignored.
func markIgnored(common string, incs []Incident) []Incident {
	ws := readWaivers(common)
	out := make([]Incident, len(incs))
	for k, i := range incs {
		i.Ignored = slices.ContainsFunc(ws, func(v waiver) bool { return v.covers(i) })
		out[k] = i
	}
	return out
}

// lastIncidents reads what the status pages reported at tkt watch's last
// read, while that read is recent.
func lastIncidents(common string, now time.Time) []Incident {
	b, err := os.ReadFile(statusPath(common))
	var f statusFile
	if err != nil || json.Unmarshal(b, &f) != nil {
		return nil
	}
	if at, err := time.Parse(time.RFC3339, f.Checked); err != nil || now.Sub(at) > statusFor {
		return nil
	}
	return f.Incidents
}

// holdText says why tkt watch and tkt auto start no agent, or "". An
// ignored incident holds nothing back.
func holdText(incs []Incident) string {
	var parts []string
	for _, i := range incs {
		if !i.Ignored {
			parts = append(parts, i.Service+": "+i.Problem)
		}
	}
	return strings.Join(parts, "; ")
}

// readStatus reads the status pages config.toml names, at most every
// statusEvery, and keeps what they report as the watcher's incidents. It
// notifies when an incident starts and when it ends. A page tkt cannot read
// is reported once, and keeps what it said last for statusFor: a page that
// fails to answer during an incident should neither let every agent loose
// at once nor hold them for good.
func (w *watcher) readStatus(c *Ctx) {
	pages := c.Cfg.Status
	if len(pages) == 0 {
		// Pages taken out of config.toml hold nothing back any more.
		if w.incidents = nil; !c.Dry && len(lastIncidents(c.Repo.Common, c.Now())) > 0 {
			os.Remove(statusPath(c.Repo.Common))
		}
		return
	}
	now := c.Now()
	if !w.checked.IsZero() && now.Sub(w.checked) < statusEvery {
		return
	}
	if w.checked.IsZero() {
		// What the last watch read still stands, so a restart neither
		// notifies about an incident again nor misses its end.
		w.incidents = lastIncidents(c.Repo.Common, now)
	}
	w.checked = now
	type read struct {
		s   statuspage.Summary
		err error
	}
	got := make([]read, len(pages))
	client := statuspage.New()
	var wg sync.WaitGroup
	for i, p := range pages {
		wg.Go(func() {
			s, err := client.Read(c.Ctx, p.URL)
			got[i] = read{s, err}
		})
	}
	wg.Wait()
	prev := map[string]Incident{}
	for _, i := range w.incidents {
		prev[i.Page] = i
	}
	var incs []Incident
	unread := map[string]bool{}
	for i, p := range pages {
		if err := got[i].err; err != nil {
			unread[p.Name] = true
			w.warnOnce(c, "read "+p.Name+" "+err.Error(), "cannot read the status page %s: %v", p.Name, err)
			if old, ok := prev[p.Name]; ok {
				if seen, err := time.Parse(time.RFC3339, old.Seen); err == nil && now.Sub(seen) < statusFor {
					incs = append(incs, old)
				}
			}
			continue
		}
		problem, missing := got[i].s.Problem(p.Components)
		for _, m := range missing {
			w.warnOnce(c, "missing "+p.Name+" "+m, "the status page %s has no component named %s, so it counts for nothing", p.Name, m)
		}
		if problem == "" {
			continue
		}
		at := now.UTC().Format(history.TimeFormat)
		inc := Incident{Page: p.Name, Service: or(got[i].s.Page.Name, p.Name), Problem: problem, URL: p.URL, Since: at, Seen: at}
		if old, ok := prev[p.Name]; ok {
			inc.Since = old.Since
		}
		incs = append(incs, inc)
	}
	for _, i := range incs {
		if _, ok := prev[i.Page]; !ok {
			c.UI.Warn("%s reports %s; tkt watch and tkt auto start no agent until it ends, or until tkt incidents --ignore", i.Service, i.Problem)
			c.notify(i.Service, notice{"incident", i.Problem + "; tkt watch and tkt auto start no agent until it ends, or until tkt incidents --ignore", "request"})
		}
		delete(prev, i.Page)
	}
	for _, i := range prev {
		if unread[i.Page] {
			c.UI.Warn("%s's status page has not answered for %s, so tkt watch starts agents again", i.Service, statusFor)
			c.notify(i.Service, notice{"status unknown", "its status page does not answer, so tkt watch starts agents again", "request"})
			continue
		}
		c.UI.Info("%s works again, so tkt watch starts agents again", i.Service)
		c.notify(i.Service, notice{"resolved", "tkt watch starts agents again", "done"})
	}
	w.incidents = incs
	if c.Dry {
		return
	}
	w.pruneWaivers(c, incs)
	path := statusPath(c.Repo.Common)
	b, _ := json.Marshal(statusFile{Checked: now.UTC().Format(history.TimeFormat), Incidents: append([]Incident{}, incs...)})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil || os.WriteFile(path, b, 0o644) != nil {
		w.warnOnce(c, "write "+path, "cannot write %s, so tkt ls does not show what the status pages report", path)
	}
}

// warnOnce warns about something once per tkt watch run.
func (w *watcher) warnOnce(c *Ctx, key, format string, args ...any) {
	if w.warned[key] {
		return
	}
	if w.warned == nil {
		w.warned = map[string]bool{}
	}
	w.warned[key] = true
	c.UI.Warn(format, args...)
}

// pruneWaivers drops the waivers of incidents the pages no longer report,
// so the file holds only the ones that still last.
func (w *watcher) pruneWaivers(c *Ctx, incs []Incident) {
	ws := readWaivers(c.Repo.Common)
	kept := slices.DeleteFunc(slices.Clone(ws), func(v waiver) bool {
		return !slices.ContainsFunc(incs, v.covers)
	})
	if len(kept) == len(ws) {
		return
	}
	if err := writeWaivers(c.Repo.Common, kept); err != nil {
		w.warnOnce(c, "write "+ignorePath(c.Repo.Common), "cannot write %s: %v", ignorePath(c.Repo.Common), err)
	}
}
