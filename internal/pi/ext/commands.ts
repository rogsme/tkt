// The slash commands: the same actions as the tools, with no model call in
// between, for when you know exactly what you want.

import type { ExtensionAPI, ExtensionCommandContext } from "@earendil-works/pi-coding-agent";
import type { AutocompleteItem } from "@earendil-works/pi-tui";
import * as act from "./actions.ts";
import type { Hub } from "./hub.ts";
import { expandIds } from "./model.ts";
import { board } from "./ui.ts";

export function registerCommands(pi: ExtensionAPI, hub: Hub): void {
	const ids = (prefix: string): AutocompleteItem[] | null => {
		const words = prefix.split(/\s+/);
		const last = (words.pop() ?? "").toUpperCase();
		const head = words.length ? `${words.join(" ")} ` : "";
		const items = hub.rows.filter((r) => r.ticket.toUpperCase().startsWith(last)).map((r) => ({ value: head + r.ticket, label: r.ticket, description: r.phase.replaceAll("_", " ") }));
		return items.length ? items : null;
	};
	const one = (args: string, ctx: ExtensionCommandContext, usage: string): string | undefined => {
		const raw = args.trim().split(/\s+/)[0];
		if (!raw) {
			ctx.ui.notify(`usage: ${usage}`, "warning");
			return undefined;
		}
		return hub.idOf(raw);
	};
	const say = (text: string) => hub.post(text);
	/** preset takes "as NAME" or "--as NAME" out of the words. */
	const preset = (words: string[]): string => {
		const i = words.findIndex((w) => w === "as" || w === "--as");
		if (i < 0 || !words[i + 1]) return "";
		return words.splice(i, 2)[1];
	};

	pi.registerCommand("start", {
		description: "Start tickets: /start ACM-12 [ACM-13 ...] [auto | after-plan] [as PRESET]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const words = args.trim().split(/\s+/).filter(Boolean);
			const as = preset(words);
			const modeWord = words.find((w) => ["auto", "full", "after-plan", "manual"].includes(w.toLowerCase()))?.toLowerCase();
			const list = expandIds(words.filter((w) => w.toLowerCase() !== modeWord).join(" "), hub.key);
			const rest = words.filter((w) => w.toLowerCase() !== modeWord && /^\d+$/.test(w)).map((w) => hub.idOf(w));
			const all = [...new Set([...list, ...rest])];
			if (all.length === 0) return ctx.ui.notify("usage: /start ACM-12 [ACM-13 ...] [auto | after-plan] [as PRESET]", "warning");
			const mode: act.Mode = modeWord === "auto" || modeWord === "full" ? "auto" : modeWord === "after-plan" ? "auto_after_plan" : "manual";
			if (all.length > 1 && !(await ctx.ui.confirm(`Start ${all.join(", ")}?`, "Each ticket starts in its own tab."))) return;
			say(act.start(hub, all, mode, as));
		},
	});

	pi.registerCommand("suggest", {
		description: "Have suggest-tickets pick tickets that can run side by side, then start them from /inbox: /suggest [N] [as PRESET]",
		handler: async (args, ctx) => {
			const words = args.trim().split(/\s+/).filter(Boolean);
			const as = preset(words);
			const n = Number(words.find((w) => /^\d+$/.test(w)) ?? "3");
			if (n < 1 || n > 10) return ctx.ui.notify("usage: /suggest [1 to 10] [as PRESET]", "warning");
			say(act.suggest(hub, n, as));
		},
	});

	pi.registerCommand("chores", {
		description: "List the repository's chores with their next run; /chores run NAME starts a run now",
		handler: async (args, ctx) => {
			const words = args.trim().split(/\s+/).filter(Boolean);
			if (words[0]?.toLowerCase() !== "run") return say(await act.chores(hub));
			if (!words[1]) return ctx.ui.notify("usage: /chores run NAME", "warning");
			say(act.choreRun(hub, words[1]));
		},
	});

	pi.registerCommand("approve", {
		description: "Approve a ticket's plan: /approve ACM-12 [auto]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/approve ACM-12 [auto]");
			if (id) say(await act.approve(hub, id, /\bauto\b/i.test(args) ? "auto" : "manual"));
		},
	});

	pi.registerCommand("next", {
		description: "Take a ticket's next step, as tkt auto would: /next ACM-12",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/next ACM-12");
			if (id) say(await act.next(hub, id));
		},
	});

	pi.registerCommand("auto", {
		description: "tkt auto for a ticket: /auto ACM-12 on | off | full [as PRESET]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/auto ACM-12 on | off | full [as PRESET]");
			if (!id) return;
			const words = args.trim().split(/\s+/);
			const as = preset(words);
			const how = words[1]?.toLowerCase() ?? "on";
			say(await act.auto(hub, id, how !== "off", how === "full", as));
		},
	});

	pi.registerCommand("hold", {
		description: "Put a ticket on hold, so tkt watch and tkt auto leave it alone: /hold ACM-12 [off]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/hold ACM-12 [off]");
			if (id) say(await act.hold(hub, id, args.trim().split(/\s+/)[1]?.toLowerCase() !== "off"));
		},
	});

	pi.registerCommand("sync", {
		description: "Merge the base into a ticket's branch: tkt itself when clean, a fresh session on a conflict: /sync ACM-12",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/sync ACM-12");
			if (id) say(act.sync(hub, id));
		},
	});

	pi.registerCommand("send", {
		description: "Type text into a ticket's session: /send ACM-12 TEXT",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/send ACM-12 TEXT");
			const text = args.trim().split(/\s+/).slice(1).join(" ");
			if (!id || !text) return ctx.ui.notify("usage: /send ACM-12 TEXT", "warning");
			say(await act.send(hub, id, text));
		},
	});

	pi.registerCommand("show", {
		description: "Show a ticket's last agent message, such as its plan: /show ACM-12",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/show ACM-12");
			if (id) say(await act.show(hub, id, "message"));
		},
	});

	pi.registerCommand("peek", {
		description: "Watch a ticket's agent screen live, such as a question it asks: /peek ACM-12",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/peek ACM-12");
			if (id) await act.watchPeek(hub, ctx, id);
		},
	});

	pi.registerCommand("check", {
		description: "Walk a ticket's human verification steps, and merge once they pass: /check ACM-12",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/check ACM-12");
			if (id) say(await act.check(hub, ctx, id));
		},
	});

	pi.registerCommand("done", {
		description: "Remove a merged ticket's tabs and worktree: /done ACM-12",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/done ACM-12");
			if (id && (await ctx.ui.confirm(`Remove ${id}?`, "Close its tabs and remove its worktree, once tkt done confirms the merge."))) say(await act.done(hub, id));
		},
	});

	pi.registerCommand("inbox", {
		description: "Go through what needs you, oldest first",
		handler: async (_args, ctx) => say(await act.inbox(hub, ctx)),
	});

	pi.registerCommand("board", {
		description: "Show every ticket in columns, and act on one; /board hide or /board show for the board pinned to the top",
		handler: async (args, ctx) => {
			const word = args.trim().toLowerCase();
			if (word === "hide" || word === "show") {
				hub.showBoard(word === "show");
				return;
			}
			await hub.refresh();
			const a = await board(ctx, () => hub.rows);
			if (!a) return;
			switch (a.key) {
				case "show":
					return say(await act.show(hub, a.ticket, "message"));
				case "peek":
					return act.watchPeek(hub, ctx, a.ticket);
				case "check":
					return say(await act.check(hub, ctx, a.ticket));
				case "approve":
					return say(await act.approve(hub, a.ticket, "manual"));
				case "next":
					return say(await act.next(hub, a.ticket));
				case "auto": {
					const r = hub.row(a.ticket);
					return say(await act.auto(hub, a.ticket, !(r?.auto?.running || r?.auto?.wanted), false));
				}
				case "done":
					if (await ctx.ui.confirm(`Remove ${a.ticket}?`, "Close its tabs and remove its worktree, once tkt done confirms the merge.")) say(await act.done(hub, a.ticket));
					return;
				case "inbox":
					return say(await act.inbox(hub, ctx));
			}
		},
	});

	pi.registerCommand("history", {
		description: "What happened to tickets, merged ones too: events, commits and reviews: /history ACM-12 [ACM-13 ...]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const list = hub.idsIn(args);
			if (list.length === 0) return ctx.ui.notify("usage: /history ACM-12 [ACM-13 ...]", "warning");
			say(await act.history(hub, list, false));
		},
	});

	pi.registerCommand("stats", {
		description: "How long tickets took from start to merge: /stats [ACM-12 ...]",
		getArgumentCompletions: ids,
		handler: async (args) => say(await act.stats(hub, hub.idsIn(args))),
	});

	pi.registerCommand("update", {
		description: "Install the newest tkt, then quit so you reopen the chat on it",
		handler: async (_args, ctx) => act.update(hub, ctx),
	});
}
