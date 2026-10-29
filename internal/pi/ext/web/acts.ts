// What the page's buttons do. Each one sends the slash command you could
// type in the chat, or opens one of the page's own views; none acts any
// other way, so the page and the terminal never drift. It imports nothing
// at run time but the extension's pure modules, so node --test covers it.

import { type Attention, choicesOf } from "../attention.ts";
import type { Row } from "../model.ts";

/** Act is one button: cmd is the slash command it sends, tab the ticket's
 * tab it opens, and reply opens a box for what to tell the ticket's agent,
 * which /send sends. */
export interface Act {
	label: string;
	description?: string;
	cmd?: string;
	tab?: Tab;
	reply?: boolean;
	/** main marks the button that moves the ticket on. */
	main?: boolean;
}

export type Tab = "message" | "pr" | "steps" | "diff" | "screen" | "history";

export const tabs: { tab: Tab; label: string }[] = [
	{ tab: "message", label: "Last message" },
	{ tab: "pr", label: "Pull request" },
	{ tab: "steps", label: "Verification" },
	{ tab: "diff", label: "Diff" },
	{ tab: "screen", label: "Screen" },
	{ tab: "history", label: "History" },
];

/** cardActs are an item's answers, as /inbox offers them, each as a
 * button. */
export function cardActs(a: Attention, row: Row | undefined): Act[] {
	const id = a.ticket;
	return choicesOf(a).map((c, i) => {
		const act: Act = { label: c.label, description: c.description, main: i === 0 };
		switch (c.do) {
			case "approve":
				return { ...act, cmd: `/approve ${id}` };
			case "approve_auto":
				return { ...act, cmd: `/approve ${id} auto` };
			case "show":
				return { ...act, tab: "message" };
			case "peek":
				return { ...act, tab: "screen" };
			case "reply":
				return { ...act, reply: true };
			case "dismiss":
				return { ...act, cmd: `/dismiss ${id}` };
			case "check":
				return { ...act, cmd: `/check ${id}` };
			case "accept":
				return { ...act, cmd: `/accept ${id}` };
			case "next":
				return { ...act, cmd: `/next ${id}` };
			case "done":
				return { ...act, cmd: `/done ${id}` };
			case "auto_on":
				return { ...act, cmd: `/auto ${id} ${row?.auto?.approve_plan ? "auto-plan" : "on"}` };
			case "auto_off":
				return { ...act, cmd: `/auto ${id} off` };
			case "run":
				return { ...act, cmd: `/run ${id} ${c.stage}` };
			case "resume":
				return { ...act, cmd: `/relaunch ${id}` };
			case "start":
				return { ...act, cmd: `/start ${id}` };
			case "start_auto_plan":
				return { ...act, cmd: `/start ${id} auto-plan` };
			case "start_manual":
				return { ...act, cmd: `/start ${id} manual` };
		}
		return act;
	});
}

/** ticketActs are the buttons a ticket's view always has, for what its
 * state allows: the steps the board's keys take, and a few more. */
export function ticketActs(r: Row): Act[] {
	const id = r.ticket;
	const out: Act[] = [];
	const merged = r.phase === "merged" || r.phase === "closed";
	if (r.phase === "awaiting_approval" && !r.chore) out.push({ label: "Approve", cmd: `/approve ${id}`, main: true }, { label: "Approve and go auto", cmd: `/approve ${id} auto` });
	if (r.phase === "ready_for_human" && !r.chore) out.push({ label: "Verify", description: "Walk the human verification steps, and merge once they pass.", cmd: `/check ${id}`, main: true });
	if (r.phase === "ready_to_merge") out.push({ label: "Merge", description: "Every human verification step passed; tkt merge hands it to finish-ticket.", cmd: `/check ${id}`, main: true });
	if (merged) out.push({ label: "Remove it", description: "Close its tabs and remove its worktree, once tkt done confirms the merge.", cmd: `/done ${id}`, main: true });
	if (!merged && r.next?.kind === "command") out.push({ label: "Next step", description: r.next.text, cmd: `/next ${id}` });
	if (!merged && !r.chore && r.next?.reason === "findings") out.push({ label: "Accept review", description: "Take the review's findings as they are, for the reason you give, and go on to human verification.", cmd: `/accept ${id}` });
	if (!merged && (r.behind ?? 0) > 0) out.push({ label: "Sync the base", description: `${r.behind} commit${r.behind === 1 ? "" : "s"} behind.`, cmd: `/sync ${id}` });
	if (!merged && !r.agent) out.push({ label: "Relaunch", description: "tkt resume relaunches its newest session.", cmd: `/relaunch ${id}` });
	if (!merged && !r.chore) {
		const on = r.auto?.running || r.auto?.wanted;
		out.push(on ? { label: "Auto off", cmd: `/auto ${id} off` } : { label: "Auto on", description: "tkt auto drives it until it is reviewed clean.", cmd: `/auto ${id} on` });
	}
	if (!merged) out.push(r.hold ? { label: "Off hold", cmd: `/hold ${id} off` } : { label: "Hold", description: "tkt watch and tkt auto leave it alone.", cmd: `/hold ${id}` });
	return out;
}

export type Mode = "auto" | "manual" | "auto-plan" | "yolo";

/** startLine is the /start line for the tickets you typed. */
export function startLine(ids: string, mode: Mode, as: string): string {
	const list = ids.split(/[\s,]+/).filter(Boolean).join(" ");
	if (!list) return "";
	return ["/start", list, mode === "auto" ? "" : mode, as.trim() ? `as ${as.trim()}` : ""].filter(Boolean).join(" ");
}

/** suggestLine is the /suggest line for what you asked for. An empty
 * request asks for 3 tickets of any kind, since /suggest alone would have
 * the chat ask what to look for. */
export function suggestLine(request: string, as: string): string {
	const r = request.replace(/\s+/g, " ").trim() || "3";
	return ["/suggest", r, as.trim() ? `as ${as.trim()}` : ""].filter(Boolean).join(" ");
}

/** sendLine is the /send line that tells a ticket's agent text. */
export function sendLine(id: string, text: string): string {
	return text.trim() ? `/send ${id} ${text.trim()}` : "";
}

/** Nav is what the center column shows: a ticket's tab, one of the
 * repository's views, or the board. It lives in the page's address, so a
 * reload keeps it. */
export type Nav = { ticket: string; tab: Tab } | { view: View } | { board: true };

export type View = "start" | "suggest" | "chores" | "stats" | "version";

const viewNames = new Set<View>(["start", "suggest", "chores", "stats", "version"]);
const tabNames = new Set<Tab>(tabs.map((t) => t.tab));

/** navOf reads the address's hash, such as #ACM-12/diff. */
export function navOf(hash: string): Nav {
	const h = decodeURIComponent(hash.replace(/^#/, ""));
	if (viewNames.has(h as View)) return { view: h as View };
	const m = /^(.+?)(?:\/(\w+))?$/.exec(h);
	if (!m || !/^[A-Za-z0-9][\w./-]*$/.test(m[1])) return { board: true };
	const tab = tabNames.has(m[2] as Tab) ? (m[2] as Tab) : "message";
	return { ticket: m[1], tab };
}

/** hashOf writes a nav into the address. */
export function hashOf(n: Nav): string {
	if ("view" in n) return `#${n.view}`;
	if ("ticket" in n) return `#${encodeURIComponent(n.ticket).replaceAll("%2F", "/")}/${n.tab}`;
	return "#";
}
