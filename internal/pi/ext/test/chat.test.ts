import assert from "node:assert/strict";
import { test } from "node:test";
import { type Entry, itemsOf, messageItems, patch, slashName } from "../chat.ts";

const at = Date.parse("2026-10-25T10:00:00");

test("itemsOf reads prompts, answers, tool calls with their results, tkt's lines and the times", () => {
	const entries: Entry[] = [
		{ type: "message", id: "s1", message: { role: "system", content: "You are tkt." } },
		{ type: "message", id: "u1", message: { role: "user", content: [{ type: "text", text: "start ACM-12" }, { type: "image", data: "x", mimeType: "image/png" }] } },
		{ type: "custom", id: "t1", customType: "tkt-stamp", data: { at } },
		{
			type: "message",
			id: "a1",
			message: {
				role: "assistant",
				content: [
					{ type: "thinking", thinking: "The user wants ACM-12." },
					{ type: "text", text: "Starting it." },
					{ type: "toolCall", id: "call1", name: "tkt_start", arguments: { tickets: ["ACM-12"], mode: "manual" } },
				],
			},
		},
		{ type: "message", id: "r1", message: { role: "toolResult", toolCallId: "call1", content: [{ type: "text", text: "Queued: ACM-12 starting." }] } },
		{ type: "custom_message", id: "n1", customType: "tkt", content: "Approved ACM-12's plan.", display: true },
		{ type: "custom_message", id: "n2", customType: "tkt", content: "[board] Nothing needs the user now.", display: false },
		{ type: "custom", id: "c1", customType: "tkt-check", data: { key: "ACM-12@abc", marks: [] } },
		{ type: "usage", id: "x1" },
		{ type: "model_change", id: "m1", provider: "acme", modelId: "large" },
	];
	assert.deepEqual(itemsOf(entries, at), [
		{ kind: "user", id: "u1", text: "start ACM-12", images: 1 },
		{ kind: "stamp", id: "t1", text: "10:00:00" },
		{ kind: "assistant", id: "a1", text: "Starting it.", thinking: "The user wants ACM-12.", error: "" },
		{ kind: "tool", id: "call1", name: "tkt_start", args: 'tickets: ["ACM-12"], mode: manual', result: "Queued: ACM-12 starting.", error: false, done: true },
		{ kind: "note", id: "n1", text: "Approved ACM-12's plan." },
		{ kind: "info", id: "m1", text: "Model: acme/large" },
	]);
});

test("a tool call waits for its result, and a failed one says so", () => {
	const call = { type: "message", id: "a1", message: { role: "assistant", content: [{ type: "toolCall", id: "c1", name: "tkt_done", arguments: { ticket: "ACM-12" } }] } };
	assert.deepEqual(itemsOf([call], at), [{ kind: "tool", id: "c1", name: "tkt_done", args: "ticket: ACM-12", result: "", error: false, done: false }]);
	const failed = { type: "message", id: "r1", message: { role: "toolResult", toolCallId: "c1", isError: true, content: [{ type: "text", text: "the pull request is open" }] } };
	assert.deepEqual(itemsOf([call, failed], at)[0], { kind: "tool", id: "c1", name: "tkt_done", args: "ticket: ACM-12", result: "the pull request is open", error: true, done: true });
});

test("a long tool result is cut, and the cut says how much", () => {
	const call = { type: "message", id: "a1", message: { role: "assistant", content: [{ type: "toolCall", id: "c1", name: "tkt_status", arguments: {} }] } };
	const result = { type: "message", id: "r1", message: { role: "toolResult", toolCallId: "c1", content: [{ type: "text", text: "x".repeat(4010) }] } };
	const item = itemsOf([call, result], at)[0] as { result: string };
	assert.equal(item.result, `${"x".repeat(4000)}\n[10 more characters]`);
});

test("an answer that failed or stopped says so; an empty one draws nothing", () => {
	assert.deepEqual(messageItems({ role: "assistant", content: [], stopReason: "error", errorMessage: "rate limited" }, "a"), [{ kind: "assistant", id: "a", text: "", thinking: "", error: "rate limited" }]);
	assert.deepEqual(messageItems({ role: "assistant", content: [{ type: "text", text: "Half" }], stopReason: "aborted" }, "a"), [{ kind: "assistant", id: "a", text: "Half", thinking: "", error: "Stopped." }]);
	assert.deepEqual(messageItems({ role: "assistant", content: [{ type: "text", text: " " }] }, "a"), []);
	assert.deepEqual(messageItems({ role: "bashExecution", content: "ls" }, "a"), []);
});

test("patch sends the tail from the first item that changed", () => {
	const a = { kind: "note" as const, id: "1", text: "one" };
	const b = { kind: "note" as const, id: "2", text: "two" };
	const first = patch([], [a, b]);
	assert.deepEqual(first?.patch, { from: 0, items: [a, b] });
	assert.equal(patch(first!.keys, [a, b]), undefined);
	const c = { ...b, text: "two, done" };
	assert.deepEqual(patch(first!.keys, [a, c])?.patch, { from: 1, items: [c] });
	assert.deepEqual(patch(first!.keys, [a])?.patch, { from: 1, items: [] });
});

test("slashName reads the command a line names", () => {
	assert.equal(slashName("/approve ACM-12"), "approve");
	assert.equal(slashName("  /Model"), "model");
	assert.equal(slashName("/scoped-models"), "scoped-models");
	assert.equal(slashName("/tmp/x is full"), "");
	assert.equal(slashName("start ACM-12"), "");
});
