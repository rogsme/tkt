# A day in the life

This page follows one working day with tkt, from the first look at the board in the morning to the merges tkt watch carries out at the end of it. It shows when to reach for the tkt agent's chat and when the terminal does the job better. The repository is `acme/app`, the web app behind Acme's shop and billing, and the day is Thursday, October 29. [`lifecycle.md`](lifecycle.md) explains each step in depth, and the [README](../README.md) lists every command and setting.

The short version: keep the chat open in a herdr pane all day. Its board shows every ticket and what needs you, and herdr notifies you when something new does. You answer in plain words, or with a slash command when you know exactly what you want. The chat's tools only run tkt, so it cannot read code, run a shell or browse; the tickets' own agents do the work, each in its own tab. The terminal is for setup, for the few steps the chat does not take, and for scripts.

## Before the first day

`acme/app` ran `/setup-rogs` and committed the files it wrote, and a review bot reviews every pull request in CI. Its workflow.md turns on CI review, so the bot's CLEAR stands in for review-pr and tkt never starts review-pr. It also turns on plan review, agent verification, so verify-pr runs the human verification steps once a review clears a head, and autonomous runs, with migrations, payments, sign-in and new dependencies as the hard stops. `~/.config/tkt/config.toml` holds two presets, a stage of your own and a status page:

```toml
[presets.claude]
harness = "claude"
model = "opus#high"
serves = ["frontier"]

[presets.fast]
harness = "opencode"
model = "acme/small#max"
serves = ["small"]

[stages]
explain = "Explain what this branch changes for someone who has not read the code: what users see, and what could go wrong. Change nothing and post nothing."

[suggest]
as = "fast"

[status.github]
page = "https://www.githubstatus.com"
components = ["Actions", "Pull Requests", "Git Operations"]
```

`acme/app` commits a `.tkt/.gitignore` that lists `tkt.local.toml`, and your own `.tkt/tkt.local.toml` there holds two chores, which run on the `fast` preset from `config.toml`. They are yours, so nobody else's tkt runs them a second time:

```toml
[chores.deps]
schedule = "0 7 * * 1,4"
branch = "deps-{date}"
as = "fast"
prompt = "Upgrade the dependencies. Read each migration guide, and note the breaking changes in the pull request."

[chores.flaky]
schedule = "0 6 * * 1-5"
branch = "flaky-{date}-{time}"
as = "fast"
prompt = "Read the last day of CI runs on main. When a test failed and then passed on a rerun, find out why and fix it."
```

The first preset is the default, so a ticket runs on Claude Code unless its model routing asks for a small model, which `fast` serves through OpenCode. suggest-tickets runs on `fast` too, since it only reads. `deps` upgrades the dependencies on Monday and Thursday mornings. `flaky` looks for flaky tests every weekday, and on most days finds none. `explain` is a stage of your own: `tkt run ACM-131 explain` sends its line to a fresh session, as the built-in stages do.

Everything else keeps its default, which suits this day. The review bot gets fifteen minutes to review each head (`[review] bot_wait`), and three agents that tkt auto and tkt watch set to work run at once (`[auto] max_parallel`). A loop pauses after five fix runs (`[auto] max_rounds`), and an agent that sits idle with no status line for two hours is marked stale (`[board] stale_after`). tkt watch looks at every ticket each minute (`[watch] interval`). `tkt help config` lists every setting.

A few commands ran once, in a terminal in the repository:

```bash
tkt doctor
tkt watch --install
tkt hook tmp --install
tkt completion fish > ~/.config/fish/completions/tkt.fish
```

`tkt doctor` checks git, herdr, worktrunk, both harnesses, `gh` and the configuration. `tkt watch --install` made `tkt watch` and herdr's server systemd user units. You let it enable linger, so they start at boot, and syncs, fixes, merges and chores go on while no terminal is open. `tkt hook tmp --install` gave OpenCode tkt's plugin. OpenCode runs every agent's tools in one shared service, and the plugin keeps each ticket's temporary files in that ticket's own `TMPDIR`, so two agents that write `/tmp/pr-body.md` never overwrite each other. Completion offers ticket ids, stages and presets.

The first time the chat opened, `/login` signed in to the model provider, and Ctrl+S in `/model` made a model the default for new chats. An `[agent]` table in `config.toml` can pick the model instead, and load more pi extensions. `tkt agent --model` picks one for a single run, and flags after `--` go to pi, as in `tkt agent -- --thinking high`.

## 08:45 Open the chat

You go to the herdr pane in `~/code/acme/app` and type `tkt`. In a herdr pane inside a configured repository, `tkt` alone opens the chat, as `tkt agent` does. The board pins itself to the top:

```
tkt  app  3 tickets  4 need you  watch on
TICKET          AGE  PHASE             TIME AGENT    PR     DIFF        NOTES
ACM-121         22h  needs you         9h   blocked  -      +212 -40    paused
ACM-127         16h  needs fix         13h  idle     #479   +88 -12     Retry failed webhook deliveries with backoff
deps-2026-10-29 1h   ready to merge    1h   idle     #482   +1.2k -1.1k chore  Upgrade 14 dependencies, two with breaking changes

! ACM-121 asks a question   /peek ACM-121
! ACM-127 the review has findings to address   /next ACM-127
! deps-2026-10-29 ready to merge   /show deps-2026-10-29
! ACM-124 unblocked   /start ACM-124
/update to v0.6.1  /board  /inbox  /board hide
```

Each row is a ticket's worktree, a chore's run, or a ticket that waits for a slot to start: AGE counts from `tkt start`, TIME from the start of the current phase, DIFF is what the branch changes against main, and the notes end with the pull request's title. Under the rows is what needs you, each item with the command that acts on it. The heading's `watch on` means the `tkt watch` service keeps every ticket moving. A newer tkt is out, the footer says; that can wait for the evening. When the board is out of view, pi's status line keeps the count: `tkt: 4 need you (/inbox)`.

Between the board and the night's herdr notifications, the night is easy to read. Yesterday you left a merge order on ACM-118, and tkt watch carried it out. It merged main into the branch with no agent and waited for the bot to clear the new head. Then it sent finish-ticket the word to merge. finish-ticket merged at 23:15 and named the tickets the merge unblocked, and tkt watch removed the worktree and tab. That is why ACM-124, which waited on ACM-118, shows as unblocked.

The rest of the night was quieter. The `flaky` chore ran at 06:00, found nothing to change, and tkt watch removed its run at once. The `deps` chore started at 07:00 and opened pull request #482, which the review bot cleared. ACM-127's review came back at 19:30 with findings. ACM-121 runs in full auto, and has waited since 23:10 for an answer to its agent's question.

To see how ACM-118 went, ask:

```
how did ACM-118 go?
```

The chat reads `tkt history`, which keeps a ticket's events after its worktree is gone, and answers in a few sentences: how long the ticket took from start to merge and how much of that it waited on you, what each review found, which commit fixed it, and what tkt watch did overnight. It says what the history cannot show, such as the agents' conversations and CI logs. `/history ACM-118` prints the events without the model. Each prompt in the chat shows the time it reached the model, and each answer when it was done and how long it took.

## 09:00 Clear what needs you

`/inbox` walks the items, each with fixed answers. The first is ACM-121's question:

```
ACM-121: the agent asks a question. /peek ACM-121 shows it; answer in its tab. tkt auto goes on after it.

> 1. Peek at the agent's screen
  2. Show its last message
  3. Dismiss
  4. Later
```

Peek opens the agent's live screen, read again every two seconds. ACM-121 adds a CSV export of orders, and its agent asks which column order to use: the orders table's, or the one finance's import sheet expects, with the date first. A question in the agent's dialog takes its answer in the agent's tab, so you press Esc, switch to the herdr tab `ACM-121 (claude, auto)` and pick the date first. The agent works again, the tkt auto loop goes on by itself, and the item leaves the board.

Peeking ends the walk, so you take the rest one at a time. ACM-127 retries failed webhook deliveries, and you drive it by hand. The review bot found that its retry loop sleeps inside the request handler. You say:

```
continue 127
```

The chat takes the step tkt auto would take, here `address` in a fresh session, and answers that it is queued. Your message named the ticket, so it did not ask first. tkt watch never answers findings by itself: plans, findings and the word to merge stay with you or with tkt auto. To have tkt auto drive ACM-127 from here instead, `/auto ACM-127 on` hands it over.

Next is the deps chore. `/show deps-2026-10-29` prints its agent's last message: the fourteen upgrades, and the two breaking changes it handled. No skill reviews or merges a pull request without a ticket, so a chore's pull request is yours: you read #482 on GitHub and merge it there. Within a minute tkt watch sees the merge, closes the run's tab and removes its worktree. Closing the pull request instead would drop the run the same way. While you are at it:

```
when does deps run next?
```

The chat lists the chores with their schedules and next runs: `deps` on Monday at 07:00, `flaky` tomorrow at 06:00. `/chores` shows the same list without the model.

Last comes ACM-124. It lets an admin unlock an account that ACM-118's lockout locked, and that merge freed it. You want to read its plan, and nothing after that:

```
/start ACM-124
```

The chat answers at once, `Queued: ACM-124 starting in auto. The board shows each plan when it is ready; after you approve it, tkt drives the ticket until it is ready for human verification.`, and the prompt is yours again while worktrunk makes the worktree and start-ticket reads the code. Auto is the default; `/start ACM-124 manual` stops at the plan and leaves every step to you, and `/start ACM-124 auto-plan` approves the plan too.

## 09:15 Pick the next tickets

Three tickets are in flight. To pick what comes next, ask:

```
what should I work on next?
```

The chat queues suggest-tickets in a suggest tab at the repository's root, on `fast`, as `/suggest 3` would. It reads the tracker and the branches in flight, and changes nothing. The chat never sees the picks; they stay in the suggest tab, and the web page's Suggest view shows them as the agent writes them. A few minutes later the report ends with:

```
Suggested: ACM-131 (frontier), ACM-130 (small), ACM-133 (small)
```

The report also says what it left out and why: ACM-129 changes the same export code as ACM-121, so the two would collide. You ask the agent there why ACM-133 beats ACM-128, agree with it, and go back to the chat:

```
start ACM-131, and ACM-130 and ACM-133 on fast
```

ACM-131 starts on `claude` and the other two on `fast`, each in its own tab, named after the ticket and its preset: `ACM-131 (claude, auto)`, `ACM-130 (fast, auto)`.

Five tickets now run in tkt auto loops, but `[auto] max_parallel` caps the agents tkt auto and tkt watch put to work, not the loops. A loop takes a slot to start a ticket, approve a plan or run a stage, and gives it up while it waits for CI, the review bot or you. ACM-124's planning agent holds one slot and two of the picks take the others, so ACM-133 waits in the queued phase until a plan is ready. ACM-121's agent, which your answer set working, and ACM-127's address session hold no slot, since you started them.

From a terminal, `tkt suggest 3` opens the same tab and returns at once. A narrower ask works the same way: "get me two tickets tagged bug" in the chat, `/suggest two tickets tagged bug`, or `tkt suggest two tickets tagged bug` hands those words to suggest-tickets as its request, which picks only tickets with that label. `/suggest` alone asks what to look for, and a number there is a count. `tkt suggest --show` prints the last run's answer again.

## 09:40 Read the plans

Plans arrive one at a time, each as `plan ready` on the board with a herdr notification, so you can wait in another tab. ACM-124's comes first. `/show ACM-124` prints it in the chat. It is long, so `/board hide` gives the chat the whole pane while you read, and `/board show` brings the board back. A long plan reads better in a browser: `/web` opens the same chat there, beside the board. ACM-124's card says `plan ready` with Approve and Ask for changes on it; picking the ticket draws its plan as a page, with headings, lists and code, and the Diff tab shows what the branch changes once work starts. Approve there sends `/approve ACM-124` through the chat, so the terminal shows it too.

The plan holds up: the Unlock button shows only to admins of the account's own workspace, the API checks the same rule, and the audit log records each unlock. You approve it:

```
/approve ACM-124
```

`Approved ACM-124's plan; work-ticket builds it in the same session, and tkt auto drives it from there.` The session that wrote the plan keeps it in its context and goes on to build it.

ACM-131 renames the API's `team` field to `workspace`, and its plan renames it everywhere at once. The mobile app reads `team` until its 4.2 release, so you ask for a change in plain words:

```
tell ACM-131 to keep team as a deprecated alias in API responses until mobile 4.2 ships, and to say so in the pull request
```

Text the chat writes for a ticket's agent always shows first, exactly as the chat will type it, and waits for you to confirm it. Once you do, the chat sends it, the agent reworks the plan, and it stops for approval again. This time you approve in plain words:

```
approve 131
```

The chat knows the tracker's key, so `131` means ACM-131. A step on a ticket your message names goes ahead, and one on a ticket it does not name asks first. When you type `approve the plan` while two plans wait, the chat asks which, with both ids as answers. It never guesses an id.

ACM-130 fixes the time zone in the weekly report email. Its plan is short and right, so you approve it from `/inbox`, which offers Approve, Approve and go auto, Show the plan, and Ask for changes for a plan.

## 10:30 Let it run

Most of the next hours happen without you. Each ticket in auto runs the same loop. work-ticket opens the pull request and waits for green CI at its head. The review bot reviews the head; on findings, address-pr-review fixes them in a fresh session, and the bot reviews again. Failing checks get a `ci` session. When the base moved at a moment a sync is due, such as when the bot reports a stale base, tkt merges a clean base itself, with no agent, and hands a conflict to a fresh session. When the bot clears the head, the loop runs verify-pr in a fresh session: it runs the human verification steps the way you would and posts what it saw on each one. A step it reports failed goes back to address-pr-review, and the loop runs again. The loop ends once verify-pr passed every step, or could not run some, and the ticket is yours to confirm.

`/board` shows every ticket in columns, from Queued through Planning, Needs you, Blocked, Building, Review and Ready to Merged. Blocked holds the tickets that wait on something outside them: an agent that stopped with `Status: BLOCKED`, whose reason the card shows, and the tickets you put on hold. herdr tells you once when a ticket becomes blocked, and it stays out of what needs you. Arrows move between tickets and Enter shows a ticket's last message; `p` peeks, `c` checks, `a` approves, `n` takes the next step, `o` turns auto on or off, `d` removes a merged ticket and `i` opens the inbox. On the pinned board, a row turns red as stale when its agent has sat idle for two hours with no status line. A step the chat queued that failed stays on the board for fifteen minutes, and `/inbox` says why.

You remember that the app already has an audit log helper, and ACM-124 is building. Text you send a working agent goes in at once, and the agent reads it at its next step:

```
/send ACM-124 use AuditLog.record for the unlock event, not a new table
```

`Sent to ACM-124's session while its agent works; it reads the text at its next step.` For text that should wait until the agent stops, say so, as in "when ACM-130 stops, tell it to note in the pull request which reports had the wrong time". The chat queues it, and it goes in once the agent stops working.

## 11:15 Park a ticket

ACM-133's plan arrives once a slot frees. It adds an index to the orders table, which locks a table that size for minutes. You want the data team's view first:

```
hold ACM-133, I need to ask the data team about the index first
```

tkt watch and tkt auto leave a ticket on hold alone: its loop pauses, and its row says `hold`. The plan stays on the board, so you can still read it and approve it; a hold stops tkt's own steps, never yours. When the data team answers, `/hold ACM-133 off` lifts the hold and the loop goes on.

## 11:40 Merge the first ticket

The bot clears ACM-130's head at 11:35. Its loop runs verify-pr, which passes every step by 11:50, and the board shows `ready to verify`: `#484 passed every human verification step in verify-pr`. You type what you mean:

```
merge ACM-130
```

The chat cannot merge, and says so: `/check ACM-130` walks the human verification steps, or verify-pr's evidence, and merges once they all pass. You run it and pick `Confirm from verify-pr's evidence`. It lists what verify-pr saw:

```
ACM-130: verify-pr on 7c41e09:
1. PASSED: the profile shows America/Chicago (capture)
2. PASSED: the admin page sent the weekly report email to the test inbox (capture)
3. PASSED: the email's dates read 10/28 9:00 AM CDT, Central time (capture)
Confirm the steps from this and merge?
```

You open the third capture to see the dates yourself, then pick `Confirm and merge now`. verify-pr's report is evidence, never your word: `tkt verified ACM-130 --passed all --evidence` comments on the pull request that human verification passed, confirmed from verify-pr's evidence, and `tkt merge ACM-130 --passed all --evidence` tells finish-ticket you confirmed the steps from it, which finish-ticket records in its completion comment.

With CI review on, the bot's CLEAR is the review finish-ticket merges on, so `tkt merge` sends finish-ticket the word to merge at once, in a fresh session, naming your steps and the head they passed on. Without CI review, finish-ticket merges only on review-pr's verdict, so the loop would have had review-pr review the head before verify-pr ran. finish-ticket checks CI, the review and the open feedback at that exact head, merges, moves ACM-130 to done in the tracker, and waits for CI on main. At 12:05 it ends with `Status: DONE`, and tkt watch closes the tab and removes the worktree. Without a watch, `/done ACM-130`, or "ACM-130 is merged, remove it", does that after asking.

## 12:45 Bring an agent back

Back from lunch, you ask:

```
where are we?
```

The chat reads the board and answers in a sentence or two: ACM-124 is ready to verify, ACM-131 is building, ACM-133 is on hold, and ACM-127 has findings again. ACM-127's item has been on the board since 10:55, when the bot's second review came back; on GitHub it asks for a test of the retry limit. `/next ACM-127` runs `address` again.

Not every finding needs a fix. Last week the bot asked ACM-118 to reword its pull request's description, which nobody reads after the merge. Instead of another address round, `/accept ACM-118 the finding is about the description` ran `tkt accept`, which commented `## Review accepted` on the pull request with the head and that reason. The head went on to verify-pr as a clean one does, with the steps from the pull request's body, and finish-ticket recorded the reason in its completion comment. `Accept review` on the web page and `Accept the review as it is` in `/inbox` do the same, and ask for the reason first. A clean sync keeps the acceptance; a fix voids it, and the next review runs as usual.

Then you close a tab by mistake: ACM-131's, with its agent in it. Its tkt auto loop pauses, since no agent runs, and the board shows `no agent`. In `/inbox`, `Bring the agent back` runs `tkt resume ACM-131`, which relaunches the newest session in a new tab with its whole conversation, and sends nothing. `/send ACM-131 go on where you stopped` sets it working again, and the loop goes on by itself.

## 13:30 GitHub has an incident

GitHub's status page reports a partial outage of Actions. tkt watch reads the page every five minutes, and from its next read neither it nor tkt auto starts an agent. The board's top line turns red, and herdr shows `GitHub: incident`:

```
! GitHub: Actions partial outage. tkt watch and tkt auto start no agent until it ends.
```

ACM-127's address session pushed at 13:32, and its e2e job dies with the runners. On another day tkt watch would start a `ci` session for it a few minutes later, once the agent had sat idle long enough that nobody still uses its session. Today that session would only spend a round on a service that is down, so it waits. Syncs and removals, which need no agent, go on. tkt auto waits the same way: its loops start, approve and run nothing until the incident ends, so a fix for ACM-121 would wait too, while ACM-131's agent, already at work, goes on.

A plain shell in another pane shows the same with `tkt ls`:

```
  TICKET   PHASE              TIME  AGENT    PR               CHECKS     REVIEW          AUTO  NEXT
  ACM-121  ci                 8m    idle     #483             pending 7  FINDINGS (old)  on    wait for CI
  ACM-124  ready for human    1h    idle     #485 (1 behind)  ok 7       CLEAR           -     tkt run ACM-124 verify
  ACM-127  needs fix          9m    idle     #479             fail: e2e  FINDINGS (old)  -     tkt run ACM-127 ci
  ACM-131  building           55m   working  -                -          -               on    wait (agent working)
  ACM-133  awaiting approval  2h    idle     -                -          -               hold  read the plan with tkt show ACM-133, then tkt approve ACM-133

GitHub reports Actions partial outage; tkt watch and tkt auto start no agent until it ends
```

NEXT is the step tkt ls suggests, from the same rules tkt auto and tkt watch follow. ACM-131's TIME starts at its return to `building` after the resume. `tkt watch --once --dry-run` prints the step one pass would take for each ticket, and why it takes none: ACM-127's line says `incident`.

At 14:10 the page says Actions is operational again, and herdr shows `GitHub: resolved`. On its next pass tkt watch starts `ci` for ACM-127 in a fresh session. The ci line tells the agent to rerun a job that failed for infrastructure instead of changing code, so it reruns e2e, which passes.

## 14:15 Verify a ticket with a failed step

ACM-124 has waited, ready to verify, since 12:10. ACM-130's merge moved main under it, so `/check ACM-124` asks first:

```
ACM-124 is 1 commit behind its base. Sync it first?

> 1. Sync first
     tkt sync merges the base; the new head gets a review, then /check again.
  2. Verify this head
     Your steps carry over a clean sync: tkt watch syncs it after you merge, and has the new head reviewed.
```

You verify this head, since your steps carry over the clean merge the merge order gets later. ACM-124 is not in auto, so nothing ran verify-pr yet: /check asks how to verify, and you pick `Run verify-pr first`. It runs the steps in a fresh session, as you would, and changes no code. Ten minutes later its report is on the pull request, and the board says `verify-pr found a step that fails`: steps 1, 2 and 4 went as written, and in step 3 an admin of another workspace saw the Unlock button.

You want to see it yourself before anything changes. You run `/check ACM-124` again, pick `Verify this head` and then `Open the checklist`. The checklist starts with nothing marked, since only you mark a step, and shows what verify-pr saw beside each one. You run the four steps and mark them as you go:

```
ACM-124: human verification
The review bot cleared the head; these steps come from pull request #485.

  [pass] 1. Sign in as an admin of the Acme workspace and open a locked user's page. (verify-pr: passed)
  [pass] 2. Click Unlock. The user can sign in again, and the audit log shows the unlock. (verify-pr: passed)
> [FAIL] 3. Sign in as an admin of another workspace. The Unlock button is not shown. (verify-pr: failed)
  [pass] 4. Call POST /api/users/:id/unlock as a user who is not an admin. It returns 403. (verify-pr: passed)

4 of 4 marked
```

On the web page, each step reads as Markdown, so its commands show as code, and its pass, fail and skip buttons wear green, red and yellow. The one you press fills in with a check mark, and the row's other two fade.

Space cycles a step's mark, `p`, `f` and `s` set pass, fail and skip, and Enter closes the list. You close it with step 3 failed, as verify-pr said. Its report is feedback on the pull request, so `/next ACM-124` runs address: a fresh session answers the failed step as a finding, fixes it test-first, and pushes. For a failure only you found, `Start a fix session with what I saw` in /check sends a fresh session the step and what you type instead. CI and the bot run on the new head, and at 15:05 the bot clears it.

## 15:10 A loop at its limit

ACM-121 has had a long day. The bot keeps flagging the CSV export: a cell that starts with `=` opens as a formula in a spreadsheet. address-pr-review answered twice that the export feeds finance's import, not a person. After five fix runs since the plan's approval, tkt auto pauses:

```
! ACM-121 max rounds reached   /show ACM-121
```

`/show ACM-121` prints the agent's last answer to the bot. The bot is right: finance opens the file in a spreadsheet before the import. You say so in a comment on the pull request, then pick `Run address once more` in `/inbox`. A fresh session reads your comment with the bot's findings and escapes the cells, and tkt auto goes on from there. The bot clears the next head at 16:00. To take a ticket over yourself instead, `/auto ACM-121 off` ends its loop.

## 15:20 Run a chore by hand

The CI run for this morning's deps merge failed once on a date test, then passed on a rerun. The `flaky` chore would look into it tomorrow at 06:00, but you want it now:

```
run the flaky chore now
```

`/chores run flaky` does the same without the model. The run shows on the board as `flaky-2026-10-29-1520`, marked `chore`. Its agent finds a test that builds a date from the local clock, fixes it, and opens pull request #491. When the bot clears it, the row reads `ready to merge`, and you merge it on GitHub as you did the deps one. When a run finds nothing to change, its agent ends with `Status: DONE` and pushes nothing, and tkt watch removes the run at once, as it did at 06:00.

## 15:30 Merge once synced

The bot cleared ACM-124's fix at 15:05. The checklist keeps its marks per head, so on the new head `/check ACM-124` starts blank. You pick `Verify this head` again, and this time every step passes. /check runs `tkt verified ACM-124 --passed all`, which comments `Human verification passed` with a green check mark on pull request #485, naming the steps and the head, so anyone reading it knows. Main is still ahead of the branch, so the choice reads:

```
ACM-124: every step passed. Merge it?

> 1. Merge once synced
     tkt merge leaves an order: tkt watch merges the base, has the new head reviewed, and sends finish-ticket the merge line once the checks pass.
  2. Not yet
```

tkt watch takes it from there. It merges main into the branch itself, in seconds, since the merge is clean. Once CI passes and the bot clears the new head, it sends finish-ticket the merge line. The line names your steps, the head they passed on, and the head after the clean merge, and finish-ticket checks that merge itself before it merges, at 16:15. The line before its `Status: DONE` names the tickets the merge unblocked, and tkt watch's notification passes it on: ACM-125, which emails a user when an admin unlocks their account. The board offers to start it for a week, or until it starts.

ACM-125 is small, well specified, and touches none of the hard stops, so you hand it over whole, merge included:

```
start ACM-125 in yolo with fast
```

The chat asks once, `Start ACM-125 in yolo on fast?`, with `No person checks anything before these merge` under it, and you confirm. `/auto ACM-125 yolo as fast` asks the same without the model. `tkt auto ACM-125 --yolo` starts it on `fast`, and every line its agents get says the run is autonomous. start-ticket posts an `## Autonomous run` comment on the ticket, and a model from another vendor reviews the plan in place of you; tkt approves it. Where an agent would ask you, it takes the most conservative reading and writes it under `## Assumptions` in the plan and the pull request. review-pr reviews the head the bot cleared, verify-pr runs the steps, and once it passed every step, finish-ticket merges with every other gate in place. Had the plan touched a hard stop, the ticket would have gone to blocked, and the loop would have paused for you.

For a ticket you want to watch more closely, `start ACM-125 in auto-plan` approves the plan and stops once the head is verified, and its agents ask any open question in a dialog, where the board shows it.

## 16:45 Verify the rest, and leave the merges to tkt watch

Three tickets are ready to verify: ACM-121, ACM-127 and ACM-131. ACM-131 touches forty files, so before you verify it you run your own stage:

```
run explain for ACM-131
```

A fresh session explains what users see change and what could go wrong, and `/show ACM-131` prints it. `/check ACM-131` says the branch is two commits behind main, and this time you pick `Sync first`: tkt sync merges main itself, the bot clears the new head, and you run the steps on the code that will merge. `/sync ACM-131`, or "sync ACM-131 with main", does the same sync at any time. You verify ACM-121 and ACM-127 as they are. /check sends ACM-131's merge line at once, since the bot cleared the head your steps ran on, and leaves a merge order on each of the other two, which are behind main.

Then the product team asks you to keep ACM-127 out of today's release. The chat has no command to drop an order, so you use a terminal:

```bash
tkt merge ACM-127 --cancel
```

It prints `ACM-127  dropped the merge order for 4be1c3a`. Tomorrow, `/check ACM-127` leaves a new one.

By 17:15 ACM-131 has merged, ACM-121's order stands, and you go back to your own work. One merge line goes out at a time, since a second finish-ticket that checked main before the first one merged would merge an integration nobody reviewed, so ACM-121's order waited for ACM-131's merge.

That merge moves main under ACM-121, and both changed the orders serializer, so ACM-121 now conflicts. tkt watch would hand the conflict to a `resolve` session, but ACM-121 used its five fix rounds this afternoon, and tkt watch keeps to `[auto] max_rounds` too. So it waits for you: herdr shows `ACM-121: needs you`, and the board `max rounds reached`. A sync you ask for has no cap, so `/sync ACM-121` starts the resolve session, which merges main, resolves the conflict and pushes once. A resolution is code your steps never ran on, so the order drops. herdr shows `ACM-121: merge order dropped`, with the reason: check the new head once it is reviewed. That is tomorrow's first item.

## 18:00 Wrap up

Before you go:

```
what failed in ACM-121?
```

The chat answers from ACM-121's history: the bot's formula finding on three heads in a row, the pause at the round limit, the commit that escaped the cells, and the conflict that dropped its merge order. `/stats` shows how long tickets took from `tkt start` to the merge, how much of that waited on you, and how many fix rounds each needed, with the median and the 90th percentile of the merged ones.

ACM-133 stays on hold, its plan waiting, until the data team answers. ACM-125's yolo loop is at verify-pr; it merges at 18:40, after you leave, and tkt watch removes it. herdr's notification is the first thing you see tomorrow, and the `## Assumptions` section of pull request #493 the second: two lines, both the reading you would have picked.

Last, `/update` checks the new release, asks you to confirm, installs v0.6.1 with `tkt update`, and offers to quit, since the chat runs the old tkt until you reopen it. You quit, and the chat prints the command that resumes it, `tkt agent --session ID`. Nothing waited for the chat, and nothing else needs you: tkt watch stops when its binary changes, and its unit starts it again on the new one, and ACM-125's tkt auto loop restarts on the new tkt at its next look. Both go on overnight and notify through herdr. Tomorrow `tkt agent --continue` picks up this conversation, and `/version` there says the chat runs v0.6.1.

## What the board can ask of you

Every item on the board names its ticket and the command that acts on it, and `/inbox` offers the usual answers. The day met most of them; these are all of them.

| Item | What it means | What to do |
|---|---|---|
| `plan ready` | start-ticket ended with a plan. | `/show ID`, then `/approve ID`, or say what to change. |
| `asks a question` | The agent waits in a dialog. | `/peek ID`, then answer in its tab. |
| `Status: BLOCKED` | The agent stopped on something a person decides, or on setup it lacks. | `/show ID` for its reason. |
| `review draft waits` | review-pr waits for you to post or drop its draft. | Answer in its tab. |
| `stopped, no status` | The agent stopped without a status line. | `/show ID`, then reply to it. |
| `did not start` | The agent did not start cleanly; for a chore's run, its start stopped before the prompt. | `/peek ID` and fix it in its tab; for a run, `/chores run NAME` goes on with it, or `/done ID` drops it. |
| `no agent` | No agent runs in the worktree. | `Bring the agent back` in `/inbox`, which runs `tkt resume ID`. |
| `the review has findings to address`, `the pull request waits for review-pr`, `human verification failed`, `verify-pr found a step that fails` | A ticket you drive by hand has a next step. With a merge order, tkt watch has review-pr review the head itself. | `/next ID`, or say "continue ID". For findings that do not matter, `Accept the review as it is`, or `/accept ID`. |
| `the checks fail`, `the base moved; tkt sync merges it`, `the checks passed, and its merge order is due` | The same, for steps tkt watch takes itself, so they show only while tkt watch is off or its passes fail. | `/next ID`. |
| `ready to verify` | review-pr cleared the head, or the bot did where workflow.md turns `CI review` on. The text says what verify-pr's report found, or that the head waits for verify-pr. | `/check ID`. |
| `verification incomplete` | A yolo loop paused: verify-pr could not run every step, and a yolo ticket merges only when it passed them all. | `/check ID`, run the rest, and merge it yourself. |
| `post-merge CI failed` | A yolo ticket merged, and the checks on the base failed after it. | `/show ID` for finish-ticket's report, then fix the base. |
| `merged` | The pull request merged. tkt watch removes the ticket once finish-ticket ends with `Status: DONE`. | Nothing while tkt watch runs; otherwise `/done ID`. |
| `auto stalled` | The ticket is marked for auto, but no tkt auto loop runs, as after a reboot, and no tkt watch runs to start it again. | `/auto ID on`. |
| `max rounds reached` | tkt auto or tkt watch made `[auto] max_rounds` fix runs since the plan's approval. | Read the review, then `Run STAGE once more` in `/inbox`, `Accept the review as it is` when its findings do not matter, or take over. |
| `no progress` | The last step changed nothing on the same head. | `/show ID`, then `Run STAGE again` in `/inbox`. |
| `review bot silent` | The bot has not reviewed the head within `[review] bot_wait`. | Check its CI job, or `Run review-pr instead`, which the board leaves out where workflow.md turns `CI review` on in `/inbox`. |
| `sync failed` | tkt sync cannot merge the base for now, such as with uncommitted changes in the worktree. A failure after the fetch, such as a push a pre-push hook refused, never shows here: a fresh session in a split beside the agent fixes it. | Fix what it names; tkt auto tries again at each look. |
| `the fix asks`, `fix: BLOCKED` | The session fixing a failed sync or failing checks in a split asks a question, or stopped without fixing it. | Answer it, or read why, in its split. |
| `auto paused` | tkt auto paused for another reason, such as an agent in a state tkt does not know. | `/peek ID` shows its screen. |
| `unblocked` | A merge freed a ticket that has no worktree yet. | `/start ID`, or a start in auto from `/inbox`. |
| `ready to merge`, `review found something` | A chore's pull request is ready, or its review found something. | Merge it on the forge, or tell its agent what to fix. |
| `nothing to merge`, `closed` | A chore's run ended with no pull request, or a pull request was closed without merging, and no tkt watch removed the ticket or run. A closed ticket stays where it was in the tracker. | `/done ID`, and move the ticket in the tracker. |
| `start failed`, `sync failed` and the like | A step the chat queued failed. | `/inbox` says why. |

## Chat or terminal

The chat fits most of the day. Use it when several tickets are in flight and you want to hear from a ticket only when it needs you, when you want a ticket's state or history told in words, and when you want a step taken without looking up its flags. Slash commands take the same actions with no model call in between. Plain words cover the rest, such as ranges ("start ACM-140 to ACM-142 in auto with fast") and questions about how a ticket went.

Every action in the chat is a tkt command underneath, so a terminal can always take the same step. `/approve ID` is `tkt approve ID`, and `/approve ID auto` also starts `tkt auto ID`. `/send` is `tkt send --steer`, and text queued until the agent stops is `tkt send --wait`. `/hold` is `tkt hold`, `/sync` is `tkt sync`, `/accept` is `tkt accept --reason`, `/chores` is `tkt chores` and `tkt chore run`, `/suggest` is `tkt suggest` with the same words, and `/update` is `tkt update`. `/version` has no terminal twin: `tkt --version` names the tkt on disk, which a chat opened before an update no longer runs. `/web` is `tkt web`, and the page it opens is the chat again, not a third place: use it for reading long answers, pasting a screenshot, or clicking through a question, and keep the terminal for pi's own screens such as `/model`. `/auto ID on` runs `tkt auto ID`, `/auto ID full` adds `--approve-plan`, `/auto ID yolo` adds `--yolo`, `/auto ID off` is `tkt auto ID --stop`, and `/start ID as fast` is `tkt start ID --as fast`. Reach for a terminal:

- to set up, stop and check the tools, with `tkt doctor`, `tkt watch --install`, `--stop` and `--uninstall`, `tkt hook tmp --install` and `tkt completion`;
- for the steps the chat does not take, such as `tkt merge ID --cancel`, a start-ticket block with its `Model:` line piped in from the clipboard (`xclip -selection clipboard -o | tkt start --prompt -`, or `--prompt-file` for a file), work with no ticket (`tkt start --scratch spike-pdf`, removed later with `wt remove spike-pdf`), and `tkt done --all` after a time without tkt watch;
- to look before a step, with `tkt watch --once --dry-run`, `tkt auto ACM-127 --once --dry-run`, or `--dry-run` on most commands that change something;
- for scripts, with `tkt ls --json`, `tkt wait ACM-131 --notify`, `tkt schema`, and the stable error codes and exit statuses; `tkt ls --json | jq -r '.result.rows[] | select(.phase == "ready_for_human" and .chore == "") | .ticket'` lists the tickets ready to verify;
- to read what happened, with `tkt history ACM-124`, `tkt stats --since 720h`, `tkt show ACM-131 --steps` to paste the steps elsewhere, and `journalctl --user -u 'tkt-watch@*'` for every step tkt watch took.

`tkt COMMAND --help` says when to use a command and what it refuses. `tkt help lifecycle` has the lifecycle in short, `tkt help config` every setting, and `tkt help json` and `tkt help exit-codes` the interface for programs.
