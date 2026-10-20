import assert from "node:assert/strict";
import { test } from "node:test";
import { boardColumns, columnOf, grid, order, widgetLines } from "../board.ts";
import { row } from "./fixtures.ts";

const now = Date.parse("2026-01-01T02:00:00Z");

test("the widget puts what needs you first and marks stale agents", () => {
	const rows = [
		row({ ticket: "ACM-3", phase: "building", phase_since: "2026-01-01T01:30:00Z", agent: { pane_id: "p", name: "a", harness: "claude", status: "working" } }),
		row({ ticket: "ACM-12", phase: "awaiting_approval" }),
		row({ ticket: "ACM-5", phase: "needs_you", stale: true }),
	];
	const lines = widgetLines({ rows, jobs: new Map([["ACM-3", "review"]]), waiting: 0, now, tick: 1, repo: "app" }, 200, new Set(["ACM-12"]));
	assert.equal(lines[0].text, "tkt  app  3 tickets, 1 needs you (/inbox)");
	assert.equal(lines[0].tone, "warning");
	assert.match(lines[1].text, /^ACM-12 +awaiting approval +2h +2h +idle/);
	assert.equal(lines[1].tone, "warning");
	assert.match(lines[2].text, /^ACM-3 +building +30m .*review \//);
	assert.match(lines[3].text, /! stale/);
	assert.equal(lines[3].tone, "error");
	assert.ok(widgetLines({ rows, jobs: new Map(), waiting: 0, now, tick: 0, repo: "app" }, 20, new Set()).every((l) => l.text.length <= 20));
});

test("the widget shows auto and queued tickets", () => {
	const rows = [
		row({ ticket: "ACM-1", phase: "building", auto: { wanted: true, approve_plan: true, running: true, rounds: 0, stop: "", waiting: "" } }),
		row({ ticket: "ACM-2", phase: "queued", auto: { wanted: true, approve_plan: false, running: true, rounds: 0, stop: "", waiting: "slot" } }),
	];
	const lines = widgetLines({ rows, jobs: new Map(), waiting: 0, now, tick: 0, repo: "app" }, 200, new Set());
	assert.match(lines[1].text, /auto full$/);
	assert.match(lines[2].text, /auto$/);
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
