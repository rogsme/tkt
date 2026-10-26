// The mirror keeps the web pages in step with the chat: the items they
// draw, the answer streaming in, whether the model works, the questions
// waiting for an answer, the notices, and the board. A page that opens late
// gets all of it at once. It imports nothing from pi or Bun, so node --test
// covers it; web.ts feeds it the chat's events and carries its messages.

import type { Attention } from "./attention.ts";
import { type Item, patch } from "./chat.ts";
import type { Incident, Row } from "./model.ts";
import type { Mark } from "./report.ts";

export type Theme = "light" | "dark" | "";
export type Level = "info" | "warning" | "error";

export interface Option {
	label: string;
	description?: string;
}

/** Dialog is a question the extension asks, as the page shows it. An ask
 * takes one of its options, or with other a text you type. */
export interface Dialog {
	id: string;
	kind: "ask" | "confirm" | "input" | "editor" | "checklist";
	title: string;
	message?: string;
	options?: Option[];
	other?: boolean;
	placeholder?: string;
	prefill?: string;
	/** steps and marks are a checklist's: the human verification steps,
	 * and how each is marked so far. */
	steps?: { n: number; text: string }[];
	marks?: Mark[];
}

/** Board is what the board shows, as the chat knows it: every ticket, what
 * needs you, the jobs running, and the repository's state. */
export interface Board {
	rows: Row[];
	items: Attention[];
	/** jobs maps a ticket to the label of its running job. */
	jobs: Record<string, string>;
	incidents: Incident[];
	watch?: "on" | "off" | "failing";
	/** update is a newer tkt release that /update installs. */
	update?: string;
}

/** Query asks the chat for a view a page draws, such as a ticket's diff.
 * A query only reads: every change goes through a slash command. */
export interface Query {
	what: "message" | "screen" | "steps" | "diff" | "history" | "stats" | "chores" | "suggest" | "version";
	ticket: string;
}

const ticketViews = new Set<Query["what"]>(["message", "screen", "steps", "diff", "history"]);
const views = new Set<Query["what"]>([...ticketViews, "stats", "chores", "suggest", "version"]);

/** idLike is what a ticket's id, or a chore run's branch, may look like:
 * nothing a command could read as a flag. */
const idLike = /^[A-Za-z0-9][\w./-]{0,99}$/;

export interface Command {
	name: string;
	description: string;
}

export interface Image {
	type: "image";
	data: string;
	mimeType: string;
}

/** ToPage is what the mirror sends a page. */
export type ToPage =
	| { t: "hello"; repo: string; theme: Theme; busy: boolean; items: Item[]; live: Item[]; dialogs: Dialog[]; commands: Command[]; board?: Board }
	| { t: "items"; from: number; items: Item[] }
	| { t: "live"; items: Item[] }
	| { t: "busy"; busy: boolean }
	| { t: "dialog"; dialog: Dialog }
	| { t: "closed"; id: string }
	| { t: "notify"; text: string; level: Level }
	| { t: "theme"; theme: Theme }
	| { t: "board"; board: Board }
	| { t: "result"; id: number; value: unknown }
	| { t: "open"; ticket: string; tab: string };

export interface Page {
	send(msg: ToPage): void;
}

/** Actions are what a page asks of the chat. send gets the page that sent
 * the text, so a command that opens a view opens it there. */
export interface Actions {
	send(text: string, images: Image[], page: Page): void;
	stop(): void;
	theme(theme: Theme): void;
	commands(): Command[];
	query(q: Query): Promise<unknown>;
}

/** maxImages bounds the images one message carries. */
const maxImages = 8;
const imageTypes = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

export class Mirror {
	readonly pages = new Set<Page>();
	theme: Theme = "";
	private items: Item[] = [];
	private keys: string[] = [];
	private live: Item[] = [];
	private busy = false;
	private board?: Board;
	private boardKey = "";
	private dialogs = new Map<string, { dialog: Dialog; answer: (value: unknown) => void }>();
	private next = 0;
	private readonly repo: string;
	private readonly act: Actions;

	constructor(repo: string, act: Actions) {
		this.repo = repo;
		this.act = act;
	}

	join(page: Page): void {
		this.pages.add(page);
		page.send({ t: "hello", repo: this.repo, theme: this.theme, busy: this.busy, items: this.items, live: this.live, dialogs: [...this.dialogs.values()].map((d) => d.dialog), commands: this.act.commands(), board: this.board });
	}

	leave(page: Page): void {
		this.pages.delete(page);
	}

	/** setItems takes the chat's items as they stand and sends each page
	 * what changed. */
	setItems(items: Item[]): void {
		const p = patch(this.keys, items);
		if (!p) return;
		this.items = items;
		this.keys = p.keys;
		this.broadcast({ t: "items", ...p.patch });
	}

	/** reset forgets the items, as when the chat moves to another session,
	 * so each page draws the new ones whole. */
	reset(items: Item[]): void {
		this.items = items;
		this.keys = items.map((it) => JSON.stringify(it));
		this.broadcast({ t: "items", from: 0, items });
		this.setLive([]);
	}

	/** setLive shows the answer streaming in, or nothing once it is done. */
	setLive(items: Item[]): void {
		if (items.length === 0 && this.live.length === 0) return;
		this.live = items;
		this.broadcast({ t: "live", items });
	}

	/** setBoard takes the board as it stands and sends it to each page when
	 * it changed. */
	setBoard(board: Board): void {
		const key = JSON.stringify(board);
		if (key === this.boardKey) return;
		this.board = board;
		this.boardKey = key;
		this.broadcast({ t: "board", board });
	}

	setBusy(busy: boolean): void {
		if (busy === this.busy) return;
		this.busy = busy;
		this.broadcast({ t: "busy", busy });
	}

	/** ask shows a question on every page, and on pages that open while it
	 * waits. answer settles with the first page's answer; it never settles
	 * when the question closes another way. */
	ask(d: Omit<Dialog, "id">): { id: string; answer: Promise<unknown> } {
		const id = `d${++this.next}`;
		const dialog = { ...d, id };
		const answer = new Promise<unknown>((resolve) => this.dialogs.set(id, { dialog, answer: resolve }));
		this.broadcast({ t: "dialog", dialog });
		return { id, answer };
	}

	/** close takes a question off the pages, as when you answered it in the
	 * terminal. */
	close(id: string): void {
		if (this.dialogs.delete(id)) this.broadcast({ t: "closed", id });
	}

	notify(text: string, level: Level = "info"): void {
		this.broadcast({ t: "notify", text, level });
	}

	/** receive takes a message from a page. What a page sends is checked
	 * here: a page is a browser tab, so nothing it says is trusted. */
	receive(raw: unknown, page: Page): void {
		if (!raw || typeof raw !== "object") return;
		const m = raw as Record<string, unknown>;
		switch (m.t) {
			case "send": {
				const text = typeof m.text === "string" ? m.text : "";
				const images = Array.isArray(m.images) ? m.images.filter(isImage).slice(0, maxImages) : [];
				if (text.trim() || images.length) this.act.send(text, images, page);
				return;
			}
			case "query": {
				const what = m.what as Query["what"];
				const ticket = typeof m.ticket === "string" ? m.ticket : "";
				if (typeof m.id !== "number" || !views.has(what) || (ticketViews.has(what) ? !idLike.test(ticket) : ticket !== "")) return;
				const id = m.id;
				void this.act.query({ what, ticket }).then(
					(value) => this.pages.has(page) && page.send({ t: "result", id, value }),
					(err: unknown) => this.pages.has(page) && page.send({ t: "result", id, value: { ok: false, error: { code: "internal", message: String(err) } } }),
				);
				return;
			}
			case "answer": {
				const d = typeof m.id === "string" ? this.dialogs.get(m.id) : undefined;
				if (!d) return;
				const value = answerOf(d.dialog, m.value);
				if (value === invalid) return;
				this.dialogs.delete(d.dialog.id);
				this.broadcast({ t: "closed", id: d.dialog.id });
				d.answer(value);
				return;
			}
			case "stop":
				this.act.stop();
				return;
			case "theme":
				if (m.theme !== "light" && m.theme !== "dark") return;
				this.theme = m.theme;
				this.act.theme(m.theme);
				this.broadcast({ t: "theme", theme: m.theme });
				return;
		}
	}

	private broadcast(msg: ToPage): void {
		for (const p of this.pages) p.send(msg);
	}
}

const invalid = Symbol("invalid");

/** answerOf checks a page's answer against its question: a confirm takes
 * true or false, an input or editor a text, an ask one of its options or,
 * with other, a text you typed. null cancels any of them. */
function answerOf(d: Dialog, v: unknown): unknown {
	if (v === null) return undefined;
	switch (d.kind) {
		case "confirm":
			return typeof v === "boolean" ? v : invalid;
		case "input":
		case "editor":
			return typeof v === "string" ? v : invalid;
		case "checklist":
			return Array.isArray(v) && v.length === (d.steps?.length ?? 0) && v.every((x) => x === "" || x === "pass" || x === "fail" || x === "skip") ? (v as Mark[]) : invalid;
		case "ask": {
			const a = v as { answer?: unknown; typed?: unknown } | undefined;
			if (typeof a?.answer !== "string") return invalid;
			if (a.typed === true) return d.other && a.answer.trim() ? { answer: a.answer.trim(), typed: true } : invalid;
			return d.options?.some((o) => o.label === a.answer) ? { answer: a.answer, typed: false } : invalid;
		}
	}
	return invalid;
}

function isImage(v: unknown): v is Image {
	const i = v as Image | undefined;
	return i?.type === "image" && typeof i.data === "string" && i.data.length > 0 && imageTypes.has(i.mimeType);
}
