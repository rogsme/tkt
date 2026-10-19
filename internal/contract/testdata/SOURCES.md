# Fixture sources

Each fixture copies text that another project produces, so a change there shows up here as a failing test. Update a fixture and the code that reads it in one commit, and cite the source commit.

| Fixture | Source | Commit |
|---|---|---|
| `issue-tracker.md` | gitlab.com/rogs/skills `skills/lifecycle/setup-rogs/assets/tracker-linear.md`, the `- Ticket ids:` line filled for Acme | `6a7b88c` |
| `forge.md` | gitlab.com/rogs/skills `skills/lifecycle/setup-rogs/assets/forge-github.md`, the `## Forge` section filled for Acme | `6a7b88c` |
| `summary-changes-requested.txt`, `summary-ready.txt` | gitlab.com/rogs/skills `skills/lifecycle/review-pr/references/summary.md`, the two summary blocks | `6a7b88c` |
| `status-lines.md` | gitlab.com/rogs/skills `CLAUDE.md`, the "Status lines" registry | `6a7b88c` |
| `automated-review.txt` | the forge's automated reviewer, not a skill: its review summary line | none |
