# Maintaining tkt

tkt is a Go command-line tool that runs the mechanical steps of the [rogs-skills](https://gitlab.com/rogs/skills) ticket lifecycle from a herdr pane. People and agents both drive it, so every change keeps two readers in mind: a person at a terminal and a program that parses the output.

`testdata/script/` holds the black-box tests: testscript scripts that run tkt against fakes of every outside tool. [`testdata/README.md`](testdata/README.md) describes them and the commands the scripts use.

## Layout

- `main.go` sets the version, takes tkt's own TMPDIR out of a ticket's directory, so nothing tkt starts keeps one that `tkt done` removes, and calls `internal/cli`.
- `internal/cli` wires cobra and fang: flags, help, completion, error printing. It holds no command logic.
- `internal/app` runs each command and returns a typed result plus an error. The `*Opts` structs are a command's arguments.
- `internal/apperr` holds every error code and its exit status, in `codes.go`. It is the only place an exit status is assigned.
- `internal/fake` holds the fake herdr, wt, gh, opencode, claude, git and pi that the black-box tests put on PATH.
- `internal/pi` runs pi, the coding agent behind `tkt agent`. `pi.lock` pins the pi release, `fetch/` downloads its tarballs into the ignored `bundle/`, a build with the `bundled_pi` tag embeds the one for its system, and `ext/` holds tkt's pi extension, system prompt and themes. In `ext/`, `model.ts`, `attention.ts`, `jobs.ts`, `board.ts`, `report.ts`, `paint.ts`, `chat.ts`, `mirror.ts`, `gate.ts` and `web/acts.ts`, `web/ansi.ts`, `web/diff.ts` and `web/state.ts` import nothing from pi, so `node --test 'internal/pi/ext/test/*.test.ts'` tests them; `hub.ts`, `actions.ts`, `tools.ts`, `commands.ts`, `dialog.ts`, `ui.ts`, `web.ts` and `index.ts` use pi's API. The extension runs tkt with `--json` and nothing else: lifecycle decisions stay in Go, where the scripts prove them. `web.ts` serves the web page from inside the chat on pi's Bun, through `serve.ts`, which holds the gate, the page's files and the WebSocket and imports nothing from pi; Bun turns the page's TypeScript under `ext/web/` into JavaScript as it serves it, so the page has no build step; `ext/web/vendor/` holds Preact with htm and marked, as `ext/web/vendor/README.md` says. The page imports `model.ts`, `attention.ts`, `board.ts` and `report.ts`, which `web.ts` serves beside it, so the page and the terminal's board read tickets the same way; `ext/web/acts.ts`, `ansi.ts`, `diff.ts` and `state.ts` are the page's pure modules. The page reads its views, such as a ticket's diff, through queries that run tkt with `--json` and change nothing. Every question and notice the extension gives goes through `dialog.ts`, which asks in the terminal and on the open pages at once, so the two never drift; every button the page gets sends a slash command. `ext/play/` is the playground that `just play` runs: `serve.ts` serves the same page with made-up tickets (`scenario.ts`), the query answers tkt would give (`answers.ts`), and a fake chat (`fake.ts`) that answers every command `commands.ts` registers and starts what only a chat starts (`triggers.ts`, by `/play` or the toolbar). Those four import nothing from pi or Bun, so `node --test` covers them; `main.ts` and `toolbar.ts` do not ship, since the embed leaves `ext/play/` out.
- `internal/contract` holds what tkt reads from the skills repository, with verbatim fixtures in `testdata/`.
- The other `internal/` packages each wrap one outside system: `gitx`, `wt` (worktrunk), `herdr`, `harness` (Claude Code and OpenCode), `forge` (gh), `statuspage` (the services' status pages), and the agent flows in `agent`. `internal/cron` reads the five-field schedules chores run on.
- `internal/history` appends ticket events to `$XDG_STATE_HOME/tkt/history.jsonl`, the one file that outlives worktrees, and builds the timelines `tkt stats` shows. A failed history write is a warning, never a failure.
- `internal/app/next.go` holds the one rule list: `NextStep` gives `tkt ls` its next step and a stable `reason`, and `PhaseOf` names the phase. `tkt auto` (`auto.go`) maps the same reasons to actions in `autoStep`, and `tkt watch` (`watch.go`) maps them to its upkeep steps in `watchStep`; keep the three in step.

## Rules

- A change in behavior comes with a script in `testdata/script/` that shows it, or a unit test next to the code. It also updates the README, `docs/lifecycle.md` and `docs/day-in-the-life.md` where they describe it.

- Every external command runs through `runner.Runner`, so it is logged and tests can replace it. Polls that repeat many times a second set `Quiet`.
- Every failure a person or program sees is an `*apperr.Error` with a registered code. Add the code to `Registry` with its exit status and a summary. Codes never change meaning once released.
- stdout carries only a command's result. Progress, warnings and dry-run lines go to stderr through `output.UI`. A child process's stdout is captured, never passed through.
- In a dry run, a change goes through `Ctx.Would` with the exact argv a real run would execute. Use the `...Argv` builders so both paths share the command.
- Worktrees are created and removed only through `wt`, with its hooks. Agents start only through `herdr agent start`, never by running `claude` directly.
- A chore's run is a worktree whose state names its chore, made by `tkt chore run` through `tkt start`'s path. The commands that act on a worktree take its branch as an id through `workID`, and `workWorktrees` lists runs beside tickets; the ones that need a ticket, such as `tkt merge` and `tkt auto`, keep `NormalizeID`, and `tkt run` and `tkt approve` refuse a run any line that names a lifecycle skill, since no skill takes a pull request without a ticket.
- A ticket's tabs and splits open only through `openTab` and `openSplit`, which set `TMPDIR` to the ticket's private directory (`internal/app/tmp.go`), so two tickets' agents never share a temporary file. `tkt done` removes the directory. OpenCode runs every agent's tools in its one service, outside the tab, so tkt's OpenCode plugin, which `tkt hook tmp --install` writes, gives its shells that directory and moves the new `/tmp` paths an agent types into it (`internal/app/hook.go`); the rules live in Go, and the plugin asks `tkt hook tmp` on every call. The same plugin tells `tkt hook event` about each new prompt, each status line and each skill start in a ticket's session, which tkt reads over the session's last 50 messages. Claude Code runs its tools in the tab and needs no TMPDIR hook; a second Bash rewrite hook would cancel the user's own, such as RTK's. Its `UserPromptSubmit`, `Stop` and `PreToolUse` on `Skill` hooks, which `tkt hook claude --install` adds to the project's settings, rewrite nothing: they run `tkt hook event --claude`, which prints nothing and never fails, and records the same events (`internal/app/claudehook.go`). `tkt doctor` checks both harnesses' hooks, in the project's files or the user's.
- One exception: `tkt agent` replaces tkt's own process with pi through `runner.Replacer`, which logs the command first. pi is the front end in the person's own pane, not a ticket's agent, so it does not go through `herdr agent start`. A `tkt auto` loop whose binary was replaced replaces its own process the same way, with the same command on the new tkt (`restartAuto`), once it holds no slot; what it knows beyond the ticket's state goes over in `TKT_AUTO_CARRY`.
- Another: `tkt watch` runs for days. Every repository command in a herdr pane starts it when none runs (`ensureWatch`), through `runner.Spawner`, which logs the command first and starts it in a session of its own. It takes its steps by calling the other commands' `App` methods, and keeps no state of its own beyond the ticket's state file and, in the common git directory, `tkt/watch`, `tkt/chores` (each chore's last slot) and `tkt/status` (what the status pages said last). The scripts set `TKT_WATCH=off`, so a watch never outlives one. `tkt watch --install` writes the systemd units or launchd agents that run it and herdr's server (`internal/service`), with the `PATH` of the shell it runs in, since the agents in herdr's panes inherit herdr's; tkt commands then start the unit instead.
- Reach for the standard library first. A new dependency needs a reason in its commit message.
- Match the surrounding code's naming and comment density. Comments say why, not what.

## The contract with rogs-skills

tkt reads no skill files at run time. It depends on these lines from gitlab.com/rogs/skills:

- the configuration markers and the `## Forge` and `- Ticket ids:` lines that `/setup-rogs` writes;
- the status lines in that repository's CLAUDE.md registry;
- review-pr's `Verdict:` and `Reviewed head:` lines, and the numbered steps under `## Human verification` in its ready summary;
- the skill names in the built-in stage lines and in the lines `tkt approve`, `tkt merge` and `tkt suggest` send;
- finish-ticket's rules that it merges only on the user's explicit word, with every human verification step confirmed, and stops with `BLOCKED_FOR_SYNC` when the base moved after the review, which the line `tkt merge` sends, and its refusals, rely on; and that a confirmation counts for the head it names and carries over clean merges of the base, which the line's heads and a merge order across `tkt sync` rely on;
- review-pr's rule that a clean merge of the base keeps the pull request's review brief current, which `tkt sync` relies on when it pushes such a merge itself;
- finish-ticket's `Unblocked:` line, right before `Status: DONE`, which `tkt done` reads to name the tickets a merge unblocked;
- start-ticket's rule that the viewer's approval starts work-ticket in the same session, so `tkt ls` takes a `work-ticket` skill start on the start stage for the approval when no `Status: PLAN_APPROVED` came;
- start-ticket's `## Unattended runs` section, which keys on the word "unattended" in the line `tkt auto --auto-plan` adds to the start line.
- the `Agent verification`, `Autonomous runs` and `Plan review` rows of workflow.md's feature table, which decide whether verify-pr runs after a clean review and whether `tkt auto --yolo` may run;
- verify-pr's report, a comment-only review: its `Verified head:` and `Verification:` lines and one line per step, which `tkt ls`, `tkt show --steps`, `tkt merge --evidence` and `/check` read, and its rule that a failed step is feedback address-pr-review answers;
- finish-ticket's rule that a confirmation made from verify-pr's evidence counts, which the line `tkt merge --evidence` sends;
- finish-ticket's rule that a step the user explicitly waived with a reason counts, which the line `tkt merge --waived` sends;
- finish-ticket's and verify-pr's rule that a `## Review accepted` conversation comment, with `Accepted head:` and `Reason:` lines, stands in for a clean review at that head and across clean merges of the base, with the procedure from the pull request body, which `tkt accept` posts and `tkt ls` reads; and address-pr-review's and review-pr's rules that leave its findings alone;
- the `## Autonomous runs` sections, which key on "This run is autonomous" in every line `tkt auto --yolo` sends; start-ticket's `## Autonomous run` comment; and finish-ticket's rule that it merges such a ticket on verify-pr's passing report for the head the line names.

`internal/contract` pins them, and `internal/contract/testdata/SOURCES.md` records where each fixture comes from. When the skills change one of them, update the fixture and the code in one commit that cites the skills commit. `contract.Version` changes only when the contract's version does.

## Writing

Help text, error messages, the README, the pages in `docs/` and commit messages follow the skills repository's writing rules:

- Plain words, short sentences, present tense.
- No em dashes or en dashes (U+2014, U+2013): rewrite the sentence.
- No bold-label bullets.
- ASCII only, except where a fixture or test needs another character.
- Examples use Acme: team key `ACM`, tickets like `ACM-123`, hosts like `git.example.com`, repository `acme/app`. No client, employer or personal data.

An error message says what is wrong and, when it can, what to run next. Put the next command in the error's hint.

## Verification

The `justfile` holds the common tasks; `just` lists them.

- `just run ARGS` runs the working tree's tkt on your terminal, such as `just run doctor`. `just build` writes `./tkt`, and `just install` replaces the tkt on PATH. These builds report a `dev-` version, so `tkt update` replaces them only with `--force`. All three first download this system's pi tarball (`just fetch-pi`) and build with the `bundled_pi` tag.
- `just play` serves the web page on `http://127.0.0.1:7357/?token=play` with made-up tickets, so you can press every button, open every tab and view, and start every question, notice and banner from `/play` or the toolbar without a chat. It reads the page's files on every request, so a reload shows a change to `ext/web/`; a change to `ext/play/` or `serve.ts` needs a restart. It needs Bun on PATH. Playwright can drive it at that fixed address; `--port`, `--token` and `--network` change it.
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

The downloads work because a Cloudflare rule skips the challenge for `/rogs/tkt/releases/download/`; Gitea serves the assets there without redirecting to `/attachments/`. A 403 with `cf-mitigated: challenge` on a download means that rule changed. `go install gitlab.com/rogs/tkt@...` needs the public GitLab mirror. At v0.2.0 the mirror did not exist yet; a "could not read Username for 'https://gitlab.com'" error means it still does not. The settings outside the repository, that rule among them, were in the README's "Maintainer setup" section until 569e7a5 trimmed it; `git show 569e7a5^:README.md` prints it.
