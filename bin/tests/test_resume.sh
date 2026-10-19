# shellcheck shell=bash disable=SC2016
# tkt resume: relaunch the newest session with the right flags.

stopped_ticket() {
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  WT=$FAKE_WT_ROOT/ACM-12
  jq '.agents = []' "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  : >"$FAKE_LOG"
}

test_resume_claude_from_state_in_bypass_mode() {
  stopped_ticket
  local sid
  sid=$(state_of "$WT" session)
  tkt resume ACM-12
  expect_rc 0
  assert_log "herdr agent start acm-12 --kind claude --pane w1:p10 --timeout 90000 -- --resume $sid --dangerously-skip-permissions"
  assert_eq "$(state_of "$WT" status)" resumed
  [[ ! -f $FAKE_DIR/prompts || $(grep -c 'end of prompt' "$FAKE_DIR/prompts") == 1 ]] || fail "resume sent a prompt"
}

test_resume_does_nothing_while_an_agent_runs() {
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  : >"$FAKE_LOG"
  tkt resume ACM-12
  expect_rc 0
  assert_contains "$T_ERR" "already has an agent"
  assert_no_log "herdr agent start"
}

test_resume_discovers_the_newest_claude_session() {
  "$REAL_GIT" -C "$REPO" worktree add -q -b ACM-12 "$FAKE_WT_ROOT/ACM-12" main
  local dir
  dir=$HOME/.claude/projects/$(printf '%s' "$FAKE_WT_ROOT/ACM-12" | sed 's/[^A-Za-z0-9]/-/g')
  mkdir -p "$dir"
  touch -d '2026-01-01' "$dir/11111111-1111-1111-1111-111111111111.jsonl"
  touch -d '2026-01-02' "$dir/22222222-2222-2222-2222-222222222222.jsonl"
  tkt resume ACM-12
  expect_rc 0
  assert_log "-- --resume 22222222-2222-2222-2222-222222222222 --dangerously-skip-permissions"
  assert_log "herdr tab create --workspace w1 --cwd $FAKE_WT_ROOT/ACM-12 --label ACM-12 --no-focus"
}

test_resume_discovers_opencode_and_skips_plan_review() {
  write_presets
  "$REAL_GIT" -C "$REPO" worktree add -q -b ACM-12 "$FAKE_WT_ROOT/ACM-12" main
  jq -n --arg d "$FAKE_WT_ROOT/ACM-12" '{data: [
    {id: "ses_review", agent: "plan-review", parentID: null, location: {directory: $d}, time: {updated: 3}},
    {id: "ses_child", agent: "general", parentID: "ses_work", location: {directory: $d}, time: {updated: 2}},
    {id: "ses_work", agent: "build", parentID: null, location: {directory: $d}, time: {updated: 1}}]}' >"$T/sessions.json"
  FAKE_OC_SESSIONS=$T/sessions.json tkt resume ACM-12
  expect_rc 0
  assert_log "herdr agent start acm-12 --kind opencode --pane w1:p10 --timeout 90000 -- -s ses_work $FAKE_WT_ROOT/ACM-12"
  assert_log "--label ACM-12\ \(big\)"
}

test_resume_refuses_a_session_running_elsewhere() {
  stopped_ticket
  local sid
  sid=$(state_of "$WT" session)
  jq --arg s "$sid" --arg r "$REPO" '.agents += [{name: "other", agent: "claude", agent_status: "idle", pane_id: "w1:p1", tab_id: "w1:t1", cwd: $r, foreground_cwd: $r, agent_session: {value: $s}}]' \
    "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  tkt resume ACM-12
  expect_rc 4
  assert_contains "$T_ERR" "already runs in pane w1:p1"
}

test_resume_without_any_session() {
  "$REAL_GIT" -C "$REPO" worktree add -q -b ACM-12 "$FAKE_WT_ROOT/ACM-12" main
  tkt resume ACM-12
  expect_rc 4
  assert_contains "$T_ERR" "found no session"
}
