# tkt

`tkt` runs the mechanical steps around the [rogs-skills](https://gitlab.com/rogs/skills) ticket lifecycle from a [herdr](https://herdr.dev) pane. For each ticket it:

- creates the ticket's worktree;
- opens a tab named after the ticket;
- starts Claude Code or OpenCode in that tab;
- hands the agent its prompt.

Each command is one step you take, and `tkt ls` suggests the next one. `tkt watch`, which tkt starts by itself, takes the upkeep steps for every ticket: it merges a moved base when a sync is due, fixes failing checks once per head, and removes a ticket once finish-ticket merged it. When you want, `tkt auto` takes all the steps for a ticket until it needs you: it approves the plan if you allow it, waits for CI and the review bot, and runs the review and the fixes. You still run the human verification steps and merge.

People and programs both drive tkt. Every command takes `--json`, every failure has a stable error code, and `tkt schema` describes the whole interface.

## Install

On Linux or macOS, the install script downloads the latest release, checks it against the release's `SHA256SUMS` and installs it to `~/.local/bin`:

```bash
curl -fsSL https://git.rogs.me/rogs/tkt/releases/download/latest/install.sh | sh
```

`sh -s -- --dir DIR --version vX.Y.Z` picks another directory or release. The checksum catches a broken download, not a compromised host.

To download the binary yourself (this works in fish, bash and zsh; replace `linux_amd64` with `linux_arm64`, `darwin_amd64` or `darwin_arm64` for other systems):

```bash
mkdir -p ~/.local/bin && curl -fsSLo ~/.local/bin/tkt https://git.rogs.me/rogs/tkt/releases/download/latest/tkt_linux_amd64 && chmod +x ~/.local/bin/tkt
```

With Go 1.26 or newer (this build lacks the pi that `tkt agent` runs; see [The tkt agent](#the-tkt-agent)):

```bash
go install gitlab.com/rogs/tkt@latest
```

To update an installed tkt to the latest release, run `tkt update`. It makes the same checksum check as the install script, runs the new binary once, and only then replaces the old one. `tkt update --check` reports without changing anything, and `tkt update vX.Y.Z` installs a given release, prereleases included. A development build, whose `tkt --version` prints `dev` or a `dev-` version, has no release to compare, so `tkt update` replaces it only with `--force`.

### Requirements

- git 2.38 or newer;
- herdr 0.9.3 or newer, with tkt running inside a herdr pane;
- [worktrunk](https://worktrunk.dev) (`wt`) 0.68 or newer;
- Claude Code or OpenCode;
- the configuration that the `/setup-rogs` skill writes into the project;
- `gh` for `tkt done`, `tkt watch` and the pull request columns of `tkt ls`, which support GitHub forges only.

`tkt doctor` checks all of these.

worktrunk must name each worktree directory after its branch. Put the setup an agent needs, such as copying `.env` files and dependencies, in a `pre-start` hook. worktrunk runs `post-start` hooks in the background, so an agent could start before they finish:

```toml
worktree-path = "~/code/worktrees/{{ repo }}/{{ branch | sanitize_hash }}"

[pre-start]
copy = "wt step copy-ignored"
```

### Shell completion

Completion offers ticket ids, stages, presets and panes:

```bash
tkt completion fish > ~/.config/fish/completions/tkt.fish
```

`tkt completion bash` and `tkt completion zsh` work the same way.

## Use

| Command | What it does |
|---|---|
| `tkt agent` | Opens the tkt agent, a chat in your pane that drives your tickets. |
| `tkt start [ID]` | Creates the worktree and tab, starts an agent and sends it the prompt. |
| `tkt approve ID` | Approves the plan in the ticket's own session, so work-ticket begins there. |
| `tkt send ID TEXT` | Types text into the ticket's running session, such as changes to the plan; `--steer` types it in while the agent works, and `--wait` queues it until the agent stops. |
| `tkt auto ID` | Drives the ticket: approves the plan with `--approve-plan`, waits for CI and the review bot, runs the fixes, pauses when the ticket needs you, and stops when the head is reviewed clean. |
| `tkt hold ID` | Puts the ticket on hold, so `tkt watch` and `tkt auto` leave it alone; `--off` takes it off hold. |
| `tkt watch` | Keeps every ticket moving: syncs a moved base, runs ci once per head when the checks fail, and removes a ticket after finish-ticket's `Status: DONE`. tkt commands start it by themselves. |
| `tkt run ID STAGE` | Replaces the ticket's idle agent with a fresh session and sends a stage line: `review`, `address` (which merges the base first), `resolve`, `ci`, `verify` or `start`. `sync`, the old name of `address`, still works and warns. |
| `tkt sync ID` | Merges the base into the ticket's branch: itself, with no agent, when the merge is clean; through a fresh session that only resolves, on a conflict. |
| `tkt resume ID` | Relaunches the ticket's newest session when no agent runs for it, and sends nothing. |
| `tkt done ID...` | Closes merged tickets' tabs and removes their worktrees. `--all` checks every ticket worktree. |
| `tkt ls [ID...]` | Lists ticket worktrees with their agent, pull request, checks, review and the next step. |
| `tkt show ID` | Prints the agent's last message, such as the plan; `--screen` its live screen; `--steps` the human verification steps. |
| `tkt wait ID` | Blocks until the ticket's agent needs you or has finished. |
| `tkt stats` | Shows how long tickets took from start to merge, and how long they waited for you. |
| `tkt history ID...` | Shows what happened to tickets, merged ones too: each event with its time, and the pull request's commits and reviews. |
| `tkt doctor` | Checks the tools, versions and configuration tkt needs. |
| `tkt schema` | Describes every command, flag, result and error code as JSON. |

A ticket from start to merge, in short:

1. Run `tkt start ACM-12`, or pipe in the dispatcher's block with `tkt start --prompt -`.
2. Read the plan with `tkt show ACM-12`, then `tkt approve ACM-12`.
3. When the agent reports `Status: READY_FOR_REVIEW`, run `tkt run ACM-12 review`.
4. Act on the verdict with `address`, or on a moved base with `tkt sync ACM-12`, and review again.
5. Verify, merge in the tab, then run `tkt done ACM-12`.

For work with no ticket, `tkt start --scratch spike-login` makes the worktree, branch and tab `spike-login` and starts the agent without sending it anything. The other commands work on tickets only, so remove that worktree with `wt remove spike-login`.

### The tkt agent

`tkt agent` opens a chat in your pane about the repository's tickets. Typing `tkt` alone opens it too, at a terminal in a herdr pane inside a configured repository; anywhere else, `tkt` alone prints help.

The chat runs [pi](https://github.com/earendil-works/pi), an MIT-licensed coding agent. Release builds of tkt carry pi inside them and unpack it on first use under `~/.local/share/tkt/pi`. A tkt built with `go install` has no pi inside; set `TKT_PI` to a pi binary to use one.

The chat stays apart from any pi you run yourself. It loads only tkt's own extension, and none of your pi extensions, skills, prompt templates or themes, nor the project's `AGENTS.md` or `CLAUDE.md`. It keeps its settings, logins and sessions in `~/.config/tkt/pi`, so log in to a model provider once with `/login` inside the chat. `tkt agent --continue` picks up the last chat about the repository, and `tkt agent --session ID` the one whose id the chat printed when you quit. Flags after `--` go to pi as they are, such as `tkt agent -- --thinking high`.

The chat brings its own themes, `tkt-dark` and `tkt-light`, and picks the one that suits the terminal. Under a dark theme in fullscreen mode, the chat paints its own dark background, so a transparent terminal does not show through. Overlays such as `/board` and `/peek` draw on a panel of their own, so they read well on a transparent terminal. A theme you pick in `/settings` wins from then on.

Say what you want in plain words, such as "start ACM-12", "run ACM-13 to ACM-15 in auto with sol", "I approved the plan for ACM-12, continue" or "ACM-12 is merged, remove it". Starting tickets and stages runs in the background and the prompt comes back at once, so you can start the next ticket while the first one plans. A board pinned to the top of the chat shows every ticket with its age since `tkt start` and its time in the current phase, and on top what needs you: a plan to approve, a question from its agent, a pull request ready for human verification, or a `tkt auto` loop that paused for you. The chat scrolls below it. An item leaves the board as soon as its ticket moves on, so the chat stays clean; each new one also shows a herdr notification. Tickets in auto run in `tkt auto` loops that outlive the chat, and a loop that paused for you goes on by itself once you act. Text you send a working agent goes in at once, and the agent reads it at its next step. Each prompt shows the time it reached the model, and each answer the time it was done and how long it took.

Ask how a ticket went, merged ones too, such as "how did ACM-12 go?" or "what failed in ACM-12 and ACM-13?". The chat reads `tkt history`: each step with its time, and the pull request's commits and reviews with the review bot's findings.

| Command | What it does |
|---|---|
| `/start ID... [auto \| after-plan] [as PRESET]` | Starts tickets: by hand, in full auto, or in auto once you approve the plan, on a preset you name. |
| `/approve ID [auto]` | Approves the plan; with `auto`, `tkt auto` takes over. |
| `/next ID` | Takes the ticket's next step, as `tkt auto` would. |
| `/auto ID on \| off \| full` | Turns `tkt auto` on or off for a ticket. |
| `/sync ID` | Merges the base into a ticket's branch, as `tkt sync` does. `/check` offers it first when the ticket is behind its base. |
| `/hold ID [off]` | Puts a ticket on hold, so `tkt watch` and `tkt auto` leave it alone, or takes it off hold. |
| `/send ID TEXT`, `/show ID`, `/peek ID` | Talks to the ticket's agent, prints its last message, or watches its live screen. |
| `/check ID` | Walks the human verification steps as a checklist, and starts a fix session for a step that failed. |
| `/inbox`, `/board`, `/stats` | What needs you, with fixed answers; every ticket in columns (`/board hide` and `/board show` put the pinned board away and back); how long tickets take. |
| `/history ID...` | What happened to tickets: each event, commit and review. |
| `/done ID` | Removes a merged ticket. |
| `/update` | Installs a newer tkt, then offers to quit. Reopen the chat with the `tkt agent --session ID` it prints to run the new tkt. The board names a newer release when the chat finds one. |

Without an `[agent]` model, pi starts on its own default: in `/model`, press Ctrl+S on a model to make it the default for new chats. An `[agent]` table in `config.toml` picks the model and loads more pi extensions, such as one that adds a model provider:

```toml
[agent]
model = "acme/large"
extensions = ["~/.pi/agent/extensions/acme-provider.ts"]
```

`tkt help lifecycle` walks through each step. [`docs/lifecycle.md`](docs/lifecycle.md) follows a ticket from the tracker to a merged pull request, with diagrams of how tkt, the skills, herdr, worktrunk, the tracker and the forge fit together. `tkt COMMAND --help` explains when to use a command and what it refuses.

### Settings

`~/.config/tkt/config.toml` holds presets, which choose the harness and model, and stage lines:

```toml
[presets.claude]
harness = "claude"
serves = ["frontier"]

[presets.fast]
harness = "opencode"
model = "acme/small#max"
serves = ["small"]

[stages]
lint = "Run the linter on {id} and fix what it reports."

[review]
bot_wait = "15m"   # how long a new head waits for the review bot; "0s" without one, so review-pr reviews

[auto]
max_parallel = 3   # tkt auto and tkt watch agents at work at once in a repository

[watch]
interval = "60s"   # how often tkt watch looks at every ticket
```

Without the file, tkt runs Claude Code and OpenCode on their default models. The first preset is the default. A prompt's `Model:` line picks the first preset that serves it. `tkt help config` has the details.

### Driving tkt from a program

- `--json`, or `TKT_FORMAT=json` in the environment, makes stdout hold exactly one JSON document per run. On success it carries the command's result; on failure it carries an error with a stable `code`, the exit status, a `hint` naming what to do next, and `details` (`tkt help json`).
- `tkt schema` describes every command's arguments, flags and result fields, every error code and the environment tkt reads.
- `tkt wait ID` blocks until an agent needs you, so programs never have to poll.
- In `tkt ls --json`, each row's `next` holds the suggested command as an argv list.
- Exit statuses: 0 done, 1 an external command failed, 2 bad usage or input, 3 environment or configuration, 4 refused because of the current state, 5 uncertain outcome, so inspect the ticket's tab before you retry. `tkt help exit-codes` lists every code.
- tkt never prompts when stdin is not a terminal.

### State and logs

tkt keeps each ticket's state (preset, session, tab, pane, phase) in the worktree's git directory, so it disappears with the worktree. Each ticket's tabs get `TMPDIR` set to a private directory under `/tmp`, which `tkt done` removes too. It logs every command it runs to `~/.local/state/tkt/tkt.log`; a prompt appears there only as its size and hash. Past 10 MB the log starts over, and the older lines stay in `tkt.log.1`. `-v` prints the same lines as tkt goes.

`tkt watch` writes its lines to `tkt/watch.log` in the repository's git directory, and how its last pass went to `tkt/watch` beside it.

What has to outlive a worktree goes to `~/.local/state/tkt/history.jsonl`, one JSON event per line: when tkt started a ticket, each phase it saw it enter, each stage, each `tkt auto` step, and the merge. `tkt stats` and `tkt history` read it.

## Development

You need Go 1.26 or newer, [just](https://just.systems), and [uv](https://docs.astral.sh/uv/) for the shellcheck step. Run `just` to list the recipes.

To try a change, run tkt from the working tree on your own terminal. Arguments go to tkt as they are:

```bash
just run doctor
just run ls --json
just run start ACM-12 --dry-run
```

`just build` writes `./tkt`. `just install` builds the working tree over the tkt on PATH (`~/.local/bin`, or `TKT_INSTALL_DIR`), so herdr panes and agents use it too. These builds print a version such as `dev-v0.3.2-1-gabc1234`, so `tkt update` replaces them only with `--force`. Run `tkt update --force` to go back to the latest release.

`just run`, `just build` and `just install` first download the pi tarball for your system from pi's GitHub releases, check it against `internal/pi/pi.lock`, and build it in with the `bundled_pi` tag. `just bump-pi VERSION` pins another pi release.

The chat's extension is TypeScript in `internal/pi/ext`. Its tests need Node 22.18 or newer: `node --test 'internal/pi/ext/test/*.test.ts'`.

`just test` runs the tests with the race detector; `just test -run TestDoctorPretty` passes flags to `go test`. Before pushing, run `just ci`. It runs the same checks as CI: gofmt, vet, the race tests, shellcheck on `install.sh`, a build for each release target without pi and with it, the extension's tests, and the search for en and em dashes.

To release, run `just release vX.Y.Z` on a clean master. It runs `just ci`, pushes master and the tag together, waits for the release job to publish, and checks that the install script installs the new version. A tag with a hyphen, such as `vX.Y.Z-rc.1`, becomes a prerelease.

[`AGENTS.md`](AGENTS.md) has the layout, the rules, and how CI and releases work. [`testdata/README.md`](testdata/README.md) describes the tests.
