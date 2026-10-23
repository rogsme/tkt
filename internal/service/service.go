// Package service writes the user services that keep herdr's server and tkt
// watch running while nobody is at the terminal: systemd user units on
// Linux, launchd agents on macOS. It renders the files and the commands;
// the app package runs them.
package service

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The service managers tkt writes for.
const (
	Systemd = "systemd"
	Launchd = "launchd"
)

// Spec is what the services run: the absolute paths of herdr and tkt, and
// the PATH they run with. Agents in herdr's panes inherit that PATH, so it
// must find the harnesses.
type Spec struct {
	Herdr string
	Tkt   string
	Path  string
}

// systemd units. One template serves every repository; each repository is
// an instance named after its path, as systemd-escape --path writes it.
const (
	HerdrUnit     = "herdr.service"
	WatchTemplate = "tkt-watch@.service"
)

// WatchUnit names the instance of the template for a repository whose path
// systemd-escape --path turned into escaped.
func WatchUnit(escaped string) string { return "tkt-watch@" + escaped + ".service" }

// EscapeArgv asks systemd how it names a path in a unit's instance.
func EscapeArgv(path string) []string { return []string{"systemd-escape", "--path", path} }

// SystemctlArgv runs systemctl on the user's own manager.
func SystemctlArgv(args ...string) []string { return append([]string{"systemctl", "--user"}, args...) }

// UnitDir is where systemd reads a user's own units.
func UnitDir(getenv func(string) string) string {
	if d := getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "systemd", "user")
	}
	return filepath.Join(getenv("HOME"), ".config", "systemd", "user")
}

// written heads every file tkt writes, so a person who finds one knows
// where it came from and that editing it is pointless.
const written = "tkt watch --install wrote this file, and writes it again each time it runs."

// HerdrUnitFile runs herdr's server, which stays in the foreground, and
// starts it again when it crashes. herdr server stop ends it for good.
func HerdrUnitFile(s Spec) string {
	return "# " + written + "\n" +
		"[Unit]\n" +
		"Description=herdr server\n" +
		"\n" +
		"[Service]\n" +
		"Environment=" + unitQuote("PATH="+s.Path) + "\n" +
		"ExecStart=" + unitWord(s.Herdr) + " server\n" +
		"Restart=on-failure\n" +
		"RestartSec=5\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
}

// WatchUnitFile runs tkt watch in the repository the instance names: %f is
// the instance's path, unescaped. It starts after herdr's server when both
// start at boot, and needs no more: tkt watch waits while herdr is down. It
// starts again whenever it exits, as tkt watch does on purpose when tkt
// update replaces it, except on the statuses a restart cannot fix: 3 and 4
// are tkt's own for a setting or a state, such as another watch that runs,
// and 200 is systemd's when the repository's directory is gone.
func WatchUnitFile(s Spec) string {
	return "# " + written + "\n" +
		"# One instance per repository: tkt-watch@$(systemd-escape --path DIR).service.\n" +
		"[Unit]\n" +
		"Description=tkt watch for %f\n" +
		"After=" + HerdrUnit + "\n" +
		"\n" +
		"[Service]\n" +
		"Environment=" + unitQuote("PATH="+s.Path) + "\n" +
		"WorkingDirectory=%f\n" +
		"ExecStart=" + unitWord(s.Tkt) + " watch\n" +
		"Restart=always\n" +
		"RestartSec=10\n" +
		"RestartPreventExitStatus=3 4 200\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_@+=:,./-]+$`)

// unitWord writes one word of a unit's command line: as it is when it is
// plain, otherwise in double quotes, with $ doubled, since systemd expands
// variables on command lines.
func unitWord(w string) string {
	if plainWord.MatchString(w) {
		return w
	}
	return unitQuote(strings.ReplaceAll(w, "$", "$$"))
}

// unitQuote quotes a value for a unit file. systemd reads C escapes inside
// double quotes and expands % specifiers.
func unitQuote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%")
	return `"` + r.Replace(v) + `"`
}

// UnitPath reads the PATH a unit file sets, or "" when it sets none.
func UnitPath(file string) string {
	for _, line := range strings.Split(file, "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment=")
		if !ok {
			continue
		}
		if s, err := strconv.Unquote(v); err == nil {
			v = s
		}
		if p, ok := strings.CutPrefix(v, "PATH="); ok {
			return strings.ReplaceAll(p, "%%", "%")
		}
	}
	return ""
}

// launchd agents. Their labels carry tkt's name, so they sort together in
// launchctl list.
const HerdrLabel = "tkt.herdr"

var labelUnsafe = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// WatchLabel names a repository's tkt watch agent after its path.
func WatchLabel(repo string) string {
	s := strings.ReplaceAll(strings.Trim(repo, "/"), "/", ".")
	return "tkt.watch." + labelUnsafe.ReplaceAllString(s, "-")
}

// AgentDir is where launchd reads a user's own agents.
func AgentDir(home string) string { return filepath.Join(home, "Library", "LaunchAgents") }

// Domain is the launchd domain of the user's own session.
func Domain(uid int) string { return "gui/" + strconv.Itoa(uid) }

// LaunchctlArgv runs launchctl.
func LaunchctlArgv(args ...string) []string { return append([]string{"launchctl"}, args...) }

// HerdrPlist runs herdr's server at login and starts it again when it
// crashes, but not after herdr server stop.
func HerdrPlist(s Spec, log string) string {
	return plist(HerdrLabel, []string{s.Herdr, "server"}, "", s.Path, log,
		"\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
}

// WatchPlist runs tkt watch in the repository at login and starts it again
// whenever it exits, as it does when tkt update replaces it.
func WatchPlist(s Spec, label, repo, log string) string {
	return plist(label, []string{s.Tkt, "watch"}, repo, s.Path, log,
		"\t<key>KeepAlive</key>\n\t<true/>\n\t<key>ThrottleInterval</key>\n\t<integer>30</integer>\n")
}

func plist(label string, argv []string, dir, path, log, keep string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<!-- " + written + " -->\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	str := func(k, v string) { fmt.Fprintf(&b, "\t<key>%s</key>\n\t<string>%s</string>\n", k, xmlText(v)) }
	str("Label", label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range argv {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlText(a))
	}
	b.WriteString("\t</array>\n")
	if dir != "" {
		str("WorkingDirectory", dir)
	}
	fmt.Fprintf(&b, "\t<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>PATH</key>\n\t\t<string>%s</string>\n\t</dict>\n", xmlText(path))
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString(keep)
	str("StandardOutPath", log)
	str("StandardErrorPath", log)
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func xmlText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

var plistPathRE = regexp.MustCompile(`<key>PATH</key>\s*<string>([^<]*)</string>`)

// PlistPath reads the PATH an agent's plist sets, or "".
func PlistPath(file string) string {
	m := plistPathRE.FindStringSubmatch(file)
	if m == nil {
		return ""
	}
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(m[1])
}
