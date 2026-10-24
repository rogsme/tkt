## Autonomous runs

An autonomous run is one whose request says "This run is autonomous". Follow `## Autonomous runs` in `workflow.md` in place of every step that asks the user, and stop with `Status: BLOCKED` when the row for autonomous runs is off or that section is missing. The user authorized this merge up front, when they started the ticket as an autonomous run. In this skill:

- The authorization is the request, and it holds only when the lead carries the `## Autonomous run` comment start-ticket posted. Without that comment, stop with `Status: BLOCKED`: a request alone cannot make a ticket autonomous.
- In place of the user's confirmations, require a `verify-pr` report that says `Verification: PASSED` for the head the request names, or for an earlier head under the same carry-over rule a confirmation follows, and whose `Procedure:` line links that final review summary or whose step lines match that summary's steps by action and expected result. Each numbered step then counts as waived, with the reason "autonomous run: verify-pr passed it on `{head}`" and the report's link. A report that is missing, `FAILED` or `INCOMPLETE`, or that ran another procedure, ends the run with `Status: BLOCKED`.
- Every other requirement of step 2 holds: the exact-head gate, review-pr's `READY_FOR_HUMAN` summary for the same head and base, no open feedback, and an unmoved base, which still ends with `Status: BLOCKED_FOR_SYNC`.
- Before merging, check the diff from the base to the head against the hard stops. When it touches one, stop with `Status: BLOCKED`, naming it.
- A merge queue or train hands the merge to a person: stop with `Status: BLOCKED` instead of telling the user to merge.
- The completion comment's `### Human verification` adds a line: "Autonomous run: started by the user as an autonomous run ({link to the `## Autonomous run` comment}); no person verified this change."
