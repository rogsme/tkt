# shellcheck shell=bash disable=SC2016
# Configuration, presets and stage lines.

load_tkt() {
  # shellcheck source=bin/tkt
  source "$TESTS_DIR/../tkt"
  repo_resolve
  cfg_load
}

test_config_reads_tracker_and_forge() {
  load_tkt
  assert_eq "$TICKET_RE" '^ACM-[1-9][0-9]*$'
  assert_eq "$TICKET_PREFIX" ACM
  assert_eq "$FORGE_KIND" GitHub
  assert_eq "$FORGE_HOST" github.com
  assert_eq "$FORGE_REPO" acme/app
  assert_eq "$REMOTE" origin
  assert_eq "$BASE" main
}

test_config_missing_files_exit_3() {
  rm "$REPO/docs/agents/forge.md"
  tkt start ACM-1 --dry-run
  expect_rc 3
  assert_contains "$T_ERR" setup-rogs
}

test_config_wrong_marker_exits_3() {
  sed -i '1s/v1/v2/' "$REPO/docs/agents/issue-tracker.md"
  tkt ls --no-fetch
  expect_rc 3
}

test_ids_are_normalized() {
  load_tkt
  assert_eq "$(normalize_id acm-12)" ACM-12
  assert_eq "$(normalize_id 12)" ACM-12
  assert_eq "$(normalize_id ACM-12)" ACM-12
  set +e
  (normalize_id ACM-0) 2>/dev/null
  assert_eq $? 2
}

test_builtin_presets() {
  load_tkt
  presets_load
  assert_eq "${P_ORDER[*]}" "claude opencode"
  assert_eq "${P_HARNESS[opencode]}" opencode
}

test_user_presets_keep_hash_in_models() {
  write_presets
  load_tkt
  presets_load
  assert_eq "${P_ORDER[*]}" "claude big fast"
  assert_eq "${P_MODEL[fast]}" acme/fast#max
  assert_eq "${P_SERVES[fast]}" small
}

test_preset_with_bad_harness_exits_3() {
  mkdir -p "$XDG_CONFIG_HOME/tkt"
  echo 'odd vim - *' >"$XDG_CONFIG_HOME/tkt/presets"
  tkt start ACM-1 --dry-run
  expect_rc 3
  assert_contains "$T_ERR" "claude or opencode"
}

test_preset_pick_follows_model_line() {
  write_presets
  load_tkt
  presets_load
  preset_pick "" "$(prompt_with ACM-1 '- Model: `small` (acme/fast)')"
  assert_eq "$PRESET" fast
  preset_pick "" "$(prompt_with ACM-1 'Model: frontier.')"
  assert_eq "$PRESET" claude
  preset_pick "" "$(prompt_with ACM-1)"
  assert_eq "$PRESET" claude
  preset_pick big "$(prompt_with ACM-1 'Model: frontier')"
  assert_eq "$PRESET" big
}

test_preset_pick_refuses_conflicts() {
  write_presets
  load_tkt
  presets_load
  set +e
  (preset_pick claude "Model: small") 2>/dev/null
  assert_eq $? 2 "--as against the Model line"
  (preset_pick "" $'Model: small\nModel: frontier') 2>/dev/null
  assert_eq $? 2 "two Model values"
  (preset_pick "" "Model: huge") 2>/dev/null
  assert_eq $? 2 "nothing serves the value"
  set -e
  FORCE=1
  preset_pick claude "Model: small"
  assert_eq "$PRESET" claude
}

test_stage_lines_and_overrides() {
  load_tkt
  stages_load
  assert_eq "$(stage_line start ACM-7)" "Use the start-ticket skill to start ticket ACM-7."
  mkdir -p "$XDG_CONFIG_HOME/tkt"
  printf '# mine\naddress  Address ticket {id} please.\nlint Run the linter.\n' >"$XDG_CONFIG_HOME/tkt/stages"
  stages_load
  assert_eq "$(stage_line address ACM-7)" "Address ticket ACM-7 please."
  assert_eq "$(stage_line lint ACM-7)" "Run the linter."
  set +e
  (stage_line nope ACM-7) 2>/dev/null
  assert_eq $? 2
}
