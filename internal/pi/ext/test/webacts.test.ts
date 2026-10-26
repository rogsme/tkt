import assert from "node:assert/strict";
import { test } from "node:test";
import { attentionOf, unblockedOf } from "../attention.ts";
import { cardActs, hashOf, navOf, sendLine, startLine, suggestLine, ticketActs } from "../web/acts.ts";
import { row } from "./fixtures.ts";

test("each answer /inbox offers is a button that sends the slash command you could type", () => {
	const plan = row({ phase: "awaiting_approval", last_status: "AWAITING_APPROVAL" });
	const acts = cardActs(attentionOf(plan, false)!, plan);
	assert.deepEqual(
		acts.map((a) => [a.label, a.cmd ?? a.tab ?? (a.reply ? "reply" : "")]),
		[
			["Approve", "/approve ACM-12"],
			["Approve and go auto", "/approve ACM-12 auto"],
			["Show the plan", "message"],
			["Ask for changes", "reply"],
		],
	);
	assert.equal(acts[0].main, true);
	assert.equal(acts[1].main, false);

	const stalled = row({ phase: "building", auto: { wanted: true, approve_plan: true, running: false, rounds: 0, stop: "", waiting: "", paused: "", why: "" } });
	assert.deepEqual(
		cardActs(attentionOf(stalled, false)!, stalled).map((a) => a.cmd),
		["/auto ACM-12 auto-plan", "/auto ACM-12 off", "/dismiss ACM-12"],
	);

	const gone = row({ phase: "no_agent", agent: null });
	assert.equal(cardActs(attentionOf(gone, false)!, gone)[0].cmd, "/relaunch ACM-12");

	const u = unblockedOf({ ticket: "ACM-13", by: "ACM-12", at: "2026-01-01T00:00:00Z" });
	assert.deepEqual(
		cardActs(u, undefined).map((a) => a.cmd),
		["/start ACM-13", "/start ACM-13 auto-plan", "/start ACM-13 manual", "/dismiss ACM-13"],
	);

	const fix = row({ phase: "needs_fix", auto: { wanted: true, approve_plan: false, running: true, rounds: 3, stop: "", waiting: "", paused: "round_limit", why: "" }, next: { kind: "command", reason: "findings", argv: ["tkt", "run", "ACM-12", "address"], text: "tkt run ACM-12 address" } });
	assert.equal(cardActs(attentionOf(fix, false)!, fix)[0].cmd, "/run ACM-12 address");
});

test("a ticket's own buttons follow what its state allows", () => {
	const labels = (r: ReturnType<typeof row>) => ticketActs(r).map((a) => a.cmd);
	assert.deepEqual(labels(row({ phase: "awaiting_approval" })), ["/approve ACM-12", "/approve ACM-12 auto", "/auto ACM-12 on", "/hold ACM-12"]);
	assert.deepEqual(labels(row({ phase: "ready_for_human", behind: 2, hold: true })), ["/check ACM-12", "/sync ACM-12", "/auto ACM-12 on", "/hold ACM-12 off"]);
	assert.deepEqual(labels(row({ phase: "merged" })), ["/done ACM-12"]);
	assert.deepEqual(labels(row({ phase: "needs_fix", agent: null, next: { kind: "command", reason: "findings", argv: [], text: "tkt run ACM-12 address" } })), ["/next ACM-12", "/relaunch ACM-12", "/auto ACM-12 on", "/hold ACM-12"]);
	// A chore's run has no plan and no tkt auto.
	assert.deepEqual(labels(row({ ticket: "chore-deps-1", chore: "deps", phase: "ready_for_human" })), ["/hold chore-deps-1"]);
	assert.deepEqual(labels(row({ auto: { wanted: true, approve_plan: false, running: true, rounds: 0, stop: "", waiting: "", paused: "", why: "" } })), ["/auto ACM-12 off", "/hold ACM-12"]);
});

test("the forms build the line you could type", () => {
	assert.equal(startLine(" ACM-12, ACM-13 ", "auto", ""), "/start ACM-12 ACM-13");
	assert.equal(startLine("ACM-12", "manual", ""), "/start ACM-12 manual");
	assert.equal(startLine("ACM-12 to ACM-15", "auto-plan", " opencode "), "/start ACM-12 to ACM-15 auto-plan as opencode");
	assert.equal(startLine("", "auto", "x"), "");
	assert.equal(suggestLine("", "opencode"), "/suggest");
	assert.equal(suggestLine(" five  tickets\ttagged feedback ", "sol"), "/suggest five tickets tagged feedback as sol");
	assert.equal(sendLine("ACM-12", "  keep the old endpoint\nfor now "), "/send ACM-12 keep the old endpoint\nfor now");
	assert.equal(sendLine("ACM-12", "  "), "");
});

test("the address keeps what the center column shows", () => {
	assert.deepEqual(navOf(""), { board: true });
	assert.deepEqual(navOf("#stats"), { view: "stats" });
	assert.deepEqual(navOf("#ACM-12/diff"), { ticket: "ACM-12", tab: "diff" });
	assert.deepEqual(navOf("#ACM-12/nope"), { ticket: "ACM-12", tab: "message" });
	assert.deepEqual(navOf("#chore/deps-1/screen"), { ticket: "chore/deps-1", tab: "screen" });
	assert.deepEqual(navOf("#-rf/diff"), { board: true });
	for (const n of [{ ticket: "ACM-12", tab: "history" as const }, { ticket: "chore/deps-1", tab: "pr" as const }, { view: "chores" as const }]) {
		assert.deepEqual(navOf(hashOf(n)), n);
	}
	assert.equal(hashOf({ board: true }), "#");
});
