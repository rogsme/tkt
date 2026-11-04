package app

import (
	"bytes"
	"testing"

	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/output"
)

// A waiver names an incident by its page and the time tkt first read it,
// so it ends with that incident: the next one on the same page holds agents
// back again, and tkt watch drops the waiver once the page stops reporting.
func TestWaiverEndsWithItsIncident(t *testing.T) {
	common := t.TempDir()
	now := Incident{Page: "github", Service: "GitHub", Problem: "Actions major outage", Since: "2026-11-04T10:00:00Z"}
	later := now
	later.Since = "2026-11-04T18:00:00Z"
	if err := writeWaivers(common, []waiver{{Page: "github", Since: now.Since}}); err != nil {
		t.Fatal(err)
	}
	w := &watcher{c: &Ctx{App: &App{UI: &output.UI{Err: &bytes.Buffer{}}}, Repo: gitx.Repo{Common: common}}, incidents: []Incident{now}}
	if got := w.hold(); got != "" {
		t.Errorf("hold() with the incident ignored = %q, want none", got)
	}
	w.incidents = []Incident{later}
	if got := w.hold(); got != "GitHub: Actions major outage" {
		t.Errorf("hold() for a new incident = %q, want it held", got)
	}
	if got := markIgnored(common, []Incident{now, later}); !got[0].Ignored || got[1].Ignored {
		t.Errorf("markIgnored = %+v, want only the first ignored", got)
	}

	w.pruneWaivers(w.c, []Incident{later})
	if ws := readWaivers(common); len(ws) != 0 {
		t.Errorf("waivers after the incident ended = %+v, want none", ws)
	}
}
