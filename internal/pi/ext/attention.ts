// Which ticket changes interrupt you. Everything else only moves the board.

import type { Row } from "./model.ts";

export type AttentionKind = "plan" | "question" | "ready" | "next" | "merged" | "auto_stop" | "resume_auto" | "failed";

export interface Attention {
	/** key is the same while the ticket stays in the same situation, so an
	 * item is raised once. */
	key: string;
	ticket: string;
	kind: AttentionKind;
	title: string;
	text: string;
	/** byAuto marks an item a tkt auto loop already notified about. */
	byAuto: boolean;
}

/** The fix and review steps tkt auto --once would take, and how to say them. */
const nextSteps: Record<string, string> = {
	needs_review: "the pull request waits for review",
	bot_clear: "the review bot is clear; review-pr gives the final verdict",
	findings: "the review has findings to address",
	base_moved: "the base moved; the branch needs a sync",
	checks_fail: "the checks fail",
	qa_failed: "human verification failed",
};

const autoStops: Record<string, string> = {
	needs_you: "the agent needs you",
	plan_awaits_approval: "the plan waits for your approval",
	agent_missing: "no agent runs for it",
	round_limit: "it reached [auto] max_rounds",
	no_progress: "its last step changed nothing",
	closed: "the pull request was closed",
	unknown: "it is in a state tkt auto does not know",
};

/** attentionOf says whether a ticket needs you now, or nothing when it does
 * not, when a job for it still runs, or while a tkt auto loop drives it. */
export function attentionOf(row: Row, inFlight: boolean): Attention | undefined {
	if (inFlight || row.auto?.running) return undefined;
	const id = row.ticket;
	const head = row.pr?.head ?? "";
	const base = `${id}|${row.phase}|${head}|${row.last_status}|${row.next?.reason ?? ""}`;
	const item = (kind: AttentionKind, title: string, text: string, byAuto = false): Attention => ({
		key: `${kind}|${base}|${row.auto?.stop ?? ""}`,
		ticket: id,
		kind,
		title,
		text,
		byAuto,
	});
	const stop = row.auto?.stop ?? "";
	if (stop && stop !== "stopped" && !row.auto?.wanted) {
		switch (stop) {
			case "ready_for_human":
				return item("ready", "ready for you", readyText(row), true);
			case "merged":
				return item("merged", "merged", `${id} is merged. Say "remove it" or /done ${id}.`, true);
			case "plan_awaits_approval":
				return item("plan", "plan ready", planText(id), true);
			case "needs_you":
				return item("question", "needs you", questionText(row), true);
		}
		if (autoStops[stop] && row.phase !== "merged") {
			return item("auto_stop", "auto stopped", `tkt auto stopped ${id}: ${autoStops[stop]}. /show ${id}, then /auto ${id} on to go on.`, true);
		}
	}
	if (row.auto?.wanted && !row.auto.running) {
		return item("resume_auto", "auto paused", `${id} was in auto mode, but no loop drives it. /auto ${id} on resumes it.`);
	}
	switch (row.phase) {
		case "awaiting_approval":
			return item("plan", "plan ready", planText(id));
		case "needs_you":
			return item("question", "needs you", questionText(row));
		case "ready_for_human":
			return item("ready", "ready for you", readyText(row));
		case "merged":
			return item("merged", "merged", `${id} is merged. Say "remove it" or /done ${id}.`);
	}
	const step = nextSteps[row.next?.reason ?? ""];
	if (step && row.agent?.status !== "working") {
		return item("next", "next step", `${id}: ${step}. /next ${id}, or say "continue ${id}".`);
	}
	return undefined;
}

function planText(id: string): string {
	return `${id}: the plan is ready. /show ${id} to read it, /approve ${id} to go on, or say what to change.`;
}

function questionText(row: Row): string {
	const id = row.ticket;
	if (row.agent?.status === "blocked") return `${id}: the agent asks a question. /peek ${id} shows it; answer in its tab.`;
	if (row.launch === "not_ready" || row.launch === "prompt_uncertain") return `${id}: the agent did not start cleanly. /peek ${id}, then fix it in its tab.`;
	if (row.last_status) return `${id}: the agent stopped with Status: ${row.last_status}. /show ${id} for its reason.`;
	return `${id}: the agent stopped without a status line. /show ${id} for what it said.`;
}

function readyText(row: Row): string {
	const pr = row.pr ? ` Pull request #${row.pr.number}` : " The pull request";
	return `${row.ticket}:${pr} is ready for human verification. /check ${row.ticket} walks the steps; merge when they pass.`;
}

/** sound picks the herdr notification sound for an item. */
export function sound(a: Attention): "done" | "request" {
	return a.kind === "ready" || a.kind === "merged" ? "done" : "request";
}
