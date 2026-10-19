package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/output"
	"gitlab.com/rogs/tkt/internal/release"
	"gitlab.com/rogs/tkt/internal/runner"
)

// updateApp returns an App whose releases are v0.3.0 (latest) and
// v0.4.0-rc.1 in a temporary directory, and whose binary is a file there.
func updateApp(t *testing.T, version string) (*App, string, *[][]string) {
	t.Helper()
	asset, ok := release.Asset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		t.Skip("no release build for this system")
	}
	dir := t.TempDir()
	for _, tag := range []string{"v0.3.0", "v0.4.0-rc.1"} {
		d := filepath.Join(dir, "rel", tag)
		os.MkdirAll(d, 0o755)
		bin := []byte("new tkt " + tag)
		sum := sha256.Sum256(bin)
		os.WriteFile(filepath.Join(d, asset), bin, 0o644)
		os.WriteFile(filepath.Join(d, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644)
	}
	os.MkdirAll(filepath.Join(dir, "api", "releases"), 0o755)
	os.WriteFile(filepath.Join(dir, "api", "releases", "latest"), []byte(`{"tag_name":"v0.3.0"}`), 0o644)
	target := filepath.Join(dir, "bin", "tkt")
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, []byte("old tkt"), 0o755)
	env := map[string]string{
		"TKT_RELEASES":      "file://" + filepath.Join(dir, "rel"),
		"TKT_RELEASES_API":  "file://" + filepath.Join(dir, "api"),
		"TKT_UPDATE_TARGET": target,
	}
	var ran [][]string
	r := runner.Func(func(_ context.Context, c runner.Cmd) runner.Result {
		ran = append(ran, c.Argv)
		b, _ := os.ReadFile(c.Argv[0])
		return runner.Result{Stdout: []byte("tkt version " + string(b[len("new tkt "):]) + "\n")}
	})
	a := &App{UI: &output.UI{Err: io.Discard}, Getenv: func(k string) string { return env[k] }, R: r, Version: version}
	return a, target, &ran
}

func TestUpdateDecides(t *testing.T) {
	cases := []struct {
		version   string
		opts      UpdateOpts
		to        string
		available bool
		updated   bool
		next      string
	}{
		{"v0.3.0", UpdateOpts{}, "v0.3.0", false, false, ""},
		{"v0.4.0-rc.1", UpdateOpts{}, "v0.3.0", false, false, ""},
		{"v0.2.0", UpdateOpts{Check: true}, "v0.3.0", true, false, "tkt update"},
		{"v0.2.0", UpdateOpts{}, "v0.3.0", true, true, ""},
		{"v0.3.0", UpdateOpts{Version: "v0.4.0-rc.1", Check: true}, "v0.4.0-rc.1", true, false, "tkt update v0.4.0-rc.1"},
		{"v0.4.0-rc.1", UpdateOpts{Version: "v0.3.0"}, "v0.3.0", true, true, ""},
		{"v0.3.0", UpdateOpts{Force: true}, "v0.3.0", false, true, ""},
		{"dev", UpdateOpts{Check: true}, "v0.3.0", true, false, "tkt update --force"},
		{"dev", UpdateOpts{Force: true}, "v0.3.0", true, true, ""},
	}
	for _, tc := range cases {
		a, target, ran := updateApp(t, tc.version)
		res, err := a.Update(context.Background(), tc.opts)
		if err != nil {
			t.Errorf("%s %+v: %v", tc.version, tc.opts, err)
			continue
		}
		if res.To != tc.to || res.Available != tc.available || res.Updated != tc.updated || res.Next != tc.next {
			t.Errorf("%s %+v: got %+v", tc.version, tc.opts, res)
		}
		b, _ := os.ReadFile(target)
		if got := string(b) == "new tkt "+tc.to; got != tc.updated {
			t.Errorf("%s %+v: the binary holds %q", tc.version, tc.opts, b)
		}
		if tc.updated && len(*ran) != 1 {
			t.Errorf("%s %+v: ran %v, want the new binary's --version once", tc.version, tc.opts, *ran)
		}
	}
}

func TestUpdateRefuses(t *testing.T) {
	a, target, _ := updateApp(t, "dev")
	_, err := a.Update(context.Background(), UpdateOpts{})
	if e := apperr.As(err); e.Code != apperr.UpdateDevBuild || e.Hint == "" {
		t.Errorf("a dev build: %v", err)
	}

	a.Version = "v0.2.0"
	a.R = runner.Func(func(context.Context, runner.Cmd) runner.Result {
		return runner.Result{Code: 126, Stderr: []byte("exec format error")}
	})
	_, err = a.Update(context.Background(), UpdateOpts{})
	if apperr.As(err).Code != apperr.UpdateVerify {
		t.Errorf("a binary that does not run: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "old tkt" {
		t.Errorf("the binary was replaced: %q", b)
	}
	if es, _ := os.ReadDir(filepath.Dir(target)); len(es) != 1 {
		t.Errorf("the download was left behind: %v", es)
	}

	if _, err := a.Update(context.Background(), UpdateOpts{Version: "latest"}); apperr.As(err).Code != apperr.Usage {
		t.Errorf("a version that is not a tag: %v", err)
	}
}
