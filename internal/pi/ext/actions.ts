// What the tools and the slash commands do. Slow work goes to the hub's
// jobs, so an action returns at once and says the work is queued; quick
// steps, such as typing into a session, wait for tkt and say what happened.

import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import { type Attention, choicesOf } from "./attention.ts";
import { type Hub, reasonOf } from "./hub.ts";
import { type ChoresResult, choresText, type Envelope, type MergeResult, mergeText, stepText, type SuggestResult, type UpdateResult, verifiedAt, type Verification, versionText } from "./model.ts";
import { fixPrompt, type HistoryResult, historyText, statsText, tally, type Mark, type StatsResult } from "./report.ts";
import { ask, checklist, peek, type Step } from "./ui.ts";

export type Mode = "manual" | "auto" | "auto_after_plan" | "yolo";

/** start starts tickets in the background: manual ones with tkt start,
 * the others with a tkt auto loop that starts the ticket itself. as picks
 * a preset from config.toml or the .tkt files, such as one that runs another model. */
export function start(hub: Hub, ids: string[], mode: Mode, as = ""): string {
	for (const id of ids) {
		if (mode === "manual") hub.add({ ticket: id, label: "start", args: ["start", id, ...(as ? ["--as", as] : [])] });
		else hub.startAuto(id, mode === "auto", as, mode === "yolo");
	}
	const list = ids.join(", ");
	const on = as ? ` on ${as}` : "";
	switch (mode) {
		case "auto":
			return `Queued: ${list} starting${on} in full auto. tkt approves the plan and drives each ticket until it is reviewed clean; the board shows when one needs you.`;
		case "auto_after_plan":
			return `Queued: ${list} starting${on} in auto. The board shows each plan when it is ready; after you approve it, tkt drives the ticket.`;
		case "yolo":
			return `Queued: ${list} starting${on} in yolo. tkt carries each ticket to the merge with nobody watching; the board shows when one needs you, and the plan and pull request list the assumptions its agents made.`;
	}
	return `Queued: ${list} starting${on}. The board shows each plan when it is ready.`;
}

/** suggest opens a suggest tab where suggest-tickets picks the tickets to
 * start: the ones request asks for, or a count, 3 by default. tkt reads
 * none of the answer: you read it in the tab, talk it over with the agent,
 * and start the tickets you want. */
export function suggest(hub: Hub, request: string, as = ""): string {
	const ask = request.trim();
	const count = /^\d+$/.test(ask) ? Number(ask) : ask ? 0 : 3;
	hub.add({
		ticket: "suggest",
		label: "suggest",
		args: ["suggest", ...(ask ? [ask] : []), ...(as ? ["--as", as] : [])],
		then: (env) => {
			if (!env.ok || !env.result) return;
			const r = env.result as SuggestResult;
			if (count === 0) return hub.post(`suggest-tickets has your request in the suggest tab (pane ${r.pane}). Read its picks there, and /start the ones you want.`);
			hub.post(`suggest-tickets is picking in the suggest tab (pane ${r.pane}). Read its picks there, and /start the ones you want.`);
		},
	});
	if (count === 0) return "Queued: suggest-tickets gets your request in a suggest tab, reading only. Its picks show in that tab, not here.";
	const n = `${count} ticket${count === 1 ? "" : "s"}`;
	return `Queued: suggest-tickets picks ${n} in a suggest tab, reading only. Its picks show in that tab, not here.`;
}

/** chores lists the repository's chores with their next run. */
export async function chores(hub: Hub): Promise<string> {
	const env = await hub.tkt<ChoresResult>(["chores"]);
	if (!env.ok || !env.result) return failure("chores", env);
	return choresText(env.result);
}

/** choreRun starts a run of a chore now, in the background: a branch, a
 * tab and an agent with the chore's prompt. */
export function choreRun(hub: Hub, name: string): string {
	hub.add({ ticket: `chore ${name}`, label: "chore", args: ["chore", "run", name] });
	return `Queued: a run of chore ${name} in its own tab. The board shows it once it starts.`;
}

/** approve sends the approval and waits for tkt's answer. With then auto,
 * a tkt auto loop drives the ticket from there; a loop that already waits
 * on the plan goes on by itself. */
export async function approve(hub: Hub, id: string, then: "manual" | "auto"): Promise<string> {
	if (hub.row(id)?.chore) return `${id} is a run of a chore, with no ticket and no plan to approve. /send ${id} TEXT tells its agent what to do.`;
	const env = await hub.run({ ticket: id, label: "approve", args: ["approve", id], quiet: true });
	if (!env.ok) return failure(id, env);
	const row = hub.row(id);
	if (then === "auto" && !row?.auto?.running) hub.startAuto(id, false);
	if (then === "auto" || row?.auto?.running) return `Approved ${id}'s plan; work-ticket builds it in the same session, and tkt auto drives it from there.`;
	return `Approved ${id}'s plan; work-ticket builds it in the same session.`;
}

/** send types text into the ticket's session. A free agent takes it as a
 * new turn, and a working one reads it at its next step. An agent that asks
 * a question in its own dialog gets it once the question is answered, so
 * the text waits in a queue; with hold, it waits until the agent stops. */
export async function send(hub: Hub, id: string, text: string, hold = false): Promise<string> {
	const r = hub.row(id);
	const busy = r?.agent && (r.agent.status === "working" || r.agent.status === "blocked" || (r.agent.background ?? 0) > 0);
	if (hold && busy) {
		hub.add({ ticket: id, label: "send queued", args: ["send", id, "--wait", "--prompt", text] });
		return `Queued, not sent yet: ${id}'s agent is busy, and the text goes in when it stops. The board shows if that fails.`;
	}
	const queue = () => {
		hub.add({ ticket: id, label: "send queued", args: ["send", id, "--steer", "--wait", "--prompt", text] });
		return `Queued, not sent yet: ${id}'s agent asks a question in its tab, and the text goes in once it is answered. The board shows if that fails.`;
	};
	if (r?.agent?.status === "blocked") return queue();
	const env = await hub.run({ ticket: id, label: "send", args: ["send", id, "--steer", "--prompt", text], quiet: true });
	// The board can be a poll behind an agent that just opened a question.
	if (!env.ok && env.error?.code === "agent_busy" && env.error.details?.status === "blocked") return queue();
	if (!env.ok) return failure(id, env);
	if ((env.result as { steered?: boolean } | undefined)?.steered) return `Sent to ${id}'s session while its agent works; it reads the text at its next step.`;
	return `Sent to ${id}'s session.`;
}

export function stage(hub: Hub, id: string, name: string, as = ""): string {
	hub.add({ ticket: id, label: name, args: ["run", id, name, ...(as ? ["--as", as] : [])] });
	return `Queued: ${name} for ${id} in a fresh session${as ? ` on ${as}` : ""}. The board shows when it needs you.`;
}

/** sync merges the base into the ticket's branch in the background: tkt
 * itself when the merge is clean, a fresh session on a conflict. */
export function sync(hub: Hub, id: string): string {
	hub.add({ ticket: id, label: "sync", args: ["sync", id] });
	return `Queued: tkt sync for ${id}. It merges the base itself when the merge is clean, and hands a conflict to a fresh session.`;
}

/** merge hands a ticket whose steps all passed on head to finish-ticket:
 * tkt merge sends it the merge line now, or leaves an order that tkt watch
 * carries out once the checks pass. */
export function merge(hub: Hub, id: string, steps: number[], head: string, evidence = false): string {
	const args = ["merge", id, "--passed", steps.join(","), ...(head ? ["--head", head] : []), ...(evidence ? ["--evidence"] : [])];
	hub.add({ ticket: id, label: "merge", args, then: (env) => (env.ok && env.result ? hub.post(mergeText(env.result as MergeResult)) : undefined) });
	return `Queued: tkt merge for ${id}. finish-ticket merges it now when the checks passed at its head; otherwise tkt watch hands it over once they do.`;
}

/** resume relaunches the ticket's newest session when no agent runs for it. */
export function resume(hub: Hub, id: string): string {
	hub.add({ ticket: id, label: "resume", args: ["resume", id] });
	return `Queued: tkt resume for ${id}, which relaunches its newest session. The board shows when it needs you.`;
}

export async function auto(hub: Hub, id: string, on: boolean, approvePlan: boolean, as = "", yolo = false): Promise<string> {
	// tkt auto refuses a run's id, and its loop runs detached, so the chat
	// says so here rather than after a silent failure.
	if (hub.row(id)?.chore) return `${id} is a run of a chore: tkt auto drives tickets only. tkt watch syncs its base and fixes its checks, and you merge its pull request.`;
	if (on && yolo) {
		const refused = await yoloRefusal(hub, [id]);
		if (refused) return refused;
		hub.startAuto(id, true, as, true);
		return `tkt auto now carries ${id} to the merge with nobody watching (yolo). The board shows when it needs you.`;
	}
	if (on) {
		hub.startAuto(id, approvePlan, as);
		return `tkt auto now drives ${id}${approvePlan ? ", approving its plan too" : ""}. The board shows when it needs you.`;
	}
	const env = await hub.run({ ticket: id, label: "auto off", args: ["auto", id, "--stop"], quiet: true });
	if (!env.ok) return failure(id, env);
	return `Stopped tkt auto for ${id}; its loop ends at its next look.`;
}

/** yoloRefusal says why tkt auto --yolo would refuse the first of ids, such
 * as a repository whose workflow.md does not turn on autonomous runs, or
 * nothing. Its loop runs detached, so the chat asks for one step's dry run
 * first rather than lose the refusal. */
export async function yoloRefusal(hub: Hub, ids: string[]): Promise<string | undefined> {
	const id = ids[0];
	if (!id) return undefined;
	const env = await hub.run({ ticket: id, label: "yolo check", args: ["auto", id, "--yolo", "--once", "--dry-run"], quiet: true });
	return env.ok || env.error?.code !== "autonomous_off" ? undefined : failure(id, env);
}

/** hold puts a ticket on hold, or takes it off, and waits for tkt's answer. */
export async function hold(hub: Hub, id: string, on: boolean): Promise<string> {
	const env = await hub.run({ ticket: id, label: on ? "hold" : "hold off", args: ["hold", id, ...(on ? [] : ["--off"])], quiet: true });
	if (!env.ok) return failure(id, env);
	if (!on) return `${id} is off hold; tkt watch and tkt auto can drive it again.`;
	return `${id} is on hold: tkt watch and tkt auto leave it alone, and the board shows no step to take for it. /hold ${id} off lifts it.`;
}

export async function done(hub: Hub, id: string): Promise<string> {
	const env = await hub.run({ ticket: id, label: "done", args: ["done", id], quiet: true });
	if (!env.ok) return failure(id, env);
	const t = (env.result as { tickets?: { outcome: string; elapsed_s: number; unblocked?: string[] }[] } | undefined)?.tickets?.[0];
	if (t?.outcome !== "removed") return `${id}: tkt done did not remove it (${t?.outcome ?? "no result"}).`;
	const took = t.elapsed_s > 0 ? ` It took ${duration(t.elapsed_s)} from start to merge.` : "";
	const freed = t.unblocked?.length ? ` The merge unblocked ${t.unblocked.join(", ")}: /inbox offers to start ${t.unblocked.length === 1 ? "it" : "them"}.` : "";
	return `${id} is done: its tabs are closed and its worktree is removed.${took}${freed}`;
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
	/** hold is what the status pages report while the step waits for
	 * their incident to end. */
	hold?: string;
	next: { text: string; reason: string };
}

/** next takes the ticket's next step, as tkt auto would: it asks tkt for
 * the step first, without taking it, so it can say what happens. */
export async function next(hub: Hub, id: string): Promise<string> {
	// A merge order is yours, so tkt auto never takes it; tkt merge does.
	const n = hub.row(id)?.next;
	// tkt auto drives tickets only, so a chore's run takes its step as tkt
	// ls names it, such as tkt sync or tkt run ci.
	if (hub.row(id)?.chore) {
		if (n?.kind !== "command" || !n.argv) return `${id} has no step to take now: ${n?.text ?? "tkt ls shows it"}.`;
		hub.add({ ticket: id, label: n.argv[1] === "run" ? n.argv[3] : n.argv[1], args: n.argv.slice(1) });
		return `Queued: ${n.text}.`;
	}
	if (n?.reason === "merge_due" && n.argv) {
		hub.add({ ticket: id, label: "merge", args: n.argv.slice(1), then: (env) => (env.ok && env.result ? hub.post(mergeText(env.result as MergeResult)) : undefined) });
		return `Queued: ${id}'s merge order. finish-ticket gets the merge line in a fresh session.`;
	}
	const dry = await hub.tkt<AutoOnce>(["auto", id, "--once", "--dry-run"]);
	if (!dry.ok) {
		if (dry.error?.code === "auto_running") return `tkt auto already drives ${id}; it goes on by itself.`;
		return failure(id, dry);
	}
	const r = dry.result as AutoOnce;
	if (r.did) {
		hub.add({ ticket: id, label: r.did.stage || r.did.action, args: ["auto", id, "--once"] });
		return `Queued: ${id} ${stepText(r.did)}.`;
	}
	if (r.wait === "incident") return `${id} waits: ${r.hold || "a status page reports an incident"}, and tkt auto sets no agent to work until it ends. tkt run ${id} STAGE starts one anyway.`;
	if (r.wait === "awaiting_bot") return `${id} waits for the review bot in CI; ${hub.row(id)?.next.text ?? "tkt ls shows it"}.`;
	if (r.wait) return `${id} has nothing to do yet: ${r.next.text}.`;
	switch (r.stop) {
		case "plan_awaits_approval":
			return `${id}'s plan waits for your approval: /show ${id}, then /approve ${id}.`;
		case "ready_for_human":
			return `${id} is ready for human verification: /check ${id}.`;
		case "merge_ordered":
			return `${id}'s merge order is due: tkt watch sends finish-ticket the merge line.`;
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

/** history reads what happened to tickets. bodies adds each review's
 * findings, for the model. */
export async function history(hub: Hub, ids: string[], bodies: boolean): Promise<string> {
	const env = await hub.tkt<HistoryResult>(["history", ...ids]);
	if (!env.ok || !env.result) return failure(ids.join(", "), env);
	return historyText(env.result, bodies);
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
	// Your steps can run on the code that will merge, or carry over the
	// clean sync a merge order gets.
	const behind = hub.row(id)?.behind ?? 0;
	if (behind > 0) {
		const n = `${behind} commit${behind === 1 ? "" : "s"}`;
		const a = await ask(ctx, `${id} is ${n} behind its base. Sync it first?`, [
			{ label: "Sync first", description: "tkt sync merges the base; the new head gets a review, then /check again." },
			{ label: "Verify this head", description: "Your steps carry over a clean sync: tkt watch syncs it after you merge, and has the new head reviewed." },
		], false);
		if (!a) return "Closed.";
		if (a.answer.startsWith("Sync")) return `${sync(hub, id)} /check ${id} again once the new head is reviewed.`;
	}
	// verify-pr's report, when it holds for the head, is evidence: you may
	// confirm the steps from it, and the checklist starts with the steps
	// it passed marked.
	const v = verifiedAt(hub.row(id));
	const first = await ask(ctx, `${id}: how do you want to verify?`, howOptions(v), false);
	if (!first) return "Closed.";
	if (first.answer.startsWith("Run verify-pr")) return stage(hub, id, "verify");
	const env = await hub.tkt<{ steps: Step[]; procedure: string; pr: number; source: string }>(["show", id, "--steps"]);
	if (!env.ok || !env.result) return failure(id, env);
	const steps = env.result.steps;
	if (first.answer.startsWith("Confirm") && v) return confirmEvidence(hub, ctx, id, steps, v, behind);
	const from = env.result.source === "pull_request" ? `The review bot cleared the head; these steps come from pull request #${env.result.pr}.` : "";
	const head = hub.row(id)?.pr?.head ?? "";
	const key = `${id}@${head}`;
	const got = await checklist(ctx, id, steps, marks.get(key) ?? evidenceMarks(steps, v), from);
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
	// finish-ticket merges only when every step passed, or you waived it
	// with a reason, which only you can give.
	if (t.skip > 0) return `${id}: ${t.pass} passed and ${t.skip} skipped. finish-ticket merges only when each step passed or you waived it with a reason: tell its agent in the tab which steps passed and why you skip the others.`;
	// tkt merge refuses a head that moved since the steps ran, which it can
	// check only against the head they ran on.
	if (!head) return `${id}: every step passed, but the board has no head for its pull request yet. /check ${id} again in a moment to merge it.`;
	const pending = hub.row(id)?.checks?.state === "pending";
	const go = await ask(ctx, `${id}: every step passed. Merge it?`, [
		behind > 0
			? { label: "Merge once synced", description: "tkt merge leaves an order: tkt watch merges the base, has the new head reviewed, and sends finish-ticket the merge line once the checks pass." }
			: pending
				? { label: "Merge once CI is green", description: "tkt merge leaves an order, and tkt watch sends finish-ticket the merge line once the checks pass." }
				: { label: "Merge now", description: "tkt merge sends finish-ticket the merge line in a fresh session in its tab, or leaves an order while another ticket merges." },
		{ label: "Not yet" },
	], false);
	if (!go?.answer.startsWith("Merge")) return `${id}: every step passed. /check ${id} merges it when you are ready.`;
	return merge(hub, id, steps.map((s) => s.n), head);
}

/** howOptions are /check's first answers: confirm from verify-pr's
 * evidence when its report passed every step, the checklist, and a run of
 * verify-pr. */
function howOptions(v: Verification | undefined): { label: string; description?: string }[] {
	const list = { label: "Open the checklist", description: v ? "The steps verify-pr passed start marked; mark the rest as you run them." : "Mark each human verification step as you run it." };
	const run = { label: "Run verify-pr first", description: "verify-pr runs the steps in a fresh session and posts what it saw; it changes no code. /check again when it is done." };
	if (v?.verification === "PASSED") return [{ label: "Confirm from verify-pr's evidence", description: "Read what it saw on each step, then merge on it." }, list, { ...run, label: "Run verify-pr again" }];
	return v ? [list, { ...run, label: "Run verify-pr again" }] : [list, run];
}

/** evidenceMarks marks the steps verify-pr passed. */
function evidenceMarks(steps: Step[], v: Verification | undefined): Mark[] {
	if (!v) return [];
	const passed = new Set(v.steps.filter((s) => s.result === "PASSED").map((s) => s.n));
	return steps.map((s) => (passed.has(s.n) ? "pass" : ""));
}

/** confirmEvidence shows what verify-pr saw on each step, and merges with
 * tkt merge --evidence once you confirm: finish-ticket records that you
 * confirmed the steps from it. */
async function confirmEvidence(hub: Hub, ctx: ExtensionContext, id: string, steps: Step[], v: Verification, behind: number): Promise<string> {
	const seen = steps.map((s) => {
		const r = v.steps.find((x) => x.n === s.n);
		return `${s.n}. ${r ? `${r.result}: ${r.text}` : "not in the report"}`;
	});
	const pending = hub.row(id)?.checks?.state === "pending";
	const go = await ask(ctx, `${id}: verify-pr on ${v.head.slice(0, 7)}${v.at_head ? "" : ", carried over clean merges of the base"}:\n${seen.join("\n")}\nConfirm the steps from this and merge?`, [
		behind > 0 || pending
			? { label: "Confirm and merge once ready", description: "tkt merge --evidence leaves an order, which tkt watch carries out once the base is synced and the checks pass." }
			: { label: "Confirm and merge now", description: "tkt merge --evidence sends finish-ticket the merge line, which says you confirmed the steps from verify-pr's evidence." },
		{ label: "Not yet" },
	], false);
	if (!go?.answer.startsWith("Confirm")) return `${id}: nothing confirmed. /check ${id} goes on when you are ready.`;
	return merge(hub, id, steps.map((s) => s.n), v.head, true);
}

/** inbox walks what needs you, each with fixed answers. It asks about each
 * ticket's kind of item once: right after you answer, the board can still
 * show the item under a new key, such as a plan you just approved. */
export async function inbox(hub: Hub, ctx: ExtensionContext): Promise<string> {
	await hub.refresh();
	const seen = new Set<string>();
	for (;;) {
		const a = hub.items.find((x) => !seen.has(`${x.ticket}|${x.kind}`));
		if (!a) break;
		seen.add(`${a.ticket}|${a.kind}`);
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
	const choices = choicesOf(a);
	const got = (await ask(ctx, a.text, [...choices, { label: "Later" }], false))?.answer;
	if (got === "Later") return "";
	const c = choices.find((x) => x.label === got);
	switch (c?.do) {
		case "approve":
		case "approve_auto":
			return approve(hub, id, c.do === "approve" ? "manual" : "auto");
		case "show":
			hub.post(await show(hub, id, "message"));
			return undefined;
		case "peek":
			await watchPeek(hub, ctx, id);
			return undefined;
		case "reply": {
			if (!ctx.hasUI) return undefined;
			const text = await ctx.ui.editor(a.kind === "plan" ? `What should change in ${id}'s plan?` : `What do you tell ${id}'s agent?`, "");
			if (!text?.trim()) return undefined;
			return send(hub, id, text.trim());
		}
		case "dismiss":
			hub.dismiss(a);
			return "";
		case "check":
			return check(hub, ctx, id);
		case "next":
			return next(hub, id);
		case "done":
			return done(hub, id);
		case "auto_on":
			return auto(hub, id, true, hub.row(id)?.auto?.approve_plan ?? false);
		case "auto_off":
			return auto(hub, id, false, false);
		case "run":
			return stage(hub, id, c.stage!);
		case "resume":
			return resume(hub, id);
		case "start":
			return start(hub, [id], "manual");
		case "start_auto":
			return start(hub, [id], "auto");
		case "start_after_plan":
			return start(hub, [id], "auto_after_plan");
	}
	return undefined;
}

/** version says which tkt and pi this chat runs, and whether the tkt it
 * calls is another one now. The chat runs the tkt that started it, so only
 * what tkt agent set then says which; the binary may have changed since. */
export async function version(hub: Hub): Promise<string> {
	const r = await hub.pi.exec(hub.bin, ["--version"], { cwd: hub.cwd });
	const installed = /^tkt version (\S+)/m.exec(r.stdout)?.[1] ?? "";
	return versionText(process.env.TKT_VERSION ?? "", process.env.TKT_PI_VERSION ?? "", installed, hub.bin);
}

/** update installs a newer tkt with tkt update, after you confirm. The
 * chat keeps running the old tkt's extension and pi, so it offers to quit;
 * pi then prints the command that reopens the chat on the new tkt. */
export async function update(hub: Hub, ctx: ExtensionContext): Promise<void> {
	const check = await hub.tkt<UpdateResult>(["update", "--check"]);
	if (!check.ok || !check.result) return hub.post(`Cannot check for a newer tkt: ${reasonOf(check)}`);
	const r = check.result;
	if (r.dev) return hub.post(`This tkt is a development build (${r.from}), so /update does not replace it. Rebuild it, or run ${r.next || "tkt update --force"} in a shell.`);
	if (!r.available) {
		hub.newer = undefined;
		hub.render();
		return hub.post(`tkt ${r.from} is up to date.`);
	}
	if (!(await ctx.ui.confirm(`Update tkt from ${r.from} to ${r.to}?`, "This chat runs the old tkt until you reopen it. tkt auto loops and tkt watch move to the new tkt by themselves."))) return;
	ctx.ui.setStatus("tkt-update", `tkt: downloading ${r.to}`);
	const env = await hub.tkt<UpdateResult>(["update", r.to]).finally(() => ctx.ui.setStatus("tkt-update", undefined));
	if (!env.ok) return hub.post(`tkt update failed: ${reasonOf(env)}`);
	hub.newer = undefined;
	hub.render();
	hub.post(`Updated tkt from ${r.from} to ${r.to}. This chat still runs ${r.from}: quit and reopen it to run ${r.to}.`);
	if (await ctx.ui.confirm("Quit now?", "pi prints the command that reopens this chat: tkt agent --session ID.")) ctx.shutdown();
}
