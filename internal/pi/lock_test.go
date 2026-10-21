package pi

import (
	"bytes"
	"strings"
	"testing"
)

// pi.lock parses, pins a tarball for every target, and survives a rewrite
// by the fetch tool unchanged.
func TestLockFile(t *testing.T) {
	l, err := ParseLock(lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if l.Version == "" || !strings.Contains(l.URL, "{asset}") {
		t.Errorf("lock %+v", l)
	}
	for _, target := range Targets {
		goos, goarch, _ := strings.Cut(target, "/")
		a, ok := AssetFor(goos, goarch)
		if !ok {
			t.Fatalf("no asset for %s", target)
		}
		if _, ok := l.Assets[a]; !ok {
			t.Errorf("pi.lock pins no %s", a)
		}
	}
	if len(l.Assets) != len(Targets) {
		t.Errorf("pi.lock pins %d assets, want %d", len(l.Assets), len(Targets))
	}
	if !bytes.Equal(l.Format(), lockFile) {
		t.Errorf("Format differs from pi.lock:\n%s", l.Format())
	}
	if locked().Version != l.Version {
		t.Errorf("locked() = %q", locked().Version)
	}
}

func TestAssetURL(t *testing.T) {
	l := Lock{Version: "1.3.0", URL: "https://example.com/pi/v{version}/{asset}"}
	if got := l.AssetURL("pi-linux-x64.tar.gz"); got != "https://example.com/pi/v1.3.0/pi-linux-x64.tar.gz" {
		t.Errorf("AssetURL = %s", got)
	}
}

func TestParseLockRefuses(t *testing.T) {
	const sum = "24f5be328b66d4da36876296989501974f43ab8434fbb57c35c523a3ea505ded"
	for name, src := range map[string]string{
		"unknown word":   "version 1\nurl u\nsha256 " + sum + " a\nmirror m\n",
		"short digest":   "version 1\nurl u\nsha256 abc a\n",
		"extra value":    "version 1 2\nurl u\nsha256 " + sum + " a\n",
		"no version":     "url u\nsha256 " + sum + " a\n",
		"no url":         "version 1\nsha256 " + sum + " a\n",
		"no sha256 line": "version 1\nurl u\n",
	} {
		if _, err := ParseLock([]byte(src)); err == nil {
			t.Errorf("%s: ParseLock accepted\n%s", name, src)
		}
	}
}

func TestAssetFor(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"linux", "amd64"}:  "pi-linux-x64.tar.gz",
		{"linux", "arm64"}:  "pi-linux-arm64.tar.gz",
		{"darwin", "amd64"}: "pi-darwin-x64.tar.gz",
		{"darwin", "arm64"}: "pi-darwin-arm64.tar.gz",
	} {
		if got, ok := AssetFor(in[0], in[1]); !ok || got != want {
			t.Errorf("AssetFor(%s, %s) = %q, %v; want %q", in[0], in[1], got, ok, want)
		}
	}
	for _, in := range [][2]string{{"windows", "amd64"}, {"linux", "386"}, {"freebsd", "arm64"}} {
		if got, ok := AssetFor(in[0], in[1]); ok {
			t.Errorf("AssetFor(%s, %s) = %q", in[0], in[1], got)
		}
	}
}
