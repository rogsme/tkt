// The chat as the web page draws it: the session's entries read into items,
// and what changed since the page last heard. It imports nothing from pi,
// so node --test covers it. What only the model reads, such as the system
// prompt and the board notes, never reaches the page.

import { type Stamp, stampText } from "./model.ts";

/** Item is one thing the page draws. A tool call carries its result once
 * the result arrives; done says it has. */
export type Item =
	| { kind: "user"; id: string; text: string; images: number }
	| { kind: "assistant"; id: string; text: string; thinking: string; error: string }
	| { kind: "tool"; id: string; name: string; args: string; result: string; error: boolean; done: boolean }
	| { kind: "note"; id: string; text: string }
	| { kind: "stamp"; id: string; text: string }
	| { kind: "info"; id: string; text: string };

/** Entry is the part of a pi session entry the page needs. */
export interface Entry {
	type: string;
	id: string;
	customType?: string;
	data?: unknown;
	content?: unknown;
	display?: boolean;
	message?: Message;
	provider?: string;
	modelId?: string;
}

/** Message is the part of a pi message the page needs. */
export interface Message {
	role: string;
	content?: unknown;
	toolCallId?: string;
	isError?: boolean;
	stopReason?: string;
	errorMessage?: string;
}

/** maxResult and maxArgs bound what a tool call shows, so a long tkt ls
 * does not swamp the page. */
const maxResult = 4000;
const maxArgs = 300;

/** itemsOf reads the entries on the session's branch, oldest first. */
export function itemsOf(entries: Entry[], now: number): Item[] {
	const items: Item[] = [];
	const tools = new Map<string, Extract<Item, { kind: "tool" }>>();
	for (const e of entries) {
		switch (e.type) {
			case "message": {
				const m = e.message;
				if (!m) break;
				if (m.role === "toolResult") {
					const t = m.toolCallId ? tools.get(m.toolCallId) : undefined;
					if (t) Object.assign(t, { result: clip(textOf(m.content), maxResult), error: !!m.isError, done: true });
					break;
				}
				for (const it of messageItems(m, e.id)) {
					if (it.kind === "tool") tools.set(it.id, it);
					items.push(it);
				}
				break;
			}
			case "custom_message":
				if (e.display && e.customType === "tkt") items.push({ kind: "note", id: e.id, text: textOf(e.content) });
				break;
			case "custom":
				if (e.customType === "tkt-stamp" && isStamp(e.data)) items.push({ kind: "stamp", id: e.id, text: stampText(e.data, now) });
				break;
			case "model_change":
				items.push({ kind: "info", id: e.id, text: `Model: ${e.provider}/${e.modelId}` });
				break;
			case "compaction":
				items.push({ kind: "info", id: e.id, text: "The chat above was compacted: the model now reads a summary of it." });
				break;
			case "branch_summary":
				items.push({ kind: "info", id: e.id, text: "The chat moved to another branch." });
				break;
		}
	}
	return items;
}

/** messageItems reads one message: a prompt, or an answer with its tool
 * calls, such as the one streaming in. Other roles draw nothing. */
export function messageItems(m: Message, id: string): Item[] {
	if (m.role === "user") {
		const blocks = Array.isArray(m.content) ? m.content : [];
		const images = blocks.filter((b) => (b as { type?: string })?.type === "image").length;
		return [{ kind: "user", id, text: textOf(m.content), images }];
	}
	if (m.role !== "assistant") return [];
	const blocks = (Array.isArray(m.content) ? m.content : []) as { type?: string; text?: string; thinking?: string; id?: string; name?: string; arguments?: unknown }[];
	const text = blocks.filter((b) => b.type === "text").map((b) => b.text ?? "").join("");
	const thinking = blocks.filter((b) => b.type === "thinking").map((b) => b.thinking ?? "").join("");
	let error = "";
	if (m.stopReason === "error") error = m.errorMessage || "The model failed.";
	else if (m.stopReason === "aborted") error = "Stopped.";
	const out: Item[] = [];
	if (text.trim() || thinking.trim() || error) out.push({ kind: "assistant", id, text, thinking, error });
	for (const b of blocks) {
		if (b.type !== "toolCall" || !b.id) continue;
		out.push({ kind: "tool", id: b.id, name: b.name ?? "", args: clip(argsText(b.arguments), maxArgs), result: "", error: false, done: false });
	}
	return out;
}

/** Patch brings a page's items up to date: keep the first from items it
 * has, then add these. */
export interface Patch {
	from: number;
	items: Item[];
}

/** patch compares the items a page has, as keys, with the new ones, and
 * returns what changed, or undefined when nothing did. Items only change
 * near the end, such as a tool's result arriving, so a page gets the tail
 * from the first change on. */
export function patch(had: string[], items: Item[]): { patch: Patch; keys: string[] } | undefined {
	const keys = items.map((it) => JSON.stringify(it));
	let from = 0;
	while (from < had.length && from < keys.length && had[from] === keys[from]) from++;
	if (from === had.length && from === keys.length) return undefined;
	return { patch: { from, items: items.slice(from) }, keys };
}

/** terminalOnly are pi's own slash commands, as pi 1.3.0 names them. They
 * open pi's screens, which only the terminal can draw. */
export const terminalOnly = new Set(["settings", "model", "tree", "thinking", "scoped-models", "export", "import", "share", "bug", "copy", "name", "session", "changelog", "hotkeys", "fork", "clone", "trust", "login", "logout", "new", "compact", "resume", "reload", "quit"]);

/** slashName is the command a line names, such as "approve" for
 * "/approve ACM-12", or "" for a line that names none. */
export function slashName(text: string): string {
	const m = /^\/([a-z][\w-]*)(\s|$)/i.exec(text.trimStart());
	return m ? m[1].toLowerCase() : "";
}

/** pageView is the page's own view a slash command opens, such as the
 * agent's screen for /peek ACM-12, or undefined for a command that runs in
 * the chat. The terminal draws these views as overlays, which a page
 * cannot show. */
export function pageView(text: string, idOf: (raw: string) => string): { ticket: string; tab: string } | undefined {
	const words = text.trim().split(/\s+/);
	const name = slashName(text);
	if (name === "board" && words.length === 1) return { ticket: "", tab: "" };
	if (name === "peek" && words.length === 2) return { ticket: idOf(words[1]), tab: "screen" };
	return undefined;
}

function textOf(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	return content
		.filter((b) => (b as { type?: string })?.type === "text")
		.map((b) => (b as { text?: string }).text ?? "")
		.join("\n");
}

function argsText(args: unknown): string {
	if (args === undefined || args === null) return "";
	if (typeof args !== "object") return String(args);
	const entries = Object.entries(args as Record<string, unknown>);
	return entries.map(([k, v]) => `${k}: ${typeof v === "string" ? v : JSON.stringify(v)}`).join(", ");
}

function clip(s: string, max: number): string {
	return s.length > max ? `${s.slice(0, max)}\n[${s.length - max} more characters]` : s;
}

function isStamp(d: unknown): d is Stamp {
	return typeof (d as Stamp | undefined)?.at === "number";
}
