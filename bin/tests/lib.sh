# shellcheck shell=bash disable=SC2016
# Helpers for the tkt tests. The runner sources this file into every test.

TKT=$TESTS_DIR/tkt-link

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

assert_eq() {
  [[ $1 == "$2" ]] || fail "expected [$2], got [$1]${3:+ ($3)}"
}

assert_contains() {
  [[ $1 == *"$2"* ]] || fail "expected to find [$2] in: $1"
}

assert_not_contains() {
  [[ $1 != *"$2"* ]] || fail "did not expect [$2] in: $1"
}

assert_log() {
  grep -qF -- "$1" "$FAKE_LOG" || fail "the log has no line with [$1]:"$'\n'"$(cat "$FAKE_LOG")"
}

assert_no_log() {
  if grep -qF -- "$1" "$FAKE_LOG"; then fail "the log has a line with [$1]"; fi
}

# Runs tkt and keeps stdout, stderr and the exit code in T_OUT, T_ERR, T_RC.
tkt() {
  local errf
  errf=$(mktemp)
  set +e
  T_OUT=$("$TKT" "$@" 2>"$errf")
  T_RC=$?
  set -e
  T_ERR=$(<"$errf")
  rm -f "$errf"
}

expect_rc() {
  [[ $T_RC == "$1" ]] || fail "expected exit $1, got $T_RC"$'\n'"stdout: $T_OUT"$'\n'"stderr: $T_ERR"
}

# Rules every test checks: worktrees go through wt with hooks intact.
assert_house_rules() {
  if grep -E '^wt .*(--no-hooks|--clobber|--yes)' "$FAKE_LOG"; then fail "wt ran with a forbidden flag"; fi
  if grep -E '^git .*worktree (add|remove|prune|move)' "$FAKE_LOG"; then fail "tkt changed worktrees with git"; fi
  if grep -E '^herdr worktree' "$FAKE_LOG"; then fail "tkt used herdr worktrees"; fi
  if grep -E '^claude' "$FAKE_LOG"; then fail "tkt ran claude directly"; fi
  return 0
}

state_of() {
  local f
  f=$("$REAL_GIT" -C "$1" rev-parse --absolute-git-dir)/tkt/state
  sed -n "s/^$2=//p" "$f"
}

herdr_state() {
  jq -c "$@" "$FAKE_DIR/herdr.json"
}

# Commits and pushes an empty change on a ticket worktree.
commit_on() {
  "$REAL_GIT" -C "$1" commit -q --allow-empty -m "work on ${1##*/}"
}

prompt_with() {
  printf 'Load the start-ticket skill and follow it.\n\nWork in the %s worktree.\n%s\nStatus: AWAITING_APPROVAL\n' "$1" "${2:-}"
}

# Sets up a repository with a bare origin, the lifecycle configuration and a
# herdr session whose only pane sits in the primary checkout.
setup_sandbox() {
  T=$(mktemp -d)
  export HOME=$T/home XDG_CONFIG_HOME=$T/home/.config XDG_STATE_HOME=$T/home/.local/state
  export FAKE_DIR=$T/fake FAKE_LOG=$T/fake/log FAKE_WT_ROOT=$T/wt
  export GIT_AUTHOR_NAME=Acme GIT_AUTHOR_EMAIL=dev@example.com GIT_COMMITTER_NAME=Acme GIT_COMMITTER_EMAIL=dev@example.com
  mkdir -p "$HOME" "$FAKE_DIR" "$FAKE_WT_ROOT"
  : >"$FAKE_LOG"
  REPO=$T/app
  "$REAL_GIT" init -q --bare -b main "$T/origin.git"
  "$REAL_GIT" init -q -b main "$REPO"
  mkdir -p "$REPO/docs/agents"
  cat >"$REPO/docs/agents/issue-tracker.md" <<'CFG'
<!-- rogs-skills config v1: issue-tracker -->
# Issue tracker

## Tracker

- Kind: Example tracker
- Ticket ids: `ACM-<number>`, matched by `^ACM-[1-9][0-9]*$`.
CFG
  cat >"$REPO/docs/agents/forge.md" <<'CFG'
<!-- rogs-skills config v1: forge -->
# Forge

## Forge

- Kind: GitHub
- Host: github.com
- Repository: acme/app
- Remote: origin
- Base branch: main; re-read with: `gh repo view`

## Operations
CFG
  "$REAL_GIT" -C "$REPO" add -A
  "$REAL_GIT" -C "$REPO" commit -q -m init
  "$REAL_GIT" -C "$REPO" remote add origin "$T/origin.git"
  "$REAL_GIT" -C "$REPO" push -q -u origin main
  jq -n --arg r "$REPO" '{next: 10,
    workspaces: [{workspace_id: "w1", label: "acme"}],
    tabs: [{tab_id: "w1:t1", workspace_id: "w1", label: "main"}],
    panes: [{pane_id: "w1:p1", tab_id: "w1:t1", workspace_id: "w1", cwd: $r, foreground_cwd: $r}],
    agents: []}' >"$FAKE_DIR/herdr.json"
  export HERDR_ENV=1 HERDR_WORKSPACE_ID=w1 HERDR_TAB_ID=w1:t1 HERDR_PANE_ID=w1:p1
  cd "$REPO"
}

write_presets() {
  mkdir -p "$XDG_CONFIG_HOME/tkt"
  cat >"$XDG_CONFIG_HOME/tkt/presets" <<'CFG'
# name  harness   model              serves
claude  claude    -                  frontier
big     opencode  acme/big#high      frontier
fast     opencode  acme/fast#max      small
CFG
}
