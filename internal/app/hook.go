package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/harness"
	"gitlab.com/rogs/tkt/internal/history"
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
		if c := moveCommand(req.Command, res.TmpDir, "/tmp"); c != req.Command {
			res.Command = c
		}
	}
	if req.Input != nil {
		res.Input = moveInput(req.Tool, req.Input, res.TmpDir, "/tmp")
	}
	res.System = tmpLine(res.TmpDir)
	return res, nil
}

// ticketAt finds the ticket whose worktree holds dir, and its temporary
// directory, from the worktree's state file.
func ticketAt(dir string) (id, tmp string) {
	_, st := stateAt(dir)
	return st["ticket"], st["tmpdir"]
}

// stateAt finds the state file of the worktree that holds dir, and reads
// it. A linked worktree's .git is a file naming its git directory; the
// primary worktree's is a directory, and holds no ticket.
func stateAt(dir string) (string, state.State) {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		dot := filepath.Join(d, ".git")
		if fi, err := os.Lstat(dot); err == nil {
			if fi.IsDir() {
				return "", state.State{}
			}
			b, err := os.ReadFile(dot)
			gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
			if err != nil || !ok {
				return "", state.State{}
			}
			if !filepath.IsAbs(gitdir) {
				gitdir = filepath.Join(d, gitdir)
			}
			p := state.Path(gitdir)
			return p, state.Read(p)
		}
		if d == filepath.Dir(d) {
			return "", state.State{}
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
// move instead of fighting it, and knows how to keep a path as written.
func tmpLine(dir string) string {
	return "This ticket has its own temporary directory, " + dir + ", which is $TMPDIR in your shells. tkt moves a new path under /tmp into it when a file tool names it or a shell command uses it unquoted: /tmp/notes.md becomes " + dir + "/notes.md, and later calls find it there. That move is expected. A /tmp path in quotes stays as written, as in a search pattern or a message, and so does a path /tmp holds already."
}

// The tools whose /tmp paths move: the shell, and the file tools that take
// a path. Any other tool, such as an MCP server's, may run its command or
// name its path on another machine.
var (
	shellTools = []string{"shell", "bash"}
	fileTools  = []string{"read", "write", "edit", "glob", "grep", "list", "patch"}
)

// pathFields are the fields of a tool's input that hold a path. Content
// and text fields stay as written: they are a file's content, or words.
var pathFields = []string{"path", "filePath", "file_path", "workdir", "cwd", "directory"}

// moveInput returns a copy of a tool's input with its /tmp paths moved, or
// nil when nothing moved.
func moveInput(tool string, in map[string]any, dir, root string) map[string]any {
	tool = strings.ToLower(tool)
	shell := slices.Contains(shellTools, tool)
	if !shell && !slices.Contains(fileTools, tool) {
		return nil
	}
	var out map[string]any
	for k, v := range in {
		s, ok := v.(string)
		if !ok {
			continue
		}
		moved := s
		switch {
		case slices.Contains(pathFields, k):
			moved = movePath(s, dir, root)
		case shell && k == "command":
			moved = moveCommand(s, dir, root)
		}
		if moved != s {
			if out == nil {
				out = maps.Clone(in)
			}
			out[k] = moved
		}
	}
	return out
}

// movePath moves one path, a file tool's: /tmp/NAME... goes into dir when
// NAME moves.
func movePath(p, dir, root string) string {
	rest, ok := strings.CutPrefix(p, "/tmp/")
	if !ok {
		return p
	}
	if name, _, _ := strings.Cut(rest, "/"); name == "" || !moves(name, dir, root) {
		return p
	}
	return dir + "/" + rest
}

// moveCommand moves the /tmp paths a shell command uses into dir. A path
// moves only where the shell takes it as a path to use: at the start of an
// unquoted word, or after an unquoted =, as in --out=/tmp/x. Everywhere
// else it is protected: in quotes, where it is as often a search pattern or
// a message as a place; in a here-document or a here-string, which are data
// or another program's script; in arithmetic; after a colon, as on another
// host; and in an option, as in -o/tmp/x. A name moves only when every path
// to it in the command can, so the command never reads from one place what
// it wrote to the other. A comment's paths count for nothing.
func moveCommand(s, dir, root string) string {
	refs := tmpRefs(s, dir)
	keep := map[string]bool{}
	for _, r := range refs {
		if !r.movable {
			keep[r.name] = true
		}
	}
	decided := map[string]bool{}
	var b strings.Builder
	last := 0
	for _, r := range refs {
		if !r.movable || keep[r.name] {
			continue
		}
		move, ok := decided[r.name]
		if !ok {
			move = moves(r.name, dir, root)
			decided[r.name] = move
		}
		if move {
			b.WriteString(s[last:r.at])
			b.WriteString(dir)
			last = r.at + len("/tmp")
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

// tmpRef is one /tmp/NAME path in a shell command: where it starts, its
// first name, and whether the shell takes it as a path that can move.
type tmpRef struct {
	at      int
	name    string
	movable bool
}

// nameEnd ends the first name under /tmp.
const nameEnd = "/ \t\n'\"`;:)|&<>}],*?[$\\"

// tmpRefs finds the /tmp/NAME paths in a shell command, with a small lexer
// for what changes their meaning: quotes, escapes, comments,
// here-documents, here-strings and arithmetic. A path in dir is left out:
// it is where it belongs.
func tmpRefs(s, dir string) []tmpRef {
	var refs []tmpRef
	// at reports whether a /tmp path starts at i: not inside a longer path
	// such as /var/tmp/x, but after an option's letters, as in -o/tmp/x.
	at := func(i int) bool {
		if !strings.HasPrefix(s[i:], "/tmp/") {
			return false
		}
		j := i
		for j > 0 && isAlnum(s[j-1]) {
			j--
		}
		if j < i {
			return j > 0 && s[j-1] == '-'
		}
		return i == 0 || strings.IndexByte("._-~+%@/", s[i-1]) < 0
	}
	add := func(i int, movable bool) {
		j := i + len("/tmp/")
		k := j
		for k < len(s) && strings.IndexByte(nameEnd, s[k]) < 0 {
			k++
		}
		if k > j {
			refs = append(refs, tmpRef{at: i, name: s[j:k], movable: movable})
		}
	}
	inDir := func(i int) bool {
		return dir != "" && strings.HasPrefix(s[i:], dir) && (i+len(dir) == len(s) || strings.IndexByte(nameEnd, s[i+len(dir)]) >= 0)
	}
	// protect records the paths in s[i:j] as ones that cannot move.
	protect := func(i, j int) {
		for k := i; k < j; k++ {
			if inDir(k) {
				k += len(dir) - 1
			} else if at(k) {
				add(k, false)
			}
		}
	}
	word := true
	var docs []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case inDir(i):
			i += len(dir)
			word = false
		case c == '\n' && len(docs) > 0:
			i++
			for _, d := range docs {
				body, next := heredocBody(s, i, d)
				protect(i, body)
				i = next
			}
			docs, word = nil, true
		case c == '\'':
			j := closing(s, i+1, '\'', false)
			protect(i, j)
			i, word = j, false
		case c == '$' && strings.HasPrefix(s[i:], "$'"):
			j := closing(s, i+2, '\'', true)
			protect(i, j)
			i, word = j, false
		case c == '"':
			j := closing(s, i+1, '"', true)
			protect(i, j)
			i, word = j, false
		case c == '\\':
			i, word = min(i+2, len(s)), false
		case c == '#' && word:
			if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
				i += n
			} else {
				i = len(s)
			}
		case strings.HasPrefix(s[i:], "$((") || word && strings.HasPrefix(s[i:], "(("):
			j := arithEnd(s, i+strings.Index(s[i:], "((")+2)
			protect(i, j)
			i, word = j, false
		case strings.HasPrefix(s[i:], "<<<"):
			j := i + 3
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			k := wordEnd(s, j)
			protect(i, k)
			i, word = k, false
		case strings.HasPrefix(s[i:], "<<"):
			if d, n := heredocWord(s[i+2:]); d != "" {
				docs = append(docs, d)
				i, word = i+2+n, false
			} else {
				i, word = i+2, true
			}
		case at(i):
			add(i, word || i > 0 && s[i-1] == '=')
			i, word = i+1, false
		case strings.IndexByte(" \t\n;&|()<>`", c) >= 0:
			i, word = i+1, true
		default:
			i, word = i+1, false
		}
	}
	return refs
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// closing finds the end of a quoted span that starts after its opening
// quote at i: the index after the closing quote, or the end of s. With
// escapes, a backslash takes the next byte as it is.
func closing(s string, i int, quote byte, escapes bool) int {
	for i < len(s) {
		switch {
		case escapes && s[i] == '\\':
			i += 2
		case s[i] == quote:
			return i + 1
		default:
			i++
		}
	}
	return len(s)
}

// wordEnd finds where the shell word that starts at i ends.
func wordEnd(s string, i int) int {
	for i < len(s) && strings.IndexByte(" \t\n;&|()<>", s[i]) < 0 {
		switch s[i] {
		case '\'':
			i = closing(s, i+1, '\'', false)
		case '"':
			i = closing(s, i+1, '"', true)
		case '\\':
			i += 2
		default:
			i++
		}
	}
	return min(i, len(s))
}

// arithEnd finds the end of arithmetic that opened with (( before i.
func arithEnd(s string, i int) int {
	depth := 2
	for ; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}

// heredocBody finds the body of a here-document that starts at i and ends
// with a line holding only word, after any tabs. It returns where the body
// ends and where the command goes on after the word's line.
func heredocBody(s string, i int, word string) (body, next int) {
	for i < len(s) {
		end := len(s)
		if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
			end = i + n
		}
		if strings.TrimLeft(s[i:end], "\t") == word {
			return i, min(end+1, len(s))
		}
		i = min(end+1, len(s))
	}
	return len(s), len(s)
}

// heredocWord reads the word that ends a here-document, after its <<: an
// optional -, then a word that starts with a letter, an underscore or a
// quote, so 1 << 2 and similar shifts are no here-document. It returns the
// word and how many bytes it took, or "" when there is none.
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
	if i == len(s) || !(s[i] == '_' || s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z') {
		return "", 0
	}
	j := i
	for j < len(s) && (s[j] == '_' || s[j] == '-' || isAlnum(s[j])) {
		j++
	}
	return s[i:j], j
}

// moves decides whether /tmp/name belongs in dir: when dir holds it
// already, or neither dir nor root (the real /tmp) does, so a new file
// lands in dir and later calls find it there. A name root holds stays,
// such as a file the user put there, and so do the names other programs
// own: hidden ones, the tickets' own directories (tkt-), Claude Code's
// and OpenCode's.
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
// configuration that runs OpenCode. OpenCode loads it from the project's
// .opencode/plugins as well as from the global directory.
func (a *App) pluginCheck(cfg config.Config, cfgErr error, root string) Check {
	const id = "opencode_plugin"
	if cfgErr != nil || !slices.ContainsFunc(cfg.Presets, func(p config.Preset) bool { return p.Harness == config.OpenCode }) {
		return Check{ID: id, Status: CheckSkip, Message: "no preset runs OpenCode"}
	}
	paths := []string{OpenCodePluginPath(a.Getenv)}
	if root != "" {
		paths = append(paths, filepath.Join(root, ".opencode", "plugins", "tkt.js"))
	}
	var stale string
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if tkt, err := exec.LookPath("tkt"); err == nil {
			tkt, _ = filepath.Abs(tkt)
			if string(b) != pluginJS(tkt) {
				stale = path
				continue
			}
		}
		return Check{ID: id, Status: CheckOK, Message: "OpenCode agents get their ticket's TMPDIR and report their status lines through " + path}
	}
	if stale != "" {
		return Check{ID: id, Status: CheckWarn, Message: stale + " is from another tkt", Hint: "tkt hook tmp --install"}
	}
	return Check{ID: id, Status: CheckWarn, Message: "OpenCode agents share the service's TMPDIR and /tmp, and tkt reads their status lines only from the last 50 messages, since tkt's plugin is not installed",
		Hint: "tkt hook tmp --install"}
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
// hook tmp decides each call; elsewhere it does nothing. It also tells tkt
// hook event about each new prompt, each status line and each skill started
// in a session, in order, so tkt need not read a long session back.
import { spawn, spawnSync } from "node:child_process"

const tkt = TKT_PATH

// tell hands tkt hook event one event, after the ones before it, without
// holding up the service while tkt runs.
let told = Promise.resolve()
function tell(ev) {
	told = told.then(() => new Promise((resolve) => {
		try {
			const p = spawn(tkt, ["hook", "event", "--json"], { stdio: ["pipe", "ignore", "ignore"], timeout: 5000 })
			p.on("error", resolve)
			p.on("close", resolve)
			p.stdin.end(JSON.stringify(ev))
		} catch {
			resolve()
		}
	}))
}

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
				// A skill's start, such as work-ticket's after the plan viewer's
				// approval, tells tkt the stage moved on.
				if (event.tool === "skill" && typeof event.input.name === "string") {
					tell({ directories: [dir], session: event.sessionID, skill: event.input.name })
				}
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
		if (!ctx.event?.subscribe) return
		const controller = new AbortController()
		void (async () => {
			for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
				const data = event.data ?? {}
				let ev
				if (event.type === "session.inbox.delivered") ev = { turn: "new" }
				else if (event.type === "session.text.ended") {
					const statuses = [...String(data.text ?? "").matchAll(/Status: ([A-Z_]+)/g)].map((m) => m[1])
					if (statuses.length > 0) ev = { statuses }
				}
				if (!ev || !data.sessionID) continue
				const dir = await dirOf(data.sessionID)
				if (dir) tell({ directories: [dir], session: data.sessionID, ...ev })
			}
		})().catch(() => {})
		return () => controller.abort()
	},
}
`

// HookEventResult is tkt hook event's answer: the ticket whose session the
// event came from, or "" for any other session, and the status lines its
// current turn holds so far.
type HookEventResult struct {
	Ticket   string   `json:"ticket"`
	Session  string   `json:"session"`
	Statuses []string `json:"statuses"`
}

func (r *HookEventResult) Text(w io.Writer) {
	if r.Ticket != "" {
		fmt.Fprintf(w, "%s  %s\n", r.Ticket, strings.Join(r.Statuses, " "))
	}
}

// hookEvent is what a hook tells: the session's directories, the session,
// and either that a new prompt reached it (turn "new"), the status lines of
// a text it finished, or a skill it started.
type hookEvent struct {
	Directories []string `json:"directories"`
	Session     string   `json:"session"`
	Turn        string   `json:"turn"`
	Statuses    []string `json:"statuses"`
	Skill       string   `json:"skill"`
}

// claudeHook is the part of a Claude Code hook's input tkt reads. AgentID
// is set inside a subagent, whose session id is its parent's.
type claudeHook struct {
	Event   string `json:"hook_event_name"`
	Session string `json:"session_id"`
	Cwd     string `json:"cwd"`
	AgentID string `json:"agent_id"`
	Tool    string `json:"tool_name"`
	Input   struct {
		Skill string `json:"skill"`
	} `json:"tool_input"`
	Last string `json:"last_assistant_message"`
}

// event turns a Claude Code hook's input into what tkt records: a prompt
// starts a turn, a stop gives the status lines of the turn's last text,
// and a Skill call the skill it starts. ok is false for anything else.
func (h claudeHook) event() (hookEvent, bool) {
	ev := hookEvent{Directories: []string{h.Cwd}, Session: h.Session}
	if h.AgentID != "" {
		return ev, false
	}
	switch {
	case h.Event == "UserPromptSubmit":
		ev.Turn = "new"
	case h.Event == "Stop":
		// The turn's last text, where a run's status line goes. One in the
		// middle of the turn, such as start-ticket's PLAN_APPROVED, is in
		// the transcript, and work-ticket's start stands in for it.
		for _, m := range contract.StatusLineRE.FindAllStringSubmatch(h.Last, -1) {
			ev.Statuses = append(ev.Statuses, m[1])
		}
	case h.Event == "PreToolUse" && h.Tool == "Skill" && h.Input.Skill != "":
		ev.Skill = h.Input.Skill
	default:
		return ev, false
	}
	return ev, true
}

// HookEventOpts are tkt hook event's arguments. Claude reads a Claude Code
// hook's input in place of the OpenCode plugin's event.
type HookEventOpts struct {
	Claude bool
}

// The ticket state's keys for what tkt's hooks saw in a session: which
// session, the status lines and skills of its current turn in order, and
// when they last told. The fix's session keeps its own under fixHook.
// Worktrees started before the Claude Code hooks keep the OpenCode
// plugin's older keys, which tkt still reads.
const (
	keyHookSession  = "hook_session"
	keyHookStatuses = "hook_statuses"
	keyHookSkills   = "hook_skills"
	keyHookAt       = "hook_at"
	fixHook         = "fix_"

	keyOCSession  = "oc_session"
	keyOCStatuses = "oc_statuses"
	keyOCAt       = "oc_at"
)

// HookEvent records what tkt's hooks saw in a ticket's session, read as
// JSON from stdin: a new prompt starts a turn, each text the agent finishes
// adds its status lines, and each skill it starts is noted. tkt reads them
// back over the session's own record, which a long turn outgrows and a
// compaction cuts, so start-ticket's Status: PLAN_APPROVED, or work-ticket
// starting after the plan viewer's approval, still counts. It reads and
// writes only the state file, so it stays fast.
func (a *App) HookEvent(ctx context.Context, o HookEventOpts) (*HookEventResult, error) {
	var ev hookEvent
	in := io.LimitReader(a.Stdin, 1<<20)
	if o.Claude {
		var h claudeHook
		if err := json.NewDecoder(in).Decode(&h); err != nil {
			return nil, apperr.New(apperr.Usage, "tkt hook event --claude reads a Claude Code hook's JSON on stdin: %v", err)
		}
		var ok bool
		if ev, ok = h.event(); !ok {
			return &HookEventResult{Session: h.Session, Statuses: []string{}}, nil
		}
	} else if err := json.NewDecoder(in).Decode(&ev); err != nil {
		return nil, apperr.New(apperr.Usage, "tkt hook event reads a JSON event on stdin: %v", err).
			WithHint(`echo '{"directories": ["DIR"], "session": "ses_...", "statuses": ["READY_FOR_REVIEW"]}' | tkt hook event`)
	}
	res := &HookEventResult{Session: ev.Session, Statuses: []string{}}
	for _, d := range ev.Directories {
		if d == "" || ev.Session == "" {
			continue
		}
		path, st := stateAt(d)
		if path == "" || st["ticket"] == "" {
			continue
		}
		prefix := ""
		switch ev.Session {
		case st["session"]:
		case st[keyFixSession]:
			prefix = fixHook
		default:
			// Another session in the worktree, such as a subagent's.
			return res, nil
		}
		var statuses, skills []string
		if ev.Turn != "new" && st[prefix+keyHookSession] == ev.Session {
			statuses = strings.Fields(st[prefix+keyHookStatuses])
			skills = strings.Fields(st[prefix+keyHookSkills])
		}
		for _, s := range ev.Statuses {
			if contract.StatusLineRE.MatchString("Status: " + s) {
				statuses = append(statuses, s)
			}
		}
		if ev.Skill != "" && !strings.ContainsAny(ev.Skill, " \t\n") {
			skills = append(skills, ev.Skill)
		}
		now := a.Now()
		if _, err := state.Update(path, now, prefix+keyHookSession, ev.Session, prefix+keyHookStatuses, strings.Join(statuses, " "),
			prefix+keyHookSkills, strings.Join(skills, " "), prefix+keyHookAt, now.UTC().Format(history.TimeFormat)); err != nil {
			return nil, apperr.New(apperr.Internal, "cannot write %s: %v", path, err)
		}
		res.Ticket, res.Statuses = st["ticket"], append(res.Statuses, statuses...)
		return res, nil
	}
	return res, nil
}

// withHookEvents adds the status lines tkt's hooks saw in the session's
// current turn after the turn's text, so the last of them counts, and the
// skills it started: the hooks saw every text, and the session's own record
// may hold only the turn's end.
func withHookEvents(turn harness.Turn, st state.State, sid string) harness.Turn {
	session, statuses, at := st[keyHookSession], st[keyHookStatuses], st[keyHookAt]
	if session == "" {
		session, statuses, at = st[keyOCSession], st[keyOCStatuses], st[keyOCAt]
	}
	if sid == "" || session != sid || at == "" {
		return turn
	}
	for _, s := range strings.Fields(statuses) {
		turn.All += "\nStatus: " + s
	}
	for _, s := range strings.Fields(st[keyHookSkills]) {
		if !slices.Contains(turn.Skills, s) {
			turn.Skills = append(turn.Skills, s)
		}
	}
	return turn
}
