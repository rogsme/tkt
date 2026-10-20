package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
	"gitlab.com/rogs/tkt/internal/lock"
	"gitlab.com/rogs/tkt/internal/state"
)

// stateAt reads a worktree's state file, logging the git call under ctx's
// ticket.
func (c *Ctx) stateAt(ctx context.Context, worktree string) (*ticketState, error) {
	dir, err := c.Git.AbsGitDir(ctx, worktree)
	if err != nil {
		return nil, err
	}
	p := state.Path(dir)
	return &ticketState{path: p, st: state.Read(p)}, nil
}

// phasePath is the file next to the state file that records the ticket's
// phase and since when. It is separate so phase changes, which tkt ls
// records, never race with the state the other commands write.
func phasePath(ts *ticketState) string {
	if ts.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ts.path), "phase")
}

// phaseChanged reports whether phase differs from the recorded one. A new
// head while waiting for review counts as a change, because the wait for the
// review bot starts again.
func phaseChanged(rec state.State, phase, head string) bool {
	return rec["phase"] != phase || phase == PhaseWaitingReview && rec["head"] != head
}

// observePhase records a ticket's phase when it changed, in its phase file
// and in the history, and returns when the phase began. Two commands that see
// the same change record it once: the second finds the lock held, or the
// file already up to date.
func (c *Ctx) observePhase(ts *ticketState, id, phase, head string) string {
	p := phasePath(ts)
	if p == "" {
		return ""
	}
	rec := state.Read(p)
	if !phaseChanged(rec, phase, head) || c.Dry || phase == PhaseUnknown {
		return rec["since"]
	}
	f, err := lock.Try(c.Repo.Common, id+".phase")
	if err != nil {
		return rec["since"]
	}
	defer f.Close()
	if rec = state.Read(p); !phaseChanged(rec, phase, head) {
		return rec["since"]
	}
	now := c.Now().UTC().Format(history.TimeFormat)
	if err := (state.State{"phase": phase, "since": now, "head": head}).Save(p, c.Now()); err != nil {
		c.UI.Warn("cannot write %s: %v", p, err)
		return rec["since"]
	}
	c.record(history.Event{Ticket: id, Event: history.Phase, Phase: phase, From: rec["phase"]})
	return now
}

// repoKey names the repository in the history: the forge's host and
// repository, or the primary worktree when the forge names none.
func (c *Ctx) repoKey() string {
	if f := c.LC.Forge; f.Host != "" && f.Repo != "" {
		return f.Host + "/" + f.Repo
	}
	return c.Repo.Root
}

// record appends an event to the history. A failed write is a warning: the
// history must never stop a command.
func (c *Ctx) record(e history.Event) {
	if c.Dry {
		return
	}
	if e.At == "" {
		e.At = c.Now().UTC().Format(history.TimeFormat)
	}
	e.Repo = c.repoKey()
	if err := history.Append(history.Path(c.Getenv), e); err != nil {
		c.UI.Warn("cannot write the history: %v", err)
	}
}

// lastTurn reads what the agent said since the last prompt: from its
// session's own record when tkt can read it, otherwise from its recent
// output. source says which: transcript, api or screen.
func (c *Ctx) lastTurn(ctx context.Context, ag *herdr.Agent, st state.State, path string) (harness.Turn, string) {
	h, sid := st["harness"], st["session"]
	if ag != nil {
		if ag.Kind == config.Claude || ag.Kind == config.OpenCode {
			h = ag.Kind
		}
		if s := ag.SessionID(); s != "" && h == config.Claude {
			sid = s
		}
	}
	turn, ok := c.Agent.LastTurn(ctx, h, sid, path)
	if ok {
		if h == config.OpenCode {
			return turn, "api"
		}
		return turn, "transcript"
	}
	if ag == nil {
		return harness.Turn{At: turn.At}, ""
	}
	return harness.Turn{All: c.H.ReadRecent(ctx, ag.PaneID, 200), At: turn.At}, "screen"
}

// ticketAgent picks the ticket's agent: the one in pane want, the only one
// in the worktree, or, among several, the one on the recorded pane.
func (c *Ctx) ticketAgent(id, path, want string, st state.State) (herdr.Agent, error) {
	agents, err := c.H.Agents(c.Ctx)
	if err != nil {
		return herdr.Agent{}, herdrErr(err)
	}
	inPath := herdr.AgentsInPath(agents, path)
	switch {
	case want != "":
		i := slices.IndexFunc(inPath, func(ag herdr.Agent) bool { return ag.PaneID == want })
		if i < 0 {
			return herdr.Agent{}, apperr.New(apperr.AgentMissing, "no agent of %s runs in pane %s", id, want).WithHint("tkt ls %s", id)
		}
		return inPath[i], nil
	case len(inPath) == 0:
		return herdr.Agent{}, apperr.New(apperr.AgentMissing, "no agent runs for %s", id).WithHint("tkt resume %s", id)
	case len(inPath) == 1:
		return inPath[0], nil
	}
	i := slices.IndexFunc(inPath, func(ag herdr.Agent) bool { return ag.PaneID == st["pane"] })
	if i < 0 {
		panes := make([]string, len(inPath))
		for j, ag := range inPath {
			panes[j] = ag.PaneID
		}
		return herdr.Agent{}, apperr.New(apperr.AmbiguousPane, "%d agents run in %s; pick one with --pane (%s)", len(inPath), path, strings.Join(panes, ", ")).
			WithHint("pass --pane %s", panes[0]).With("panes", panes)
	}
	return inPath[i], nil
}

// The values of the auto state key: on drives the ticket from the approved
// plan on; plan also approves plans.
const (
	autoOn   = "on"
	autoPlan = "plan"
)

// autoRunning reports whether a tkt auto loop holds the ticket, by trying
// its lock for a moment.
func (c *Ctx) autoRunning(id string) bool {
	f, err := lock.Try(c.Repo.Common, id+".auto")
	if errors.Is(err, lock.ErrHeld) {
		return true
	}
	if err == nil {
		f.Close()
	}
	return false
}
