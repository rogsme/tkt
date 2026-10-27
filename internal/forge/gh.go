// Package forge reads pull requests, their checks and their review verdicts,
// and comments on them. Only GitHub, through gh, is supported.
package forge

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/runner"
)

// GH reads, and comments on, pull requests of one repository, named
// host/owner/name.
type GH struct {
	R    runner.Runner
	Repo string
	// Name is owner/name, which a pull request's head repository must match.
	Name string
}

// PR is a pull request as gh pr list --json prints it.
type PR struct {
	Number         int    `json:"number"`
	State          string `json:"state"`
	HeadRefName    string `json:"headRefName"`
	HeadRefOid     string `json:"headRefOid"`
	HeadRepository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	Mergeable         string   `json:"mergeable"`
	StatusCheckRollup []Check  `json:"statusCheckRollup"`
	Reviews           []Review `json:"reviews"`
	CreatedAt         string   `json:"createdAt"`
	MergedAt          string   `json:"mergedAt"`
	// Body is read only by ListBodies.
	Body string `json:"body"`
	// Title and URL are read by ListOpen, ListHead with full, and
	// ListHistory; the size and the commits only by ListHistory.
	Title        string   `json:"title"`
	URL          string   `json:"url"`
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
	ChangedFiles int      `json:"changedFiles"`
	Commits      []Commit `json:"commits"`
}

// Commit is a commit of a pull request.
type Commit struct {
	Oid             string `json:"oid"`
	MessageHeadline string `json:"messageHeadline"`
	CommittedDate   string `json:"committedDate"`
}

// Check is a check run (Name, Status, Conclusion) or a commit status
// (Context, State).
type Check struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion string  `json:"conclusion"`
	Context    *string `json:"context"`
	State      string  `json:"state"`
}

// Review is a submitted review.
type Review struct {
	Body        string `json:"body"`
	SubmittedAt string `json:"submittedAt"`
	Commit      *struct {
		Oid string `json:"oid"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

func (g GH) list(ctx context.Context, args ...string) ([]PR, error) {
	argv := append([]string{"gh", "pr", "list", "--repo", g.Repo}, args...)
	r := g.R.Run(ctx, runner.Cmd{Argv: argv, Timeout: 60 * time.Second})
	if !r.OK() {
		return nil, apperr.New(apperr.ForgeQueryFailed, "gh pr list failed: %s", runner.Head(strings.TrimSpace(r.ErrText()), 200))
	}
	var prs []PR
	if strings.TrimSpace(r.Out()) == "" {
		return prs, nil
	}
	if err := json.Unmarshal(r.Stdout, &prs); err != nil {
		return nil, apperr.New(apperr.ForgeQueryFailed, "gh pr list printed output tkt cannot read").Wrap(err)
	}
	return prs, nil
}

// CommentArgv is the gh command that comments body on pull request n.
func (g GH) CommentArgv(n int, body string) []string {
	return []string{"gh", "pr", "comment", strconv.Itoa(n), "--repo", g.Repo, "--body", body}
}

// Comment comments body on pull request n, and returns the comment's URL.
func (g GH) Comment(ctx context.Context, n int, body string) (string, error) {
	r := g.R.Run(ctx, runner.Cmd{Argv: g.CommentArgv(n, body), Timeout: 60 * time.Second})
	if !r.OK() {
		return "", apperr.New(apperr.ForgeCommentFailed, "gh pr comment failed: %s", runner.Head(strings.TrimSpace(r.ErrText()), 200))
	}
	return strings.TrimSpace(r.Out()), nil
}

// ListBodies returns the branch's open pull requests with their bodies.
func (g GH) ListBodies(ctx context.Context, branch string) ([]PR, error) {
	return g.list(ctx, "--head", branch, "--state", "open", "--limit", "10", "--json", "number,state,headRefName,headRefOid,headRepository,body")
}

// ListOpen returns the open pull requests with their checks and reviews.
func (g GH) ListOpen(ctx context.Context) ([]PR, error) {
	return g.list(ctx, "--state", "open", "--limit", "100",
		"--json", "number,title,url,headRefName,headRefOid,headRepository,mergeable,statusCheckRollup,reviews")
}

// ListAll returns the latest pull requests in any state.
func (g GH) ListAll(ctx context.Context) ([]PR, error) {
	return g.list(ctx, "--state", "all", "--limit", "200", "--json", "number,state,headRefName,headRepository")
}

// ListHead returns every pull request whose head is branch.
func (g GH) ListHead(ctx context.Context, branch string, full bool) ([]PR, error) {
	fields := "number,state,headRefName,headRefOid,headRepository,createdAt,mergedAt"
	if full {
		fields += ",title,url,mergeable,statusCheckRollup,reviews"
	}
	return g.list(ctx, "--head", branch, "--state", "all", "--limit", "100", "--json", fields)
}

// ListHistory returns every pull request whose head is branch, with what
// tkt history shows: the title, the size, the commits and the reviews.
func (g GH) ListHistory(ctx context.Context, branch string) ([]PR, error) {
	return g.list(ctx, "--head", branch, "--state", "all", "--limit", "20", "--json",
		"number,state,title,url,headRefName,headRefOid,headRepository,createdAt,mergedAt,additions,deletions,changedFiles,commits,reviews")
}

// Mine keeps the pull requests whose head is branch in this repository, not
// in a fork.
func (g GH) Mine(prs []PR, branch string) []PR {
	var out []PR
	for _, p := range prs {
		repo := g.Name
		if p.HeadRepository != nil && p.HeadRepository.NameWithOwner != "" {
			repo = p.HeadRepository.NameWithOwner
		}
		if p.HeadRefName == branch && strings.EqualFold(repo, g.Name) {
			out = append(out, p)
		}
	}
	return out
}

// Ref renders a pull request number as "#7".
func Ref(n int) string { return "#" + strconv.Itoa(n) }
