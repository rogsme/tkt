// What the page knows of the chat, and how each message from the chat
// changes it. It imports nothing at run time, so node --test covers it as
// it covers the extension's own modules.

import type { Item } from "../chat.ts";
import type { Board, Command, Dialog, Level, Theme, ToPage } from "../mirror.ts";

export interface Toast {
	id: number;
	text: string;
	level: Level;
}

/** Link is how the page reaches the chat: open, trying again, or shut out
 * because its token is not the chat's. */
export type Link = "connecting" | "open" | "closed" | "denied";

export interface State {
	link: Link;
	repo: string;
	theme: Theme;
	busy: boolean;
	items: Item[];
	live: Item[];
	dialogs: Dialog[];
	commands: Command[];
	toasts: Toast[];
	/** board is the board as the chat last sent it; seen holds the keys of
	 * the items the page told about, so each new one gets a toast once. */
	board?: Board;
	seen: string[];
}

export const initial: State = { link: "connecting", repo: "", theme: "", busy: false, items: [], live: [], dialogs: [], commands: [], toasts: [], seen: [] };

export type Event = { t: "link"; link: Link } | { t: "dismiss"; id: number } | { t: "theme-local"; theme: Theme } | ToPage;

let toastId = 0;

/** apply returns the state after an event: a message from the chat, the
 * link changing, or a toast you dismissed. */
export function apply(s: State, e: Event): State {
	switch (e.t) {
		case "link":
			return { ...s, link: e.link, busy: e.link === "open" ? s.busy : false, dialogs: e.link === "open" ? s.dialogs : [] };
		case "dismiss":
			return { ...s, toasts: s.toasts.filter((t) => t.id !== e.id) };
		case "theme-local":
			return { ...s, theme: e.theme };
		case "hello":
			return { ...s, link: "open", repo: e.repo, theme: e.theme, busy: e.busy, items: e.items, live: e.live, dialogs: e.dialogs, commands: e.commands, board: e.board, seen: e.board?.items.map((a) => a.key) ?? [] };
		case "board": {
			// What needed you when the page opened is on the board already;
			// what comes after gets a toast, as herdr's notification does.
			const fresh = e.board.items.filter((a) => !s.seen.includes(a.key));
			let toasts = s.toasts;
			for (const text of new Set(fresh.map((a) => `${a.ticket}: ${a.title}`))) toasts = [...toasts, { id: ++toastId, text, level: "warning" as const }];
			return { ...s, board: e.board, seen: [...s.seen, ...fresh.map((a) => a.key)], toasts: toasts.slice(-5) };
		}
		case "items":
			return { ...s, items: [...s.items.slice(0, e.from), ...e.items] };
		case "live":
			return { ...s, live: e.items };
		case "busy":
			return { ...s, busy: e.busy };
		case "dialog":
			return s.dialogs.some((d) => d.id === e.dialog.id) ? s : { ...s, dialogs: [...s.dialogs, e.dialog] };
		case "closed":
			return { ...s, dialogs: s.dialogs.filter((d) => d.id !== e.id) };
		case "notify":
			return { ...s, toasts: [...s.toasts, { id: ++toastId, text: e.text, level: e.level }].slice(-5) };
		case "theme":
			return { ...s, theme: e.theme };
	}
	return s;
}

/** completions are the slash commands a line starts to name, while you
 * type the name: "/ap" offers /approve. */
export function completions(text: string, commands: Command[]): Command[] {
	const m = /^\/([\w-]*)$/.exec(text);
	if (!m) return [];
	const word = m[1].toLowerCase();
	return commands.filter((c) => c.name.toLowerCase().startsWith(word)).sort((a, b) => a.name.localeCompare(b.name));
}

/** title is the browser tab's title, with how many tickets need you and
 * how many questions wait for an answer. */
export function title(s: State): string {
	const name = s.repo ? `tkt ${s.repo}` : "tkt";
	if (s.link === "closed" || s.link === "denied") return `${name} (closed)`;
	const n = needs(s.board) + s.dialogs.length;
	return n > 0 ? `(${n}) ${name}` : name;
}

/** needs counts the tickets that need you. */
export function needs(b: Board | undefined): number {
	return new Set(b?.items.map((a) => a.ticket) ?? []).size;
}
