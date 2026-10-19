package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/schema.golden.json")

func testRoot() *globals {
	g := &globals{io: IO{Getenv: func(string) string { return "" }}, version: "test"}
	g.root = newRoot(g, false)
	return g
}

// The schema is part of tkt's interface: a change to it shows in review as a
// change to the golden file. Run go test ./internal/cli -update to accept one.
func TestSchemaGolden(t *testing.T) {
	g := testRoot()
	var b bytes.Buffer
	if err := writeJSON(&b, buildSchema(g.root, "test")); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "schema.golden.json")
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(golden, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run go test ./internal/cli -update", err)
	}
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("tkt schema changed; run go test ./internal/cli -update and review the diff")
	}
}

func TestSchemaCoversEveryCommand(t *testing.T) {
	s := buildSchema(testRoot().root, "test")
	seen := map[string]bool{}
	for _, c := range s.Commands {
		seen[c.Path] = true
		if c.Short == "" {
			t.Errorf("%s has no short help", c.Path)
		}
	}
	for path := range resultTypes {
		if !seen[path] {
			t.Errorf("resultTypes names %s, which is not a command", path)
		}
		if s := resultOf(path); len(s) == 0 {
			t.Errorf("%s has no result fields", path)
		}
	}
	for _, want := range []string{"start", "run", "resume", "done", "ls", "wait", "doctor", "schema"} {
		if !seen[want] {
			t.Errorf("the schema lacks %s", want)
		}
	}
}

func TestArgsOf(t *testing.T) {
	got := argsOf("run ID [STAGE]")
	if len(got) != 2 || !got[0].Required || got[1].Required || got[1].Name != "STAGE" {
		t.Errorf("run: %+v", got)
	}
	got = argsOf("done [ID...]")
	if len(got) != 1 || got[0].Required || !got[0].Variadic || got[0].Name != "ID" {
		t.Errorf("done: %+v", got)
	}
}

func TestEveryCommandHasHelpAndExamples(t *testing.T) {
	for _, c := range buildSchema(testRoot().root, "test").Commands {
		if strings.HasPrefix(c.Path, "completion") {
			continue
		}
		if c.Long == "" {
			t.Errorf("%s has no long help", c.Path)
		}
		if c.Example == "" {
			t.Errorf("%s has no examples", c.Path)
		}
	}
}

func TestHelpIsPlainASCII(t *testing.T) {
	entries, _ := helpFiles.ReadDir("help")
	for _, e := range entries {
		text := helpText(strings.TrimSuffix(e.Name(), ".txt"))
		for i, r := range text {
			if r > 0x7e || (r < 0x20 && r != '\n') {
				t.Errorf("%s: character %U at byte %d", e.Name(), r, i)
				break
			}
		}
	}
}
