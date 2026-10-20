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

- `herdr` keeps workspaces, tabs, panes and agents in `$FAKE_DIR/herdr.json` and answers the commands tkt uses. Environment variables inject failures, such as `FAKE_HERDR_START_ERRORS=agent_pane_busy,...` or `FAKE_HERDR_PROMPT_ERRORS=agent_prompt_stalled`. A `-` in such a list lets that call succeed. `agent read` prints `$FAKE_DIR/screen` when it exists, otherwise `FAKE_HERDR_SCREEN`.
- In turns mode, `FAKE_HERDR_TURNS` names a JSON list of agent turns, such as `[{"screen": "Status: AWAITING_APPROVAL"}, {"screen": "...", "prs": "/path/prs.json"}]`. A prompt sets the agent working, and each `agent wait` on a working agent ends its turn with the next entry: its `status` (idle by default), its screen, and a pull request file that `gh` lists from then on. The `auto_` scripts drive whole tickets this way.
- `wt` creates and removes real git worktrees under `$FAKE_WT_ROOT` and prints worktrunk's JSON: schema 1, or schema 2 with `FAKE_WT_SCHEMA=2`.
- `gh` answers `pr list` from `$FAKE_DIR/prs.json` when it exists, otherwise from the JSON file in `FAKE_GH_PRS`.
- `opencode` answers the session API calls, and a session's messages from the JSON file in `FAKE_OC_MESSAGES`.
- `claude` fails if anything calls it, because tkt starts agents through herdr only.
- `git` logs each call, then runs the real git.

The `update_` scripts point `TKT_RELEASES` and `TKT_RELEASES_API` at `file://` URLs under `$WORK`, and set `TKT_UPDATE_TARGET` to the file `tkt update` replaces. Only the tests set `TKT_UPDATE_TARGET`: without it, `tkt update` would replace the shared test binary.

Every fake appends its command line to `$FAKE_LOG`, each word quoted like bash `printf %q`.

The scripts use these commands beyond testscript's own:

| Command | Checks or does |
|---|---|
| `rc N` | The last `tkt` run exited with status N. |
| `logged WORDS...` | The log has a line containing the words, quoted as the log quotes them. |
| `logcontains TEXT` | The log holds TEXT exactly. |
| `resetlog` | Forgets the commands and prompts so far. |
| `houserules` | No `wt` run used `--no-hooks`, `--clobber` or `--yes`; no git or herdr command changed worktrees; nothing ran `claude` directly. |
| `jqedit FILE EXPR`, `jqeq FILE EXPR WANT` | Edits or checks a JSON file with a jq expression. Expressions can read `$REPO`, `$WORK`, `$WT`, `$FAKE_WT_ROOT`, `$FAKE_DIR`, `$HOME`, `$SID` and `$H1` to `$H3`. |
| `state DIR KEY WANT`, `statevar DIR KEY VAR` | Checks or reads the ticket state of the worktree in DIR. |
| `mkprompt FILE ID [LINE...]` | Writes a start-ticket prompt for ID. |
| `realgit ARGS...` | Runs the real git without logging, for setup. |
| `gitvar VAR DIR REV` | Sets VAR to a commit. |
| `claudedir VAR PATH` | Sets VAR to Claude Code's project directory for PATH. |
| `touchat FILE DATE` | Creates FILE with a modification time. |
| `mkrelease TAG` | Publishes a fake release TAG under `$WORK/rel`: a binary for each system that logs `new-tkt ARGS` and prints `tkt version TAG`, and `SHA256SUMS`. |
| `envelope [EXPR WANT]` | stdout holds exactly one JSON envelope, and EXPR applied to it gives WANT. |

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
