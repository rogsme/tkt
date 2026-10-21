# Common tasks for working on tkt. Run just with no arguments to list them.

# A local build is a development build: tkt update replaces it only with
# --force. A bare git describe would read as a prerelease of the last tag.
version := "dev-" + `git describe --tags --always --dirty`
ldflags := "-X main.version=" + version
targets := "linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"
releases := "https://git.rogs.me/rogs/tkt/releases/download"

# List the recipes.
default:
    @just --list --unsorted

# Build ./tkt from the working tree, with pi inside.
build: fetch-pi
    go build -tags bundled_pi -ldflags '{{ ldflags }}' -o tkt .

# Run tkt from the working tree on this terminal, such as: just run doctor
[positional-arguments]
run *args: fetch-pi
    go run -tags bundled_pi -ldflags '{{ ldflags }}' . "$@"

# Install the working tree's tkt where install.sh puts it.
install dir=env("TKT_INSTALL_DIR", home_directory() / ".local/bin"): fetch-pi
    go build -tags bundled_pi -trimpath -ldflags '{{ ldflags }}' -o '{{ dir }}/tkt' .
    '{{ dir }}/tkt' --version

# Download pi for this system, or for SYSTEMS such as linux/arm64, and check it against pi.lock.
fetch-pi *systems:
    go run ./internal/pi/fetch {{ systems }}

# Pin pi VERSION in internal/pi/pi.lock, then download and check its tarballs.
bump-pi version:
    go run ./internal/pi/fetch -bump '{{ version }}'
    go run ./internal/pi/fetch {{ targets }}

# Run the tests with the race detector.
test *args:
    go test -race ./... {{ args }}

# Run every check that .gitea/workflows/ci.yml runs.
ci:
    @test -z "$(gofmt -l .)" || { gofmt -l .; echo "gofmt: format the files above"; exit 1; }
    go vet ./...
    go test -race ./...
    uv run --quiet --with shellcheck-py shellcheck install.sh
    for t in {{ targets }}; do CGO_ENABLED=0 GOOS=${t%/*} GOARCH=${t#*/} go build -trimpath -o /dev/null . || exit 1; done
    go run ./internal/pi/fetch {{ targets }}
    for t in {{ targets }}; do CGO_ENABLED=0 GOOS=${t%/*} GOARCH=${t#*/} go build -tags bundled_pi -trimpath -o /dev/null . || exit 1; done
    node --test 'internal/pi/ext/test/*.test.ts'
    @# Bytes, not characters: in the C locale git grep cannot read \x{2014}, and an error must not pass as no match.
    @LC_ALL=C git grep --untracked -nP '\xE2\x80[\x93\x94]' -- ':!.claude'; test $? -eq 1 || { echo "rewrite the lines above without dashes"; exit 1; }

# Check, push master and the tag VERSION together, then wait for the release.
release version: (_can-release version) ci
    #!/bin/sh
    set -eu
    v='{{ version }}'
    git tag -a "$v" -m "tkt ${v#v}"
    git push --atomic origin master "$v"
    echo "waiting for release.yml to publish $v"
    sha=$(git rev-parse HEAD)
    i=0
    until curl -fsSL -o /dev/null '{{ releases }}'"/$v/SHA256SUMS" 2>/dev/null; do
        status=$(curl -s https://git.rogs.me/api/v1/repos/rogs/tkt/actions/tasks |
            jq -r --arg sha "$sha" '[.workflow_runs[] | select(.head_sha == $sha and .name == "Build and publish")][0].status // ""')
        case $status in
        failure | cancelled)
            echo "the release job for $v ended with $status; read its log as AGENTS.md says" >&2
            exit 1
            ;;
        esac
        i=$((i + 1))
        if [ "$i" -gt 60 ]; then
            echo "$v is not published after ten minutes" >&2
            exit 1
        fi
        sleep 10
    done
    dir=$(mktemp -d)
    trap 'rm -rf "$dir"' EXIT
    case $v in
    *-*) curl -fsSL '{{ releases }}'"/$v/install.sh" | TKT_INSTALL_DIR=$dir sh -s -- --version "$v" >/dev/null ;;
    *) curl -fsSL '{{ releases }}/latest/install.sh' | TKT_INSTALL_DIR=$dir sh >/dev/null ;;
    esac
    got=$("$dir/tkt" --version)
    if [ "$got" != "tkt version $v" ]; then
        echo "install.sh installed \"$got\", not $v" >&2
        exit 1
    fi
    echo "released $v: $got"

# Refuse a release that would not be the tested master.
[private]
_can-release version:
    #!/bin/sh
    set -eu
    v='{{ version }}'
    if ! printf '%s\n' "$v" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'; then
        echo "$v is not a release tag such as v0.3.0 or v0.3.0-rc.1" >&2
        exit 1
    fi
    if [ "$(git branch --show-current)" != master ]; then
        echo "release from master" >&2
        exit 1
    fi
    if [ -n "$(git status --porcelain)" ]; then
        echo "commit or stash your changes first" >&2
        exit 1
    fi
    git fetch -q origin master --tags
    if ! git merge-base --is-ancestor origin/master HEAD; then
        echo "origin/master has commits this branch lacks; pull first" >&2
        exit 1
    fi
    if git rev-parse -q --verify "refs/tags/$v" >/dev/null; then
        echo "$v already exists" >&2
        exit 1
    fi
