// The slash commands: the same actions as the tools, with no model call in
// between, for when you know exactly what you want.

import type { ExtensionAPI, ExtensionCommandContext } from "@earendil-works/pi-coding-agent";
import type { AutocompleteItem } from "@earendil-works/pi-tui";
import * as act from "./actions.ts";
import { type Hub, reasonOf } from "./hub.ts";
import { expandIds } from "./model.ts";
import { ask, confirm, input, notify } from "./dialog.ts";
import { board } from "./ui.ts";
import type { Web } from "./web.ts";

export function registerCommands(pi: ExtensionAPI, hub: Hub, web: Web): void {
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
			notify(ctx, `usage: ${usage}`, "warning");
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
		description: "Start tickets: /start ACM-12 [ACM-13 ...] [auto | auto-plan | manual | yolo] [as PRESET]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const words = args.trim().split(/\s+/).filter(Boolean);
			const as = preset(words);
			const modeWord = words.find((w) => ["auto", "auto-plan", "full", "after-plan", "manual", "yolo"].includes(w.toLowerCase()))?.toLowerCase();
			const list = expandIds(words.filter((w) => w.toLowerCase() !== modeWord).join(" "), hub.key);
			const rest = words.filter((w) => w.toLowerCase() !== modeWord && /^\d+$/.test(w)).map((w) => hub.idOf(w));
			const all = [...new Set([...list, ...rest])];
			if (all.length === 0) return notify(ctx, "usage: /start ACM-12 [ACM-13 ...] [auto | auto-plan | manual | yolo] [as PRESET]", "warning");
			// after-plan and full are auto and auto-plan under their old names.
			const mode: act.Mode = modeWord === "auto-plan" || modeWord === "full" ? "auto-plan" : modeWord === "manual" ? "manual" : modeWord === "yolo" ? "yolo" : "auto";
			if (mode === "yolo") {
				// Yolo always asks, even for one ticket, and its question stands
				// in for the plain one, so you answer once.
				const a = await ask(ctx, `No person checks anything before ${all.join(", ")} merge${all.length === 1 ? "s" : ""}. Start in yolo?`, [
					{ label: "Start in yolo", description: "tkt auto approves the plan, has every step reviewed and verified by agents, and merges once verify-pr passed every step." },
					{ label: "Not now" },
				], false);
				if (!a?.answer.startsWith("Start")) return say("Nothing started.");
				const refused = await act.yoloRefusal(hub, all);
				if (refused) return say(refused);
			} else if (all.length > 1 && !(await confirm(ctx, `Start ${all.join(", ")}?`, "Each ticket starts in its own tab."))) return;
			say(act.start(hub, all, mode, as));
		},
	});

	pi.registerCommand("suggest", {
		description: "Have suggest-tickets pick tickets to start in the suggest tab: /suggest [N | what you want] [as PRESET], or /suggest alone to type it",
		handler: async (args, ctx) => {
			const words = args.trim().split(/\s+/).filter(Boolean);
			// Only the last two words name a preset, so a request may say "as".
			const n = words.length;
			const as = n >= 2 && (words[n - 2] === "as" || words[n - 2] === "--as") ? words.splice(n - 2, 2)[1] : "";
			let request = words.join(" ");
			// Without words, you type the request; a number there is a count too.
			if (!request) {
				if (!ctx.hasUI) return notify(ctx, "usage: /suggest [1 to 10 | what you want] [as PRESET]", "warning");
				request = (await input(ctx, "What should suggest-tickets look for?", "five tickets tagged feedback, or a number"))?.trim() ?? "";
				if (!request) return notify(ctx, "Nothing sent to suggest-tickets.", "info");
			}
			if (/^\d+$/.test(request) && (Number(request) < 1 || Number(request) > 10)) return notify(ctx, "usage: /suggest [1 to 10 | what you want] [as PRESET]", "warning");
			say(await act.suggest(hub, request, as));
		},
	});

	pi.registerCommand("chores", {
		description: "List the repository's chores with their next run; /chores run NAME starts a run now",
		handler: async (args, ctx) => {
			const words = args.trim().split(/\s+/).filter(Boolean);
			if (words[0]?.toLowerCase() !== "run") return say(await act.chores(hub));
			if (!words[1]) return notify(ctx, "usage: /chores run NAME", "warning");
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
		description: "tkt auto for a ticket: /auto ACM-12 on | off | auto-plan | yolo [as PRESET]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/auto ACM-12 on | off | auto-plan | yolo [as PRESET]");
			if (!id) return;
			const words = args.trim().split(/\s+/);
			const as = preset(words);
			const how = words[1]?.toLowerCase() ?? "on";
			if (how === "yolo") {
				const a = await ask(ctx, `No person checks anything before ${id} merges. Start it in yolo?`, [
					{ label: "Start in yolo", description: "tkt auto approves the plan, has every step reviewed and verified by agents, and merges once verify-pr passed every step." },
					{ label: "Not now" },
				], false);
				if (!a?.answer.startsWith("Start")) return say("Nothing started.");
			}
			say(await act.auto(hub, id, how !== "off", how === "full" || how === "auto-plan", as, how === "yolo"));
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
			// The text keeps its lines, such as feedback on a plan.
			const text = args.trim().replace(/^\S+\s*/, "");
			if (!id || !text) return notify(ctx, "usage: /send ACM-12 TEXT", "warning");
			say(await act.send(hub, id, text));
		},
	});

	pi.registerCommand("run", {
		description: "Run a stage for a ticket in a fresh session: /run ACM-12 review | fix | verify ... [as PRESET]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const words = args.trim().split(/\s+/).filter(Boolean);
			const as = preset(words);
			if (words.length !== 2) return notify(ctx, "usage: /run ACM-12 STAGE [as PRESET]", "warning");
			say(act.stage(hub, hub.idOf(words[0]), words[1], as));
		},
	});

	pi.registerCommand("relaunch", {
		description: "Relaunch a ticket's newest session when no agent runs for it, as tkt resume does: /relaunch ACM-12",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/relaunch ACM-12");
			if (id) say(act.resume(hub, id));
		},
	});

	pi.registerCommand("dismiss", {
		description: "Set aside what a ticket needs from you, until its situation changes: /dismiss ACM-12",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			// A failed job's item names its job, such as "chore nightly".
			const raw = args.trim();
			if (!raw) return notify(ctx, "usage: /dismiss ACM-12", "warning");
			const id = hub.items.some((a) => a.ticket === raw) ? raw : hub.idOf(raw);
			const n = hub.dismissTicket(id);
			notify(ctx, n ? `Set aside what ${id} needs; it comes back once its situation changes.` : `${id} needs nothing from you now.`);
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

	pi.registerCommand("accept", {
		description: "Accept the review at a ticket's head as it is, and go on to human verification: /accept ACM-12 [reason]",
		getArgumentCompletions: ids,
		handler: async (args, ctx) => {
			const id = one(args, ctx, "/accept ACM-12 [reason]");
			if (!id) return;
			const why = args.trim().replace(/^\S+\s*/, "") || (await act.acceptReason(ctx, id));
			if (!why) return notify(ctx, `Nothing accepted: tkt accept needs your reason, such as /accept ${id} the finding is about the description.`, "warning");
			say(await act.accept(hub, id, why));
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
			if (id && (await confirm(ctx, `Remove ${id}?`, "Close its tabs and remove its worktree, once tkt done confirms the merge."))) say(await act.done(hub, id));
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
					if (await confirm(ctx, `Remove ${a.ticket}?`, "Close its tabs and remove its worktree, once tkt done confirms the merge.")) say(await act.done(hub, a.ticket));
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
			if (list.length === 0) return notify(ctx, "usage: /history ACM-12 [ACM-13 ...]", "warning");
			say(await act.history(hub, list, false));
		},
	});

	pi.registerCommand("stats", {
		description: "How long tickets took from start to merge: /stats [ACM-12 ...]",
		getArgumentCompletions: ids,
		handler: async (args) => say(await act.stats(hub, hub.idsIn(args))),
	});

	pi.registerCommand("version", {
		description: "Say which tkt and pi this chat runs, and whether a newer tkt is installed",
		handler: async () => say(await act.version(hub)),
	});

	pi.registerCommand("web", {
		description: "Open the chat's web page in your browser, mirrored both ways",
		handler: async (_args, ctx) => {
			if (web.why && !web.other) return say(`This chat serves no web page: ${web.why}.`);
			const env = await hub.tkt<{ url: string; opened: boolean }>(["web"]);
			if (!env.ok || !env.result) return say(`tkt web failed: ${reasonOf(env)}`);
			// The address holds the page's token, so it goes to you alone,
			// never into the session the model reads.
			if (!env.result.opened) return notify(ctx, `Open the web page at ${env.result.url}`);
			say(web.other ? "Opened the web page of the chat that serves it, which mirrors that chat." : "Opened the web page in your browser. What you type there shows here too.");
		},
	});

	pi.registerCommand("update", {
		description: "Install the newest tkt, then quit so you reopen the chat on it",
		handler: async (_args, ctx) => act.update(hub, ctx),
	});
}
