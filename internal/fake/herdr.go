package fake

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// obj is one herdr object (workspace, tab, pane or agent) in the fake's
// state file, kept generic so tests can edit the file freely.
type obj = map[string]any

type herdrState struct {
	Next       int   `json:"next"`
	Workspaces []obj `json:"workspaces"`
	Tabs       []obj `json:"tabs"`
	Panes      []obj `json:"panes"`
	Agents     []obj `json:"agents"`
}

func statePath() string { return filepath.Join(os.Getenv("FAKE_DIR"), "herdr.json") }

func loadState() *herdrState {
	b, err := os.ReadFile(statePath())
	if err != nil {
		die(2, "fake herdr: %v", err)
	}
	var s herdrState
	if err := json.Unmarshal(b, &s); err != nil {
		die(2, "fake herdr: %v", err)
	}
	return &s
}

func (s *herdrState) save() {
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(statePath(), b, 0o644)
}

func str(o obj, k string) string {
	s, _ := o[k].(string)
	return s
}

func (s *herdrState) agentOf(target string) obj {
	for _, a := range s.Agents {
		if str(a, "name") == target || str(a, "pane_id") == target {
			return a
		}
	}
	return nil
}

func (s *herdrState) pane(id string) obj {
	for _, p := range s.Panes {
		if str(p, "pane_id") == id {
			return p
		}
	}
	return nil
}

func filter(list []obj, keep func(obj) bool) []obj {
	out := []obj{}
	for _, o := range list {
		if keep(o) {
			out = append(out, o)
		}
	}
	return out
}

func herdrFail(code string) {
	b, _ := json.Marshal(obj{"error": obj{"code": code, "message": code}, "id": "fake"})
	fmt.Fprintln(os.Stderr, string(b))
	os.Exit(1)
}

func herdrOK(result any) {
	printJSON(obj{"id": "fake", "result": result})
	os.Exit(0)
}

// injected takes the next error listed in the variable, for example
// FAKE_HERDR_START_ERRORS=agent_pane_busy,-,agent_not_ready.
func injected(variable string) (string, bool) {
	list := os.Getenv(variable)
	if list == "" {
		return "", false
	}
	n := counter(filepath.Join(os.Getenv("FAKE_DIR"), variable+".count"))
	errs := strings.Split(list, ",")
	if n >= len(errs) || errs[n] == "-" {
		return "", false
	}
	return errs[n], true
}

func flagValues(args []string) (map[string]string, []string) {
	flags := map[string]string{}
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if strings.HasPrefix(args[i], "--") && i+1 < len(args) {
			flags[args[i]] = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return flags, rest
}

// repeated returns every value of a flag that may repeat, such as --env.
func repeated(args []string, flag string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			out = append(out, args[i+1])
			i++
		}
	}
	return out
}

var slugUnsafe = regexp.MustCompile(`[^A-Za-z0-9]`)

// Herdr keeps workspaces, tabs, panes and agents in $FAKE_DIR/herdr.json and
// answers the herdr commands tkt uses.
func Herdr() {
	logCall("herdr")
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("herdr " + envOr("FAKE_HERDR_VERSION", "0.9.3"))
		os.Exit(0)
	}
	if len(args) < 2 {
		die(2, "fake herdr: unsupported: %s", strings.Join(args, " "))
	}
	s := loadState()
	group, cmd, args := args[0], args[1], args[2:]
	switch group + " " + cmd {
	case "workspace list":
		herdrOK(obj{"type": "workspace_list", "workspaces": s.Workspaces})
	case "workspace create":
		f, _ := flagValues(args)
		n := fmt.Sprint(s.Next)
		s.Next++
		ws, tab, pane := "w"+n, "w"+n+":t"+n, "w"+n+":p"+n
		s.Workspaces = append(s.Workspaces, obj{"workspace_id": ws, "label": f["--label"]})
		s.Tabs = append(s.Tabs, obj{"tab_id": tab, "workspace_id": ws, "label": "1"})
		p := obj{"pane_id": pane, "tab_id": tab, "workspace_id": ws, "cwd": f["--cwd"], "foreground_cwd": f["--cwd"]}
		s.Panes = append(s.Panes, p)
		s.save()
		herdrOK(obj{"type": "workspace_created", "workspace": obj{"workspace_id": ws, "label": f["--label"]}, "tab": obj{"tab_id": tab, "workspace_id": ws}, "root_pane": p})
	case "tab list":
		f, _ := flagValues(args)
		herdrOK(obj{"tabs": filter(s.Tabs, func(t obj) bool { return str(t, "workspace_id") == f["--workspace"] })})
	case "tab create":
		f, _ := flagValues(args)
		ws, n := f["--workspace"], fmt.Sprint(s.Next)
		s.Next++
		tab, pane := ws+":t"+n, ws+":p"+n
		s.Tabs = append(s.Tabs, obj{"tab_id": tab, "workspace_id": ws, "label": f["--label"]})
		p := obj{"pane_id": pane, "tab_id": tab, "workspace_id": ws, "cwd": f["--cwd"], "foreground_cwd": f["--cwd"]}
		// The real herdr gives the tab's shell these variables; the fake
		// keeps them on the pane for the tests to read.
		if env := repeated(args, "--env"); len(env) > 0 {
			p["env"] = env
		}
		s.Panes = append(s.Panes, p)
		s.save()
		herdrOK(obj{"tab": obj{"tab_id": tab}, "root_pane": obj{"pane_id": pane}})
	case "tab close":
		t := args[0]
		other := func(o obj) bool { return str(o, "tab_id") != t }
		s.Agents, s.Panes, s.Tabs = filter(s.Agents, other), filter(s.Panes, other), filter(s.Tabs, other)
		s.save()
		herdrOK(obj{"type": "ok"})
	case "tab focus":
		herdrOK(obj{"type": "ok"})
	case "tab rename":
		for _, t := range s.Tabs {
			if str(t, "tab_id") == args[0] {
				t["label"] = strings.Join(args[1:], " ")
			}
		}
		s.save()
		herdrOK(obj{"type": "ok"})
	case "pane list":
		var panes []obj
		for _, p := range s.Panes {
			q := obj{}
			for k, v := range p {
				q[k] = v
			}
			if a := s.agentOf(str(p, "pane_id")); a != nil && str(a, "pane_id") == str(p, "pane_id") {
				q["agent"], q["agent_status"] = a["agent"], a["agent_status"]
			} else {
				q["agent_status"] = "unknown"
			}
			panes = append(panes, q)
		}
		herdrOK(obj{"panes": panes})
	case "pane get":
		p := s.pane(args[0])
		if p == nil {
			herdrFail("pane_not_found")
		}
		herdrOK(obj{"pane": p})
	case "pane process-info":
		_, rest := flagValues(args)
		id := args[len(args)-1]
		if len(rest) > 0 {
			id = rest[0]
		}
		if s.pane(id) == nil {
			herdrFail("pane_not_found")
		}
		fg := 1
		if s.agentOf(id) != nil {
			fg = 2
		}
		herdrOK(obj{"process_info": obj{"foreground_process_group_id": fg, "shell_pid": 1}})
	case "pane run":
		p := s.pane(args[0])
		if p == nil {
			herdrFail("pane_not_found")
		}
		kind := strings.ReplaceAll(strings.Fields(args[1])[0], "'", "")
		s.Agents = append(s.Agents, obj{"name": nil, "agent": kind, "agent_status": "idle", "pane_id": args[0],
			"tab_id": p["tab_id"], "cwd": p["cwd"], "foreground_cwd": p["cwd"]})
		s.save()
		herdrOK(obj{"type": "ok"})
	case "agent list":
		herdrOK(obj{"agents": s.Agents})
	case "agent get":
		a := s.agentOf(args[0])
		if a == nil {
			herdrFail("agent_not_found")
		}
		herdrOK(obj{"agent": a})
	case "agent start":
		herdrStart(s, args)
	case "agent prompt":
		herdrPrompt(s, args)
	case "agent wait":
		if e, ok := injected("FAKE_HERDR_WAIT_ERRORS"); ok {
			herdrFail(e)
		}
		a := s.agentOf(args[0])
		if a == nil {
			herdrFail("agent_not_found")
		}
		if os.Getenv("FAKE_HERDR_TURNS") != "" && str(a, "agent_status") == "working" {
			nextTurn(a)
			s.save()
		}
		herdrOK(obj{"agent": a})
	case "agent read":
		if b, err := os.ReadFile(filepath.Join(os.Getenv("FAKE_DIR"), "screen")); err == nil {
			os.Stdout.Write(b)
			os.Exit(0)
		}
		fmt.Println(os.Getenv("FAKE_HERDR_SCREEN"))
		os.Exit(0)
	case "notification show":
		herdrOK(obj{"type": "ok"})
	case "agent send-keys":
		if slices.Contains(args[1:], "ctrl+c") && os.Getenv("FAKE_HERDR_CTRLC_IGNORED") != "1" {
			s.removeAgent(args[0])
			s.save()
		}
		herdrOK(obj{"type": "ok"})
	case "agent rename":
		if a := s.agentOf(args[0]); a != nil {
			a["name"] = args[1]
			s.save()
		}
		herdrOK(obj{"type": "ok"})
	default:
		die(2, "fake herdr: unsupported: %s %s", group, cmd)
	}
}

func (s *herdrState) removeAgent(target string) {
	s.Agents = filter(s.Agents, func(a obj) bool { return str(a, "name") != target && str(a, "pane_id") != target })
}

func herdrStart(s *herdrState, args []string) {
	name := args[0]
	f, rest := flagValues(args[1:])
	if e, ok := injected("FAKE_HERDR_START_ERRORS"); ok {
		herdrFail(e)
	}
	if s.agentOf(name) != nil {
		herdrFail("agent_name_taken")
	}
	pane := f["--pane"]
	if s.agentOf(pane) != nil {
		herdrFail("agent_pane_busy")
	}
	sid := ""
	for i, a := range rest {
		if (a == "--session-id" || a == "--resume") && i+1 < len(rest) {
			sid = rest[i+1]
		}
	}
	status := envOr("FAKE_HERDR_START_STATUS", "idle")
	p := s.pane(pane)
	if p == nil {
		herdrFail("pane_not_found")
	}
	a := obj{"name": name, "agent": f["--kind"], "agent_status": status, "pane_id": pane, "tab_id": p["tab_id"], "cwd": p["cwd"], "foreground_cwd": p["cwd"]}
	if sid != "" {
		a["agent_session"] = obj{"value": sid}
	}
	s.Agents = append(s.Agents, a)
	s.save()
	if status == "blocked" {
		herdrFail("agent_not_ready")
	}
	herdrOK(obj{"type": "agent_started", "argv": append([]string{f["--kind"]}, rest...)})
}

func herdrPrompt(s *herdrState, args []string) {
	target, text := args[0], args[1]
	a := s.agentOf(target)
	if a == nil {
		herdrFail("agent_not_found")
	}
	if text == "/exit" {
		if os.Getenv("FAKE_HERDR_EXIT_IGNORED") != "1" {
			s.removeAgent(target)
			s.save()
		}
		herdrOK(obj{"agent": a})
	}
	f, _ := os.OpenFile(filepath.Join(os.Getenv("FAKE_DIR"), "prompts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "%s\n--- end of prompt ---\n", text)
	f.Close()
	if os.Getenv("FAKE_HERDR_RECORD_TURN") == "1" {
		dir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", slugUnsafe.ReplaceAllString(str(a, "cwd"), "-"))
		os.MkdirAll(dir, 0o755)
		sid := ""
		if sess, ok := a["agent_session"].(obj); ok {
			sid = str(sess, "value")
		}
		t, _ := os.OpenFile(filepath.Join(dir, sid+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		fmt.Fprintln(t, `{"type": "user", "message": {"content": "prompt"}}`)
		t.Close()
	}
	if e, ok := injected("FAKE_HERDR_PROMPT_ERRORS"); ok {
		herdrFail(e)
	}
	working := obj{}
	for k, v := range a {
		working[k] = v
	}
	working["agent_status"] = "working"
	if os.Getenv("FAKE_HERDR_TURNS") != "" {
		a["agent_status"] = "working"
		s.save()
	}
	herdrOK(obj{"agent": working})
}

// turn is what a working agent ends its turn with, in turns mode: its status
// (idle by default), its screen, and the pull requests gh then lists.
type turn struct {
	Status string `json:"status"`
	Screen string `json:"screen"`
	PRs    string `json:"prs"`
}

// nextTurn ends the agent's turn with the next entry of the JSON list in the
// file $FAKE_HERDR_TURNS names; when the list runs out, the agent goes idle.
// In turns mode a prompt sets the agent working, so tkt auto can be driven
// through a whole ticket.
func nextTurn(a obj) {
	dir := os.Getenv("FAKE_DIR")
	var turns []turn
	if b, err := os.ReadFile(os.Getenv("FAKE_HERDR_TURNS")); err == nil {
		json.Unmarshal(b, &turns)
	}
	n := counter(filepath.Join(dir, "turns.count"))
	t := turn{Status: "idle"}
	if n < len(turns) {
		t = turns[n]
	}
	a["agent_status"] = t.Status
	if t.Status == "" {
		a["agent_status"] = "idle"
	}
	if t.Screen != "" {
		os.WriteFile(filepath.Join(dir, "screen"), []byte(t.Screen+"\n"), 0o644)
	}
	if t.PRs != "" {
		if b, err := os.ReadFile(t.PRs); err == nil {
			os.WriteFile(filepath.Join(dir, "prs.json"), b, 0o644)
		}
	}
}
