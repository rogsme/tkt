// Text for /stats, and the verification checklist's marks.

import { ago } from "./model.ts";

export interface StatsTicket {
	ticket: string;
	repo: string;
	started: string;
	/** merged is when the pull request merged; "" for one closed without
	 * merging, or still open. */
	merged?: string;
	total_s: number;
	in_progress: boolean;
	waiting_on_you_s: number;
	rounds: number;
	auto: boolean;
	pr: number;
}

export interface StatsResult {
	repo: string;
	tickets: StatsTicket[];
	summary: {
		merged: number;
		closed?: number;
		in_progress: number;
		median_total_s: number;
		p90_total_s: number;
		median_waiting_on_you_s: number;
		p90_waiting_on_you_s: number;
		without_start: number;
	};
}

const secs = (s: number) => ago(s * 1000);

/** statsText renders tkt stats for the chat, as a Markdown table. */
export function statsText(r: StatsResult, limit = 15): string {
	if (r.tickets.length === 0) return "No tickets in the history yet. tkt records a ticket from its first tkt start.";
	const out = ["| Ticket | Started | Start to merge | Waiting on you | Rounds | Auto |", "|---|---|---|---|---|---|"];
	for (const t of r.tickets.slice(0, limit)) {
		const total = secs(t.total_s) + (t.in_progress ? " so far" : t.merged === "" ? " (closed)" : "");
		out.push(`| ${t.ticket} | ${t.started.split("T")[0]} | ${total} | ${secs(t.waiting_on_you_s)} | ${t.rounds} | ${t.auto ? "yes" : "-"} |`);
	}
	const s = r.summary;
	if (s.merged > 0) {
		out.push("", `${s.merged} merged: start to merge median ${secs(s.median_total_s)}, p90 ${secs(s.p90_total_s)}; waiting on you median ${secs(s.median_waiting_on_you_s)}, p90 ${secs(s.p90_waiting_on_you_s)}.`);
	}
	if (s.closed) out.push(`${s.closed} closed without merging, left out of the times.`);
	if (s.in_progress > 0) out.push(`${s.in_progress} in progress.`);
	if (s.without_start > 0) out.push(`${s.without_start} tickets have no recorded start and are left out.`);
	return out.join("\n");
}

export interface HistoryEvent {
	at: string;
	event: string;
	phase?: string;
	from?: string;
	stage?: string;
	action?: string;
	stop?: string;
	pr?: number;
	/** unblocked lists the tickets finish-ticket said a merge unblocked. */
	unblocked?: string[];
	/** note says why a step failed. */
	note?: string;
}

export interface HistoryPR {
	number: number;
	title: string;
	url: string;
	state: string;
	additions: number;
	deletions: number;
	changed_files: number;
	commits: { at: string; oid: string; subject: string }[];
	reviews: { at: string; author: string; verdict: string; head: string; body: string }[];
}

export interface HistoryResult {
	repo: string;
	forge: string;
	tickets: { ticket: string; timeline: StatsTicket | null; events: HistoryEvent[]; pull_requests: HistoryPR[] }[];
}

/** bodyLimit bounds one review's body in historyText, so a long review
 * leaves room for the rest. */
const bodyLimit = 3000;

/** historyText renders tkt history: per ticket, the timeline, each event,
 * and each pull request's commits and reviews in the order they happened.
 * With bodies, a review's findings follow it, for the model; times are
 * local, as the person reads them. */
export function historyText(r: HistoryResult, bodies: boolean): string {
	const out: string[] = [];
	for (const t of r.tickets) {
		if (out.length) out.push("");
		if (t.events.length === 0 && t.pull_requests.length === 0) {
			out.push(`${t.ticket}: no history and no pull request.`);
			continue;
		}
		const tl = t.timeline;
		const head = tl
			? `: ${secs(tl.total_s)}${tl.in_progress ? " so far" : " start to merge"}, ${secs(tl.waiting_on_you_s)} waiting on you, ${tl.rounds} round${tl.rounds === 1 ? "" : "s"}${tl.auto ? ", in auto" : ""}`
			: "";
		out.push(`${t.ticket}${head}`);
		for (const e of t.events) out.push(`- ${local(e.at)} ${eventText(e)}`);
		for (const p of t.pull_requests) {
			out.push(`Pull request #${p.number} ${p.state}: ${p.title} (+${p.additions} -${p.deletions} in ${p.changed_files} files) ${p.url}`.trimEnd());
			const entries: { at: string; lines: string[] }[] = [
				...p.commits.map((c) => ({ at: c.at, lines: [`- ${local(c.at)} commit ${c.oid} ${c.subject}`] })),
				...p.reviews.map((v) => {
					const lines = [`- ${local(v.at)} review ${v.verdict ? `${v.verdict} at ${v.head}` : "comment"} by ${v.author}`];
					if (bodies) {
						const body = v.body.length > bodyLimit ? `${v.body.slice(0, bodyLimit)} [cut]` : v.body;
						lines.push(...body.split("\n").map((l) => `    ${l}`.trimEnd()));
					}
					return { at: v.at, lines };
				}),
			];
			entries.sort((a, b) => a.at.localeCompare(b.at));
			for (const e of entries) out.push(...e.lines);
		}
	}
	if (r.forge === "failed") out.push("", "tkt could not read the pull requests; this is the history alone.");
	return out.join("\n");
}

export function eventText(e: HistoryEvent): string {
	switch (e.event) {
		case "approved":
			return e.action === "session" ? "plan approved, in its session" : "plan approved";
		case "stage":
			return `ran ${e.stage}`;
		case "phase":
			return `phase ${e.phase}`;
		case "auto":
			return ["auto", e.action, e.stage, e.stop].filter(Boolean).join(" ");
		case "merged":
			return `merged #${e.pr}`;
		case "closed":
			return `#${e.pr} closed without merging`;
		case "verified":
			return e.action === "evidence" ? `human verification passed, from verify-pr's evidence; commented on #${e.pr}` : `human verification passed; commented on #${e.pr}`;
		case "hold":
			return e.action === "off" ? "taken off hold" : "put on hold";
		case "sync":
			return e.action === "failed" ? `sync failed: ${e.note ?? ""}` : "merged the base, with no agent";
		case "watch":
			if (e.action === "pause") return `tkt watch waits for you: ${e.stop}`;
			if (e.stop === "failed") return `tkt watch could not ${[e.action, e.stage].filter(Boolean).join(" ")}${e.note ? `: ${e.note}` : ""}`;
			if (e.action === "merge") return "tkt watch carried out the merge order";
			if (e.action === "auto") return "tkt watch started its tkt auto loop again";
			return `tkt watch ran ${e.stage}`;
		case "merge":
			if (e.action === "ordered") return "merge order left, for when finish-ticket can merge";
			if (e.action === "dropped") return "merge order dropped by a new head";
			return "merge order cancelled";
		case "done":
			if (e.unblocked?.length) return `removed; the merge unblocked ${e.unblocked.join(", ")}`;
	}
	return e.event;
}

/** local renders an RFC 3339 time as local "2026-01-02 15:04". */
export function local(at: string): string {
	const d = new Date(at);
	if (Number.isNaN(d.getTime())) return at;
	const p = (n: number) => String(n).padStart(2, "0");
	return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export type Mark = "" | "pass" | "fail" | "skip";

/** cycle moves a step's mark on: unmarked, passed, failed, skipped. */
export function cycle(m: Mark): Mark {
	return m === "" ? "pass" : m === "pass" ? "fail" : m === "fail" ? "skip" : "";
}

/** withEvidence adds verify-pr's result to each step it reported on, to
 * show beside the step: only you mark a step. */
export function withEvidence<S extends { n: number }>(steps: S[], v: { steps: { n: number; result: "PASSED" | "FAILED" | "NOT RUN" }[] } | undefined): (S & { verified?: "PASSED" | "FAILED" | "NOT RUN" })[] {
	if (!v) return steps;
	return steps.map((s) => {
		const r = v.steps.find((x) => x.n === s.n);
		return r ? { ...s, verified: r.result } : s;
	});
}

/** tally counts the marks over n steps. */
export function tally(marks: Mark[], n: number): { pass: number; fail: number; skip: number; open: number } {
	const t = { pass: 0, fail: 0, skip: 0, open: 0 };
	for (let i = 0; i < n; i++) {
		const m = marks[i] ?? "";
		if (m === "") t.open++;
		else t[m]++;
	}
	return t;
}

/** fixPrompt is what a fix session gets when a human step failed. */
export function fixPrompt(step: number, text: string, saw: string): string {
	return [
		`Human verification step ${step} of this branch's pull request failed.`,
		`The step: ${text.replace(/\s+/g, " ").trim()}`,
		`What I saw: ${saw.trim()}`,
		"Find the cause, fix it test-first, commit and push, then end with Status: READY_FOR_REVIEW.",
	].join("\n");
}
