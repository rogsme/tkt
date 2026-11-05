package app

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/lock"
)

// A sync's merge and push run the repository's hooks, which may run its
// whole test suite. However many tickets' bases move at once, only [sync]
// pushes of them run on the machine at a time, each once [sync] min_free of
// memory is free: the slots are the machine's, in tkt's state directory, since
// every repository's hooks share its memory.

// How long a sync a person asked for waits for its turn to push, and how
// often it looks.
var (
	pushWaitFor = 30 * time.Minute
	pushPoll    = time.Second
)

// The things a sync's push waits on.
const (
	PushOnSlots  = "slots"
	PushOnMemory = "memory"
)

// PushWait says what keeps a due sync from merging and pushing. On is slots
// when every [sync] pushes slot is taken, with Pushes naming the syncs that
// hold them, or memory when less than [sync] min_free is free, with Free
// and Need in bytes.
type PushWait struct {
	On     string   `json:"on"`
	Pushes []string `json:"pushes"`
	Free   uint64   `json:"free"`
	Need   uint64   `json:"need"`
}

// Text says what the sync waits for, after "waits for".
func (w *PushWait) Text() string {
	if w.On == PushOnMemory {
		return fmt.Sprintf("free memory: %s free of the %s a push needs ([sync] min_free)", gib(w.Free), gib(w.Need))
	}
	if len(w.Pushes) == 0 {
		return "a push slot ([sync] pushes)"
	}
	verb := "push"
	if len(w.Pushes) == 1 {
		verb = "pushes"
	}
	return fmt.Sprintf("a push slot: %s %s ([sync] pushes)", strings.Join(w.Pushes, " and "), verb)
}

func gib(b uint64) string { return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + " GB" }

// stateHome is the directory above tkt's state directory, where the
// machine's push slots live.
func (c *Ctx) stateHome() string {
	return cmp.Or(c.Getenv("XDG_STATE_HOME"), filepath.Join(c.Getenv("HOME"), ".local", "state"))
}

func pushSlot(k int) string { return "push-" + strconv.Itoa(k) }

// pushName is what a sync writes into its slot: its repository and ticket.
func (c *Ctx) pushName(id string) string {
	return cmp.Or(c.LC.Forge.Repo, filepath.Base(c.Repo.Root)) + " " + id
}

// pushHolder names a slot's sync for this repository's messages: its ticket
// id, with the repository in front when it is another one's.
func (c *Ctx) pushHolder(name string) string {
	repo, id, ok := strings.Cut(name, " ")
	if !ok || repo == cmp.Or(c.LC.Forge.Repo, filepath.Base(c.Repo.Root)) {
		return id
	}
	return name
}

// memFree reads MemAvailable and MemTotal, in bytes, from the text of
// /proc/meminfo.
func memFree(text string) (avail, total uint64, ok bool) {
	var haveAvail, haveTotal bool
	s := bufio.NewScanner(strings.NewReader(text))
	for s.Scan() {
		key, rest, found := strings.Cut(s.Text(), ":")
		if !found {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			n *= 1 << 10
		}
		switch key {
		case "MemAvailable":
			avail, haveAvail = n, true
		case "MemTotal":
			total, haveTotal = n, true
		}
	}
	return avail, total, haveAvail && haveTotal
}

// lowMemory reports what a push waits for when less memory is free than
// [sync] min_free asks, or nil. A system without /proc/meminfo, such as
// macOS, has the slots alone. TKT_MEMINFO names another file, for tests.
func (c *Ctx) lowMemory() *PushWait {
	b, err := os.ReadFile(cmp.Or(c.Getenv("TKT_MEMINFO"), "/proc/meminfo"))
	if err != nil {
		return nil
	}
	avail, total, ok := memFree(string(b))
	if !ok {
		return nil
	}
	if need := c.Cfg.Sync.MinFree.Of(total); avail < need {
		return &PushWait{On: PushOnMemory, Pushes: []string{}, Free: avail, Need: need}
	}
	return nil
}

// pushWait reports what keeps id's sync from pushing now, or nil when it
// could, or when id's own sync holds a slot. It takes no slot to keep.
func (c *Ctx) pushWait(id string) *PushWait {
	dir, own := c.stateHome(), c.pushName(id)
	w := &PushWait{On: PushOnSlots, Pushes: []string{}}
	for k := range c.Cfg.Sync.Pushes {
		h := lock.Holder(dir, pushSlot(k))
		switch h {
		case "":
			return c.lowMemory()
		case own:
			return nil
		}
		w.Pushes = append(w.Pushes, c.pushHolder(h))
	}
	return w
}

// tryPush takes a free push slot for id's sync and marks it, when memory
// allows, or says what the sync waits for.
func (c *Ctx) tryPush(id string) (*os.File, *PushWait, error) {
	dir := c.stateHome()
	w := &PushWait{On: PushOnSlots, Pushes: []string{}}
	for k := range c.Cfg.Sync.Pushes {
		f, err := lock.Try(dir, pushSlot(k))
		if errors.Is(err, lock.ErrHeld) {
			if h := lock.Holder(dir, pushSlot(k)); h != "" {
				w.Pushes = append(w.Pushes, c.pushHolder(h))
			}
			continue
		}
		if err != nil {
			return nil, nil, apperr.New(apperr.Internal, "cannot take a push slot: %v", err)
		}
		if low := c.lowMemory(); low != nil {
			f.Close()
			return nil, low, nil
		}
		if err := lock.Mark(f, c.pushName(id)); err != nil {
			c.UI.Warn("cannot mark %s's push slot: %v", id, err)
		}
		return f, nil, nil
	}
	return nil, w, nil
}

// takePush takes a push slot for id's sync and returns what releases it.
// With wait, as when a person runs tkt sync, it waits for one, saying once
// on what, up to pushWaitFor; without, as for tkt watch and tkt auto, which
// try again at their next look, it returns SyncPushBusy at once. A dry run
// takes nothing.
func (c *Ctx) takePush(id string, wait bool) (func(), error) {
	if c.Dry {
		return func() {}, nil
	}
	deadline := time.Now().Add(pushWaitFor)
	said := ""
	for {
		f, w, err := c.tryPush(id)
		if err != nil {
			return nil, err
		}
		if f != nil {
			return func() { f.Close() }, nil
		}
		if !wait || time.Now().After(deadline) {
			return nil, pushBusy(id, w, "")
		}
		if t := w.Text(); t != said {
			c.UI.Info("%s: waits for %s", id, t)
			said = t
		}
		select {
		case <-c.Ctx.Done():
			return nil, pushBusy(id, w, "tkt sync stopped while it waited, before it merged; ")
		case <-time.After(pushPoll):
		}
	}
}

// pushBusy is the error of a sync that could not take its turn to push. It
// changed nothing, and is no failure for sync-fix to take: a later sync
// goes once the turn comes.
func pushBusy(id string, w *PushWait, lead string) error {
	return apperr.New(apperr.SyncPushBusy, "%s%s waits for %s", lead, id, w.Text()).
		WithHint("tkt sync %s goes once its turn comes; tkt watch and tkt auto try again by themselves", id).
		With("push_wait", w)
}
