// What the page knows of the chat, and how each message from the chat
// changes it. It imports nothing at run time, so node --test covers it as
// it covers the extension's own modules.

import type { Item } from "../chat.ts";
import type { Board, Command, Dialog, Level, Theme, ToPage } from "../mirror.ts";

/** Press is a button you pressed: the command it sent, the ticket that
 * command names, and whether a job tkt runs for that ticket took over the
 * wait. The button stays busy until tkt answers or, when the command
 * started a job, until that job ends. */
export interface Press {
	cmd: string;
	ticket: string;
	job: boolean;
}

export interface Toast {
	id: number;
	text: string;
	/** full is the whole text when text is its brief. */
	full?: string;
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
	pressed: Press[];
}

export const initial: State = { link: "connecting", repo: "", theme: "", busy: false, items: [], live: [], dialogs: [], commands: [], toasts: [], seen: [], pressed: [] };

export type Event =
	| { t: "link"; link: Link }
	| { t: "dismiss"; id: number }
	| { t: "theme-local"; theme: Theme }
	| { t: "press"; cmd: string }
	| { t: "unpress"; cmd: string }
	| ToPage;

/** maxToast bounds a notice's brief. */
const maxToast = 120;

let toastId = 0;

/** apply returns the state after an event: a message from the chat, the
 * link changing, a toast you dismissed, or a button you pressed. A button
 * sends a command through the chat, whose answer lands in the chat
 * column, away from the button, so the answer also gets a toast. The
 * chat sends the board with a command's job before its answer, so a press
 * that started a job is bound to it by the time the answer comes. */
export function apply(s: State, e: Event): State {
	switch (e.t) {
		case "link":
			return e.link === "open" ? { ...s, link: e.link } : { ...s, link: e.link, busy: false, dialogs: [], pressed: [] };
		case "press":
			return s.pressed.some((p) => p.cmd === e.cmd) ? s : { ...s, pressed: [...s.pressed, { cmd: e.cmd, ticket: ticketOf(e.cmd), job: false }] };
		case "unpress":
			return { ...s, pressed: s.pressed.filter((p) => p.job || p.cmd !== e.cmd) };
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
			const pressed = s.pressed.flatMap((p) => (e.board.jobs[p.ticket] ? [{ ...p, job: true }] : p.job ? [] : [p]));
			return { ...s, board: e.board, seen: [...s.seen, ...fresh.map((a) => a.key)], toasts: toasts.slice(-5), pressed };
		}
		case "items": {
			const items = [...s.items.slice(0, e.from), ...e.items];
			if (s.pressed.length === 0) return { ...s, items };
			const had = new Set(s.items.map((it) => it.id));
			const notes = e.items.flatMap((it) => (it.kind === "note" && !had.has(it.id) ? [it.text] : []));
			if (notes.length === 0) return { ...s, items };
			const toasts = notes.map((text) => toast(text, "info"));
			return { ...s, items, pressed: answered(s.pressed), toasts: [...s.toasts, ...toasts].slice(-5) };
		}
		case "live":
			return { ...s, live: e.items };
		case "busy":
			return { ...s, busy: e.busy };
		case "dialog":
			return s.dialogs.some((d) => d.id === e.dialog.id) ? s : { ...s, dialogs: [...s.dialogs, e.dialog], pressed: answered(s.pressed) };
		case "closed":
			return { ...s, dialogs: s.dialogs.filter((d) => d.id !== e.id) };
		case "notify":
			return { ...s, pressed: answered(s.pressed), toasts: [...s.toasts, toast(e.text, e.level)].slice(-5) };
		case "theme":
			return { ...s, theme: e.theme };
	}
	return s;
}

/** answered drops the presses tkt answered: an answer ends a wait unless
 * a job took it over. */
function answered(pressed: Press[]): Press[] {
	return pressed.some((p) => !p.job) ? pressed.filter((p) => p.job) : pressed;
}

/** ticketOf is the ticket a command names, as the board's jobs name it:
 * "/sync ACM-12" names ACM-12, and "/chores run deps" the chore's run. */
export function ticketOf(cmd: string): string {
	const w = cmd.trim().split(/\s+/);
	if (w[0] === "/chores" && w[1] === "run") return w[2] ? `chore ${w[2]}` : "";
	return w[1] ?? "";
}

/** toast is a notice for text. An info notice shows its brief, since the
 * chat holds the whole and the notice shows it on hover; a warning or an
 * error shows whole, since what went wrong is in the detail. */
function toast(text: string, level: Level): Toast {
	const short = level === "info" ? brief(text) : text;
	return { id: ++toastId, text: short, full: short === text ? undefined : text, level };
}

/** brief is a text's first sentence, cut at a word when it runs past
 * maxToast: "Queued: tkt sync for ACM-12. It merges..." is "Queued: tkt
 * sync for ACM-12." */
export function brief(text: string): string {
	const first = text.trim().split(/(?<=[.!?])\s+/)[0];
	if (first.length <= maxToast) return first;
	const cut = first.slice(0, maxToast);
	const space = cut.lastIndexOf(" ");
	return `${(space > maxToast / 2 ? cut.slice(0, space) : cut).trimEnd()}...`;
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
