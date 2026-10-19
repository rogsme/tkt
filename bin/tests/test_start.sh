# shellcheck shell=bash disable=SC2016
# tkt start: worktree, tab, agent, prompt.

test_start_claude_happy_path() {
  local p
  p=$(prompt_with ACM-12 'Model: frontier')
  tkt start ACM-12 --prompt "$p"
  expect_rc 0
  local wt=$FAKE_WT_ROOT/ACM-12
  [[ -d $wt ]] || fail "no worktree"
  assert_log "wt -C $REPO switch --create ACM-12 --no-cd --format json"
  assert_log "herdr tab create --workspace w1 --cwd $wt --label ACM-12 --no-focus"
  assert_log "herdr agent start acm-12 --kind claude --pane w1:p10 --timeout 90000 -- --dangerously-skip-permissions --session-id"
  assert_contains "$(cat "$FAKE_DIR/prompts")" "Work in the ACM-12 worktree."
  assert_eq "$(state_of "$wt" status)" submitted
  assert_eq "$(state_of "$wt" preset)" claude
  assert_eq "$(state_of "$wt" stage)" start
  assert_eq "$(state_of "$wt" session)" "$(herdr_state -r '.agents[0].agent_session.value')"
  assert_contains "$T_OUT" "ACM-12  $wt  tab w1:t10  agent acm-12 (claude, claude)"
  assert_house_rules
}

test_start_infers_id_and_picks_opencode_preset() {
  write_presets
  tkt start --prompt "$(prompt_with ACM-5 'Model: small (acme/fast)')"
  expect_rc 0
  local wt=$FAKE_WT_ROOT/ACM-5
  assert_contains "$T_ERR" "ticket ACM-5 (the first id in the prompt)"
  assert_log "herdr tab create --workspace w1 --cwd $wt --label ACM-5\ \(fast\) --no-focus"
  assert_log "opencode api POST /api/session -d"
  assert_log '\"agent\":\"build\"'
  assert_log '\"model\":\{\"providerID\":\"acme\"\,\"id\":\"fast\"\,\"variant\":\"max\"\}'
  assert_log "herdr agent start acm-5 --kind opencode --pane w1:p10 --timeout 90000 -- -s ses_fake1 $wt"
  assert_eq "$(state_of "$wt" harness)" opencode
  assert_eq "$(state_of "$wt" session)" ses_fake1
  assert_house_rules
}

test_start_reads_prompt_from_stdin_byte_exact() {
  local p
  p=$'Start ACM-3: it\'s "quoted" `code` $(not run) \\ backslash\n\nMore lines.'
  printf '%s' "$p" | {
    tkt start --prompt -
    expect_rc 0
  }
  assert_eq "$(sed '/^--- end of prompt ---$/d' "$FAKE_DIR/prompts")" "$p"
}

test_start_without_prompt_sends_the_start_line() {
  tkt start ACM-4
  expect_rc 0
  assert_contains "$(cat "$FAKE_DIR/prompts")" "Use the start-ticket skill to start ticket ACM-4."
}

test_start_refuses_id_mismatch() {
  tkt start ACM-12 --prompt "$(prompt_with ACM-13)"
  expect_rc 2
  assert_contains "$T_ERR" "first ticket is ACM-13"
  [[ ! -d $FAKE_WT_ROOT/ACM-12 ]] || fail "created a worktree"
  tkt start ACM-12 --force --prompt "$(prompt_with ACM-13)"
  expect_rc 0
}

test_start_refuses_model_conflict() {
  write_presets
  tkt start ACM-6 --as claude --prompt "$(prompt_with ACM-6 'Model: small')"
  expect_rc 2
  assert_no_log "wt -C"
}

test_start_refuses_a_second_agent() {
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  : >"$FAKE_LOG"
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 4
  assert_contains "$T_ERR" "already runs in"
  assert_no_log "herdr tab create"
}

test_start_matches_annotated_tabs_only() {
  jq --arg r "$REPO" '.tabs += [{tab_id: "w1:t2", workspace_id: "w1", label: "ACM-120 (fast)"}]
    | .panes += [{pane_id: "w1:p2", tab_id: "w1:t2", workspace_id: "w1", cwd: $r, foreground_cwd: $r}]' \
    "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  assert_log "herdr tab create --workspace w1 --cwd $FAKE_WT_ROOT/ACM-12 --label ACM-12"
}

test_start_reuses_an_idle_ticket_tab() {
  "$REAL_GIT" -C "$REPO" worktree add -q -b ACM-12 "$FAKE_WT_ROOT/ACM-12" main
  jq --arg c "$FAKE_WT_ROOT/ACM-12" '.tabs += [{tab_id: "w1:t2", workspace_id: "w1", label: "ACM-12 (blocked by 11)"}]
    | .panes += [{pane_id: "w1:p2", tab_id: "w1:t2", workspace_id: "w1", cwd: $c, foreground_cwd: $c}]' \
    "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  assert_contains "$T_ERR" "reusing worktree"
  assert_contains "$T_ERR" "reusing tab w1:t2"
  assert_no_log "wt -C $REPO switch"
  assert_no_log "herdr tab create"
  assert_log "herdr agent start acm-12 --kind claude --pane w1:p2"
}

test_start_refuses_a_branch_without_worktree() {
  "$REAL_GIT" -C "$REPO" branch ACM-12 main
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 4
  assert_contains "$T_ERR" "exists without a worktree"
  tkt start ACM-12 --force --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  assert_log "wt -C $REPO switch ACM-12 --no-cd --format json"
}

test_start_checks_out_a_remote_branch() {
  "$REAL_GIT" -C "$REPO" push -q origin main:ACM-12
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  assert_contains "$T_ERR" "exists on origin"
  assert_log "wt -C $REPO switch ACM-12 --no-cd --format json"
}

test_start_refuses_a_stale_remote_ref() {
  "$REAL_GIT" -C "$REPO" update-ref refs/remotes/origin/ACM-12 HEAD
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 4
  assert_contains "$T_ERR" "is stale"
}

test_start_stops_at_a_startup_dialog_then_continues() {
  FAKE_HERDR_START_STATUS=blocked FAKE_HERDR_SCREEN='Yes, I trust this folder' tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 5
  assert_contains "$T_ERR" "Yes, I trust this folder"
  assert_eq "$(state_of "$FAKE_WT_ROOT/ACM-12" status)" not_ready
  [[ ! -f $FAKE_DIR/prompts ]] || fail "sent a prompt to a blocked agent"
  jq '.agents[0].agent_status = "idle"' "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  assert_contains "$T_ERR" "continuing the start"
  assert_eq "$(grep -c 'end of prompt' "$FAKE_DIR/prompts")" 1
}

test_start_never_resends_a_stalled_prompt() {
  FAKE_HERDR_PROMPT_ERRORS=agent_prompt_stalled tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 5
  assert_eq "$(state_of "$FAKE_WT_ROOT/ACM-12" status)" prompt_uncertain
  assert_eq "$(grep -c 'end of prompt' "$FAKE_DIR/prompts")" 1
}

test_start_types_the_command_when_the_pane_stays_busy() {
  FAKE_HERDR_START_ERRORS=agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy,agent_pane_busy \
    tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  assert_contains "$T_ERR" "typing the command"
  assert_log "herdr pane run w1:p10 \\'claude\\'\\ \\'--dangerously-skip-permissions\\'"
  assert_log "herdr agent rename w1:p10 acm-12"
  assert_eq "$(herdr_state -r '.agents[0].name')" acm-12
}

test_start_dry_run_changes_nothing() {
  tkt start ACM-12 --dry-run --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  assert_contains "$T_ERR" "dry run: wt -C $REPO switch --create ACM-12"
  assert_contains "$T_ERR" "dry run: herdr agent prompt \\<pane\\> "
  [[ ! -d $FAKE_WT_ROOT/ACM-12 ]] || fail "created a worktree"
  assert_no_log "herdr tab create"
  assert_no_log "herdr agent start"
}

test_start_outside_herdr_exits_3() {
  unset HERDR_ENV
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 3
}

test_start_reports_a_failed_hook() {
  FAKE_WT_SWITCH_FAIL=1 tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 1
  assert_contains "$T_ERR" "hook pre-start"
}

test_start_rejects_a_hashed_directory() {
  "$REAL_GIT" -C "$REPO" worktree add -q -b ACM-12 "$FAKE_WT_ROOT/ACM-12-a1b2" main
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 3
  assert_contains "$T_ERR" "branch = directory = ticket id"
}

test_start_reads_wt_schema_2() {
  FAKE_WT_SCHEMA=2 tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
}
