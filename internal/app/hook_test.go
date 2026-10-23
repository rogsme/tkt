package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveTmpMovesNewPathsOnly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tkt-acme-app-ACM-12-123")
	os.MkdirAll(filepath.Join(dir, "made"), 0o700)
	os.WriteFile(filepath.Join(root, "screenshot.png"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "made"), nil, 0o644)
	for _, tc := range []struct{ in, want string }{
		// A new name lands in the ticket's directory.
		{"cat > /tmp/pr-body.md", "cat > DIR/pr-body.md"},
		{"gh pr create --body-file /tmp/pr-body.md", "gh pr create --body-file DIR/pr-body.md"},
		{"python3 -c \"open('/tmp/x.json','w')\"", "python3 -c \"open('DIR/x.json','w')\""},
		{"go test -coverprofile=/tmp/c.out ./...", "go test -coverprofile=DIR/c.out ./..."},
		{"mkdir -p /tmp/run && cp a /tmp/run/b", "mkdir -p DIR/run && cp a DIR/run/b"},
		{"echo $(cat /tmp/x)", "echo $(cat DIR/x)"},
		// One the ticket's directory holds wins over the real /tmp.
		{"ls /tmp/made", "ls DIR/made"},
		// What /tmp holds already, and names other programs own, stay.
		{"open /tmp/screenshot.png", "open /tmp/screenshot.png"},
		{"ls /tmp/.X11-unix", "ls /tmp/.X11-unix"},
		{"cat /tmp/claude-1000/notes", "cat /tmp/claude-1000/notes"},
		{"cat /tmp/tkt-acme-app-ACM-13-9/x", "cat /tmp/tkt-acme-app-ACM-13-9/x"},
		{"cat " + dir + "/x", "cat " + dir + "/x"},
		// A bare /tmp is as often a word as a place.
		{"git commit -m 'Write to /tmp less'", "git commit -m 'Write to /tmp less'"},
		{"grep -rn '/tmp/' src", "grep -rn '/tmp/' src"},
		{"cd /tmp", "cd /tmp"},
		// Not a path under /tmp, or on another host.
		{"cat /var/tmp/x /tmpfoo", "cat /var/tmp/x /tmpfoo"},
		{"scp a host:/tmp/x", "scp a host:/tmp/x"},
		{"ls /tmp/*.log /tmp/$USER", "ls /tmp/*.log /tmp/$USER"},
		// Here-document bodies stay; the lines around them move.
		{"cat > /tmp/t.go <<'EOF'\nos.WriteFile(\"/tmp/x\", nil, 0)\nEOF\ngo run /tmp/t.go", "cat > DIR/t.go <<'EOF'\nos.WriteFile(\"/tmp/x\", nil, 0)\nEOF\ngo run DIR/t.go"},
		{"cat <<-END > /tmp/a\n\t/tmp/b\n\tEND", "cat <<-END > DIR/a\n\t/tmp/b\n\tEND"},
		{"tr a b <<< /tmp/c", "tr a b <<< DIR/c"},
	} {
		want := strings.ReplaceAll(tc.want, "DIR", dir)
		if got := moveTmp(tc.in, dir, root, true); got != want {
			t.Errorf("moveTmp(%q)\n got %q\nwant %q", tc.in, got, want)
		}
	}
	// A file path is one path, with no shell around it.
	if got := moveTmp("/tmp/notes.md", dir, root, false); got != dir+"/notes.md" {
		t.Errorf("a path: %q", got)
	}
	if got := moveTmp("/tmp/a <<EOF\n/tmp/b", dir, root, false); got != dir+"/a <<EOF\n"+dir+"/b" {
		t.Errorf("a path knows no here-documents: %q", got)
	}
}

func TestMoveInputMovesPathsNotContent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tkt-acme-app-ACM-12-123")
	in := map[string]any{"path": "/tmp/notes.md", "content": "see /tmp/notes.md", "limit": 10.0}
	out := moveInput(in, dir, root)
	if out["path"] != dir+"/notes.md" || out["content"] != "see /tmp/notes.md" || out["limit"] != 10.0 {
		t.Errorf("moveInput: %v", out)
	}
	if in["path"] != "/tmp/notes.md" {
		t.Errorf("moveInput changed its argument: %v", in)
	}
	if out := moveInput(map[string]any{"command": "ls", "workdir": "/home/acme"}, dir, root); out != nil {
		t.Errorf("nothing moved, yet: %v", out)
	}
	if out := moveInput(map[string]any{"command": "cat /tmp/x", "workdir": "/tmp/w"}, dir, root); out["command"] != "cat "+dir+"/x" || out["workdir"] != dir+"/w" {
		t.Errorf("a shell call: %v", out)
	}
}

func TestTicketAtReadsTheWorktreesState(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "wt", "ACM-12")
	gitdir := filepath.Join(base, "app", ".git", "worktrees", "ACM-12")
	os.MkdirAll(filepath.Join(wt, "src"), 0o755)
	os.MkdirAll(filepath.Join(gitdir, "tkt"), 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644)
	os.WriteFile(filepath.Join(gitdir, "tkt", "state"), []byte("ticket=ACM-12\ntmpdir=/tmp/tkt-acme-app-ACM-12-123\n"), 0o644)
	if id, tmp := ticketAt(filepath.Join(wt, "src")); id != "ACM-12" || tmp != "/tmp/tkt-acme-app-ACM-12-123" {
		t.Errorf("ticketAt = %q, %q", id, tmp)
	}
	os.MkdirAll(filepath.Join(base, "app", "src"), 0o755)
	if id, _ := ticketAt(filepath.Join(base, "app", "src")); id != "" {
		t.Errorf("the primary worktree holds %q", id)
	}
	if id, _ := ticketAt(base); id != "" {
		t.Errorf("a directory outside git holds %q", id)
	}
	if !ownTmp("/tmp/tkt-acme-app-ACM-12-123", "ACM-12") || ownTmp("/tmp/tkt-acme-app-ACM-13-1", "ACM-12") || ownTmp("/etc", "ACM-12") {
		t.Error("ownTmp")
	}
}
