// What the tools and the slash commands do. Slow work goes to the hub's
// jobs, so each action returns a line at once.

import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Attention } from "./attention.ts";
import type { Hub } from "./hub.ts";
import type { Envelope } from "./model.ts";
import { fixPrompt, statsText, tally, type Mark, type StatsResult } from "./report.ts";
import { ask, checklist, peek, type Step } from "./ui.ts";

export type Mode = "manual" | "auto" | "auto_after_plan";

/** start starts tickets in the background: manual ones with tkt start,
 * the others with a tkt auto loop that starts the ticket itself. */
export function start(hub: Hub, ids: string[], mode: Mode): string {
	for (const id of ids) {
		if (mode === "manual") hub.add({ ticket: id, label: "start", args: ["start", id] });
		else hub.startAuto(id, mode === "auto");
	}
	const list = ids.join(", ");
	switch (mode) {
		case "auto":
			return `Starting ${list} in full auto. tkt approves the plan and drives each ticket to READY_FOR_HUMAN; I will tell you when one needs you.`;
		case "auto_after_plan":
			return `Starting ${list} in auto. I will tell you when a plan is ready; after you approve it, tkt drives the ticket to READY_FOR_HUMAN.`;
	}
	return `Starting ${list} in the background. I will tell you when a plan is ready.`;
}

/** approve sends the approval; with then "auto", a tkt auto loop takes over
 * once the approval is in. */
export function approve(hub: Hub, id: string, then: "manual" | "auto"): string {
	hub.add({
		ticket: id,
		label: "approve",
		args: ["approve", id],
		then: (env) => {
			if (env.ok && then === "auto") hub.startAuto(id, false);
		},
	});
	return then === "auto"
		? `Approving the plan for ${id}; tkt auto then drives it to READY_FOR_HUMAN.`
		: `Approving the plan for ${id}; work-ticket starts in the same session. I will tell you when it finishes.`;
}

export function send(hub: Hub, id: string, text: string): string {
	hub.add({ ticket: id, label: "send", args: ["send", id, "--prompt", text] });
	return `Sending your message to ${id}'s session.`;
}

export function stage(hub: Hub, id: string, name: string): string {
	hub.add({ ticket: id, label: name, args: ["run", id, name] });
	return `Running ${name} for ${id} in a fresh session.`;
}

export function auto(hub: Hub, id: string, on: boolean, approvePlan: boolean): string {
	if (on) {
		hub.startAuto(id, approvePlan);
		return `tkt auto now drives ${id}${approvePlan ? ", approving its plan too" : ""}. I will tell you when it needs you.`;
	}
	hub.add({ ticket: id, label: "auto off", args: ["auto", id, "--stop"] });
	return `Stopping tkt auto for ${id} at its next look.`;
}

export function done(hub: Hub, id: string): string {
	hub.add({
		ticket: id,
		label: "done",
		args: ["done", id],
		then: (env) => {
			const t = (env.result as { tickets?: { outcome: string; elapsed_s: number }[] } | undefined)?.tickets?.[0];
			if (env.ok && t?.outcome === "removed") {
				const took = t.elapsed_s > 0 ? ` It took ${duration(t.elapsed_s)} from start to merge.` : "";
				hub.post(`${id} is done: its tabs are closed and its worktree is removed.${took}`);
			}
		},
	});
	return `Removing ${id}'s tabs and worktree once tkt done confirms the merge.`;
}

function duration(s: number): string {
	if (s < 3600) return `${Math.max(1, Math.round(s / 60))} minutes`;
	if (s < 48 * 3600) return `${Math.round(s / 3600)} hours`;
	return `${Math.round(s / 86400)} days`;
}

interface AutoOnce {
	stop: string;
	did: { action: string; stage?: string } | null;
	wait: string;
	next: { text: string; reason: string };
}

/** next takes the ticket's next step, as tkt auto would: it asks tkt for
 * the step first, without taking it, so it can say what happens, and asks
 * you when the review bot may still post. */
export async function next(hub: Hub, ctx: ExtensionContext, id: string): Promise<string> {
	const dry = await hub.tkt<AutoOnce>(["auto", id, "--once", "--dry-run"]);
	if (!dry.ok) {
		if (dry.error?.code === "auto_running") return `tkt auto already drives ${id}; it goes on by itself.`;
		return failure(id, dry);
	}
	const r = dry.result as AutoOnce;
	if (r.did) {
		hub.add({ ticket: id, label: r.did.stage || r.did.action, args: ["auto", id, "--once"] });
		return `${id}: ${r.did.action === "approve" ? "approving the plan" : r.did.action === "start" ? "starting" : `running ${r.did.stage}`} in the background.`;
	}
	if (r.wait === "awaiting_bot") {
		const a = await ask(ctx, `${id} waits for the review bot. What now?`, [{ label: "Keep waiting for the bot" }, { label: "Run review-pr now" }], false);
		if (a?.answer === "Run review-pr now") return stage(hub, id, "review");
		return `${id} keeps waiting for the review bot; ${hub.row(id)?.next.text ?? "tkt ls shows it"}.`;
	}
	if (r.wait) return `${id} has nothing to do yet: ${r.next.text}.`;
	switch (r.stop) {
		case "plan_awaits_approval":
			return `${id}'s plan waits for your approval: /show ${id}, then /approve ${id}.`;
		case "ready_for_human":
			return `${id} is ready for human verification: /check ${id}.`;
		case "merged":
			return `${id} is merged: /done ${id} removes it.`;
		case "needs_you":
			return `${id}'s agent needs you: /peek ${id}, then answer in its tab.`;
	}
	return `${id} stops here (${r.stop}): ${r.next.text}.`;
}

/** show returns a view of the ticket as text, or the failure. */
export async function show(hub: Hub, id: string, view: "message" | "screen" | "steps"): Promise<string> {
	const args = ["show", id];
	if (view === "screen") args.push("--screen");
	if (view === "steps") args.push("--steps");
	const env = await hub.tkt<{ message: string; screen: string; procedure: string; last_status: string; source: string }>(args);
	if (!env.ok || !env.result) return failure(id, env);
	const r = env.result;
	if (view === "screen") return r.screen.trimEnd();
	if (view === "steps") return r.procedure;
	return r.message || `${id}'s agent has said nothing since the last prompt.`;
}

export async function stats(hub: Hub, ids: string[], sinceDays?: number): Promise<string> {
	const args = ["stats", ...ids];
	if (sinceDays && sinceDays > 0) args.push("--since", `${Math.round(sinceDays * 24)}h`);
	const env = await hub.tkt<StatsResult>(args);
	if (!env.ok || !env.result) return failure("stats", env);
	return statsText(env.result);
}

/** failure says why tkt refused, once: tkt's messages often name the
 * ticket and the command to run already. */
export function failure(id: string, env: Envelope): string {
	const e = env.error;
	if (!e) return `${id}: tkt failed.`;
	const msg = e.message.includes(id) ? e.message : `${id}: ${e.message}`;
	const hint = e.hint && !e.message.includes(e.hint) ? ` Next: ${e.hint}.` : "";
	return `${msg.replace(/[.\s]+$/, "")}.${hint}`;
}

/** watchPeek opens the live screen. */
export function watchPeek(hub: Hub, ctx: ExtensionContext, id: string): Promise<void> {
	return peek(ctx, id, () => show(hub, id, "screen"));
}

/** marks keeps each ticket's checklist marks for its head, in this chat. */
const marks = new Map<string, Mark[]>();

export function restoreMarks(entries: { type: string; customType?: string; data?: unknown }[]): void {
	for (const e of entries) {
		if (e.type !== "custom" || e.customType !== "tkt-check") continue;
		const d = e.data as { key: string; marks: Mark[] } | undefined;
		if (d?.key) marks.set(d.key, d.marks);
	}
}

/** check walks the human verification steps: verify first if you want,
 * then the checklist, then a fix session for a failed step. */
export async function check(hub: Hub, ctx: ExtensionContext, id: string): Promise<string> {
	const first = await ask(ctx, `${id}: how do you want to verify?`, [
		{ label: "Open the checklist", description: "Mark each human verification step as you run it." },
		{ label: "Run tkt run verify first", description: "An agent runs the steps in a browser and reports; it changes nothing. /check again when it is done." },
	], false);
	if (!first) return "Closed.";
	if (first.answer.startsWith("Run")) return stage(hub, id, "verify");
	const env = await hub.tkt<{ steps: Step[]; procedure: string; pr: number }>(["show", id, "--steps"]);
	if (!env.ok || !env.result) return failure(id, env);
	const steps = env.result.steps;
	const key = `${id}@${hub.row(id)?.pr?.head ?? ""}`;
	const got = await checklist(ctx, id, steps, marks.get(key) ?? []);
	marks.set(key, got);
	hub.pi.appendEntry("tkt-check", { key, marks: got });
	const t = tally(got, steps.length);
	if (t.fail > 0) {
		const i = got.indexOf("fail");
		const a = await ask(ctx, `Step ${steps[i].n} failed. What now?`, [{ label: "Start a fix session with what I saw" }, { label: "I will handle it" }], false);
		if (a?.answer.startsWith("Start") && ctx.hasUI) {
			const saw = await ctx.ui.input(`What did you see in step ${steps[i].n}?`, "the error, the page, the value");
			if (saw?.trim()) {
				hub.add({ ticket: id, label: "fix", args: ["run", id, "--prompt", fixPrompt(steps[i].n, steps[i].text, saw)] });
				return `Starting a fix session for ${id}'s step ${steps[i].n}. /next ${id} reviews it after.`;
			}
		}
		return `${id}: ${t.fail} step${t.fail === 1 ? "" : "s"} failed.`;
	}
	if (t.open > 0) return `${id}: ${t.pass} passed, ${t.skip} skipped, ${t.open} not run yet. /check ${id} goes on.`;
	return `${id}: every step passed${t.skip ? ` (${t.skip} skipped)` : ""}. Merge it, then say "${id} is merged, remove it".`;
}

/** inbox walks what needs you, oldest first, each with fixed answers. */
export async function inbox(hub: Hub, ctx: ExtensionContext): Promise<string> {
	let handled = 0;
	while (hub.inbox.length > 0) {
		const a = hub.inbox[0];
		const reply = await answer(hub, ctx, a);
		if (reply === undefined) break;
		handled++;
		if (reply) hub.post(reply);
	}
	if (hub.inbox.length === 0) return handled ? "The inbox is empty." : "Nothing needs you.";
	return `${hub.inbox.length} item${hub.inbox.length === 1 ? "" : "s"} still need you. /inbox to go on.`;
}

/** answer asks about one item. It returns the line to post, "" when the
 * item is done with nothing to say, or undefined to stop the walk. */
async function answer(hub: Hub, ctx: ExtensionContext, a: Attention): Promise<string | undefined> {
	const id = a.ticket;
	const later = { label: "Later" };
	const pick = async (options: { label: string; description?: string }[]) => (await ask(ctx, a.text, [...options, later], false))?.answer;
	let choice: string | undefined;
	switch (a.kind) {
		case "plan":
			choice = await pick([{ label: "Approve" }, { label: "Approve and go auto", description: "tkt auto then drives it to READY_FOR_HUMAN." }, { label: "Show the plan" }, { label: "Ask for changes" }]);
			if (choice === "Approve" || choice === "Approve and go auto") {
				hub.dismiss(a);
				return approve(hub, id, choice === "Approve" ? "manual" : "auto");
			}
			if (choice === "Show the plan") {
				hub.post(await show(hub, id, "message"));
				return undefined;
			}
			if (choice === "Ask for changes" && ctx.hasUI) {
				const text = await ctx.ui.editor(`What should change in ${id}'s plan?`, "");
				if (!text?.trim()) return undefined;
				hub.dismiss(a);
				return send(hub, id, text.trim());
			}
			return undefined;
		case "question":
			choice = await pick([{ label: "Peek at the agent's screen" }, { label: "Show its last message" }, { label: "Dismiss" }]);
			if (choice?.startsWith("Peek")) await watchPeek(hub, ctx, id);
			else if (choice?.startsWith("Show")) hub.post(await show(hub, id, "message"));
			else if (choice === "Dismiss") {
				hub.dismiss(a);
				return "";
			}
			return undefined;
		case "ready":
			choice = await pick([{ label: "Open the checklist" }, { label: "Dismiss" }]);
			if (choice === "Open the checklist") {
				hub.dismiss(a);
				return check(hub, ctx, id);
			}
			if (choice === "Dismiss") {
				hub.dismiss(a);
				return "";
			}
			return undefined;
		case "next":
			choice = await pick([{ label: "Continue" }, { label: "Show its last message" }]);
			if (choice === "Continue") {
				hub.dismiss(a);
				return next(hub, ctx, id);
			}
			if (choice?.startsWith("Show")) hub.post(await show(hub, id, "message"));
			return undefined;
		case "merged":
			choice = await pick([{ label: "Remove it (tkt done)" }]);
			if (choice?.startsWith("Remove")) {
				hub.dismiss(a);
				return done(hub, id);
			}
			return undefined;
		case "auto_stop":
		case "resume_auto":
			choice = await pick([{ label: "Resume tkt auto" }, { label: "Show its last message" }, { label: "Dismiss" }]);
			if (choice === "Resume tkt auto") {
				hub.dismiss(a);
				return auto(hub, id, true, hub.row(id)?.auto?.approve_plan ?? false);
			}
			if (choice?.startsWith("Show")) hub.post(await show(hub, id, "message"));
			else if (choice === "Dismiss") {
				hub.dismiss(a);
				return "";
			}
			return undefined;
		case "failed":
			choice = await pick([{ label: "Dismiss" }]);
			if (choice === "Dismiss") {
				hub.dismiss(a);
				return "";
			}
			return undefined;
	}
	return undefined;
}
