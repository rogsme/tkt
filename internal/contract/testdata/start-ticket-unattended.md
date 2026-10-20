## Unattended runs

An unattended run is one whose request says so. The runner that sent the request approves the plan itself, with nobody reading it first, and calls the user only when this session waits on a question. In an unattended run:

- Ask every question through the harness's structured question tool (`AskUserQuestion` in Claude Code, `question` in OpenCode): a pending question is what the runner watches for. Without such a tool, ask in plain text and end the turn there, with no status line.
- Settle everything before the handoff. Every remaining question, open decision and review finding left for the user becomes a question asked that way, and its answer goes into the report. An answer that changes the plan substantively is a human-requested change: give it the single review round step 6 describes. `Status: AWAITING_APPROVAL` then means nothing is left to decide.
- Skip plan presentation even when it is on, since nobody watches the browser. Return the complete report in the terminal, with its review section or `Not reviewed:` line, marked `Not presented: unattended run.`
