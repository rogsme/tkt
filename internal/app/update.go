package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/release"
	"gitlab.com/rogs/tkt/internal/runner"
)

// UpdateOpts are tkt update's arguments.
type UpdateOpts struct {
	// Version is the tag to install; empty means the latest full release.
	Version string
	Check   bool
	Force   bool
}

// UpdateResult says what tkt update found and did.
type UpdateResult struct {
	From string `json:"from"`
	To   string `json:"to"`
	Path string `json:"path"`
	// Available is true when To would replace From: it is newer, it was
	// asked for by name, or From is a development build.
	Available bool `json:"available"`
	// Dev is true when From has no release version, so only --force
	// replaces it.
	Dev     bool `json:"dev"`
	Updated bool `json:"updated"`
	// Next is the command that installs To, when it is available and was
	// not installed.
	Next string `json:"next,omitempty"`
}

func (r *UpdateResult) Text(w io.Writer) {
	switch {
	case r.Updated:
		fmt.Fprintf(w, "updated %s from %s to %s\n", r.Path, r.From, r.To)
	case r.Available:
		fmt.Fprintf(w, "%s is available (this tkt is %s); run %s\n", r.To, r.From, r.Next)
	default:
		fmt.Fprintf(w, "tkt %s is up to date\n", r.From)
	}
}

// Update replaces the running tkt with a release: it downloads the binary
// for this system, checks it against the release's SHA256SUMS, runs it once,
// then renames it over the old one.
func (a *App) Update(ctx context.Context, o UpdateOpts) (*UpdateResult, error) {
	asset, ok := release.Asset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return nil, apperr.New(apperr.UpdateUnsupported, "there is no tkt release for %s/%s", runtime.GOOS, runtime.GOARCH).
			WithHint("go install gitlab.com/rogs/tkt@latest")
	}
	if o.Version != "" && !release.Valid(o.Version) {
		return nil, apperr.New(apperr.Usage, "%q is not a release tag such as v0.3.0", o.Version)
	}
	rel := release.New(a.Getenv("TKT_RELEASES"), a.Getenv("TKT_RELEASES_API"))
	res := &UpdateResult{From: a.Version, To: o.Version}
	if res.To == "" {
		tag, err := rel.Latest(ctx)
		if err != nil {
			return nil, releaseErr(ctx, err, "cannot read the latest tkt release: %v", err)
		}
		res.To = tag
	}
	path, err := updateTarget(a.Getenv)
	if err != nil {
		return nil, apperr.New(apperr.Internal, "cannot find the running tkt: %v", err).Wrap(err)
	}
	res.Path = path

	cmp, known := release.Compare(res.To, res.From)
	res.Dev = !known
	switch {
	case !known:
		res.Available = true
	case o.Version != "":
		res.Available = cmp != 0
	default:
		res.Available = cmp > 0
	}
	if res.Available {
		res.Next = "tkt update"
		if o.Version != "" {
			res.Next += " " + o.Version
		}
		if !known {
			res.Next += " --force"
		}
	}
	if o.Check || (!res.Available && !o.Force) {
		return res, nil
	}
	if !known && !o.Force {
		return nil, apperr.New(apperr.UpdateDevBuild, "this tkt is a development build (%s), so tkt update does not replace it", res.From).
			WithHint("%s, or rebuild it from source", res.Next).With("path", path)
	}
	if err := a.install(ctx, rel, res.To, asset, path); err != nil {
		return nil, err
	}
	res.Updated, res.Next = true, ""
	return res, nil
}

// updateTarget is the file tkt update replaces: the running binary, through
// any symlinks, so a link keeps pointing at it. The tests set
// TKT_UPDATE_TARGET, because their tkt is the shared test binary.
func updateTarget(getenv func(string) string) (string, error) {
	p := getenv("TKT_UPDATE_TARGET")
	if p == "" {
		var err error
		if p, err = os.Executable(); err != nil {
			return "", err
		}
	}
	return filepath.EvalSymlinks(p)
}

// install downloads tag's asset next to target and renames it over target
// once it matches SHA256SUMS and runs. On any failure target stays as it was.
func (a *App) install(ctx context.Context, rel release.Client, tag, asset, target string) error {
	a.UI.Info("downloading %s (%s)", asset, tag)
	var sums strings.Builder
	if err := rel.Fetch(ctx, tag, "SHA256SUMS", &sums); err != nil {
		return releaseErr(ctx, err, "cannot download SHA256SUMS of %s: %v", tag, err)
	}
	want, ok := release.Checksum([]byte(sums.String()), asset)
	if !ok {
		return apperr.New(apperr.ReleaseFailed, "SHA256SUMS of %s has no line for %s", tag, asset)
	}

	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".tkt.update.*")
	if err != nil {
		return notWritable(dir, err)
	}
	tmp := f.Name()
	// After the rename this finds nothing to remove.
	defer os.Remove(tmp)
	h := sha256.New()
	err = rel.Fetch(ctx, tag, asset, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil && cerr != nil {
		return notWritable(dir, cerr)
	}
	if err != nil {
		return releaseErr(ctx, err, "cannot download %s of %s: %v", asset, tag, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return apperr.New(apperr.UpdateChecksum, "the checksum of %s %s does not match SHA256SUMS; nothing was replaced", asset, tag).
			With("want", want).With("got", got)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return notWritable(dir, err)
	}

	// Run the new binary before it replaces the old one: a binary that cannot
	// run here must not take the place of one that can.
	r := a.R.Run(ctx, runner.Cmd{Argv: []string{tmp, "--version"}, Timeout: 30 * time.Second})
	if !r.OK() || !slices.Contains(strings.Fields(r.Out()), tag) {
		return apperr.New(apperr.UpdateVerify, "the downloaded tkt did not print version %s; nothing was replaced", tag).
			With("stdout", runner.Head(r.Out(), 2000)).With("stderr", runner.Head(r.ErrText(), 2000))
	}
	if err := os.Rename(tmp, target); err != nil {
		return notWritable(dir, err)
	}
	return nil
}

// releaseErr reports a failed read from the release host. An interrupted
// download stays a cancelled context, which tkt reports as an interruption.
func releaseErr(ctx context.Context, err error, format string, args ...any) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return apperr.New(apperr.ReleaseFailed, format, args...).Wrap(err).
		WithHint("check the network, then retry; the releases are at %s", release.DefaultReleases)
}

func notWritable(dir string, err error) error {
	return apperr.New(apperr.UpdateNotWritable, "cannot replace tkt in %s: %v", dir, err).Wrap(err).
		WithHint("rerun with write access to %s, or install with install.sh --dir DIR", dir)
}
