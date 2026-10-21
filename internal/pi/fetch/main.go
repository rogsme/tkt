// Command fetch downloads the pi tarballs that internal/pi/pi.lock pins into
// internal/pi/bundle, where a build with the bundled_pi tag embeds them, and
// checks each against the lock. Run it from the repository root:
//
//	go run ./internal/pi/fetch                  # this system's tarball
//	go run ./internal/pi/fetch linux/arm64 ...  # the named systems'
//	go run ./internal/pi/fetch -bump 1.4.0      # pin another pi release
//
// -bump rewrites pi.lock from that release's SHA256SUMS and downloads
// nothing; fetch the tarballs afterwards to check them.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/pi"
)

func main() {
	bump := flag.String("bump", "", "rewrite pi.lock to pin pi `VERSION`, from its release's SHA256SUMS")
	dir := flag.String("dir", filepath.Join("internal", "pi"), "the `DIR` that holds pi.lock and bundle/")
	flag.Parse()
	var err error
	if *bump != "" {
		err = bumpLock(*dir, strings.TrimPrefix(*bump, "v"))
	} else {
		err = fetch(*dir, flag.Args())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch: %v\n", err)
		os.Exit(1)
	}
}

var client = &http.Client{Timeout: 10 * time.Minute}

func readLock(dir string) (pi.Lock, error) {
	b, err := os.ReadFile(filepath.Join(dir, "pi.lock"))
	if err != nil {
		return pi.Lock{}, fmt.Errorf("%v; run fetch from the repository root", err)
	}
	return pi.ParseLock(b)
}

// fetch downloads each target's tarball, or the host's, unless the bundle
// directory already holds it with the locked hash.
func fetch(dir string, targets []string) error {
	l, err := readLock(dir)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		targets = []string{runtime.GOOS + "/" + runtime.GOARCH}
	}
	bundle := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		return err
	}
	for _, t := range targets {
		goos, goarch, _ := strings.Cut(t, "/")
		asset, ok := pi.AssetFor(goos, goarch)
		if !ok {
			return fmt.Errorf("pi has no tarball for %s; the systems are %s", t, strings.Join(pi.Targets, " "))
		}
		want, ok := l.Assets[asset]
		if !ok {
			return fmt.Errorf("pi.lock has no sha256 line for %s", asset)
		}
		dest := filepath.Join(bundle, asset)
		switch got, err := fileSum(dest); {
		case err == nil && got == want:
			continue
		case err == nil:
			fmt.Fprintf(os.Stderr, "fetch: %s does not match pi.lock; downloading it again\n", dest)
		}
		if err := download(l.AssetURL(asset), dest, want); err != nil {
			return err
		}
	}
	return nil
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// download writes url to dest only when its SHA-256 is want.
func download(url, dest, want string) error {
	fmt.Fprintf(os.Stderr, "fetch: downloading %s\n", url)
	f, err := os.CreateTemp(filepath.Dir(dest), ".fetch-*")
	if err != nil {
		return err
	}
	// After the rename this finds nothing to remove.
	defer os.Remove(f.Name())
	h := sha256.New()
	err = get(url, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s has sha256 %s, but pi.lock wants %s", url, got, want)
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), dest)
}

func get(url string, w io.Writer) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	return nil
}

// bumpLock pins version: it reads the hashes of the four tarballs from the
// release's SHA256SUMS and rewrites pi.lock with them.
func bumpLock(dir, version string) error {
	old, err := readLock(dir)
	if err != nil {
		return err
	}
	next := pi.Lock{Version: version, URL: old.URL, Assets: map[string]string{}}
	var sums bytes.Buffer
	if err := get(next.AssetURL("SHA256SUMS"), &sums); err != nil {
		return err
	}
	for _, t := range pi.Targets {
		goos, goarch, _ := strings.Cut(t, "/")
		asset, _ := pi.AssetFor(goos, goarch)
		sum, ok := checksum(sums.Bytes(), asset)
		if !ok {
			return fmt.Errorf("SHA256SUMS of pi %s has no line for %s", version, asset)
		}
		next.Assets[asset] = sum
	}
	// Parse what will be written, so a bad version never lands in the file.
	if _, err := pi.ParseLock(next.Format()); err != nil {
		return err
	}
	path := filepath.Join(dir, "pi.lock")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, next.Format(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	fmt.Fprintf(os.Stderr, "fetch: pi.lock pins pi %s; run go run ./internal/pi/fetch %s to check the tarballs\n", version, strings.Join(pi.Targets, " "))
	return nil
}

// checksum finds name in a sha256sum file.
func checksum(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && (f[1] == name || f[1] == "*"+name) && len(f[0]) == 64 {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}
