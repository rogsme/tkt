import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import type { AttentionKind } from "../attention.ts";
import { boardColumns, columns } from "../board.ts";
import type { Board, Page, ToPage } from "../mirror.ts";
import type { ChoresResult } from "../model.ts";
import { commandsOf, Fake } from "../play/fake.ts";
import { triggers } from "../play/triggers.ts";

const commands = commandsOf(readFileSync(new URL("../commands.ts", import.meta.url), "utf8"));

/** setup joins a page that cancels every question the fake asks, so a
 * trigger that asks one ends. */
function setup() {
	const fake = new Fake({ commands, pace: 0 });
	const got: ToPage[] = [];
	const page: Page = {
		send: (msg) => {
			got.push(msg);
			if (msg.t === "dialog") queueMicrotask(() => fake.mirror.receive({ t: "answer", id: msg.dialog.id, value: null }, page));
		},
	};
	fake.mirror.join(page);
	const board = () => (got.filter((m) => m.t === "board" || m.t === "hello").at(-1) as { board: Board }).board;
	return { fake, got, page, board };
}

test("the playground answers every command the extension registers", () => {
	assert.ok(commands.length > 20, `read ${commands.length} commands from commands.ts`);
	const { fake } = setup();
	const missing = commands.filter((c) => !fake.handlers[c.name]).map((c) => c.name);
	assert.deepEqual(missing, [], "add a handler to play/fake.ts for each");
	assert.ok(commands.every((c) => c.description.length > 0));
});

test("the scenario shows every kind of card and fills every column", () => {
	const { fake } = setup();
	const b = fake.board();
	const kinds = new Set(b.items.map((a) => a.kind));
	const all: AttentionKind[] = ["plan", "question", "blocked", "check", "ready", "merge", "merged", "next", "paused", "stalled", "failed", "unblocked", "chore"];
	assert.deepEqual(all.filter((k) => !kinds.has(k)), []);
	const empty = boardColumns(b.rows, Date.now()).filter((c) => c.cells.length === 0).map((c) => c.title);
	assert.deepEqual(empty, [], `every one of ${columns.length} columns has a ticket`);
});

test("every trigger runs", async () => {
	const { fake, got } = setup();
	for (const t of triggers) await fake.trigger(t.name);
	const notices = got.filter((m) => m.t === "notify");
	assert.ok(!notices.some((m) => m.t === "notify" && m.text.includes("no such trigger")));
	assert.ok(got.some((m) => m.t === "dialog" && m.dialog.kind === "checklist"));
	assert.ok(got.some((m) => m.t === "live"));
});

test("/approve takes the plan's card off the board", async () => {
	const { fake, page, board } = setup();
	assert.ok(board().items.some((a) => a.ticket === "ACM-101" && a.kind === "plan"));
	await fake.handlers.approve(["ACM-101"], page);
	assert.ok(!board().items.some((a) => a.ticket === "ACM-101"));
	assert.equal(board().rows.find((r) => r.ticket === "ACM-101")?.phase, "working");
});

test("/check offers a skipped step as a waiver, and asks why", async () => {
	const fake = new Fake({ commands, pace: 0 });
	const titles: string[] = [];
	const page: Page = {
		send: (msg) => {
			if (msg.t !== "dialog") return;
			titles.push(msg.dialog.title);
			const d = msg.dialog;
			const value =
				d.kind === "checklist" ? d.steps!.map((_, i) => (i === 0 ? "skip" : "pass"))
				: d.kind === "input" ? "it needs production credentials"
				: { answer: d.title.endsWith("Merge it?") ? "Not yet" : d.options!.find((o) => /Open the checklist|Post it/.test(o.label))!.label, typed: false };
			queueMicrotask(() => fake.mirror.receive({ t: "answer", id: d.id, value }, page));
		},
	};
	fake.mirror.join(page);
	await fake.handlers.check(["ACM-107"], page);
	const n = fake.board().rows.find((r) => r.ticket === "ACM-107");
	assert.ok(titles.some((t) => /step 1 skipped\. Post that human verification passed, with the skipped steps\?$/.test(t)), titles.join("\n"));
	assert.ok(titles.includes("Why can step 1 stay unrun?"));
	assert.deepEqual(n?.confirmed?.waived, [1]);
	assert.equal(n?.confirmed?.reason, "it needs production credentials");
	assert.equal(n?.phase, "ready_to_merge");
});

test("a command that opens a view opens it on the page that sent it", () => {
	const { fake, got, page } = setup();
	fake.send("/peek acm-102", [], page);
	assert.deepEqual(got.at(-1), { t: "open", ticket: "ACM-102", tab: "screen" });
});

test("the page's queries get tkt's envelopes", () => {
	const { fake } = setup();
	for (const what of ["message", "screen", "diff", "steps", "history"] as const) {
		const env = fake.query({ what, ticket: "ACM-108" }) as { ok: boolean };
		assert.ok(env.ok, what);
	}
	assert.equal((fake.query({ what: "diff", ticket: "ACM-101" }) as { ok: boolean }).ok, false);
	assert.equal((fake.query({ what: "message", ticket: "ACM-999" }) as { ok: boolean }).ok, false);
});

test("the chores view gets a chore that opens a pull request and one that does not", () => {
	const { fake } = setup();
	const env = fake.query({ what: "chores" }) as { result: ChoresResult };
	assert.deepEqual([...new Set(env.result.chores.map((c) => c.pr))].sort(), [false, true]);
});

test("the chores view gets a prompt of many lines and one that runs a command", () => {
	const { fake } = setup();
	const env = fake.query({ what: "chores" }) as { result: ChoresResult };
	const prompts = env.result.chores.map((c) => c.prompt);
	assert.ok(prompts.some((p) => p.includes("\n")));
	assert.ok(prompts.some((p) => p.startsWith("/")));
});
