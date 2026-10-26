import assert from "node:assert/strict";
import { test } from "node:test";
import type { Item } from "../chat.ts";
import { type Image, Mirror, type Theme, type ToPage } from "../mirror.ts";

function setup() {
	const sent: { text: string; images: Image[] }[] = [];
	const themes: Theme[] = [];
	let stops = 0;
	const m = new Mirror("app", {
		send: (text, images) => sent.push({ text, images }),
		stop: () => stops++,
		theme: (t) => themes.push(t),
		commands: () => [{ name: "approve", description: "Approve a ticket's plan" }],
	});
	const page = () => {
		const got: ToPage[] = [];
		const p = { send: (msg: ToPage) => got.push(msg) };
		m.join(p);
		return { p, got };
	};
	return { m, page, sent, themes, stops: () => stops };
}

const note = (id: string, text: string): Item => ({ kind: "note", id, text });

test("a page that opens late gets the chat as it stands, then only what changes", () => {
	const { m, page } = setup();
	m.setItems([note("1", "one")]);
	m.setBusy(true);
	const q = m.ask({ kind: "confirm", title: "Remove ACM-12?", message: "Close its tabs." });
	const { got } = page();
	assert.deepEqual(got, [
		{ t: "hello", repo: "app", theme: "", busy: true, items: [note("1", "one")], live: [], dialogs: [{ kind: "confirm", title: "Remove ACM-12?", message: "Close its tabs.", id: q.id }], commands: [{ name: "approve", description: "Approve a ticket's plan" }] },
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
	m.receive({ t: "answer", id: q.id, value: { answer: "Ship it", typed: true } });
	m.receive({ t: "answer", id: q.id, value: { answer: "Delete", typed: false } });
	m.receive({ t: "answer", id: q.id, value: { answer: "Merge now", typed: false } });
	m.receive({ t: "answer", id: q.id, value: { answer: "Not yet", typed: false } });
	assert.deepEqual(await q.answer, { answer: "Merge now", typed: false });
	for (const pg of [a, b]) assert.deepEqual(pg.got.slice(-1), [{ t: "closed", id: q.id }]);
	assert.equal(b.got.filter((g) => g.t === "closed").length, 1);
});

test("each kind of question takes only its kind of answer, and null cancels", async () => {
	const { m } = setup();
	const c = m.ask({ kind: "confirm", title: "Start ACM-12 and ACM-13?" });
	m.receive({ t: "answer", id: c.id, value: "yes" });
	m.receive({ t: "answer", id: c.id, value: true });
	assert.equal(await c.answer, true);
	const i = m.ask({ kind: "input", title: "What did you see?" });
	m.receive({ t: "answer", id: i.id, value: 3 });
	m.receive({ t: "answer", id: i.id, value: null });
	assert.equal(await i.answer, undefined);
	const o = m.ask({ kind: "ask", title: "Merge it?", options: [{ label: "Merge now" }], other: true });
	m.receive({ t: "answer", id: o.id, value: { answer: "  ", typed: true } });
	m.receive({ t: "answer", id: o.id, value: { answer: " after lunch ", typed: true } });
	assert.deepEqual(await o.answer, { answer: "after lunch", typed: true });
});

test("a question answered in the terminal leaves the pages, and a late answer from a page does nothing", () => {
	const { m, page } = setup();
	const { got } = page();
	const q = m.ask({ kind: "confirm", title: "Quit now?" });
	m.close(q.id);
	m.close(q.id);
	m.receive({ t: "answer", id: q.id, value: true });
	assert.deepEqual(got.slice(1), [
		{ t: "dialog", dialog: { kind: "confirm", title: "Quit now?", id: q.id } },
		{ t: "closed", id: q.id },
	]);
});

test("what a page sends is checked before the chat sees it", () => {
	const { m, page, sent, themes, stops } = setup();
	const { got } = page();
	const png: Image = { type: "image", data: "iVBOR", mimeType: "image/png" };
	m.receive({ t: "send", text: "start ACM-12" });
	m.receive({ t: "send", text: "  ", images: [] });
	m.receive({ t: "send", text: "", images: [png, { type: "image", data: "x", mimeType: "text/html" }, { type: "image", data: "", mimeType: "image/png" }] });
	m.receive({ t: "stop" });
	m.receive({ t: "theme", theme: "purple" });
	m.receive({ t: "theme", theme: "dark" });
	m.receive("garbage");
	m.receive(null);
	m.receive({ t: "unknown" });
	assert.deepEqual(sent, [
		{ text: "start ACM-12", images: [] },
		{ text: "", images: [png] },
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
