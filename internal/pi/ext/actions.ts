// What the tools and the slash commands do. Slow work goes to the hub's
// jobs, so an action returns at once and says the work is queued; quick
// steps, such as typing into a session, wait for tkt and say what happened.

import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Attention } from "./attention.ts";
import { type Hub, reasonOf } from "./hub.ts";
import type { Envelope } from "./model.ts";
import { fixPrompt, statsText, tally, type Mark, type StatsResult } from "./report.ts";
import { ask, checklist, peek, type Step } from "./ui.ts";

export type Mode = "manual" | "auto" | "auto_after_plan";

/** start starts tickets in the background: manual ones with tkt start,
 * the others with a tkt auto loop that starts the ticket itself. as picks
 * a preset from config.toml, such as one that runs another model. */
export function start(hub: Hub, ids: string[], mode: Mode, as = ""): string {
	for (const id of ids) {
		if (mode === "manual") hub.add({ ticket: id, label: "start", args: ["start", id, ...(as ? ["--as", as] : [])] });
		else hub.startAuto(id, mode === "auto", as);
	}
	const list = ids.join(", ");
	const on = as ? ` on ${as}` : "";
	switch (mode) {
		case "auto":
			return `Queued: ${list} starting${on} in full auto. tkt approves the plan and drives each ticket until it is reviewed clean; the board shows when one needs you.`;
		case "auto_after_plan":
			return `Queued: ${list} starting${on} in auto. The board shows each plan when it is ready; after you approve it, tkt drives the ticket.`;
	}
	return `Queued: ${list} starting${on}. The board shows each plan when it is ready.`;
}

/** approve sends the approval and waits for tkt's answer. With then auto,
 * a tkt auto loop drives the ticket from there; a loop that already waits
 * on the plan goes on by itself. */
export async function approve(hub: Hub, id: string, then: "manual" | "auto"): Promise<string> {
	const env = await hub.run({ ticket: id, label: "approve", args: ["approve", id], quiet: true });
	if (!env.ok) return failure(id, env);
	const row = hub.row(id);
	if (then === "auto" && !row?.auto?.running) hub.startAuto(id, false);
	if (then === "auto" || row?.auto?.running) return `Approved ${id}'s plan; work-ticket builds it in the same session, and tkt auto drives it from there.`;
	return `Approved ${id}'s plan; work-ticket builds it in the same session.`;
}

/** send types text into the ticket's session. An agent that is free takes
 * it now; a busy one gets it when it stops, so the text waits in a queue. */
export async function send(hub: Hub, id: string, text: string): Promise<string> {
	const r = hub.row(id);
	const busy = r?.agent && (r.agent.status === "working" || r.agent.status === "blocked" || (r.agent.background ?? 0) > 0);
	if (busy) {
		hub.add({ ticket: id, label: "send queued", args: ["send", id, "--wait", "--prompt", text] });
		return `Queued, not sent yet: ${id}'s agent is busy, and the text goes in when it stops. The board shows if that fails.`;
	}
	const env = await hub.run({ ticket: id, label: "send", args: ["send", id, "--prompt", text], quiet: true });
	if (!env.ok) return failure(id, env);
	return `Sent to ${id}'s session.`;
}

export function stage(hub: Hub, id: string, name: string, as = ""): string {
	hub.add({ ticket: id, label: name, args: ["run", id, name, ...(as ? ["--as", as] : [])] });
	return `Queued: ${name} for ${id} in a fresh session${as ? ` on ${as}` : ""}. The board shows when it needs you.`;
}

export async function auto(hub: Hub, id: string, on: boolean, approvePlan: boolean, as = ""): Promise<string> {
	if (on) {
		hub.startAuto(id, approvePlan, as);
		return `tkt auto now drives ${id}${approvePlan ? ", approving its plan too" : ""}. The board shows when it needs you.`;
	}
	const env = await hub.run({ ticket: id, label: "auto off", args: ["auto", id, "--stop"], quiet: true });
	if (!env.ok) return failure(id, env);
	return `Stopped tkt auto for ${id}; its loop ends at its next look.`;
}

export async function done(hub: Hub, id: string): Promise<string> {
	const env = await hub.run({ ticket: id, label: "done", args: ["done", id], quiet: true });
	if (!env.ok) return failure(id, env);
	const t = (env.result as { tickets?: { outcome: string; elapsed_s: number }[] } | undefined)?.tickets?.[0];
	if (t?.outcome !== "removed") return `${id}: tkt done did not remove it (${t?.outcome ?? "no result"}).`;
	const took = t.elapsed_s > 0 ? ` It took ${duration(t.elapsed_s)} from start to merge.` : "";
	return `${id} is done: its tabs are closed and its worktree is removed.${took}`;
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
 * the step first, without taking it, so it can say what happens. */
export async function next(hub: Hub, id: string): Promise<string> {
	const dry = await hub.tkt<AutoOnce>(["auto", id, "--once", "--dry-run"]);
	if (!dry.ok) {
		if (dry.error?.code === "auto_running") return `tkt auto already drives ${id}; it goes on by itself.`;
		return failure(id, dry);
	}
	const r = dry.result as AutoOnce;
	if (r.did) {
		hub.add({ ticket: id, label: r.did.stage || r.did.action, args: ["auto", id, "--once"] });
		return `Queued: ${id} ${r.did.action === "approve" ? "approving the plan" : r.did.action === "start" ? "starting" : `running ${r.did.stage}`}.`;
	}
	if (r.wait === "awaiting_bot") return `${id} waits for the review bot in CI; ${hub.row(id)?.next.text ?? "tkt ls shows it"}.`;
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
		case "bot_silent":
			return `${id}: the review bot has not reviewed the head. Check its job in CI.`;
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

/** failure says why tkt refused, with its reasons, once: tkt's messages
 * often name the ticket and the command to run already. */
export function failure(id: string, env: Envelope): string {
	const text = reasonOf(env);
	return text.includes(id) ? text : `${id}: ${text}`;
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
	const env = await hub.tkt<{ steps: Step[]; procedure: string; pr: number; source: string }>(["show", id, "--steps"]);
	if (!env.ok || !env.result) return failure(id, env);
	const steps = env.result.steps;
	const from = env.result.source === "pull_request" ? `The review bot cleared the head; these steps come from pull request #${env.result.pr}.` : "";
	const key = `${id}@${hub.row(id)?.pr?.head ?? ""}`;
	const got = await checklist(ctx, id, steps, marks.get(key) ?? [], from);
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
				return `Queued: a fix session for ${id}'s step ${steps[i].n}.`;
			}
		}
		return `${id}: ${t.fail} step${t.fail === 1 ? "" : "s"} failed.`;
	}
	if (t.open > 0) return `${id}: ${t.pass} passed, ${t.skip} skipped, ${t.open} not run yet. /check ${id} goes on.`;
	return `${id}: every step passed${t.skip ? ` (${t.skip} skipped)` : ""}. Merge it, then say "${id} is merged, remove it".`;
}

/** inbox walks what needs you, each with fixed answers. */
export async function inbox(hub: Hub, ctx: ExtensionContext): Promise<string> {
	await hub.refresh();
	const seen = new Set<string>();
	for (;;) {
		const a = hub.items.find((x) => !seen.has(x.key));
		if (!a) break;
		seen.add(a.key);
		const reply = await answer(hub, ctx, a);
		if (reply === undefined) break;
		if (reply) hub.post(reply);
	}
	const n = hub.items.length;
	return n === 0 ? "Nothing needs you." : `${n} item${n === 1 ? "" : "s"} still ${n === 1 ? "needs" : "need"} you; the board lists ${n === 1 ? "it" : "them"}.`;
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
			choice = await pick([{ label: "Approve" }, { label: "Approve and go auto", description: "tkt auto then drives it until it is reviewed clean." }, { label: "Show the plan" }, { label: "Ask for changes" }]);
			if (choice === "Approve" || choice === "Approve and go auto") return approve(hub, id, choice === "Approve" ? "manual" : "auto");
			if (choice === "Show the plan") {
				hub.post(await show(hub, id, "message"));
				return undefined;
			}
			if (choice === "Ask for changes" && ctx.hasUI) {
				const text = await ctx.ui.editor(`What should change in ${id}'s plan?`, "");
				if (!text?.trim()) return undefined;
				return send(hub, id, text.trim());
			}
			return choice === "Later" ? "" : undefined;
		case "question":
		case "check":
		case "blocked":
		case "paused":
			choice = await pick([{ label: "Peek at the agent's screen" }, { label: "Show its last message" }, { label: "Dismiss" }]);
			if (choice?.startsWith("Peek")) await watchPeek(hub, ctx, id);
			else if (choice?.startsWith("Show")) hub.post(await show(hub, id, "message"));
			else if (choice === "Dismiss") {
				hub.dismiss(a);
				return "";
			}
			return choice === "Later" ? "" : undefined;
		case "ready":
			choice = await pick([{ label: "Open the checklist" }, { label: "Dismiss" }]);
			if (choice === "Open the checklist") return check(hub, ctx, id);
			if (choice === "Dismiss") {
				hub.dismiss(a);
				return "";
			}
			return choice === "Later" ? "" : undefined;
		case "next":
			choice = await pick([{ label: "Continue" }, { label: "Show its last message" }]);
			if (choice === "Continue") return next(hub, id);
			if (choice?.startsWith("Show")) hub.post(await show(hub, id, "message"));
			return choice === "Later" ? "" : undefined;
		case "merged":
			choice = await pick([{ label: "Remove it (tkt done)" }]);
			if (choice?.startsWith("Remove")) return done(hub, id);
			return choice === "Later" ? "" : undefined;
		case "stalled":
			choice = await pick([{ label: "Resume tkt auto" }, { label: "Dismiss" }]);
			if (choice === "Resume tkt auto") return auto(hub, id, true, hub.row(id)?.auto?.approve_plan ?? false);
			if (choice === "Dismiss") {
				hub.dismiss(a);
				return "";
			}
			return choice === "Later" ? "" : undefined;
		case "failed":
			choice = await pick([{ label: "Dismiss" }]);
			if (choice === "Dismiss") {
				hub.dismiss(a);
				return "";
			}
			return choice === "Later" ? "" : undefined;
	}
	return undefined;
}
