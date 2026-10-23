// Command tkt runs the mechanical steps of the rogs-skills ticket lifecycle
// from a herdr pane.
package main

import (
	"context"
	"os"

	"gitlab.com/rogs/tkt/internal/app"
	"gitlab.com/rogs/tkt/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version string

func main() {
	// tkt run in a ticket's tab sees that ticket's TMPDIR, which tkt done
	// removes. The programs tkt starts get the directory above it, so an
	// OpenCode service tkt starts never keeps one.
	if d := os.Getenv("TMPDIR"); d != "" && app.TmpRoot(d) != d {
		os.Setenv("TMPDIR", app.TmpRoot(d))
	}
	os.Exit(cli.Main(context.Background(), os.Args[1:], version, cli.IO{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.Getenv,
	}))
}
