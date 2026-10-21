import assert from "node:assert/strict";
import { test } from "node:test";
import { attentionOf, sound } from "../attention.ts";
import { row } from "./fixtures.ts";

const working = { pane_id: "p", name: "a", harness: "claude", status: "working" };
const auto = (over: object) => ({ wanted: true, approve_plan: true, running: true, rounds: 0, stop: "", waiting: "", paused: "", why: "", ...over });

test("a plan, a question and a ready ticket need you", () => {
	assert.equal(attentionOf(row({ phase: "awaiting_approval", last_status: "AWAITING_APPROVAL" }), false)?.kind, "plan");
	const q = attentionOf(row({ phase: "needs_you", agent: { ...working, status: "blocked" }, next: { kind: "human", reason: "agent_blocked", argv: null, text: "answer" } }), false);
	assert.equal(q?.kind, "question");
	assert.match(q?.text ?? "", /\/peek ACM-12/);
	const ready = attentionOf(row({ phase: "ready_for_human", pr: { number: 4, state: "OPEN", head: "abc" } }), false);
	assert.equal(ready?.kind, "ready");
	assert.equal(sound(ready!), "done");
	assert.match(ready?.text ?? "", /Pull request #4/);
});

test("quiet phases only move the board", () => {
	for (const phase of ["planning", "building", "ci", "reviewing", "fixing", "starting", "queued"]) {
		assert.equal(attentionOf(row({ phase, agent: working, next: { kind: "wait", reason: "agent_working", argv: null, text: "wait" } }), false), undefined, phase);
	}
	assert.equal(attentionOf(row({ phase: "waiting_review", next: { kind: "wait", reason: "awaiting_bot", argv: null, text: "wait for the review bot" } }), false), undefined);
	// An agent that waits on its own CI watcher is working, not stuck.
	assert.equal(attentionOf(row({ phase: "building", agent: { ...working, status: "idle", background: 1 }, next: { kind: "wait", reason: "agent_waiting", argv: null, text: "wait" } }), false), undefined);
});

test("a manual ticket waiting for its next step needs your go", () => {
	const a = attentionOf(row({ phase: "needs_fix", next: { kind: "command", reason: "findings", argv: ["tkt", "run", "ACM-12", "address"], text: "tkt run ACM-12 address" } }), false);
	assert.equal(a?.kind, "next");
	assert.match(a?.text ?? "", /\/next ACM-12/);
});

test("nothing is raised while a job runs or tkt auto drives the ticket", () => {
	const plan = row({ phase: "awaiting_approval" });
	assert.equal(attentionOf(plan, true), undefined);
	assert.equal(attentionOf({ ...plan, auto: auto({}) }, false), undefined);
	// A loop that drives a working agent says nothing, even after an
	// earlier loop stopped for you.
	assert.equal(attentionOf(row({ phase: "building", agent: working, auto: auto({ stop: "needs_you" }) }), false), undefined);
});

test("a paused loop needs you, and says how it goes on", () => {
	const q = attentionOf(row({ phase: "needs_you", agent: { ...working, status: "blocked" }, auto: auto({ paused: "needs_you", why: "agent_blocked" }) }), false);
	assert.equal(q?.kind, "question");
	assert.equal(q?.byAuto, true);
	assert.match(q?.text ?? "", /tkt auto goes on after it/);
	const silent = attentionOf(row({ phase: "needs_you", auto: auto({ paused: "bot_silent" }) }), false);
	assert.equal(silent?.kind, "paused");
	assert.equal(silent?.title, "review bot silent");
	const plan = attentionOf(row({ phase: "awaiting_approval", auto: auto({ approve_plan: false, paused: "plan_awaits_approval" }) }), false);
	assert.equal(plan?.kind, "plan");
	// Right after you approve, the loop still says it paused for the plan
	// until its next look, but the agent already builds it.
	assert.equal(attentionOf(row({ phase: "building", agent: working, auto: auto({ approve_plan: false, paused: "plan_awaits_approval" }) }), false), undefined);
});

test("a ticket marked for auto with no loop has stalled", () => {
	const a = attentionOf(row({ phase: "building", auto: auto({ running: false }) }), false);
	assert.equal(a?.kind, "stalled");
	assert.match(a?.text ?? "", /\/auto ACM-12 on/);
});

test("the key stays while the situation does, and changes with it", () => {
	const r = row({ phase: "awaiting_approval", last_status: "AWAITING_APPROVAL" });
	assert.equal(attentionOf(r, false)?.key, attentionOf({ ...r, phase_since: "2026-02-02T00:00:00Z" }, false)?.key);
	assert.notEqual(attentionOf(r, false)?.key, attentionOf({ ...r, phase: "needs_you", last_status: "BLOCKED" }, false)?.key);
});
