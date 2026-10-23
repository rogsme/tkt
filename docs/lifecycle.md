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
| tkt | Runs the mechanical steps between stages: worktrees, tabs, agent sessions, prompts, clean base merges and cleanup. `tkt watch` takes the upkeep steps for every ticket, and `tkt auto` chains all of them for a ticket you hand over. |
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
  st[start-ticket<br/>read-only investigation and plan]
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

A ticket starts in the tracker, in the state the configuration maps to `new`. You can write it by hand, or ask an agent in the main checkout to run `create-ticket`. That skill audits the code for work already done, searches the tracker for duplicates, shows a preview and waits with `Status: AWAITING_CREATE_APPROVAL`. Only after you approve does it create the tickets and their blocked-by relations, read them back, and end with `Status: TICKETS_CREATED`.

tkt plays no part here. It needs only the id.

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

The agent checks that it sits in the right worktree, loads the ticket with every comment and relation, looks for blockers among the tickets in flight, and reads the code end to end. It writes no code. It returns a plan that opens with a plain overview and diagrams, maps every acceptance criterion to a step, and names the seams the tests will cover. When plan review is on, a model from another vendor reviews the plan through `review-plan` before you see it. When plan presentation is on, `present-plan` shows it in a browser.

The agent ends with `Status: AWAITING_APPROVAL`, and the tracker is unchanged.

### 4. You approve, and work-ticket builds

Read the plan with `tkt show ACM-123`, which prints the agent's last message from its session's record. Ask for changes with `tkt send ACM-123 "what to change"` until the plan is right, then approve it with `tkt approve ACM-123`. Both type into the planning session, as an answer in the tab would. Do not use `tkt run` here: it starts a fresh session, and the plan lives in this one. The same session goes on with `work-ticket`:

1. It posts the plan as an `## Approved implementation plan` comment and moves the ticket to `in-progress`.
2. It implements the plan, test-first where the configuration says so, and commits each unit through `commit`.
3. It merges the base into the branch, runs the full check, and pushes. Nothing rebases or force-pushes.
4. With the browser pass on, it captures each visible acceptance criterion with `playwright-cli`.
5. It opens the pull request through `create-pr`, with a review brief and a numbered human verification procedure.
6. It waits for CI on the exact head and checks that the head contains the current base.
7. It posts a handoff comment on the ticket and moves it to `in-review`.

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
Use the finish-ticket skill to merge the PR. Human verification steps 1 to 3 passed.
```

`--passed all` names every step, and `/check` in the tkt agent runs it when you mark the last one passed. tkt merge refuses what `finish-ticket` would: a list that leaves a step out, a head the steps did not run on (`--head`), one no review cleared, a branch behind its base, and failing checks. While the checks at the head still run, it leaves a merge order instead, and `tkt watch` sends the line once they pass; a new head drops the order, since the steps ran on the old one. To waive a step, or to merge after a step failed, tell the agent in the tab yourself.

Before it merges, `finish-ticket` requires green CI for the exact head, a `READY_FOR_HUMAN` verdict for that same head and base, no open feedback, and your confirmation of every human step. It then merges, checks that the merge commit's tree equals the reviewed head's, waits for post-merge CI, posts a `## Completion` comment, and moves the ticket to `done`.

| Ending | Next |
|---|---|
| `Status: DONE` | Step 9. |
| `Status: HUMAN_QA_FAILED` | A step failed. The agent records what you saw on the pull request; run `tkt run ACM-123 address`. |
| `Status: BLOCKED_FOR_SYNC` | The base moved after the review. Even a clean merge would ship an integration nobody reviewed, so run `tkt sync ACM-123` and review again. |
| `Status: BLOCKED_POST_MERGE` | The merge landed but post-merge CI failed. The ticket stays in `in-review`; look at the base branch's checks. |

### 9. tkt done cleans up

```bash
tkt done ACM-123
```

`tkt watch` runs it for you once `finish-ticket` ends with `Status: DONE`; otherwise, run it from another tab. It confirms through `gh` that the pull request merged, closes the ticket's tabs, removes the worktree through `wt remove`, and removes the ticket's temporary directory. It keeps the worktree, and says why, when the worktree has uncommitted changes, the branch tip is neither the merged head nor in the base, or an agent still works there. `tkt done --all` checks every ticket worktree at once. It also records the merge in tkt's history, so `tkt stats` can tell how long the ticket took from `tkt start` to the merge, and how much of that it waited for you.

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
  ci -->|no| order{"your merge order<br/>for this head, checks green?"}
  order -->|yes| merge["tkt merge: finish-ticket<br/>in a fresh session"]
  order -->|no| leave
```

Plans, reviews, findings and human verification stay with you, or with `tkt auto`, and so does the word to merge: watch merges only through the merge order `tkt merge` leaves when you verified a head whose checks still run. A step watch would repeat on the same head, or a fix run past `[auto] max_rounds`, waits for you instead: the ticket shows as `needs_you`, and herdr notifies once. Its agent runs take the same `[auto] max_parallel` slots as `tkt auto`'s, and every step goes into the history.

You rarely start it yourself: every tkt command in a herdr pane starts one when none runs for the repository, in a session of its own, so it outlives the command and the pane. `[watch] autostart = false`, or `TKT_WATCH=off`, turns that off; `tkt watch --stop` stops it, after the step it is taking; `tkt watch --once --dry-run` says what one pass would do to each ticket. Outside a herdr pane, it finds the workspace whose panes work in the repository, or opens one there before its first step when none has a pane; while herdr is down it waits. `tkt hold ACM-123` keeps it, and `tkt auto`, away from a ticket.

`tkt watch --install` keeps it running while no terminal is open: it makes `tkt watch` and herdr's server systemd user units, or launchd agents on macOS, that start at boot, with the `PATH` of the shell you ran it from. Every tkt command then starts the unit rather than a watch of its own, and `tkt watch --stop` stops the unit. `tkt doctor` checks that both run and that their `PATH` finds the harnesses.

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

`git merge-tree` tests the merge in memory, without touching the worktree or the branch. A clean merge becomes a merge commit and a normal push; tkt never rebases and never force-pushes, and when the push fails it undoes its merge. A conflict goes to the `resolve` stage, which merges the base, resolves the conflicts with `resolving-merge-conflicts`, changes nothing else, refreshes the pull request's review brief, and pushes once. Review findings stay for `address`.

A clean merge does not make the pull request's review brief stale: the merge commit's tree equals what `git merge-tree` gives for its two parents, and review-pr checks that for itself instead of trusting tkt.

A sync costs a CI run and a review, so tkt syncs a ticket that is behind only at the moments that need it:

| Moment | How tkt knows | Why it can't wait |
|---|---|---|
| The branch conflicts | The forge reports the pull request as conflicting, or `git merge-tree` finds a conflict | Nothing merges, and conflicts only grow. |
| A review can't happen | The bot posts `STALE_BASE`, or review-pr stops with `BLOCKED_FOR_SYNC` | review-pr reviews only a head that contains the current base. |
| You are about to verify | You open `/check` in the tkt agent on a ticket that is behind | Your steps should run on the code that will merge, so `/check` offers to sync first. |
| A merge is due | A merge order on a ticket that is behind, or finish-ticket stops with `BLOCKED_FOR_SYNC` | finish-ticket merges only when the base has not moved since the review. |

A ticket without a pull request waits, since work-ticket merges the base itself right before it pushes, and so does a reviewed ticket that nobody verifies or merges yet. With five tickets in flight, syncing every branch whenever the base moves would cost four CI runs and four reviews per merge; at these moments a merge costs a sync only for the tickets that reach one, usually the next one you merge.

## Who changes the tracker

Only three skills move a ticket, and each moves it once:

```mermaid
stateDiagram-v2
  state "new" as new
  state "in-progress" as progress
  state "in-review" as review
  state "done" as done
  [*] --> new: create-ticket, or by hand
  new --> progress: work-ticket, before the first edit
  progress --> review: work-ticket, after exact-head CI passes
  review --> done: finish-ticket, after the merge is verified
  done --> [*]
```

`start-ticket` and `review-pr` never write to the tracker. `address-pr-review` writes only replacement browser captures. tkt never touches the tracker at all.

## The rules that hold it together

- One ticket, one branch, one worktree, one tab, all named `ACM-123`.
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
| `Status: BLOCKED` with a `Reason:` line | Read the reason in the tab. It names what a person has to decide or fix. |
| The agent waits for an answer | Answer it in the tab. `tkt ls` shows it as blocked. |
| No agent runs for the ticket | `tkt resume ACM-123` brings back the last session and sends nothing. Use it, not `tkt run`, when the conversation matters, such as a plan waiting for approval. |
| The pull request closed without merging | Decide by hand. tkt does not guess. |

`tkt ls` applies these rules in order and prints the next step. `tkt help ls` lists the rules.
