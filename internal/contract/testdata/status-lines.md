## Status lines

The lifecycle skills hand off through these lines. A skill restates the lines it emits; this table is the registry.

| Line | Emitted by | Next |
|---|---|---|
| `Status: SETUP_REQUIRED` | any lifecycle skill | `/setup-rogs` |
| `Status: CONFIGURED`, `Status: SETUP_OK`, `Status: SETUP_DRIFT` | setup-rogs | the lifecycle, or another setup run |
| `Status: AWAITING_CREATE_APPROVAL`, `Status: TICKETS_CREATED` | create-ticket | the user |
| `Status: AWAITING_APPROVAL` | start-ticket | the user approves, then work-ticket |
| `Status: READY_FOR_REVIEW` | work-ticket, address-pr-review | review-pr in a fresh session |
| `Status: AWAITING_POST_APPROVAL`, `Status: REVIEW_POSTED`, `Status: READY_FOR_HUMAN` | review-pr | address-pr-review, or the human verification |
| `Verdict: CHANGES_REQUESTED`, `Verdict: READY_FOR_HUMAN`, with `Reviewed head:` and `Reviewed base:` lines | review-pr, inside the posted summary | address-pr-review, finish-ticket |
| `Status: BLOCKED_FOR_SYNC` | review-pr, finish-ticket | address-pr-review (sync-only pass) |
| `Status: HUMAN_QA_FAILED` | finish-ticket | address-pr-review |
| `Status: BLOCKED_POST_MERGE`, `Status: DONE` | finish-ticket | the user |
| `Status: BLOCKED`, followed by a `Reason:` line | any lifecycle skill | the user |
