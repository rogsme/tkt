package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/state"
)

// HookOpts are tkt hook tmp's arguments. Install writes the OpenCode plugin
// instead of answering it.
type HookOpts struct {
	Install bool
	Dry     bool
}

// HookResult is tkt hook tmp's answer to the OpenCode plugin about one tool
// call or shell: the ticket whose worktree it runs in, the TMPDIR it gets,
// its input or command with the /tmp paths moved (null or "" when nothing
// moved), and the line for the agent's system prompt. Outside a ticket,
// TmpDir is set only to take a shell out of a ticket's directory it
// inherited. With --install, Plugin is the file it wrote, and Installed
// says it changed.
type HookResult struct {
	Ticket    string         `json:"ticket"`
	TmpDir    string         `json:"tmpdir"`
	Input     map[string]any `json:"input"`
	Command   string         `json:"command"`
	System    string         `json:"system"`
	Plugin    string         `json:"plugin"`
	Installed bool           `json:"installed"`
	DryRun    bool           `json:"dry_run"`
	Actions   []Action       `json:"actions"`
}

func (r *HookResult) Text(w io.Writer) {
	switch {
	case r.Plugin != "":
		fmt.Fprintln(w, r.Plugin)
	case r.Command != "":
		fmt.Fprintln(w, r.Command)
	case r.Input != nil:
		b, _ := json.Marshal(r.Input)
		fmt.Fprintln(w, string(b))
	case r.TmpDir != "":
		fmt.Fprintln(w, r.TmpDir)
	}
}

// hookRequest is what the plugin asks: the directories the call runs in,
// first the most precise, the shell's TMPDIR, and the tool input or the
// shell command to look at.
type hookRequest struct {
	Directories []string       `json:"directories"`
	TmpDir      string         `json:"tmpdir"`
	Tool        string         `json:"tool"`
	Input       map[string]any `json:"input"`
	Command     string         `json:"command"`
}

// Hook answers the OpenCode plugin's question about one tool call or shell,
// read as JSON from stdin. OpenCode runs every agent's tools in its one
// background service, so the TMPDIR a ticket's tab has never reaches them;
// the plugin gives each shell the ticket's own instead, and moves the /tmp
// paths an agent types into it. It reads only files, so it stays fast: it
// runs on every tool call.
func (a *App) Hook(ctx context.Context, o HookOpts) (*HookResult, error) {
	if o.Install {
		return a.installPlugin(ctx, o.Dry)
	}
	var req hookRequest
	if err := json.NewDecoder(io.LimitReader(a.Stdin, 64<<20)).Decode(&req); err != nil {
		return nil, apperr.New(apperr.Usage, "tkt hook tmp reads a JSON request on stdin: %v", err).
			WithHint(`echo '{"directories": ["DIR"], "command": "..."}' | tkt hook tmp`)
	}
	res := &HookResult{Actions: []Action{}}
	for _, d := range req.Directories {
		if d == "" {
			continue
		}
		if id, tmp := ticketAt(d); id != "" {
			res.Ticket, res.TmpDir = id, tmp
			break
		}
	}
	if res.Ticket == "" || !ownTmp(res.TmpDir, res.Ticket) {
		// A service started in a ticket's tab hands its shells that
		// ticket's directory, which tkt done removes.
		res.TmpDir = ""
		if t := req.TmpDir; t != "" && tmpRoot(t) != t {
			res.TmpDir = tmpRoot(t)
		}
		return res, nil
	}
	// systemd-tmpfiles may have cleaned it up since tkt start made it.
	if err := os.MkdirAll(res.TmpDir, 0o700); err != nil {
		res.TmpDir = ""
		return res, nil
	}
	if req.Command != "" {
		if c := moveTmp(req.Command, res.TmpDir, "/tmp", true); c != req.Command {
			res.Command = c
		}
	}
	if req.Input != nil {
		res.Input = moveInput(req.Input, res.TmpDir, "/tmp")
	}
	res.System = tmpLine(res.TmpDir)
	return res, nil
}

// ticketAt finds the ticket whose worktree holds dir, and its temporary
// directory, from the worktree's state file. A linked worktree's .git is a
// file naming its git directory; the primary worktree's is a directory, and
// holds no ticket.
func ticketAt(dir string) (id, tmp string) {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		dot := filepath.Join(d, ".git")
		if fi, err := os.Lstat(dot); err == nil {
			if fi.IsDir() {
				return "", ""
			}
			b, err := os.ReadFile(dot)
			gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
			if err != nil || !ok {
				return "", ""
			}
			if !filepath.IsAbs(gitdir) {
				gitdir = filepath.Join(d, gitdir)
			}
			st := state.Read(state.Path(gitdir))
			return st["ticket"], st["tmpdir"]
		}
		if d == filepath.Dir(d) {
			return "", ""
		}
	}
}

// ownTmp reports whether dir is named the way ticketTmp names id's
// directory, so the hook never hands out, or creates, another.
func ownTmp(dir, id string) bool {
	base := filepath.Base(dir)
	return dir != "" && filepath.IsAbs(dir) && strings.HasPrefix(base, "tkt-") && strings.Contains(base, "-"+id+"-")
}

// tmpLine is the line an agent's system prompt gets, so it expects the
// move instead of fighting it.
func tmpLine(dir string) string {
	return "This ticket has its own temporary directory, " + dir + ", which is $TMPDIR in your shells. tkt moves a new path under /tmp in your tool calls into it: /tmp/notes.md becomes " + dir + "/notes.md, and later calls find it there. That move is expected. A file that was in /tmp already stays where it is."
}

// The fields of a tool's input that hold a path or a command, in Claude
// Code's and OpenCode's built-in tools. Content and text fields stay as
// written: they are a file's content, or words.
var (
	pathFields  = []string{"path", "filePath", "file_path", "workdir", "cwd", "directory"}
	shellFields = []string{"command"}
)

// moveInput returns a copy of a tool's input with its /tmp paths moved, or
// nil when nothing moved.
func moveInput(in map[string]any, dir, root string) map[string]any {
	var out map[string]any
	for k, v := range in {
		s, ok := v.(string)
		if !ok {
			continue
		}
		moved := s
		switch {
		case slices.Contains(pathFields, k):
			moved = moveTmp(s, dir, root, false)
		case slices.Contains(shellFields, k):
			moved = moveTmp(s, dir, root, true)
		}
		if moved == s {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(in))
			for k2, v2 := range in {
				out[k2] = v2
			}
		}
		out[k] = moved
	}
	return out
}

// Where a path starts, and where its first name ends.
const (
	pathStart = " \t\n'\"`=(;,|&<>{[!"
	nameEnd   = "/ \t\n'\"`;)|&<>}],*?[$\\"
)

// moveTmp moves the /tmp paths in s into dir, the ticket's own temporary
// directory. A path /tmp/NAME... moves when NAME is in dir already, or in
// neither dir nor root (the real /tmp): a new file lands in dir, and later
// calls find it there. It stays when root holds NAME already, such as a
// file the user put there, and when NAME is one other programs own: a
// hidden name, a ticket's directory (tkt-), Claude Code's and OpenCode's
// own. A bare /tmp stays, since it is as often a word in a message or a
// search as a place, and so does a path after a colon, which may be on
// another host. In a shell command, here-document bodies stay as written:
// they are often a file's content.
func moveTmp(s, dir, root string, shell bool) string {
	var b strings.Builder
	var pending []string
	for i := 0; i < len(s); {
		if shell && s[i] == '\n' && len(pending) > 0 {
			b.WriteByte('\n')
			i++
			for _, word := range pending {
				for i < len(s) {
					next := len(s)
					if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
						next = i + n + 1
					}
					line := strings.TrimRight(s[i:next], "\n")
					b.WriteString(s[i:next])
					i = next
					if strings.TrimLeft(line, "\t") == word {
						break
					}
				}
			}
			pending = nil
			continue
		}
		if shell && strings.HasPrefix(s[i:], "<<") && !strings.HasPrefix(s[i:], "<<<") {
			if word, n := heredocWord(s[i+2:]); word != "" {
				pending = append(pending, word)
				b.WriteString(s[i : i+2+n])
				i += 2 + n
				continue
			}
		}
		// A path in dir is where it belongs, wherever TMPDIR put dir.
		if strings.HasPrefix(s[i:], dir) && (i+len(dir) == len(s) || strings.IndexByte(nameEnd, s[i+len(dir)]) >= 0) {
			b.WriteString(dir)
			i += len(dir)
			continue
		}
		if strings.HasPrefix(s[i:], "/tmp/") && (i == 0 || strings.IndexByte(pathStart, s[i-1]) >= 0) {
			j := i + len("/tmp/")
			k := j
			for k < len(s) && strings.IndexByte(nameEnd, s[k]) < 0 {
				k++
			}
			if name := s[j:k]; name != "" && moves(name, dir, root) {
				b.WriteString(dir)
				i += len("/tmp")
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// heredocWord reads the word that ends a here-document, after its <<: an
// optional -, then a plain or quoted word. It returns the word and how many
// bytes it took, or "" when there is none.
func heredocWord(s string) (string, int) {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i < len(s) && s[i] == '\\' {
		i++
	}
	if i < len(s) && (s[i] == '\'' || s[i] == '"') {
		end := strings.IndexByte(s[i+1:], s[i])
		if end <= 0 {
			return "", 0
		}
		return s[i+1 : i+1+end], i + 2 + end
	}
	j := i
	for j < len(s) && (s[j] == '_' || s[j] == '-' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
		j++
	}
	if j == i {
		return "", 0
	}
	return s[i:j], j
}

// moves decides whether /tmp/name belongs in dir.
func moves(name, dir, root string) bool {
	for _, p := range []string{".", "tkt-", "claude-", "opencode", "systemd-", "tmux-", "ssh-"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
		return true
	}
	_, err := os.Lstat(filepath.Join(root, name))
	return err != nil
}

// OpenCodePluginPath is where tkt's OpenCode plugin goes: OpenCode loads
// every .js file in its global plugins directory.
func OpenCodePluginPath(getenv func(string) string) string {
	dir := getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "opencode", "plugins", "tkt.js")
}

// installPlugin writes tkt's plugin into OpenCode's global plugins
// directory, naming the tkt on PATH: the OpenCode service's own PATH may
// not find it.
func (a *App) installPlugin(ctx context.Context, dry bool) (*HookResult, error) {
	tkt, err := exec.LookPath("tkt")
	if err != nil {
		return nil, apperr.New(apperr.ToolMissing, "tkt is not on PATH, and the plugin runs the tkt your shell finds").
			WithHint("put tkt on PATH, then run tkt hook tmp --install again")
	}
	tkt, _ = filepath.Abs(tkt)
	c := &Ctx{App: a, Ctx: ctx, Dry: dry}
	path := OpenCodePluginPath(a.Getenv)
	res := &HookResult{Plugin: path, DryRun: dry, Actions: []Action{}}
	want := pluginJS(tkt)
	if b, err := os.ReadFile(path); err == nil && string(b) == want {
		a.UI.Info("%s is up to date", path)
		return res, nil
	}
	if dry {
		c.Would([]string{"tee", path})
		res.Actions = c.Plan
		return res, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, apperr.New(apperr.Internal, "cannot create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return nil, apperr.New(apperr.Internal, "cannot write %s: %v", path, err)
	}
	res.Installed = true
	a.UI.Info("OpenCode loads the plugin when its service starts: run opencode service restart while no agent works")
	return res, nil
}

// pluginCheck is tkt doctor's view of the OpenCode plugin, for a
// configuration that runs OpenCode.
func (a *App) pluginCheck(cfg config.Config, cfgErr error) Check {
	const id = "opencode_plugin"
	if cfgErr != nil || !slices.ContainsFunc(cfg.Presets, func(p config.Preset) bool { return p.Harness == config.OpenCode }) {
		return Check{ID: id, Status: CheckSkip, Message: "no preset runs OpenCode"}
	}
	path := OpenCodePluginPath(a.Getenv)
	b, err := os.ReadFile(path)
	if err != nil {
		return Check{ID: id, Status: CheckWarn, Message: "OpenCode agents share the service's TMPDIR and /tmp, since tkt's plugin is not installed",
			Hint: "tkt hook tmp --install"}
	}
	if tkt, err := exec.LookPath("tkt"); err == nil {
		tkt, _ = filepath.Abs(tkt)
		if string(b) != pluginJS(tkt) {
			return Check{ID: id, Status: CheckWarn, Message: path + " is from another tkt", Hint: "tkt hook tmp --install"}
		}
	}
	return Check{ID: id, Status: CheckOK, Message: "OpenCode agents get their ticket's TMPDIR from " + path}
}

// pluginJS is tkt's OpenCode plugin. OpenCode runs plugins in its one
// background service, where every agent's tools run. Each call asks tkt
// hook tmp, so the rules live in Go, about the directory it runs in: a
// shell's own, or a tool call's session's. It needs nothing from
// @opencode/plugin, whose define returns the plugin as it is.
func pluginJS(tkt string) string {
	b, _ := json.Marshal(tkt)
	return strings.Replace(pluginSource, "TKT_PATH", string(b), 1)
}

const pluginSource = `// tkt's OpenCode plugin. tkt hook tmp --install wrote it, and writes it
// again each time it runs; change tkt, not this file. In a ticket's
// worktree it gives each shell the ticket's own TMPDIR, moves the new /tmp
// paths an agent types into that directory, and tells the agent so. tkt
// hook tmp decides each call; elsewhere it does nothing.
import { spawnSync } from "node:child_process"

const tkt = TKT_PATH

function ask(req) {
	try {
		const r = spawnSync(tkt, ["hook", "tmp", "--json"], { input: JSON.stringify(req), encoding: "utf8", timeout: 5000 })
		if (r.status !== 0 || !r.stdout) return undefined
		const env = JSON.parse(r.stdout)
		return env.ok ? env.result : undefined
	} catch {
		return undefined
	}
}

export default {
	id: "tkt",
	async setup(ctx) {
		// A session's directory, the worktree it works in. Unknown, it is
		// none: a guess could move another session's paths.
		const sessions = new Map()
		async function dirOf(sessionID) {
			if (!sessionID || !ctx.session?.get) return undefined
			if (!sessions.has(sessionID)) {
				const info = await ctx.session.get({ sessionID }).catch(() => undefined)
				sessions.set(sessionID, info?.location?.directory)
			}
			return sessions.get(sessionID)
		}
		if (ctx.tool?.hook) {
			await ctx.tool.hook("execute.before", async (event) => {
				if (!event.input || typeof event.input !== "object") return
				const dir = await dirOf(event.sessionID)
				if (!dir) return
				const r = ask({ directories: [dir], tool: event.tool, input: event.input })
				// In place, as OpenCode may hold on to the object it passed.
				if (r?.input) Object.assign(event.input, r.input)
			})
		}
		if (ctx.shell?.hook) {
			await ctx.shell.hook("create.before", (event) => {
				const r = ask({ directories: [event.cwd], tmpdir: event.env?.TMPDIR, command: event.command })
				if (r?.tmpdir && event.env) event.env.TMPDIR = r.tmpdir
				if (r?.command) event.command = r.command
			})
		}
		if (ctx.session?.hook) {
			await ctx.session.hook("context", async (event) => {
				const dir = await dirOf(event.sessionID)
				if (!dir) return
				const r = ask({ directories: [dir] })
				if (r?.system && Array.isArray(event.system)) event.system.push({ type: "text", text: r.system })
			})
		}
	},
}
`
