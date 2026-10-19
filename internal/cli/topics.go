package cli

import (
	"embed"
	"strings"

	"github.com/spf13/cobra"
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
		topic("config", "Presets, stage lines and the config.toml file", helpText("config")),
	}
}
