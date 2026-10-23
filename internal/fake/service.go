package fake

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// units is what the fake service managers keep in $FAKE_DIR: the units or
// agents that are enabled, and the ones that run.
type units struct {
	Enabled []string `json:"enabled"`
	Active  []string `json:"active"`
}

func loadUnits(name string) (*units, string) {
	p := filepath.Join(os.Getenv("FAKE_DIR"), name)
	u := &units{Enabled: []string{}, Active: []string{}}
	if b, err := os.ReadFile(p); err == nil {
		json.Unmarshal(b, u)
	}
	return u, p
}

func (u *units) save(p string) {
	b, _ := json.MarshalIndent(u, "", "  ")
	os.WriteFile(p, b, 0o644)
}

func add(list []string, items ...string) []string {
	for _, i := range items {
		if !slices.Contains(list, i) {
			list = append(list, i)
		}
	}
	return list
}

func drop(list []string, items ...string) []string {
	return slices.DeleteFunc(list, func(s string) bool { return slices.Contains(items, s) })
}

var instanceRE = regexp.MustCompile(`^([^@]+@)[^.]+(\.service)$`)

// Systemctl answers the systemctl --user commands tkt runs, keeping the
// units' states in $FAKE_DIR/systemd.json. Like systemd, it refuses a unit
// with no file in the user's unit directory, and finds an instance's file
// under its template's name. Nothing runs: a started unit only counts as
// active.
func Systemctl() {
	logCall("systemctl")
	args := os.Args[1:]
	if len(args) < 2 || args[0] != "--user" {
		die(2, "fake systemctl: unsupported: %s", strings.Join(args, " "))
	}
	verb, names := args[1], []string{}
	now := false
	for _, a := range args[2:] {
		switch a {
		case "--now":
			now = true
		case "--no-block", "--quiet":
		default:
			names = append(names, a)
		}
	}
	u, p := loadUnits("systemd.json")
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	need := func() {
		for _, n := range names {
			file := instanceRE.ReplaceAllString(n, "$1$2")
			if _, err := os.Stat(filepath.Join(dir, "systemd", "user", file)); err != nil {
				die(1, "Failed to %s unit: Unit file %s does not exist.", verb, n)
			}
		}
	}
	switch verb {
	case "daemon-reload":
	case "enable":
		need()
		u.Enabled = add(u.Enabled, names...)
		if now {
			u.Active = add(u.Active, names...)
		}
	case "disable":
		u.Enabled = drop(u.Enabled, names...)
		if now {
			u.Active = drop(u.Active, names...)
		}
	case "start", "restart":
		need()
		u.Active = add(u.Active, names...)
	case "stop":
		u.Active = drop(u.Active, names...)
	case "is-enabled":
		if slices.Contains(u.Enabled, names[0]) {
			fmt.Println("enabled")
			os.Exit(0)
		}
		fmt.Println("disabled")
		os.Exit(1)
	case "is-active":
		if slices.Contains(u.Active, names[0]) {
			fmt.Println("active")
			os.Exit(0)
		}
		fmt.Println("inactive")
		os.Exit(3)
	default:
		die(2, "fake systemctl: unsupported: %s", verb)
	}
	u.save(p)
	os.Exit(0)
}

// SystemdEscape answers systemd-escape --path.
func SystemdEscape() {
	logCall("systemd-escape")
	args := os.Args[1:]
	if len(args) != 2 || args[0] != "--path" {
		die(2, "fake systemd-escape: unsupported: %s", strings.Join(args, " "))
	}
	fmt.Println(EscapePath(args[1]))
	os.Exit(0)
}

// EscapePath names a path the way systemd-escape --path does: the slashes
// become dashes, and every byte but letters, digits, ":", "_" and a "."
// that does not lead becomes \xNN.
func EscapePath(p string) string {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return "-"
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '/':
			b.WriteByte('-')
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == ':', c == '_', c == '.' && i > 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	return b.String()
}

// Loginctl answers whether the user lingers from $FAKE_DIR/linger, and
// enable-linger writes yes there.
func Loginctl() {
	logCall("loginctl")
	args := os.Args[1:]
	file := filepath.Join(os.Getenv("FAKE_DIR"), "linger")
	switch {
	case len(args) > 0 && args[0] == "show-user":
		b, err := os.ReadFile(file)
		if err != nil {
			b = []byte("no")
		}
		fmt.Println(strings.TrimSpace(string(b)))
	case len(args) > 0 && args[0] == "enable-linger":
		os.WriteFile(file, []byte("yes\n"), 0o644)
	default:
		die(2, "fake loginctl: unsupported: %s", strings.Join(args, " "))
	}
	os.Exit(0)
}

var labelRE = regexp.MustCompile(`<key>Label</key>\s*<string>([^<]+)</string>`)

// Launchctl answers the launchctl commands tkt runs, keeping the agents in
// $FAKE_DIR/launchd.json: the loaded ones in enabled, the running ones in
// active. bootstrap loads and starts the agent its plist names, and, like
// launchd, refuses one that is loaded already; kickstart starts a loaded
// one; bootout unloads one; print says whether one is loaded, and runs.
func Launchctl() {
	logCall("launchctl")
	args := os.Args[1:]
	if len(args) < 2 {
		die(2, "fake launchctl: unsupported: %s", strings.Join(args, " "))
	}
	u, p := loadUnits("launchd.json")
	label := func(target string) string { return target[strings.LastIndex(target, "/")+1:] }
	switch args[0] {
	case "bootstrap":
		b, err := os.ReadFile(args[2])
		m := labelRE.FindSubmatch(b)
		if err != nil || m == nil || slices.Contains(u.Enabled, string(m[1])) {
			die(5, "Bootstrap failed: 5: Input/output error")
		}
		u.Enabled, u.Active = add(u.Enabled, string(m[1])), add(u.Active, string(m[1]))
	case "bootout":
		if !slices.Contains(u.Enabled, label(args[1])) {
			die(3, "Boot-out failed: 3: No such process")
		}
		u.Enabled, u.Active = drop(u.Enabled, label(args[1])), drop(u.Active, label(args[1]))
	case "kickstart":
		if !slices.Contains(u.Enabled, label(args[len(args)-1])) {
			die(113, "Could not find service %q in domain for user gui", label(args[len(args)-1]))
		}
		u.Active = add(u.Active, label(args[len(args)-1]))
	case "print":
		if !slices.Contains(u.Enabled, label(args[1])) {
			die(113, "Could not find service %q in domain for user gui", label(args[1]))
		}
		state := "not running"
		if slices.Contains(u.Active, label(args[1])) {
			state = "running"
		}
		fmt.Printf("%s = {\n\tstate = %s\n}\n", args[1], state)
		os.Exit(0)
	default:
		die(2, "fake launchctl: unsupported: %s", args[0])
	}
	u.save(p)
	os.Exit(0)
}
