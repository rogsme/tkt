# tkt black-box tests

`go test ./...` runs them, along with the unit tests. To run some scripts only:

```bash
go test -run 'TestScripts/start_' .
```

## How they work

`main_test.go` runs each script in `script/` with [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript). Every script gets its own sandbox:

- a temporary `HOME`;
- a git repository in `$REPO` with a bare `origin`;
- the lifecycle configuration for the Acme tracker (`ACM-<number>`) and a GitHub forge;
- a herdr session whose only pane sits in that repository.

The script starts in `$REPO`. Files in a script's archive land in `$WORK`, so a script names them as `$WORK/<file>`.

The fakes in `internal/fake` stand in for the real tools on `PATH`:

- `herdr` keeps workspaces, tabs, panes and agents in `$FAKE_DIR/herdr.json` and answers the commands tkt uses. A pane that `tab create` opens keeps its `--env` values as `env`. Environment variables inject failures, such as `FAKE_HERDR_START_ERRORS=agent_pane_busy,...` or `FAKE_HERDR_PROMPT_ERRORS=agent_prompt_stalled`. A `-` in such a list lets that call succeed. `agent read` prints `$FAKE_DIR/screen` when it exists, otherwise `FAKE_HERDR_SCREEN`. `FAKE_HERDR_DOWN=1` makes every call fail, as when herdr's server does not run.
- In turns mode, `FAKE_HERDR_TURNS` names a JSON list of agent turns, such as `[{"screen": "Status: AWAITING_APPROVAL"}, {"screen": "...", "prs": "/path/prs.json"}]`. A prompt sets the agent working, and each `agent wait` on a working agent ends its turn with the next entry: its `status` (idle by default), its screen, and a pull request file that `gh` lists from then on. The `auto_` scripts drive whole tickets this way.
- `wt` creates and removes real git worktrees under `$FAKE_WT_ROOT` and prints worktrunk's JSON: schema 1, or schema 2 with `FAKE_WT_SCHEMA=2`.
- `gh` answers `pr list` from `$FAKE_DIR/prs.json` when it exists, otherwise from the JSON file in `FAKE_GH_PRS`.
- `opencode` answers the session API calls, and a session's messages from the JSON file in `FAKE_OC_MESSAGES`; `FAKE_OC_PAD=N` adds an old message of N bytes. Like the real `opencode api`, it writes only the first 256 KiB of an answer to a pipe.
- `claude` fails if anything calls it, because tkt starts agents through herdr only.
- `git` logs each call, then runs the real git.
- `systemctl` keeps the user units' states in `$FAKE_DIR/systemd.json` (enabled and active) and answers `--user` commands. Like systemd, it refuses to enable or start a unit whose file, or template's file, is not in `$XDG_CONFIG_HOME/systemd/user`. It runs nothing: a started unit only counts as active. `systemd-escape --path` names a path with systemd's rules, `loginctl` reads and writes `$FAKE_DIR/linger`, and `launchctl` keeps the loaded agents in `$FAKE_DIR/launchd.json`.
- `pi` answers `--version` with `1.3.0`. Any other call appends the variables tkt agent sets (`PI_CODING_AGENT_DIR`, `PI_SKIP_VERSION_CHECK`, `PI_TELEMETRY`, `TKT_BIN`) to `$FAKE_DIR/pi-env` and exits. The test binary has no bundled pi, so the `agent_` scripts set `TKT_PI=pi`; the condition `[bundled_pi]` holds when `go test -tags bundled_pi` built one in. A script has no terminal, so `tkt agent` never gets past its terminal check there: the scripts check its dry run, and unit tests in `internal/pi` and `internal/runner` cover the command line and the process replacement.

The `update_` scripts point `TKT_RELEASES` and `TKT_RELEASES_API` at `file://` URLs under `$WORK`, and set `TKT_UPDATE_TARGET` to the file `tkt update` replaces. Only the tests set `TKT_UPDATE_TARGET`: without it, `tkt update` would replace the shared test binary.

Every fake appends its command line to `$FAKE_LOG`, each word quoted like bash `printf %q`.

`TKT_WATCH=off` keeps every script's tkt commands from starting a `tkt watch` that would outlive the script; the `watch_` scripts turn it back on where they test that. `TKT_SERVICE_MANAGER` picks the services `tkt watch --install` writes, so a script on Linux can test the launchd agents too.

The scripts use these commands beyond testscript's own:

| Command | Checks or does |
|---|---|
| `rc N` | The last `tkt` run exited with status N. |
| `logged WORDS...` | The log has a line containing the words, quoted as the log quotes them. |
| `logcontains TEXT` | The log holds TEXT exactly. |
| `resetlog` | Forgets the commands and prompts so far. |
| `houserules` | No `wt` run used `--no-hooks`, `--clobber` or `--yes`; no git or herdr command changed worktrees; nothing ran `claude` directly. |
| `jqedit FILE EXPR`, `jqeq FILE EXPR WANT` | Edits or checks a JSON file with a jq expression. Expressions can read `$REPO`, `$WORK`, `$WT`, `$FAKE_WT_ROOT`, `$FAKE_DIR`, `$HOME`, `$SID` and `$H1` to `$H5`. |
| `state DIR KEY WANT`, `statevar DIR KEY VAR` | Checks or reads the ticket state of the worktree in DIR. |
| `waitstate DIR KEY WANT` | Waits up to 30 seconds for the ticket state to record KEY=WANT, for a `tkt auto` loop started in the background with `exec ... &`. |
| `waitfile FILE` | Waits up to 30 seconds for FILE to exist, such as the status file a `tkt watch` started in the background writes. |
| `mkprompt FILE ID [LINE...]` | Writes a start-ticket prompt for ID. |
| `realgit ARGS...` | Runs the real git without logging, for setup. |
| `gitvar VAR DIR REV` | Sets VAR to a commit. |
| `claudedir VAR PATH` | Sets VAR to Claude Code's project directory for PATH. |
| `touchat FILE DATE` | Creates FILE with a modification time. |
| `mkrelease TAG` | Publishes a fake release TAG under `$WORK/rel`: a binary for each system that logs `new-tkt ARGS` and prints `tkt version TAG`, and `SHA256SUMS`. |
| `envelope [EXPR WANT]` | stdout holds exactly one JSON envelope, and EXPR applied to it gives WANT. |
| `unitvar VAR PATH` | Sets VAR to PATH as `systemd-escape --path` names it. |

## Manual check in herdr

The fakes cannot prove that the real tools behave as assumed. After a change to how tkt talks to herdr, worktrunk, Claude Code or OpenCode, run these from a herdr pane in a scratch repository that has the lifecycle configuration:

1. Run `tkt start ACM-1 --dry-run --prompt "..."`, then the same without `--dry-run`. Check:
   - the tab label;
   - that the agent runs in build mode: Claude shows "bypass permissions on", and OpenCode shows Build and the preset's model;
   - that the prompt arrived whole.
2. Run the same start again. It refuses, because an agent runs in the worktree.
3. Run `tkt run ACM-1 address` while the agent works. It refuses. Run it again once the agent is idle: the agent exits, a new one starts, and it gets the address line.
4. Exit the agent, then run `tkt resume ACM-1`. The newest session comes back in bypass mode.
5. Run `tkt ls`. It shows the row and a sensible next step.
6. Close the tab, then remove the scratch worktrees with `wt remove`.

After a change to how tkt reads sessions or drives tickets (`show`, `approve`, `send`, `auto`, the last status in `ls` and `wait`):

1. Start a real ticket and wait for the plan. `tkt show ACM-1` prints it whole, and `tkt ls` shows `AWAITING_APPROVAL` and the phase `awaiting_approval`, even after you scroll the tab.
2. `tkt send ACM-1 "..."` asks for a change in the same session; `tkt approve ACM-1` then starts work-ticket there.
3. `tkt wait ACM-1 --notify` shows a herdr notification when the agent stops.
4. On another ticket, `tkt auto ACM-2 --approve-plan --notify` runs to `READY_FOR_HUMAN`. Check that it waits for CI and for the review bot, and that it stops with a notification when the agent asks a question.
5. `tkt show ACM-2 --steps` lists the review's human verification steps. After the merge, `tkt done ACM-2` reports the time from start to merge, and `tkt stats` lists it.

After a change to `tkt sync`, `tkt watch` or the tabs' `TMPDIR`, in a scratch GitHub repository with the lifecycle configuration and a pull request for `ACM-1`:

1. Merge another branch into `main` that touches other files, then run `tkt sync ACM-1`. It pushes a merge commit within seconds, starts no agent, and CI and the review bot run on the new head.
2. Merge a change to the same lines into `main`, then run `tkt sync ACM-1`. A fresh session in the ticket's tab gets the resolve line, resolves the conflict, and pushes one merge.
3. In the ticket's tab, run `echo $TMPDIR` and `mktemp`: both name `/tmp/tkt-...-ACM-1-...`. `tkt done ACM-1` after the merge removes the directory.
4. Run `tkt ls` in a herdr pane: it says it started `tkt watch`, `tkt ls --json` shows `watch.running`, and the chat's board heading says `watch on`. Make the checks fail on a new head: within a minute, a fresh session runs ci, and herdr notifies. `tkt watch --stop` stops it.
5. From a terminal outside herdr, `tkt watch --once --dry-run` finds the workspace from the panes and says what each ticket would get.

After a change to `tkt watch --install` or the units, on a Linux machine with systemd, from a herdr pane in a scratch repository with the lifecycle configuration and a pull request for `ACM-1`:

1. Run `tkt watch --install --dry-run`, then `tkt watch --install`. `systemctl --user status 'tkt-watch@*'` shows the unit running, and `tkt doctor` says so.
2. Run `herdr server stop`, then `systemctl --user start herdr.service`, from a terminal outside herdr. Attach a herdr client: the workspaces and tabs are back, and agents resumed. In a new pane, `echo $PATH` matches the shell you installed from.
3. Merge a change into `main` and make the pull request's base merge due. Within a minute, with no terminal attached, the unit syncs it: `journalctl --user -u 'tkt-watch@*'` shows the sync.
4. Reboot, or log out and in. Both units run again before you open a herdr client, and `tkt ls --json` shows `watch.running`.
5. Run `tkt watch --uninstall`. The unit stops and stays stopped after a reboot; `herdr.service` still runs.

On a Mac, the same steps with `launchctl print gui/$(id -u)/tkt.herdr` for the status and `~/Library/Logs/herdr.log` and `tkt/watch.log` in the repository's git directory for the lines; the agents start at login, not at boot.

After a change to how tkt starts pi, or after `just bump-pi`, build with `just build` and check pi itself:

1. Run `XDG_DATA_HOME=$(mktemp -d) ./tkt agent --dry-run --json`. It unpacks pi under that directory and prints the command line.
2. Run that pi with `--version`, then with the same flags and `--help`: pi must accept every flag.
3. Run `./tkt agent` in a herdr pane. The chat opens without a startup banner, with tkt's theme, and the board pinned to the top starts with `tkt`, the repository and its ticket count, which shows that tkt's extension loaded. The chat scrolls below the board, in fullscreen mode and, after a switch in `/settings`, in regular mode, where the board sits above the editor. `/login` stores its credentials under `~/.config/tkt/pi`, not `~/.pi/agent`. Quit after a reply: the last line says `tkt agent --session ID`, and that command opens the same chat.

After a change to the extension in `internal/pi/ext`, run `just install`, then `tkt agent` in a herdr pane inside a configured scratch repository:

1. Say "start ACM-1", then right away "run ACM-2 to ACM-3 in auto with fast" (a preset in config.toml). Each reply comes back in seconds while the tickets launch in their own tabs, on that preset, and the batch asks once, in one line, for both ids. With `[auto] max_parallel = 1`, ACM-3 shows as queued before it has a worktree.
2. Keep typing while they launch: nothing takes the focus, and the board shows them as starting, then planning. The auto tickets' tabs read `ACM-2 (fast, auto)`.
3. Only ACM-1's plan shows on the board as needing you, with a herdr notification; nothing new appears in the chat. The auto tickets move on the board without a word.
4. `/inbox` offers fixed answers for the plan; approve it from there, and the item leaves the board.
5. When an auto ticket's agent asks a question, the board shows it as paused. `/peek` it, then answer in its tab: the ticket goes on by itself, and the item leaves the board.
6. While an agent waits on its own CI watcher, the board shows it as waiting, not as needing you. Tell a working agent something: the chat says it went in while the agent works, and the agent's tab shows it taken at its next step. Ask to hold a message until the agent stops: the chat says it is queued, and it goes in when the agent stops.
7. When the review bot clears the head, the ticket is ready: `/check` offers verify first, then the checklist, with the steps from the pull request's body. Mark a step failed and start a fix session, then mark them all passed.
8. Leave an uncommitted file in a merged ticket's worktree, then say "ACM-1 is merged, remove it". The chat confirms first, then says the worktree has uncommitted changes. Clean it up and ask again: it reports the time from start to merge, and `/stats` lists it.
9. Quit the chat while an auto ticket works and open `tkt agent` again: the ticket kept moving, and the board shows where it is.
10. Each prompt has its time above it, and each answer its time and duration after it; quit and open the chat with `tkt agent --session ID`, and the times are still there. The board has a header, and its AGE column keeps counting from `tkt start` while TIME restarts with each phase.
11. Ask "how did ACM-1 go?": the answer names the reviews' findings and the commits that fixed them, from `tkt history`. `/board` and `/peek` draw on a solid panel on a transparent terminal.
12. On a dark transparent terminal, the whole chat sits on a solid dark background, empty rows too, in fullscreen mode and again after `/reload` and `/new`. After a switch to regular mode in `/settings`, the terminal's own background shows. Quit: the shell below the chat has the terminal's background, not the chat's.
13. Build a tkt that claims an old release: `just fetch-pi`, then `go build -tags bundled_pi -ldflags '-X main.version=v0.0.1' -o DIR/tkt .`, and run `DIR/tkt agent`. A note says the latest release is available, and the line under the board starts with `/update to vNEW`. `/update` asks first, downloads, and says it updated; answer yes to `Quit now?`. The last line says `tkt agent --session ID`; that command, with `DIR/tkt`, opens the same chat, and `DIR/tkt --version` prints vNEW. With a `just install` build, nothing names an update, and `/update` says it is a development build.
