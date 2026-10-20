import assert from "node:assert/strict";
import { test } from "node:test";
import { cycle, fixPrompt, statsText, tally } from "../report.ts";

test("statsText", () => {
	const text = statsText({
		repo: "github.com/acme/app",
		tickets: [
			{ ticket: "ACM-2", repo: "r", started: "2026-01-09T00:00:00Z", total_s: 7200, in_progress: true, waiting_on_you_s: 0, rounds: 0, auto: true, pr: 0 },
			{ ticket: "ACM-1", repo: "r", started: "2026-01-01T10:00:00Z", total_s: 23 * 3600, in_progress: false, waiting_on_you_s: 19 * 3600, rounds: 1, auto: false, pr: 7 },
		],
		summary: { merged: 1, in_progress: 1, median_total_s: 23 * 3600, p90_total_s: 23 * 3600, median_waiting_on_you_s: 19 * 3600, p90_waiting_on_you_s: 19 * 3600, without_start: 2 },
	});
	assert.match(text, /\| ACM-2 \| 2026-01-09 \| 2h so far \| 0s \| 0 \| yes \|/);
	assert.match(text, /\| ACM-1 \| 2026-01-01 \| 23h \| 19h \| 1 \| - \|/);
	assert.match(text, /1 merged: start to merge median 23h/);
	assert.match(text, /2 tickets have no recorded start/);
	assert.match(statsText({ repo: "", tickets: [], summary: { merged: 0, in_progress: 0, median_total_s: 0, p90_total_s: 0, median_waiting_on_you_s: 0, p90_waiting_on_you_s: 0, without_start: 0 } }), /No tickets/);
});

test("checklist marks", () => {
	assert.equal(cycle(""), "pass");
	assert.equal(cycle("pass"), "fail");
	assert.equal(cycle("fail"), "skip");
	assert.equal(cycle("skip"), "");
	assert.deepEqual(tally(["pass", "fail", "", "skip"], 5), { pass: 1, fail: 1, skip: 1, open: 2 });
});

test("fixPrompt", () => {
	const p = fixPrompt(2, "Sign out,\n   then press Back.", " the dashboard shows ");
	assert.match(p, /^Human verification step 2 of this branch's pull request failed\./);
	assert.match(p, /The step: Sign out, then press Back\./);
	assert.match(p, /What I saw: the dashboard shows\n/);
	assert.match(p, /Status: READY_FOR_REVIEW\.$/);
});
