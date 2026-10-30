// The playground's made-up repository: one ticket in each situation the
// web page draws, so every card, column and button shows. Times count back
// from now, so ages read the same whenever you start it. It imports nothing
// at run time but the extension's pure modules, so node --test covers it.

import type { Incident, Row, Unblocked, Verification } from "../model.ts";

export const repo = "acme/app";

/** Scenario is everything the board reads. */
export interface Scenario {
	rows: Row[];
	unblocked: Unblocked[];
	incidents: Incident[];
	watch: "on" | "off" | "failing";
	update: string;
}

const heads: Record<string, string> = {};

/** headOf makes up a stable commit for a ticket. */
export function headOf(id: string): string {
	if (!heads[id]) {
		let h = 0;
		for (const c of id) h = (h * 31 + c.charCodeAt(0)) >>> 0;
		heads[id] = (h.toString(16) + "c0ffee1234567890abcdef1234567890abcdef").slice(0, 40);
	}
	return heads[id];
}

/** row builds a tkt ls row for a ticket in a phase, with an idle Claude
 * agent and nothing else, as a just-started ticket has. */
export function row(ticket: string, now: number, over: Partial<Row> = {}): Row {
	const ago = (min: number) => new Date(now - min * 60_000).toISOString();
	return {
		ticket,
		path: `/home/acme/app.${ticket}`,
		agent: { pane_id: `w1:p${ticket.replace(/\D/g, "")}`, name: ticket.toLowerCase(), harness: "claude", status: "idle" },
		last_status: "",
		pr: null,
		checks: null,
		review: null,
		next: { kind: "wait", reason: "agent_working", argv: null, text: "the agent works" },
		stage: "work",
		launch: "submitted",
		auto: { wanted: false, approve_plan: false, running: false, rounds: 0, stop: "", waiting: "", paused: "", why: "" },
		phase: "working",
		phase_since: ago(12),
		started: ago(180),
		idle_since: "",
		stale: false,
		...over,
	};
}

/** pr is an open pull request at the ticket's head. */
function pr(id: string, n: number, over: Partial<NonNullable<Row["pr"]>> = {}): Row["pr"] {
	return { number: n, state: "OPEN", mergeable: "MERGEABLE", head: headOf(id), url: `https://git.example.com/acme/app/pulls/${n}`, ...over };
}

const green = { state: "success", total: 6, failing: [], pending: 0 };
const clean = (id: string) => ({ verdict: "CLEAR", head: headOf(id), at_head: true });
const auto = (over: Partial<NonNullable<Row["auto"]>> = {}): Row["auto"] => ({ wanted: true, approve_plan: false, running: true, rounds: 1, stop: "", waiting: "", paused: "", why: "", ...over });

/** report is a verify-pr report on the ticket's head. */
export function report(id: string, failed: number[] = []): Verification {
	const texts = ["The export button shows on the reports page.", "The CSV opens with one row per report line.", "A report with no lines exports a header only."];
	return {
		verification: failed.length ? "FAILED" : "PASSED",
		head: headOf(id),
		at_head: true,
		carried: false,
		steps: texts.map((text, i) => ({ n: i + 1, result: failed.includes(i + 1) ? "FAILED" : "PASSED", text: failed.includes(i + 1) ? "The file had no header row." : text })),
	};
}

/** scenario is the repository as the playground starts it. */
export function scenario(now: number): Scenario {
	const ago = (min: number) => new Date(now - min * 60_000).toISOString();
	const rows: Row[] = [
		row("ACM-101", now, { title: "Export reports as CSV", phase: "awaiting_approval", stage: "start", last_status: "PLAN_READY", next: { kind: "human", reason: "plan_ready", argv: null, text: "read the plan, then approve it" }, phase_since: ago(25), idle_since: ago(25) }),
		row("ACM-102", now, { title: "Retry failed webhooks", phase: "needs_you", agent: { pane_id: "w1:p102", name: "acm-102", harness: "opencode", status: "blocked" }, next: { kind: "human", reason: "agent_blocked", argv: null, text: "the agent asks a question in its tab" }, phase_since: ago(8) }),
		row("ACM-103", now, { title: "Move billing to the new API", phase: "blocked", last_status: "BLOCKED", blocked_reason: "waits for ACM-90's API to ship", next: { kind: "human", reason: "blocked", argv: null, text: "it waits on something outside it" }, phase_since: ago(300), started: ago(2000) }),
		row("ACM-104", now, { title: "Paginate the audit log", phase: "working", agent: { pane_id: "w1:p104", name: "acm-104", harness: "claude", status: "working", background: 1 }, pr: pr("ACM-104", 41), checks: { state: "pending", total: 6, failing: [], pending: 2 }, diff: { files: 7, added: 293, removed: 41 }, auto: auto(), phase_since: ago(4) }),
		row("ACM-105", now, { title: "Fix the timezone in invoices", phase: "needs_fix", pr: pr("ACM-105", 42), checks: { state: "failure", total: 6, failing: ["test (linux)", "lint"], pending: 0 }, review: clean("ACM-105"), behind: 3, diff: { files: 3, added: 58, removed: 12 }, next: { kind: "command", reason: "checks_fail", argv: ["tkt", "run", "ACM-105", "fix"], text: "the checks fail: tkt run ACM-105 fix" }, phase_since: ago(40), idle_since: ago(40) }),
		row("ACM-106", now, { title: "Rate limit the public API", phase: "needs_fix", pr: pr("ACM-106", 43), checks: green, review: { verdict: "FINDINGS", head: headOf("ACM-106"), at_head: true }, diff: { files: 5, added: 140, removed: 9 }, next: { kind: "command", reason: "findings", argv: ["tkt", "run", "ACM-106", "address"], text: "the review has findings: tkt run ACM-106 address" }, phase_since: ago(65), idle_since: ago(65) }),
		row("ACM-107", now, { title: "Add a dark theme to emails", phase: "ready_for_human", pr: pr("ACM-107", 44), checks: green, review: clean("ACM-107"), behind: 0, verification: report("ACM-107"), diff: { files: 4, added: 88, removed: 20 }, next: { kind: "human", reason: "steps", argv: null, text: "walk the human verification steps" }, phase_since: ago(90), idle_since: ago(90) }),
		row("ACM-108", now, { title: "Show seats on the plan page", phase: "ready_for_human", pr: pr("ACM-108", 45), checks: green, review: clean("ACM-108"), behind: 2, verification: report("ACM-108", [2]), diff: { files: 2, added: 31, removed: 4 }, next: { kind: "human", reason: "steps", argv: null, text: "walk the human verification steps" }, phase_since: ago(130), idle_since: ago(130) }),
		row("ACM-109", now, { title: "Cache the pricing table", phase: "ready_to_merge", pr: pr("ACM-109", 46), checks: green, review: clean("ACM-109"), behind: 0, confirmed: { head: headOf("ACM-109"), evidence: true, url: "https://git.example.com/acme/app/pulls/46#issuecomment-1", at_head: true, carried: false }, diff: { files: 2, added: 64, removed: 18 }, next: { kind: "human", reason: "steps_passed", argv: null, text: "every step passed: merge it" }, phase_since: ago(15), idle_since: ago(15) }),
		row("ACM-110", now, { title: "Drop the legacy login page", phase: "merged", pr: pr("ACM-110", 38, { state: "MERGED" }), checks: green, review: clean("ACM-110"), diff: { files: 9, added: 12, removed: 410 }, next: { kind: "command", reason: "merged", argv: ["tkt", "done", "ACM-110"], text: "tkt done ACM-110 removes it" }, phase_since: ago(20), idle_since: ago(20) }),
		row("ACM-111", now, { title: "Try a new chart library", phase: "closed", pr: pr("ACM-111", 39, { state: "CLOSED" }), next: { kind: "command", reason: "closed", argv: ["tkt", "done", "ACM-111"], text: "tkt done ACM-111 removes it" }, phase_since: ago(600), started: ago(5000) }),
		row("ACM-112", now, { title: "Speed up the search index", phase: "needs_fix", pr: pr("ACM-112", 47), checks: green, review: { verdict: "CHANGES_REQUESTED", head: headOf("ACM-112"), at_head: true }, next: { kind: "command", reason: "findings", argv: ["tkt", "run", "ACM-112", "address"], text: "the review has findings: tkt run ACM-112 address" }, auto: auto({ rounds: 3, paused: "round_limit", why: "round_limit" }), diff: { files: 6, added: 201, removed: 77 }, phase_since: ago(35), idle_since: ago(35) }),
		row("ACM-113", now, { title: "Archive old projects", phase: "waiting_review", pr: pr("ACM-113", 48), checks: green, next: { kind: "wait", reason: "review_running", argv: null, text: "review-pr reviews it" }, auto: auto({ running: false }), diff: { files: 3, added: 45, removed: 2 }, phase_since: ago(55), idle_since: ago(55) }),
		row("ACM-114", now, { title: "Translate the settings page", phase: "no_agent", agent: null, pr: pr("ACM-114", 49), checks: green, next: { kind: "command", reason: "no_agent", argv: ["tkt", "resume", "ACM-114"], text: "no agent runs: tkt resume ACM-114" }, diff: { files: 12, added: 380, removed: 300 }, phase_since: ago(240) }),
		row("ACM-115", now, { title: "Upgrade the queue client", phase: "working", hold: true, pr: pr("ACM-115", 50), checks: green, diff: { files: 2, added: 6, removed: 6 }, phase_since: ago(1440), started: ago(4000) }),
		row("ACM-116", now, { title: "Log slow queries", phase: "queued", agent: null, stage: "start", auto: auto({ waiting: "slot", rounds: 0 }), next: { kind: "wait", reason: "queued", argv: null, text: "waits for a free slot" }, phase_since: ago(3), started: ago(3) }),
		row("ACM-117", now, { title: "Add SSO for admins", phase: "planning", stage: "start", agent: { pane_id: "w1:p117", name: "acm-117", harness: "claude", status: "working" }, next: { kind: "wait", reason: "agent_working", argv: null, text: "start-ticket plans it" }, phase_since: ago(6), started: ago(6) }),
		row("ACM-118", now, { title: "Clean up feature flags", phase: "reviewing", stage: "review", stale: true, agent: { pane_id: "w1:p118", name: "acm-118", harness: "claude", status: "working" }, pr: pr("ACM-118", 51), checks: green, next: { kind: "wait", reason: "review_running", argv: null, text: "review-pr reviews it" }, diff: { files: 8, added: 22, removed: 190 }, phase_since: ago(70) }),
		row("chore/deps-2026-10-29", now, { chore: "deps", title: "Update dependencies", phase: "ready_for_human", stage: "chore", pr: pr("chore/deps-2026-10-29", 52), checks: green, review: clean("chore/deps-2026-10-29"), next: { kind: "human", reason: "chore_ready", argv: null, text: "review it and merge it yourself" }, diff: { files: 2, added: 40, removed: 40 }, phase_since: ago(50), started: ago(120) }),
		row("ACM-120", now, { title: "Import contacts from a file", phase: "needs_you", last_status: "BLOCKED", next: { kind: "human", reason: "status_blocked", argv: null, text: "the agent stopped with Status: BLOCKED" }, phase_since: ago(18), idle_since: ago(18) }),
		row("ACM-121", now, { title: "Rotate the signing keys", phase: "merged", pr: pr("ACM-121", 37, { state: "MERGED" }), checks: green, review: clean("ACM-121"), auto: auto({ yolo: true, running: false, wanted: false, stop: "post_merge_failed" }), next: { kind: "command", reason: "merged", argv: ["tkt", "done", "ACM-121"], text: "tkt done ACM-121 removes it" }, phase_since: ago(45) }),
	];
	return { rows, unblocked: [{ ticket: "ACM-130", by: "ACM-110", at: ago(20) }], incidents: [], watch: "off", update: "" };
}

/** incident is what the incident trigger reports. */
export function incident(now: number): Incident {
	return { page: "githubstatus.com", service: "GitHub Actions", problem: "degraded performance", url: "https://www.githubstatus.com/", since: new Date(now - 10 * 60_000).toISOString() };
}
