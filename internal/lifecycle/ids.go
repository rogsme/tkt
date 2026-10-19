package lifecycle

import (
	"path/filepath"
	"regexp"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
)

var digits = regexp.MustCompile(`^[0-9]+$`)

// NormalizeID turns what a person typed into a ticket id: "12" gains the
// tracker's prefix, and a lowercase id is uppercased when only that matches.
func (c Config) NormalizeID(s string) (string, error) {
	id := s
	if digits.MatchString(id) && c.Prefix != "" {
		id = c.Prefix + "-" + id
	}
	if !c.RE.MatchString(id) {
		if up := strings.ToUpper(id); c.RE.MatchString(up) {
			id = up
		}
	}
	if !c.RE.MatchString(id) {
		return "", apperr.New(apperr.InvalidTicketID, "'%s' is not a ticket id (expected %s)", s, c.Pattern)
	}
	return id, nil
}

var scratchName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ScratchName checks a name for a worktree with no ticket. It must work as a
// branch and a directory name, and must not pass for a ticket id, so ls and
// done never take the worktree for a ticket's.
func (c Config) ScratchName(s string) error {
	if !scratchName.MatchString(s) {
		return apperr.New(apperr.InvalidScratchName,
			"'%s' is not a scratch name: use letters, digits, '-' and '_', starting with a letter or digit", s).
			WithHint("tkt start --scratch spike-login")
	}
	if id, err := c.NormalizeID(s); err == nil {
		return apperr.New(apperr.InvalidScratchName, "'%s' is a ticket id; start the ticket instead, or pick another name", s).
			WithHint("tkt start %s", id)
	}
	return nil
}

var idToken = regexp.MustCompile(`[A-Za-z0-9-]+`)

// FirstID returns the first ticket id in text, or "" when it names none.
func (c Config) FirstID(text string) string {
	for _, tok := range idToken.FindAllString(text, -1) {
		if c.RE.MatchString(tok) {
			return tok
		}
	}
	return ""
}

// IsTicketWorktree reports whether a worktree belongs to a ticket: its branch
// is a ticket id and its directory is named after the branch.
func (c Config) IsTicketWorktree(branch, path string) bool {
	return c.RE.MatchString(branch) && filepath.Base(path) == branch
}
