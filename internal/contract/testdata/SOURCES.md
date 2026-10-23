# Fixture sources

Each fixture copies text that another project produces, so a change there shows up here as a failing test. Update a fixture and the code that reads it in one commit, and cite the source commit.

| Fixture | Source | Commit |
|---|---|---|
| `issue-tracker.md` | gitlab.com/rogs/skills `skills/lifecycle/setup-rogs/assets/tracker-linear.md`, the `- Ticket ids:` line filled for Acme | `6a7b88c` |
| `forge.md` | gitlab.com/rogs/skills `skills/lifecycle/setup-rogs/assets/forge-github.md`, the `## Forge` section filled for Acme | `6a7b88c` |
| `summary-changes-requested.txt`, `summary-ready.txt` | gitlab.com/rogs/skills `skills/lifecycle/review-pr/references/summary.md`, the two summary blocks | `6a7b88c` |
| `summary-ready-verification.txt` | gitlab.com/rogs/skills `skills/lifecycle/review-pr/references/summary.md`, the ready summary "followed by a `## Human verification` section holding the final numbered procedure", with a procedure written for Acme to the standard in `skills/lifecycle/setup-rogs/assets/workflow.md` `## Human verification` | `33d185b` |
| `status-lines.md` | gitlab.com/rogs/skills `CLAUDE.md`, the "Status lines" registry | `26cfa58` |
| `start-ticket-unattended.md` | gitlab.com/rogs/skills `skills/lifecycle/start-ticket/SKILL.md`, the `## Unattended runs` section | `619ef65` |
| `review-pr-clean-merge.md` | gitlab.com/rogs/skills `skills/lifecycle/review-pr/SKILL.md`, the paragraph on a brief pinned to an earlier head, under `## 3. Review the complete change` | `30cf6a6` |
| `resolving-merge-conflicts.md` | gitlab.com/rogs/skills `skills/engineering/resolving-merge-conflicts/SKILL.md`, whole | `b502691` |
| `automated-review.txt` | the forge's automated reviewer, not a skill: its review summary line | none |

`config.ApproveLine`, the line tkt approve sends, relies on work-ticket's trigger: the skill runs "when the user approves the plan and says ... execute the plan" (gitlab.com/rogs/skills `skills/lifecycle/work-ticket/SKILL.md`, `33d185b`).

`config.UnattendedLine`, which `tkt auto --approve-plan` adds to the start line, relies on that `## Unattended runs` section: start-ticket reads the word "unattended" in its request, skips the browser presentation, and asks every open question through the harness's question tool (gitlab.com/rogs/skills, `619ef65`, merged in `b6f9d9c`).

The built-in `sync` stage sends the same line as `address`: address-pr-review merges the base first on every run, so one line covers a moved base and review feedback (gitlab.com/rogs/skills `skills/lifecycle/address-pr-review/SKILL.md`, `26cfa58`). `tkt run` warns that it goes away in the next release, since `tkt sync` replaces it.

The built-in `resolve` stage, which `tkt sync` starts when the base merge conflicts, names `resolving-merge-conflicts`. The line relies on what the fixture says: the skill takes over a merge another party started, keeps it a merge, asks about a hunk that decides product behavior, and concludes with `git commit --no-edit`.

`tkt sync` pushes a clean base merge without touching the pull request's Review brief. It relies on review-pr's rule in `review-pr-clean-merge.md`: a brief pinned to an earlier head stays current when every commit after it is a merge commit whose second parent is on the base and whose tree equals what `git merge-tree --write-tree` prints for its parents, which is the merge `tkt sync` makes and checks before it pushes.
