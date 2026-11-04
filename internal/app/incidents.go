package app

import (
	"context"
	"fmt"
	"io"

	"gitlab.com/rogs/tkt/internal/history"
)

// IncidentsOpts are tkt incidents' arguments: Ignore lets agents start
// through the incidents reported now, and Off holds them back again.
type IncidentsOpts struct {
	Ignore bool
	Off    bool
	Dry    bool
}

// IncidentsResult is what the status pages report, as tkt watch last read
// them, each with whether it is ignored. Changed is false when every
// incident already stood as asked.
type IncidentsResult struct {
	Incidents []Incident `json:"incidents"`
	Changed   bool       `json:"changed"`
	DryRun    bool       `json:"dry_run"`
	Actions   []Action   `json:"actions"`
}

func (r *IncidentsResult) Text(w io.Writer) {
	if len(r.Incidents) == 0 {
		fmt.Fprintln(w, "no status page reports an incident")
		return
	}
	incidentLines(w, r.Incidents)
}

// Incidents lists what the status pages report. With Ignore, tkt watch and
// tkt auto start agents through each incident reported now, until it ends:
// a problem an agent may never meet, such as a pull request outage while
// it plans, need not hold every ticket back. The next incident holds them
// again. Off ends every waiver at once.
func (a *App) Incidents(ctx context.Context, o IncidentsOpts) (*IncidentsResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	res := &IncidentsResult{Incidents: c.incidents(), DryRun: c.Dry, Actions: []Action{}}
	if !o.Ignore && !o.Off {
		return res, nil
	}
	ws := readWaivers(c.Repo.Common)
	at := c.Now().UTC().Format(history.TimeFormat)
	for k, i := range res.Incidents {
		if i.Ignored == o.Ignore {
			continue
		}
		res.Changed = true
		res.Incidents[k].Ignored = o.Ignore
		if o.Ignore {
			ws = append(ws, waiver{Page: i.Page, Since: i.Since, At: at})
			c.UI.Info("%s: agents start through %s until it ends", i.Service, i.Problem)
		}
	}
	if o.Off {
		res.Changed = res.Changed || len(ws) > 0
		ws = nil
	}
	if o.Ignore && len(res.Incidents) == 0 {
		c.UI.Info("no status page reports an incident, so nothing holds agents back")
	}
	if !res.Changed || c.Dry {
		return res, nil
	}
	if err := writeWaivers(c.Repo.Common, ws); err != nil {
		c.UI.Warn("cannot write %s: %v", ignorePath(c.Repo.Common), err)
	}
	return res, nil
}
