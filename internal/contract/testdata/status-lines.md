## Status lines

The lifecycle skills hand off through these lines. A skill restates the lines it emits; this table is the registry.

| Line | Emitted by | Next |
|---|---|---|
| `Status: SETUP_REQUIRED` | any lifecycle skill | `/setup-rogs` |
| `Status: CONFIGURED`, `Status: SETUP_OK`, `Status: SETUP_DRIFT` | setup-rogs | the lifecycle, or another setup run |
| `Status: AWAITING_CREATE_APPROVAL`, `Status: TICKETS_CREATED` | create-ticket | the user |
| `Status: TICKETS_SUGGESTED`, after a `Suggested:` line with the picks | suggest-tickets | the user starts the picks with start-ticket |
| `Status: AWAITING_APPROVAL` | start-ticket | the user approves (in an unattended or autonomous run, the runner that started it), then work-ticket |
| `Status: READY_FOR_REVIEW` | work-ticket, address-pr-review | review-pr in a fresh session |
| `Status: AWAITING_POST_APPROVAL`, `Status: REVIEW_POSTED`, `Status: READY_FOR_HUMAN` | review-pr | address-pr-review, or verify-pr and the human verification |
| `Verdict: CHANGES_REQUESTED`, `Verdict: READY_FOR_HUMAN`, with `Reviewed head:` and `Reviewed base:` lines | review-pr, inside the posted summary | address-pr-review, finish-ticket |
| `Status: VERIFIED`, `Status: VERIFICATION_FAILED`, `Status: VERIFICATION_INCOMPLETE` | verify-pr | the user confirms the steps from the evidence, address-pr-review fixes the failed steps, or the user runs the steps not run |
| `Verification: PASSED`, `Verification: FAILED`, `Verification: INCOMPLETE`, with `Verified head:` and `Verified base:` lines and one line per step | verify-pr, inside the posted report | address-pr-review, finish-ticket, the user |
| `Status: BLOCKED_FOR_SYNC` | review-pr, verify-pr, finish-ticket | address-pr-review, or a fresh review |
| `Status: HUMAN_QA_FAILED` | finish-ticket | address-pr-review |
| `Status: BLOCKED_POST_MERGE`, `Status: DONE` | finish-ticket | the user |
| `Unblocked:` with the ticket ids, or `none` | finish-ticket, right before `Status: DONE` | the user starts the tickets it names |
| `Status: BLOCKED`, followed by a `Reason:` line | any lifecycle skill | the user |
