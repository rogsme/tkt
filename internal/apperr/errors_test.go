package apperr

import (
	"errors"
	"regexp"
	"testing"
)

func TestRegistryIsComplete(t *testing.T) {
	snake := regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	seen := map[Code]bool{}
	for _, i := range Registry {
		if seen[i.Code] {
			t.Errorf("%s is registered twice", i.Code)
		}
		seen[i.Code] = true
		if !snake.MatchString(string(i.Code)) {
			t.Errorf("%s is not snake_case", i.Code)
		}
		if i.Exit < 1 || i.Exit > 5 {
			t.Errorf("%s has exit %d", i.Code, i.Exit)
		}
		if i.Summary == "" {
			t.Errorf("%s has no summary", i.Code)
		}
	}
}

func TestExit(t *testing.T) {
	if got := Exit(nil); got != 0 {
		t.Errorf("Exit(nil) = %d", got)
	}
	if got := Exit(New(AgentBusy, "busy")); got != ExitState {
		t.Errorf("agent_busy exits %d", got)
	}
	if got := Exit(errors.New("boom")); got != ExitTool {
		t.Errorf("an unclassified error exits %d", got)
	}
	wrapped := New(PromptUncertain, "unsure").Wrap(errors.New("cause"))
	if got := Exit(wrapped); got != ExitUncertain {
		t.Errorf("prompt_uncertain exits %d", got)
	}
}
