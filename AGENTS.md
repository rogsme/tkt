# Maintaining tkt

tkt is a Go command-line tool that runs the mechanical steps of the [rogs-skills](https://gitlab.com/rogs/skills) ticket lifecycle from a herdr pane. People and agents both drive it, so every change keeps two readers in mind: a person at a terminal and a program that parses the output.

`testdata/script/` holds the black-box tests: testscript scripts that run tkt against fakes of every outside tool. [`testdata/README.md`](testdata/README.md) describes them and the commands the scripts use.

## Layout

- `main.go` sets the version and calls `internal/cli`.
- `internal/cli` wires cobra and fang: flags, help, completion, error printing. It holds no command logic.
- `internal/app` runs each command and returns a typed result plus an error. The `*Opts` structs are a command's arguments.
- `internal/apperr` holds every error code and its exit status, in `codes.go`. It is the only place an exit status is assigned.
- `internal/fake` holds the fake herdr, wt, gh, opencode, claude, git and pi that the black-box tests put on PATH.
- `internal/pi` runs pi, the coding agent behind `tkt agent`. `pi.lock` pins the pi release, `fetch/` downloads its tarballs into the ignored `bundle/`, a build with the `bundled_pi` tag embeds the one for its system, and `ext/` holds tkt's pi extension, system prompt and themes. In `ext/`, `model.ts`, `attention.ts`, `jobs.ts`, `board.ts`, `report.ts` and `paint.ts` import nothing from pi, so `node --test 'internal/pi/ext/test/*.test.ts'` tests them; `hub.ts`, `actions.ts`, `tools.ts`, `commands.ts`, `ui.ts` and `index.ts` use pi's API. The extension runs tkt with `--json` and nothing else: lifecycle decisions stay in Go, where the scripts prove them.
- `internal/contract` holds what tkt reads from the skills repository, with verbatim fixtures in `testdata/`.
- The other `internal/` packages each wrap one outside system: `gitx`, `wt` (worktrunk), `herdr`, `harness` (Claude Code and OpenCode), `forge` (gh), and the agent flows in `agent`.
- `internal/history` appends ticket events to `$XDG_STATE_HOME/tkt/history.jsonl`, the one file that outlives worktrees, and builds the timelines `tkt stats` shows. A failed history write is a warning, never a failure.
- `internal/app/next.go` holds the one rule list: `NextStep` gives `tkt ls` its next step and a stable `reason`, and `PhaseOf` names the phase. `tkt auto` (`auto.go`) maps the same reasons to actions in `autoStep`, and `tkt watch` (`watch.go`) maps them to its upkeep steps in `watchStep`; keep the three in step.

## Rules

- A change in behavior comes with a script in `testdata/script/` that shows it, or a unit test next to the code.

- Every external command runs through `runner.Runner`, so it is logged and tests can replace it. Polls that repeat many times a second set `Quiet`.
- Every failure a person or program sees is an `*apperr.Error` with a registered code. Add the code to `Registry` with its exit status and a summary. Codes never change meaning once released.
- stdout carries only a command's result. Progress, warnings and dry-run lines go to stderr through `output.UI`. A child process's stdout is captured, never passed through.
- In a dry run, a change goes through `Ctx.Would` with the exact argv a real run would execute. Use the `...Argv` builders so both paths share the command.
- Worktrees are created and removed only through `wt`, with its hooks. Agents start only through `herdr agent start`, never by running `claude` directly.
- A ticket's tabs open only through `openTab`, which sets `TMPDIR` to the ticket's private directory (`internal/app/tmp.go`), so two tickets' agents never share a temporary file. `tkt done` removes the directory.
- One exception: `tkt agent` replaces tkt's own process with pi through `runner.Replacer`, which logs the command first. pi is the front end in the person's own pane, not a ticket's agent, so it does not go through `herdr agent start`.
- Another: `tkt watch` runs for days. Every repository command in a herdr pane starts it when none runs (`ensureWatch`), through `runner.Spawner`, which logs the command first and starts it in a session of its own. It takes its steps by calling the other commands' `App` methods, and keeps no state of its own beyond the ticket's state file and `tkt/watch` in the common git directory. The scripts set `TKT_WATCH=off`, so a watch never outlives one.
- Reach for the standard library first. A new dependency needs a reason in its commit message.
- Match the surrounding code's naming and comment density. Comments say why, not what.

## The contract with rogs-skills

tkt reads no skill files at run time. It depends on these lines from gitlab.com/rogs/skills:

- the configuration markers and the `## Forge` and `- Ticket ids:` lines that `/setup-rogs` writes;
- the status lines in that repository's CLAUDE.md registry;
- review-pr's `Verdict:` and `Reviewed head:` lines, and the numbered steps under `## Human verification` in its ready summary;
- the skill names in the built-in stage lines and in the line `tkt approve` sends;
- review-pr's rule that a clean merge of the base keeps the pull request's review brief current, which `tkt sync` relies on when it pushes such a merge itself;
- start-ticket's `## Unattended runs` section, which keys on the word "unattended" in the line `tkt auto --approve-plan` adds to the start line.

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

The `justfile` holds the common tasks; `just` lists them.

- `just run ARGS` runs the working tree's tkt on your terminal, such as `just run doctor`. `just build` writes `./tkt`, and `just install` replaces the tkt on PATH. These builds report a `dev-` version, so `tkt update` replaces them only with `--force`. All three first download this system's pi tarball (`just fetch-pi`) and build with the `bundled_pi` tag.
- `just test` runs the tests with the race detector. The tests build without the tag, so they see no bundled pi; the scripts point `TKT_PI` at the fake pi.
- `just ci` runs every check that `.gitea/workflows/ci.yml` runs: gofmt, vet, the race tests, shellcheck on `install.sh`, the four release builds without pi and with it, and the search for en and em dashes. Run it before pushing. A change to either file's checks goes in both.
- `just bump-pi VERSION` pins another pi release: it rewrites `internal/pi/pi.lock` from that release's `SHA256SUMS`, then downloads and checks the four tarballs. Read pi's changelog for changes to the flags in `internal/pi/argv.go`, then run the unpacked pi as the manual check in [`testdata/README.md`](testdata/README.md) says.

The scripts in `testdata/script/` have no terminal, so they never see a view that only appears on one, such as `tkt doctor`'s. Cover that view with a unit test that sets `prettyTerminal` (see `internal/cli/show_test.go`), and try it with `just run` in both a passing and a failing case.

The suite must stay well inside `go test`'s ten-minute default on the CI runner. Under `-race`, every fake is a race-built copy of the test binary, and such a binary sleeps a second as it exits. `GORACE=atexit_sleep_ms=0` in the sandbox (`setupSandbox` in `main_test.go`) keeps the suite at about twenty seconds; without it, CI times out.

The fakes cannot prove that herdr, worktrunk, Claude Code, OpenCode and pi behave as assumed. After changing how tkt talks to one of them, run the manual check in [`testdata/README.md`](testdata/README.md).

## CI and releases

`.gitea/workflows/ci.yml` runs on every push and pull request to master. `just release` runs the same checks locally, so a release does not wait for it.

git.rogs.me answers its web pages with a Cloudflare challenge, but `/api/v1` is open, so read CI through the API:
- `curl -s https://git.rogs.me/api/v1/repos/rogs/tkt/actions/tasks` lists the runs with their status.
- A failed job's log needs a login: `tea api --login git.rogs.me /repos/rogs/tkt/actions/runs/RUN/jobs`, then `.../actions/jobs/JOB/logs`. RUN is the number in the run's URL.

To release, run `just release vX.Y.Z` on master with a clean tree. It runs `just ci`, makes the annotated tag, and pushes master and the tag in one atomic push. `.gitea/workflows/release.yml` then tests, builds the four binaries, and publishes them with `install.sh` and `SHA256SUMS`, using the job's automatic token. That token sufficed for v0.2.0; `RELEASE_TOKEN` is only a fallback. `just release` waits for the files and installs the release the way the README does; it fails unless the installed tkt prints the new version.

- A tag with a hyphen, such as `vX.Y.Z-rc.N`, becomes a prerelease. `latest`, and so the default `install.sh`, follow full releases only. Install a prerelease from its own URL: `curl -fsSL https://git.rogs.me/rogs/tkt/releases/download/vX.Y.Z-rc.N/install.sh | sh -s -- --version vX.Y.Z-rc.N`. `just release` checks a prerelease that way.
- Push a prerelease first after changing the release workflow or `install.sh`.
- Each release binary embeds its system's pi tarball, so it weighs 40 to 50 MB instead of about 10. The release job downloads the four tarballs from pi's GitHub releases and checks them against `pi.lock` before it builds. The first release with pi inside must be a prerelease, to prove that the job, the upload and `tkt update` cope with that size.

The downloads work because a Cloudflare rule skips the challenge for `/rogs/tkt/releases/download/`; Gitea serves the assets there without redirecting to `/attachments/`. A 403 with `cf-mitigated: challenge` on a download means that rule changed. `go install gitlab.com/rogs/tkt@...` needs the public GitLab mirror. At v0.2.0 the mirror did not exist yet; a "could not read Username for 'https://gitlab.com'" error means it still does not. The README's "Maintainer setup" has the settings outside the repository.
