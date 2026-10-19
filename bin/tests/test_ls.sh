# shellcheck shell=bash disable=SC2016
# tkt ls: one row per ticket worktree with the next step.

ls_fixture() {
  local id
  for id in ACM-1 ACM-2 ACM-3 ACM-4; do
    tkt start "$id" --prompt "$(prompt_with "$id")"
    expect_rc 0
  done
  local h1 h2 h3
  h1=$("$REAL_GIT" -C "$FAKE_WT_ROOT/ACM-1" rev-parse HEAD)
  h2=$("$REAL_GIT" -C "$FAKE_WT_ROOT/ACM-2" rev-parse HEAD)
  h3=$("$REAL_GIT" -C "$FAKE_WT_ROOT/ACM-3" rev-parse HEAD)
  jq -n --arg h1 "$h1" --arg h2 "$h2" --arg h3 "$h3" '[
    {number: 1, state: "OPEN", headRefName: "ACM-1", headRefOid: $h1, headRepository: {nameWithOwner: "acme/app"}, mergeable: "MERGEABLE",
     statusCheckRollup: [{name: "tests", status: "COMPLETED", conclusion: "SUCCESS"}],
     reviews: [{author: {login: "claude"}, body: ("**Automated review: CLEAR** \u00b7 head `" + $h1[0:7] + "` \u00b7 reviewed against ACM-1"), submittedAt: "2026-01-02T00:00:00Z", commit: {oid: $h1}}]},
    {number: 2, state: "OPEN", headRefName: "ACM-2", headRefOid: $h2, headRepository: {nameWithOwner: "acme/app"}, mergeable: "MERGEABLE",
     statusCheckRollup: [{name: "tests", status: "COMPLETED", conclusion: "SUCCESS"}],
     reviews: [{author: {login: "dev"}, body: ("Reviewed head: " + $h2 + "\nReviewed base: abc\nVerdict: CHANGES_REQUESTED"), submittedAt: "2026-01-02T00:00:00Z", commit: {oid: $h2}}]},
    {number: 3, state: "MERGED", headRefName: "ACM-3", headRefOid: $h3, headRepository: {nameWithOwner: "acme/app"}}]' >"$T/prs.json"
  export FAKE_GH_PRS=$T/prs.json
  export FAKE_HERDR_SCREEN=$'Plan ready.\nStatus: AWAITING_APPROVAL'
}

row() {
  grep -E "^$1 " <<<"$T_OUT"
}

test_ls_suggests_the_next_step() {
  ls_fixture
  tkt ls --no-fetch
  expect_rc 0
  assert_contains "$(head -n 1 <<<"$T_OUT")" "TICKET"
  assert_contains "$(row ACM-1)" "tkt run ACM-1 verify"
  assert_contains "$(row ACM-1)" "CLEAR"
  assert_contains "$(row ACM-1)" "ok 1"
  assert_contains "$(row ACM-2)" "tkt run ACM-2 address"
  assert_contains "$(row ACM-2)" "CHANGES_REQUESTED"
  assert_contains "$(row ACM-3)" "tkt done ACM-3"
  assert_contains "$(row ACM-3)" "#3 merged"
  assert_contains "$(row ACM-4)" "approve the plan in its tab"
  assert_contains "$(row ACM-4)" "AWAITING_APPROVAL"
  assert_not_contains "$T_OUT" "main"
}

test_ls_skips_other_worktrees() {
  "$REAL_GIT" -C "$REPO" worktree add -q -b chore/docs "$FAKE_WT_ROOT/chore-docs" main
  "$REAL_GIT" -C "$REPO" worktree add -q -b someone/acm-9 "$FAKE_WT_ROOT/ACM-9" main
  tkt ls --no-fetch
  expect_rc 0
  assert_contains "$T_ERR" "no ticket worktrees"
}

test_ls_reports_gh_failures() {
  ls_fixture
  FAKE_GH_FAIL=1 tkt ls --no-fetch
  expect_rc 1
  assert_contains "$(row ACM-1)" "?"
}

test_ls_outside_herdr() {
  ls_fixture
  unset HERDR_ENV
  tkt ls --no-fetch
  expect_rc 0
  assert_contains "$(row ACM-1)" "tkt run ACM-1 verify"
}
