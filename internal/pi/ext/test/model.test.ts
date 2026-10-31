import assert from "node:assert/strict";
import { test } from "node:test";
import { ago, choresText, expandIds, idOf, idsIn, incidentsOf, keyFromTracker, keyOf, mentions, mergeText, normalizeId, rowsOf, stampText, updateNotice, stepText, versionText } from "../model.ts";
import { row } from "./fixtures.ts";

test("normalizeId", () => {
	assert.equal(normalizeId("acm-12", "ACM"), "ACM-12");
	assert.equal(normalizeId("ACM 7", "ACM"), "ACM-7");
	assert.equal(normalizeId("12", "ACM"), "ACM-12");
	assert.equal(normalizeId("FOO-1", "ACM"), "FOO-1");
	assert.equal(normalizeId("12", ""), "12");
});

test("expandIds reads ids and ranges", () => {
	assert.deepEqual(expandIds("start ACM-12 to ACM-15 in auto", "ACM"), ["ACM-12", "ACM-13", "ACM-14", "ACM-15"]);
	assert.deepEqual(expandIds("acm-3..5 and ACM-9", "ACM"), ["ACM-3", "ACM-4", "ACM-5", "ACM-9"]);
	assert.deepEqual(expandIds("ACM-12 and ACM-12", "ACM"), ["ACM-12"]);
	assert.deepEqual(expandIds("nothing here", "ACM"), []);
	// A range that runs backwards, or over fifty tickets, is no range.
	assert.deepEqual(expandIds("ACM-15 to ACM-12", "ACM"), ["ACM-15", "ACM-12"]);
});

test("mentions", () => {
	assert.ok(mentions("I approved the plan for ACM-12, continue", "ACM-12"));
	assert.ok(mentions("approve acm 12", "ACM-12"));
	assert.ok(mentions("approve 12", "ACM-12"));
	assert.ok(!mentions("approve 123", "ACM-12"));
	assert.ok(!mentions("approve ACM-120", "ACM-12"));
	assert.ok(!mentions("approve it", "ACM-12"));
});

test("keyOf and ago", () => {
	assert.equal(keyOf(["spike", "ACM-3"]), "ACM");
	assert.equal(ago(45_000), "45s");
	assert.equal(ago(12 * 60_000), "12m");
	assert.equal(ago(3 * 3600_000), "3h");
	assert.equal(ago(72 * 3600_000), "3d");
});

test("rowsOf takes rows from a partial failure too", () => {
	const r = row();
	assert.deepEqual(rowsOf({ ok: true, command: "ls", warnings: [], result: { sources: { herdr: "ok", forge: "ok", fetch: "skipped" }, rows: [r] } }), [r]);
	assert.deepEqual(rowsOf({ ok: false, command: "ls", warnings: [], error: { code: "forge_query_failed", exit: 1, message: "x", hint: "", details: { rows: [r] } } }), [r]);
	assert.equal(rowsOf({ ok: false, command: "ls", warnings: [], error: { code: "herdr_failed", exit: 1, message: "x", hint: "", details: {} } }), undefined);
});

test("keyFromTracker reads the key the way tkt does", () => {
	assert.equal(keyFromTracker("# Issue tracker\n\n- Ticket ids: `ACM-<number>`, matched by `^ACM-[1-9][0-9]*$`.\n"), "ACM");
	assert.equal(keyFromTracker("- Ticket ids: `^[0-9]+$`\n"), "");
	assert.equal(keyFromTracker("no line here"), "");
});

test("stampText gives the time of day, and the date on another day", () => {
	const at = new Date(2026, 9, 21, 10, 50, 23).getTime();
	const now = new Date(2026, 9, 21, 18, 0, 0).getTime();
	assert.equal(stampText({ at }, now), "10:50:23");
	assert.equal(stampText({ at: at + 6000, took: 6000 }, now), "10:50:29  6s");
	assert.equal(stampText({ at, took: 125_000 }, now + 86_400_000), "Oct 21 10:50:23  2m");
});

test("updateNotice suggests a newer release, and nothing to a development build", () => {
	const r = { from: "v0.3.0", to: "v0.4.0", path: "/usr/local/bin/tkt", available: true, dev: false, updated: false, next: "tkt update" };
	assert.equal(updateNotice(r), "v0.4.0");
	assert.equal(updateNotice({ ...r, to: "v0.3.0", available: false, next: undefined }), undefined);
	assert.equal(updateNotice({ ...r, from: "dev-1a2b3c", dev: true, next: "tkt update --force" }), undefined);
	assert.equal(updateNotice({ ...r, updated: true, next: undefined }), undefined);
});

test("stepText says what tkt auto's step does", () => {
	assert.equal(stepText({ action: "sync" }), "merging the base");
	assert.equal(stepText({ action: "run", stage: "ci" }), "running ci");
	assert.equal(stepText({ action: "approve" }), "approving the plan");
});

test("mergeText says what tkt merge did", () => {
	const r = { ticket: "ACM-12", pr: 7, head: "abc", steps: [1, 2] };
	assert.match(mergeText({ ...r, action: "merging" }), /^ACM-12: finish-ticket merges pull request #7 in a fresh session/);
	assert.match(mergeText({ ...r, action: "ordered" }), /^ACM-12 merges once its checks pass/);
	assert.match(mergeText({ ...r, action: "ordered", waits: "sync" }), /^ACM-12 merges once tkt watch merged its base and a review cleared the new head; your steps carry over/);
	// review-pr reviews a head a sync made, or one only the review bot cleared.
	assert.match(mergeText({ ...r, action: "ordered", waits: "review", verified: "aaa" }), /^ACM-12 merges once a review clears the head a sync made/);
	assert.match(mergeText({ ...r, action: "ordered", waits: "review", verified: "abc" }), /^ACM-12 merges once review-pr, which tkt watch starts, clears its head, since finish-ticket merges on review-pr's verdict/);
	assert.match(mergeText({ ...r, action: "ordered", waits: "turn", after: "ACM-11" }), /^ACM-12 merges once ACM-11 merged first/);
	assert.match(mergeText({ ...r, action: "cancelled", sent: "2026-01-01T00:00:00Z" }), /no merge order now\. finish-ticket got the merge line at 2026-01-01T00:00:00Z/);
	assert.equal(mergeText({ ...r, action: "cancelled" }), "ACM-12 has no merge order now.");
	assert.match(mergeText({ ...r, action: "none", sent: "2026-01-01T00:00:00Z" }), /finish-ticket got the merge line at 2026-01-01T00:00:00Z/);
});

test("choresText lists the chores with the run that holds the next one back", () => {
	const chore = { name: "deps", schedule: "0 9 * * 1,4", as: "", last: "", due: false, next: "2026-10-26T12:00:00Z", pr: true, runs: ["deps-2026-10-22"], open: "deps-2026-10-22" };
	const text = choresText({ repo: "/w/app", watch: { running: false }, chores: [chore] });
	assert.match(text, /^- deps \(0 9 \* \* 1,4\): next .*Oct 26.*; deps-2026-10-22 is still open/);
	assert.match(text, /tkt watch does not run for this repository/);
	assert.match(choresText({ repo: "/w/app", watch: { running: true }, chores: [] }), /^No chore runs in this repository/);
});

test("idOf and idsIn keep a chore run's id as its branch", () => {
	const rows = [row({ ticket: "ACM-12" }), row({ ticket: "deps-2026-10-26", chore: "deps" })];
	assert.equal(idOf("acm-12", rows, "ACM"), "ACM-12");
	assert.equal(idOf("12", rows, "ACM"), "ACM-12");
	assert.equal(idOf("DEPS-2026-10-26", rows, "ACM"), "deps-2026-10-26");
	// A run gone from the board keeps its case, for its history.
	assert.equal(idOf("deps-2026-10-22", rows, "ACM"), "deps-2026-10-22");
	assert.equal(idOf("deps-2026", rows, "ACM"), "deps-2026");
	assert.deepEqual(idsIn("ACM-12 to ACM-14 deps-2026-10-22", rows, "ACM"), ["ACM-12", "ACM-13", "ACM-14", "deps-2026-10-22"]);
	assert.deepEqual(idsIn("acm-12, deps", rows, "ACM"), ["ACM-12", "deps"]);
});

test("incidentsOf reads them from a forge failure too", () => {
	const incident = { page: "github", service: "GitHub", problem: "API Requests major outage", url: "", since: "", seen: "" };
	assert.deepEqual(incidentsOf({ ok: true, command: "ls", warnings: [], result: { sources: { herdr: "ok", forge: "ok", fetch: "ok" }, rows: [], incidents: [incident] } }), [incident]);
	assert.deepEqual(incidentsOf({ ok: false, command: "ls", warnings: [], error: { code: "forge_query_failed", exit: 1, message: "gh pr list failed", hint: "", details: { rows: [], incidents: [incident] } } }), [incident]);
	assert.deepEqual(incidentsOf({ ok: false, command: "ls", warnings: [], error: { code: "internal", exit: 1, message: "x", hint: "", details: {} } }), []);
});

test("versionText says what the chat runs, and when the installed tkt moved on", () => {
	assert.equal(versionText("v0.6.0", "1.3.0", "v0.6.0", "/usr/bin/tkt"), "This chat runs tkt v0.6.0, with pi 1.3.0. It is the tkt at /usr/bin/tkt.");
	assert.equal(
		versionText("v0.6.0", "1.3.0", "v0.6.1", "/usr/bin/tkt"),
		"This chat runs tkt v0.6.0, with pi 1.3.0. The tkt at /usr/bin/tkt is v0.6.1 now: quit, then run tkt agent --continue to move this chat to it.",
	);
	assert.equal(versionText("v0.6.0", "", "", "/usr/bin/tkt"), "This chat runs tkt v0.6.0. The tkt at /usr/bin/tkt did not say its version.");
	assert.match(versionText("", "", "v0.6.0", "tkt"), /^This chat does not know its tkt version\. The tkt at tkt is v0\.6\.0 now/);
});
