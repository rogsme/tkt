// The hub keeps what the extension knows between events: the board's rows,
// the jobs in flight, one watcher per manual ticket, and what needs you.

import { spawn } from "node:child_process";
import { readFileSync } from "node:fs";
import { basename, join } from "node:path";
import type { ExtensionAPI, ExtensionContext, Theme } from "@earendil-works/pi-coding-agent";
import { type Component, isViewportTUI, type TUI, VStack } from "@earendil-works/pi-tui";
import { type Attention, attentionOf, sound, unblockedOf } from "./attention.ts";
import { notify } from "./dialog.ts";
import type { BoardState } from "./board.ts";
import { Jobs, type Job } from "./jobs.ts";
import { type Incident, idOf, idsIn, incidentsOf, keyFromTracker, keyOf, type Envelope, type LsResult, type Row, rowsOf, type Unblocked, type UpdateResult, updateNotice } from "./model.ts";
import { hud } from "./ui.ts";

/** pollMs is how often the board reads tkt ls while nothing else wakes it. */
const pollMs = 15_000;

/** lsTimeoutMs bounds one tkt ls, so a stuck read never freezes the board. */
const lsTimeoutMs = 60_000;

/** failedForMs is how long a failed job stays on the board. */
const failedForMs = 15 * 60_000;

/** updateEveryMs is how often a chat left open asks for a newer tkt. */
const updateEveryMs = 6 * 3_600_000;

export class Hub {
	readonly pi: ExtensionAPI;
	readonly bin = process.env.TKT_BIN || "tkt";
	readonly cwd = process.cwd();
	readonly repo = basename(process.cwd());
	readonly herdr = process.env.HERDR_ENV === "1";
	rows: Row[] = [];
	/** unblocked lists the tickets merges unblocked, which wait for a start. */
	unblocked: Unblocked[] = [];
	/** incidents is what the status pages report, as tkt ls last said. */
	incidents: Incident[] = [];
	key = "";
	/** items is what needs you now: one per ticket from tkt ls, and the
	 * failed jobs. */
	items: Attention[] = [];
	/** lastInput is what you last typed, for the guards on tools. */
	lastInput = "";
	/** newer is a newer tkt release that /update installs. */
	newer?: string;
	/** watching says whether tkt watch runs, and whether its passes fail,
	 * as tkt ls last said. */
	watching?: "on" | "off" | "failing";
	ctx?: ExtensionContext;
	render: () => void = () => {};
	/** changed tells the web pages that the board changed. */
	changed: () => void = () => {};
	readonly jobs: Jobs;
	readonly labels = new Map<string, string>();
	tick = 0;
	/** announced holds the keys already told about, so each one is told
	 * once; dismissed holds the ones you set aside. */
	private announced = new Set<string>();
	private dismissed = new Set<string>();
	/** told is the last boardNote the model got. */
	private told = "";
	private failed: { item: Attention; at: number }[] = [];
	private watchers = new Map<string, AbortController>();
	private timers: ReturnType<typeof setInterval>[] = [];
	private refreshing?: Promise<void>;
	private again = false;
	private stopped = false;
	private boardHidden = false;
	/** docked is the layout that holds the board, and pi's own inside it. */
	private docked?: { stack: Component; root: Component };

	constructor(pi: ExtensionAPI) {
		this.pi = pi;
		try {
			this.key = keyFromTracker(readFileSync(join(this.cwd, "docs/agents/issue-tracker.md"), "utf8"));
		} catch {
			// tkt agent checked the configuration; the board's ids fill in.
		}
		this.jobs = new Jobs(
			(args, signal) => this.tkt(args, signal),
			(job, env) => this.jobDone(job, env),
		);
	}

	/** tkt runs tkt with --json and returns its envelope; a run that prints
	 * no JSON becomes an internal error with what it printed. */
	async tkt<T = unknown>(args: string[], signal?: AbortSignal): Promise<Envelope<T>> {
		const r = await this.pi.exec(this.bin, [...args, "--json"], { signal, cwd: this.cwd });
		try {
			return JSON.parse(r.stdout) as Envelope<T>;
		} catch {
			const message = (r.stderr || r.stdout || `tkt exited ${r.code}`).trim().split("\n").slice(-3).join(" ");
			return { ok: false, command: args[0] ?? "", warnings: [], error: { code: r.killed ? "interrupted" : "internal", exit: r.code, message, hint: "", details: {} } };
		}
	}

	start(ctx: ExtensionContext): void {
		this.ctx = ctx;
		this.stopped = false;
		this.timers.push(setInterval(() => void this.refresh(), pollMs));
		this.timers.push(
			setInterval(() => {
				if (this.labels.size > 0) {
					this.tick++;
					this.render();
				}
			}, 250),
		);
		this.timers.push(setInterval(() => void this.checkUpdate(), updateEveryMs));
		void this.refresh();
		void this.checkUpdate();
	}

	/** checkUpdate asks tkt update for a newer release, puts it on the board,
	 * and says so once per release. A check that fails, such as without a
	 * network, shows nothing. It stays out of the jobs, so it never shows as
	 * a ticket's work. */
	async checkUpdate(): Promise<void> {
		const env = await this.tkt<UpdateResult>(["update", "--check"]);
		if (!env.ok || !env.result || this.stopped) return;
		const tag = updateNotice(env.result);
		if (tag && tag !== this.newer) notify(this.ctx, `tkt ${tag} is available (this tkt is ${env.result.from}): /update installs it`);
		this.newer = tag;
		this.redraw();
	}

	stop(): void {
		this.stopped = true;
		for (const t of this.timers) clearInterval(t);
		this.timers = [];
		this.jobs.stop();
		for (const ac of this.watchers.values()) ac.abort();
		this.watchers.clear();
	}

	/** pinBoard pins the board to the top of the chat and returns the widget
	 * the extension puts above the editor. In fullscreen mode, pi's own
	 * layout gets rows for the board above the transcript, so it covers
	 * nothing; the widget draws nothing and docks the board again when pi
	 * rebuilds its layout, as it does after you switch modes. In regular
	 * mode the terminal owns the scrollback, so the widget draws the board
	 * above the editor. The board takes no keys. */
	pinBoard(tui: TUI, theme: Theme): Component & { dispose(): void } {
		const view = hud(theme, () => this.state(), () => tui.terminal.rows);
		const board: Component = {
			render: (width) => (this.boardHidden ? [] : view.render(width)),
			invalidate() {},
		};
		this.render = () => tui.requestRender();
		this.dock(tui, board);
		return {
			render: (width) => {
				if (!isViewportTUI(tui)) return board.render(width);
				if (layoutRoot(tui) !== this.docked?.stack) queueMicrotask(() => this.dock(tui, board));
				return [];
			},
			invalidate() {},
			dispose: () => this.unpinBoard(tui),
		};
	}

	/** dock puts the board above pi's fullscreen layout. pi exposes the
	 * layout only to set it, so dock reads the one it set from the TUI. */
	private dock(tui: TUI, board: Component): void {
		if (!isViewportTUI(tui)) return;
		const root = layoutRoot(tui);
		if (!root || root === this.docked?.stack) return;
		const stack = new VStack([
			{ component: board, basis: "auto", grow: 0, shrink: 0, minSize: 0 },
			{ component: root, basis: 0, grow: 1, shrink: 1, minSize: 1 },
		]);
		this.docked = { stack, root };
		tui.setLayoutRoot(stack);
		releaseMouse(tui);
	}

	private unpinBoard(tui: TUI): void {
		if (this.docked && isViewportTUI(tui) && layoutRoot(tui) === this.docked.stack) tui.setLayoutRoot(this.docked.root);
		this.docked = undefined;
		this.render = () => {};
	}

	/** redraw draws the board again, in the terminal and on the pages. */
	redraw(): void {
		this.render();
		this.changed();
	}

	/** showBoard hides the pinned board, or shows it again. */
	showBoard(on: boolean): void {
		this.boardHidden = !on;
		this.redraw();
	}

	/** state is what the board draws. */
	state(): BoardState {
		return { rows: this.rows, jobs: this.labels, items: this.items, now: Date.now(), tick: this.tick, repo: this.repo, update: this.newer, watch: this.watching, incidents: this.incidents };
	}

	/** refresh reads tkt ls, works out what needs you, and keeps one watcher
	 * per ticket whose agent runs without a tkt auto loop. Calls that arrive
	 * during a refresh run it once more after. */
	refresh(): Promise<void> {
		if (this.refreshing) {
			this.again = true;
			return this.refreshing;
		}
		this.refreshing = (async () => {
			do {
				this.again = false;
				await this.read();
			} while (this.again && !this.stopped);
		})().finally(() => {
			this.refreshing = undefined;
		});
		return this.refreshing;
	}

	private async read(): Promise<void> {
		const env = await this.tkt<LsResult>(["ls", "--no-fetch"], AbortSignal.timeout(lsTimeoutMs));
		const rows = rowsOf(env);
		if (!rows || this.stopped) return;
		this.rows = rows;
		this.unblocked = (env.ok ? env.result?.unblocked : undefined) ?? [];
		this.incidents = incidentsOf(env);
		const w = env.ok ? env.result?.watch : undefined;
		if (w) this.watching = !w.running ? "off" : w.error ? "failing" : "on";
		// A chore's run is named by its branch, which may look like an id.
		this.key = keyOf(rows.filter((r) => !r.chore).map((r) => r.ticket)) || this.key;
		this.update();
		this.watch(rows);
	}

	/** update works the items out again from the rows, tells about the new
	 * ones once, and redraws. An item goes as soon as its ticket moves on. */
	private update(): void {
		const now = Date.now();
		this.failed = this.failed.filter((f) => now - f.at < failedForMs && !this.dismissed.has(f.item.key));
		const items: Attention[] = [];
		for (const row of this.rows) {
			// A watch whose passes fail takes no step, so the steps wait for you.
			const a = attentionOf(row, this.jobs.inFlight(row.ticket), this.watching === "on");
			if (a && !this.dismissed.has(a.key)) items.push(a);
		}
		for (const u of this.unblocked) {
			const a = unblockedOf(u);
			if (!this.jobs.inFlight(u.ticket) && !this.dismissed.has(a.key)) items.push(a);
		}
		const all = [...items, ...this.failed.map((f) => f.item)];
		for (const a of all) {
			if (!this.announced.has(a.key)) this.announce(a);
		}
		// A blocked ticket is announced once, then waits in the Blocked
		// column.
		this.items = all.filter((a) => !a.parked);
		this.status();
		this.redraw();
	}

	/** announce tells about an item once with a herdr notification, unless
	 * the tkt auto loop that drives the ticket sent one already. The chat
	 * itself stays clean: the board shows the item until it is done, and the
	 * model reads it in boardNote. */
	private announce(a: Attention): void {
		this.announced.add(a.key);
		if (!a.byAuto) this.toast(`${a.ticket}: ${a.title}`, a.text, sound(a));
	}

	/** boardNote is what needs you now, for the model with your next
	 * message, or undefined when that has not changed since the last note.
	 * It describes the items as they stand, so the model never reads one
	 * that went away while you were not typing. */
	boardNote(): string | undefined {
		let note = this.items.map((a) => `[board] ${a.text}`).join("\n");
		if (!note && this.told) note = "[board] Nothing needs the user now.";
		if (note === this.told) return undefined;
		this.told = note;
		return note;
	}

	private status(): void {
		if (!this.ctx?.hasUI) return;
		const n = new Set(this.items.map((a) => a.ticket)).size;
		this.ctx.ui.setStatus("tkt", n ? `tkt: ${n} need${n === 1 ? "s" : ""} you (/inbox)` : undefined);
	}

	/** post puts a line in the chat for you, such as an answer to a slash
	 * command, without starting a model turn. While the model works, the line
	 * shows as a note and reaches the model with the next turn. */
	post(text: string, details?: unknown): void {
		const ctx = this.ctx;
		if (ctx && !ctx.isIdle()) {
			notify(ctx, text);
			this.pi.sendMessage({ customType: "tkt", content: text, display: false, details }, { deliverAs: "nextTurn" });
			return;
		}
		this.pi.sendMessage({ customType: "tkt", content: text, display: true, details }, { triggerTurn: false });
	}

	/** toast shows a herdr notification. */
	toast(title: string, body: string, sound: "done" | "request"): void {
		if (!this.herdr) return;
		void this.pi.exec("herdr", ["notification", "show", title, "--body", body, "--sound", sound], { cwd: this.cwd });
	}

	/** add runs a job in the background and says so on the board. */
	add(job: Job): void {
		this.labels.set(job.ticket, job.label);
		this.jobs.add(job);
		this.redraw();
	}

	/** run runs a job in its ticket's queue and waits for its envelope, for
	 * a quick step whose result the answer needs. */
	run(job: Job): Promise<Envelope> {
		return new Promise((resolve) => {
			this.add({
				...job,
				then: async (env) => {
					try {
						if (job.then) await job.then(env);
					} finally {
						resolve(env);
					}
				},
			});
		});
	}

	private jobDone(job: Job, env: Envelope): void {
		if (!this.jobs.inFlight(job.ticket)) this.labels.delete(job.ticket);
		if (!env.ok && env.error && !job.quiet) {
			const text = `${job.ticket}: ${job.label} failed: ${reasonOf(env)}`;
			this.failed.push({ item: { key: `failed|${job.ticket}|${Date.now()}`, ticket: job.ticket, kind: "failed", title: `${job.label} failed`, text, byAuto: false }, at: Date.now() });
		}
		void this.refresh();
	}

	/** startAuto starts a tkt auto loop that outlives this chat, so a ticket
	 * in auto keeps moving after you close it. The loop notifies through
	 * herdr when it pauses for you or ends; the board reads it from tkt ls. */
	startAuto(ticket: string, approvePlan: boolean, as?: string, yolo = false): void {
		const args = ["auto", ticket, "--notify"];
		if (yolo) args.push("--yolo");
		else if (approvePlan) args.push("--auto-plan");
		if (as) args.push("--as", as);
		const child = spawn(this.bin, args, { cwd: this.cwd, detached: true, stdio: "ignore" });
		child.on("error", (err) => {
			this.failed.push({ item: { key: `failed|${ticket}|${Date.now()}`, ticket, kind: "failed", title: "auto failed", text: `${ticket}: tkt auto did not start: ${err.message}`, byAuto: false }, at: Date.now() });
			this.update();
		});
		child.unref();
		setTimeout(() => void this.refresh(), 2000);
	}

	/** watch keeps one waiter per ticket whose agent runs without tkt auto:
	 * it blocks until the agent stops, refreshes the board, then blocks until
	 * it works again. Each wait blocks inside herdr, so nothing polls. */
	private watch(rows: Row[]): void {
		const want = new Set(rows.filter((r) => r.agent && !r.auto?.running).map((r) => r.ticket));
		for (const [id, ac] of this.watchers) {
			if (!want.has(id)) {
				ac.abort();
				this.watchers.delete(id);
			}
		}
		for (const id of want) {
			if (this.watchers.has(id)) continue;
			const ac = new AbortController();
			this.watchers.set(id, ac);
			void this.waitLoop(id, ac.signal).finally(() => {
				if (this.watchers.get(id) === ac) this.watchers.delete(id);
			});
		}
	}

	private async waitLoop(id: string, signal: AbortSignal): Promise<void> {
		while (!signal.aborted) {
			const settled = await this.tkt(["wait", id, "--until", "idle,blocked,done"], signal);
			if (!settled.ok || signal.aborted) return;
			await this.refresh();
			const working = await this.tkt(["wait", id, "--until", "working"], signal);
			if (!working.ok || signal.aborted) return;
			await this.refresh();
		}
	}

	row(id: string): Row | undefined {
		return this.rows.find((r) => r.ticket === id);
	}

	/** idOf turns what you typed into an id: one on the board, a ticket's,
	 * or a chore run's as typed. */
	idOf(raw: string): string {
		return idOf(raw, this.rows, this.key);
	}

	/** idsIn reads the ids in what you typed: tickets and their ranges, and
	 * chore runs by their branch. */
	idsIn(text: string): string[] {
		return idsIn(text, this.rows, this.key);
	}

	/** dismiss sets an item aside until its ticket's situation changes. */
	dismiss(a: Attention): void {
		this.dismissed.add(a.key);
		this.update();
	}

	/** dismissTicket sets aside what a ticket needs from you now, and says
	 * how many items that was. */
	dismissTicket(id: string): number {
		const set = this.items.filter((a) => a.ticket === id);
		for (const a of set) this.dismissed.add(a.key);
		if (set.length) this.update();
		return set.length;
	}
}

/** mouseOff stops the reports that pi's fullscreen mode turns on: clicks,
 * drags, motion, their SGR encoding and focus. */
const mouseOff = "\x1b[?1006l\x1b[?1004l\x1b[?1003l\x1b[?1002l\x1b[?1000l";

/** releaseMouse gives the mouse back to the terminal, so its own selection
 * and copy work in the chat as in any other program. pi's fullscreen mode
 * takes every click and drag for a selection of its own and has no setting
 * to stop. With mouseEnabled false the TUI sends no mouse sequences when it
 * comes back from an editor or a suspend, nor as it quits. The wheel goes
 * with the mouse; PageUp and PageDown scroll. */
function releaseMouse(tui: TUI): void {
	const t = tui as unknown as { mouseEnabled?: boolean };
	if (!t.mouseEnabled) return;
	t.mouseEnabled = false;
	tui.terminal.write(mouseOff);
}

/** layoutRoot is the fullscreen layout the TUI draws. */
function layoutRoot(tui: TUI): Component | undefined {
	return (tui as unknown as { layoutRoot?: Component }).layoutRoot;
}

/** reasonOf says why tkt refused, with the reasons tkt done keeps a
 * worktree for, and the next step. */
export function reasonOf(env: Envelope): string {
	const e = env.error;
	if (!e) return "tkt failed.";
	const hint = e.hint && !e.message.includes(e.hint) ? ` Next: ${e.hint}.` : "";
	return `${e.message.replace(/[.\s]+$/, "")}.${hint}`;
}
