// What tkt prints with --json, and small helpers shared by the extension and
// its tests. Nothing here imports pi, so `node --test` can load it.

export interface Envelope<T = unknown> {
	ok: boolean;
	command: string;
	result?: T;
	error?: { code: string; exit: number; message: string; hint: string; details: Record<string, unknown> };
	warnings: string[];
}

export interface Next {
	kind: "command" | "wait" | "human" | "unknown";
	reason: string;
	argv: string[] | null;
	text: string;
}

export interface Row {
	ticket: string;
	path: string;
	/** background counts the tasks an idle agent waits on, such as a CI
	 * watcher; it goes on by itself when they finish. */
	agent: { pane_id: string; name: string; harness: string; status: string; background?: number } | null;
	last_status: string;
	pr: { number: number; state: string; mergeable?: string; head?: string } | null;
	checks: { state: string; total: number; failing: string[]; pending: number } | null;
	review: { verdict: string; head: string; at_head: boolean } | null;
	/** behind counts the base's commits the branch lacks, or null. */
	behind?: number | null;
	next: Next;
	stage: string;
	launch: string;
	/** paused says why a running tkt auto loop waits for you, why names
	 * the rule behind it, and note what failed, such as a sync's error; all
	 * are empty while the loop drives the ticket. */
	auto: { wanted: boolean; approve_plan: boolean; running: boolean; rounds: number; stop: string; waiting: string; paused?: string; why?: string; note?: string } | null;
	/** hold says tkt watch and tkt auto leave the ticket alone (tkt hold). */
	hold?: boolean;
	/** watch is what tkt watch did: last is its last step, such as
	 * run:ci@HEAD; paused says why it waits for you, round_limit or
	 * no_progress. */
	watch?: { last: string; paused: string } | null;
	/** merge is the merge order tkt merge left for the current head while
	 * its checks ran: tkt watch hands the steps to finish-ticket once they
	 * pass. */
	merge?: { head: string; steps: number[]; at: string } | null;
	phase: string;
	phase_since: string;
	started: string;
	idle_since: string;
	stale: boolean;
}

export interface LsResult {
	sources: { herdr: string; forge: string; fetch: string };
	/** watch says whether tkt watch keeps the repository's tickets moving. */
	watch?: { running: boolean; last_pass: string; error: string };
	rows: Row[];
}

/** rowsOf takes tkt ls's rows from a result, or from the partial result an
 * error carries when the forge failed. */
export function rowsOf(env: Envelope<LsResult>): Row[] | undefined {
	if (env.ok && env.result) return env.result.rows;
	const rows = env.error?.details?.rows;
	return Array.isArray(rows) ? (rows as Row[]) : undefined;
}

/** UpdateResult is what tkt update prints. dev says tkt is a development
 * build, which only --force replaces. */
export interface UpdateResult {
	from: string;
	to: string;
	path: string;
	available: boolean;
	dev: boolean;
	updated: boolean;
	next?: string;
}

/** updateNotice returns the release /update would install, or undefined
 * when there is none to suggest. A development build gets no suggestion:
 * /update does not replace it. */
export function updateNotice(r: UpdateResult): string | undefined {
	return r.available && !r.dev && !r.updated ? r.to : undefined;
}

/** stepText says what a step of tkt auto does, as tkt auto --once reports
 * it; a sync step has no stage. */
export function stepText(did: { action: string; stage?: string }): string {
	switch (did.action) {
		case "approve":
			return "approving the plan";
		case "start":
			return "starting";
		case "sync":
			return "merging the base";
	}
	return `running ${did.stage ?? did.action}`;
}

/** MergeResult is what tkt merge prints: merging (finish-ticket got the
 * merge line), ordered (tkt watch sends it once the checks pass) or
 * cancelled. */
export interface MergeResult {
	ticket: string;
	pr: number;
	head: string;
	steps: number[];
	action: "merging" | "ordered" | "cancelled";
}

/** mergeText says what tkt merge did, for the chat. */
export function mergeText(r: MergeResult): string {
	if (r.action === "ordered") return `${r.ticket} merges once its checks pass: tkt watch sends finish-ticket the merge line then. The board shows it as merges when green.`;
	if (r.action === "cancelled") return `${r.ticket} has no merge order now.`;
	return `${r.ticket}: finish-ticket merges pull request #${r.pr} in a fresh session in its tab. Once it ends with Status: DONE, tkt watch removes the ticket, or /done ${r.ticket} does.`;
}

/** ago renders milliseconds for people: 45s, 12m, 3h, 2d. */
export function ago(ms: number): string {
	const s = Math.max(0, Math.floor(ms / 1000));
	if (s < 60) return `${s}s`;
	if (s < 3600) return `${Math.floor(s / 60)}m`;
	if (s < 48 * 3600) return `${Math.floor(s / 3600)}h`;
	return `${Math.floor(s / 86400)}d`;
}

/** Stamp is a time in the chat: when a prompt reached the model, or, with
 * took, when the answer was done and how long the run took. */
export interface Stamp {
	at: number;
	took?: number;
}

/** stampText renders a stamp in local time, with the date when it falls on
 * another day than now, as in a chat resumed later. */
export function stampText(s: Stamp, now: number): string {
	const d = new Date(s.at);
	let at = [d.getHours(), d.getMinutes(), d.getSeconds()].map((n) => String(n).padStart(2, "0")).join(":");
	if (d.toDateString() !== new Date(now).toDateString()) at = `${d.toLocaleDateString("en-US", { month: "short", day: "numeric" })} ${at}`;
	return s.took === undefined ? at : `${at}  ${ago(s.took)}`;
}

/** since returns how long ago an RFC 3339 time was, or "" when unknown. */
export function since(at: string, now: number): string {
	const t = Date.parse(at);
	return Number.isNaN(t) ? "" : ago(now - t);
}

/** normalizeId turns "acm-12", "ACM 12" or "12" into "ACM-12", given the
 * tracker's key; anything else comes back as it is, upper-cased. */
export function normalizeId(raw: string, key: string): string {
	const s = raw.trim().toUpperCase();
	const m = s.match(/^([A-Z][A-Z0-9]*)?[-_ ]?(\d+)$/);
	if (m && key && (!m[1] || m[1] === key)) return `${key}-${Number(m[2])}`;
	return s;
}

/** keyOf finds the tracker key that ticket ids share, such as ACM. */
export function keyOf(ids: string[]): string {
	for (const id of ids) {
		const m = id.match(/^([A-Z][A-Z0-9]*)-\d+$/);
		if (m) return m[1];
	}
	return "";
}

/** keyFromTracker reads the tracker key from the "- Ticket ids:" line of
 * docs/agents/issue-tracker.md, the way tkt does, so ids work before any
 * ticket has a worktree. */
export function keyFromTracker(text: string): string {
	const line = text.split("\n").find((l) => l.startsWith("- Ticket ids:"));
	const m = line?.match(/`\^([A-Za-z][A-Za-z0-9]*)-/);
	return m ? m[1].toUpperCase() : "";
}

/** expandIds reads ticket ids from text: single ids and ranges such as
 * "ACM-12 to ACM-15" or "ACM-12..15". */
export function expandIds(text: string, key: string): string[] {
	const out: string[] = [];
	const add = (id: string) => {
		if (!out.includes(id)) out.push(id);
	};
	const k = key || "[A-Z][A-Z0-9]*";
	const range = new RegExp(`\\b(${k})[- ]?(\\d+)\\s*(?:to|through|-|\\.\\.)\\s*(?:\\1[- ]?)?(\\d+)\\b`, "gi");
	let rest = text;
	for (const m of text.matchAll(range)) {
		const lo = Number(m[2]);
		const hi = Number(m[3]);
		if (hi >= lo && hi - lo < 50) {
			for (let n = lo; n <= hi; n++) add(`${m[1].toUpperCase()}-${n}`);
			rest = rest.replace(m[0], " ");
		}
	}
	for (const m of rest.matchAll(new RegExp(`\\b(${k})[- ]?(\\d+)\\b`, "gi"))) add(`${m[1].toUpperCase()}-${Number(m[2])}`);
	return out;
}

/** mentions reports whether a person's message names the ticket, as an id
 * or, with the tracker key known, as its bare number. */
export function mentions(text: string, ticket: string): boolean {
	const m = ticket.match(/^([A-Z][A-Z0-9]*)-(\d+)$/);
	if (!m) return text.toUpperCase().includes(ticket.toUpperCase());
	return new RegExp(`(^|[^A-Za-z0-9])(${m[1]}[- ]?)?0*${m[2]}([^0-9]|$)`, "i").test(text);
}
