package fake

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WT creates and removes real git worktrees under $FAKE_WT_ROOT and prints
// worktrunk's JSON: schema 1, or schema 2 with FAKE_WT_SCHEMA=2.
func WT() {
	logCall("wt")
	args := os.Args[1:]
	dir, _ := os.Getwd()
	for len(args) >= 2 && args[0] == "-C" {
		dir, args = args[1], args[2:]
	}
	if len(args) == 0 {
		die(2, "fake wt: no command")
	}
	if args[0] == "--version" {
		fmt.Println("wt " + envOr("FAKE_WT_VERSION", "v0.68.0"))
		os.Exit(0)
	}
	cmd, args := args[0], args[1:]
	root := os.Getenv("FAKE_WT_ROOT")
	switch cmd {
	case "list":
		items := worktrees(dir)
		if os.Getenv("FAKE_WT_SCHEMA") == "2" {
			var v2 []map[string]any
			for _, w := range items {
				v2 = append(v2, map[string]any{"branch": w["branch"], "worktree": map[string]any{"path": w["path"], "branch_mismatch": false}})
			}
			printJSON(map[string]any{"schema": 2, "repo": map[string]any{}, "items": v2})
		} else {
			for _, w := range items {
				w["kind"] = "worktree"
				w["worktree"] = map[string]any{"detached": false}
			}
			printJSON(items)
		}
	case "switch":
		create, branch, base := false, "", "main"
		for i := 0; i < len(args); i++ {
			switch a := args[i]; {
			case a == "--create":
				create = true
			case a == "--base" && i+1 < len(args):
				base = args[i+1]
				i++
			case a == "--format":
				i++
			case strings.HasPrefix(a, "--"):
			default:
				branch = a
			}
		}
		path := filepath.Join(root, branch)
		if _, err := os.Stat(path); err == nil {
			printJSON(map[string]any{"action": "existing", "branch": branch, "path": path})
			os.Exit(0)
		}
		var err error
		if create {
			_, err = realGit("-C", dir, "worktree", "add", "-q", "-b", branch, path, base)
		} else {
			_, err = realGit("-C", dir, "worktree", "add", "-q", path, branch)
		}
		if err != nil {
			die(1, "fake wt: git worktree add failed: %v", err)
		}
		if os.Getenv("FAKE_WT_SWITCH_FAIL") == "1" {
			die(1, "pre-start command failed")
		}
		printJSON(map[string]any{"action": "created", "branch": branch, "path": path, "created_branch": create})
	case "remove":
		branch := args[0]
		if _, err := realGit("-C", dir, "worktree", "remove", filepath.Join(root, branch)); err != nil {
			die(1, "fake wt: git worktree remove failed: %v", err)
		}
		realGit("-C", dir, "branch", "-d", branch)
		fmt.Println("{}")
	case "hook":
		fmt.Println(os.Getenv("FAKE_WT_HOOKS"))
	default:
		die(2, "fake wt: unsupported: %s", cmd)
	}
	os.Exit(0)
}

func worktrees(dir string) []map[string]any {
	out, err := realGit("-C", dir, "worktree", "list", "--porcelain")
	if err != nil {
		die(1, "fake wt: git worktree list failed: %v", err)
	}
	items := []map[string]any{}
	var cur map[string]any
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur = map[string]any{"path": strings.TrimPrefix(line, "worktree "), "branch": nil}
			items = append(items, cur)
		case strings.HasPrefix(line, "branch refs/heads/") && cur != nil:
			cur["branch"] = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	return items
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
