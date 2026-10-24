## Autonomous runs

An autonomous run is one whose request says "This run is autonomous". Follow `## Autonomous runs` in `workflow.md` in place of every step that asks the user, and stop with `Status: BLOCKED` when the row for autonomous runs is off or that section is missing. The user starts nothing else and reads nothing before the merge, so:

- Before any tracker write, require the `Plan review` and `Agent verification` rows on as well. Otherwise stop with `Status: BLOCKED` and a `Reason:` naming the row that is off.
- Right after the step 2 move to `in-progress`, post an `## Autonomous run` comment on the lead with `comment`, with a pointer naming the lead on each rider. It records that the user started this ticket as an autonomous run, quotes the request, and gives the date. `finish-ticket` reads it as the user's merge authorization. Read it back.
- Settle every remaining question, open decision and review finding left for the user as `## Autonomous runs` says, and list each in the plan under `## Assumptions`, with its evidence. A plan that would touch a hard stop marks the ticket blocked, as [Blocked tickets](#blocked-tickets) says, naming the hard stop.
- Plan review must succeed: when `review-plan` hands the plan back with a `Not reviewed:` line, stop with `Status: BLOCKED` and that line as the `Reason:`. On `RETHINK`, rework the approach when the reviewer's case holds; otherwise keep it and record the reviewer's case as an assumption.
- Skip plan presentation. Return the complete report in the terminal, marked `Not presented: autonomous run.`, and end with `Status: AWAITING_APPROVAL`: the runner approves the plan, and nothing is left to decide.

