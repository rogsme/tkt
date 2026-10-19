package release

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.2.0", "v0.2.0", 0, true},
		{"v0.2.0", "v0.3.0", -1, true},
		{"v1.0.0", "v0.9.9", 1, true},
		{"v0.10.0", "v0.9.0", 1, true},
		{"v0.3.0-rc.1", "v0.3.0", -1, true},
		{"v0.3.0-rc.2", "v0.3.0-rc.10", -1, true},
		{"v0.3.0-rc.1", "v0.2.0", 1, true},
		{"v0.3.0-alpha", "v0.3.0-alpha.1", -1, true},
		{"v0.3.0-1", "v0.3.0-alpha", -1, true},
		{"v0.3.0+meta", "v0.3.0", 0, true},
		{"dev", "v0.2.0", 0, false},
		{"v0.2.0", "test", 0, false},
		{"0.2.0", "v0.2.0", 0, false},
		{"v0.2", "v0.2.0", 0, false},
		{"v0.2.0-", "v0.2.0", 0, false},
	}
	for _, tc := range cases {
		got, ok := Compare(tc.a, tc.b)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d, %v", tc.a, tc.b, got, ok, tc.want, tc.ok)
		}
	}
}

func TestChecksum(t *testing.T) {
	sums := []byte("aaa  tkt_linux_amd64\nBBB *tkt_darwin_arm64\nccc  install.sh\n")
	for name, want := range map[string]string{"tkt_linux_amd64": "aaa", "tkt_darwin_arm64": "bbb"} {
		if got, ok := Checksum(sums, name); !ok || got != want {
			t.Errorf("Checksum(%s) = %q, %v; want %q", name, got, ok, want)
		}
	}
	if _, ok := Checksum(sums, "tkt_linux_arm64"); ok {
		t.Error("Checksum found a name the file lacks")
	}
}

func TestAsset(t *testing.T) {
	if got, ok := Asset("darwin", "arm64"); !ok || got != "tkt_darwin_arm64" {
		t.Errorf("Asset(darwin, arm64) = %q, %v", got, ok)
	}
	for _, s := range [][2]string{{"windows", "amd64"}, {"linux", "386"}} {
		if _, ok := Asset(s[0], s[1]); ok {
			t.Errorf("Asset(%s, %s) has a build", s[0], s[1])
		}
	}
}

func TestFileURLs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "api", "releases"), 0o755)
	os.MkdirAll(filepath.Join(dir, "rel", "v9.9.9"), 0o755)
	os.WriteFile(filepath.Join(dir, "api", "releases", "latest"), []byte(`{"id":1,"tag_name":"v9.9.9"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "rel", "v9.9.9", "SHA256SUMS"), []byte("sums\n"), 0o644)
	c := New("file://"+filepath.Join(dir, "rel"), "file://"+filepath.Join(dir, "api")+"/")
	ctx := context.Background()

	tag, err := c.Latest(ctx)
	if err != nil || tag != "v9.9.9" {
		t.Fatalf("Latest = %q, %v", tag, err)
	}
	var b bytes.Buffer
	if err := c.Fetch(ctx, tag, "SHA256SUMS", &b); err != nil || b.String() != "sums\n" {
		t.Fatalf("Fetch = %q, %v", b.String(), err)
	}
	if err := c.Fetch(ctx, tag, "tkt_plan9_mips", &b); err == nil {
		t.Fatal("Fetch of a missing asset succeeded")
	}
}
