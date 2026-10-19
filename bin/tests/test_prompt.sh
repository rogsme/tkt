# shellcheck shell=bash disable=SC2016
# Prompt parsing and guards, names, and the review logic behind tkt ls.

load_tkt() {
  # shellcheck source=bin/tkt
  source "$TESTS_DIR/../tkt"
  repo_resolve
  cfg_load
}

test_first_id_is_the_lead() {
  load_tkt
  assert_eq "$(prompt_first_id 'See ACM-1700x, then **ACM-17** and ACM-18 (rider).')" ACM-17
  assert_eq "$(prompt_first_id "ACM-9's worktree")" ACM-9
  assert_eq "$(prompt_first_id 'no ticket here')" ""
}

test_model_values() {
  load_tkt
  assert_eq "$(prompt_model_values $'Model: frontier.\n- `Model: frontier`, not small')" frontier
  assert_eq "$(prompt_model_values 'nothing')" ""
}

test_prompt_guards() {
  load_tkt
  set +e
  (prompt_guard "$(head -c 131001 /dev/zero | tr '\0' a)") 2>/dev/null
  assert_eq $? 2 "oversized"
  (prompt_guard '!rm -rf build') 2>/dev/null
  assert_eq $? 2 "leading !"
  (prompt_guard $'/review\nmore') 2>/dev/null
  assert_eq $? 2 "multi-line slash"
  (prompt_guard '   ') 2>/dev/null
  assert_eq $? 2 "empty"
  set -e
  prompt_guard '/exit'
  prompt_guard '- a list item'
  prompt_guard '# Start ticket: ACM-1'
  FORCE=1
  prompt_guard '!echo hi'
  set +e
  (prompt_guard "$(head -c 131001 /dev/zero | tr '\0' a)") 2>/dev/null
  assert_eq $? 2 "--force never lifts the size cap"
}

test_agent_names() {
  load_tkt
  assert_eq "$(agent_name_for ACM-12)" acm-12
  assert_eq "$(agent_name_for 12-X)" t-12-x
  assert_eq "$(agent_name_for ABCDEFGHIJKLMNOPQRSTUVWXYZ-123456789)" abcdefghijklmnopqrstuvwxyz-12345
}

test_shell_words() {
  load_tkt
  assert_eq "$(shell_words claude --session-id 'a b')" "'claude' '--session-id' 'a b'"
  set +e
  shell_words opencode "/tmp/it's" >/dev/null
  assert_eq $? 1
}

test_verdict_formats() {
  load_tkt
  local head=6f3f46aa71062cb73ab94d0f91993229aab65833 pr
  pr=$(jq -nc --arg h "$head" '{reviews: [
    {body: "**Automated review: FINDINGS** \u00b7 head `97c7506` \u00b7 reviewed against ACM-1", submittedAt: "2026-01-01T10:00:00Z", commit: {oid: "97c7506"}},
    {body: "**Automated review: CLEAR** \u00b7 head `6f3f46a` \u00b7 reviewed against ACM-1", submittedAt: "2026-01-02T10:00:00Z", commit: {oid: $h}},
    {body: "looks fine", submittedAt: "2026-01-03T10:00:00Z", commit: {oid: $h}}]}')
  assert_eq "$(jq -c --arg h "$head" "$JQ_REVIEW verdict(\$h) | [.v, .at_head]" <<<"$pr")" '["CLEAR",true]'
  pr=$(jq -nc '{reviews: [{body: "Summary\n\nReviewed head: 1111111111111111111111111111111111111111\nReviewed base: 2222222\nVerdict: CHANGES_REQUESTED\n", submittedAt: "2026-01-02T10:00:00Z", commit: {oid: "1111111"}}]}')
  assert_eq "$(jq -c --arg h "$head" "$JQ_REVIEW verdict(\$h) | [.v, .at_head]" <<<"$pr")" '["CHANGES_REQUESTED",false]'
  assert_eq "$(jq -c --arg h "$head" "$JQ_REVIEW verdict(\$h)" <<<'{"reviews": []}')" null
}

test_checks_summary() {
  load_tkt
  local rollup
  rollup='{"statusCheckRollup": [
    {"name": "tests", "status": "COMPLETED", "conclusion": "SUCCESS"},
    {"name": "lint", "status": "COMPLETED", "conclusion": "FAILURE"},
    {"context": "deploy", "state": "PENDING"}]}'
  assert_eq "$(jq -c "$JQ_REVIEW checks" <<<"$rollup")" '{"state":"fail","text":"fail: lint"}'
  rollup='{"statusCheckRollup": [{"name": "tests", "status": "IN_PROGRESS", "conclusion": null}, {"name": "x", "status": "COMPLETED", "conclusion": "SKIPPED"}]}'
  assert_eq "$(jq -c "$JQ_REVIEW checks" <<<"$rollup")" '{"state":"pending","text":"pending 1"}'
  assert_eq "$(jq -c "$JQ_REVIEW checks" <<<'{"statusCheckRollup": []}')" '{"state":"none","text":"-"}'
}

test_next_step_table() {
  load_tkt
  assert_eq "$(next_step ACM-1 MERGED idle - ok CLEAR true MERGEABLE)" "tkt done ACM-1"
  assert_eq "$(next_step ACM-1 CLOSED - - '' '' '' '')" "closed without merging"
  assert_eq "$(next_step ACM-1 OPEN working - ok '' '' MERGEABLE)" "wait (agent working)"
  assert_eq "$(next_step ACM-1 OPEN blocked - ok '' '' MERGEABLE)" "answer the agent in its tab"
  assert_eq "$(next_step ACM-1 '' - - '' '' '' '')" "tkt resume ACM-1"
  assert_eq "$(next_step ACM-1 '' idle AWAITING_APPROVAL '' '' '' '')" "approve the plan in its tab"
  assert_eq "$(next_step ACM-1 OPEN idle - pending FINDINGS true MERGEABLE)" "wait for CI"
  assert_eq "$(next_step ACM-1 OPEN idle - ok STALE_BASE true MERGEABLE)" "tkt run ACM-1 sync"
  assert_eq "$(next_step ACM-1 OPEN idle - ok CLEAR true CONFLICTING)" "tkt run ACM-1 sync"
  assert_eq "$(next_step ACM-1 OPEN idle - ok FINDINGS true MERGEABLE)" "tkt run ACM-1 address"
  assert_eq "$(next_step ACM-1 OPEN idle - fail CLEAR true MERGEABLE)" "tkt run ACM-1 ci"
  assert_eq "$(next_step ACM-1 OPEN idle - ok CLEAR true MERGEABLE)" "tkt run ACM-1 verify"
  assert_eq "$(next_step ACM-1 OPEN idle - ok CLEAR false MERGEABLE)" "wait for review"
}
