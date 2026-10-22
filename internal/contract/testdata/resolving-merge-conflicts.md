---
name: resolving-merge-conflicts
description: "Resolve an in-progress git merge or rebase conflict hunk by hunk, tracing each side's intent to its primary source. Use when a merge, pull, rebase or cherry-pick stops on conflicts, or when another skill hands over a conflicted merge."
license: MIT
metadata:
  rogs-skills/contract: "1"
---

1. **See the current state** of the merge or rebase: which operation is in progress (`git status`), the conflicting files, and the history of both sides for those files (`git log --merge`).

2. **Find the primary sources** for each conflict. Understand deeply why each change was made, and what the original intent was. Read the commit messages, then the pull requests and tickets they reference. When the project has `docs/agents/forge.md` and `docs/agents/issue-tracker.md`, those files say how to read pull requests and tickets.

3. **Resolve each hunk.** Preserve both intents where possible. Where incompatible, pick the one matching the merge's stated goal and note the trade-off. Do **not** invent new behaviour. When neither intent settles a hunk and the choice would decide product behaviour, stop and ask the user, leaving the operation in progress. Resolve every other hunk; never `--abort`.

4. Run the project's **automated checks**: the commands in `docs/agents/workflow.md` under `## Verification` when that file exists, otherwise the ones its task runner and manifests define, typically typecheck, then tests, then format. Fix anything the merge broke.

5. **Finish the merge/rebase.** Stage each resolved file by explicit path, so unrelated work in the worktree stays out of the commit, then conclude a merge with `git commit --no-edit`. If rebasing, continue the rebase process until all commits are rebased. A workflow that syncs by merging never turns a merge into a rebase.

When another skill handed over the conflict, report each resolved hunk and every trade-off you noted, then hand control back to it.
