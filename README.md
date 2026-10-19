# tkt

`tkt` runs the mechanical steps around the [rogs-skills](https://gitlab.com/rogs/skills) ticket lifecycle from a [herdr](https://herdr.dev) pane. For each ticket it:

- creates the ticket's worktree;
- opens a tab named after the ticket;
- starts Claude Code or OpenCode in that tab;
- hands the agent its prompt.

Each command is one step you take. Nothing chains stages together: you still approve plans, run the human verification steps and merge, and `tkt ls` only suggests the next command.

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

With Go 1.26 or newer:

```bash
go install gitlab.com/rogs/tkt@latest
```

To update an installed tkt to the latest release, run `tkt update`. It makes the same checksum check as the install script, runs the new binary once, and only then replaces the old one. `tkt update --check` reports without changing anything, and `tkt update vX.Y.Z` installs a given release, prereleases included. A development build, whose `tkt --version` prints `dev` or a `dev-` version, has no release to compare, so `tkt update` replaces it only with `--force`.

### Requirements

- git 2.31 or newer;
- herdr 0.9.3 or newer, with tkt running inside a herdr pane;
- [worktrunk](https://worktrunk.dev) (`wt`) 0.68 or newer;
- Claude Code or OpenCode;
- the configuration that the `/setup-rogs` skill writes into the project;
- `gh` for `tkt done` and the pull request columns of `tkt ls`, which support GitHub forges only.

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
| `tkt start [ID]` | Creates the worktree and tab, starts an agent and sends it the prompt. |
| `tkt run ID STAGE` | Replaces the ticket's idle agent with a fresh session and sends a stage line: `review`, `address`, `sync`, `ci`, `verify` or `start`. |
| `tkt resume ID` | Relaunches the ticket's newest session when no agent runs for it, and sends nothing. |
| `tkt done ID...` | Closes merged tickets' tabs and removes their worktrees. `--all` checks every ticket worktree. |
| `tkt ls [ID...]` | Lists ticket worktrees with their agent, pull request, checks, review and the next step. |
| `tkt wait ID` | Blocks until the ticket's agent needs you or has finished. |
| `tkt doctor` | Checks the tools, versions and configuration tkt needs. |
| `tkt schema` | Describes every command, flag, result and error code as JSON. |

A ticket from start to merge, in short:

1. Run `tkt start ACM-12`, or pipe in the dispatcher's block with `tkt start --prompt -`.
2. Approve the plan in the ticket's tab.
3. When the agent reports `Status: READY_FOR_REVIEW`, run `tkt run ACM-12 review`.
4. Act on the verdict with `address` or `sync`, and review again.
5. Verify, merge in the tab, then run `tkt done ACM-12`.

`tkt help lifecycle` walks through each step. `tkt COMMAND --help` explains when to use a command and what it refuses.

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

tkt keeps each ticket's state (preset, session, tab, pane) in the worktree's git directory, so it disappears with the worktree. It logs every command it runs to `~/.local/state/tkt/tkt.log`; a prompt appears there only as its size and hash. `-v` prints the same lines as tkt goes.

## Development

You need Go 1.26 or newer, [just](https://just.systems), and [uv](https://docs.astral.sh/uv/) for the shellcheck step. Run `just` to list the recipes.

To try a change, run tkt from the working tree on your own terminal. Arguments go to tkt as they are:

```bash
just run doctor
just run ls --json
just run start ACM-12 --dry-run
```

`just build` writes `./tkt`. `just install` builds the working tree over the tkt on PATH (`~/.local/bin`, or `TKT_INSTALL_DIR`), so herdr panes and agents use it too. These builds print a version such as `dev-v0.3.2-1-gabc1234`, so `tkt update` replaces them only with `--force`. Run `tkt update --force` to go back to the latest release.

`just test` runs the tests with the race detector; `just test -run TestDoctorPretty` passes flags to `go test`. Before pushing, run `just ci`. It runs the same checks as CI: gofmt, vet, the race tests, shellcheck on `install.sh`, a build for each release target, and the search for en and em dashes.

To release, run `just release vX.Y.Z` on a clean master. It runs `just ci`, pushes master and the tag together, waits for the release job to publish, and checks that the install script installs the new version. A tag with a hyphen, such as `vX.Y.Z-rc.1`, becomes a prerelease.

[`AGENTS.md`](AGENTS.md) has the layout, the rules, and how CI and releases work. [`testdata/README.md`](testdata/README.md) describes the tests.
