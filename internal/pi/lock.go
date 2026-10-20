// Package pi runs the pi coding agent behind tkt agent: the release that
// release builds embed, pinned in pi.lock, and tkt's own extension for it.
package pi

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// lockFile is pi.lock, which also the fetch tool and the release workflow
// read.
//
//go:embed pi.lock
var lockFile []byte

// Targets are the systems with a pi tarball, as GOOS/GOARCH.
var Targets = []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}

// Lock pins a pi release: its version, where its tarballs live, and the
// SHA-256 of each tarball by asset name.
type Lock struct {
	Version string
	// URL holds {version} and {asset}.
	URL    string
	Assets map[string]string
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ParseLock reads pi.lock: a word and its values on each line, and #
// starting a comment line.
func ParseLock(b []byte) (Lock, error) {
	l := Lock{Assets: map[string]string{}}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		bad := func(why string) (Lock, error) {
			return Lock{}, fmt.Errorf("pi.lock line %d: %s", n, why)
		}
		switch {
		case f[0] == "version" && len(f) == 2:
			l.Version = f[1]
		case f[0] == "url" && len(f) == 2:
			l.URL = f[1]
		case f[0] == "sha256" && len(f) == 3 && sha256Hex.MatchString(f[1]):
			l.Assets[f[2]] = f[1]
		case f[0] == "version" || f[0] == "url" || f[0] == "sha256":
			return bad(f[0] + " has the wrong values")
		default:
			return bad(fmt.Sprintf("unknown word %q", f[0]))
		}
	}
	switch {
	case l.Version == "":
		return Lock{}, fmt.Errorf("pi.lock has no version line")
	case l.URL == "":
		return Lock{}, fmt.Errorf("pi.lock has no url line")
	case len(l.Assets) == 0:
		return Lock{}, fmt.Errorf("pi.lock has no sha256 lines")
	}
	return l, nil
}

// Format writes l in pi.lock's format, with the assets in Targets order.
func (l Lock) Format() []byte {
	var b bytes.Buffer
	b.WriteString("# The pi release that release builds of tkt embed. Each line is a word and\n# its values; # starts a comment. Rewrite it with: just bump-pi VERSION\n")
	fmt.Fprintf(&b, "version %s\nurl %s\n", l.Version, l.URL)
	for _, t := range Targets {
		goos, goarch, _ := strings.Cut(t, "/")
		a, _ := AssetFor(goos, goarch)
		if sum, ok := l.Assets[a]; ok {
			fmt.Fprintf(&b, "sha256 %s %s\n", sum, a)
		}
	}
	return b.Bytes()
}

// AssetURL is where asset of l's release downloads from.
func (l Lock) AssetURL(asset string) string {
	return strings.NewReplacer("{version}", l.Version, "{asset}", asset).Replace(l.URL)
}

// AssetFor names pi's tarball for a system, as pi's releases name it. It
// reports false for a system without one.
func AssetFor(goos, goarch string) (string, bool) {
	arch, ok := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if !ok || (goos != "linux" && goos != "darwin") {
		return "", false
	}
	return "pi-" + goos + "-" + arch + ".tar.gz", true
}

// locked is the release that the embedded pi.lock pins. The tests parse
// pi.lock, so a broken one never reaches a build.
var locked = sync.OnceValue(func() Lock {
	l, err := ParseLock(lockFile)
	if err != nil {
		panic(err)
	}
	return l
})
