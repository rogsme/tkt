// The questions and notices the extension gives you go to the terminal and
// to every open web page at once. You answer in either; the first answer
// wins and closes the question in the other. Everything the tools and the
// slash commands ask goes through here, so the two places never drift.

import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Dialog, Level, Mirror, Option } from "./mirror.ts";
import { question, textEditor } from "./ui.ts";

let mirror: Mirror | undefined;

/** mirrorTo sends the questions to the pages a mirror keeps, or, with
 * undefined, to the terminal alone. */
export function mirrorTo(m: Mirror | undefined): void {
	mirror = m;
}

/** race asks in the terminal and on the pages, and takes the first answer.
 * The terminal's question closes through the signal; the pages' through
 * the mirror. */
async function race<T>(ctx: ExtensionContext, d: Omit<Dialog, "id">, terminal: (signal: AbortSignal) => Promise<T>, fromPage: (v: unknown) => T): Promise<T> {
	const ac = new AbortController();
	const m = mirror;
	if (!m || !ctx.hasUI) return terminal(ac.signal);
	const { id, answer } = m.ask(d);
	const first = await Promise.race([terminal(ac.signal).then((v) => ({ v, page: false })), answer.then((v) => ({ v: fromPage(v), page: true }))]);
	if (first.page) ac.abort();
	else m.close(id);
	return first.v;
}

export function confirm(ctx: ExtensionContext, title: string, message: string): Promise<boolean> {
	return race(ctx, { kind: "confirm", title, message }, (signal) => ctx.ui.confirm(title, message, { signal }), (v) => v === true);
}

export function input(ctx: ExtensionContext, title: string, placeholder = ""): Promise<string | undefined> {
	return race(ctx, { kind: "input", title, placeholder }, (signal) => ctx.ui.input(title, placeholder, { signal }), (v) => (typeof v === "string" ? v : undefined));
}

export function editor(ctx: ExtensionContext, title: string, prefill = ""): Promise<string | undefined> {
	return race(ctx, { kind: "editor", title, prefill }, (signal) => textEditor(ctx, title, prefill, signal), (v) => (typeof v === "string" ? v : undefined));
}

/** ask shows a question with fixed answers and, when other is set, a last
 * answer you type yourself. It returns the label you picked or the text you
 * typed, or undefined when you cancelled. */
export function ask(ctx: ExtensionContext, title: string, options: Option[], other = true): Promise<{ answer: string; typed: boolean } | undefined> {
	return race(ctx, { kind: "ask", title, options, other }, (signal) => question(ctx, title, options, other, signal), (v) => v as { answer: string; typed: boolean } | undefined);
}

/** notify shows a notice in the terminal and on the pages. */
export function notify(ctx: ExtensionContext | undefined, text: string, level: Level = "info"): void {
	if (ctx?.hasUI) ctx.ui.notify(text, level);
	mirror?.notify(text, level);
}
