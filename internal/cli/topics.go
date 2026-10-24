package cli

import (
	"embed"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/rogs/tkt/internal/apperr"
)

//go:embed help/*.txt
var helpFiles embed.FS

// helpText returns an embedded help file.
func helpText(name string) string {
	b, err := helpFiles.ReadFile("help/" + name + ".txt")
	if err != nil {
		panic(err)
	}
	return strings.TrimRight(string(b), "\n")
}

// topic is a help topic: `tkt help NAME` prints it.
func topic(name, short, long string) *cobra.Command {
	return &cobra.Command{Use: name, Short: short, Long: long}
}

func topics() []*cobra.Command {
	return []*cobra.Command{
		topic("lifecycle", "A ticket from start to merge, step by step", helpText("lifecycle")),
		topic("json", "Machine-readable output with --json", helpText("json")),
		topic("exit-codes", "Exit statuses and error codes", exitCodesText()),
		topic("config", "Presets, stage lines, config.toml and the .tkt files", helpText("config")),
	}
}

// exitCodesText lists the exit statuses and every error code under its
// status, from the registry.
func exitCodesText() string {
	var b strings.Builder
	b.WriteString("tkt exits with one of these statuses. With --json, error.code names the exact\nfailure; programs branch on it.\n")
	for _, m := range apperr.ExitMeanings {
		fmt.Fprintf(&b, "\n%d  %s\n", m.Exit, m.Meaning)
		for _, i := range apperr.Registry {
			if i.Exit == m.Exit {
				fmt.Fprintf(&b, "   %-24s %s\n", i.Code, i.Summary)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
