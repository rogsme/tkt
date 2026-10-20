package pi

import (
	"os/exec"
	"runtime"

	"gitlab.com/rogs/tkt/internal/apperr"
)

// Where the pi that tkt agent runs comes from.
const (
	SourceEnv    = "env"
	SourceBundle = "bundle"
)

// Runtime is the pi binary tkt agent runs.
type Runtime struct {
	Path string
	// Version is empty when TKT_PI names the binary.
	Version string
	Source  string
}

// Resolve finds pi: the binary TKT_PI names when it is set, otherwise the
// bundled release, unpacked under dataDir. note hears about an unpack.
func Resolve(getenv func(string) string, dataDir string, note func(format string, args ...any)) (Runtime, error) {
	asset, _ := AssetFor(runtime.GOOS, runtime.GOARCH)
	return resolve(getenv, dataDir, note, bundle, asset, locked)
}

func resolve(getenv func(string) string, dataDir string, note func(string, ...any), tarball []byte, asset string, lock func() Lock) (Runtime, error) {
	if v := getenv("TKT_PI"); v != "" {
		p, err := exec.LookPath(v)
		if err != nil {
			return Runtime{}, apperr.New(apperr.PiMissing, "TKT_PI names %s, which is not a program tkt can run", v).
				WithHint("set TKT_PI to a pi binary, or unset it to use the bundled pi")
		}
		return Runtime{Path: p, Source: SourceEnv}, nil
	}
	if len(tarball) == 0 {
		return Runtime{}, apperr.New(apperr.PiMissing, "this tkt has no bundled pi, and TKT_PI is not set").
			WithHint("install a release build of tkt (install.sh), or set TKT_PI to a pi binary")
	}
	l := lock()
	p, err := Unpack(dataDir, l, asset, tarball, note)
	if err != nil {
		return Runtime{}, err
	}
	return Runtime{Path: p, Version: l.Version, Source: SourceBundle}, nil
}

// Bundled reports the version of the pi this tkt embeds, without unpacking
// it.
func Bundled() (string, bool) {
	if len(bundle) == 0 {
		return "", false
	}
	return locked().Version, true
}
