# tkt tests

Run them from the repository root:

```bash
bash bin/tests/run            # every test
bash bin/tests/run start      # only tests whose name contains "start"
```

The runner needs bash 4.4 or newer, git, jq and the coreutils. It prints one line per test and exits non-zero when a test fails.

## How they work

Each test runs in a subshell with its own temporary `HOME`, a git repository with a bare `origin`, the lifecycle configuration for the Acme tracker (`ACM-<number>`) and a GitHub forge, and a herdr session whose only pane sits in that repository. `bin/tests/fakes/` comes first on `PATH`:

- `herdr` keeps workspaces, tabs, panes and agents in a JSON file and answers the commands tkt uses. Environment variables inject failures, such as `FAKE_HERDR_START_ERRORS=agent_pane_busy,...` or `FAKE_HERDR_PROMPT_ERRORS=agent_prompt_stalled`.
- `wt` creates and removes real git worktrees under `$FAKE_WT_ROOT` and prints worktrunk's JSON (schema 1, or schema 2 with `FAKE_WT_SCHEMA=2`).
- `gh` answers `pr list` from the JSON file in `FAKE_GH_PRS`.
- `opencode` answers the session API calls.
- `claude` fails if anything calls it, because tkt starts agents through herdr only.
- `git` logs each call, then runs the real git.

Every fake appends its arguments to `$FAKE_LOG`, so tests assert the exact commands tkt ran. `assert_house_rules` checks the rules every command keeps: no `--no-hooks`, `--clobber` or `--yes` for `wt`, no worktree changes through git or herdr, and no direct `claude` call.

Pure functions (configuration, presets, prompt guards, the review verdict and checks parsing, the next-step table) are tested by sourcing `bin/tkt`, which runs nothing when sourced.

## Manual check in herdr

The fakes cannot prove that the real tools behave as assumed. After a change to how tkt talks to herdr, worktrunk, Claude Code or OpenCode, run these from a herdr pane in a scratch repository that has the lifecycle configuration:

1. `tkt start ACM-1 --dry-run --prompt "..."`, then the same without `--dry-run`. Check the tab label, that the agent runs in build mode (Claude shows "bypass permissions on"; OpenCode shows Build and the preset's model) and that the prompt arrived whole.
2. Run the same start again: it refuses because an agent runs in the worktree.
3. `tkt run ACM-1 address` while the agent works: it refuses. Once the agent is idle: the agent exits, a new one starts and gets the address line.
4. Exit the agent, then `tkt resume ACM-1`: the newest session comes back in bypass mode.
5. `tkt ls` shows the row and a sensible next step.
6. Close the tab, then remove the scratch worktrees with `wt remove`.
