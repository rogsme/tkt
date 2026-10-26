package gitx

import (
	"context"
	"strconv"
	"strings"
)

// File is one file a diff changes, with its hunks. OldPath is set for a
// rename. Status is added, deleted, renamed or modified.
type File struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary"`
	Hunks   []Hunk `json:"hunks"`
}

// Hunk is one hunk of a file's diff: its @@ line, and its lines as git
// prints them, each starting with " ", "+", "-" or "\".
type Hunk struct {
	Header string   `json:"header"`
	Lines  []string `json:"lines"`
}

// Diff returns the diff a range names, such as origin/main...HEAD for what
// a branch changes since it left its base. ok is false when git cannot
// tell.
func (g Git) Diff(ctx context.Context, path, rng string) (string, bool) {
	r := g.run(ctx, 0, "-C", path, "diff", "--no-ext-diff", "--no-textconv", "--no-color", rng)
	return r.Out(), r.OK()
}

// ParseDiff reads git's unified diff into its files and hunks.
func ParseDiff(text string) []File {
	files := []File{}
	var f *File
	var h *Hunk
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			files = append(files, File{Path: gitHeaderPath(line), Status: "modified", Hunks: []Hunk{}})
			f, h = &files[len(files)-1], nil
		case f == nil:
		case h != nil && line != "" && strings.ContainsRune(" +-\\", rune(line[0])):
			h.Lines = append(h.Lines, line)
			switch line[0] {
			case '+':
				f.Added++
			case '-':
				f.Removed++
			}
		case strings.HasPrefix(line, "@@ "):
			f.Hunks = append(f.Hunks, Hunk{Header: line, Lines: []string{}})
			h = &f.Hunks[len(f.Hunks)-1]
		case strings.HasPrefix(line, "new file mode"):
			f.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			f.Status = "deleted"
		case strings.HasPrefix(line, "rename from "):
			f.Status, f.OldPath = "renamed", unquote(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			f.Path = unquote(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "Binary files "):
			f.Binary = true
		case strings.HasPrefix(line, "+++ "):
			if p := strings.TrimPrefix(line, "+++ "); p != "/dev/null" {
				f.Path = strings.TrimPrefix(unquote(p), "b/")
			}
		case strings.HasPrefix(line, "--- "):
			if p := strings.TrimPrefix(line, "--- "); p != "/dev/null" && f.Status == "deleted" {
				f.Path = strings.TrimPrefix(unquote(p), "a/")
			}
		}
	}
	return files
}

// gitHeaderPath reads the path from a "diff --git a/P b/P" line, which
// names the same path twice unless the file moved. A binary file's diff
// has no other line that names it.
func gitHeaderPath(line string) string {
	s := strings.TrimPrefix(line, "diff --git ")
	if strings.HasPrefix(s, `"`) {
		if i := strings.Index(s[1:], `" `); i >= 0 {
			return strings.TrimPrefix(unquote(s[:i+2]), "a/")
		}
	}
	if half := (len(s) - 1) / 2; len(s)%2 == 1 && half > 2 && s[half] == ' ' && s[:2] == "a/" && s[half+1:half+3] == "b/" && s[2:half] == s[half+3:] {
		return s[2:half]
	}
	return s
}

// unquote reads a path git quoted, as it does one with unusual characters.
func unquote(p string) string {
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}
