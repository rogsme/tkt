package cli

import "testing"

func TestShouldLaunch(t *testing.T) {
	ready := func() launch {
		return launch{stdinTTY: true, stdoutTTY: true, herdrEnv: "1", configured: func() bool { return true }}
	}
	if !shouldLaunch(ready()) {
		t.Error("bare tkt at a terminal in a configured repository in herdr prints help")
	}
	for name, change := range map[string]func(*launch){
		"an argument":                func(l *launch) { l.args = []string{"bogus"} },
		"--json":                     func(l *launch) { l.json = true },
		"stdin is not a terminal":    func(l *launch) { l.stdinTTY = false },
		"stdout is not a terminal":   func(l *launch) { l.stdoutTTY = false },
		"outside herdr":              func(l *launch) { l.herdrEnv = "" },
		"no lifecycle configuration": func(l *launch) { l.configured = func() bool { return false } },
	} {
		l := ready()
		change(&l)
		if shouldLaunch(l) {
			t.Errorf("%s: bare tkt opens the agent", name)
		}
	}

	// The repository check runs git, so it comes last.
	l := ready()
	l.stdoutTTY = false
	l.configured = func() bool { t.Error("checked the repository without a terminal"); return true }
	shouldLaunch(l)
}
