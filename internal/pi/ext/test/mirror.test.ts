import assert from "node:assert/strict";
import { test } from "node:test";
import type { Item } from "../chat.ts";
import { type Board, type Image, Mirror, type Page, type Query, type Theme, type ToPage } from "../mirror.ts";

function setup() {
	const sent: { text: string; images: Image[]; page: Page }[] = [];
	const themes: Theme[] = [];
	const queries: Query[] = [];
	let stops = 0;
	const m = new Mirror("app", {
		send: (text, images, page) => sent.push({ text, images, page }),
		stop: () => stops++,
		theme: (t) => themes.push(t),
		commands: () => [{ name: "approve", description: "Approve a ticket's plan" }],
		query: async (q) => {
			queries.push(q);
			return { ok: true, result: { view: q.what } };
		},
	});
	const page = () => {
		const got: ToPage[] = [];
		const p = { send: (msg: ToPage) => got.push(msg) };
		m.join(p);
		return { p, got };
	};
	return { m, page, sent, themes, queries, stops: () => stops };
}

const note = (id: string, text: string): Item => ({ kind: "note", id, text });
const nobody: Page = { send: () => {} };

test("a page that opens late gets the chat as it stands, then only what changes", () => {
	const { m, page } = setup();
	m.setItems([note("1", "one")]);
	m.setBusy(true);
	const q = m.ask({ kind: "confirm", title: "Remove ACM-12?", message: "Close its tabs." });
	const { got } = page();
	assert.deepEqual(got, [
		{ t: "hello", repo: "app", theme: "", busy: true, items: [note("1", "one")], live: [], dialogs: [{ kind: "confirm", title: "Remove ACM-12?", message: "Close its tabs.", id: q.id }], commands: [{ name: "approve", description: "Approve a ticket's plan" }], board: undefined },
	]);
	m.setItems([note("1", "one"), note("2", "two")]);
	m.setItems([note("1", "one"), note("2", "two")]);
	m.setLive([note("live", "Thin")]);
	m.setLive([]);
	m.setLive([]);
	assert.deepEqual(got.slice(1), [
		{ t: "items", from: 1, items: [note("2", "two")] },
		{ t: "live", items: [note("live", "Thin")] },
		{ t: "live", items: [] },
	]);
});

test("reset draws the items whole, as after a session switch", () => {
	const { m, page } = setup();
	m.setItems([note("1", "one")]);
	const { got } = page();
	m.reset([note("9", "nine")]);
	assert.deepEqual(got.slice(1), [{ t: "items", from: 0, items: [note("9", "nine")] }]);
});

test("the first page to answer wins, and every page closes the question", async () => {
	const { m, page } = setup();
	const a = page();
	const b = page();
	const q = m.ask({ kind: "ask", title: "Merge it?", options: [{ label: "Merge now" }, { label: "Not yet" }], other: false });
	m.receive({ t: "answer", id: q.id, value: { answer: "Ship it", typed: true } }, nobody);
	m.receive({ t: "answer", id: q.id, value: { answer: "Delete", typed: false } }, nobody);
	m.receive({ t: "answer", id: q.id, value: { answer: "Merge now", typed: false } }, nobody);
	m.receive({ t: "answer", id: q.id, value: { answer: "Not yet", typed: false } }, nobody);
	assert.deepEqual(await q.answer, { answer: "Merge now", typed: false });
	for (const pg of [a, b]) assert.deepEqual(pg.got.slice(-1), [{ t: "closed", id: q.id }]);
	assert.equal(b.got.filter((g) => g.t === "closed").length, 1);
});

test("each kind of question takes only its kind of answer, and null cancels", async () => {
	const { m } = setup();
	const c = m.ask({ kind: "confirm", title: "Start ACM-12 and ACM-13?" });
	m.receive({ t: "answer", id: c.id, value: "yes" }, nobody);
	m.receive({ t: "answer", id: c.id, value: true }, nobody);
	assert.equal(await c.answer, true);
	const i = m.ask({ kind: "input", title: "What did you see?" });
	m.receive({ t: "answer", id: i.id, value: 3 }, nobody);
	m.receive({ t: "answer", id: i.id, value: null }, nobody);
	assert.equal(await i.answer, undefined);
	const o = m.ask({ kind: "ask", title: "Merge it?", options: [{ label: "Merge now" }], other: true });
	m.receive({ t: "answer", id: o.id, value: { answer: "  ", typed: true } }, nobody);
	m.receive({ t: "answer", id: o.id, value: { answer: " after lunch ", typed: true } }, nobody);
	assert.deepEqual(await o.answer, { answer: "after lunch", typed: true });
});

test("a question answered in the terminal leaves the pages, and a late answer from a page does nothing", () => {
	const { m, page } = setup();
	const { got } = page();
	const q = m.ask({ kind: "confirm", title: "Quit now?" });
	m.close(q.id);
	m.close(q.id);
	m.receive({ t: "answer", id: q.id, value: true }, nobody);
	assert.deepEqual(got.slice(1), [
		{ t: "dialog", dialog: { kind: "confirm", title: "Quit now?", id: q.id } },
		{ t: "closed", id: q.id },
	]);
});

test("what a page sends is checked before the chat sees it", () => {
	const { m, page, sent, themes, stops } = setup();
	const { got } = page();
	const png: Image = { type: "image", data: "iVBOR", mimeType: "image/png" };
	m.receive({ t: "send", text: "start ACM-12" }, nobody);
	m.receive({ t: "send", text: "  ", images: [] }, nobody);
	m.receive({ t: "send", text: "", images: [png, { type: "image", data: "x", mimeType: "text/html" }, { type: "image", data: "", mimeType: "image/png" }] }, nobody);
	m.receive({ t: "stop" }, nobody);
	m.receive({ t: "theme", theme: "purple" }, nobody);
	m.receive({ t: "theme", theme: "dark" }, nobody);
	m.receive("garbage", nobody);
	m.receive(null, nobody);
	m.receive({ t: "unknown" }, nobody);
	assert.deepEqual(sent, [
		{ text: "start ACM-12", images: [], page: nobody },
		{ text: "", images: [png], page: nobody },
	]);
	assert.equal(stops(), 1);
	assert.deepEqual(themes, ["dark"]);
	assert.equal(m.theme, "dark");
	assert.deepEqual(got.slice(1), [{ t: "theme", theme: "dark" }]);
});

test("a page that leaves hears nothing more", () => {
	const { m, page } = setup();
	const { p, got } = page();
	m.leave(p);
	m.notify("tkt v0.7.0 is available");
	assert.equal(got.length, 1);
});

test("a page's question to the chat is checked, and only that page hears the answer", async () => {
	const { m, page, queries } = setup();
	const a = page();
	const b = page();
	m.receive({ t: "query", id: 1, what: "diff", ticket: "ACM-12" }, a.p);
	m.receive({ t: "query", id: 2, what: "stats", ticket: "" }, a.p);
	m.receive({ t: "query", id: 3, what: "diff", ticket: "--output=/tmp/x" }, a.p);
	m.receive({ t: "query", id: 4, what: "diff", ticket: "" }, a.p);
	m.receive({ t: "query", id: 5, what: "stats", ticket: "ACM-12" }, a.p);
	m.receive({ t: "query", id: 6, what: "rm", ticket: "ACM-12" }, a.p);
	m.receive({ t: "query", id: "7", what: "diff", ticket: "ACM-12" }, a.p);
	m.receive({ t: "query", id: 8, what: "screen", ticket: "chore/nightly-1" }, a.p);
	await new Promise((r) => setTimeout(r, 0));
	assert.deepEqual(queries, [
		{ what: "diff", ticket: "ACM-12" },
		{ what: "stats", ticket: "" },
		{ what: "screen", ticket: "chore/nightly-1" },
	]);
	assert.deepEqual(
		a.got.filter((g) => g.t === "result"),
		[
			{ t: "result", id: 1, value: { ok: true, result: { view: "diff" } } },
			{ t: "result", id: 2, value: { ok: true, result: { view: "stats" } } },
			{ t: "result", id: 8, value: { ok: true, result: { view: "screen" } } },
		],
	);
	assert.equal(b.got.filter((g) => g.t === "result").length, 0);
});

test("the board goes to every page when it changes, and to a page that opens late", () => {
	const { m, page } = setup();
	const a = page();
	const board: Board = { rows: [], items: [], jobs: { "ACM-12": "approve" }, incidents: [], watch: "on" };
	m.setBoard(board);
	m.setBoard({ ...board, jobs: { "ACM-12": "approve" } });
	m.setBoard({ ...board, jobs: {} });
	assert.deepEqual(
		a.got.filter((g) => g.t === "board").map((g) => (g as { board: Board }).board.jobs),
		[{ "ACM-12": "approve" }, {}],
	);
	const late = page();
	assert.deepEqual((late.got[0] as { board?: Board }).board?.jobs, {});
});

test("a checklist takes one mark per step, and nothing else", async () => {
	const { m } = setup();
	const steps = [
		{ n: 1, text: "Open the login page." },
		{ n: 2, text: "Sign in." },
	];
	const q = m.ask({ kind: "checklist", title: "ACM-12: human verification", steps, marks: ["", ""] });
	m.receive({ t: "answer", id: q.id, value: ["pass"] }, nobody);
	m.receive({ t: "answer", id: q.id, value: ["pass", "maybe"] }, nobody);
	m.receive({ t: "answer", id: q.id, value: "pass" }, nobody);
	m.receive({ t: "answer", id: q.id, value: ["pass", "fail"] }, nobody);
	assert.deepEqual(await q.answer, ["pass", "fail"]);
});
