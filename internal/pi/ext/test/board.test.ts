import assert from "node:assert/strict";
import { test } from "node:test";
import { attentionOf } from "../attention.ts";
import { boardColumns, boardLines, columnOf, footer, grid, order, statusText, title } from "../board.ts";
import { row } from "./fixtures.ts";

const now = Date.parse("2026-01-01T02:00:00Z");

test("the board puts what needs you first, with what to do about it", () => {
	const rows = [
		row({ ticket: "ACM-3", phase: "building", phase_since: "2026-01-01T01:30:00Z", agent: { pane_id: "p", name: "a", harness: "claude", status: "working" } }),
		row({ ticket: "ACM-12", phase: "awaiting_approval" }),
		row({ ticket: "ACM-5", phase: "planning", stale: true }),
		row({ ticket: "ACM-7", phase: "ready_for_human" }),
	];
	const items = [attentionOf(rows[1], false)!];
	const s = { rows, jobs: new Map([["ACM-3", "review"]]), items, now, tick: 1, repo: "app" };
	assert.equal(title(s), "tkt  app  4 tickets  1 needs you");
	const [head, ...lines] = boardLines(s, 200);
	assert.equal(head.text, "TICKET    AGE  PHASE             TIME AGENT    PR     NOTES");
	assert.equal(head.tone, "muted");
	assert.match(lines[0].text, /^ACM-12 +2h +awaiting approval +2h +idle/);
	assert.equal(lines[0].tone, "warning");
	assert.match(lines[1].text, /^ACM-3 +2h +building +30m +working .*review \//);
	assert.match(lines[2].text, /stale$/);
	assert.equal(lines[2].tone, "error");
	assert.match(lines[3].text, /^ACM-7 +2h +ready to verify/);
	assert.equal(lines[3].tone, "success");
	assert.equal(lines[5].text, "! ACM-12 plan ready   /show ACM-12, /approve ACM-12");
	assert.ok(boardLines(s, 20).every((l) => l.text.length <= 20));
	assert.match(statusText(s), /Needs the user:\n- ACM-12: the plan is ready/);
});

test("the board shows auto, paused, queued and waiting tickets", () => {
	const auto = { wanted: true, approve_plan: true, running: true, rounds: 0, stop: "", waiting: "", paused: "", why: "" };
	const rows = [
		row({ ticket: "ACM-1", phase: "building", auto }),
		row({ ticket: "ACM-2", phase: "queued", agent: null, auto: { ...auto, waiting: "slot" } }),
		row({ ticket: "ACM-3", phase: "needs_you", auto: { ...auto, paused: "needs_you" } }),
		row({ ticket: "ACM-4", phase: "building", agent: { pane_id: "p", name: "a", harness: "claude", status: "idle", background: 2 } }),
	];
	const [, ...lines] = boardLines({ rows, jobs: new Map(), items: [], now, tick: 0, repo: "app" }, 200);
	assert.match(lines[0].text, / auto$/);
	assert.match(lines[1].text, / queued$/);
	assert.equal(lines[1].tone, "muted");
	assert.match(lines[2].text, / paused$/);
	assert.match(lines[3].text, /^ACM-4 +2h +building +2h +waiting/);
});

test("the age counts from tkt start, the time from the phase's start", () => {
	const r = row({ ticket: "ACM-9", phase: "planning", started: "2026-01-01T01:19:00Z", phase_since: "2026-01-01T01:59:11Z" });
	const [, line] = boardLines({ rows: [r], jobs: new Map(), items: [], now, tick: 0, repo: "app" }, 200);
	assert.match(line.text, /^ACM-9 +41m +planning +49s /);
	const [, queued] = boardLines({ rows: [row({ phase: "queued", started: "", agent: null })], jobs: new Map(), items: [], now, tick: 0, repo: "app" }, 200);
	assert.match(queued.text, /^ACM-12 +- +queued/);
});

test("an empty board says how to begin", () => {
	const lines = boardLines({ rows: [], jobs: new Map(), items: [], now, tick: 0, repo: "app" }, 80);
	assert.equal(lines[0].text, 'No tickets yet. Say "start ACM-123".');
});

test("the columns hold each phase once", () => {
	assert.equal(columnOf(row({ phase: "fixing" })), "Review");
	assert.equal(columnOf(row({ phase: "no_agent" })), "Needs you");
	assert.equal(columnOf(row({ phase: "something new" })), "Needs you");
	const cols = boardColumns([row({ ticket: "ACM-2", phase: "ci" }), row({ ticket: "ACM-10", phase: "ci" }), row({ ticket: "ACM-1", phase: "merged" })], now);
	assert.deepEqual(order(cols), ["ACM-2", "ACM-10", "ACM-1"]);
	const g = grid(cols, 80, "ACM-10");
	assert.match(g[0], /^Needs you \(0\)/);
	assert.ok(g.some((l) => l.includes(">ACM-10")));
	assert.ok(g.every((l) => l.length <= 80));
});

test("the board's footer names a newer tkt first", () => {
	const s = { rows: [], jobs: new Map<string, string>(), items: [], now, tick: 0, repo: "app" };
	assert.equal(footer(s), "/board  /inbox  /board hide");
	assert.equal(footer({ ...s, update: "v0.4.0" }), "/update to v0.4.0  /board  /inbox  /board hide");
});

test("the board marks a ticket on hold", () => {
	const auto = { wanted: true, approve_plan: false, running: true, rounds: 0, stop: "", waiting: "", paused: "hold", why: "" };
	const rows = [row({ ticket: "ACM-1", phase: "waiting_review", hold: true }), row({ ticket: "ACM-2", phase: "ci", hold: true, auto })];
	const [, ...lines] = boardLines({ rows, jobs: new Map(), items: [], now, tick: 0, repo: "app" }, 200);
	assert.match(lines[0].text, / hold$/);
	assert.match(lines[1].text, / hold$/);
});
