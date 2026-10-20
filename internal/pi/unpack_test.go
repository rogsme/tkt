package pi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
)

const testAsset = "pi-test.tar.gz"

type entry struct {
	name string
	typ  byte
	mode int64
	body string
	link string
}

func dir(name string) entry { return entry{name: name, typ: tar.TypeDir, mode: 0o755} }
func file(name, body string) entry {
	return entry{name: name, typ: tar.TypeReg, mode: 0o644, body: body}
}
func symlink(name, link string) entry {
	return entry{name: name, typ: tar.TypeSymlink, mode: 0o777, link: link}
}
func hardlink(name, link string) entry {
	return entry{name: name, typ: tar.TypeLink, mode: 0o644, link: link}
}

// tarball builds a gzipped tarball in memory.
func tarball(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link, Size: int64(len(e.body))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func lockFor(data []byte) Lock {
	sum := sha256.Sum256(data)
	return Lock{Version: "1.0.0", URL: "u", Assets: map[string]string{testAsset: hex.EncodeToString(sum[:])}}
}

func versionDirOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "1.0.0-" + hex.EncodeToString(sum[:])[:12]
}

// release is a tarball laid out like pi's: a pi directory with the binary
// next to its package files.
func release(t *testing.T) []byte {
	exe := file("pi/pi", "#!/bin/sh\n")
	exe.mode = 0o755
	private := file("pi/theme/dark.json", "{}")
	private.mode = 0o600
	return tarball(t,
		dir("pi/"), exe, file("pi/package.json", `{"name":"pi"}`),
		private, symlink("pi/theme/default.json", "dark.json"),
		symlink("pi/readme", "../pi/package.json"), hardlink("pi/package.copy", "pi/package.json"))
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode()
}

func TestUnpack(t *testing.T) {
	data := t.TempDir()
	tb := release(t)
	var notes []string
	note := func(format string, args ...any) { notes = append(notes, format) }
	bin, err := Unpack(data, lockFor(tb), testAsset, tb, note)
	if err != nil {
		t.Fatal(err)
	}
	top := filepath.Join(data, "pi", versionDirOf(tb))
	if bin != filepath.Join(top, "pi", "pi") {
		t.Errorf("binary %s", bin)
	}
	for p, want := range map[string]os.FileMode{
		"pi/pi":                 0o755,
		"pi/package.json":       0o644,
		"pi/theme/dark.json":    0o600,
		"pi/theme/default.json": os.ModeSymlink,
	} {
		got := mode(t, filepath.Join(top, p))
		if got.Type() == os.ModeSymlink {
			got = os.ModeSymlink
		} else {
			got = got.Perm()
		}
		if got != want {
			t.Errorf("%s has mode %v, want %v", p, got, want)
		}
	}
	if b, err := os.ReadFile(filepath.Join(top, "pi", "readme")); err != nil || string(b) != `{"name":"pi"}` {
		t.Errorf("readme reads %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(top, "pi", "package.copy")); err != nil || string(b) != `{"name":"pi"}` {
		t.Errorf("package.copy reads %q, %v", b, err)
	}
	if !complete(top) || len(notes) != 1 {
		t.Errorf("complete %v, notes %q", complete(top), notes)
	}

	// A second run reuses the directory as it is, quietly.
	os.WriteFile(filepath.Join(top, "pi", "package.json"), []byte("kept"), 0o644)
	again, err := Unpack(data, lockFor(tb), testAsset, tb, note)
	if err != nil || again != bin {
		t.Fatalf("second Unpack = %s, %v", again, err)
	}
	if b, _ := os.ReadFile(filepath.Join(top, "pi", "package.json")); string(b) != "kept" {
		t.Error("the second Unpack wrote the directory again")
	}
	if len(notes) != 1 {
		t.Errorf("notes %q", notes)
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "pi")); len(entries) != 1 {
		t.Errorf("pi holds %d entries, want 1", len(entries))
	}
}

func TestUnpackRefuses(t *testing.T) {
	exe := file("pi/pi", "bin")
	for name, entries := range map[string][]entry{
		"a path that climbs out":      {exe, file("../evil", "x")},
		"a path that climbs out late": {exe, file("pi/../../evil", "x")},
		"an absolute path":            {exe, file("/tmp/evil", "x")},
		"a symlink out":               {exe, symlink("pi/out", "../../evil")},
		"an absolute symlink":         {exe, symlink("pi/out", "/etc/passwd")},
		"a hard link out":             {exe, hardlink("pi/out", "../evil")},
		"a hard link to a symlink":    {exe, symlink("pi/s", "pi"), hardlink("pi/h", "pi/s")},
		"a file through a symlink":    {exe, dir("pi/sub/"), symlink("pi/d", "sub"), file("pi/d/f", "x")},
		"no binary":                   {file("pi/package.json", "{}")},
	} {
		t.Run(name, func(t *testing.T) {
			data := t.TempDir()
			tb := tarball(t, entries...)
			_, err := Unpack(data, lockFor(tb), testAsset, tb, nil)
			if apperr.As(err).Code != apperr.PiUnpackFailed {
				t.Fatalf("Unpack = %v, want pi_unpack_failed", err)
			}
			if entries, _ := os.ReadDir(filepath.Join(data, "pi")); len(entries) != 0 {
				t.Errorf("Unpack left %d entries", len(entries))
			}
			if _, err := os.Lstat(filepath.Join(data, "evil")); err == nil {
				t.Error("an entry landed outside")
			}
		})
	}
}

func TestUnpackChecksMismatch(t *testing.T) {
	data := t.TempDir()
	tb := release(t)
	l := lockFor(tb)
	l.Assets[testAsset] = strings.Repeat("0", 64)
	_, err := Unpack(data, l, testAsset, tb, nil)
	if apperr.As(err).Code != apperr.PiUnpackFailed || !strings.Contains(err.Error(), "does not match pi.lock") {
		t.Fatalf("Unpack = %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "pi")); err == nil {
		t.Error("Unpack wrote before it checked the tarball")
	}
}

// When another run renames its copy first, that copy wins.
func TestFinishLosesTheRace(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "1.0.0-aaaaaaaaaaaa")
	tmp := final + tmpInfix + "1"
	for _, d := range []string{final, tmp} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "who"), []byte(filepath.Base(d)), 0o644)
	}
	os.WriteFile(filepath.Join(final, completeMarker), nil, 0o644)
	if err := finish(tmp, final); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(final, "who")); string(b) != filepath.Base(final) {
		t.Errorf("final holds %q", b)
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Error("the losing copy is still there")
	}
}

// A final directory without the marker is a leftover, which a finished copy
// replaces.
func TestFinishReplacesALeftover(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "1.0.0-aaaaaaaaaaaa")
	tmp := final + tmpInfix + "1"
	os.MkdirAll(filepath.Join(final, "pi"), 0o755)
	os.MkdirAll(tmp, 0o755)
	os.WriteFile(filepath.Join(tmp, "who"), []byte("tmp"), 0o644)
	if err := finish(tmp, final); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(final, "who")); string(b) != "tmp" || !complete(final) {
		t.Errorf("final holds %q, complete %v", b, complete(final))
	}
}

func TestUnpackPrunes(t *testing.T) {
	data := t.TempDir()
	root := filepath.Join(data, "pi")
	tb := release(t)
	name := versionDirOf(tb)
	old := time.Now().Add(-2 * time.Hour)
	for _, d := range []string{"0.9.0-aaaaaaaaaaaa", name + tmpInfix + "1", name + tmpInfix + "2", "notes"} {
		os.MkdirAll(filepath.Join(root, d, "x"), 0o755)
	}
	os.Chtimes(filepath.Join(root, name+tmpInfix+"1"), old, old)
	os.WriteFile(filepath.Join(root, "0.8.0-bbbbbbbbbbbb"), nil, 0o644)
	if _, err := Unpack(data, lockFor(tb), testAsset, tb, nil); err != nil {
		t.Fatal(err)
	}
	for d, want := range map[string]bool{
		name:                  true,
		"0.9.0-aaaaaaaaaaaa":  false,
		name + tmpInfix + "1": false,
		name + tmpInfix + "2": true,
		"notes":               true,
		"0.8.0-bbbbbbbbbbbb":  true,
	} {
		if _, err := os.Stat(filepath.Join(root, d)); (err == nil) != want {
			t.Errorf("%s exists: %v, want %v", d, err == nil, want)
		}
	}
}
