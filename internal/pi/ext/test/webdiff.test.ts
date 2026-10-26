import assert from "node:assert/strict";
import { test } from "node:test";
import { type File, lines, section, total } from "../web/diff.ts";

test("each line of a hunk gets its numbers in the old and the new file", () => {
	const h = { header: "@@ -10,3 +10,4 @@ func f() {", lines: [" a", "-b", "+B", "+C", " d", "\\ No newline at end of file"] };
	assert.deepEqual(lines(h), [
		{ kind: "ctx", old: 10, new: 10, text: "a" },
		{ kind: "del", old: 11, text: "b" },
		{ kind: "add", new: 11, text: "B" },
		{ kind: "add", new: 12, text: "C" },
		{ kind: "ctx", old: 12, new: 13, text: "d" },
		{ kind: "note", text: "No newline at end of file" },
	]);
	assert.equal(section(h), "func f() {");
	assert.deepEqual(lines({ header: "@@ -0,0 +1 @@", lines: ["+hello"] }), [{ kind: "add", new: 1, text: "hello" }]);
});

test("the totals sum the files", () => {
	const f = (added: number, removed: number): File => ({ path: "x", status: "modified", added, removed, binary: false, hunks: [] });
	assert.deepEqual(total([f(3, 1), f(2, 0)]), { added: 5, removed: 1 });
	assert.deepEqual(total([]), { added: 0, removed: 0 });
});
