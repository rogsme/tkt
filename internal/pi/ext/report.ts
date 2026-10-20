// Text for /stats, and the verification checklist's marks.

import { ago } from "./model.ts";

export interface StatsTicket {
	ticket: string;
	repo: string;
	started: string;
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
		const total = secs(t.total_s) + (t.in_progress ? " so far" : "");
		out.push(`| ${t.ticket} | ${t.started.split("T")[0]} | ${total} | ${secs(t.waiting_on_you_s)} | ${t.rounds} | ${t.auto ? "yes" : "-"} |`);
	}
	const s = r.summary;
	if (s.merged > 0) {
		out.push("", `${s.merged} merged: start to merge median ${secs(s.median_total_s)}, p90 ${secs(s.p90_total_s)}; waiting on you median ${secs(s.median_waiting_on_you_s)}, p90 ${secs(s.p90_waiting_on_you_s)}.`);
	}
	if (s.in_progress > 0) out.push(`${s.in_progress} in progress.`);
	if (s.without_start > 0) out.push(`${s.without_start} tickets have no recorded start and are left out.`);
	return out.join("\n");
}

export type Mark = "" | "pass" | "fail" | "skip";

/** cycle moves a step's mark on: unmarked, passed, failed, skipped. */
export function cycle(m: Mark): Mark {
	return m === "" ? "pass" : m === "pass" ? "fail" : m === "fail" ? "skip" : "";
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
