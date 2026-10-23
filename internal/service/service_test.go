package service

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestUnitFilesCarryThePathAndTheBinaries(t *testing.T) {
	s := Spec{Herdr: "/home/acme/.local/bin/herdr", Tkt: "/home/acme/.local/bin/tkt", Path: "/home/acme/.local/bin:/usr/bin"}
	h := HerdrUnitFile(s)
	for _, want := range []string{
		"\nEnvironment=\"PATH=/home/acme/.local/bin:/usr/bin\"\n",
		"\nExecStart=/home/acme/.local/bin/herdr server\n",
		"\nRestart=on-failure\n",
		"\nWantedBy=default.target\n",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("herdr.service lacks %q:\n%s", want, h)
		}
	}
	w := WatchUnitFile(s)
	for _, want := range []string{
		"\nDescription=tkt watch for %f\n",
		"\nAfter=herdr.service\n",
		"\nWorkingDirectory=%f\n",
		"\nExecStart=/home/acme/.local/bin/tkt watch\n",
		"\nRestart=always\n",
		"\nRestartPreventExitStatus=3 4 200\n",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("tkt-watch@.service lacks %q:\n%s", want, w)
		}
	}
	// A watch that cannot start herdr still waits for it.
	if strings.Contains(w, "Wants=") || strings.Contains(w, "Requires=") {
		t.Errorf("the watch unit pulls herdr.service in:\n%s", w)
	}
	if got := UnitPath(w); got != s.Path {
		t.Errorf("UnitPath = %q, want %q", got, s.Path)
	}
}

func TestUnitFilesQuoteWhatSystemdWouldExpand(t *testing.T) {
	s := Spec{Herdr: "/opt/my tools/herdr", Tkt: "/opt/$HOME/tkt", Path: `/opt/100%/bin:/opt/a"b`}
	h := HerdrUnitFile(s)
	if !strings.Contains(h, "\nExecStart=\"/opt/my tools/herdr\" server\n") {
		t.Errorf("a path with a space is not quoted:\n%s", h)
	}
	if !strings.Contains(h, "\nEnvironment=\"PATH=/opt/100%%/bin:/opt/a\\\"b\"\n") {
		t.Errorf("the PATH is not escaped:\n%s", h)
	}
	if w := WatchUnitFile(s); !strings.Contains(w, "\nExecStart=\"/opt/$$HOME/tkt\" watch\n") {
		t.Errorf("a $ on the command line is not doubled:\n%s", w)
	}
	if got := UnitPath(h); got != s.Path {
		t.Errorf("UnitPath = %q, want %q", got, s.Path)
	}
}

func TestPlistsRunTheWatchInItsRepository(t *testing.T) {
	s := Spec{Herdr: "/Users/acme/.local/bin/herdr", Tkt: "/Users/acme/.local/bin/tkt", Path: "/Users/acme/.local/bin:/usr/bin"}
	label := WatchLabel("/Users/acme/code/acme app")
	if label != "tkt.watch.Users.acme.code.acme-app" {
		t.Fatalf("label %q", label)
	}
	p := WatchPlist(s, label, "/Users/acme/code/acme app", "/Users/acme/code/acme app/.git/tkt/watch.log")
	for _, want := range []string{
		"<key>Label</key>\n\t<string>tkt.watch.Users.acme.code.acme-app</string>",
		"<string>/Users/acme/.local/bin/tkt</string>\n\t\t<string>watch</string>",
		"<key>WorkingDirectory</key>\n\t<string>/Users/acme/code/acme app</string>",
		"<key>KeepAlive</key>\n\t<true/>",
		"<key>StandardErrorPath</key>\n\t<string>/Users/acme/code/acme app/.git/tkt/watch.log</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the watch plist lacks %q:\n%s", want, p)
		}
	}
	if got := PlistPath(p); got != s.Path {
		t.Errorf("PlistPath = %q, want %q", got, s.Path)
	}
	h := HerdrPlist(s, "/Users/acme/Library/Logs/herdr.log")
	if !strings.Contains(h, "<key>SuccessfulExit</key>\n\t\t<false/>") || strings.Contains(h, "WorkingDirectory") {
		t.Errorf("the herdr plist:\n%s", h)
	}
	if got := PlistPath(WatchPlist(Spec{Path: "/a&b"}, "x", "/r", "/l")); got != "/a&b" {
		t.Errorf("PlistPath = %q", got)
	}
	// launchd refuses a plist that is not well-formed XML.
	for _, doc := range []string{p, h, WatchPlist(Spec{Tkt: "/a<b>&c", Path: "/x"}, "x", "/r", "/l")} {
		d := xml.NewDecoder(strings.NewReader(doc))
		for {
			_, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("not well-formed XML: %v\n%s", err, doc)
			}
		}
	}
}
