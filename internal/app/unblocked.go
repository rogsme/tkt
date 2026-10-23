package app

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/state"
	"gitlab.com/rogs/tkt/internal/wt"
)

// Unblocked is a ticket a merge unblocked: finish-ticket named it when By
// merged, at At, and it has no worktree yet. tkt ls lists it, and the tkt
// agent's board offers to start it.
type Unblocked struct {
	Ticket string `json:"ticket"`
	By     string `json:"by"`
	At     string `json:"at"`
}

// unblockedFor is how long tkt ls offers a ticket a merge unblocked.
const unblockedFor = 7 * 24 * time.Hour

// unblockedPath is the file where tkt done keeps the tickets merges
// unblocked, next to tkt watch's in the repository's git directory.
func unblockedPath(common string) string { return filepath.Join(common, "tkt", "unblocked") }

// sessionUnblocked reads the tickets finish-ticket said the merge unblocked,
// from the last turn of the ticket's session; never the ticket itself.
func (c *Ctx) sessionUnblocked(id, path string, agents []herdr.Agent, st state.State) []string {
	var ag *herdr.Agent
	for i := range agents {
		if ag == nil || agents[i].PaneID == st["pane"] {
			ag = &agents[i]
		}
	}
	turn, _ := c.lastTurn(c.Ctx, ag, st, path)
	m := contract.UnblockedRE.FindAllStringSubmatch(turn.All, -1)
	if len(m) == 0 {
		return []string{}
	}
	return slices.DeleteFunc(c.LC.IDs(m[len(m)-1][1]), func(t string) bool { return t == id })
}

// noteUnblocked keeps the tickets by's merge unblocked, for tkt ls to offer.
func (c *Ctx) noteUnblocked(by string, ids []string) {
	if c.Dry || len(ids) == 0 {
		return
	}
	at := c.Now().UTC().Format(history.TimeFormat)
	var kv []string
	for _, id := range ids {
		kv = append(kv, id, by+" "+at)
	}
	if _, err := state.Update(unblockedPath(c.Repo.Common), c.Now(), kv...); err != nil {
		c.UI.Warn("cannot write %s: %v", unblockedPath(c.Repo.Common), err)
	}
}

// forgetUnblocked stops offering a ticket once it starts.
func (c *Ctx) forgetUnblocked(id string) {
	path := unblockedPath(c.Repo.Common)
	if c.Dry || state.Read(path)[id] == "" {
		return
	}
	if _, err := state.Update(path, c.Now(), id, ""); err != nil {
		c.UI.Warn("cannot write %s: %v", path, err)
	}
}

// unblocked lists the tickets merges unblocked in the last week that have
// no worktree yet, oldest first.
func (c *Ctx) unblocked(es wt.Entries) []Unblocked {
	out := []Unblocked{}
	for id, v := range state.Read(unblockedPath(c.Repo.Common)) {
		by, at, ok := strings.Cut(v, " ")
		t, err := time.Parse(time.RFC3339, at)
		if !ok || err != nil || !c.LC.RE.MatchString(id) || c.Now().Sub(t) > unblockedFor {
			continue
		}
		if _, started := es.Find(id); !started {
			out = append(out, Unblocked{Ticket: id, By: by, At: at})
		}
	}
	slices.SortFunc(out, func(a, b Unblocked) int { return strings.Compare(a.At+a.Ticket, b.At+b.Ticket) })
	return out
}

// idsText names tickets for people: ACM-15, ACM-15 and ACM-16, ACM-15,
// ACM-16 and ACM-17.
func idsText(ids []string) string {
	if len(ids) < 2 {
		return strings.Join(ids, "")
	}
	return strings.Join(ids[:len(ids)-1], ", ") + " and " + ids[len(ids)-1]
}
