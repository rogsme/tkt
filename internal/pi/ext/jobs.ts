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

/** SyncResult is the part of tkt sync's result a notice needs. */
interface SyncResult {
	outcome: string;
	new_head: string;
	conflicts: string[] | null;
	failed: string;
}

/** doneText is the notice a job gives when it went through, so a button
 * you pressed says how it ended, or "" for a job that failed, since the
 * board shows that, or that reports its own result, as a quiet one's
 * caller does and a then does. */
export function doneText(job: Job, env: Envelope): string {
	if (!env.ok || job.quiet || job.then) return "";
	const t = job.ticket;
	switch (job.args[0]) {
		case "sync": {
			const r = env.result as SyncResult | undefined;
			const conflicts = r?.conflicts?.length ? `conflicts in ${r.conflicts.join(", ")}` : "a stop";
			switch (r?.outcome) {
				case "up_to_date":
					return `${t}: already up to date with its base.`;
				case "merged":
					return `${t}: merged its base and pushed ${r.new_head ? r.new_head.slice(0, 7) : "the merge"}.`;
				case "resolving":
					return `${t}: merging its base hit ${conflicts}; a fresh session resolves it in a split.`;
				case "fixing":
					return `${t}: the sync failed${r.failed ? `: ${r.failed}` : ""}; a fresh session fixes it in a split.`;
			}
			return `${t}: merging its base hit ${conflicts}.`;
		}
		case "start":
			return `${t}: started.`;
		case "resume":
			return `${t}: its newest session runs again.`;
		case "run":
			return `${t}: ${job.label} runs in a fresh session.`;
		case "chore":
			return `${t}: its run started.`;
		case "send":
			return `${t}: the agent got your message.`;
	}
	return `${t}: tkt ${job.args[0]} went through.`;
}

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
