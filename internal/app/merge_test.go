package app

import (
	"slices"
	"testing"

	"gitlab.com/rogs/tkt/internal/apperr"
)

func TestParsePassed(t *testing.T) {
	want := []int{1, 2, 3, 4}
	for in, out := range map[string][]int{
		"all":     want,
		"1-4":     want,
		"1,2,3,4": want,
		"4,1-3":   want,
		" 2 , 3 ": {2, 3},
		"1-2,2-3": {1, 2, 3},
	} {
		got, err := parsePassed(in, want)
		if err != nil || !slices.Equal(got, out) {
			t.Errorf("parsePassed(%q) = %v, %v; want %v", in, got, err, out)
		}
	}
	for _, in := range []string{"", "0", "3-1", "one", "1,", "1-x"} {
		if _, err := parsePassed(in, want); err == nil || apperr.As(err).Code != apperr.Usage {
			t.Errorf("parsePassed(%q) took it: %v", in, err)
		}
	}
	if got := parseStepList(stepList([]int{1, 2, 4})); !slices.Equal(got, []int{1, 2, 4}) {
		t.Errorf("a merge order's steps came back as %v", got)
	}
}
