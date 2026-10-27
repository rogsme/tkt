package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/forge"
	"gitlab.com/rogs/tkt/internal/history"
)

// VerifiedOpts are tkt verified's arguments: the steps that passed, the head
// they ran on, and whether you confirmed them from verify-pr's evidence.
type VerifiedOpts struct {
	ID       string
	Passed   string
	Head     string
	Evidence bool
	Dry      bool
}

// VerifiedResult is what tkt verified did. Action is commented, or none when
// the pull request has the comment for this head already. URL is the
// comment's.
type VerifiedResult struct {
	Ticket  string   `json:"ticket"`
	PR      int      `json:"pr"`
	Head    string   `json:"head"`
	Steps   []int    `json:"steps"`
	Action  string   `json:"action"`
	URL     string   `json:"url"`
	DryRun  bool     `json:"dry_run"`
	Actions []Action `json:"actions"`
}

func (r *VerifiedResult) Text(w io.Writer) {
	switch {
	case r.Action == "none":
		fmt.Fprintf(w, "%s  %s has the comment for %s already\n", r.Ticket, forge.Ref(r.PR), short(r.Head))
	case r.URL != "":
		fmt.Fprintf(w, "%s  commented on %s: %s\n", r.Ticket, forge.Ref(r.PR), r.URL)
	}
}

// keyVerifiedHead is the head whose passing steps tkt verified commented
// on, so it comments once per head.
const keyVerifiedHead = "verified_head"

// verifiedComment is the comment that says every step passed on head.
func verifiedComment(steps []int, head string, evidence bool) string {
	line := strings.ToUpper(config.StepsText(steps)[:1]) + config.StepsText(steps)[1:] + " passed on head " + head
	if evidence {
		line += ", confirmed from verify-pr's evidence"
	}
	return "Human verification passed ✅\n\n" + line + "."
}

// Verified comments on a ticket's pull request that every human
// verification step passed, once per head. It checks the steps as tkt merge
// does: every step of the procedure at the head they ran on, which must be
// the pull request's head or one clean merges of the base left behind, and
// with --evidence, verify-pr's report marks each one passed.
func (a *App) Verified(ctx context.Context, o VerifiedOpts) (*VerifiedResult, error) {
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
	if err := c.LC.RequireGitHub("verified"); err != nil {
		return nil, err
	}
	if err := c.needTools("gh"); err != nil {
		return nil, err
	}
	path, err := c.ticketPath(id)
	if err != nil {
		return nil, err
	}
	if err := c.lockTicket(id); err != nil {
		return nil, err
	}
	ts, err := c.readState(path)
	if err != nil {
		return nil, err
	}
	l, err := c.look(lookOpts{ids: []string{id}})
	if err != nil {
		return nil, err
	}
	if l.src.Forge != "ok" {
		if l.forgeErr != nil {
			return nil, l.forgeErr
		}
		return nil, apperr.New(apperr.ForgeQueryFailed, "tkt verified cannot read %s's pull request", id)
	}
	row, _ := c.row(c.Ctx, l, ticketWorktree{ID: id, Path: path})
	if row.PR == nil || row.PR.State != "OPEN" {
		return nil, apperr.New(apperr.NoVerification, "%s has no open pull request", id).WithHint("tkt ls %s", id)
	}
	res := &VerifiedResult{Ticket: id, PR: row.PR.Number, Head: row.PR.Head, Steps: []int{}, DryRun: c.Dry, Actions: []Action{}}
	if v := row.Verification; o.Evidence && o.Head == "" && v != nil {
		o.Head = v.Head
	}
	if o.Head != "" && !strings.HasPrefix(row.PR.Head, o.Head) {
		v := c.Git.Rev(c.Ctx, path, o.Head+"^{commit}")
		clean := false
		if v != "" {
			if clean, err = c.Git.CleanMerges(c.Ctx, path, v, row.PR.Head, l.baseRef); err != nil {
				return nil, err
			}
		}
		if !clean {
			return nil, apperr.New(apperr.MergeHeadMoved, "%s's pull request is at %s now, and more than clean merges of the base came after %s, where the steps ran; run them again on the new head", id, short(row.PR.Head), o.Head).
				WithHint("tkt show %s --steps", id).With("head", row.PR.Head)
		}
		res.Head = v
	}
	steps, err := c.passedSteps(id, res.Head, o.Passed)
	if err != nil {
		return nil, err
	}
	res.Steps = steps
	if o.Evidence {
		if err := evidenceFor(id, row.Verification, res.Head, steps); err != nil {
			return nil, err
		}
	}
	if ts.st[keyVerifiedHead] == res.Head {
		res.Action = "none"
		return res, nil
	}
	gh := forge.GH{R: c.R, Repo: c.LC.Forge.Host + "/" + c.LC.Forge.Repo, Name: c.LC.Forge.Repo}
	body := verifiedComment(steps, res.Head, o.Evidence)
	if c.Dry {
		c.Would(gh.CommentArgv(res.PR, body))
		res.Action, res.Actions = "commented", c.Plan
		return res, nil
	}
	if res.URL, err = gh.Comment(c.Ctx, res.PR, body); err != nil {
		return nil, err
	}
	c.save(ts, keyVerifiedHead, res.Head)
	how := ""
	if o.Evidence {
		how = "evidence"
	}
	c.record(history.Event{Ticket: id, Event: history.Verified, PR: res.PR, Action: how})
	res.Action = "commented"
	return res, nil
}
