import assert from "node:assert/strict";
import { test } from "node:test";
import { color256, look, parseAnsi } from "../web/ansi.ts";

test("colors and weights become spans, and other escapes go", () => {
	const screen = "\x1b[0m\x1b[1m\x1b[38;5;2mroger@yams\x1b[0m:\x1b[1;34m~\x1b[0m$ ls\r\n\x1b]0;title\x07\x1b[2Kplain \x1b[7minverse\x1b[27m\r\n\r\n";
	assert.deepEqual(parseAnsi(screen), [
		[{ text: "roger@yams", bold: true, fg: 2 }, { text: ":" }, { text: "~", bold: true, fg: 4 }, { text: "$ ls" }],
		[{ text: "plain " }, { text: "inverse", inverse: true }],
	]);
});

test("a style runs on to the next line, as in a terminal", () => {
	assert.deepEqual(parseAnsi("\x1b[31mone\ntwo\x1b[39m three"), [[{ text: "one", fg: 1 }], [{ text: "two", fg: 1 }, { text: " three" }]]);
});

test("the 256 colors and true color become the page's colors", () => {
	assert.deepEqual(parseAnsi("\x1b[38;2;80;250;123;48;5;236mok\x1b[0m"), [[{ text: "ok", fg: "#50fa7b", bg: "#303030" }]]);
	assert.deepEqual(parseAnsi("\x1b[38:2::1:2:3mx"), [[{ text: "x", fg: "#010203" }]]);
	assert.deepEqual(parseAnsi("\x1b[95;101mx"), [[{ text: "x", fg: 13, bg: 9 }]]);
	assert.equal(color256(9), 9);
	assert.equal(color256(16), "#000000");
	assert.equal(color256(231), "#ffffff");
	assert.equal(color256(232), "#080808");
});

test("a span draws with classes for the terminal's colors and a style for the rest", () => {
	assert.deepEqual(look({ text: "a", fg: 2, bold: true }), { cls: "fg2 b", style: {} });
	assert.deepEqual(look({ text: "a", fg: "#50fa7b", underline: true }), { cls: "u", style: { color: "#50fa7b" } });
	assert.deepEqual(look({ text: "a", inverse: true }), { cls: "fg-bg bg-fg", style: {} });
	assert.deepEqual(look({ text: "a", inverse: true, fg: 1, dim: true }), { cls: "fg-bg bg1 d", style: {} });
});
