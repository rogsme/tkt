// The hub keeps what the extension knows between events: the board's rows,
// the jobs in flight, one watcher per manual ticket, and what needs you.

import { spawn } from "node:child_process";
import { readFileSync } from "node:fs";
import { basename, join } from "node:path";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { type Attention, attentionOf, sound } from "./attention.ts";
import { Jobs, type Job } from "./jobs.ts";
import { keyFromTracker, keyOf, type Envelope, type LsResult, type Row, rowsOf } from "./model.ts";

/** pollMs is how often the board reads tkt ls while nothing else wakes it. */
const pollMs = 30_000;

export class Hub {
	readonly pi: ExtensionAPI;
	readonly bin = process.env.TKT_BIN || "tkt";
	readonly cwd = process.cwd();
	readonly repo = basename(process.cwd());
	readonly herdr = process.env.HERDR_ENV === "1";
	rows: Row[] = [];
	key = "";
	inbox: Attention[] = [];
	/** lastInput is what you last typed, for the guards on tools. */
	lastInput = "";
	ctx?: ExtensionContext;
	render: () => void = () => {};
	readonly jobs: Jobs;
	readonly labels = new Map<string, string>();
	tick = 0;
	private raised = new Set<string>();
	private watchers = new Map<string, AbortController>();
	private timers: ReturnType<typeof setInterval>[] = [];
	private refreshing?: Promise<void>;
	private again = false;
	private stopped = false;

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
		void this.refresh();
	}

	stop(): void {
		this.stopped = true;
		for (const t of this.timers) clearInterval(t);
		this.timers = [];
		this.jobs.stop();
		for (const ac of this.watchers.values()) ac.abort();
		this.watchers.clear();
	}

	/** refresh reads tkt ls, raises what newly needs you, and keeps one
	 * watcher per ticket whose agent runs without a tkt auto loop. Calls that
	 * arrive during a refresh run it once more after. */
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
		const env = await this.tkt<LsResult>(["ls", "--no-fetch"]);
		const rows = rowsOf(env);
		if (!rows || this.stopped) return;
		this.rows = rows;
		this.key = keyOf(rows.map((r) => r.ticket)) || this.key;
		const now = new Map<string, Attention>();
		for (const row of rows) {
			const a = attentionOf(row, this.jobs.inFlight(row.ticket));
			if (!a) continue;
			now.set(row.ticket, a);
			if (!this.raised.has(a.key)) this.raise(a);
		}
		// An item goes once its ticket moved on; a failed job stays until
		// you dismiss it.
		this.inbox = this.inbox.filter((a) => a.kind === "failed" || now.get(a.ticket)?.key === a.key);
		this.watch(rows);
		this.render();
	}

	/** needsYou lists the tickets with an item in the inbox. */
	needsYou(): Set<string> {
		return new Set(this.inbox.map((a) => a.ticket));
	}

	raise(a: Attention): void {
		this.raised.add(a.key);
		this.inbox = this.inbox.filter((x) => x.ticket !== a.ticket || x.kind === "failed");
		this.inbox.push(a);
		this.post(a.text, a);
		if (!a.byAuto) this.toast(`${a.ticket}: ${a.title}`, a.text, sound(a));
		this.render();
	}

	/** post puts a line in the chat without starting a model turn. While the
	 * model works, the line shows as a notice and reaches the model with the
	 * next turn. */
	post(text: string, details?: unknown): void {
		const ctx = this.ctx;
		if (ctx && !ctx.isIdle()) {
			if (ctx.hasUI) ctx.ui.notify(text, "info");
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
		this.render();
	}

	private jobDone(job: Job, env: Envelope): void {
		if (!this.jobs.inFlight(job.ticket)) this.labels.delete(job.ticket);
		if (!env.ok && env.error) {
			const e = env.error;
			const hint = e.hint && !e.message.includes(e.hint) ? ` Next: ${e.hint}.` : "";
			const text = `${job.ticket}: ${job.label} failed: ${e.message.replace(/[.\s]+$/, "")}.${hint}`;
			this.raise({ key: `failed|${job.ticket}|${Date.now()}`, ticket: job.ticket, kind: "failed", title: `${job.label} failed`, text, byAuto: false });
		}
		void this.refresh();
	}

	/** startAuto starts a tkt auto loop that outlives this chat, so a ticket
	 * in auto mode keeps moving after you close it. The loop notifies
	 * through herdr when it stops; the board picks it up from tkt ls. */
	startAuto(ticket: string, approvePlan: boolean): void {
		const args = ["auto", ticket, "--notify"];
		if (approvePlan) args.push("--approve-plan");
		const child = spawn(this.bin, args, { cwd: this.cwd, detached: true, stdio: "ignore" });
		child.on("error", (err) => this.raise({ key: `failed|${ticket}|${Date.now()}`, ticket, kind: "failed", title: "auto failed", text: `${ticket}: tkt auto did not start: ${err.message}`, byAuto: false }));
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

	/** dismiss takes an item out of the inbox. */
	dismiss(a: Attention): void {
		this.inbox = this.inbox.filter((x) => x !== a);
		this.render();
	}
}
