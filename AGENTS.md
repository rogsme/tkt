# Maintaining tkt

tkt is a Go command-line tool that runs the mechanical steps of the [rogs-skills](https://gitlab.com/rogs/skills) ticket lifecycle from a herdr pane. People and agents both drive it, so every change keeps two readers in mind: a person at a terminal and a program that parses the output.

`testdata/script/` holds the black-box tests: testscript scripts that run tkt against fakes of every outside tool. [`testdata/README.md`](testdata/README.md) describes them and the commands the scripts use.

## Layout

- `main.go` sets the version and calls `internal/cli`.
- `internal/cli` wires cobra and fang: flags, help, completion, error printing. It holds no command logic.
- `internal/app` runs each command and returns a typed result plus an error. The `*Opts` structs are a command's arguments.
- `internal/apperr` holds every error code and its exit status, in `codes.go`. It is the only place an exit status is assigned.
- `internal/fake` holds the fake herdr, wt, gh, opencode, claude and git that the black-box tests put on PATH.
- `internal/contract` holds what tkt reads from the skills repository, with verbatim fixtures in `testdata/`.
- The other `internal/` packages each wrap one outside system: `gitx`, `wt` (worktrunk), `herdr`, `harness` (Claude Code and OpenCode), `forge` (gh), and the agent flows in `agent`.

## Rules

- A change in behavior comes with a script in `testdata/script/` that shows it, or a unit test next to the code.

- Every external command runs through `runner.Runner`, so it is logged and tests can replace it. Polls that repeat many times a second set `Quiet`.
- Every failure a person or program sees is an `*apperr.Error` with a registered code. Add the code to `Registry` with its exit status and a summary. Codes never change meaning once released.
- stdout carries only a command's result. Progress, warnings and dry-run lines go to stderr through `output.UI`. A child process's stdout is captured, never passed through.
- In a dry run, a change goes through `Ctx.Would` with the exact argv a real run would execute. Use the `...Argv` builders so both paths share the command.
- Worktrees are created and removed only through `wt`, with its hooks. Agents start only through `herdr agent start`, never by running `claude` directly.
- Reach for the standard library first. A new dependency needs a reason in its commit message.
- Match the surrounding code's naming and comment density. Comments say why, not what.

## The contract with rogs-skills

tkt reads no skill files at run time. It depends on these lines from gitlab.com/rogs/skills:

- the configuration markers and the `## Forge` and `- Ticket ids:` lines that `/setup-rogs` writes;
- the status lines in that repository's CLAUDE.md registry;
- review-pr's `Verdict:` and `Reviewed head:` lines;
- the skill names in the built-in stage lines.

`internal/contract` pins them, and `internal/contract/testdata/SOURCES.md` records where each fixture comes from. When the skills change one of them, update the fixture and the code in one commit that cites the skills commit. `contract.Version` changes only when the contract's version does.

## Writing

Help text, error messages, the README and commit messages follow the skills repository's writing rules:

- Plain words, short sentences, present tense.
- No em dashes or en dashes (U+2014, U+2013): rewrite the sentence.
- No bold-label bullets.
- ASCII only, except where a fixture or test needs another character.
- Examples use Acme: team key `ACM`, tickets like `ACM-123`, hosts like `git.example.com`, repository `acme/app`. No client, employer or personal data.

An error message says what is wrong and, when it can, what to run next. Put the next command in the error's hint.

## Verification

Run these before pushing:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
uv run --quiet --with shellcheck-py shellcheck install.sh
for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do CGO_ENABLED=0 GOOS=${t%/*} GOARCH=${t#*/} go build -trimpath -o /dev/null . ; done
grep -rnP '[\x{2013}\x{2014}]' --exclude-dir=.git .
```

The suite must stay well inside `go test`'s ten-minute default on the CI runner. Under `-race`, every fake is a race-built copy of the test binary, and such a binary sleeps a second as it exits. `GORACE=atexit_sleep_ms=0` in the sandbox (`setupSandbox` in `main_test.go`) keeps the suite at about twenty seconds; without it, CI times out.

The fakes cannot prove that herdr, worktrunk, Claude Code and OpenCode behave as assumed. After changing how tkt talks to one of them, run the manual check in [`testdata/README.md`](testdata/README.md).

## CI and releases

`.gitea/workflows/ci.yml` runs on every push and pull request to master. Tag only a commit whose CI run passed.

git.rogs.me answers its web pages with a Cloudflare challenge, but `/api/v1` is open, so read CI through the API:
- `curl -s https://git.rogs.me/api/v1/repos/rogs/tkt/actions/tasks` lists the runs with their status.
- A failed job's log needs a login: `tea api --login git.rogs.me /repos/rogs/tkt/actions/runs/RUN/jobs`, then `.../actions/jobs/JOB/logs`. RUN is the number in the run's URL.

To release, tag a green commit `vX.Y.Z` and push the tag. `.gitea/workflows/release.yml` tests, builds the four binaries, and publishes them with `install.sh` and `SHA256SUMS`, using the job's automatic token. That token sufficed for v0.2.0; `RELEASE_TOKEN` is only a fallback.

- A tag with a hyphen, such as `vX.Y.Z-rc.N`, becomes a prerelease. `latest`, and so the default `install.sh`, follow full releases only. Install a prerelease from its own URL: `curl -fsSL https://git.rogs.me/rogs/tkt/releases/download/vX.Y.Z-rc.N/install.sh | sh -s -- --version vX.Y.Z-rc.N`.
- Push a prerelease first after changing the release workflow or `install.sh`.
- After a full release, check the install that the README gives. It must print the new version:
  ```bash
  curl -fsSL https://git.rogs.me/rogs/tkt/releases/download/latest/install.sh | TKT_INSTALL_DIR=$(mktemp -d) sh
  ```

The downloads work because a Cloudflare rule skips the challenge for `/rogs/tkt/releases/download/`; Gitea serves the assets there without redirecting to `/attachments/`. A 403 with `cf-mitigated: challenge` on a download means that rule changed. `go install gitlab.com/rogs/tkt@...` needs the public GitLab mirror. At v0.2.0 the mirror did not exist yet; a "could not read Username for 'https://gitlab.com'" error means it still does not. The README's "Maintainer setup" has the settings outside the repository.
