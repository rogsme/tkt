// Which tickets need you right now. tkt ls says where each ticket stands, and
// this turns that into a short item for the board, or nothing, with the
// answers /inbox offers for it. Items are worked out again on every read, so
// an item goes away as soon as its ticket moves on.

import { type Row, type Unblocked, verifiedAt } from "./model.ts";

export type AttentionKind = "plan" | "question" | "blocked" | "check" | "ready" | "merged" | "next" | "paused" | "stalled" | "failed" | "unblocked" | "chore";

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
	/** byAuto marks an item of a ticket that tkt auto drives, or that tkt
	 * watch paused on; both notify by themselves. */
	byAuto: boolean;
	/** offers are the answers the item's situation calls for, such as one
	 * more fix run; /inbox puts them before its kind's usual answers. */
	offers?: Choice[];
	/** chore marks an item of a chore's run, which tkt auto does not drive
	 * and which has no plan to approve. */
	chore?: boolean;
}

/** Choice is one answer /inbox offers for an item. do names the action, and
 * stage is the stage a run starts. */
export interface Choice {
	label: string;
	description?: string;
	do: "approve" | "approve_auto" | "show" | "reply" | "peek" | "dismiss" | "check" | "next" | "done" | "auto_on" | "auto_off" | "run" | "resume" | "start" | "start_auto" | "start_after_plan";
	stage?: string;
}

const look: Choice[] = [
	{ label: "Peek at the agent's screen", do: "peek" },
	{ label: "Show its last message", do: "show" },
];
const dismiss: Choice = { label: "Dismiss", do: "dismiss" };

/** usual holds each kind's answers, whatever the situation. */
const usual: Record<AttentionKind, Choice[]> = {
	plan: [
		{ label: "Approve", do: "approve" },
		{ label: "Approve and go auto", description: "tkt auto then drives it until it is reviewed clean.", do: "approve_auto" },
		{ label: "Show the plan", do: "show" },
		{ label: "Ask for changes", do: "reply" },
	],
	question: [...look, dismiss],
	check: [...look, dismiss],
	blocked: [...look, dismiss],
	paused: [...look, dismiss],
	ready: [{ label: "Open the checklist", do: "check" }, dismiss],
	chore: [{ label: "Show its last message", do: "show" }, dismiss],
	next: [
		{ label: "Continue", do: "next" },
		{ label: "Show its last message", do: "show" },
		{ label: "Hand it to tkt auto", description: "tkt auto takes this step and drives it until it is reviewed clean.", do: "auto_on" },
	],
	merged: [{ label: "Remove it (tkt done)", do: "done" }],
	stalled: [
		{ label: "Resume tkt auto", do: "auto_on" },
		{ label: "Turn auto off", description: "Clears its auto mark, so you drive it yourself.", do: "auto_off" },
		dismiss,
	],
	failed: [dismiss],
	unblocked: [
		{ label: "Start it", description: "tkt start opens its tab, and start-ticket plans it.", do: "start" },
		{ label: "Start it in auto", description: "tkt auto approves the plan too, and drives it until it is reviewed clean.", do: "start_auto" },
		{ label: "Start it in auto after the plan", description: "You approve the plan; tkt auto drives it from there.", do: "start_after_plan" },
		dismiss,
	],
};

/** ticketOnly are the answers that need a ticket: tkt auto drives tickets
 * only, and a chore's run has no plan. */
const ticketOnly = new Set<Choice["do"]>(["approve", "approve_auto", "auto_on", "auto_off"]);

/** choicesOf lists the answers /inbox offers for an item: its situation's
 * first, so Enter takes the step that moves the ticket on, then its kind's. */
export function choicesOf(a: Attention): Choice[] {
	return [...(a.offers ?? []), ...usual[a.kind]].filter((c) => !(a.chore && ticketOnly.has(c.do)));
}

const reply: Choice = { label: "Reply to the agent", description: "Type what to tell it; it reads the text as a new prompt.", do: "reply" };
const resume: Choice = { label: "Bring the agent back", description: "tkt resume relaunches its newest session.", do: "resume" };
const review: Choice = { label: "Run review-pr instead", description: "review-pr reviews the head in a fresh session.", do: "run", stage: "review" };

/** rerun offers the stage tkt ls's next step runs, or nothing when the step
 * runs none. */
function rerun(row: Row, label: (stage: string) => string, description: string): Choice[] {
	const v = row.next?.argv;
	const stage = v?.length === 4 && v[1] === "run" ? v[3] : "";
	return stage ? [{ label: label(stage), description, do: "run", stage }] : [];
}

/** The next steps tkt watch takes by itself while it runs. It reviews too,
 * but only the head a sync made for a merge order. */
const watchTakes = new Set(["base_moved", "checks_fail", "merge_due", "sync_failed_fix"]);
const watchTakesFor = (row: Row, reason: string) => watchTakes.has(reason) || (reason === "needs_review" && !!row.merge);

/** The next steps of a manual ticket that wait for your go. */
const nextSteps: Record<string, string> = {
	needs_review: "the pull request waits for review-pr",
	findings: "the review has findings to address",
	base_moved: "the base moved; tkt sync merges it",
	checks_fail: "the checks fail",
	qa_failed: "human verification failed",
	verify_failed: "verify-pr found a step that fails",
	merge_due: "the checks passed, and its merge order is due",
	sync_failed_fix: "the sync failed; tkt sync hands it to a fresh session",
};

/** attentionOf says whether a ticket needs you now. It says nothing while a
 * job for the ticket runs, and nothing while a tkt auto loop drives it,
 * unless the loop paused for you. A ticket on hold still shows what its
 * agent needs, such as a plan or a question, but no step to take: you put
 * it there to wait. */
export function attentionOf(row: Row, inFlight: boolean, watching = false): Attention | undefined {
	if (inFlight) return undefined;
	const id = row.ticket;
	const a = row.auto;
	const reason = row.next?.reason ?? "";
	const base = [id, row.phase, row.pr?.head ?? "", row.last_status, reason].join("|");
	// A pause's own reason is part of its situation. A plan or a question
	// is the same item whoever waits on it, so a loop that pauses on the
	// hold you set does not announce the plan again.
	const keyOf = (kind: AttentionKind) => (kind === "paused" ? `${kind}|${base}|${a?.paused ?? ""}|${a?.why ?? ""}` : `${kind}|${base}`);
	const item = (kind: AttentionKind, title: string, text: string, byAuto = false, offers?: Choice[]): Attention => ({ key: keyOf(kind), ticket: id, kind, title, text, byAuto, offers, chore: !!row.chore });
	if (row.phase === "merged" && a?.stop === "post_merge_failed") return item("failed", "post-merge CI failed", `${id} merged in yolo, but the checks on the base failed after it. /show ${id} for finish-ticket's report; tkt leaves the ticket as it is.`);
	if (row.phase === "merged") return item("merged", "merged", `${id} is merged. Say "remove it", or /done ${id}.`);
	if (a?.running && !row.hold) {
		// A pause stays in the loop's state until its next look. Once the
		// agent works again, you acted, such as approving the plan.
		if (a.paused && row.agent?.status === "working") return undefined;
		if (a.paused) return paused(row, item);
		// The loop runs verify-pr itself; it stops when the steps need you.
		if (row.phase === "ready_for_human" && reason !== "needs_verify") return item("ready", "ready to verify", readyText(row), true);
		return undefined;
	}
	if (a?.wanted && !row.hold) return item("stalled", "auto stalled", `${id} is marked for auto, but no tkt auto loop drives it. /auto ${id} on resumes it.`);
	if (row.watch?.paused) return watchPaused(row, item);
	if (row.chore) {
		// No skill reviews, fixes or merges a pull request without a ticket,
		// so a chore's run comes to you where a ticket's goes on.
		const pr = row.pr ? `pull request #${row.pr.number}` : "pull request";
		switch (reason) {
			case "chore_ready":
				return item("chore", "ready to merge", `${id}: chore ${row.chore}'s ${pr} is ready. Review it and merge it yourself; tkt watch removes the run after the merge.`);
			case "chore_findings":
				return item("chore", "review found something", `${id}: the review found something on chore ${row.chore}'s ${pr}. Read it, then tell its agent what to fix, or merge it as it is.`, false, [reply]);
			case "no_changes":
				return watching ? undefined : item("merged", "nothing to change", `${id}: chore ${row.chore} found nothing to change. /done ${id} removes the run.`);
			case "closed":
				return watching ? undefined : item("merged", "closed", `${id}: you closed chore ${row.chore}'s ${pr}. /done ${id} removes the run.`);
			case "no_agent":
				if (row.next?.argv?.[1] === "chore") return item("check", "did not start", `${id}: the run of chore ${row.chore} stopped before its agent got the prompt. /chores run ${row.chore} goes on with it, or /done ${id} drops it.`);
		}
	}
	switch (row.phase) {
		case "awaiting_approval":
			return item("plan", "plan ready", planText(id));
		case "ready_for_human":
			return item("ready", "ready to verify", readyText(row));
		case "no_agent":
			return item("check", "no agent", `${id}: no agent runs in its worktree. tkt resume ${id} brings it back.`, false, [resume]);
		case "needs_you":
			return byReason(row, reason, item);
	}
	const step = nextSteps[reason];
	if (step && row.agent?.status !== "working" && !row.hold && !(watching && watchTakesFor(row, reason))) {
		return item("next", step, `${id}: ${step}. /next ${id}, or say "continue ${id}".`);
	}
	return undefined;
}

type Make = (kind: AttentionKind, title: string, text: string, byAuto?: boolean, offers?: Choice[]) => Attention;

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
			return item("paused", "review bot silent", `${id}: the review bot has not reviewed the head. Check its job in CI; tkt auto goes on when it posts.`, true, [review]);
		case "round_limit": {
			// tkt run leaves the loop's round count alone, so the loop takes
			// the run's outcome and pauses here again if the ticket still
			// needs a fix.
			const more = rerun(row, (s) => `Run ${s} once more`, "One more fix run in a fresh session. tkt auto goes on after it, and pauses again if it still needs a fix.");
			return item("paused", "max rounds reached", `${id}: tkt auto paused after [auto] max_rounds fix runs. Read the review, then say what to do.`, true, more);
		}
		case "no_progress": {
			const again = rerun(row, (s) => `Run ${s} again`, "The same step in a fresh session. tkt auto goes on if it changes something.");
			return item("paused", "no progress", `${id}: the last step changed nothing on the same head. /show ${id} for what the agent said.`, true, again);
		}
		case "agent_missing":
			return item("paused", "no agent", `${id}: no agent runs in its worktree. tkt resume ${id} brings it back, and tkt auto goes on.`, true, [resume]);
		case "sync_failed":
			return item("paused", "sync failed", `${id}: tkt sync cannot merge the base${a.note ? `: ${a.note}` : ""}. tkt auto tries again at each look, and goes on once it can.`, true);
	}
	return item("paused", "auto paused", `${id}: tkt auto paused (${a.paused}). /peek ${id} shows its screen.`, true);
}

/** watchPaused words a tkt watch pause. watch notified already, and goes on
 * once the ticket moves, such as after a run you start here. */
function watchPaused(row: Row, item: Make): Attention {
	const id = row.ticket;
	const w = row.watch!;
	if (w.paused === "round_limit") {
		const more = rerun(row, (s) => `Run ${s} once more`, "One more fix run in a fresh session.");
		return item("paused", "max rounds reached", `${id}: tkt watch reached [auto] max_rounds fix runs since the plan's approval. Read the review, then say what to do.`, true, more);
	}
	const again = rerun(row, (s) => `Run ${s} again`, "The same step in a fresh session.");
	const kind = w.last.slice(0, Math.max(0, w.last.indexOf("@")));
	const step = kind.startsWith("run:") ? `ran ${kind.slice(4)}` : (watchSteps[kind] ?? "took a step");
	return item("paused", "no progress", `${id}: tkt watch ${step} on this head, and nothing changed. /show ${id} for what the agent said.`, true, again);
}

/** watchSteps words the steps tkt watch takes with no agent. */
const watchSteps: Record<string, string> = { "sync:": "tried to merge the base", "done:": "tried to remove it" };

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
			return item("blocked", "Status: BLOCKED", `${id}: the agent stopped with Status: ${row.last_status || "BLOCKED"}. /show ${id} for its reason.${after}`, byAuto, [reply]);
		case "review_awaits_approval":
			return item("check", "review draft waits", `${id}: review-pr waits for you to post or drop its draft, in its tab.${after}`, byAuto, [reply]);
		case "bot_silent":
			return item("paused", "review bot silent", `${id}: the review bot has not reviewed the head. Check its job in CI.`, byAuto, [review]);
		case "fix_blocked":
			return item("question", "the fix asks", `${id}: the session fixing it in a split asks a question. Answer it in pane ${row.fix?.pane ?? "its split"}.${after}`, byAuto);
		case "fix_stopped":
			return item("blocked", `fix: ${row.fix?.last_status || "stopped"}`, `${id}: the session fixing ${row.fix?.stage || "it"} in a split stopped with Status: ${row.fix?.last_status || "BLOCKED"}. Read why in pane ${row.fix?.pane ?? "its split"}.${after}`, byAuto);
		case "verify_incomplete":
			return item("paused", "verification incomplete", `${id}: verify-pr could not run every step, so the yolo run cannot merge it. /check ${id} shows the rest; run them, and merge it yourself.`, byAuto);
	}
	return item("check", "stopped, no status", `${id}: the agent stopped without a status line. /show ${id} for what it said.${after}`, byAuto, [reply]);
}

function planText(id: string): string {
	return `${id}: the plan is ready. /show ${id} to read it, /approve ${id} to go on, or say what to change.`;
}

function readyText(row: Row): string {
	const id = row.ticket;
	const pr = row.pr ? ` Pull request #${row.pr.number}` : " The pull request";
	switch (verifiedAt(row)?.verification) {
		case "PASSED":
			return `${id}:${pr} passed every human verification step in verify-pr. /check ${id} shows its evidence, and merges once you confirm it.`;
		case "INCOMPLETE":
			return `${id}: verify-pr could not run every human verification step. /check ${id} starts with the ones it passed, and merges once the rest pass.`;
	}
	if (row.next?.reason === "needs_verify") return `${id}:${pr} waits for verify-pr. /check ${id} starts it, or walks the steps yourself.`;
	return `${id}:${pr} is ready for human verification. /check ${id} walks the steps, and merges once they all pass.`;
}

/** unblockedOf offers to start a ticket a merge unblocked: finish-ticket
 * said its blockers are all done, and it has no worktree yet. */
export function unblockedOf(u: Unblocked): Attention {
	const text = `${u.ticket} is unblocked: ${u.by} merged, and its blockers are all done. /start ${u.ticket} starts it.`;
	return { key: `unblocked|${u.ticket}|${u.by}`, ticket: u.ticket, kind: "unblocked", title: "unblocked", text, byAuto: false };
}

/** sound picks the herdr notification sound for an item. */
export function sound(a: Attention): "done" | "request" {
	return a.kind === "ready" || a.kind === "merged" || a.kind === "unblocked" || a.kind === "chore" ? "done" : "request";
}
