package pi

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// extFiles is tkt's pi extension and the system prompt it starts pi with.
// Tests under ext/test stay out.
//
//go:embed ext/*.ts ext/system.md
var extFiles embed.FS

// WriteExt writes tkt's pi extension under dataDir/ext/SHA12, named after
// its content, and returns that directory. A directory with the same
// content is reused, so a tkt upgrade gets its own copy.
func WriteExt(dataDir string) (string, error) {
	names, sum, err := extContent()
	if err != nil {
		return "", unpackErr(dataDir, "cannot read the embedded extension: %v", err)
	}
	root := filepath.Join(dataDir, "ext")
	final := filepath.Join(root, sum[:12])
	if complete(final) {
		return final, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", unpackErr(dataDir, "cannot create %s: %v", root, err)
	}
	tmp, err := os.MkdirTemp(root, sum[:12]+tmpInfix)
	if err != nil {
		return "", unpackErr(dataDir, "cannot create a directory in %s: %v", root, err)
	}
	for _, n := range names {
		b, _ := extFiles.ReadFile(n)
		if err := os.WriteFile(filepath.Join(tmp, path.Base(n)), b, 0o644); err != nil {
			os.RemoveAll(tmp)
			return "", unpackErr(dataDir, "cannot write the extension: %v", err)
		}
	}
	if err := finish(tmp, final); err != nil {
		return "", unpackErr(dataDir, "cannot write %s: %v", final, err)
	}
	// Only stale temporary directories go: a running pi of another tkt
	// version may still read its own copy.
	prune(root, sum[:12])
	return final, nil
}

// extContent lists the embedded files and hashes their names and content.
func extContent() ([]string, string, error) {
	var names []string
	h := sha256.New()
	err := fs.WalkDir(extFiles, "ext", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := extFiles.ReadFile(p)
		if err != nil {
			return err
		}
		names = append(names, p)
		h.Write([]byte(p + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
		return nil
	})
	return names, hex.EncodeToString(h.Sum(nil)), err
}
