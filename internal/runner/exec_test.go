package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// StdoutFile captures the whole output, past what one pipe buffer holds.
func TestExecStdoutFile(t *testing.T) {
	for _, file := range []bool{false, true} {
		r := Exec{}.Run(context.Background(), Cmd{Argv: []string{"sh", "-c", "head -c 300000 /dev/zero; echo err >&2"}, StdoutFile: file})
		if !r.OK() || len(r.Stdout) != 300000 || string(r.Stderr) != "err\n" {
			t.Errorf("StdoutFile %v: code %d, %d bytes of stdout, stderr %q", file, r.Code, len(r.Stdout), r.Stderr)
		}
	}
}

// A spawned command runs on its own, with its environment and directory, and
// its output in the log.
func TestExecSpawn(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "spawn.log")
	err := Exec{}.Spawn(context.Background(), Cmd{Argv: []string{"sh", "-c", `echo "in $PWD as $SPAWNED"`}}, []string{"SPAWNED=yes"}, dir, log)
	if err != nil {
		t.Fatal(err)
	}
	var out string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, _ := os.ReadFile(log); strings.Contains(string(b), "as ") {
			out = string(b)
			break
		}
	}
	// sh reads its directory back from the system, which resolves links,
	// as macOS has under its temporary directory.
	real, _ := filepath.EvalSymlinks(dir)
	if out != "in "+dir+" as yes\n" && out != "in "+real+" as yes\n" {
		t.Fatalf("log: %q", out)
	}
}
