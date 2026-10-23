package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveCommandMovesThePathsTheShellUses(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tkt-acme-app-ACM-12-123")
	os.MkdirAll(filepath.Join(dir, "made"), 0o700)
	for _, f := range []string{"screenshot.png", "made", "data"} {
		os.WriteFile(filepath.Join(root, f), nil, 0o644)
	}
	for _, tc := range []struct{ in, want string }{
		// A new name lands in the ticket's directory.
		{"cat > /tmp/pr-body.md", "cat > DIR/pr-body.md"},
		{"gh pr create --body-file /tmp/pr-body.md", "gh pr create --body-file DIR/pr-body.md"},
		{"go test -coverprofile=/tmp/c.out ./...", "go test -coverprofile=DIR/c.out ./..."},
		{"mkdir -p /tmp/run && cp a /tmp/run/b", "mkdir -p DIR/run && cp a DIR/run/b"},
		{"echo $(cat /tmp/x)>/tmp/y", "echo $(cat DIR/x)>DIR/y"},
		{"PATH=/tmp/bin:$PATH make", "PATH=DIR/bin:$PATH make"},
		{"docker run -v /tmp/vol:/data img", "docker run -v DIR/vol:/data img"},
		// One the ticket's directory holds wins over the real /tmp.
		{"ls /tmp/made", "ls DIR/made"},
		// What /tmp holds already, and names other programs own, stay.
		{"open /tmp/screenshot.png", "open /tmp/screenshot.png"},
		{"docker run -v /tmp/data:/data img", "docker run -v /tmp/data:/data img"},
		{"ls /tmp/.X11-unix", "ls /tmp/.X11-unix"},
		{"cat /tmp/claude-1000/notes", "cat /tmp/claude-1000/notes"},
		{"cat /tmp/tkt-acme-app-ACM-13-9/x", "cat /tmp/tkt-acme-app-ACM-13-9/x"},
		{"cat " + dir + "/x", "cat " + dir + "/x"},
		// A path in quotes is as often a pattern or words as a place.
		{"rg \"/tmp/app.sock\" src", "rg \"/tmp/app.sock\" src"},
		{"sed -i 's|/tmp/app.sock|/run/app.sock|g' f", "sed -i 's|/tmp/app.sock|/run/app.sock|g' f"},
		{"git commit -m \"Move the socket from /tmp/app.sock\"", "git commit -m \"Move the socket from /tmp/app.sock\""},
		{"echo 'socket = \"/tmp/app.sock\"' >> config.toml", "echo 'socket = \"/tmp/app.sock\"' >> config.toml"},
		{"python3 -c \"open('/tmp/x.json','w')\"", "python3 -c \"open('/tmp/x.json','w')\""},
		// A bare /tmp is as often a word as a place; /var/tmp is no /tmp.
		{"cd /tmp && ls /tmp/", "cd /tmp && ls /tmp/"},
		{"cat /var/tmp/x ./tmp/y ~/tmp/z /tmpfoo", "cat /var/tmp/x ./tmp/y ~/tmp/z /tmpfoo"},
		{"scp a host:/tmp/x", "scp a host:/tmp/x"},
		{"ls /tmp/*.log /tmp/$USER", "ls /tmp/*.log /tmp/$USER"},
		// A name moves everywhere in the command, or nowhere: a protected
		// path keeps the others with it.
		{"cat /tmp/x && rg '/tmp/x' .", "cat /tmp/x && rg '/tmp/x' ."},
		{"gcc -o/tmp/hello main.c && /tmp/hello", "gcc -o/tmp/hello main.c && /tmp/hello"},
		{"cat > /tmp/t.go <<'EOF'\nos.WriteFile(\"/tmp/out\", nil, 0)\nEOF\ngo run /tmp/t.go && cat /tmp/out", "cat > DIR/t.go <<'EOF'\nos.WriteFile(\"/tmp/out\", nil, 0)\nEOF\ngo run DIR/t.go && cat /tmp/out"},
		{"cat <<-END > /tmp/a\n\t/tmp/b\n\tEND\ncat /tmp/c", "cat <<-END > DIR/a\n\t/tmp/b\n\tEND\ncat DIR/c"},
		{"bash <<< \"cat /tmp/w\" && cat /tmp/w", "bash <<< \"cat /tmp/w\" && cat /tmp/w"},
		// A here-string, a shift and a << in quotes start no here-document.
		{"echo data > /tmp/x\ntr a-z A-Z <<< \"abc\"\ncat /tmp/x", "echo data > DIR/x\ntr a-z A-Z <<< \"abc\"\ncat DIR/x"},
		{"echo $((1 << 2)) > /tmp/y", "echo $((1 << 2)) > DIR/y"},
		{"echo 'Use << for here-documents' && cat /tmp/z", "echo 'Use << for here-documents' && cat DIR/z"},
		// A comment counts for nothing.
		{"cat /tmp/n # not /tmp/n in 'quotes'", "cat DIR/n # not /tmp/n in 'quotes'"},
	} {
		want := strings.ReplaceAll(tc.want, "DIR", dir)
		if got := moveCommand(tc.in, dir, root); got != want {
			t.Errorf("moveCommand(%q)\n got %q\nwant %q", tc.in, got, want)
		}
	}
}

func TestMovePathMovesOnePath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tkt-acme-app-ACM-12-123")
	os.WriteFile(filepath.Join(root, "real.md"), nil, 0o644)
	for in, want := range map[string]string{
		"/tmp/notes.md":          dir + "/notes.md",
		"/tmp/run/out.txt":       dir + "/run/out.txt",
		"/tmp/real.md":           "/tmp/real.md",
		"/tmp/":                  "/tmp/",
		"/var/tmp/x":             "/var/tmp/x",
		"/tmp/tkt-other-ACM-1/x": "/tmp/tkt-other-ACM-1/x",
	} {
		if got := movePath(in, dir, root); got != want {
			t.Errorf("movePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMoveInputMovesPathsNotContent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tkt-acme-app-ACM-12-123")
	in := map[string]any{"path": "/tmp/notes.md", "content": "see /tmp/notes.md", "limit": 10.0}
	out := moveInput("write", in, dir, root)
	if out["path"] != dir+"/notes.md" || out["content"] != "see /tmp/notes.md" || out["limit"] != 10.0 {
		t.Errorf("moveInput: %v", out)
	}
	if in["path"] != "/tmp/notes.md" {
		t.Errorf("moveInput changed its argument: %v", in)
	}
	if out := moveInput("shell", map[string]any{"command": "ls", "workdir": "/home/acme"}, dir, root); out != nil {
		t.Errorf("nothing moved, yet: %v", out)
	}
	if out := moveInput("shell", map[string]any{"command": "cat /tmp/x", "workdir": "/tmp/w"}, dir, root); out["command"] != "cat "+dir+"/x" || out["workdir"] != dir+"/w" {
		t.Errorf("a shell call: %v", out)
	}
	// Another tool may run its command, or name its path, elsewhere.
	if out := moveInput("acme_ssh", map[string]any{"command": "cat /tmp/x", "path": "/tmp/y"}, dir, root); out != nil {
		t.Errorf("an MCP tool's input moved: %v", out)
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
