import assert from "node:assert/strict";
import { test } from "node:test";
import { Painter } from "../paint.ts";

const bg = "\x1b[48;2;10;10;10m";
const enter = "\x1b[?1049h";
const exit = "\x1b[?1049l";

function dark(): Painter {
	const p = new Painter();
	p.background = () => bg;
	return p;
}

test("the painter leaves the main screen alone", () => {
	const p = dark();
	const line = "\x1b[2Khello\x1b[0m\r\n";
	assert.equal(p.paint(line), line);
});

test("on the alternate screen the background stays on after every reset", () => {
	const p = dark();
	assert.equal(p.paint(`${enter}\x1b[2J\x1b[H`), `${enter}${bg}\x1b[2J\x1b[H`);
	assert.equal(p.paint("\x1b[1;1H\x1b[2Kplain\x1b[0m\x1b]8;;\x07"), `\x1b[1;1H\x1b[2Kplain\x1b[0m${bg}\x1b]8;;\x07`);
	// A panel's own background holds until it ends, then the chat's comes back.
	assert.equal(p.paint("\x1b[48;2;30;30;30mme\x1b[49m \x1b[38;2;0;0;0mx\x1b[39m\x1b[m"), `\x1b[48;2;30;30;30mme\x1b[49m${bg} \x1b[38;2;0;0;0mx\x1b[39m\x1b[m${bg}`);
	assert.equal(p.paint(`${exit}\x1b[?25h`), `${exit}\x1b[49m\x1b[?25h`);
	assert.equal(p.paint("after\x1b[0m"), "after\x1b[0m");
});

test("without a background the painter changes nothing", () => {
	const p = new Painter();
	const frame = `${enter}\x1b[2Jtext\x1b[0m${exit}`;
	assert.equal(p.paint(frame), frame);
});

test("a background that starts or stops on the alternate screen applies from the next write", () => {
	const p = new Painter();
	p.paint(`${enter}\x1b[2J`);
	p.background = () => bg;
	assert.equal(p.paint("\x1b[2J"), `${bg}\x1b[2J`);
	assert.equal(p.paint("x\x1b[0m"), `x\x1b[0m${bg}`);
	p.background = () => undefined;
	assert.equal(p.paint("x\x1b[0m"), "\x1b[49mx\x1b[0m");
});
