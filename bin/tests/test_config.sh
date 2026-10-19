# shellcheck shell=bash disable=SC2016
# Configuration errors seen through the tkt command.

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

test_preset_with_bad_harness_exits_3() {
  mkdir -p "$XDG_CONFIG_HOME/tkt"
  echo 'odd vim - *' >"$XDG_CONFIG_HOME/tkt/presets"
  tkt start ACM-1 --dry-run
  expect_rc 3
  assert_contains "$T_ERR" "claude or opencode"
}
