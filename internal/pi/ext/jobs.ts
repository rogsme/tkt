// Background jobs: every slow tkt command runs here, so a tool returns at
// once. Jobs for one ticket run in order, because tkt's ticket lock would
// refuse a second one; different tickets run side by side.

import type { Envelope } from "./model.ts";

export interface Job {
	ticket: string;
	/** label says what the job does, for messages: "start", "review". */
	label: string;
	args: string[];
	/** then runs after the job, with its envelope. */
	then?: (env: Envelope) => void | Promise<void>;
	/** quiet keeps a failure off the board, for a job whose caller shows
	 * the result itself. */
	quiet?: boolean;
}

export type Runner = (args: string[], signal: AbortSignal) => Promise<Envelope>;

export class Jobs {
	private queues = new Map<string, Job[]>();
	private running = new Map<string, AbortController>();
	private run: Runner;
	private done: (job: Job, env: Envelope) => void;

	constructor(run: Runner, done: (job: Job, env: Envelope) => void) {
		this.run = run;
		this.done = done;
	}

	/** add queues a job; it starts at once when its ticket has none running. */
	add(job: Job): void {
		const q = this.queues.get(job.ticket) ?? [];
		q.push(job);
		this.queues.set(job.ticket, q);
		if (!this.running.has(job.ticket)) void this.next(job.ticket);
	}

	/** inFlight reports whether a job for the ticket runs or waits. */
	inFlight(ticket: string): boolean {
		return this.running.has(ticket) || (this.queues.get(ticket)?.length ?? 0) > 0;
	}

	/** tickets lists the tickets with a job running. */
	tickets(): string[] {
		return [...this.running.keys()];
	}

	/** stop drops the queues and cancels the running jobs. */
	stop(): void {
		this.queues.clear();
		for (const ac of this.running.values()) ac.abort();
		this.running.clear();
	}

	private async next(ticket: string): Promise<void> {
		const q = this.queues.get(ticket);
		const job = q?.shift();
		if (!job) {
			this.queues.delete(ticket);
			return;
		}
		const ac = new AbortController();
		this.running.set(ticket, ac);
		let env: Envelope;
		try {
			env = await this.run(job.args, ac.signal);
		} catch (err) {
			env = { ok: false, command: job.label, warnings: [], error: { code: "internal", exit: 1, message: String(err), hint: "", details: {} } };
		}
		if (ac.signal.aborted) return;
		this.running.delete(ticket);
		try {
			if (job.then) await job.then(env);
		} finally {
			this.done(job, env);
			void this.next(ticket);
		}
	}
}
