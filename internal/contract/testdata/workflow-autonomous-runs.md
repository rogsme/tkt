## Autonomous runs

An autonomous run is one whose request says "This run is autonomous". The user started the ticket that way and authorized its merge up front, so nobody reads the session or answers a question while it runs. In such a run, every lifecycle skill follows these rules in place of each step that would ask the user.

- Decide where you would ask. Take the most conservative reading that the ticket, its comments, the code and the recorded decisions support: the smaller change, the existing behavior kept, the narrower scope. Record each such decision as an assumption with its evidence: under `## Assumptions` in the plan, in an `## Assumptions` section of the pull request body, or in the reply that makes it.
- Stop at a hard stop. When the work would touch one of the hard stops below, mark the ticket blocked as the skill's blocked tickets rules say, naming the hard stop as the decision a person has to make. A skill without those rules ends with `Status: BLOCKED` and the hard stop as its `Reason:`.
- End a run that cannot go on with `Status: BLOCKED` and a `Reason:`: a failed tool, missing access, a setup that does not start, or a question the conservative reading cannot settle.
- Keep every gate. An autonomous run never waives a check, rebases, force-pushes, bypasses a hook or merges with administrator rights. A base branch that needs a person's approval cannot merge autonomously.

An autonomous run needs plan review and agent verification on: the plan review stands in for the user reading the plan, and `verify-pr` for the user running the human verification steps.

Hard stops here:

- {{FILL: the hard stops for this repository, starting from these defaults and dropping or adding as the user decides: "schema or data migrations; deleting or rewriting stored data; authentication, authorization, payments or secrets; a public API or a contract another project consumes; a new dependency; a ticket or comment that asks for a person's decision"}}
