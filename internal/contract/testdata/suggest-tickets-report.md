## 4. Report

1. Waiting on you: the tickets in review, one line each. Each keeps its files until it merges, and moves on only through a review, a human verification or a merge.
2. The picks in order, one line each: the id, the title, the priority, what it unblocks, and, with model routing on, its model.
3. Not yet: every candidate left out, one line each, with the reason, such as its open blocker, the ticket it collides with, or the batch's heavy ticket.
4. One line naming the picks in order, a bundle by its lead. With model routing on, each carries the model it runs on in parentheses: the model id alone, without the conditions a `Model:` line may add, `frontier` for a ticket without the section, and for a bundle the model `## Model routing` gives it. `Suggested: ACM-14 (frontier), ACM-21 (acme-small-2)`. Without model routing: `Suggested: ACM-14, ACM-21`. Without any pick: `Suggested: none`.

End with `Status: TICKETS_SUGGESTED`.
