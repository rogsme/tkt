package herdr

import (
	"context"
	"strings"
	"testing"

	"gitlab.com/rogs/tkt/internal/runner"
)

// fake answers herdr commands from a table keyed by the joined arguments.
func fake(answers map[string]runner.Result) Client {
	return Client{R: runner.Func(func(_ context.Context, c runner.Cmd) runner.Result {
		key := strings.Join(c.Argv[1:], " ")
		if r, ok := answers[key]; ok {
			return r
		}
		return runner.Result{Code: 1, Stderr: []byte(`{"error":{"code":"unknown_command","message":"x"},"id":"t"}`)}
	})}
}

func ok(body string) runner.Result { return runner.Result{Stdout: []byte(body)} }

func TestPaneGetShapes(t *testing.T) {
	c := fake(map[string]runner.Result{
		"pane get w1:p1": ok(`{"id":"t","result":{"pane":{"pane_id":"w1:p1","tab_id":"w1:t1","cwd":"/r"}}}`),
		"pane get w1:p2": ok(`{"id":"t","result":{"pane_id":"w1:p2","tab_id":"w1:t2","cwd":"/s"}}`),
	})
	p, err := c.PaneGet(context.Background(), "w1:p1")
	if err != nil || p.TabID != "w1:t1" || p.Cwd != "/r" {
		t.Errorf("nested: %+v, %v", p, err)
	}
	p, err = c.PaneGet(context.Background(), "w1:p2")
	if err != nil || p.TabID != "w1:t2" {
		t.Errorf("flat: %+v, %v", p, err)
	}
	_, err = c.PaneGet(context.Background(), "w1:p9")
	if CodeOf(err) != "unknown_command" {
		t.Errorf("error code: %v", err)
	}
}

func TestAgentsAndShell(t *testing.T) {
	c := fake(map[string]runner.Result{
		"agent list":                     ok(`{"result":{"agents":[{"agent":"claude","agent_status":"idle","pane_id":"w1:p2","cwd":"/w/ACM-1/sub","agent_session":{"value":"s1"}},{"name":"x","agent":"opencode","agent_status":"working","pane_id":"w1:p3","cwd":"/elsewhere"}]}}`),
		"pane process-info --pane w1:p2": ok(`{"result":{"process_info":{"foreground_process_group_id":5,"shell_pid":5}}}`),
		"pane process-info --pane w1:p3": ok(`{"result":{"process_info":{"foreground_process_group_id":6,"shell_pid":5}}}`),
	})
	agents, err := c.Agents(context.Background())
	if err != nil || len(agents) != 2 {
		t.Fatalf("agents: %v, %v", agents, err)
	}
	if in := AgentsInPath(agents, "/w/ACM-1"); len(in) != 1 || in[0].SessionID() != "s1" || in[0].NameOr("-") != "-" {
		t.Errorf("in path: %+v", in)
	}
	if a, ok := c.AgentOnPane(context.Background(), "w1:p3"); !ok || a.NameOr("") != "x" {
		t.Errorf("on pane: %+v", a)
	}
	if !c.PaneIsShell(context.Background(), "w1:p2") || c.PaneIsShell(context.Background(), "w1:p3") {
		t.Error("PaneIsShell")
	}
}

func TestTicketTabs(t *testing.T) {
	tabs := []Tab{{TabID: "1", Label: "ACM-12"}, {TabID: "2", Label: "ACM-120 (fast)"}, {TabID: "3", Label: "ACM-12 (fast)"}}
	got := TicketTabs(tabs, "ACM-12")
	if len(got) != 2 || got[0].TabID != "1" || got[1].TabID != "3" {
		t.Errorf("got %+v", got)
	}
	if Inside("/w/ACM-10", "/w/ACM-1") || !Inside("/w/ACM-1", "/w/ACM-1") {
		t.Error("Inside")
	}
}

func TestNotify(t *testing.T) {
	var got []string
	c := Client{R: runner.Func(func(_ context.Context, cmd runner.Cmd) runner.Result {
		got = cmd.Argv
		if strings.Contains(strings.Join(cmd.Argv, " "), "fail") {
			return runner.Result{Code: 1, Stderr: []byte(`{"error":{"code":"no_ui","message":"x"}}`)}
		}
		return runner.Result{}
	})}
	if err := c.Notify(context.Background(), "ACM-12: needs you", "a question", "request"); err != nil {
		t.Fatal(err)
	}
	if want := "herdr notification show ACM-12: needs you --body a question --sound request"; strings.Join(got, " ") != want {
		t.Errorf("argv: %q", got)
	}
	if err := c.Notify(context.Background(), "fail", "", ""); CodeOf(err) != "no_ui" {
		t.Errorf("error: %v", err)
	}
}
