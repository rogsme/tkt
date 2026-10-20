// The tools the model calls. Each one runs tkt and nothing else. The
// guards live here, in code, not in the system prompt: a tool that changes
// a ticket your last message did not name asks you first, and removing a
// ticket always asks.

import { StringEnum } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import * as act from "./actions.ts";
import { widgetLines } from "./board.ts";
import type { Hub } from "./hub.ts";
import { expandIds, mentions, normalizeId } from "./model.ts";
import { ask } from "./ui.ts";

const text = (t: string) => ({ content: [{ type: "text" as const, text: t }], details: undefined });

/** guard lets a change through when your last message names every ticket
 * it touches, or when you confirm it. */
async function guard(hub: Hub, ctx: ExtensionContext, ids: string[], what: string): Promise<boolean> {
	if (ids.length === 1 && mentions(hub.lastInput, ids[0])) return true;
	if (!ctx.hasUI) return false;
	return ctx.ui.confirm(`${what}?`, `${what}: ${ids.join(", ")}`);
}

export function registerTools(pi: ExtensionAPI, hub: Hub): void {
	const id = (raw: string) => normalizeId(raw, hub.key);
	const ticket = Type.String({ description: "A ticket id, such as ACM-123" });

	pi.registerTool({
		name: "tkt_status",
		label: "tkt status",
		description: "Read the board: every ticket's phase, agent, pull request and next step, and what needs the user. Pass ticket for one ticket's details.",
		parameters: Type.Object({ ticket: Type.Optional(ticket) }),
		executionMode: "sequential",
		async execute(_id, p) {
			await hub.refresh();
			if (p.ticket) {
				const r = hub.row(id(p.ticket));
				if (!r) return text(`${id(p.ticket)} has no worktree. Known tickets: ${hub.rows.map((x) => x.ticket).join(", ") || "none"}.`);
				const lines = [
					`${r.ticket}: phase ${r.phase}${r.phase_since ? ` since ${r.phase_since}` : ""}, stage ${r.stage || "-"}`,
					`agent: ${r.agent ? `${r.agent.status} in pane ${r.agent.pane_id}` : "none"}; last status: ${r.last_status || "none"}`,
					`pull request: ${r.pr ? `#${r.pr.number} ${r.pr.state}` : "none"}; checks: ${r.checks?.state ?? "-"}; review: ${r.review ? `${r.review.verdict}${r.review.at_head ? "" : " (old head)"}` : "-"}`,
					`auto: ${r.auto?.running ? "running" : r.auto?.wanted ? "paused" : r.auto?.stop ? `stopped (${r.auto.stop})` : "off"}; rounds ${r.auto?.rounds ?? 0}`,
					`next: ${r.next.text} (${r.next.reason})`,
				];
				return text(lines.join("\n"));
			}
			const s = { rows: hub.rows, jobs: hub.labels, waiting: 0, now: Date.now(), tick: 0, repo: hub.repo };
			const board = widgetLines(s, 0, hub.needsYou()).map((l) => l.text);
			const inbox = hub.inbox.map((a) => `- ${a.text}`);
			return text([...board, "", inbox.length ? "Needs the user:" : "Nothing needs the user.", ...inbox].join("\n"));
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
			"Start tickets in the background and return at once. mode manual stops at the plan; auto_after_plan also stops at the plan, then tkt auto takes over once the user approves; auto approves the plan too and drives each ticket to READY_FOR_HUMAN. Expand ranges such as ACM-12 to ACM-15 into every id.",
		parameters: Type.Object({
			tickets: Type.Array(ticket, { minItems: 1 }),
			mode: StringEnum(["manual", "auto", "auto_after_plan"] as const),
		}),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const ids = [...new Set(p.tickets.flatMap((t) => {
				const many = expandIds(t, hub.key);
				return many.length > 0 ? many : [id(t)];
			}))];
			if (ids.length === 0) return text("Name the tickets to start, such as ACM-123.");
			if (!(await guard(hub, ctx, ids, `Start ${ids.length} ticket${ids.length === 1 ? "" : "s"} (${p.mode.replaceAll("_", " ")})`))) return text("The user did not confirm; nothing started.");
			return text(act.start(hub, ids, p.mode));
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
			if (!(await guard(hub, ctx, [t], "Approve the plan"))) return text("The user did not confirm; nothing was approved.");
			return text(act.approve(hub, t, p.then));
		},
	});

	pi.registerTool({
		name: "tkt_send",
		label: "tkt send",
		description: "Type text into a ticket's running session, such as a change the user wants in the plan. The user sees the text and confirms first.",
		parameters: Type.Object({ ticket, text: Type.String({ description: "The text, in the user's words" }) }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!ctx.hasUI || !(await ctx.ui.confirm(`Send this to ${t}?`, p.text))) return text("The user did not confirm; nothing was sent.");
			return text(act.send(hub, t, p.text));
		},
	});

	pi.registerTool({
		name: "tkt_continue",
		label: "tkt continue",
		description: "Take a ticket's next step, as tkt auto would: start, approve when allowed, review, address, sync or ci. Use it when the user says continue, go on or next for a ticket.",
		parameters: Type.Object({ ticket }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], "Take the next step"))) return text("The user did not confirm; nothing ran.");
			return text(await act.next(hub, ctx, t));
		},
	});

	pi.registerTool({
		name: "tkt_stage",
		label: "tkt run",
		description: "Run a stage in a fresh session: review, address, sync, ci, verify or start. Prefer tkt_continue, which picks the stage itself.",
		parameters: Type.Object({ ticket, stage: Type.String({ description: "review, address, sync, ci, verify, start, or a stage from config.toml" }) }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], `Run ${p.stage}`))) return text("The user did not confirm; nothing ran.");
			return text(act.stage(hub, t, p.stage));
		},
	});

	pi.registerTool({
		name: "tkt_auto",
		label: "tkt auto",
		description: "Turn tkt auto on or off for a ticket. On, it drives the ticket to READY_FOR_HUMAN and stops when the ticket needs the user; approve_plan lets it approve the plan too.",
		parameters: Type.Object({ ticket, on: Type.Boolean(), approve_plan: Type.Optional(Type.Boolean()) }),
		executionMode: "sequential",
		async execute(_id, p, _signal, _update, ctx) {
			const t = id(p.ticket);
			if (!(await guard(hub, ctx, [t], p.on ? "Turn tkt auto on" : "Turn tkt auto off"))) return text("The user did not confirm; nothing changed.");
			return text(act.auto(hub, t, p.on, p.approve_plan ?? false));
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
			if (!ctx.hasUI || !(await ctx.ui.confirm(`Remove ${t}?`, `Close ${t}'s tabs and remove its worktree. tkt checks that its pull request is merged first.`))) return text("The user did not confirm; nothing was removed.");
			return text(act.done(hub, t));
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
