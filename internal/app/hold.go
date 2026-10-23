package app

import (
	"context"
	"fmt"
	"io"

	"gitlab.com/rogs/tkt/internal/history"
)

// HoldOpts are tkt hold's arguments.
type HoldOpts struct {
	ID  string
	Off bool
	Dry bool
}

// HoldResult is a ticket's hold mark after tkt hold. Since is when it was
// put on hold, or "" when it is not; Changed is false when the mark already
// stood as asked.
type HoldResult struct {
	Ticket  string   `json:"ticket"`
	Hold    bool     `json:"hold"`
	Since   string   `json:"since"`
	Changed bool     `json:"changed"`
	DryRun  bool     `json:"dry_run"`
	Actions []Action `json:"actions"`
}

func (r *HoldResult) Text(w io.Writer) {
	if r.Hold {
		fmt.Fprintf(w, "%s  on hold: tkt watch and tkt auto leave it alone until tkt hold %s --off\n", r.Ticket, r.Ticket)
		return
	}
	fmt.Fprintf(w, "%s  not on hold\n", r.Ticket)
}

// Hold puts a ticket on hold, or takes it off with Off. tkt watch and tkt
// auto leave a ticket on hold alone, such as one you parked or want to drive
// by hand; the commands you run yourself still work on it.
func (a *App) Hold(ctx context.Context, o HoldOpts) (*HoldResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	id, err := c.LC.NormalizeID(o.ID)
	if err != nil {
		return nil, err
	}
	c.ticket(id)
	path, err := c.ticketPath(id)
	if err != nil {
		return nil, err
	}
	ts, err := c.readState(path)
	if err != nil {
		return nil, err
	}
	res := &HoldResult{Ticket: id, Hold: !o.Off, Since: ts.st["hold"], DryRun: c.Dry, Actions: []Action{}}
	if (ts.st["hold"] != "") == !o.Off {
		return res, nil
	}
	res.Changed = true
	since, action := "", "off"
	if !o.Off {
		since, action = c.Now().UTC().Format(history.TimeFormat), "on"
	}
	c.save(ts, "hold", since)
	c.record(history.Event{Ticket: id, Event: history.Hold, Action: action})
	res.Since = since
	return res, nil
}
