import assert from "node:assert/strict";
import { test } from "node:test";
import { ago, expandIds, keyFromTracker, keyOf, mentions, normalizeId, rowsOf, stampText } from "../model.ts";
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
