package pi

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gitlab.com/rogs/tkt/internal/apperr"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestResolveTKTPI(t *testing.T) {
	bin := t.TempDir()
	p := filepath.Join(bin, "mypi")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	tb := release(t)
	for _, v := range []string{p, "mypi"} {
		// TKT_PI wins over a bundle.
		rt, err := resolve(env(map[string]string{"TKT_PI": v}), t.TempDir(), nil, tb, testAsset, func() Lock { return lockFor(tb) })
		if err != nil {
			t.Fatal(err)
		}
		if rt.Path != p || rt.Source != SourceEnv || rt.Version != "" {
			t.Errorf("TKT_PI=%s: %+v", v, rt)
		}
	}
	_, err := resolve(env(map[string]string{"TKT_PI": "no-such-pi"}), t.TempDir(), nil, tb, testAsset, nil)
	if e := apperr.As(err); e.Code != apperr.PiMissing || !strings.Contains(e.Msg, "no-such-pi") {
		t.Errorf("a missing TKT_PI: %v", err)
	}
}

func TestResolveBundle(t *testing.T) {
	data := t.TempDir()
	tb := release(t)
	rt, err := resolve(env(nil), data, nil, tb, testAsset, func() Lock { return lockFor(tb) })
	if err != nil {
		t.Fatal(err)
	}
	if rt.Source != SourceBundle || rt.Version != "1.0.0" || rt.Path != filepath.Join(data, "pi", versionDirOf(tb), "pi", "pi") {
		t.Errorf("bundle: %+v", rt)
	}
	if _, err := os.Stat(rt.Path); err != nil {
		t.Error(err)
	}
}

func TestResolveWithoutPi(t *testing.T) {
	_, err := resolve(env(nil), t.TempDir(), nil, nil, testAsset, nil)
	if e := apperr.As(err); e.Code != apperr.PiMissing || !strings.Contains(e.Hint, "install.sh") {
		t.Errorf("no pi: %v", err)
	}
}

// A build with the bundled_pi tag embeds the tarball pi.lock pins for its
// system; the usual test build embeds none.
func TestBundle(t *testing.T) {
	v, ok := Bundled()
	if ok != (len(bundle) > 0) || (ok && v != locked().Version) {
		t.Errorf("Bundled() = %s, %v with a bundle of %d bytes", v, ok, len(bundle))
	}
	if len(bundle) == 0 {
		t.Skip("built without the bundled_pi tag")
	}
	asset, _ := AssetFor(runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(bundle)
	if got := hex.EncodeToString(sum[:]); got != locked().Assets[asset] {
		t.Errorf("the bundle has sha256 %s, pi.lock wants %s", got, locked().Assets[asset])
	}
}
