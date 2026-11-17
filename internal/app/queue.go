package app

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"gitlab.com/rogs/tkt/internal/forge"
)

// The merge queue is the repository's merge orders in the order you gave
// them: by merge_at, then by ticket id. Only the order at its front acts.
// The others wait untouched, so a merge ahead of them does not make each
// one sync, wait for a review and wait for its checks again, only to fall
// behind once more when the next one merges. The queue is read from the
// orders themselves, so --cancel, a dropped order and tkt done take a
// ticket out of it without anything else to keep in step.

// queueEntry is one merge order, as its ticket's state holds it.
type queueEntry struct {
	ID string
	At time.Time
}

// orderAt is when an order joins the queue, written to merge_at. It keeps
// the fraction of a second, so two orders given in one second keep their
// order.
func orderAt(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// mergeQueue is a look's reading of the queue. A look reads it at most
// once, and only when a row holds an order; rows run in parallel, so open
// keeps what each pull request said behind a lock.
type mergeQueue struct {
	once    sync.Once
	entries []queueEntry
	mu      sync.Mutex
	open    map[string]bool
}

// queueEntries reads every ticket worktree's merge order, front first.
func (c *Ctx) queueEntries(l *look) []queueEntry {
	q := &l.queue
	q.once.Do(func() {
		es := l.entries
		if es == nil {
			var err error
			if es, err = c.WT.List(c.Ctx); err != nil {
				// Without the worktrees there is no queue to wait in; the
				// merge turn still keeps two lines from going at once.
				return
			}
		}
		for _, t := range c.ticketWorktrees(es) {
			ts, err := c.stateAt(c.Ctx, t.Path)
			if err != nil || ts.st["merge_head"] == "" {
				continue
			}
			// An order with no time, from before the queue, goes first.
			at, _ := time.Parse(time.RFC3339, ts.st["merge_at"])
			q.entries = append(q.entries, queueEntry{ID: t.ID, At: at})
		}
		slices.SortFunc(q.entries, func(a, b queueEntry) int {
			return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.ID, b.ID))
		})
	})
	return q.entries
}

// queueOpen says whether id's pull request is still open, so its order
// still holds a place. A pull request that merged leaves the queue at once,
// while finish-ticket waits for the checks after the merge. One tkt cannot
// read keeps its place.
func (c *Ctx) queueOpen(l *look, id string) bool {
	q := &l.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	if v, ok := q.open[id]; ok {
		return v
	}
	open := true
	switch {
	case len(l.gh.Mine(l.open, id)) > 0:
	case l.src.Forge == "ok" && (len(l.ids) == 0 || slices.Contains(l.ids, id)):
		// The look listed this pull request, and not as open.
		open = false
	default:
		if prs, err := l.gh.ListHead(c.Ctx, id, false); err == nil {
			open = slices.ContainsFunc(l.gh.Mine(prs, id), func(p forge.PR) bool { return p.State == "OPEN" })
		}
	}
	if q.open == nil {
		q.open = map[string]bool{}
	}
	q.open[id] = open
	return open
}

// queuePlace is id's place in the merge queue, from 1, and the ticket
// right ahead of it, or "" at the front. A ticket with no order yet goes
// at the back, where a new order puts it.
func (c *Ctx) queuePlace(l *look, id string) (place int, ahead string) {
	for _, e := range c.queueEntries(l) {
		if e.ID == id {
			break
		}
		if c.queueOpen(l, e.ID) {
			place, ahead = place+1, e.ID
		}
	}
	return place + 1, ahead
}

// backOfQueue is a time for an order that puts it behind every other: now,
// or just after the last order when that is later.
func (c *Ctx) backOfQueue(l *look, id string) time.Time {
	at := c.Now()
	for _, e := range c.queueEntries(l) {
		if e.ID != id && !e.At.Before(at) {
			at = e.At.Add(time.Millisecond)
		}
	}
	return at
}

// queueLen is how many orders in the queue still hold a place.
func (c *Ctx) queueLen(l *look) int {
	n := 0
	for _, e := range c.queueEntries(l) {
		if c.queueOpen(l, e.ID) {
			n++
		}
	}
	return n
}
