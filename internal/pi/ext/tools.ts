// The tools the model calls. Each one runs tkt and nothing else. The
// guards live here, in code, not in the system prompt: a tool that changes
// a ticket your last message did not name asks you first, and removing a
// ticket always asks.

import { StringEnum } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import * as act from "./actions.ts";
import { phaseLabel, statusText } from "./board.ts";
import type { Hub } from "./hub.ts";
import { expandIds, mentions, normalizeId } from "./model.ts";
import { ask } from "./ui.ts";

const text = (t: string) => ({ content: [{ type: "text" as const, text: t }], details: undefined });

/** guard lets a change through when your last message names every ticket
 * it touches, or when you confirm it. The question names the tickets; the
 * line under it says what happens. */
async function guard(hub: Hub, ctx: ExtensionContext, ids: string[], question: string, what: string): Promise<boolean> {
	if (ids.length > 0 && ids.every((i) => mentions(hub.lastInput, i))) return true;
	if (!ctx.hasUI) return false;
	return ctx.ui.confirm(question, what);
}

/** list names tickets for a sentence: ACM-1, ACM-2 and ACM-3. */
function list(ids: string[]): string {
	return ids.length < 2 ? ids.join("") : `${ids.slice(0, -1).join(", ")} and ${ids[ids.length - 1]}`;
}

const preset = Type.Optional(Type.String({ description: "A preset from the user's config.toml, such as sol or claude, when the user names one ('as sol', 'with sol', '--as sol'); otherwise leave it out" }));

export function registerTools(pi: ExtensionAPI, hub: Hub): void {
	const id = (raw: string) => normalizeId(raw, hub.key);
	const ticket = Type.String({ description: "A ticket id, such as ACM-123" });

	pi.registerTool({
		name: "tkt_status",
		label: "tkt status",
		description: "Read the board: every ticket's phase, agent, pull request and auto state, and what needs the user. Pass ticket for one ticket's details.",
		parameters: Type.Object({ ticket: Type.Optional(ticket) }),
		executionMode: "sequential",
		async execute(_id, p) {
			await hub.refresh();
			if (p.ticket) {
				const r = hub.row(id(p.ticket));
				if (!r) return text(`${id(p.ticket)} has no worktree and no tkt auto loop waits to start it. Known tickets: ${hub.rows.map((x) => x.ticket).join(", ") || "none"}.`);
				const a = r.auto;
				const auto = !a ? "off"
					: a.running && a.waiting === "slot" ? "on, queued for a free slot"
					: a.running && a.paused ? `on, paused for the user (${a.paused}${a.why ? `, ${a.why}` : ""}); it goes on by itself when the ticket moves`
					: a.running ? `on${a.approve_plan ? ", approving plans" : ""}`
					: a.wanted ? "stalled: marked for auto, but no loop runs"
					: a.stop ? `off, last loop ended: ${a.stop}` : "off";
				const agent = r.agent ? `${(r.agent.background ?? 0) > 0 ? "idle, waiting on its own background task" : r.agent.status} in pane ${r.agent.pane_id}` : "none";
				const item = hub.items.find((x) => x.ticket === r.ticket);
				const lines = [
					`${r.ticket}: ${phaseLabel(r.phase)}${r.phase_since ? ` since ${r.phase_since}` : ""}, stage ${r.stage || "-"}${r.hold ? "; on hold, so tkt watch and tkt auto leave it alone" : ""}`,
					`agent: ${agent}; last status: ${r.last_status || "none"}`,
					`pull request: ${r.pr ? `#${r.pr.number} ${r.pr.state}` : "none"}; checks: ${r.checks?.state ?? "-"}; review: ${r.review ? `${r.review.verdict}${r.review.at_head ? "" : " (old head)"}` : "-"}`,
					`auto: ${auto}; rounds ${a?.rounds ?? 0}`,
					`tkt watch: ${hub.watching === "off" ? "not running" : hub.watching === "failing" ? "running, but its passes fail" : r.watch?.paused ? `waits for the user (${r.watch.paused})` : r.hold ? "leaves it alone, on hold" : "keeps it moving"}`,
					`next: ${r.next.text} (${r.next.reason})`,
					item ? `needs the user: ${item.text}` : "needs the user: nothing",
				];
				return text(lines.join("\n"));
			}
			return text(statusText(hub.state()));
		},
	});

	pi.registerTool({
		name: "tkt_show",
		label: "tkt show",
		description: "Read a ticket's agent: its last message (view message, such as the plan), its live screen (view screen, such as a question it asks), or the human verification steps (view steps).",
		parameters: Type.Object({ ticket, view: StringEnum(["message", "screen", "steps"] as const) }),
		executionMode: "sequential",
		async execute(_id, p) {
			const out = await act.show(hub, id(p.ticket), p.view);
			return text(out.length > 20000 ? `${out.slice(0, 20000)}\n[cut at 20000 characters; tkt show ${id(p.ticket)} has the rest]` : out);
		},
	});

	pi.registerTool({
		name: "tkt_start",
		label: "tkt start",
		description:
			"Queue tickets to start in the background; it returns before they start. mode manual stops at the plan; auto_after_plan also stops at the plan, then tkt auto takes over once the user approves; auto approves the plan too and drives each ticket until it is reviewed clean. Expand ranges such as ACM-12 to ACM-15 into every id. as picks the preset the agents run on.",
		parameters: Type.Object({
			tickets: Type.Array(ticket, { minItems: 1 }),
			mode: StringEnum(["manual", "auto", "auto_after_plan"] as const),
			as: preset,
		}),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const ids = [...new Set(p.tickets.flatMap((t) => {
				const many = expandIds(t, hub.key);
				return many.length > 0 ? many : [id(t)];
			}))];
			if (ids.length === 0) return text("Name the tickets to start, such as ACM-123.");
			const how = p.mode === "auto" ? " in full auto" : p.mode === "auto_after_plan" ? " in auto after the plan" : "";
			const on = p.as ? ` on ${p.as}` : "";
			if (!(await guard(hub, ctx, ids, `Start ${list(ids)}${how}${on}?`, p.mode === "auto" ? "tkt auto starts each ticket, approves its plan, and drives it until it is reviewed clean." : "Each ticket starts in its own tab and stops at its plan.")))
				return text("The user did not confirm; nothing started.");
			return text(act.start(hub, ids, p.mode, p.as ?? ""));
		},
	});

	pi.registerTool({
		name: "tkt_suggest",
		label: "tkt suggest",
		description:
			"Queue suggest-tickets, which reads the tracker and the work in flight, then picks the next tickets to start, ones that can run side by side. It returns before the picks; the board shows them, and /inbox starts them. count is how many to pick, 3 unless the user names a number. as picks the preset suggest-tickets runs on.",
		parameters: Type.Object({
			count: Type.Optional(Type.Integer({ minimum: 1, maximum: 10, description: "How many tickets to pick; 3 unless the user names a number" })),
			as: preset,
		}),
		executionMode: "sequential",
		async execute(_id, p) {
			return text(act.suggest(hub, p.count ?? 3, p.as ?? ""));
		},
	});

	pi.registerTool({
		name: "tkt_approve",
		label: "tkt approve",
		description: "Approve a ticket's plan in its own session, so work-ticket begins. then auto hands the ticket to tkt auto after the approval.",
		parameters: Type.Object({ ticket, then: StringEnum(["manual", "auto"] as const) }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], `Approve ${t}'s plan?`, "work-ticket then builds it in the same session."))) return text("The user did not confirm; nothing was approved.");
			return text(await act.approve(hub, t, p.then));
		},
	});

	pi.registerTool({
		name: "tkt_send",
		label: "tkt send",
		description: "Type text into a ticket's running session, only when the user asks you to tell the ticket's agent something, such as a change to the plan. A working agent reads it at its next step. One that asks a question in its tab gets it once the question is answered: the result then says queued, not sent. The user sees the text and confirms first.",
		parameters: Type.Object({
			ticket,
			text: Type.String({ description: "The text, in the user's words" }),
			hold: Type.Optional(Type.Boolean({ description: "True only when the user asks to hold the text until the agent stops working" })),
		}),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!ctx.hasUI || !(await ctx.ui.confirm(`Send this to ${t}?`, p.text))) return text("The user did not confirm; nothing was sent.");
			return text(await act.send(hub, t, p.text, p.hold));
		},
	});

	pi.registerTool({
		name: "tkt_continue",
		label: "tkt continue",
		description: "Take a ticket's next step, as tkt auto would: start, approve when allowed, sync, review, address, resolve or ci. Use it when the user says continue, go on or next for a ticket.",
		parameters: Type.Object({ ticket }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], `Take ${t}'s next step?`, "tkt takes the step tkt auto would take."))) return text("The user did not confirm; nothing ran.");
			return text(await act.next(hub, t));
		},
	});

	pi.registerTool({
		name: "tkt_stage",
		label: "tkt run",
		description: "Queue a stage in a fresh session: review, address (which merges the base first), resolve, ci, verify or start. Prefer tkt_continue, which picks the stage itself, and tkt_sync for a moved base. Never run review where the repository has a review bot in CI unless the user asks for it.",
		parameters: Type.Object({ ticket, stage: Type.String({ description: "review, address, resolve, ci, verify, start, or a stage from config.toml" }), as: preset }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], `Run ${p.stage} for ${t}?`, "It replaces the ticket's idle agent with a fresh session."))) return text("The user did not confirm; nothing ran.");
			return text(act.stage(hub, t, p.stage, p.as ?? ""));
		},
	});

	pi.registerTool({
		name: "tkt_sync",
		label: "tkt sync",
		description: "Queue tkt sync, which merges the base into a ticket's branch: tkt makes and pushes the merge itself when it is clean, and a fresh session resolves a conflict. Use it when the user asks to sync a ticket, or to bring it up to date with main.",
		parameters: Type.Object({ ticket }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], `Sync ${t} with its base?`, "tkt merges and pushes a clean merge itself; a conflict goes to a fresh session."))) return text("The user did not confirm; nothing ran.");
			return text(act.sync(hub, t));
		},
	});

	pi.registerTool({
		name: "tkt_auto",
		label: "tkt auto",
		description: "Turn tkt auto on or off for a ticket. On, it drives the ticket until it is reviewed clean, pauses when the ticket needs the user and goes on by itself after; approve_plan lets it approve the plan too.",
		parameters: Type.Object({ ticket, on: Type.Boolean(), approve_plan: Type.Optional(Type.Boolean()), as: preset }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], p.on ? `Turn tkt auto on for ${t}?` : `Turn tkt auto off for ${t}?`, p.on ? "tkt auto drives the ticket in the background." : "Its loop ends at its next look."))) return text("The user did not confirm; nothing changed.");
			return text(await act.auto(hub, t, p.on, p.approve_plan ?? false, p.as ?? ""));
		},
	});

	pi.registerTool({
		name: "tkt_hold",
		label: "tkt hold",
		description: "Put a ticket on hold, so tkt watch and tkt auto leave it alone, or take it off hold with on false. Use it when the user parks a ticket, or wants to drive it by hand.",
		parameters: Type.Object({ ticket, on: Type.Boolean() }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], p.on ? `Put ${t} on hold?` : `Take ${t} off hold?`, p.on ? "tkt watch and tkt auto leave it alone until you take it off hold." : "tkt watch and tkt auto can drive it again.")))
				return text("The user did not confirm; nothing changed.");
			return text(await act.hold(hub, t, p.on));
		},
	});

	pi.registerTool({
		name: "tkt_done",
		label: "tkt done",
		description: "Remove a merged ticket: close its tabs and its worktree. tkt refuses unless the pull request is merged. Always asks the user first.",
		parameters: Type.Object({ ticket }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!ctx.hasUI || !(await ctx.ui.confirm(`Remove ${t}?`, `Close its tabs and remove its worktree. tkt checks that its pull request is merged first.`))) return text("The user did not confirm; nothing was removed.");
			return text(await act.done(hub, t));
		},
	});

	pi.registerTool({
		name: "tkt_stats",
		label: "tkt stats",
		description: "How long tickets took from start to merge, how long they waited for the user, and their rounds.",
		parameters: Type.Object({ tickets: Type.Optional(Type.Array(ticket)), since_days: Type.Optional(Type.Number()) }),
		executionMode: "sequential",
		async execute(_id, p) {
			return text(await act.stats(hub, (p.tickets ?? []).map(id), p.since_days));
		},
	});

	pi.registerTool({
		name: "tkt_history",
		label: "tkt history",
		description:
			"Read what happened to tickets, merged ones too: each event from start to merge with its time (phases, the plan's approval, stage runs, tkt auto's steps and pauses), the time spent waiting on the user, the rounds, and each pull request's commits and reviews with the review bot's findings. Use it to say how a ticket went, what failed and what went well.",
		parameters: Type.Object({ tickets: Type.Array(ticket, { minItems: 1 }) }),
		executionMode: "sequential",
		async execute(_id, p) {
			const ids = [...new Set(p.tickets.flatMap((t) => {
				const many = expandIds(t, hub.key);
				return many.length > 0 ? many : [id(t)];
			}))];
			const out = await act.history(hub, ids, true);
			return text(out.length > 40000 ? `${out.slice(0, 40000)}\n[cut at 40000 characters; ask about fewer tickets for the rest]` : out);
		},
	});

	pi.registerTool({
		name: "ask_user",
		label: "Question",
		description: "Ask the user one question with fixed answers whenever a request is ambiguous: which ticket, which mode, what to do next. The user can also type an answer.",
		parameters: Type.Object({
			question: Type.String(),
			options: Type.Array(Type.Object({ label: Type.String(), description: Type.Optional(Type.String()) }), { minItems: 1 }),
		}),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const a = await ask(ctx, p.question, p.options);
			if (!a) return text("The user cancelled the question.");
			return text(a.typed ? `The user wrote: ${a.answer}` : `The user picked: ${a.answer}`);
		},
	});
}
