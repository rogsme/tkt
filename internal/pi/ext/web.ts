// The web page: a server inside the chat, on every address so other
// devices on your network reach it, that serves the page under web/ and
// carries the mirror's messages over a WebSocket (serve.ts). The page
// reads its views, such as a ticket's diff, through queries that run tkt
// with --json and change nothing; every change it makes is a slash command.
// The first chat in a repository serves it and names its pid, port and
// token in the file TKT_WEB_FILE names, where tkt web finds them.
// A chat that comes back after one quit takes the same port and token, so
// an open page reconnects by itself.

import { randomBytes } from "node:crypto";
import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { marksAt } from "./actions.ts";
import { type Entry, type Item, itemsOf, messageItems, pageView, slashName, terminalOnly } from "./chat.ts";
import { mirrorTo, notify } from "./dialog.ts";
import type { Hub } from "./hub.ts";
import { type Board, type Command, type Image, Mirror, type Page, type Query, type Theme } from "./mirror.ts";
import type { Envelope } from "./model.ts";
import { type BunServer, servePage } from "./serve.ts";

/** takeOverMs is how often a chat that serves no page checks whether the
 * one that serves it is still there. */
const takeOverMs = 5000;

/** streamMs paces the answer streaming in, so a fast model does not send a
 * page a message per token. */
const streamMs = 50;

export class Web {
	readonly mirror: Mirror;
	/** why says why this chat serves no page, or "" while it serves one;
	 * other is set when another chat serves it. */
	why = "";
	other = false;
	private readonly pi: ExtensionAPI;
	private readonly hub: Hub;
	private readonly file = process.env.TKT_WEB_FILE ?? "";
	private ctx?: ExtensionContext;
	private server?: BunServer;
	private token = "";
	/** here holds the pages open in a browser on this machine, so tkt web
	 * opens no second one. */
	private readonly here = new Set<Page>();
	private poll?: ReturnType<typeof setInterval>;
	/** watching checks, while another chat serves the page, whether it
	 * still does, so this one takes the page over once it quits. */
	private watching?: ReturnType<typeof setInterval>;
	private claiming = false;
	private streaming?: { items: Item[]; timer: ReturnType<typeof setTimeout> };

	constructor(pi: ExtensionAPI, hub: Hub) {
		this.pi = pi;
		this.hub = hub;
		this.mirror = new Mirror(hub.repo, {
			send: (text, images, page) => this.send(text, images, page),
			stop: () => this.ctx?.abort(),
			theme: (t) => saveTheme(t),
			commands: () => this.commands(),
			query: (q) => this.query(q),
		});
		this.mirror.theme = loadTheme();
		hub.changed = () => {
			if (this.server) this.mirror.setBoard(this.board());
		};
		pi.on("message_update", (e) => {
			if (e.message.role === "assistant") this.stream(messageItems(e.message, "live"));
		});
		pi.on("message_end", () => {
			this.stream([]);
			this.sync();
		});
		pi.on("agent_start", () => this.mirror.setBusy(true));
		pi.on("agent_end", () => {
			this.mirror.setBusy(false);
			this.sync();
		});
	}

	/** start serves the page for a session, unless another chat serves it.
	 * first is the chat's first session, the only one that says so. */
	async start(ctx: ExtensionContext, first: boolean): Promise<void> {
		this.ctx = ctx;
		if (this.claiming) return;
		this.claiming = true;
		try {
			await this.open(ctx, first);
		} finally {
			this.claiming = false;
		}
		clearInterval(this.watching);
		this.watching = undefined;
		if (this.other) this.watching = setInterval(() => void this.takeOver(), takeOverMs);
	}

	/** takeOver serves the page from this chat when the chat that served
	 * it is gone, so its open pages come back here and /web opens this
	 * chat's page rather than a new chat in another tab. */
	async takeOver(): Promise<void> {
		if (this.server || !this.other || !this.ctx) return;
		await this.start(this.ctx, false);
		if (this.server) notify(this.ctx, "The chat that served the web page quit, so this chat serves it now.");
	}

	private async open(ctx: ExtensionContext, first: boolean): Promise<void> {
		const claim = await this.claim();
		if (typeof claim === "string") {
			this.why = claim;
			if (first && this.other) notify(ctx, `${claim}, so this chat serves none. /web opens that one.`);
			return;
		}
		this.token = claim.token;
		try {
			this.serve(claim.port);
		} catch {
			// Another chat that saw the same chat quit took the page first.
			if (await answers(claim.port, claim.token)) {
				this.other = true;
				this.why = "Another chat serves this repository's web page";
				return;
			}
			// The port went to another program while no chat served it.
			try {
				this.serve(0);
			} catch (err) {
				this.why = `the server did not start: ${(err as Error).message}`;
				notify(ctx, `This chat serves no web page: ${this.why}.`, "warning");
				return;
			}
		}
		try {
			writeState(this.file, { pid: String(process.pid), port: String(this.server!.port), token: this.token });
		} catch (err) {
			this.why = `cannot write ${this.file}: ${(err as Error).message}`;
			notify(ctx, `This chat serves no web page: ${this.why}.`, "warning");
			this.server?.stop(true);
			this.server = undefined;
			return;
		}
		this.why = "";
		mirrorTo(this.mirror);
		this.mirror.setBusy(!ctx.isIdle());
		this.mirror.reset(this.items());
		this.mirror.setBoard(this.board());
		this.poll = setInterval(() => {
			if (this.mirror.pages.size > 0) this.sync();
		}, 1000);
	}

	/** stop closes the server as a session ends. A chat that quits takes
	 * its pid out of the file and leaves the port and token, so the next
	 * chat serves the page where an open one looks for it. One that moves to
	 * another session serves the next session on the same port. */
	stop(quit: boolean): void {
		clearInterval(this.poll);
		clearInterval(this.watching);
		this.watching = undefined;
		if (this.streaming) clearTimeout(this.streaming.timer);
		this.streaming = undefined;
		mirrorTo(undefined);
		if (!this.server) return;
		const port = String(this.server.port);
		this.server.stop(true);
		this.server = undefined;
		this.here.clear();
		if (!quit || readState(this.file).pid !== String(process.pid)) return;
		try {
			writeState(this.file, { port, token: this.token });
		} catch {
			// tkt web asks the port before it trusts the file.
		}
	}

	/** claim decides whether this chat serves the page, and on which port
	 * and token: the ones a chat that ended left, so an open page comes
	 * back. It returns why not when it does not. */
	private async claim(): Promise<{ port: number; token: string } | string> {
		this.other = false;
		if (!this.file) return "tkt agent did not start this chat";
		const st = readState(this.file);
		const pid = Number(st.pid);
		if (pid && pid !== process.pid && alive(pid) && (await answers(Number(st.port), st.token))) {
			this.other = true;
			return `The chat in process ${pid} serves this repository's web page`;
		}
		const token = /^[0-9a-f]{48}$/.test(st.token ?? "") ? st.token : randomBytes(24).toString("hex");
		return { port: Number(st.port) || 0, token };
	}

	private serve(port: number): void {
		this.server = servePage(port, {
			mirror: this.mirror,
			token: this.token,
			hostname: "0.0.0.0",
			health: () => ({ file: this.file, repo: this.hub.repo, here: this.here.size }),
			joined: (page, here) => {
				if (here) this.here.add(page);
			},
			left: (page) => this.here.delete(page),
		});
	}

	/** send takes what you typed on a page: one of tkt's slash commands
	 * runs as if typed in the terminal, except the ones that open a view,
	 * which open the page's own; pi's own commands open screens only the
	 * terminal draws; anything else goes to the model, as text typed while
	 * it works does. */
	private send(text: string, images: Image[], page: Page): void {
		const ctx = this.ctx;
		if (!ctx) return;
		const name = slashName(text);
		if (name && images.length === 0) {
			const view = pageView(text, (raw) => this.hub.idOf(raw));
			if (view) {
				page.send({ t: "open", ...view });
				return;
			}
			if (this.commands().some((c) => c.name === name)) {
				this.pi.sendUserMessage(text.trim(), ctx.isIdle() ? { expandPromptTemplates: true } : { expandPromptTemplates: true, deliverAs: "steer" });
				return;
			}
			if (terminalOnly.has(name)) {
				this.mirror.notify(`/${name} opens one of pi's own screens, which only the terminal draws: type it in the chat's pane.`, "warning");
				return;
			}
		}
		this.hub.lastInput = text;
		const content = images.length > 0 ? [{ type: "text" as const, text }, ...images] : text;
		if (ctx.isIdle()) this.pi.sendUserMessage(content);
		else this.pi.sendUserMessage(content, { deliverAs: "steer" });
	}

	/** query reads a view for a page: tkt with --json, which changes
	 * nothing. The checklist's marks come with the steps. */
	private async query(q: Query): Promise<unknown> {
		const id = q.ticket ? this.hub.idOf(q.ticket) : "";
		switch (q.what) {
			case "message":
				return this.hub.tkt(["show", id]);
			case "screen":
				return this.hub.tkt(["show", id, "--screen", "--ansi"]);
			case "diff":
				return this.hub.tkt(["show", id, "--diff"]);
			case "steps": {
				const env = (await this.hub.tkt(["show", id, "--steps"])) as Envelope<Record<string, unknown>>;
				const head = this.hub.row(id)?.pr?.head ?? "";
				return env.ok && env.result ? { ...env, result: { ...env.result, head, marks: marksAt(id, head) } } : env;
			}
			case "history":
				return this.hub.tkt(["history", id]);
			case "stats":
				return this.hub.tkt(["stats"]);
			case "chores":
				return this.hub.tkt(["chores"]);
			case "suggest":
				return this.hub.tkt(["suggest", "--show"]);
			case "version":
				return { ok: true, result: { tkt: process.env.TKT_VERSION ?? "", pi: process.env.TKT_PI_VERSION ?? "", bin: this.hub.bin, update: this.hub.newer ?? "" } };
		}
	}

	private board(): Board {
		const h = this.hub;
		return { rows: h.rows, items: h.items, jobs: Object.fromEntries(h.labels), incidents: h.incidents, watch: h.watching, update: h.newer };
	}

	private commands(): Command[] {
		return this.pi
			.getCommands()
			.filter((c) => c.source === "extension")
			.map((c) => ({ name: c.name, description: c.description ?? "" }));
	}

	private items(): Item[] {
		return this.ctx ? itemsOf(this.ctx.sessionManager.getBranch() as Entry[], Date.now()) : [];
	}

	/** sync sends the pages what changed in the session. The session is the
	 * one record of the chat: what the terminal draws, the pages draw. */
	private sync(): void {
		if (this.server) this.mirror.setItems(this.items());
	}

	private stream(items: Item[]): void {
		if (!this.server) return;
		if (items.length === 0) {
			if (this.streaming) clearTimeout(this.streaming.timer);
			this.streaming = undefined;
			this.mirror.setLive([]);
			return;
		}
		if (this.streaming) {
			this.streaming.items = items;
			return;
		}
		this.streaming = {
			items,
			timer: setTimeout(() => {
				const s = this.streaming;
				this.streaming = undefined;
				if (s) this.mirror.setLive(s.items);
			}, streamMs),
		};
	}
}

/** readState reads a key=value file, as tkt's state files are. */
function readState(file: string): Record<string, string> {
	const st: Record<string, string> = {};
	try {
		for (const line of readFileSync(file, "utf8").split("\n")) {
			const i = line.indexOf("=");
			if (i > 0) st[line.slice(0, i)] = line.slice(i + 1);
		}
	} catch {
		// No file: no chat serves the page.
	}
	return st;
}

/** writeState writes a key=value file whole, readable by you alone: it
 * holds the token. */
function writeState(file: string, st: Record<string, string>): void {
	mkdirSync(dirname(file), { recursive: true });
	const tmp = `${file}.${process.pid}`;
	const body = Object.keys(st)
		.sort()
		.map((k) => `${k}=${st[k]}\n`)
		.join("");
	writeFileSync(tmp, body, { mode: 0o600 });
	renameSync(tmp, file);
}

function alive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch (err) {
		return (err as NodeJS.ErrnoException).code === "EPERM";
	}
}

/** answers asks a page's server whether it still serves the page. */
async function answers(port: number, token: string | undefined): Promise<boolean> {
	if (!port || !token) return false;
	try {
		const r = await fetch(`http://127.0.0.1:${port}/health`, { headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(1000) });
		return r.ok;
	} catch {
		return false;
	}
}

/** themeFile keeps the light or dark choice for every repository's page. */
function themeFile(): string {
	return join(process.env.XDG_STATE_HOME || join(homedir(), ".local", "state"), "tkt", "web.json");
}

function loadTheme(): Theme {
	try {
		const t = (JSON.parse(readFileSync(themeFile(), "utf8")) as { theme?: unknown }).theme;
		return t === "light" || t === "dark" ? t : "";
	} catch {
		return "";
	}
}

function saveTheme(theme: Theme): void {
	try {
		mkdirSync(dirname(themeFile()), { recursive: true });
		writeFileSync(themeFile(), `${JSON.stringify({ theme })}\n`);
	} catch {
		// The page keeps the choice until it reloads.
	}
}
