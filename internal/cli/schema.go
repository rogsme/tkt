package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"gitlab.com/rogs/tkt/internal/app"
	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/contract"
	"gitlab.com/rogs/tkt/internal/output"
)

// resultTypes names each command's result, for tkt schema.
var resultTypes = map[string]any{
	"agent":      app.FrontResult{},
	"web":        app.WebResult{},
	"start":      app.StartResult{},
	"approve":    app.SendResult{},
	"send":       app.SendResult{},
	"auto":       app.AutoResult{},
	"hold":       app.HoldResult{},
	"watch":      app.WatchResult{},
	"show":       app.ShowResult{},
	"suggest":    app.SuggestResult{},
	"history":    app.HistoryResult{},
	"run":        app.RunResult{},
	"sync":       app.SyncResult{},
	"merge":      app.MergeResult{},
	"resume":     app.ResumeResult{},
	"done":       app.DoneResult{},
	"chores":     app.ChoresResult{},
	"chore run":  app.ChoreRunResult{},
	"ls":         app.LsResult{},
	"wait":       app.WaitResult{},
	"doctor":     app.DoctorResult{},
	"hook tmp":   app.HookResult{},
	"hook event": app.HookEventResult{},
	"update":     app.UpdateResult{},
	"schema":     Schema{},
}

// Schema describes tkt to a program: every command with its arguments,
// flags and result fields, the exit statuses and error codes, and the
// environment it reads.
type Schema struct {
	Schema          int              `json:"schema"`
	Name            string           `json:"name"`
	Version         string           `json:"version"`
	ContractVersion int              `json:"contract_version"`
	Envelope        map[string]any   `json:"envelope"`
	GlobalFlags     []SchemaFlag     `json:"global_flags"`
	Commands        []SchemaCommand  `json:"commands"`
	Topics          []SchemaTopic    `json:"topics"`
	ExitCodes       any              `json:"exit_codes"`
	Errors          []apperr.Info    `json:"errors"`
	Environment     []SchemaVariable `json:"environment"`
}

// SchemaCommand is one command.
type SchemaCommand struct {
	Path    string         `json:"path"`
	Use     string         `json:"use"`
	Short   string         `json:"short"`
	Long    string         `json:"long"`
	Example string         `json:"example"`
	Group   string         `json:"group"`
	Args    []SchemaArg    `json:"args"`
	Flags   []SchemaFlag   `json:"flags"`
	Result  map[string]any `json:"result"`
}

// SchemaArg is a positional argument.
type SchemaArg struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Variadic bool   `json:"variadic"`
}

// SchemaFlag is a flag.
type SchemaFlag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default"`
	Usage     string `json:"usage"`
}

// SchemaTopic is a help topic: tkt help NAME.
type SchemaTopic struct {
	Name  string `json:"name"`
	Short string `json:"short"`
}

// SchemaVariable is an environment variable tkt reads.
type SchemaVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s Schema) Text(w io.Writer) { writeJSON(w, s) }

// writeJSON prints v as indented JSON.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

var environment = []SchemaVariable{
	{"TKT_FORMAT", "json makes every command print the JSON envelope; text, or unset, prints text"},
	{"XDG_CONFIG_HOME", "tkt reads $XDG_CONFIG_HOME/tkt/config.toml (default ~/.config/tkt/config.toml), then the repository's .tkt/tkt.toml and .tkt/tkt.local.toml"},
	{"XDG_STATE_HOME", "tkt logs every command it runs to $XDG_STATE_HOME/tkt/tkt.log, and keeps the history tkt stats reads in history.jsonl next to it, and the light or dark choice of the tkt agent's web page in web.json (default ~/.local/state/tkt)"},
	{"XDG_DATA_HOME", "tkt unpacks its bundled pi and its extension under $XDG_DATA_HOME/tkt (default ~/.local/share/tkt)"},
	{"HERDR_ENV", "herdr sets it to 1 inside its panes; agent, start, approve, send, auto, run, resume, done, show and wait need it, and tkt alone opens the agent only there"},
	{"HERDR_WORKSPACE_ID", "the herdr workspace where tkt opens tabs"},
	{"HERDR_TAB_ID", "the tab tkt runs in; tkt done refuses to close it"},
	{"HERDR_PANE_ID", "the pane tkt runs in; tkt never starts an agent in it"},
	{"TKT_RELEASES", "where tkt update downloads releases from (default https://git.rogs.me/rogs/tkt/releases/download); install.sh reads it too"},
	{"TKT_RELEASES_API", "the Gitea API that tkt update asks for the latest release (default https://git.rogs.me/api/v1/repos/rogs/tkt)"},
	{"TKT_PI", "the pi binary tkt agent runs instead of the one bundled with tkt"},
	{"TKT_WATCH", "off keeps tkt commands from starting tkt watch, as [watch] autostart = false does"},
	{"TKT_SERVICE_MANAGER", "systemd or launchd: the services tkt watch --install writes, instead of launchd on macOS and systemd elsewhere; any other value means neither"},
	{"CLAUDE_CONFIG_DIR", "where Claude Code keeps its sessions (default ~/.claude)"},
	{"NO_COLOR", "turns off color in help and errors"},
}

// buildSchema walks the command tree, so the schema always matches the help.
func buildSchema(root *cobra.Command, version string) Schema {
	// cobra adds the completion command only when it executes; add it now
	// so the schema lists it however it is built.
	root.InitDefaultCompletionCmd()
	s := Schema{
		Schema:          output.Schema,
		Name:            root.Name(),
		Version:         version,
		ContractVersion: contract.Version,
		Envelope: map[string]any{
			"description": "with --json or TKT_FORMAT=json, stdout holds one document per run; see tkt help json",
			"success":     `{"schema": 1, "ok": true, "command": PATH, "result": RESULT, "warnings": [STRING]}`,
			"error":       `{"schema": 1, "ok": false, "command": PATH, "error": {"code": CODE, "exit": STATUS, "message": STRING, "hint": STRING, "details": OBJECT}, "warnings": [STRING]}`,
		},
		GlobalFlags: flagsOf(root.PersistentFlags()),
		ExitCodes:   apperr.ExitMeanings,
		Errors:      apperr.Registry,
		Environment: environment,
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sc := range c.Commands() {
			if sc.Hidden || sc.Name() == "help" {
				continue
			}
			if !sc.Runnable() && !sc.HasSubCommands() {
				s.Topics = append(s.Topics, SchemaTopic{Name: sc.Name(), Short: sc.Short})
				continue
			}
			if sc.Runnable() {
				path := strings.TrimPrefix(sc.CommandPath(), root.Name()+" ")
				s.Commands = append(s.Commands, SchemaCommand{
					Path: path, Use: sc.Use, Short: sc.Short, Long: sc.Long, Example: sc.Example, Group: sc.GroupID,
					Args: argsOf(sc.Use), Flags: flagsOf(sc.LocalNonPersistentFlags()), Result: resultOf(path),
				})
			}
			walk(sc)
		}
	}
	walk(root)
	return s
}

// argsOf reads the arguments from a Use line: NAME is required, [NAME] is
// optional, and NAME... takes several.
func argsOf(use string) []SchemaArg {
	args := []SchemaArg{}
	for _, tok := range strings.Fields(use)[1:] {
		a := SchemaArg{Required: !strings.HasPrefix(tok, "[")}
		tok = strings.Trim(tok, "[]")
		a.Variadic = strings.HasSuffix(tok, "...")
		a.Name = strings.TrimSuffix(tok, "...")
		if a.Name == "flags" || strings.HasPrefix(a.Name, "-") {
			continue
		}
		args = append(args, a)
	}
	return args
}

func flagsOf(fs *pflag.FlagSet) []SchemaFlag {
	out := []SchemaFlag{}
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		out = append(out, SchemaFlag{Name: f.Name, Shorthand: f.Shorthand, Type: f.Value.Type(), Default: f.DefValue, Usage: f.Usage})
	})
	return out
}

// resultOf describes a command's result fields and their JSON types.
func resultOf(path string) map[string]any {
	v, ok := resultTypes[path]
	if !ok {
		return nil
	}
	return describe(reflect.TypeOf(v), 0)
}

func describe(t reflect.Type, depth int) map[string]any {
	out := map[string]any{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "" || name == "-" {
			continue
		}
		out[name] = kindOf(f.Type, depth)
	}
	return out
}

func kindOf(t reflect.Type, depth int) any {
	nullable := false
	if t.Kind() == reflect.Pointer {
		t, nullable = t.Elem(), true
	}
	var k any
	switch t.Kind() {
	case reflect.String:
		k = "string"
	case reflect.Bool:
		k = "boolean"
	case reflect.Int, reflect.Int64:
		k = "integer"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Struct && depth < 3 {
			k = []any{describe(t.Elem(), depth+1)}
		} else {
			k = fmt.Sprintf("array of %v", kindOf(t.Elem(), depth+1))
		}
	case reflect.Struct:
		if depth < 3 {
			k = describe(t, depth+1)
		} else {
			k = "object"
		}
	case reflect.Map, reflect.Interface:
		k = "object"
	default:
		k = t.Kind().String()
	}
	if nullable {
		if s, ok := k.(string); ok {
			return s + " or null"
		}
		return map[string]any{"nullable": true, "fields": k}
	}
	return k
}

func schemaCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Describe every command, flag, result and error code as JSON",
		Long: `tkt schema prints a JSON description of tkt for programs that drive it: every
command with its arguments, flags and result fields, the exit statuses and
error codes, and the environment variables tkt reads. It is built from the same
definitions as the help, so the two always agree.

Without --json it prints the description itself; with --json it is the result
of the usual envelope.`,
		Example: "  tkt schema\n  tkt schema --json | jq '.result.errors[] | select(.exit == 4) | .code'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s := buildSchema(cmd.Root(), versionString(g.version))
			if g.json {
				return show(g, cmd, s, false, nil)
			}
			return writeJSON(cmd.OutOrStdout(), s)
		},
	}
}
