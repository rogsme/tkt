package apperr

// Code is a stable, snake_case name for a kind of failure. Programs that run
// tkt branch on it; it never changes meaning once released.
type Code string

// Exit statuses. Each code maps to exactly one.
const (
	ExitOK          = 0
	ExitTool        = 1 // an external command failed
	ExitUsage       = 2 // bad usage or refused input
	ExitEnvironment = 3 // environment or configuration
	ExitState       = 4 // refused because of the current state
	ExitUncertain   = 5 // uncertain outcome: inspect before retrying
)

// Info describes one code.
type Info struct {
	Code    Code   `json:"code"`
	Exit    int    `json:"exit"`
	Summary string `json:"summary"`
}

const (
	Usage                Code = "usage"
	InvalidTicketID      Code = "invalid_ticket_id"
	TicketIDMissing      Code = "ticket_id_missing"
	TicketIDMismatch     Code = "ticket_id_mismatch"
	InvalidScratchName   Code = "invalid_scratch_name"
	UnknownPreset        Code = "unknown_preset"
	UnknownStage         Code = "unknown_stage"
	PresetModelMismatch  Code = "preset_model_mismatch"
	AmbiguousModel       Code = "ambiguous_model"
	NoPresetForModel     Code = "no_preset_for_model"
	PromptTooLarge       Code = "prompt_too_large"
	PromptEmpty          Code = "prompt_empty"
	PromptShellEscape    Code = "prompt_shell_escape"
	PromptSlashCommand   Code = "prompt_slash_command"
	PromptInvalid        Code = "prompt_invalid"
	PromptFileUnreadable Code = "prompt_file_unreadable"
	ConfirmationRequired Code = "confirmation_required"
	Cancelled            Code = "cancelled"

	ToolMissing            Code = "tool_missing"
	ToolTooOld             Code = "tool_too_old"
	NotARepository         Code = "not_a_repository"
	LifecycleConfigMissing Code = "lifecycle_config_missing"
	LifecycleConfigMarker  Code = "lifecycle_config_marker"
	LifecycleConfigVersion Code = "lifecycle_config_version"
	TicketPatternMissing   Code = "ticket_pattern_missing"
	TicketPatternInvalid   Code = "ticket_pattern_invalid"
	ForgeConfigIncomplete  Code = "forge_config_incomplete"
	ForgeUnsupported       Code = "forge_unsupported"
	ConfigInvalid          Code = "config_invalid"
	ConfigLegacy           Code = "config_legacy"
	HerdrEnvMissing        Code = "herdr_env_missing"
	WorktreeLayout         Code = "worktree_layout"
	WorktreeBranchMismatch Code = "worktree_branch_mismatch"
	NoPresetForHarness     Code = "no_preset_for_harness"
	ChecksFailed           Code = "checks_failed"
	UpdateUnsupported      Code = "update_unsupported"
	UpdateNotWritable      Code = "update_not_writable"
	PiMissing              Code = "pi_missing"
	TerminalRequired       Code = "terminal_required"
	ServiceUnsupported     Code = "service_unsupported"

	TicketLocked        Code = "ticket_locked"
	WorktreeLockTimeout Code = "worktree_lock_timeout"
	WorktreeMissing     Code = "worktree_missing"
	BranchNoWorktree    Code = "branch_without_worktree"
	StaleRemoteRef      Code = "stale_remote_ref"
	AgentRunning        Code = "agent_running"
	AgentBusy           Code = "agent_busy"
	AgentStateUnknown   Code = "agent_state_unknown"
	AgentMissing        Code = "agent_missing"
	AmbiguousPane       Code = "ambiguous_pane"
	PaneOutsideWorktree Code = "pane_outside_worktree"
	PaneIsSelf          Code = "pane_is_self"
	PaneBusy            Code = "pane_busy"
	TabInUse            Code = "tab_in_use"
	TicketInPlanning    Code = "ticket_in_planning"
	SessionMissing      Code = "session_missing"
	SessionInUse        Code = "session_in_use"
	TicketsKept         Code = "tickets_kept"
	WaitTimeout         Code = "wait_timeout"
	UpdateDevBuild      Code = "update_dev_build"
	NoPlanToApprove     Code = "no_plan_to_approve"
	NoVerification      Code = "no_verification_steps"
	AutoRunning         Code = "auto_running"
	AutoStopped         Code = "auto_stopped"
	WorktreeDirty       Code = "worktree_dirty"
	BranchNotPushed     Code = "branch_not_pushed"
	UnpushedCommits     Code = "unpushed_commits"
	SyncConflict        Code = "sync_conflict"
	WatchRunning        Code = "watch_running"

	WtFailed          Code = "wt_failed"
	WtSwitchFailed    Code = "wt_switch_failed"
	WtHookFailed      Code = "wt_hook_failed"
	WtRemoveFailed    Code = "wt_remove_failed"
	GitFailed         Code = "git_failed"
	HerdrFailed       Code = "herdr_failed"
	AgentLaunchFailed Code = "agent_launch_failed"
	PromptSendFailed  Code = "prompt_send_failed"
	OpenCodeFailed    Code = "opencode_failed"
	ForgeQueryFailed  Code = "forge_query_failed"
	ReleaseFailed     Code = "release_query_failed"
	UpdateChecksum    Code = "update_checksum"
	UpdateVerify      Code = "update_verify_failed"
	Internal          Code = "internal"
	PiUnpackFailed    Code = "pi_unpack_failed"
	ServiceFailed     Code = "service_failed"

	AgentNotReady   Code = "agent_not_ready"
	PromptUncertain Code = "prompt_uncertain"
	AgentExitFailed Code = "agent_exit_failed"
	Interrupted     Code = "interrupted"
)

// Registry lists every code. It is the only place an exit status is assigned.
var Registry = []Info{
	{Usage, ExitUsage, "the command line is wrong: an unknown command or flag, or a missing or extra argument"},
	{InvalidTicketID, ExitUsage, "an argument is not a ticket id under the tracker's id pattern"},
	{TicketIDMissing, ExitUsage, "no ticket id was given and the prompt names none"},
	{TicketIDMismatch, ExitUsage, "the ticket id differs from the first ticket the prompt names"},
	{InvalidScratchName, ExitUsage, "the --scratch name is not a plain branch name, or it is a ticket id"},
	{UnknownPreset, ExitUsage, "no preset has that name"},
	{UnknownStage, ExitUsage, "no stage has that name"},
	{PresetModelMismatch, ExitUsage, "the chosen preset does not serve the prompt's Model: value"},
	{AmbiguousModel, ExitUsage, "the prompt names several Model: values"},
	{NoPresetForModel, ExitUsage, "no preset serves the prompt's Model: value"},
	{PromptTooLarge, ExitUsage, "the prompt is over the size limit"},
	{PromptEmpty, ExitUsage, "the prompt is empty"},
	{PromptShellEscape, ExitUsage, "the prompt starts with '!', which switches the agent to shell mode"},
	{PromptSlashCommand, ExitUsage, "the prompt starts with '/' and has several lines"},
	{PromptInvalid, ExitUsage, "the prompt holds a NUL byte"},
	{PromptFileUnreadable, ExitUsage, "the prompt file cannot be read"},
	{ConfirmationRequired, ExitUsage, "the command needs --yes when stdin is not a terminal"},
	{Cancelled, ExitUsage, "the answer to a confirmation was no"},

	{ToolMissing, ExitEnvironment, "a required program is not installed"},
	{ToolTooOld, ExitEnvironment, "a required program is older than tkt supports"},
	{NotARepository, ExitEnvironment, "the directory is not inside a git repository"},
	{LifecycleConfigMissing, ExitEnvironment, "the project has no lifecycle configuration; run /setup-rogs"},
	{LifecycleConfigMarker, ExitEnvironment, "a lifecycle configuration file lacks its rogs-skills marker"},
	{LifecycleConfigVersion, ExitEnvironment, "the lifecycle configuration uses a contract version tkt does not read"},
	{TicketPatternMissing, ExitEnvironment, "the tracker configuration has no ticket id pattern"},
	{TicketPatternInvalid, ExitEnvironment, "the ticket id pattern does not compile"},
	{ForgeConfigIncomplete, ExitEnvironment, "the forge configuration lacks a required field"},
	{ForgeUnsupported, ExitEnvironment, "the command does not support this forge"},
	{ConfigInvalid, ExitEnvironment, "tkt's own configuration file is invalid"},
	{ConfigLegacy, ExitEnvironment, "tkt's configuration is in the old presets and stages files"},
	{HerdrEnvMissing, ExitEnvironment, "tkt does not run inside a herdr pane"},
	{WorktreeLayout, ExitEnvironment, "the worktree directory is not named after its branch"},
	{WorktreeBranchMismatch, ExitEnvironment, "the worktree is not on the ticket's branch"},
	{NoPresetForHarness, ExitEnvironment, "no preset uses the session's harness"},
	{ChecksFailed, ExitEnvironment, "tkt doctor found a failing check"},
	{UpdateUnsupported, ExitEnvironment, "no release build exists for this system"},
	{UpdateNotWritable, ExitEnvironment, "tkt cannot replace its own binary"},
	{PiMissing, ExitEnvironment, "tkt agent has no pi to run: this tkt has no bundled pi and TKT_PI names none"},
	{TerminalRequired, ExitEnvironment, "the command is interactive and needs a terminal on stdin and stdout"},
	{ServiceUnsupported, ExitEnvironment, "tkt watch --install found neither systemd nor launchd to run the services"},

	{TicketLocked, ExitState, "another tkt command is working on the ticket"},
	{WorktreeLockTimeout, ExitState, "another tkt start kept the worktree lock too long"},
	{WorktreeMissing, ExitState, "the ticket has no worktree"},
	{BranchNoWorktree, ExitState, "the ticket's branch exists without a worktree"},
	{StaleRemoteRef, ExitState, "the remote-tracking branch is gone from the remote"},
	{AgentRunning, ExitState, "an agent already runs in the ticket's worktree"},
	{AgentBusy, ExitState, "the agent is working or waiting for an answer"},
	{AgentStateUnknown, ExitState, "the agent is in a state tkt does not recognize"},
	{AgentMissing, ExitState, "no agent runs for the ticket"},
	{AmbiguousPane, ExitState, "several panes could be the ticket's; pick one with --pane"},
	{PaneOutsideWorktree, ExitState, "the pane works outside the ticket's worktree"},
	{PaneIsSelf, ExitState, "the pane is the one running tkt"},
	{PaneBusy, ExitState, "the pane is not at a shell prompt"},
	{TabInUse, ExitState, "the ticket's tab is busy or holds other work"},
	{TicketInPlanning, ExitState, "the ticket is still in planning, so its agent may hold a plan waiting for approval"},
	{SessionMissing, ExitState, "the ticket has no session to resume"},
	{SessionInUse, ExitState, "the session already runs in another pane"},
	{TicketsKept, ExitState, "tkt done kept one or more tickets"},
	{WaitTimeout, ExitState, "the agent did not reach a requested state in time"},
	{UpdateDevBuild, ExitState, "this tkt is a development build; tkt update replaces it only with --force"},
	{NoPlanToApprove, ExitState, "the agent's last status is not AWAITING_APPROVAL, so there is no plan to approve"},
	{NoVerification, ExitState, "no review says READY_FOR_HUMAN at the pull request's head with Human verification steps"},
	{AutoRunning, ExitState, "a tkt auto loop already drives the ticket"},
	{AutoStopped, ExitState, "tkt auto stopped because the ticket needs a person; details.stop says why"},
	{WorktreeDirty, ExitState, "the ticket's worktree has uncommitted changes or a merge in progress"},
	{BranchNotPushed, ExitState, "the ticket's branch is not on the remote yet"},
	{UnpushedCommits, ExitState, "the ticket's branch has commits that are not on the remote"},
	{SyncConflict, ExitState, "merging the base conflicts, and --no-agent kept tkt sync from starting a session to resolve it"},
	{WatchRunning, ExitState, "a tkt watch already runs for the repository, or did not stop when asked"},

	{WtFailed, ExitTool, "worktrunk failed or printed output tkt cannot read"},
	{WtSwitchFailed, ExitTool, "wt switch failed"},
	{WtHookFailed, ExitTool, "wt created the worktree but one of its hooks failed"},
	{WtRemoveFailed, ExitTool, "wt remove failed"},
	{GitFailed, ExitTool, "a git command failed"},
	{HerdrFailed, ExitTool, "a herdr command failed"},
	{AgentLaunchFailed, ExitTool, "the agent could not be started"},
	{PromptSendFailed, ExitTool, "sending the prompt failed"},
	{OpenCodeFailed, ExitTool, "the OpenCode API failed"},
	{ForgeQueryFailed, ExitTool, "reading pull requests from the forge failed"},
	{ReleaseFailed, ExitTool, "reading or downloading a tkt release failed"},
	{UpdateChecksum, ExitTool, "the downloaded binary does not match SHA256SUMS; nothing was replaced"},
	{UpdateVerify, ExitTool, "the downloaded binary did not run or did not print its version; nothing was replaced"},
	{Internal, ExitTool, "an unexpected failure inside tkt"},
	{PiUnpackFailed, ExitTool, "tkt cannot unpack its bundled pi, or write the extension or settings it starts pi with"},
	{ServiceFailed, ExitTool, "a systemctl, launchctl or loginctl command failed, or tkt cannot write a service's file"},

	{AgentNotReady, ExitUncertain, "the agent stopped at a startup dialog; answer it in its pane"},
	{PromptUncertain, ExitUncertain, "tkt cannot confirm the agent received the prompt; check before sending it again"},
	{AgentExitFailed, ExitUncertain, "the old agent did not exit; nothing was sent"},
	{Interrupted, ExitUncertain, "tkt was interrupted; check the ticket's pane before retrying"},
}

var byCode = func() map[Code]Info {
	m := make(map[Code]Info, len(Registry))
	for _, i := range Registry {
		m[i.Code] = i
	}
	return m
}()

// Lookup returns the registry entry for code. An unknown code is internal.
func Lookup(code Code) Info {
	if i, ok := byCode[code]; ok {
		return i
	}
	return byCode[Internal]
}

// ExitMeanings describes each exit status, for help and schema output.
var ExitMeanings = []struct {
	Exit    int    `json:"exit"`
	Meaning string `json:"meaning"`
}{
	{ExitOK, "done"},
	{ExitTool, "an external command failed"},
	{ExitUsage, "bad usage or refused input"},
	{ExitEnvironment, "environment or configuration"},
	{ExitState, "refused because of the current state"},
	{ExitUncertain, "uncertain outcome: inspect the ticket's tab before you retry"},
}
