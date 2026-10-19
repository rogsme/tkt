# shellcheck shell=bash disable=SC2016
# tkt run: replace the ticket's idle agent with a fresh session.

started_ticket() {
  tkt start ACM-12 "$@" --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  WT=$FAKE_WT_ROOT/ACM-12
  commit_on "$WT"
  : >"$FAKE_LOG"
  rm -f "$FAKE_DIR/prompts"
}

test_run_address_replaces_the_agent() {
  started_ticket
  local old_sid
  old_sid=$(state_of "$WT" session)
  tkt run ACM-12 address
  expect_rc 0
  assert_log "herdr agent prompt w1:p10 /exit"
  assert_log "herdr agent start acm-12 --kind claude --pane w1:p10 --timeout 90000 -- --dangerously-skip-permissions --session-id"
  assert_eq "$(sed '/end of prompt/d' "$FAKE_DIR/prompts")" "Use the address-pr-review skill to address the review on this branch's pull request."
  assert_eq "$(state_of "$WT" stage)" address
  [[ $(state_of "$WT" session) != "$old_sid" ]] || fail "kept the old session id"
  assert_house_rules
}

test_run_opencode_creates_a_build_session() {
  write_presets
  started_ticket --as fast
  tkt run ACM-12 sync
  expect_rc 0
  assert_log "herdr agent start acm-12 --kind opencode --pane w1:p10 --timeout 90000 -- -s ses_fake2 $WT"
  assert_contains "$(cat "$FAKE_DIR/prompts")" "sync the PR with the base"
}

test_run_custom_prompt() {
  started_ticket
  tkt run ACM-12 --prompt "Check the flaky test in ACM-12 again."
  expect_rc 0
  assert_contains "$(cat "$FAKE_DIR/prompts")" "Check the flaky test"
  assert_eq "$(state_of "$WT" stage)" custom
}

test_run_refuses_a_busy_agent() {
  started_ticket
  jq '.agents[0].agent_status = "working"' "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  tkt run ACM-12 address
  expect_rc 4
  assert_no_log "/exit"
}

test_run_guards_a_plan_waiting_for_approval() {
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  tkt run ACM-12 address
  expect_rc 4
  assert_contains "$T_ERR" "still in planning"
  tkt run ACM-12 address --force
  expect_rc 0
}

test_run_falls_back_to_ctrl_c() {
  started_ticket
  FAKE_HERDR_EXIT_IGNORED=1 tkt run ACM-12 verify
  expect_rc 0
  assert_log "herdr agent send-keys w1:p10 ctrl+c"
  assert_contains "$(cat "$FAKE_DIR/prompts")" "Human verification"
}

test_run_stops_when_the_agent_will_not_exit() {
  started_ticket
  FAKE_HERDR_EXIT_IGNORED=1 FAKE_HERDR_CTRLC_IGNORED=1 tkt run ACM-12 address
  expect_rc 5
  assert_contains "$T_ERR" "nothing was sent"
  [[ ! -f $FAKE_DIR/prompts ]] || fail "sent a stage line"
}

test_run_unknown_stage() {
  started_ticket
  tkt run ACM-12 deploy
  expect_rc 2
  assert_contains "$T_ERR" "unknown stage"
}

test_run_without_worktree() {
  tkt run ACM-99 address
  expect_rc 4
  assert_contains "$T_ERR" "tkt start ACM-99"
}

test_run_opens_a_tab_when_none_is_left() {
  started_ticket
  jq '.agents = [] | .tabs |= map(select(.tab_id == "w1:t1")) | .panes |= map(select(.pane_id == "w1:p1"))' \
    "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  tkt run ACM-12 address
  expect_rc 0
  assert_log "herdr tab create --workspace w1 --cwd $WT --label ACM-12 --no-focus"
}
