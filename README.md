# tkt

`tkt` runs the mechanical steps around the [rogs-skills](https://gitlab.com/rogs/skills) ticket lifecycle. It creates the ticket's worktree, opens a [herdr](https://herdr.dev) tab named after the ticket, starts Claude Code or OpenCode in that tab, and hands the agent its prompt. Each command is one step you take. Nothing chains stages together: you still approve plans, run the human verification steps and merge, and `tkt ls` only suggests the next command.

## Install

Build it with Go 1.26 or newer:

```bash
go install gitlab.com/rogs/tkt@latest
```

Or build it from a clone:

```bash
git clone https://gitlab.com/rogs/tkt.git
cd tkt
go build -o ~/.local/bin/tkt .
```

If `~/.local/bin/tkt` is still a symlink to the old bash script in a skills checkout, delete it first. `go build -o` would otherwise write through the link into that checkout.

Requirements at run time:

- git 2.31 or newer;
- herdr 0.9.3 or newer, with tkt running inside a herdr pane;
- [worktrunk](https://worktrunk.dev) (`wt`) 0.68 or newer;
- Claude Code or OpenCode;
- the configuration that the `/setup-rogs` skill writes into the project.

`tkt done` and `tkt ls` read pull requests through `gh`, so they support GitHub forges only.

worktrunk must name each worktree directory after its branch. Put the setup an agent needs, such as copying `.env` files and dependencies, in a `pre-start` hook. worktrunk runs `post-start` hooks in the background, so an agent could start before they finish. For example:

```toml
worktree-path = "~/code/worktrees/{{ repo }}/{{ branch | sanitize_hash }}"

[pre-start]
copy = "wt step copy-ignored"
```

## A ticket from start to merge

Each step is one command you run or one answer you give in the ticket's tab. `tkt ls` shows where every ticket stands and suggests the next step.

1. Start the ticket:
   - `xclip -selection clipboard -o | tkt start --prompt -`, with its start-ticket block on the clipboard;
   - or `tkt start ACM-12`, which sends only the `start` stage line.

   The agent investigates and ends with `Status: AWAITING_APPROVAL`.
2. Read the plan in the tab, then approve it there or ask for changes. The same session goes on to implement it with `work-ticket`, so answer in the tab. `tkt run` would discard the plan.
3. When the agent ends with `Status: READY_FOR_REVIEW`, run `tkt run ACM-12 review`. The reviewer must not be the session that wrote the change, and `tkt run` always starts a fresh one.
4. Act on the verdict the review posts:
   - `CHANGES_REQUESTED`: run `tkt run ACM-12 address`.
   - `Status: BLOCKED_FOR_SYNC`: run `tkt run ACM-12 sync`.
   - `READY_FOR_HUMAN`: go on to step 5.

   Both `address` and `sync` end with `Status: READY_FOR_REVIEW`, so go back to step 3.
5. Do the human verification steps listed in the review summary. `tkt run ACM-12 verify` has an agent run them in a browser and report each one, but you decide whether each step passed.
6. In the tab, tell the agent to merge and which steps passed, such as "Merge the PR. Human verification steps 1 to 3 passed." No stage sends this, because `finish-ticket` merges only on your explicit instruction. When a step failed, say what you saw instead. The agent records it on the pull request and ends with `Status: HUMAN_QA_FAILED`, and you run `tkt run ACM-12 address`.
7. After the merge, run `tkt done ACM-12` from another tab.

When the checks fail on the pull request's head and no review is waiting to be addressed, run `tkt run ACM-12 ci`.

`tkt --help` lists every command and option. [`bin/tests/README.md`](bin/tests/README.md) describes the tests.

## Maintainer setup

These one-time settings outside the repository keep releases and installs working.

### Cloudflare

Cloudflare sits in front of git.rogs.me. On every path outside `/api/v1`, it answers curl, wget and the Go HTTP client with a challenge (`cf-mitigated: challenge`, HTTP 403). Downloads from releases must skip that challenge.

| Path prefix on git.rogs.me | Why it must skip the challenge | Needed |
|---|---|---|
| `/rogs/tkt/releases/download/` | `install.sh`, the binaries and `SHA256SUMS` that the install commands fetch | Always |
| `/attachments/` | Gitea may redirect a release download here, depending on its storage settings | Only when the check below shows a redirect to it |
| `/rogs/tkt.git/` and `/rogs/tkt/info/refs` | HTTPS clones from git.rogs.me | Only to allow HTTPS clones from git.rogs.me. The README clones from the GitLab mirror instead |

When a custom WAF rule issues the challenge, exclude the paths in that rule's expression, for example:

```
... and not starts_with(http.request.uri.path, "/rogs/tkt/releases/download/")
```

Use a custom rule with the Skip action when a managed feature issues the challenge, such as Super Bot Fight Mode or Under Attack mode. The free plan's Bot Fight Mode cannot be skipped by path, so replace it with a custom challenge rule that has these exclusions.

Check after the first release. Expect `200` and no `cf-mitigated` header:

```bash
curl -sSI https://git.rogs.me/rogs/tkt/releases/download/latest/SHA256SUMS
```

A `303` to `/attachments/...` means that prefix needs the exclusion too.

### GitLab mirror

The Go module path is `gitlab.com/rogs/tkt`, so `go install` reads the mirror, never git.rogs.me. Keep `gitlab.com/rogs/tkt` public, and have the push mirror send tags as well as branches.

### Gitea Actions

Enable Actions on `rogs/tkt`. The workflows in `.gitea/workflows/` need a runner with the `ubuntu-latest` label. The release workflow creates releases with the job's automatic token. If that fails with a permission error, add a `RELEASE_TOKEN` secret with a personal access token that has the `write:repository` scope.
