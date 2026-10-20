import assert from "node:assert/strict";
import { test } from "node:test";
import { attentionOf, sound } from "../attention.ts";
import { row } from "./fixtures.ts";

test("a plan, a question and a ready ticket need you", () => {
	assert.equal(attentionOf(row({ phase: "awaiting_approval", last_status: "AWAITING_APPROVAL" }), false)?.kind, "plan");
	const q = attentionOf(row({ phase: "needs_you", agent: { pane_id: "p", name: "a", harness: "claude", status: "blocked" } }), false);
	assert.equal(q?.kind, "question");
	assert.match(q?.text ?? "", /\/peek ACM-12/);
	const ready = attentionOf(row({ phase: "ready_for_human", pr: { number: 4, state: "OPEN", head: "abc" } }), false);
	assert.equal(ready?.kind, "ready");
	assert.equal(sound(ready!), "done");
	assert.match(ready?.text ?? "", /Pull request #4/);
});

test("quiet phases only move the board", () => {
	for (const phase of ["planning", "building", "ci", "reviewing", "fixing", "starting", "queued"]) {
		assert.equal(attentionOf(row({ phase, agent: { pane_id: "p", name: "a", harness: "claude", status: "working" }, next: { kind: "wait", reason: "agent_working", argv: null, text: "wait" } }), false), undefined, phase);
	}
	assert.equal(attentionOf(row({ phase: "waiting_review", next: { kind: "wait", reason: "awaiting_bot", argv: null, text: "wait for review" } }), false), undefined);
});

test("a manual ticket waiting for its next step needs your go", () => {
	const a = attentionOf(row({ phase: "waiting_review", next: { kind: "command", reason: "needs_review", argv: ["tkt", "run", "ACM-12", "review"], text: "tkt run ACM-12 review" } }), false);
	assert.equal(a?.kind, "next");
	assert.match(a?.text ?? "", /\/next ACM-12/);
});

test("nothing is raised while a job runs or tkt auto drives the ticket", () => {
	const plan = row({ phase: "awaiting_approval" });
	assert.equal(attentionOf(plan, true), undefined);
	assert.equal(attentionOf({ ...plan, auto: { wanted: true, approve_plan: true, running: true, rounds: 0, stop: "", waiting: "" } }, false), undefined);
});

test("an auto stop comes from tkt auto, which already notified", () => {
	const stopped = row({ phase: "needs_you", auto: { wanted: false, approve_plan: false, running: false, rounds: 2, stop: "round_limit", waiting: "" } });
	const a = attentionOf(stopped, false);
	assert.equal(a?.kind, "auto_stop");
	assert.equal(a?.byAuto, true);
	const ready = attentionOf(row({ phase: "ready_for_human", auto: { wanted: false, approve_plan: false, running: false, rounds: 1, stop: "ready_for_human", waiting: "" } }), false);
	assert.equal(ready?.kind, "ready");
	assert.equal(ready?.byAuto, true);
});

test("a loop that died while it drove the ticket can resume", () => {
	const a = attentionOf(row({ phase: "building", auto: { wanted: true, approve_plan: true, running: false, rounds: 0, stop: "", waiting: "" } }), false);
	assert.equal(a?.kind, "resume_auto");
});

test("the key stays while the situation does, and changes with it", () => {
	const r = row({ phase: "awaiting_approval", last_status: "AWAITING_APPROVAL" });
	assert.equal(attentionOf(r, false)?.key, attentionOf({ ...r, phase_since: "2026-02-02T00:00:00Z" }, false)?.key);
	assert.notEqual(attentionOf(r, false)?.key, attentionOf({ ...r, phase: "needs_you", last_status: "BLOCKED" }, false)?.key);
});
