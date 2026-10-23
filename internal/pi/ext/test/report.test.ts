import assert from "node:assert/strict";
import { test } from "node:test";
import { cycle, fixPrompt, type HistoryResult, historyText, statsText, tally } from "../report.ts";

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

test("historyText", () => {
	process.env.TZ = "UTC";
	const r: HistoryResult = {
		repo: "github.com/acme/app",
		forge: "ok",
		tickets: [
			{
				ticket: "ACM-12",
				timeline: { ticket: "ACM-12", repo: "r", started: "2026-01-01T00:00:00Z", total_s: 5 * 3600, in_progress: false, waiting_on_you_s: 7200, rounds: 1, auto: true, pr: 7 },
				events: [
					{ at: "2026-01-01T00:00:00Z", event: "started" },
					{ at: "2026-01-01T02:00:00Z", event: "approved" },
					{ at: "2026-01-01T03:00:00Z", event: "auto", action: "run", stage: "address" },
					{ at: "2026-01-01T05:00:00Z", event: "merged", pr: 7 },
				],
				pull_requests: [
					{
						number: 7, title: "ACM-12: Export reports", url: "https://git.example.com/acme/app/pull/7", state: "MERGED", additions: 40, deletions: 2, changed_files: 3,
						commits: [
							{ at: "2026-01-01T02:30:00Z", oid: "aaaaaaa", subject: "feat(reports): export as CSV" },
							{ at: "2026-01-01T03:30:00Z", oid: "bbbbbbb", subject: "fix(reports): quote commas" },
						],
						reviews: [{ at: "2026-01-01T02:50:00Z", author: "claude", verdict: "FINDINGS", head: "aaaaaaa", body: "1. **BUG** reports.go:12\n" + "x".repeat(4000) }],
					},
				],
			},
			{ ticket: "ACM-99", timeline: null, events: [], pull_requests: [] },
		],
	};
	const full = historyText(r, true);
	assert.match(full, /^ACM-12: 5h start to merge, 2h waiting on you, 1 round, in auto$/m);
	assert.match(full, /^- 2026-01-01 02:00 plan approved$/m);
	assert.match(full, /^- 2026-01-01 03:00 auto run address$/m);
	assert.match(full, /^- 2026-01-01 05:00 merged #7$/m);
	assert.match(full, /^Pull request #7 MERGED: ACM-12: Export reports \(\+40 -2 in 3 files\) https:\/\/git.example.com\/acme\/app\/pull\/7$/m);
	// Commits and reviews interleave in time, and a review's body follows it.
	assert.match(full, /commit aaaaaaa .*\n- 2026-01-01 02:50 review FINDINGS at aaaaaaa by claude\n {4}1\. \*\*BUG\*\* reports\.go:12\n/);
	assert.match(full, / \[cut\]\n- 2026-01-01 03:30 commit bbbbbbb/);
	assert.match(full, /^ACM-99: no history and no pull request\.$/m);
	const short = historyText({ ...r, forge: "failed" }, false);
	assert.doesNotMatch(short, /BUG/);
	assert.match(short, /could not read the pull requests/);
});

test("historyText words what tkt watch and tkt merge did", () => {
	process.env.TZ = "UTC";
	const events = [
		{ at: "2026-01-01T01:00:00Z", event: "hold", action: "on" },
		{ at: "2026-01-01T02:00:00Z", event: "sync", action: "merged" },
		{ at: "2026-01-01T03:00:00Z", event: "watch", action: "run", stage: "ci" },
		{ at: "2026-01-01T04:00:00Z", event: "merge", action: "ordered" },
		{ at: "2026-01-01T05:00:00Z", event: "watch", action: "merge" },
	];
	const text = historyText({ repo: "r", forge: "ok", tickets: [{ ticket: "ACM-12", timeline: null, events, pull_requests: [] }] }, false);
	for (const want of ["put on hold", "merged the base, with no agent", "tkt watch ran ci", "merge order left, for when the checks pass", "tkt watch carried out the merge order"]) {
		assert.match(text, new RegExp(`^- 2026-01-01 0\\d:00 ${want}$`, "m"), want);
	}
});
