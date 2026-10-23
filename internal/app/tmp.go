package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ticketTmp returns the ticket's private temporary directory, and creates it
// the first time: a directory of mode 0700 with a random name under the
// system's temporary directory. tkt sets TMPDIR to it in every tab it opens
// for the ticket, so the agents of two tickets never write the same file,
// and tkt done removes it. A failure only costs the isolation.
func (c *Ctx) ticketTmp(id string, ts *ticketState) string {
	if d := ts.st["tmpdir"]; d != "" {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	prefix := "tkt-" + c.tmpSlug() + "-" + id + "-"
	root := tmpRoot(c.Getenv("TMPDIR"))
	if c.Dry {
		return filepath.Join(root, prefix+"XXXXXX")
	}
	d, err := os.MkdirTemp(root, prefix+"*")
	if err != nil {
		c.UI.Warn("cannot create a temporary directory for %s: %v; its agents share the system's", id, err)
		return ""
	}
	c.save(ts, "tmpdir", d)
	return d
}

// LeaveTicketTmp takes the process's TMPDIR out of a ticket's directory: a
// tkt run in a ticket's tab sees that ticket's, which tkt done removes, and
// the programs tkt starts, such as an OpenCode service that outlives it,
// get the directory above it instead.
func LeaveTicketTmp(getenv func(string) string, setenv func(string, string) error) {
	if d := getenv("TMPDIR"); d != "" && tmpRoot(d) != d {
		setenv("TMPDIR", tmpRoot(d))
	}
}

// tmpRoot is where ticket directories go: TMPDIR, or /tmp. A tkt command run
// in a ticket's tab sees that ticket's TMPDIR, which tkt done removes, so it
// climbs out of it.
func tmpRoot(dir string) string {
	for dir != "" && dir != "/" && strings.HasPrefix(filepath.Base(dir), "tkt-") {
		dir = filepath.Dir(dir)
	}
	if dir == "" || dir == "/" {
		return "/tmp"
	}
	return dir
}

var slugUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// tmpSlug names the repository in a ticket directory: acme-app for the forge
// repository acme/app, or the primary worktree's directory.
func (c *Ctx) tmpSlug() string {
	s := strings.ReplaceAll(c.LC.Forge.Repo, "/", "-")
	if s == "" {
		s = filepath.Base(c.Repo.Root)
	}
	return slugUnsafe.ReplaceAllString(s, "-")
}

// removeTmp removes a ticket's temporary directory. It removes only a
// directory named the way ticketTmp names this ticket's, whatever path the
// state file holds.
func (c *Ctx) removeTmp(id, dir string) {
	base := filepath.Base(dir)
	if dir == "" || !strings.HasPrefix(base, "tkt-") || !strings.Contains(base, "-"+id+"-") {
		return
	}
	if c.Dry {
		c.Would([]string{"rm", "-rf", dir})
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		c.UI.Warn("cannot remove %s: %v", dir, err)
	}
}
