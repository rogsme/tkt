package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"gitlab.com/rogs/tkt/internal/agent"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/herdr"
	"gitlab.com/rogs/tkt/internal/runner"
	"gitlab.com/rogs/tkt/internal/state"
)

// WebOpts are tkt web's arguments.
type WebOpts struct {
	// NoOpen prints the page's address without opening a browser.
	NoOpen bool
	// New opens the page in a browser even when one on this machine has it
	// open.
	New bool
	Dry bool
}

// WebResult is the tkt agent's web page: its address, with the token that
// lets a browser in, and how tkt web got there.
type WebResult struct {
	URL string `json:"url"`
	// Started is set when no chat served the page, so tkt web opened tkt
	// agent in a new tab, in Pane.
	Started bool   `json:"started"`
	Pane    string `json:"pane"`
	// Opened is set when tkt web asked the system to open the page in a
	// browser.
	Opened bool `json:"opened"`
	// Here counts the pages open in a browser on this machine.
	// tkt web opens no other then, unless --new.
	Here    int      `json:"here"`
	DryRun  bool     `json:"dry_run"`
	Actions []Action `json:"actions"`
}

func (r *WebResult) Text(w io.Writer) {
	if r.URL == "" {
		fmt.Fprintln(w, "would open tkt agent in a new tab, then its web page")
		return
	}
	fmt.Fprintln(w, r.URL)
}

// webWait bounds how long tkt web waits for a chat it opened to serve its
// page. A first run unpacks pi, which takes a few seconds.
const webWait = 60 * time.Second

// webFilePath is the file where the chat that serves the repository's web
// page names its port and token. tkt agent passes it to pi as TKT_WEB_FILE.
func webFilePath(common string) string { return filepath.Join(common, "tkt", "web") }

// Web opens the tkt agent's web page in a browser. The page lives inside a
// running chat, so when none serves it, tkt web opens tkt agent in a new tab
// of this herdr workspace and waits for the page.
func (a *App) Web(ctx context.Context, o WebOpts) (*WebResult, error) {
	c, err := a.begin(ctx, o.Dry, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	res := &WebResult{DryRun: c.Dry, Actions: []Action{}}
	url, here, ok := c.webPage()
	if !ok {
		if err := c.needHerdr(); err != nil {
			return nil, apperr.New(apperr.WebNotRunning, "no tkt agent chat serves this repository's web page").
				WithHint("run tkt agent in a herdr pane, then /web in the chat")
		}
		if res.Pane, err = c.openChat(); err != nil {
			return nil, err
		}
		res.Started = true
		if c.Dry {
			res.Actions = c.Plan
			return res, nil
		}
		if url, ok = c.awaitWebPage(); !ok {
			return nil, apperr.New(apperr.WebStartTimeout, "tkt agent opened in pane %s, but its web page did not answer within %s", res.Pane, webWait).
				WithHint("look at the chat in pane %s, such as a /login it waits for, then run tkt web again", res.Pane)
		}
	}
	res.URL, res.Here = url, here
	switch {
	case o.NoOpen:
	case here > 0 && !o.New:
		c.UI.Info("the web page is already open in a browser here; tkt web --new opens another")
	default:
		res.Opened = c.openBrowser(url)
	}
	if c.Dry {
		res.Actions = c.Plan
	}
	return res, nil
}

// webPage reads the page a chat serves for the repository and asks the
// chat whether it still serves it, and how many browsers on this machine
// have it open. A chat that quit leaves no file, but one that crashed may
// leave one naming a port nothing answers on, or another program now holds.
func (c *Ctx) webPage() (string, int, bool) {
	file := webFilePath(c.Repo.Common)
	st := state.Read(file)
	port, token := st["port"], st["token"]
	if port == "" || token == "" {
		return "", 0, false
	}
	base := "http://127.0.0.1:" + port
	ctx, cancel := context.WithTimeout(c.Ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	if err != nil {
		return "", 0, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, false
	}
	defer resp.Body.Close()
	var h struct {
		File string `json:"file"`
		Here int    `json:"here"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&h) != nil || h.File != file {
		return "", 0, false
	}
	return base + "/?token=" + token, h.Here, true
}

// openChat opens tkt agent in a new tab of this workspace, in the
// repository's root, and returns its pane.
func (c *Ctx) openChat() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", apperr.New(apperr.Internal, "cannot find the running tkt: %v", err).Wrap(err)
	}
	line, err := agent.ShellWords(self, "agent")
	if err != nil {
		return "", apperr.New(apperr.Internal, "cannot quote %s for the shell", self).Wrap(err)
	}
	ws := c.Getenv("HERDR_WORKSPACE_ID")
	if c.Dry {
		c.Would(herdr.TabCreateArgv(ws, c.Repo.Root, "tkt agent"))
		c.Would(herdr.PaneRunArgv("<pane>", line))
		return "<pane>", nil
	}
	_, pane, err := c.H.TabCreate(c.Ctx, ws, c.Repo.Root, "tkt agent")
	if err != nil {
		return "", herdrErr(err)
	}
	if err := c.H.PaneRun(c.Ctx, pane, line); err != nil {
		return "", herdrErr(err)
	}
	return pane, nil
}

// awaitWebPage waits for a chat tkt web opened to serve its page.
func (c *Ctx) awaitWebPage() (string, bool) {
	deadline := c.Now().Add(webWait)
	for {
		if url, _, ok := c.webPage(); ok {
			return url, true
		}
		if c.Ctx.Err() != nil || c.Now().After(deadline) {
			return "", false
		}
		c.Sleep(500 * time.Millisecond)
	}
}

// openArgv is the command that opens url in the default browser.
func openArgv(goos, url string) []string {
	if goos == "darwin" {
		return []string{"open", url}
	}
	return []string{"xdg-open", url}
}

// openBrowser asks the system to open url in a browser, in a session of its
// own: xdg-open may wait for a browser it starts. A failure is a warning,
// since the address is printed either way.
func (c *Ctx) openBrowser(url string) bool {
	argv := openArgv(runtime.GOOS, url)
	if c.Dry {
		c.Would(argv)
		return true
	}
	sp, ok := c.R.(runner.Spawner)
	if !ok {
		c.UI.Warn("cannot start %s; open the address yourself", argv[0])
		return false
	}
	if err := sp.Spawn(c.Ctx, runner.Cmd{Argv: argv}, os.Environ(), "", os.DevNull); err != nil {
		c.UI.Warn("cannot start %s: %v; open the address yourself", argv[0], err)
		return false
	}
	return true
}
