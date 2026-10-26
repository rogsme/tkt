import assert from "node:assert/strict";
import { test } from "node:test";
import type { Attention } from "../attention.ts";
import type { Board } from "../mirror.ts";
import { apply, completions, initial, title } from "../web/state.ts";

const note = (id: string, text: string) => ({ kind: "note" as const, id, text });

test("the page draws the chat from hello, then applies each change", () => {
	let s = apply(initial, { t: "hello", repo: "app", theme: "dark", busy: false, items: [note("1", "one"), note("2", "two")], live: [], dialogs: [], commands: [] });
	assert.equal(s.link, "open");
	s = apply(s, { t: "items", from: 1, items: [note("2", "two, done"), note("3", "three")] });
	assert.deepEqual(s.items, [note("1", "one"), note("2", "two, done"), note("3", "three")]);
	s = apply(s, { t: "items", from: 0, items: [] });
	assert.deepEqual(s.items, []);
	s = apply(s, { t: "busy", busy: true });
	s = apply(s, { t: "live", items: [note("live", "Thin")] });
	assert.equal(s.busy, true);
	assert.deepEqual(s.live, [note("live", "Thin")]);
});

test("questions queue in order, each once, and leave when closed", () => {
	const a = { id: "d1", kind: "confirm" as const, title: "Remove ACM-12?" };
	const b = { id: "d2", kind: "input" as const, title: "What did you see?" };
	let s = apply(initial, { t: "dialog", dialog: a });
	s = apply(s, { t: "dialog", dialog: b });
	s = apply(s, { t: "dialog", dialog: a });
	assert.deepEqual(s.dialogs, [a, b]);
	assert.equal(title({ ...s, repo: "app" }), "(2) tkt app");
	s = apply(s, { t: "closed", id: "d1" });
	assert.deepEqual(s.dialogs, [b]);
});

test("losing the chat drops its questions and its work; the title says it is closed", () => {
	let s = apply(initial, { t: "hello", repo: "app", theme: "", busy: true, items: [], live: [], dialogs: [{ id: "d1", kind: "confirm", title: "Quit now?" }], commands: [] });
	s = apply(s, { t: "link", link: "closed" });
	assert.equal(s.busy, false);
	assert.deepEqual(s.dialogs, []);
	assert.equal(title(s), "tkt app (closed)");
});

test("notices keep the last five, and a click dismisses one", () => {
	let s = initial;
	for (let i = 1; i <= 6; i++) s = apply(s, { t: "notify", text: `n${i}`, level: "info" });
	assert.deepEqual(
		s.toasts.map((t) => t.text),
		["n2", "n3", "n4", "n5", "n6"],
	);
	s = apply(s, { t: "dismiss", id: s.toasts[0].id });
	assert.equal(s.toasts.length, 4);
});

test("completions offer the commands a line starts to name", () => {
	const cmds = [
		{ name: "approve", description: "" },
		{ name: "auto", description: "" },
		{ name: "check", description: "" },
	];
	assert.deepEqual(
		completions("/a", cmds).map((c) => c.name),
		["approve", "auto"],
	);
	assert.equal(completions("/", cmds).length, 3);
	assert.deepEqual(completions("/approve ACM-12", cmds), []);
	assert.deepEqual(completions("start", cmds), []);
});

test("a new item on the board gets a toast once, and the title counts what needs you", () => {
	const item = (ticket: string, key: string): Attention => ({ key, ticket, kind: "plan", title: "plan ready", text: `${ticket}: the plan is ready.`, byAuto: false });
	const board = (items: Attention[]): Board => ({ rows: [], items, jobs: {}, incidents: [] });
	let s = apply(initial, { t: "hello", repo: "app", theme: "", busy: false, items: [], live: [], dialogs: [], commands: [], board: board([item("ACM-12", "a")]) });
	assert.deepEqual(s.toasts, []);
	assert.equal(title(s), "(1) tkt app");
	s = apply(s, { t: "board", board: board([item("ACM-12", "a"), item("ACM-13", "b"), item("ACM-13", "c")]) });
	assert.deepEqual(
		s.toasts.map((t) => t.text),
		["ACM-13: plan ready"],
	);
	assert.equal(title(s), "(2) tkt app");
	s = apply(s, { t: "board", board: board([item("ACM-13", "b")]) });
	s = apply(s, { t: "board", board: board([item("ACM-13", "b"), item("ACM-12", "a")]) });
	assert.equal(s.toasts.length, 1);
	s = apply(s, { t: "dialog", dialog: { id: "d1", kind: "confirm", title: "Remove ACM-12?" } });
	assert.equal(title(s), "(3) tkt app");
});
