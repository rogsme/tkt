// The mirror keeps the web pages in step with the chat: the items they
// draw, the answer streaming in, whether the model works, the questions
// waiting for an answer, and the notices. A page that opens late gets all
// of it at once. It imports nothing from pi or Bun, so node --test covers
// it; web.ts feeds it the chat's events and carries its messages.

import { type Item, patch } from "./chat.ts";

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
	kind: "ask" | "confirm" | "input" | "editor";
	title: string;
	message?: string;
	options?: Option[];
	other?: boolean;
	placeholder?: string;
	prefill?: string;
}

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
	| { t: "hello"; repo: string; theme: Theme; busy: boolean; items: Item[]; live: Item[]; dialogs: Dialog[]; commands: Command[] }
	| { t: "items"; from: number; items: Item[] }
	| { t: "live"; items: Item[] }
	| { t: "busy"; busy: boolean }
	| { t: "dialog"; dialog: Dialog }
	| { t: "closed"; id: string }
	| { t: "notify"; text: string; level: Level }
	| { t: "theme"; theme: Theme };

export interface Page {
	send(msg: ToPage): void;
}

/** Actions are what a page asks of the chat. */
export interface Actions {
	send(text: string, images: Image[]): void;
	stop(): void;
	theme(theme: Theme): void;
	commands(): Command[];
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
		page.send({ t: "hello", repo: this.repo, theme: this.theme, busy: this.busy, items: this.items, live: this.live, dialogs: [...this.dialogs.values()].map((d) => d.dialog), commands: this.act.commands() });
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
	receive(raw: unknown): void {
		if (!raw || typeof raw !== "object") return;
		const m = raw as Record<string, unknown>;
		switch (m.t) {
			case "send": {
				const text = typeof m.text === "string" ? m.text : "";
				const images = Array.isArray(m.images) ? m.images.filter(isImage).slice(0, maxImages) : [];
				if (text.trim() || images.length) this.act.send(text, images);
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
