import assert from "node:assert/strict";
import { test } from "node:test";
import { attentionOf, choicesOf, sound, unblockedOf } from "../attention.ts";
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

test("a blocked ticket says why once, and waits out of what needs you", () => {
	const a = attentionOf(row({ phase: "blocked", last_status: "BLOCKED", blocked_reason: "Clerk account upgrade pending with James." }), false);
	assert.equal(a?.kind, "blocked");
	assert.equal(a?.parked, true);
	assert.match(a?.text ?? "", /^ACM-12 is blocked: Clerk account upgrade pending with James\./);
	// One you put on hold says nothing.
	assert.equal(attentionOf(row({ phase: "blocked", hold: true }), false), undefined);
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
	// A running tkt watch starts the loop again by itself.
	assert.equal(attentionOf(row({ phase: "building", auto: auto({ running: false }) }), false, true), undefined);
});

test("the key stays while the situation does, and changes with it", () => {
	const r = row({ phase: "awaiting_approval", last_status: "AWAITING_APPROVAL" });
	assert.equal(attentionOf(r, false)?.key, attentionOf({ ...r, phase_since: "2026-02-02T00:00:00Z" }, false)?.key);
	assert.notEqual(attentionOf(r, false)?.key, attentionOf({ ...r, phase: "needs_you", last_status: "BLOCKED" }, false)?.key);
	// Putting a ticket on hold while its plan waits keeps the plan's item,
	// so it is not announced again; a pause's reason still tells pauses apart.
	const waiting = { ...r, auto: auto({ approve_plan: false, paused: "plan_awaits_approval" }) };
	assert.equal(attentionOf({ ...waiting, hold: true, auto: auto({ approve_plan: false, paused: "hold" }) }, false)?.key, attentionOf(waiting, false)?.key);
	const paused = row({ phase: "needs_you", auto: auto({ paused: "bot_silent" }) });
	assert.notEqual(attentionOf(paused, false)?.key, attentionOf({ ...paused, auto: auto({ paused: "no_progress" }) }, false)?.key);
});

/** offered lists an item's answers as do, or do:stage for a run. */
const offered = (r: Parameters<typeof attentionOf>[0]) => {
	const a = attentionOf(r, false);
	assert.ok(a, "no item");
	return choicesOf(a).map((c) => (c.stage ? `${c.do}:${c.stage}` : c.do));
};
const fix = (reason: string, stage: string) => ({ kind: "command" as const, reason, argv: ["tkt", "run", "ACM-12", stage], text: `tkt run ACM-12 ${stage}` });

test("a loop at its round limit offers one more fix run first", () => {
	assert.deepEqual(offered(row({ phase: "needs_you", next: fix("findings", "address"), auto: auto({ paused: "round_limit", why: "findings" }) })), ["run:address", "peek", "show", "dismiss"]);
	assert.equal(offered(row({ phase: "needs_you", next: fix("checks_fail", "ci"), auto: auto({ paused: "round_limit", why: "checks_fail" }) }))[0], "run:ci");
	const a = attentionOf(row({ phase: "needs_you", next: fix("findings", "address"), auto: auto({ paused: "round_limit", why: "findings" }) }), false);
	assert.equal(choicesOf(a!)[0].label, "Run address once more");
});

test("a loop with no progress offers the same step again, when it runs one", () => {
	assert.equal(offered(row({ phase: "needs_you", next: fix("needs_review", "review"), auto: auto({ paused: "no_progress" }) }))[0], "run:review");
	assert.deepEqual(offered(row({ phase: "needs_you", auto: auto({ paused: "no_progress" }) })), ["peek", "show", "dismiss"]);
});

test("each stop offers the answer that moves it on", () => {
	const human = (reason: string) => ({ kind: "human" as const, reason, argv: null, text: "" });
	assert.equal(offered(row({ phase: "needs_you", next: human("bot_silent") }))[0], "run:review");
	assert.equal(offered(row({ phase: "needs_you", auto: auto({ paused: "bot_silent" }) }))[0], "run:review");
	assert.equal(offered(row({ phase: "needs_you", last_status: "BLOCKED", next: human("status_blocked") }))[0], "reply");
	assert.equal(offered(row({ phase: "needs_you", next: human("check_agent") }))[0], "reply");
	assert.equal(offered(row({ phase: "needs_you", next: human("review_awaits_approval") }))[0], "reply");
	assert.equal(offered(row({ phase: "needs_you", auto: auto({ paused: "needs_you", why: "status_blocked" }) }))[0], "reply");
	// An agent that asks in its own dialog only queues what you send.
	assert.ok(!offered(row({ phase: "needs_you", agent: { pane_id: "p", name: "a", harness: "claude", status: "blocked" }, next: human("agent_blocked") })).includes("reply"));
	assert.equal(offered(row({ phase: "no_agent", agent: null, next: { kind: "command", reason: "no_agent", argv: ["tkt", "resume", "ACM-12"], text: "" } }))[0], "resume");
	assert.equal(offered(row({ phase: "needs_you", auto: auto({ paused: "agent_missing" }) }))[0], "resume");
});

test("a manual step can go to tkt auto, and a stalled ticket can leave it", () => {
	assert.deepEqual(offered(row({ phase: "needs_fix", next: fix("findings", "address") })), ["next", "show", "auto_on"]);
	assert.deepEqual(offered(row({ phase: "building", auto: auto({ running: false }) })), ["auto_on", "auto_off", "dismiss"]);
	assert.deepEqual(offered(row({ phase: "awaiting_approval", last_status: "AWAITING_APPROVAL" })), ["approve", "approve_auto", "show", "reply"]);
});

test("a ticket on hold shows what its agent needs, but no step to take", () => {
	const fix = { phase: "needs_fix", hold: true, next: { kind: "command" as const, reason: "findings", argv: ["tkt", "run", "ACM-12", "address"], text: "tkt run ACM-12 address" } };
	assert.equal(attentionOf(row(fix), false), undefined);
	// A loop paused on the hold steps aside, so the plan shows as a manual
	// ticket's would.
	const plan = row({ phase: "awaiting_approval", hold: true, auto: auto({ paused: "hold" }) });
	assert.equal(attentionOf(plan, false)?.kind, "plan");
	assert.equal(attentionOf(row({ ...fix, auto: auto({ paused: "hold" }) }), false), undefined);
	assert.equal(attentionOf(row({ ...fix, auto: auto({ running: false }) }), false), undefined);
});

test("a moved base waits for tkt sync", () => {
	const a = attentionOf(row({ phase: "needs_fix", next: { kind: "command", reason: "base_moved", argv: ["tkt", "sync", "ACM-12"], text: "tkt sync ACM-12" } }), false);
	assert.equal(a?.kind, "next");
	assert.equal(a?.title, "the base moved; tkt sync merges it");
	assert.equal(a?.offers, undefined);
});

test("tkt watch takes the upkeep steps, and says when it waits for you", () => {
	const ci = { phase: "needs_fix", next: { kind: "command" as const, reason: "checks_fail", argv: ["tkt", "run", "ACM-12", "ci"], text: "tkt run ACM-12 ci" } };
	assert.equal(attentionOf(row(ci), false, false)?.kind, "next");
	assert.equal(attentionOf(row(ci), false, true), undefined);
	const findings = { phase: "needs_fix", next: { kind: "command" as const, reason: "findings", argv: ["tkt", "run", "ACM-12", "address"], text: "tkt run ACM-12 address" } };
	assert.equal(attentionOf(row(findings), false, true)?.kind, "next");
	const stuck = attentionOf(row({ ...ci, phase: "needs_you", watch: { last: "run:ci@abc", paused: "no_progress" } }), false, true);
	assert.equal(stuck?.kind, "paused");
	assert.equal(stuck?.byAuto, true);
	assert.match(stuck?.text ?? "", /tkt watch ran ci on this head, and nothing changed/);
	assert.deepEqual(stuck?.offers?.map((o) => o.label), ["Run ci again"]);
	const spent = attentionOf(row({ ...ci, phase: "needs_you", watch: { last: "run:ci@abc", paused: "round_limit" } }), false, true);
	assert.match(spent?.text ?? "", /max_rounds/);
	assert.deepEqual(spent?.offers?.map((o) => o.label), ["Run ci once more"]);
	const sync = attentionOf(row({ phase: "needs_you", next: { kind: "command", reason: "base_moved", argv: ["tkt", "sync", "ACM-12"], text: "tkt sync ACM-12" }, watch: { last: "sync:@abc", paused: "no_progress" } }), false, true);
	assert.match(sync?.text ?? "", /tkt watch tried to merge the base on this head/);
});

test("a tkt auto loop whose sync keeps failing says why", () => {
	const a = attentionOf(row({ phase: "needs_you", auto: auto({ paused: "sync_failed", why: "base_moved", note: "ACM-12 has uncommitted changes" }) }), false);
	assert.equal(a?.kind, "paused");
	assert.equal(a?.title, "sync failed");
	assert.match(a?.text ?? "", /cannot merge the base: ACM-12 has uncommitted changes\. tkt auto tries again/);
});

test("a due merge order is tkt watch's, and yours while it does not run", () => {
	const due = row({ phase: "merging", next: { kind: "command", reason: "merge_due", argv: ["tkt", "merge", "ACM-12", "--passed", "1,2", "--head", "abc"], text: "tkt merge ACM-12 --passed 1,2 --head abc" } });
	assert.equal(attentionOf(due, false, true), undefined);
	const a = attentionOf(due, false, false);
	assert.equal(a?.kind, "next");
	assert.match(a?.text ?? "", /merge order is due\. \/next ACM-12/);
});

test("a ticket a merge unblocked offers its starts", () => {
	const a = unblockedOf({ ticket: "ACM-15", by: "ACM-12", at: "2026-01-02T00:00:00Z" });
	assert.equal(a.kind, "unblocked");
	assert.match(a.text, /^ACM-15 is unblocked: ACM-12 merged/);
	assert.deepEqual(
		choicesOf(a).map((c) => c.do),
		["start", "start_auto_plan", "start_manual", "dismiss"],
	);
	assert.equal(sound(a), "done");
});

test("tkt watch reviews the head a sync made for a merge order", () => {
	const next = { kind: "command", reason: "needs_review", argv: ["tkt", "run", "ACM-12", "review"], text: "tkt run ACM-12 review" };
	const merge = { head: "abc", steps: [1], at: "2026-01-02T00:00:00Z" };
	assert.equal(attentionOf(row({ phase: "waiting_review", next, merge }), false, true), undefined);
	assert.equal(attentionOf(row({ phase: "waiting_review", next }), false, true)?.kind, "next");
});

test("a chore's run asks for you where no skill takes over", () => {
	const pr = { number: 44, state: "OPEN", head: "abc" };
	const ready = attentionOf(row({ ticket: "deps-1", chore: "deps", phase: "ready_for_human", pr, next: { kind: "human", reason: "chore_ready", argv: null, text: "review the pull request, and merge it yourself" } }), false);
	assert.equal(ready?.kind, "chore");
	assert.equal(ready?.title, "ready to merge");
	assert.match(ready?.text ?? "", /chore deps's pull request #44 is ready\. Review it and merge it yourself/);
	assert.equal(sound(ready!), "done");
	assert.ok(!choicesOf(ready!).some((c) => c.do === "check"));
	const found = attentionOf(row({ ticket: "deps-1", chore: "deps", phase: "needs_you", pr, next: { kind: "human", reason: "chore_findings", argv: null, text: "read" } }), false);
	assert.equal(found?.title, "review found something");
	assert.equal(choicesOf(found!)[0].do, "reply");
	// A run that changed nothing is tkt watch's to remove, or yours without one.
	const nothing = row({ ticket: "deps-1", chore: "deps", phase: "finished", next: { kind: "command", reason: "no_changes", argv: ["tkt", "done", "deps-1"], text: "tkt done deps-1" } });
	assert.equal(attentionOf(nothing, false, true), undefined);
	assert.equal(attentionOf(nothing, false, false)?.kind, "merged");
	assert.match(attentionOf(nothing, false, false)?.text ?? "", /\/done deps-1 removes the run/);
});

test("a chore's run gets no tkt auto and no plan answers", () => {
	const next = attentionOf(row({ ticket: "deps-1", chore: "deps", phase: "needs_fix", next: { kind: "command", reason: "checks_fail", argv: ["tkt", "run", "deps-1", "ci"], text: "tkt run deps-1 ci" } }), false);
	assert.equal(next?.kind, "next");
	assert.ok(!choicesOf(next!).some((c) => c.do === "auto_on"), "tkt auto drives tickets only");
	assert.ok(choicesOf(next!).some((c) => c.do === "next"));
	const ticket = attentionOf(row({ phase: "needs_fix", next: { kind: "command", reason: "checks_fail", argv: ["tkt", "run", "ACM-12", "ci"], text: "tkt run ACM-12 ci" } }), false);
	assert.ok(choicesOf(ticket!).some((c) => c.do === "auto_on"));
	const stuck = attentionOf(row({ ticket: "deps-1", chore: "deps", phase: "no_agent", agent: null, launch: "tab", next: { kind: "command", reason: "no_agent", argv: ["tkt", "chore", "run", "deps"], text: "tkt chore run deps" } }), false);
	assert.match(stuck?.text ?? "", /stopped before its agent got the prompt\. \/chores run deps goes on with it/);
	const closed = row({ ticket: "deps-1", chore: "deps", phase: "closed", pr: { number: 44, state: "CLOSED" }, next: { kind: "command", reason: "closed", argv: ["tkt", "done", "deps-1"], text: "tkt done deps-1" } });
	assert.equal(attentionOf(closed, false, true), undefined);
	assert.match(attentionOf(closed, false, false)?.text ?? "", /you closed chore deps's pull request #44\. \/done deps-1 removes the run/);
});

test("a ticket whose pull request closed without merging goes with tkt watch", () => {
	const closed = row({ phase: "closed", pr: { number: 9, state: "CLOSED" }, next: { kind: "command", reason: "closed", argv: ["tkt", "done", "ACM-12"], text: "tkt done ACM-12" } });
	assert.equal(attentionOf(closed, false, true), undefined);
	assert.match(attentionOf(closed, false, false)?.text ?? "", /ACM-12: pull request #9 was closed without merging\. \/done ACM-12 removes the ticket; move it in the tracker yourself\./);
});

test("verify-pr's report shapes the ready item", () => {
	const pr = { number: 4, state: "OPEN", head: "abc" };
	const steps = [{ n: 1, result: "PASSED" as const, text: "opened" }];
	const passed = attentionOf(row({ phase: "ready_for_human", pr, verification: { verification: "PASSED", head: "abc", at_head: true, carried: false, steps } }), false);
	assert.equal(passed?.kind, "ready");
	assert.match(passed?.text ?? "", /passed every human verification step in verify-pr\. \/check ACM-12 shows its evidence/);
	const part = attentionOf(row({ phase: "ready_for_human", pr, verification: { verification: "INCOMPLETE", head: "abc", at_head: true, carried: false, steps } }), false);
	assert.match(part?.text ?? "", /could not run every human verification step/);
	// A report on an older head that no clean merge carried says nothing.
	const old = attentionOf(row({ phase: "ready_for_human", pr, verification: { verification: "PASSED", head: "old", at_head: false, carried: false, steps } }), false);
	assert.match(old?.text ?? "", /is ready for human verification/);
	const waits = attentionOf(row({ phase: "ready_for_human", pr, next: { kind: "command", reason: "needs_verify", argv: ["tkt", "run", "ACM-12", "verify"], text: "tkt run ACM-12 verify" } }), false);
	assert.match(waits?.text ?? "", /waits for verify-pr\. \/check ACM-12 starts it/);
	// A loop runs verify-pr itself, so it raises nothing before the run.
	assert.equal(attentionOf(row({ phase: "ready_for_human", pr, auto: auto({}), next: { kind: "command", reason: "needs_verify", argv: null, text: "" } }), false), undefined);
	const failed = attentionOf(row({ phase: "needs_fix", next: { kind: "command", reason: "verify_failed", argv: ["tkt", "run", "ACM-12", "address"], text: "tkt run ACM-12 address" } }), false);
	assert.equal(failed?.title, "verify-pr found a step that fails");
});

test("a yolo loop's pause and its failed base need you", () => {
	const paused = attentionOf(row({ phase: "ready_for_human", auto: auto({ yolo: true, paused: "needs_you", why: "verify_incomplete" }) }), false);
	assert.equal(paused?.title, "verification incomplete");
	assert.match(paused?.text ?? "", /the yolo run cannot merge it/);
	const base = attentionOf(row({ phase: "merged", auto: { ...auto({ running: false, wanted: false, yolo: true }), stop: "post_merge_failed" } }), false);
	assert.equal(base?.kind, "failed");
	assert.match(base?.text ?? "", /checks on the base failed/);
});
