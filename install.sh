#!/bin/sh
# Installs tkt from its releases on git.rogs.me.
#
#   curl -fsSL https://git.rogs.me/rogs/tkt/releases/download/latest/install.sh | sh
#   curl -fsSL https://git.rogs.me/rogs/tkt/releases/download/latest/install.sh | sh -s -- --dir ~/bin --version v0.2.0
#
# It downloads the binary for this system and SHA256SUMS from the same
# release, checks the binary's checksum, and moves it into place. The
# checksum catches a broken download, not a compromised host.
set -eu

releases=${TKT_RELEASES:-https://git.rogs.me/rogs/tkt/releases/download}
dir=${TKT_INSTALL_DIR:-$HOME/.local/bin}
version=${TKT_VERSION:-latest}

say() { printf 'install.sh: %s\n' "$*" >&2; }
die() {
  say "$*"
  exit 1
}

while [ $# -gt 0 ]; do
  case $1 in
    --dir) [ $# -ge 2 ] || die "--dir needs a directory"; dir=$2; shift 2 ;;
    --dir=*) dir=${1#*=}; shift ;;
    --version) [ $# -ge 2 ] || die "--version needs a tag such as v0.2.0"; version=$2; shift 2 ;;
    --version=*) version=${1#*=}; shift ;;
    -h | --help)
      sed -n '2,10s/^# \{0,1\}//p' "$0" 2>/dev/null || true
      say "options: --dir DIR (default ~/.local/bin), --version TAG (default latest)"
      exit 0
      ;;
    *) die "unknown option $1; options: --dir DIR, --version TAG" ;;
  esac
done

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "no tkt build for $(uname -s); build it with: go install gitlab.com/rogs/tkt@latest" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "no tkt build for $(uname -m); build it with: go install gitlab.com/rogs/tkt@latest" ;;
esac
asset=tkt_${os}_${arch}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    die "curl or wget is needed to download tkt"
  fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
base=$releases/$version
say "downloading $asset ($version)"
fetch "$base/$asset" "$tmp/$asset" || die "cannot download $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "cannot download $base/SHA256SUMS"

want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "SHA256SUMS has no line for $asset"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
else
  die "sha256sum or shasum is needed to check the download"
fi
[ "$got" = "$want" ] || die "the checksum of $asset does not match SHA256SUMS; nothing was installed"

mkdir -p "$dir"
chmod 755 "$tmp/$asset"
# Move into the directory first, then rename, so a running tkt is never
# half-written. A symlink at the target, such as one to the old bash
# script, is replaced, not followed.
mv "$tmp/$asset" "$dir/.tkt.$$"
mv -f "$dir/.tkt.$$" "$dir/tkt"
say "installed $dir/tkt"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) say "$dir is not on your PATH; add it to run tkt by name" ;;
esac
"$dir/tkt" --version
