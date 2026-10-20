// The extension's views: a question with fixed answers, the /board
// overlay, the /peek screen and the verification checklist. The question
// view follows pi's examples/extensions/question.ts (MIT, Mario Zechner).

import type { ExtensionContext, Theme } from "@earendil-works/pi-coding-agent";
import { Editor, type EditorTheme, Key, matchesKey, truncateToWidth, wrapTextWithAnsi } from "@earendil-works/pi-tui";
import { boardColumns, grid, order } from "./board.ts";
import type { Row } from "./model.ts";
import { cycle, type Mark } from "./report.ts";

export interface Option {
	label: string;
	description?: string;
}

/** ask shows a question with fixed answers and, when other is set, a last
 * answer you type yourself. It returns the label you picked or the text you
 * typed, or undefined when you pressed Escape. */
export async function ask(ctx: ExtensionContext, question: string, options: Option[], other = true): Promise<{ answer: string; typed: boolean } | undefined> {
	if (!ctx.hasUI) return undefined;
	const all: (Option & { isOther?: boolean })[] = other ? [...options, { label: "Type something.", isOther: true }] : options;
	return ctx.ui.custom<{ answer: string; typed: boolean } | undefined>((tui, theme, _kb, done) => {
		let index = 0;
		let editing = false;
		let cached: string[] | undefined;
		const editorTheme: EditorTheme = {
			borderColor: (s) => theme.fg("accent", s),
			selectList: {
				selectedPrefix: (t) => theme.fg("accent", t),
				selectedText: (t) => theme.fg("accent", t),
				description: (t) => theme.fg("muted", t),
				scrollInfo: (t) => theme.fg("dim", t),
				noMatch: (t) => theme.fg("warning", t),
			},
		};
		const editor = new Editor(tui, editorTheme);
		editor.onSubmit = (value) => {
			const text = value.trim();
			if (text) done({ answer: text, typed: true });
			else {
				editing = false;
				refresh();
			}
		};
		const refresh = () => {
			cached = undefined;
			tui.requestRender();
		};
		return {
			handleInput(data: string) {
				if (editing) {
					if (matchesKey(data, Key.escape)) {
						editing = false;
						editor.setText("");
					} else editor.handleInput(data);
					refresh();
					return;
				}
				if (matchesKey(data, Key.up)) index = Math.max(0, index - 1);
				else if (matchesKey(data, Key.down)) index = Math.min(all.length - 1, index + 1);
				else if (matchesKey(data, Key.enter)) {
					const o = all[index];
					if (o.isOther) editing = true;
					else return done({ answer: o.label, typed: false });
				} else if (matchesKey(data, Key.escape)) return done(undefined);
				else if (/^[1-9]$/.test(data) && Number(data) <= all.length) {
					const o = all[Number(data) - 1];
					if (o.isOther) editing = true;
					else return done({ answer: o.label, typed: false });
				}
				refresh();
			},
			render(width: number): string[] {
				if (cached) return cached;
				const w = Math.max(1, width);
				const lines = [theme.fg("accent", "-".repeat(w))];
				for (const l of wrapTextWithAnsi(theme.fg("text", question), w - 1)) lines.push(` ${l}`);
				lines.push("");
				all.forEach((o, i) => {
					const sel = i === index;
					const label = `${sel ? ">" : " "} ${i + 1}. ${o.label}`;
					lines.push(truncateToWidth(theme.fg(sel ? "accent" : "text", label), w));
					if (o.description) for (const l of wrapTextWithAnsi(theme.fg("muted", o.description), w - 5)) lines.push(`     ${l}`);
				});
				if (editing) {
					lines.push("", ` ${theme.fg("muted", "Your answer:")}`);
					for (const l of editor.render(Math.max(1, w - 2))) lines.push(` ${l}`);
				}
				lines.push("", ` ${theme.fg("dim", editing ? "Enter to send, Esc to go back" : "Up and Down to move, Enter or a number to pick, Esc to cancel")}`);
				lines.push(theme.fg("accent", "-".repeat(w)));
				cached = lines;
				return lines;
			},
			invalidate() {
				cached = undefined;
			},
		};
	});
}

export type BoardAction = { key: "show" | "peek" | "check" | "approve" | "next" | "auto" | "done" | "inbox"; ticket: string };

const boardKeys: Record<string, BoardAction["key"]> = { p: "peek", c: "check", a: "approve", n: "next", o: "auto", d: "done", i: "inbox" };

/** board shows the tickets in columns and returns what you picked for the
 * selected one. */
export function board(ctx: ExtensionContext, rows: () => Row[]): Promise<BoardAction | undefined> {
	return ctx.ui.custom<BoardAction | undefined>(
		(tui, theme, _kb, done) => {
			let selected = "";
			const timer = setInterval(() => tui.requestRender(), 1000);
			return {
				handleInput(data: string) {
					const ids = order(boardColumns(rows(), Date.now()));
					const at = Math.max(0, ids.indexOf(selected));
					if (matchesKey(data, Key.escape) || data === "q") return done(undefined);
					if (matchesKey(data, Key.down) || matchesKey(data, Key.right)) selected = ids[Math.min(ids.length - 1, at + 1)] ?? "";
					else if (matchesKey(data, Key.up) || matchesKey(data, Key.left)) selected = ids[Math.max(0, at - 1)] ?? "";
					else if (matchesKey(data, Key.enter) && selected) return done({ key: "show", ticket: selected });
					else if (boardKeys[data] && (selected || data === "i")) return done({ key: boardKeys[data], ticket: selected });
					tui.requestRender();
				},
				render(width: number): string[] {
					const cols = boardColumns(rows(), Date.now());
					if (!selected) selected = order(cols)[0] ?? "";
					const w = Math.max(20, width);
					const lines = [theme.fg("accent", truncateToWidth(" tkt board", w))];
					for (const l of grid(cols, w, selected)) lines.push(truncateToWidth(l.includes(">") ? theme.fg("accent", l) : l, w));
					if (cols.every((c) => c.cells.length === 0)) lines.push(theme.fg("muted", " No ticket worktrees. Say \"start ACM-123\" to begin."));
					lines.push("", theme.fg("dim", truncateToWidth(" arrows move  enter plan  p peek  c check  a approve  n next  o auto on/off  d done  i inbox  esc close", w)));
					return lines;
				},
				invalidate() {},
				dispose() {
					clearInterval(timer);
				},
			};
		},
		{ overlay: true, overlayOptions: { width: "95%", maxHeight: "80%", anchor: "center" } },
	);
}

/** peek shows the agent's live screen, read again every two seconds, until
 * you press Escape. It only reads: you answer in the ticket's tab. */
export function peek(ctx: ExtensionContext, ticket: string, read: () => Promise<string>): Promise<void> {
	return ctx.ui.custom<void>(
		(tui, theme, _kb, done) => {
			let screen = "reading the screen...";
			let alive = true;
			const load = async () => {
				const s = await read();
				if (!alive) return;
				screen = s;
				tui.requestRender();
			};
			void load();
			const timer = setInterval(() => void load(), 2000);
			return {
				handleInput(data: string) {
					if (matchesKey(data, Key.escape) || data === "q") done(undefined);
				},
				render(width: number): string[] {
					const w = Math.max(20, width);
					const body = screen.replace(/\s+$/, "").split("\n").slice(-30);
					return [
						theme.fg("accent", truncateToWidth(` ${ticket}: the agent's screen, live`, w)),
						theme.fg("accent", "-".repeat(w)),
						...body.map((l) => truncateToWidth(l, w)),
						theme.fg("accent", "-".repeat(w)),
						theme.fg("dim", truncateToWidth(" Answer in the ticket's tab. Esc closes.", w)),
					];
				},
				invalidate() {},
				dispose() {
					alive = false;
					clearInterval(timer);
				},
			};
		},
		{ overlay: true, overlayOptions: { width: "90%", maxHeight: "80%", anchor: "center" } },
	);
}

export interface Step {
	n: number;
	text: string;
}

/** checklist lets you mark each human verification step passed, failed or
 * skipped. It returns the marks when you close it. */
export function checklist(ctx: ExtensionContext, ticket: string, steps: Step[], start: Mark[]): Promise<Mark[]> {
	return ctx.ui.custom<Mark[]>(
		(tui, theme, _kb, done) => {
			const marks = steps.map((_, i) => start[i] ?? "");
			let index = 0;
			const sign = (m: Mark) => (m === "pass" ? theme.fg("success", "[pass]") : m === "fail" ? theme.fg("error", "[FAIL]") : m === "skip" ? theme.fg("muted", "[skip]") : "[    ]");
			return {
				handleInput(data: string) {
					if (matchesKey(data, Key.escape) || matchesKey(data, Key.enter) || data === "q") return done(marks);
					if (matchesKey(data, Key.up)) index = Math.max(0, index - 1);
					else if (matchesKey(data, Key.down)) index = Math.min(steps.length - 1, index + 1);
					else if (data === " ") marks[index] = cycle(marks[index]);
					else if (data === "p") marks[index] = "pass";
					else if (data === "f") marks[index] = "fail";
					else if (data === "s") marks[index] = "skip";
					tui.requestRender();
				},
				render(width: number): string[] {
					const w = Math.max(20, width);
					const lines = [theme.fg("accent", truncateToWidth(` ${ticket}: human verification`, w)), theme.fg("accent", "-".repeat(w))];
					steps.forEach((s, i) => {
						const head = `${i === index ? ">" : " "} ${sign(marks[i])} ${s.n}. `;
						const wrapped = wrapTextWithAnsi(s.text, Math.max(10, w - 13));
						wrapped.forEach((l, j) => lines.push(truncateToWidth((j === 0 ? head : " ".repeat(12)) + (i === index ? theme.fg("accent", l) : l), w)));
					});
					lines.push(theme.fg("accent", "-".repeat(w)));
					lines.push(theme.fg("dim", truncateToWidth(" space cycles  p pass  f fail  s skip  enter or esc closes", w)));
					return lines;
				},
				invalidate() {},
			};
		},
		{ overlay: true, overlayOptions: { width: "90%", maxHeight: "85%", anchor: "center" } },
	);
}

/** tone colors a board line. */
export function paint(theme: Theme, tone: string, text: string): string {
	switch (tone) {
		case "warning":
			return theme.fg("warning", text);
		case "error":
			return theme.fg("error", text);
		case "muted":
			return theme.fg("muted", text);
		case "accent":
			return theme.fg("accent", text);
		case "success":
			return theme.fg("success", text);
	}
	return text;
}
