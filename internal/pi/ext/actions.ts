// What the tools and the slash commands do. Slow work goes to the hub's
// jobs, so an action returns at once and says the work is queued; quick
// steps, such as typing into a session, wait for tkt and say what happened.

import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import { type Attention, choicesOf } from "./attention.ts";
import { type Hub, reasonOf } from "./hub.ts";
import { type ChoresResult, choresText, type Envelope, type MergeResult, mergeText, picksText, stepText, type SuggestResult, type UpdateResult } from "./model.ts";
import { fixPrompt, type HistoryResult, historyText, statsText, tally, type Mark, type StatsResult } from "./report.ts";
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

/** suggest has suggest-tickets pick the next tickets to start, in the
 * background and reading only. Its picks come back as an item on the
 * board, which /inbox starts, each on the preset its model needs. */
export function suggest(hub: Hub, count: number, as = ""): string {
	hub.add({
		ticket: "suggest",
		label: "suggest",
		args: ["suggest", String(count), "--wait", ...(as ? ["--as", as] : [])],
		then: (env) => {
			if (!env.ok || !env.result) return;
			const r = env.result as SuggestResult;
			if (r.status === "blocked") return hub.post(`suggest-tickets asks a question in its tab (pane ${r.pane}); answer it there, then /suggest again.`);
			if (r.last_status !== "TICKETS_SUGGESTED") return hub.post(`suggest-tickets stopped with Status: ${r.last_status || "none"}; its tab (pane ${r.pane}) says why${r.last_status === "SETUP_REQUIRED" ? ", such as /setup-rogs to run first" : ""}.`);
			if (r.picks.length === 0) return hub.post("suggest-tickets found nothing to start now: every ready ticket is blocked, or collides with work in flight.");
			hub.suggested(r);
			hub.post(`suggest-tickets picked ${picksText(r)}. /inbox starts them, each on the preset its model needs.`);
		},
	});
	const n = `${count} ticket${count === 1 ? "" : "s"}`;
	return `Queued: suggest-tickets picks ${n} in a suggest tab, reading only. The board shows the picks when it is done.`;
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

/** startPicks starts the tickets suggest-tickets picked, each on the preset
 * its model needs, and takes the picks off the board. */
export function startPicks(hub: Hub, mode: Mode): string {
	const r = hub.picks?.result;
	if (!r) return "There are no picks to start; /suggest asks suggest-tickets again.";
	for (const p of r.picks) start(hub, [p.ticket], mode, p.preset);
	hub.suggested(undefined);
	const how = mode === "auto" ? " in full auto" : mode === "auto_after_plan" ? " in auto after the plan" : "";
	return `Queued: ${picksText(r)} starting${how}, each on the preset its model needs. The board shows each plan when it is ready.`;
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
export function merge(hub: Hub, id: string, steps: number[], head: string): string {
	const args = ["merge", id, "--passed", steps.join(","), ...(head ? ["--head", head] : [])];
	hub.add({ ticket: id, label: "merge", args, then: (env) => (env.ok && env.result ? hub.post(mergeText(env.result as MergeResult)) : undefined) });
	return `Queued: tkt merge for ${id}. finish-ticket merges it now when the checks passed at its head; otherwise tkt watch hands it over once they do.`;
}

/** resume relaunches the ticket's newest session when no agent runs for it. */
export function resume(hub: Hub, id: string): string {
	hub.add({ ticket: id, label: "resume", args: ["resume", id] });
	return `Queued: tkt resume for ${id}, which relaunches its newest session. The board shows when it needs you.`;
}

export async function auto(hub: Hub, id: string, on: boolean, approvePlan: boolean, as = ""): Promise<string> {
	// tkt auto refuses a run's id, and its loop runs detached, so the chat
	// says so here rather than after a silent failure.
	if (hub.row(id)?.chore) return `${id} is a run of a chore: tkt auto drives tickets only. tkt watch syncs its base and fixes its checks, and you merge its pull request.`;
	if (on) {
		hub.startAuto(id, approvePlan, as);
		return `tkt auto now drives ${id}${approvePlan ? ", approving its plan too" : ""}. The board shows when it needs you.`;
	}
	const env = await hub.run({ ticket: id, label: "auto off", args: ["auto", id, "--stop"], quiet: true });
	if (!env.ok) return failure(id, env);
	return `Stopped tkt auto for ${id}; its loop ends at its next look.`;
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
	const head = hub.row(id)?.pr?.head ?? "";
	const key = `${id}@${head}`;
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
		case "start_picks":
			return startPicks(hub, "manual");
		case "start_picks_auto":
			return startPicks(hub, "auto");
		case "start_picks_after_plan":
			return startPicks(hub, "auto_after_plan");
	}
	return undefined;
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
	if (!(await ctx.ui.confirm(`Update tkt from ${r.from} to ${r.to}?`, "This chat runs the old tkt until you reopen it. Tickets in tkt auto finish their loops on the old tkt."))) return;
	ctx.ui.setStatus("tkt-update", `tkt: downloading ${r.to}`);
	const env = await hub.tkt<UpdateResult>(["update", r.to]).finally(() => ctx.ui.setStatus("tkt-update", undefined));
	if (!env.ok) return hub.post(`tkt update failed: ${reasonOf(env)}`);
	hub.newer = undefined;
	hub.render();
	hub.post(`Updated tkt from ${r.from} to ${r.to}. This chat still runs ${r.from}: quit and reopen it to run ${r.to}.`);
	if (await ctx.ui.confirm("Quit now?", "pi prints the command that reopens this chat: tkt agent --session ID.")) ctx.shutdown();
}
