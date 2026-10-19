# shellcheck shell=bash disable=SC2016
# tkt done: remove a merged ticket's tabs and worktree.

# Starts ACM-12, commits on it, pushes it and merges it into main on origin.
merged_ticket() {
  tkt start ACM-12 --prompt "$(prompt_with ACM-12)"
  expect_rc 0
  WT=$FAKE_WT_ROOT/ACM-12
  commit_on "$WT"
  "$REAL_GIT" -C "$WT" push -q origin ACM-12
  "$REAL_GIT" -C "$REPO" merge -q --no-ff -m "Merge ACM-12" ACM-12
  "$REAL_GIT" -C "$REPO" push -q origin main
  write_prs MERGED
  : >"$FAKE_LOG"
}

write_prs() {
  jq -n --arg s "$1" --arg h "$("$REAL_GIT" -C "$WT" rev-parse HEAD)" \
    '[{number: 7, state: $s, headRefName: "ACM-12", headRefOid: $h, headRepository: {nameWithOwner: "acme/app"}}]' >"$T/prs.json"
  export FAKE_GH_PRS=$T/prs.json
}

test_done_removes_tab_and_worktree() {
  merged_ticket
  tkt 'done' ACM-12
  expect_rc 0
  assert_log "git -C $REPO fetch --prune --quiet origin"
  assert_log "herdr tab close w1:t10"
  assert_log "wt -C $REPO remove ACM-12 --foreground --reap --format json"
  [[ ! -d $WT ]] || fail "the worktree is still there"
  assert_contains "$T_OUT" "removed $WT and branch ACM-12"
  assert_eq "$(herdr_state '[.tabs[].tab_id]')" '["w1:t1"]'
  assert_house_rules
}

test_done_accepts_a_squash_merge() {
  merged_ticket
  "$REAL_GIT" -C "$REPO" reset -q --hard HEAD~1
  "$REAL_GIT" -C "$REPO" push -q -f origin main
  tkt 'done' ACM-12
  expect_rc 0
  assert_contains "$T_OUT" "kept branch ACM-12"
}

test_done_refuses_an_open_pr() {
  merged_ticket
  write_prs OPEN
  tkt 'done' ACM-12
  expect_rc 4
  assert_contains "$T_ERR" "pull request #7 is still open"
  assert_no_log "herdr tab close"
  assert_no_log "remove ACM-12"
  [[ -d $WT ]] || fail "removed the worktree"
}

test_done_refuses_uncommitted_and_unpushed_work() {
  merged_ticket
  echo draft >"$WT/notes.txt"
  commit_on "$WT"
  echo more >"$WT/scratch.txt"
  tkt 'done' ACM-12
  expect_rc 4
  assert_contains "$T_ERR" "uncommitted changes"
  assert_contains "$T_ERR" "neither the merged head nor in origin/main"
  assert_no_log "herdr tab close"
}

test_done_refuses_a_working_agent() {
  merged_ticket
  jq '.agents[0].agent_status = "working"' "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  tkt 'done' ACM-12
  expect_rc 4
  assert_contains "$T_ERR" "working or waiting"
}

test_done_refuses_from_inside_the_ticket_tab() {
  merged_ticket
  HERDR_TAB_ID=w1:t10 tkt 'done' ACM-12
  expect_rc 4
  assert_contains "$T_ERR" "the ticket's own tab"
}

test_done_refuses_foreign_panes() {
  merged_ticket
  jq --arg c "$WT/backend" '.panes += [{pane_id: "w1:p3", tab_id: "w1:t1", workspace_id: "w1", cwd: $c, foreground_cwd: $c}]' \
    "$FAKE_DIR/herdr.json" >"$T/h" && mv "$T/h" "$FAKE_DIR/herdr.json"
  tkt 'done' ACM-12
  expect_rc 4
  assert_contains "$T_ERR" "w1:p3"
}

test_done_all_needs_yes_without_a_terminal() {
  merged_ticket
  tkt 'done' --all </dev/null
  expect_rc 2
  tkt 'done' --all --yes </dev/null
  expect_rc 0
  [[ ! -d $WT ]] || fail "the worktree is still there"
}

test_done_dry_run() {
  merged_ticket
  tkt 'done' ACM-12 --dry-run
  expect_rc 0
  assert_contains "$T_ERR" "dry run: herdr tab close w1:t10"
  assert_no_log "herdr tab close"
  [[ -d $WT ]] || fail "removed the worktree"
}

test_done_other_forges_exit_3() {
  sed -i 's/^- Kind: GitHub/- Kind: Gitea/' "$REPO/docs/agents/forge.md"
  tkt 'done' ACM-12
  expect_rc 3
  assert_contains "$T_ERR" "GitHub forges only"
}
