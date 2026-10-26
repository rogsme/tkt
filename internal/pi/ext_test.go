package pi

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestExtEmbedsTheExtensionOnly(t *testing.T) {
	names, _, err := extContent()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, "ext/index.ts") || !slices.Contains(names, "ext/system.md") || !slices.Contains(names, "ext/tkt-dark.json") || !slices.Contains(names, "ext/tkt-light.json") ||
		!slices.Contains(names, "ext/web/index.html") || !slices.Contains(names, "ext/web/vendor/preact.js") {
		t.Errorf("embedded %q", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "ext/test/") {
			t.Errorf("embedded the test file %s", n)
		}
	}
}

func TestWriteExt(t *testing.T) {
	data := t.TempDir()
	d, err := WriteExt(data)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(d) != filepath.Join(data, "ext") || !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(filepath.Base(d)) {
		t.Errorf("extension directory %s", d)
	}
	for _, n := range []string{"index.ts", "system.md", "web/index.html", "web/vendor/marked.js"} {
		want, _ := extFiles.ReadFile("ext/" + n)
		got, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(n)))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s: %v", n, err)
		}
	}

	// The same content reuses the directory.
	os.WriteFile(filepath.Join(d, "index.ts"), []byte("kept"), 0o644)
	again, err := WriteExt(data)
	if err != nil || again != d {
		t.Fatalf("second WriteExt = %s, %v", again, err)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "index.ts")); string(b) != "kept" {
		t.Error("the second WriteExt wrote the directory again")
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "ext")); len(entries) != 1 {
		t.Errorf("ext holds %d entries, want 1", len(entries))
	}
}
