# The ticket lifecycle

This page follows one ticket, `ACM-123`, from the tracker to a merged pull request. It shows which tool does each step, which [rogs-skills](https://gitlab.com/rogs/skills) skill runs, what tkt does around it, and which line tells you a step is over. `tkt help lifecycle` has the short version for the terminal.

## The parts

| Part | What it does here |
|---|---|
| The tracker | Holds the ticket: Linear, Jira, GitHub Issues, Kaneo, local markdown files, or anything else `/setup-rogs` can describe. The skills read and write it through its MCP server or CLI. tkt never talks to it. |
| The forge | Holds the branch and the pull request: GitHub, Gitea or GitLab. The skills drive it through `gh`, `tea` or `glab`. tkt reads it through `gh`, so `tkt ls` and `tkt done` support GitHub only, and pushes one thing to it: a base merge that merges clean. |
| CI | Runs the checks on every push. Every claim that CI is green names the exact head it ran on. |
| The skills | Do the thinking: plan, implement, review, fix, merge. Each one ends with a `Status:` line and never starts the next one. |
| `docs/agents/*.md` | The project's lifecycle configuration, written by `/setup-rogs`. The skills read all of it; tkt reads the ticket id pattern and the `## Forge` fields. |
| Claude Code or OpenCode | The harness that runs a skill. tkt calls the pair of harness and model a preset. |
| herdr | The terminal multiplexer. It holds one tab per ticket and knows whether each agent is working, blocked, idle or done. |
| worktrunk (`wt`) | Creates and removes worktrees, and runs their hooks. |
| tkt | Runs the mechanical steps between stages: worktrees, tabs, agent sessions, prompts, clean base merges and cleanup. `tkt watch` takes the upkeep steps for every ticket and starts the chores you schedule, and `tkt auto` chains all of them for a ticket you hand over. |
| You | Approve the plan (or let `tkt auto --approve-plan` approve it), run the human verification steps, and merge. |

```mermaid
flowchart LR
  you([You])
  subgraph local[Your machine]
    tkt[tkt]
    wt[worktrunk<br/>one worktree per ticket]
    herdr[herdr<br/>one tab per ticket]
    agent[Claude Code or OpenCode<br/>running a skill]
    config[docs/agents/*.md<br/>from /setup-rogs]
  end
  subgraph remote[Remote services]
    tracker[(Tracker)]
    forge[(Forge)]
    ci[CI]
  end
  you -->|runs commands| tkt
  you -->|approves, verifies, says merge| agent
  tkt -->|switch, remove| wt
  tkt -->|tab, agent start, prompt| herdr
  tkt -->|ticket ids, forge fields| config
  tkt -.->|gh: PR, checks, verdict| forge
  tkt -->|git push: a clean base merge| forge
  herdr -->|runs| agent
  agent -->|reads| config
  agent -->|MCP or CLI| tracker
  agent -->|push, PR, review, merge| forge
  forge -->|every push| ci
```

tkt reads the forge through `gh`, the dotted line, and changes it in one way only: when a ticket's base moved and the merge is clean, `tkt sync` makes the merge commit and pushes it, with no agent (see [Syncing with the base](#syncing-with-the-base)). Everything else that changes the tracker or the forge happens inside an agent, under a skill's rules.

## Before the first ticket

Run `/setup-rogs` once in the repository and commit the files it writes to the base branch. Ticket worktrees branch from the base, so a configuration that only lives in your main checkout never reaches them. Then run `tkt doctor`: it checks git, herdr, worktrunk, the harnesses, `gh`, and the configuration.

worktrunk must name each worktree's directory after its branch. Every lifecycle skill checks that the branch, the directory name and the ticket id are the same string, and stops when they differ.

## The whole flow

```mermaid
flowchart TD
  ticket[ACM-123 in the tracker, state new]
  ct[create-ticket<br/>optional: audit, preview, create]
  start[tkt start ACM-123<br/>worktree, tab, agent, start line]
  st[start-ticket<br/>move to in-progress, investigate, plan]
  plan{You read the plan}
  wk[work-ticket<br/>commits, PR, exact-head CI, handoff]
  review[tkt run ACM-123 review<br/>review-pr in a fresh session]
  verdict{Verdict at the head}
  address[tkt run ACM-123 address<br/>address-pr-review: merge the base, then at most two rounds]
  sync[tkt sync ACM-123<br/>merge a clean base, or resolve a conflict in a fresh session]
  cifix[tkt run ACM-123 ci<br/>fix the failing checks, push once]
  human[You run the human verification steps<br/>optional: tkt run ACM-123 verify]
  merge[tkt merge ACM-123 --passed 1-3<br/>finish-ticket in a fresh session]
  done[tkt done ACM-123<br/>close the tab, remove the worktree]

  ct -.-> ticket
  ticket --> start
  start --> st
  st -->|Status: AWAITING_APPROVAL| plan
  plan -->|ask for changes in the tab| st
  plan -->|approve in the tab| wk
  wk -->|Status: READY_FOR_REVIEW| review
  review --> verdict
  verdict -->|Verdict: CHANGES_REQUESTED| address
  verdict -->|Status: BLOCKED_FOR_SYNC, STALE_BASE or a conflict| sync
  verdict -->|checks fail at the head| cifix
  verdict -->|Verdict: READY_FOR_HUMAN| human
  address -->|Status: READY_FOR_REVIEW| review
  sync -->|a new head| review
  cifix --> review
  human --> merge
  merge -->|Status: HUMAN_QA_FAILED| address
  merge -->|Status: BLOCKED_FOR_SYNC| sync
  merge -->|Status: DONE| done
```

Every arrow out of a stage is a status line or a verdict on the screen or the pull request. Every arrow into a stage is something you do: a tkt command or an answer in the tab. `tkt watch` takes the upkeep arrows for every ticket: it syncs a moved base, fixes failing checks once per head, and runs `tkt done` after the merge (see [tkt watch keeps the tickets moving](#tkt-watch-keeps-the-tickets-moving)). The other arrows move only when you take them, or when you hand the ticket to `tkt auto`, which takes the same steps for you and pauses where a person decides (see [Letting tkt auto drive](#letting-tkt-auto-drive)). `tkt ls` reads the same facts and prints the next step for each ticket, and `tkt wait ACM-123` blocks until the agent finishes or needs you.

## Step by step

### 1. The ticket exists

A ticket starts in the tracker, in the state the configuration maps to `new`, or in `blocked` while a ticket it waits on is open. You can write it by hand, or ask an agent in the main checkout to run `create-ticket`. That skill audits the code for work already done, searches the tracker for duplicates, shows a preview and waits with `Status: AWAITING_CREATE_APPROVAL`. Only after you approve does it create the tickets and their blocked-by relations, read them back, and end with `Status: TICKETS_CREATED`.

tkt plays no part here. It needs only the id.

To pick which ready tickets to start, run `tkt suggest 3`, or `/suggest 3` in the tkt agent. A fresh session in a suggest tab at the repository's root runs `suggest-tickets`, which only reads: it keeps the ready tickets whose blockers are all done, ranks them by how many tickets wait on each and then by priority, and drops one that would collide with work in flight, judged from the files each in-flight branch changes, or with another pick. With model routing on, a batch gets at most one heavy ticket. It lists what waits on you first, then ends with the picks:

```
Suggested: ACM-123 (frontier), ACM-130 (acme-small-2)
Status: TICKETS_SUGGESTED
```

`tkt suggest --wait` prints the `tkt start` command for each, on the first preset that serves its model; the tkt agent's board shows the picks, and `/inbox` starts them by hand, in full auto, or in auto after you approve each plan. `[suggest] as` in `config.toml` names a cheap preset for the run.

### 2. tkt start creates the workspace

```bash
tkt start ACM-123
```

To send a longer prompt, such as a start-ticket block with a `Model:` line, pipe it in: `xclip -selection clipboard -o | tkt start --prompt -`.

```mermaid
sequenceDiagram
  participant you as You
  participant tkt
  participant wt as worktrunk
  participant herdr
  participant agent as Agent
  you->>tkt: tkt start ACM-123
  tkt->>tkt: read docs/agents/*.md, check the id
  tkt->>tkt: git fetch origin main
  tkt->>wt: wt switch --create ACM-123 --base origin/main
  wt->>wt: create the worktree, run pre-start hooks
  tkt->>herdr: open a tab named ACM-123
  tkt->>herdr: herdr agent start, in build mode
  herdr->>agent: launch Claude Code or OpenCode
  tkt->>herdr: herdr agent prompt with the start line
  herdr->>agent: "Use the start-ticket skill to start ticket ACM-123."
  tkt->>tkt: record preset, session, tab and pane
  tkt-->>you: worktree, tab, agent
```

The new branch starts from the base on the remote: tkt fetches it first, so the plan starts from the code the base holds now, not from a local `main` that nobody pulled. When the fetch fails, it warns and starts from the local base.

The tab's `TMPDIR` is a private directory for the ticket, such as `/tmp/tkt-acme-app-ACM-123-123456`, so `mktemp`, Python, Node, Go and `gh` in two tickets' agents never write the same temporary file. Claude Code runs its tools in the tab, so its agents have it. OpenCode runs every agent's tools in its one background service instead, which the tab's environment never reaches: `tkt hook tmp --install` puts tkt's plugin into OpenCode, which gives each shell in a ticket's worktree that ticket's `TMPDIR`, and moves into the ticket's directory a new `/tmp` path, such as `/tmp/pr-body.md`, that a file tool names or a shell command uses unquoted. A path in quotes, as in a search or a message, stays as written. `tkt doctor` warns while it is missing.

tkt keeps the ticket's preset, session, tab and pane in the worktree's git directory, so the record disappears with the worktree. The prompt's `Model:` line, when it has one, picks the first preset that serves that model.

### 3. start-ticket plans

The agent checks that it sits in the right worktree, loads the ticket with every comment and relation, and looks for blockers among the tickets in flight. When something blocks the ticket, it moves the ticket to `blocked` with a comment that says why, and stops with `Status: BLOCKED`. Otherwise it moves the ticket to `in-progress`, so the board shows it started, and reads the code end to end. It writes no code. It returns a plan that opens with a plain overview and diagrams, maps every acceptance criterion to a step, and names the seams the tests will cover. When plan review is on, a model from another vendor reviews the plan through `review-plan` before you see it. When plan presentation is on, `present-plan` shows it in a browser.

The agent ends with `Status: AWAITING_APPROVAL`. When you drop the ticket instead of approving a plan, tell the agent, and it moves the ticket back.

### 4. You approve, and work-ticket builds

Read the plan with `tkt show ACM-123`, which prints the agent's last message from its session's record. Ask for changes with `tkt send ACM-123 "what to change"` until the plan is right, then approve it with `tkt approve ACM-123`. Both type into the planning session, as an answer in the tab would. Do not use `tkt run` here: it starts a fresh session, and the plan lives in this one. The same session goes on with `work-ticket`:

1. It posts the plan as an `## Approved implementation plan` comment, after moving the ticket to `in-progress` if it is not there yet.
2. It implements the plan, test-first where the configuration says so, and commits each unit through `commit`.
3. It merges the base into the branch, runs the full check, and pushes. Nothing rebases or force-pushes.
4. With the browser pass on, it captures each visible acceptance criterion with `playwright-cli`.
5. It opens the pull request through `create-pr`, with a review brief and a numbered human verification procedure.
6. It waits for CI on the exact head and checks that the head contains the current base.
7. It moves the ticket to `in-review` and posts a handoff comment on it.

It ends with `Status: READY_FOR_REVIEW`.

### 5. review-pr reviews in a fresh session

```bash
tkt run ACM-123 review
```

`tkt run` closes the idle agent and starts a new session in the same pane, so the reviewer is never the session that wrote the change. The new session remembers nothing and needs nothing: it reads the branch, the pull request and the tracker.

`review-pr` refuses to review a stale head. When the checks are not green for the exact head, or the base moved past it, it posts nothing and ends with `Status: BLOCKED_FOR_SYNC`. Otherwise it reviews the whole diff against every acceptance criterion, tries to disprove each finding before posting, and posts one review. The summary names the reviewed head and base and carries one of two verdicts:

- `Verdict: CHANGES_REQUESTED`, ending with `Status: REVIEW_POSTED`;
- `Verdict: READY_FOR_HUMAN`, ending with `Status: READY_FOR_HUMAN`, with the final human verification procedure in the summary.

When the forge runs an automated reviewer in CI instead, it posts an `Automated review: CLEAR`, `FINDINGS` or `STALE_BASE` line, and tkt reads that the same way: the bot's `CLEAR` counts like `READY_FOR_HUMAN`, and the steps to verify are then the pull request's own Human verification section. A verdict counts only for the head it names, so after every push the ticket waits for a new review.

### 6. The review loop

Act on the verdict, then review again:

| What you see | Run | What happens |
|---|---|---|
| `Verdict: CHANGES_REQUESTED`, or comments you left | `tkt run ACM-123 address` | `address-pr-review` first merges the base when it moved, resolving conflicts. Then it checks each finding, fixes the valid ones at their root cause, answers the rest with evidence, pushes, waits for CI and replies to every comment. It runs at most two rounds and never resolves a thread. |
| `Status: BLOCKED_FOR_SYNC` with the branch behind, the bot's `STALE_BASE`, or a conflict | `tkt sync ACM-123` | tkt merges the base itself when the merge is clean, and pushes it in seconds. On a conflict, a fresh session runs the `resolve` stage, which only merges and resolves. See [Syncing with the base](#syncing-with-the-base). |
| The checks fail at the head, with no review to address | `tkt run ACM-123 ci` | A fresh agent finds the cause, fixes it, and pushes once. An infrastructure failure gets a rerun instead of a code change. |

`address` ends with `Status: READY_FOR_REVIEW`, which leads back to step 5, and so does a sync, since its new head needs a review too. On a re-review, `review-pr` checks each earlier thread against the new head and resolves the ones that are fixed.

### 7. You verify

When the verdict is `READY_FOR_HUMAN`, or the bot's `CLEAR`, run the numbered steps yourself; `tkt show ACM-123 --steps` prints them from the review summary, or from the pull request's body after the bot. `tkt run ACM-123 verify` can have an agent run them in a browser and report what it saw for each one. It changes no code and posts nothing, and you still decide whether each step passed.

### 8. finish-ticket merges

`finish-ticket` merges only on your explicit word, so no stage line sends it. Once every step passed, `tkt merge ACM-123 --passed 1-3` sends that word for you, to a fresh session in the ticket's tab:

```
Use the finish-ticket skill to merge the PR. Human verification steps 1 to 3 passed on head 3f2a....
```

`--passed all` names every step, and `/check` in the tkt agent runs it when you mark the last one passed. Before any session starts, tkt merge refuses a list that leaves a step out, a head the steps cannot carry to (`--head`), a head no review cleared that no sync made, a base whose merge conflicts, and failing checks at a head it would merge as it is; `finish-ticket` checks the rest itself. When finish-ticket could not merge yet, tkt merge leaves a merge order instead, and `tkt watch` carries it out:

```mermaid
flowchart LR
  verified["your steps passed<br/>on head A"] --> behind{"behind the base?"}
  behind -->|yes| sync["tkt watch merges the base:<br/>head B, a clean merge"]
  sync --> review["review-pr reviews B"]
  behind -->|no| green{"checks green,<br/>no other merge running?"}
  review --> green
  green -->|yes| line["finish-ticket gets the line:<br/>steps passed on A, now at B"]
  line --> merged["it checks the merges<br/>between A and B, and merges"]
```

The steps carry over clean merges of the base: finish-ticket counts a confirmation given on an earlier head when every commit after it is a merge commit whose tree equals what `git merge-tree` gives for its two parents, the merge `tkt sync` makes, and it checks each one itself. finish-ticket merges on review-pr's `READY_FOR_HUMAN` at the head it merges, so a sync brings a fresh review-pr review, and so does a head only the review bot cleared; `tkt watch` starts it. One merge goes at a time, since a second finish-ticket that checks the base before the first one merges would merge an integration nobody reviewed. So you can verify three tickets, leave three orders, and walk away: each one syncs, gets its review and merges after the one before it.

The order stands until the pull request merges: when finish-ticket stops with `BLOCKED_FOR_SYNC` because another merge moved the base, `tkt watch` syncs again and sends the line on the new head. A new head of any other kind, a fix or the resolution of a conflict, drops the order with a notification, since the steps ran on code it changed; so does finish-ticket's `HUMAN_QA_FAILED`, and another session taking the ticket over after the line went out. `tkt merge ACM-123 --cancel` drops an order, and says when the line went out. To waive a step, or to merge after a step failed, tell the agent in the tab yourself.

Before it merges, `finish-ticket` requires green CI for the exact head, a `READY_FOR_HUMAN` verdict for that same head and base, no open feedback, and your confirmation of every human step. It then merges, checks that the merge commit's tree equals the reviewed head's, moves the ticket to `done`, waits for post-merge CI, and posts a `## Completion` comment.

| Ending | Next |
|---|---|
| `Status: DONE` | Step 9. |
| `Status: HUMAN_QA_FAILED` | A step failed. The agent records what you saw on the pull request; run `tkt run ACM-123 address`. |
| `Status: BLOCKED_FOR_SYNC` | The base moved after the review. Even a clean merge would ship an integration nobody reviewed, so the new head needs a review. Your merge order stands: `tkt watch` syncs, has the new head reviewed, and sends the line again, and your steps carry over the clean merge. |
| `Status: BLOCKED_POST_MERGE` | The merge landed but post-merge CI failed. The ticket is `done`, since its change merged, but has no `## Completion` comment yet; look at the base branch's checks. |

### 9. tkt done cleans up

```bash
tkt done ACM-123
```

`tkt watch` runs it for you once `finish-ticket` ends with `Status: DONE`; otherwise, run it from another tab. It confirms through `gh` that the pull request merged, closes the ticket's tabs, removes the worktree through `wt remove`, and removes the ticket's temporary directory. It keeps the worktree, and says why, when the worktree has uncommitted changes, the branch tip is neither the merged head nor in the base, or an agent still works there. `tkt done --all` checks every ticket worktree at once. It also records the merge in tkt's history, so `tkt stats` can tell how long the ticket took from `tkt start` to the merge, and how much of that it waited for you.

`finish-ticket` ends its report with the tickets the merge unblocked, the ones whose blockers are all done now, right before `Status: DONE`:

```
Unblocked: ACM-124, ACM-125
Status: DONE
```

`tkt done` reads that line from the ticket's session before it closes the tabs, and names the tickets, with no extra agent run. `tkt ls` lists each one until it starts, and the tkt agent's board offers to start it by hand, in auto, or in auto after you approve its plan.

## Letting tkt auto drive

`tkt auto ACM-123` takes the steps above for one ticket and pauses where a person decides. It reads the same facts as `tkt ls`, so it does nothing you could not do by hand:

| The ticket | tkt auto |
|---|---|
| Has no worktree yet | Runs `tkt start`. With `--approve-plan`, the start line says the run is unattended: start-ticket skips the browser plan presentation and asks every open question with the harness's question tool, so no decision hides in a plan nobody reads. |
| The agent works, or waits on its own background task such as a CI watcher | Waits for it. |
| `Status: AWAITING_APPROVAL` | Runs `tkt approve` with `--approve-plan`; without it, pauses for you. |
| Checks pending | Waits for CI. |
| A new head with green CI and no review | Waits for the review bot in CI. |
| Bot `STALE_BASE`, a conflict, or `BLOCKED_FOR_SYNC` behind the base | Syncs: when `git merge-tree` finds the merge clean, it merges and pushes it as `tkt sync` does, with no agent and no fix round; on a conflict it runs `tkt run ACM-123 resolve`. |
| Bot `FINDINGS`, or `Verdict: CHANGES_REQUESTED` | Runs `tkt run ACM-123 address`, which merges the base first. |
| Checks fail | Runs `tkt run ACM-123 ci`. |
| Bot `CLEAR`, or `Verdict: READY_FOR_HUMAN` at the head | Stops: the ticket is yours to verify and merge. |
| No bot review after `[review] bot_wait` (15 minutes) | Pauses for you. With no bot (`bot_wait = "0s"`) it runs `tkt run ACM-123 review` instead. |
| A status page reports an incident (see [tkt watch keeps the tickets moving](#tkt-watch-keeps-the-tickets-moving)) | Waits: it starts, approves and runs nothing, and spends no round, until the incident ends. A sync goes on. |

Where a person decides, it pauses rather than ends: when the agent asks a question or ends with `Status: BLOCKED`, when no agent runs, after `[auto] max_rounds` fix runs (5), and when a step would repeat on the same head with nothing changed. It notifies once, shows the ticket as paused, and goes on by itself when the ticket moves, such as after you answer the agent in its tab. While it drives a ticket, the tab reads `ACM-123 (auto)`. `tkt hold ACM-123` pauses the loop until `tkt hold ACM-123 --off`, without a notification, for a ticket you want to park or drive by hand. At most `[auto] max_parallel` tkt auto agents (3) work at once in a repository; a loop gives up its slot while it waits for CI, the bot or you, and the others wait, queued.

`tkt agent` opens a chat that does all of this for many tickets at once: you say "start ACM-123 in auto", get the prompt back, and hear from it only when a ticket needs you.

## tkt watch keeps the tickets moving

`tkt watch` runs one per repository and keeps every ticket moving, not only the ones you hand to `tkt auto`. Every `[watch] interval` (60 seconds) it fetches once and reads every ticket with a fixed number of `gh` calls, however many there are. Then it takes at most one step per ticket:

```mermaid
flowchart TD
  pass(["every 60s: one fetch, one read of every ticket"]) --> skip{"on hold, driven by tkt auto,<br/>locked, or agent working?"}
  skip -->|yes| leave["leave it for now"]
  skip -->|no| merged{"PR merged and finish-ticket<br/>said Status: DONE?"}
  merged -->|yes| done["tkt done"]
  merged -->|no| behind{"the base moved at a moment<br/>a sync is due?"}
  behind -->|clean| sync["merge it, no agent"]
  behind -->|conflict| resolve["resolve in a fresh session"]
  behind -->|no| ci{"checks fail at the head?"}
  ci -->|yes| fix["ci in a fresh session,<br/>once per head, up to max_rounds"]
  ci -->|no| order{"your merge order holds here,<br/>checks green?"}
  order -->|"yes, reviewed, no other merge running"| merge["tkt merge: finish-ticket<br/>in a fresh session"]
  order -->|"yes, a sync left it unreviewed"| review["review-pr in a fresh session"]
  order -->|no| leave
```

Plans, findings and human verification stay with you, or with `tkt auto`, and so do reviews and the word to merge: watch merges only through the merge order `tkt merge` leaves when you verified a head finish-ticket could not merge yet, and it reviews only the head a sync made for such an order. A step watch would repeat on the same head, or a fix run past `[auto] max_rounds`, waits for you instead: the ticket shows as `needs_you`, and herdr notifies once. Its agent runs take the same `[auto] max_parallel` slots as `tkt auto`'s, and every step goes into the history.

While a status page that `config.toml` names reports a problem, such as a GitHub Actions outage or a model provider's, `tkt watch` starts no agent: no `ci`, `resolve` or `review` run, no merge line and no chore run, since each would only spend a round on a service that is down. Syncs and removals, which need no agent, go on, and `tkt auto` loops wait the same way. It reads the pages every five minutes, notifies when an incident starts and when it ends, and `tkt ls` and the tkt agent's board show it while it lasts:

```toml
[status.github]
page = "https://www.githubstatus.com"
components = ["Actions", "Pull Requests", "Git Operations"]
```

You rarely start it yourself: every tkt command in a herdr pane starts one when none runs for the repository, in a session of its own, so it outlives the command and the pane. `[watch] autostart = false`, or `TKT_WATCH=off`, turns that off; `tkt watch --stop` stops it, after the step it is taking; `tkt watch --once --dry-run` says what one pass would do to each ticket. Outside a herdr pane, it finds the workspace whose panes work in the repository, or opens one there before its first step when none has a pane; while herdr is down it waits. `tkt hold ACM-123` keeps it, and `tkt auto`, away from a ticket.

`tkt watch --install` keeps it running while no terminal is open: it makes `tkt watch` and herdr's server systemd user units, or launchd agents on macOS, that start at boot, with the `PATH` of the shell you ran it from. On Linux that takes `loginctl enable-linger`, which it asks about: without it, systemd stops the units when your last session ends, so herdr's stays off. Every tkt command then starts the unit rather than a watch of its own, and `tkt watch --stop` stops the unit. `tkt doctor` checks that both run and that their `PATH` finds the harnesses.

## Chores

A chore is an agent run you schedule in `config.toml`, such as a dependency upgrade twice a week. It has no ticket: each run is a branch and a pull request.

```mermaid
flowchart LR
  slot(["the chore's slot comes"]) --> open{"its last run<br/>still open?"}
  open -->|yes| skip["skip this slot"]
  open -->|no| run["a branch from origin/main,<br/>a worktree, a tab, an agent"]
  run --> change{"anything<br/>to change?"}
  change -->|"no: Status: DONE"| gone["tkt watch removes the run"]
  change -->|"yes: Status: READY_FOR_REVIEW"| pr["tkt watch syncs the base<br/>and fixes the checks"]
  pr --> you(["you merge it, or close it"])
  you --> gone
```

The repository's `tkt watch` starts a run when its slot comes, once per slot, however many passed while no watch ran; the first watch to see a chore starts its first run at the next slot. `tkt chore run deps` starts one now. Each run gets a branch from the base on the remote, named by the chore's `branch` setting, such as `deps-2026-10-26`, and never an older run's branch. Its agent gets the chore's prompt and one rule from tkt: commit on the run's branch, push it, open one pull request to the base, and end with `Status: READY_FOR_REVIEW`, or with `Status: DONE` and nothing pushed when there is nothing to change.

From there the pull request moves as a ticket's does, short of what needs a ticket. `tkt watch` syncs its base when a sync is due and runs `ci` once per head when its checks fail. review-pr, address-pr-review and finish-ticket each start from a ticket, so none of them takes a chore's pull request: the review bot in CI reviews it, `tkt ls` says when the review found something and when the pull request is ready, and you merge it yourself, or close it to drop the run. `tkt watch` then removes the run, as `tkt done` would. A slot that comes while the last run is still open is skipped, so one run waits for you at a time. A run whose start stopped before its agent got the prompt, as on a pre-start hook that failed, waits for you too: `tkt chore run deps` goes on with it.

## Syncing with the base

When another ticket merges, the base moves, and the branches still open fall behind it. `tkt sync ACM-123` brings one up to date:

```mermaid
flowchart LR
  moved["the base moved"] --> probe{"git merge-tree<br/>HEAD origin/main"}
  probe -->|clean| merge["tkt merges and pushes<br/>in seconds, no agent"]
  probe -->|conflict| agent["fresh session merges<br/>and resolves, pushes once"]
  merge --> next["CI and the review bot<br/>run on the new head"]
  agent --> next
```

`git merge-tree` tests the merge in memory, without touching the worktree or the branch. A clean merge becomes a merge commit and a normal push; tkt never rebases and never force-pushes, and when the push fails it undoes its merge. A conflict goes to the `resolve` stage, which merges the base, resolves the conflicts with `resolving-merge-conflicts`, changes nothing else, refreshes the pull request's review brief when it has one, and pushes once. Review findings stay for `address`.

A clean merge does not make the pull request's review brief stale: the merge commit's tree equals what `git merge-tree` gives for its two parents, and review-pr checks that for itself instead of trusting tkt.

A sync costs a CI run and a review, so tkt syncs a ticket that is behind only at the moments that need it:

| Moment | How tkt knows | Why it can't wait |
|---|---|---|
| The branch conflicts | The forge reports the pull request as conflicting, or `git merge-tree` finds a conflict | Nothing merges, and conflicts only grow. |
| A review can't happen | The bot posts `STALE_BASE`, or review-pr stops with `BLOCKED_FOR_SYNC` | review-pr reviews only a head that contains the current base. |
| You are about to verify | You open `/check` in the tkt agent on a ticket that is behind | `/check` offers to sync first, so your steps run on the code that will merge; verify the head as it is, and the steps carry over the sync your merge order gets. |
| A merge is due | A merge order on a ticket that is behind, or finish-ticket stops with `BLOCKED_FOR_SYNC` | finish-ticket merges only a head reviewed against the current base; the steps you verified carry over the clean merge. |

A ticket without a pull request waits, since work-ticket merges the base itself right before it pushes, and so does a reviewed ticket that nobody verifies or merges yet. With five tickets in flight, syncing every branch whenever the base moves would cost four CI runs and four reviews per merge; at these moments a merge costs a sync only for the tickets that reach one, usually the next one you merge.

## Who changes the tracker

Each skill moves a ticket as soon as its new state is true, so the board stays current:

```mermaid
stateDiagram-v2
  state "new" as new
  state "in-progress" as progress
  state "in-review" as review
  state "done" as done
  [*] --> new: create-ticket, or by hand
  new --> progress: start-ticket, once nothing blocks it
  progress --> review: work-ticket, once exact-head CI passes
  review --> done: finish-ticket, once the merge is verified
  done --> [*]
```

work-ticket also moves a ticket that is still `new` before its first edit, and start-ticket moves the ticket back when you drop it instead of approving a plan.

A ticket that waits on a blocker sits in `blocked`, with a `## Blocked` comment that says why. create-ticket creates a ticket there while a ticket it waits on is open, and start-ticket, work-ticket and address-pr-review move one there when they stop on a blocker. It goes back to the state it had, with an `## Unblocked` comment, when finish-ticket merges its last blocker, or when the next run on it finds its blockers cleared. When the tracker has no blocked state, the ticket keeps its state, and only the comment shows the block.

`review-pr` never writes to the tracker. `address-pr-review` writes only replacement browser captures and the blocked moves. tkt never touches the tracker at all.

## The rules that hold it together

- One ticket, one branch, one worktree, one tab, all named `ACM-123`. A chore's run is the same, named after its branch.
- The implementer never reviews its own change. `tkt run` always starts a fresh session, which makes that easy.
- The fixer never resolves review threads; the next review does.
- Only `finish-ticket` merges, and only when you say so.
- Every claim that CI passed, the change was reviewed, or the steps were verified names the exact head, and that head contains the current base.
- Branches take the base by merging it. Nothing rebases or force-pushes a published branch, `tkt sync` included.
- The skills never start one another. tkt suggests the next step in `tkt ls`; you run it, `tkt watch` runs the upkeep steps, or `tkt auto` runs it for a ticket you hand over. Neither merges a pull request.

## When something stops

| You see | Do |
|---|---|
| `Status: SETUP_REQUIRED` | Run `/setup-rogs` and commit its files to the base. |
| `Status: BLOCKED` with a `Reason:` line | Read the reason in the tab. It names what a person has to decide or fix. When a blocker holds the ticket, the tracker shows it in `blocked`, with the reason in a comment. |
| The agent waits for an answer | Answer it in the tab. `tkt ls` shows it as blocked. |
| No agent runs for the ticket | `tkt resume ACM-123` brings back the last session and sends nothing. Use it, not `tkt run`, when the conversation matters, such as a plan waiting for approval. |
| The pull request closed without merging | Decide by hand. tkt does not guess. |

`tkt ls` applies these rules in order and prints the next step. `tkt help ls` lists the rules.
