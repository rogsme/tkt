package app

import (
	"slices"
	"testing"
)

func TestOpenArgv(t *testing.T) {
	url := "http://127.0.0.1:4310/?token=abc"
	if got := openArgv("darwin", url); !slices.Equal(got, []string{"open", url}) {
		t.Errorf("darwin: %q", got)
	}
	if got := openArgv("linux", url); !slices.Equal(got, []string{"xdg-open", url}) {
		t.Errorf("linux: %q", got)
	}
}
