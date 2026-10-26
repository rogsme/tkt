## CI review

A reviewer in CI reviews every pull request on each push, a clean merge of the base included. It stands in for `review-pr`.

- Reviewer: {{FILL: the workflow file and the check that runs the reviewer, such as "`.github/workflows/review.yml`, check `review`"}}.
- Verdict: the reviewer's summary ends with one line that holds `Automated review: CLEAR`, `Automated review: FINDINGS` or `Automated review: STALE_BASE`, and the head it reviewed as ``head `<sha>` ``.

Rules:

- The reviewer's summary with `Automated review: CLEAR` for a head counts as `review-pr`'s summary with `Verdict: READY_FOR_HUMAN` for that head, wherever a lifecycle skill or a runner requires one. It names no base: the base it includes is the one that is an ancestor of that head.
- After that CLEAR, the human verification procedure is the pull request body's `## Human verification` section at that head.
- `Automated review: FINDINGS` is review feedback, which `address-pr-review` answers.
- A runner never starts `review-pr` here. `review-pr` runs only when the user asks for it.
