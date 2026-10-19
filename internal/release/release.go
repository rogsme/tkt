// Package release reads tkt's releases on git.rogs.me: the latest tag from
// the Gitea API, and the binaries and SHA256SUMS that install.sh also
// downloads.
package release

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Where the releases live. TKT_RELEASES, which install.sh reads too, and
// TKT_RELEASES_API override them.
const (
	DefaultReleases = "https://git.rogs.me/rogs/tkt/releases/download"
	DefaultAPI      = "https://git.rogs.me/api/v1/repos/rogs/tkt"
)

// Client downloads from the release host.
type Client struct {
	HTTP     *http.Client
	Releases string
	API      string
}

// New returns a client for the given bases, or the defaults when they are
// empty. It also reads file:// URLs, which the tests use instead of a server.
func New(releases, api string) Client {
	if releases == "" {
		releases = DefaultReleases
	}
	if api == "" {
		api = DefaultAPI
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	return Client{
		HTTP:     &http.Client{Transport: t, Timeout: 5 * time.Minute},
		Releases: strings.TrimRight(releases, "/"),
		API:      strings.TrimRight(api, "/"),
	}
}

// Latest returns the tag of the newest full release. Like the latest
// download URL, it skips prereleases.
func (c Client) Latest(ctx context.Context) (string, error) {
	var b bytes.Buffer
	if err := c.get(ctx, c.API+"/releases/latest", &b); err != nil {
		return "", err
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(b.Bytes(), &r); err != nil || r.TagName == "" {
		return "", fmt.Errorf("%s/releases/latest names no tag", c.API)
	}
	return r.TagName, nil
}

// Fetch writes the release asset name of tag to w.
func (c Client) Fetch(ctx context.Context, tag, name string, w io.Writer) error {
	return c.get(ctx, c.Releases+"/"+tag+"/"+name, w)
}

func (c Client) get(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
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

// Asset names the release binary for a system, as the release workflow
// builds it. It reports false for a system without one.
func Asset(goos, goarch string) (string, bool) {
	if (goos != "linux" && goos != "darwin") || (goarch != "amd64" && goarch != "arm64") {
		return "", false
	}
	return "tkt_" + goos + "_" + goarch, true
}

// Checksum returns the SHA-256 that a SHA256SUMS file gives for name, in
// sha256sum's text or binary form.
func Checksum(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && (f[1] == name || f[1] == "*"+name) {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// version is a parsed vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD].
type version struct {
	core [3]int
	pre  []string
}

func parse(v string) (version, bool) {
	var out version
	s, ok := strings.CutPrefix(v, "v")
	if !ok {
		return out, false
	}
	s, _, _ = strings.Cut(s, "+")
	s, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" {
			return out, false
		}
		out.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return out, false
		}
		out.pre = strings.Split(pre, ".")
	}
	return out, true
}

// Valid reports whether v is a release tag such as v0.3.0 or v0.3.0-rc.1.
func Valid(v string) bool {
	_, ok := parse(v)
	return ok
}

// Compare orders two tags by semantic versioning: -1, 0 or 1. It reports
// false when either is not a tag, such as a development build's "dev".
func Compare(a, b string) (int, bool) {
	x, ok1 := parse(a)
	y, ok2 := parse(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	for i := range x.core {
		if x.core[i] != y.core[i] {
			return sign(x.core[i] - y.core[i]), true
		}
	}
	// A release sorts after its prereleases.
	switch {
	case len(x.pre) == 0 && len(y.pre) == 0:
		return 0, true
	case len(x.pre) == 0:
		return 1, true
	case len(y.pre) == 0:
		return -1, true
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		if c := comparePre(x.pre[i], y.pre[i]); c != 0 {
			return c, true
		}
	}
	return sign(len(x.pre) - len(y.pre)), true
}

// comparePre orders two prerelease identifiers: numbers by value and
// before words, words by bytes.
func comparePre(a, b string) int {
	m, errA := strconv.Atoi(a)
	n, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return sign(m - n)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
