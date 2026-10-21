// Which tickets need you right now. tkt ls says where each ticket stands, and
// this turns that into a short item for the board, or nothing. Items are
// worked out again on every read, so an item goes away as soon as its ticket
// moves on.

import type { Row } from "./model.ts";

export type AttentionKind = "plan" | "question" | "blocked" | "check" | "ready" | "merged" | "next" | "paused" | "stalled" | "failed";

export interface Attention {
	/** key is the same while the ticket stays in the same situation, so a
	 * notification goes out once. */
	key: string;
	ticket: string;
	kind: AttentionKind;
	/** title is a few words for the board. */
	title: string;
	/** text is the full line, with the commands that act on it. */
	text: string;
	/** byAuto marks an item of a ticket that tkt auto drives; its loop
	 * notifies by itself. */
	byAuto: boolean;
}

/** The next steps of a manual ticket that wait for your go. */
const nextSteps: Record<string, string> = {
	needs_review: "the pull request waits for review-pr",
	findings: "the review has findings to address",
	base_moved: "the base moved; address merges it",
	checks_fail: "the checks fail",
	qa_failed: "human verification failed",
};

/** attentionOf says whether a ticket needs you now. It says nothing while a
 * job for the ticket runs, and nothing while a tkt auto loop drives it,
 * unless the loop paused for you. */
export function attentionOf(row: Row, inFlight: boolean): Attention | undefined {
	if (inFlight) return undefined;
	const id = row.ticket;
	const a = row.auto;
	const reason = row.next?.reason ?? "";
	const base = [id, row.phase, row.pr?.head ?? "", row.last_status, reason, a?.paused ?? "", a?.why ?? ""].join("|");
	const item = (kind: AttentionKind, title: string, text: string, byAuto = false): Attention => ({ key: `${kind}|${base}`, ticket: id, kind, title, text, byAuto });
	if (row.phase === "merged") return item("merged", "merged", `${id} is merged. Say "remove it", or /done ${id}.`);
	if (a?.running) {
		// A pause stays in the loop's state until its next look. Once the
		// agent works again, you acted, such as approving the plan.
		if (a.paused && row.agent?.status === "working") return undefined;
		if (a.paused) return paused(row, item);
		if (row.phase === "ready_for_human") return item("ready", "ready to verify", readyText(row), true);
		return undefined;
	}
	if (a?.wanted) return item("stalled", "auto stalled", `${id} is marked for auto, but no tkt auto loop drives it. /auto ${id} on resumes it.`);
	switch (row.phase) {
		case "awaiting_approval":
			return item("plan", "plan ready", planText(id));
		case "ready_for_human":
			return item("ready", "ready to verify", readyText(row));
		case "no_agent":
			return item("check", "no agent", `${id}: no agent runs in its worktree. tkt resume ${id} brings it back.`);
		case "needs_you":
			return byReason(row, reason, item);
	}
	const step = nextSteps[reason];
	if (step && row.agent?.status !== "working") {
		return item("next", step, `${id}: ${step}. /next ${id}, or say "continue ${id}".`);
	}
	return undefined;
}

type Make = (kind: AttentionKind, title: string, text: string, byAuto?: boolean) => Attention;

/** paused words a tkt auto pause. The loop goes on by itself once the
 * ticket moves, so each line says what moves it. */
function paused(row: Row, item: Make): Attention {
	const id = row.ticket;
	const a = row.auto!;
	switch (a.paused) {
		case "needs_you":
			return byReason(row, a.why || row.next?.reason || "", item, true);
		case "plan_awaits_approval":
			return item("plan", "plan ready", `${planText(id)} tkt auto goes on after it.`, true);
		case "bot_silent":
			return item("paused", "review bot silent", `${id}: the review bot has not reviewed the head. Check its job in CI; tkt auto goes on when it posts.`, true);
		case "round_limit":
			return item("paused", "max rounds reached", `${id}: tkt auto paused after [auto] max_rounds fix runs. Read the review, then say what to do.`, true);
		case "no_progress":
			return item("paused", "no progress", `${id}: the last step changed nothing on the same head. /show ${id} for what the agent said.`, true);
		case "agent_missing":
			return item("paused", "no agent", `${id}: no agent runs in its worktree. tkt resume ${id} brings it back, and tkt auto goes on.`, true);
	}
	return item("paused", "auto paused", `${id}: tkt auto paused (${a.paused}). /peek ${id} shows its screen.`, true);
}

function byReason(row: Row, reason: string, item: Make, byAuto = false): Attention {
	const id = row.ticket;
	const after = byAuto ? " tkt auto goes on after it." : "";
	if (row.launch === "not_ready" || row.launch === "prompt_uncertain") {
		return item("check", "did not start", `${id}: the agent did not start cleanly. /peek ${id}, then fix it in its tab.`, byAuto);
	}
	switch (reason) {
		case "agent_blocked":
			return item("question", "asks a question", `${id}: the agent asks a question. /peek ${id} shows it; answer in its tab.${after}`, byAuto);
		case "status_blocked":
			return item("blocked", "Status: BLOCKED", `${id}: the agent stopped with Status: ${row.last_status || "BLOCKED"}. /show ${id} for its reason.${after}`, byAuto);
		case "review_awaits_approval":
			return item("check", "review draft waits", `${id}: review-pr waits for you to post or drop its draft, in its tab.${after}`, byAuto);
		case "bot_silent":
			return item("paused", "review bot silent", `${id}: the review bot has not reviewed the head. Check its job in CI.`, byAuto);
	}
	return item("check", "stopped, no status", `${id}: the agent stopped without a status line. /show ${id} for what it said.${after}`, byAuto);
}

function planText(id: string): string {
	return `${id}: the plan is ready. /show ${id} to read it, /approve ${id} to go on, or say what to change.`;
}

function readyText(row: Row): string {
	const pr = row.pr ? ` Pull request #${row.pr.number}` : " The pull request";
	return `${row.ticket}:${pr} is ready for human verification. /check ${row.ticket} walks the steps; merge when they pass.`;
}

/** sound picks the herdr notification sound for an item. */
export function sound(a: Attention): "done" | "request" {
	return a.kind === "ready" || a.kind === "merged" ? "done" : "request";
}
