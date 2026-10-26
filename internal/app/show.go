package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/gitx"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/history"
)

// ShowOpts are tkt show's arguments. Screen, Steps and Diff pick another
// view; ANSI keeps the screen's colors.
type ShowOpts struct {
	ID     string
	Pane   string
	Screen bool
	ANSI   bool
	Steps  bool
	Diff   bool
}

// ShowResult is one view of a ticket: the agent's last message, its live
// screen, the pull request's human verification steps, or the branch's
// diff.
type ShowResult struct {
	Ticket string `json:"ticket"`
	// View is message, screen, steps or diff.
	View        string `json:"view"`
	Pane        string `json:"pane"`
	AgentStatus string `json:"agent_status"`
	Harness     string `json:"harness"`
	Session     string `json:"session"`
	// Source says where Message came from: transcript, api or screen. With
	// --steps it says where the steps came from: review (review-pr's
	// READY_FOR_HUMAN summary) or pull_request (the body, after the bot's
	// CLEAR).
	Source     string `json:"source"`
	LastStatus string `json:"last_status"`
	Message    string `json:"message"`
	// At is the session's last activity.
	At     string `json:"at"`
	Screen string `json:"screen"`
	PR     int    `json:"pr"`
	// Procedure is the whole Human verification section; Steps are its
	// numbered steps.
	Procedure string       `json:"procedure"`
	Steps     []forge.Step `json:"steps"`
	// Verification is verify-pr's newest report on the pull request, with
	// each step's result, or null. AtHead says whether it is about the head
	// the steps are for.
	Verification *forge.Verification `json:"verification"`
	// Base is the branch the diff is against, such as origin/main, and Files
	// what the branch changes since it left it, file by file. The text
	// output prints the diff as git does.
	Base  string      `json:"base"`
	Files []gitx.File `json:"files"`
	diff  string
}

func (r *ShowResult) Text(w io.Writer) {
	switch r.View {
	case "screen":
		fmt.Fprintln(w, strings.TrimRight(r.Screen, "\n"))
	case "diff":
		fmt.Fprint(w, r.diff)
	case "steps":
		fmt.Fprintln(w, r.Procedure)
		if v := r.Verification; v != nil {
			at := "on " + short(v.Head)
			if !v.AtHead {
				at += ", an older head"
			}
			fmt.Fprintf(w, "\nverify-pr: %s %s\n", v.Value, at)
			for _, s := range v.Steps {
				fmt.Fprintf(w, "%d. %s: %s\n", s.N, s.Result, s.Text)
			}
		}
	default:
		fmt.Fprintln(w, strings.TrimRight(r.Message, "\n"))
	}
}

// Show prints one view of a ticket. The default is the agent's last
// message, which after start-ticket is the plan, read from the session's own
// record so nothing scrolls away.
func (a *App) Show(ctx context.Context, o ShowOpts) (*ShowResult, error) {
	c, id, path, ts, err := a.beginTicket(ctx, o.ID, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	res := &ShowResult{Ticket: id, View: "message", Steps: []forge.Step{}, Files: []gitx.File{}}
	if o.Steps {
		res.View = "steps"
		return res, c.showSteps(id, res)
	}
	if o.Diff {
		res.View = "diff"
		return res, c.showDiff(path, res)
	}
	target, err := c.ticketAgent(id, path, o.Pane, ts.st)
	var ag *herdr.Agent
	switch {
	case err == nil:
		ag = &target
		res.Pane, res.AgentStatus = target.PaneID, target.Status
	case o.Screen || apperr.As(err).Code != apperr.AgentMissing:
		return nil, err
	}
	if o.Screen {
		res.View = "screen"
		if o.ANSI {
			res.Screen = c.H.ReadANSI(c.Ctx, target.PaneID, 120)
		} else {
			res.Screen = c.H.Read(c.Ctx, target.PaneID, 120)
		}
		return res, nil
	}
	turn, src := c.lastTurn(c.Ctx, ag, ts.st, path)
	if src == "" {
		return nil, apperr.New(apperr.AgentMissing, "no agent runs for %s and tkt cannot read its session", id).WithHint("tkt resume %s", id)
	}
	res.Source, res.Harness, res.Session = src, ts.st["harness"], ts.st["session"]
	if ag != nil && ag.Kind != "" {
		res.Harness = ag.Kind
		if s := ag.SessionID(); s != "" {
			res.Session = s
		}
	}
	res.Message, res.LastStatus = turn.Final, agent.LastStatus(turn.All)
	if src == "screen" {
		res.Message = turn.All
	}
	if !turn.At.IsZero() {
		res.At = turn.At.UTC().Format(history.TimeFormat)
	}
	return res, nil
}

// showDiff reads what the branch changes since it left its base: the
// committed change its pull request shows, against the base as the last
// fetch left it.
func (c *Ctx) showDiff(path string, res *ShowResult) error {
	f := c.LC.Forge
	ref := "refs/remotes/" + f.Remote + "/" + f.Base
	res.Base = f.Remote + "/" + f.Base
	if !c.Git.HasRef(c.Ctx, path, ref) {
		return apperr.New(apperr.GitFailed, "%s has no %s to compare the branch with", path, res.Base).
			WithHint("git -C %s fetch %s", path, f.Remote)
	}
	text, ok := c.Git.Diff(c.Ctx, path, ref+"...HEAD")
	if !ok {
		return apperr.New(apperr.GitFailed, "git diff %s...HEAD failed in %s", res.Base, path)
	}
	res.diff, res.Files = text, gitx.ParseDiff(text)
	return nil
}

// showSteps reads the human verification steps from the review at the open
// pull request's head.
func (c *Ctx) showSteps(id string, res *ShowResult) error {
	return c.stepsAt(id, "", res)
}

// stepsAt reads the human verification steps for head, or for the open
// pull request's head when head is "": review-pr's, or the pull request
// body's after the review bot's CLEAR.
func (c *Ctx) stepsAt(id, head string, res *ShowResult) error {
	if err := c.LC.RequireGitHub("show --steps"); err != nil {
		return err
	}
	if err := c.needTools("gh"); err != nil {
		return err
	}
	f := c.LC.Forge
	gh := forge.GH{R: c.R, Repo: f.Host + "/" + f.Repo, Name: f.Repo}
	prs, err := gh.ListHead(c.Ctx, id, true)
	if err != nil {
		return err
	}
	for _, p := range gh.Mine(prs, id) {
		if p.State != "OPEN" {
			continue
		}
		res.PR = p.Number
		if v := forge.LatestVerification(p); v != nil {
			v.AtHead = v.Head != "" && strings.HasPrefix(or(head, p.HeadRefOid), v.Head)
			res.Verification = v
		}
		if proc := forge.HumanVerificationAt(p, or(head, p.HeadRefOid)); proc != nil {
			res.Procedure, res.Steps, res.Source = proc.Section, proc.Steps, "review"
			return nil
		}
	}
	if res.PR == 0 {
		return apperr.New(apperr.NoVerification, "%s has no open pull request", id).WithHint("tkt ls %s", id)
	}
	// The review bot's CLEAR leaves no review-pr procedure: the pull
	// request's own Human verification section holds the steps.
	bodies, err := gh.ListBodies(c.Ctx, id)
	if err != nil {
		return err
	}
	for _, p := range gh.Mine(bodies, id) {
		if p.Number != res.PR {
			continue
		}
		if proc := forge.BodyVerification(p); proc != nil {
			res.Procedure, res.Steps, res.Source = proc.Section, proc.Steps, "pull_request"
			return nil
		}
	}
	return apperr.New(apperr.NoVerification, "pull request %s has no Human verification steps, in a review that says READY_FOR_HUMAN at its head or in its body", forge.Ref(res.PR)).
		WithHint("tkt ls %s", id)
}
