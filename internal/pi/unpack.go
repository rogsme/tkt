package pi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
)

const (
	// binPath is the pi binary inside a release tarball. A Bun build finds
	// its package files next to the binary, so the tree stays as it is.
	binPath = "pi/pi"
	// completeMarker is written last, so a directory with it holds a whole
	// copy.
	completeMarker = ".complete"
	// tmpInfix marks a directory that a run is still writing.
	tmpInfix = ".tmp-"
	// staleTmp is how old a temporary directory must be before another run
	// removes it: an unpack takes seconds, so an older one was interrupted.
	staleTmp = time.Hour
)

// versionDir matches the directories Unpack makes: VERSION-SHA12.
var versionDir = regexp.MustCompile(`^[0-9A-Za-z.+_-]+-[0-9a-f]{12}$`)

// Unpack writes a pi release tarball under dataDir/pi/VERSION-SHA12 and
// returns the path of the pi binary in it. It checks the tarball against
// the lock first. A directory an earlier run finished is reused, and the
// directories of other versions are removed. note, when set, hears about
// an unpack before it starts, since it takes a few seconds.
func Unpack(dataDir string, l Lock, asset string, tarball []byte, note func(format string, args ...any)) (string, error) {
	sum := sha256.Sum256(tarball)
	got := hex.EncodeToString(sum[:])
	if want := l.Assets[asset]; got != want {
		return "", unpackErr(dataDir, "the bundled %s does not match pi.lock: sha256 %s, want %s", asset, got, want)
	}
	root := filepath.Join(dataDir, "pi")
	name := l.Version + "-" + got[:12]
	final := filepath.Join(root, name)
	bin := filepath.Join(final, filepath.FromSlash(binPath))
	if complete(final) {
		prune(root, name)
		return bin, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", unpackErr(dataDir, "cannot create %s: %v", root, err)
	}
	tmp, err := os.MkdirTemp(root, name+tmpInfix)
	if err != nil {
		return "", unpackErr(dataDir, "cannot create a directory in %s: %v", root, err)
	}
	if note != nil {
		note("unpacking pi %s into %s", l.Version, final)
	}
	if err := extract(tmp, tarball); err != nil {
		os.RemoveAll(tmp)
		return "", unpackErr(dataDir, "cannot unpack %s: %v", asset, err)
	}
	if fi, err := os.Stat(filepath.Join(tmp, filepath.FromSlash(binPath))); err != nil || !fi.Mode().IsRegular() {
		os.RemoveAll(tmp)
		return "", unpackErr(dataDir, "%s has no %s", asset, binPath)
	}
	if err := finish(tmp, final); err != nil {
		return "", unpackErr(dataDir, "cannot write %s: %v", final, err)
	}
	prune(root, name)
	return bin, nil
}

func unpackErr(dataDir, format string, args ...any) error {
	return apperr.New(apperr.PiUnpackFailed, format, args...).
		WithHint("free some space or fix the permissions under %s, then retry; or set TKT_PI to a pi binary", dataDir)
}

func complete(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, completeMarker))
	return err == nil
}

// finish marks tmp complete and renames it to final. When another run
// finished final first, its copy wins and tmp goes.
func finish(tmp, final string) error {
	if err := os.WriteFile(filepath.Join(tmp, completeMarker), nil, 0o644); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	err := os.Rename(tmp, final)
	if err == nil {
		return nil
	}
	if complete(final) {
		os.RemoveAll(tmp)
		return nil
	}
	// No run renames an unfinished copy, so a final directory without the
	// marker is a leftover; replace it.
	if rmErr := os.RemoveAll(final); rmErr != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.RemoveAll(tmp)
		if complete(final) {
			return nil
		}
		return err
	}
	return nil
}

// prune removes other versions' directories, and temporary ones that an
// interrupted run left behind. It leaves anything else alone.
func prune(root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		switch {
		case n == keep || !e.IsDir():
		case strings.Contains(n, tmpInfix):
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > staleTmp {
				os.RemoveAll(filepath.Join(root, n))
			}
		case versionDir.MatchString(n):
			os.RemoveAll(filepath.Join(root, n))
		}
	}
}

// extract writes a gzipped tarball into dst. It refuses entries that would
// land outside dst: absolute paths, paths that climb out with "..", links
// that point out, and paths that pass through a symlink.
func extract(dst string, tarball []byte) error {
	zr, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := entryPath(h.Name)
		if err != nil {
			return err
		}
		if name == "." {
			continue
		}
		if err := noSymlinkIn(dst, name); err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if h.Typeflag != tar.TypeDir {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
		}
		mode := h.FileInfo().Mode().Perm()
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			// The owner keeps write access, or later entries could not land.
			if err := os.Chmod(target, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if path.IsAbs(h.Linkname) {
				return fmt.Errorf("%s links to the absolute path %s", h.Name, h.Linkname)
			}
			if p := path.Join(path.Dir(name), h.Linkname); p == ".." || strings.HasPrefix(p, "../") {
				return fmt.Errorf("%s links to %s, outside the tarball", h.Name, h.Linkname)
			}
			// The cleaned target reads the same through any symlink, since a
			// ".." after a symlink would otherwise climb from its target.
			if err := os.Symlink(path.Clean(h.Linkname), target); err != nil {
				return err
			}
		case tar.TypeLink:
			src, err := entryPath(h.Linkname)
			if err != nil {
				return err
			}
			if err := noSymlinkIn(dst, src); err != nil {
				return err
			}
			old := filepath.Join(dst, filepath.FromSlash(src))
			if fi, err := os.Lstat(old); err != nil || !fi.Mode().IsRegular() {
				return fmt.Errorf("%s links to %s, which is not a file in the tarball", h.Name, h.Linkname)
			}
			if err := os.Link(old, target); err != nil {
				return err
			}
		default:
			// Devices, FIFOs and the like: pi needs none.
		}
	}
}

// entryPath cleans a tarball path and refuses one that is absolute or
// climbs out.
func entryPath(name string) (string, error) {
	if path.IsAbs(name) || filepath.IsAbs(name) {
		return "", fmt.Errorf("%s is an absolute path", name)
	}
	p := path.Clean(name)
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("%s climbs out of the tarball", name)
	}
	return p, nil
}

// noSymlinkIn refuses a path under dst that passes through, or is, a
// symlink already written, so no entry lands outside dst through one.
func noSymlinkIn(dst, name string) error {
	cur := dst
	for _, part := range strings.Split(name, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s passes through a symlink", name)
		}
	}
	return nil
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// OpenFile's mode passes through the umask; the tarball's must not.
	return os.Chmod(target, mode)
}
