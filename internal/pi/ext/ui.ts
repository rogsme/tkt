// The extension's views: a question with fixed answers, a text editor, the
// board pinned to the top, the times in the chat, and the overlays: /board,
// the /peek screen and the verification checklist. The question view
// follows pi's examples/extensions/question.ts (MIT, Mario Zechner). The
// question and the editor close when their signal aborts, as when you
// answer on the web page instead; dialog.ts asks in both places.

import type { ExtensionContext, Theme } from "@earendil-works/pi-coding-agent";
import { type Component, Editor, type EditorTheme, Key, matchesKey, Text, truncateToWidth, visibleWidth, wrapTextWithAnsi } from "@earendil-works/pi-tui";
import { type BoardState, boardColumns, boardLines, footer, grid, order, title } from "./board.ts";
import { type Row, type Stamp, stampText } from "./model.ts";
import { cycle, type Mark } from "./report.ts";

export interface Option {
	label: string;
	description?: string;
}

/** question shows a question with fixed answers and, when other is set, a
 * last answer you type yourself. It returns the label you picked or the text
 * you typed, or undefined when you pressed Escape or signal aborted. */
export async function question(ctx: ExtensionContext, question: string, options: Option[], other: boolean, signal?: AbortSignal): Promise<{ answer: string; typed: boolean } | undefined> {
	if (!ctx.hasUI || signal?.aborted) return undefined;
	const all: (Option & { isOther?: boolean })[] = other ? [...options, { label: "Type something.", isOther: true }] : options;
	return ctx.ui.custom<{ answer: string; typed: boolean } | undefined>((tui, theme, _kb, done) => {
		closeOn(signal, () => done(undefined));
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

/** textEditor asks for a text of several lines, such as feedback on a
 * plan. It returns the text, or undefined when you pressed Escape or signal
 * aborted. */
export async function textEditor(ctx: ExtensionContext, title: string, prefill: string, signal?: AbortSignal): Promise<string | undefined> {
	if (!ctx.hasUI || signal?.aborted) return undefined;
	return ctx.ui.custom<string | undefined>((tui, theme, _kb, done) => {
		closeOn(signal, () => done(undefined));
		const editor = new Editor(tui, {
			borderColor: (s) => theme.fg("accent", s),
			selectList: {
				selectedPrefix: (t) => theme.fg("accent", t),
				selectedText: (t) => theme.fg("accent", t),
				description: (t) => theme.fg("muted", t),
				scrollInfo: (t) => theme.fg("dim", t),
				noMatch: (t) => theme.fg("warning", t),
			},
		});
		editor.setText(prefill);
		editor.onSubmit = (value) => done(value);
		return {
			handleInput(data: string) {
				if (matchesKey(data, Key.escape)) return done(undefined);
				editor.handleInput(data);
				tui.requestRender();
			},
			render(width: number): string[] {
				const w = Math.max(1, width);
				const lines = [theme.fg("accent", "-".repeat(w))];
				for (const l of wrapTextWithAnsi(theme.fg("text", title), w - 1)) lines.push(` ${l}`);
				lines.push("");
				for (const l of editor.render(Math.max(1, w - 2))) lines.push(` ${l}`);
				lines.push("", ` ${theme.fg("dim", "Enter to send, Shift+Enter for a new line, Esc to cancel")}`);
				lines.push(theme.fg("accent", "-".repeat(w)));
				return lines;
			},
			invalidate() {},
		};
	});
}

/** closeOn calls close once signal aborts. */
function closeOn(signal: AbortSignal | undefined, close: () => void): void {
	signal?.addEventListener("abort", close, { once: true });
}

/** frame draws a box around lines, with a title in its top border and a
 * hint in its bottom one. Every line is padded to the full width, so an
 * overlay covers what is under it and reads as one panel. */
export function frame(theme: Theme, width: number, title: string, lines: string[], hint = ""): string[] {
	const w = Math.max(10, width);
	const inner = w - 4;
	const b = (s: string) => theme.fg("border", s);
	const edge = (left: string, label: string, right: string, color: (s: string) => string) => {
		const text = label ? truncateToWidth(` ${label} `, inner) : "";
		return b(left + "─") + color(text) + b("─".repeat(Math.max(0, w - 3 - visibleWidth(text))) + right);
	};
	const out = [edge("╭", title, "╮", (s) => theme.fg("accent", theme.bold(s)))];
	for (const l of lines) {
		const cut = truncateToWidth(l, inner);
		out.push(b("│") + " " + cut + " ".repeat(Math.max(0, inner - visibleWidth(cut))) + " " + b("│"));
	}
	out.push(edge("╰", hint, "╯", (s) => theme.fg("dim", s)));
	return out;
}

/** hud is the board pinned to the top of the chat. It takes no keys. It
 * keeps to two fifths of the terminal's rows, so the chat keeps the rest,
 * and gives way on a terminal shorter than minRows. */
export function hud(theme: Theme, state: () => BoardState, rows: () => number): Component {
	return {
		render(width: number): string[] {
			const h = rows();
			if (h < minRows) return [];
			const s = state();
			let lines = boardLines(s, Math.max(10, width - 4));
			const max = Math.max(2, Math.floor(h * 0.4) - 2);
			if (lines.length > max) lines = [...lines.slice(0, max - 1), { text: `${lines.length - max + 1} more lines: /board shows every ticket, /inbox what needs you`, tone: "muted" }];
			return frame(theme, width, title(s), lines.map((l) => paint(theme, l.tone, l.text)), footer(s));
		},
		invalidate() {},
	};
}

/** minRows is the shortest terminal the pinned board shows on. */
const minRows = 16;

/** panel paints an overlay's lines on a background of their own, so the
 * overlay reads as a solid box on a transparent terminal. pi's themes have
 * no token for it; toolPendingBg is the neutral panel in each of them and
 * in tkt's. Styled text resets the background as it ends, so the panel's
 * goes back on after each reset. */
export function panel(theme: Theme, lines: string[]): string[] {
	const on = theme.getBgAnsi("toolPendingBg");
	return lines.map((l) => on + l.replace(/\x1b\[(?:0|49)?m/g, (m) => m + on) + "\x1b[49m");
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
					const w = Math.max(24, width);
					const body = grid(cols, w - 4, selected).map((l) => (l.includes(">") ? theme.fg("accent", l) : l));
					if (cols.every((c) => c.cells.length === 0)) body.push(theme.fg("muted", 'No tickets yet. Say "start ACM-123".'));
					return panel(theme, frame(theme, w, "tkt board", body, "arrows move  enter plan  p peek  c check  a approve  n next  o auto  d done  i inbox  esc close"));
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
					const rows = Math.max(8, (process.stdout.rows || 40) - 12);
					const body = screen.replace(/\s+$/, "").split("\n").slice(-rows);
					return panel(theme, frame(theme, width, `${ticket}: the agent's screen, live`, body, "answer in the ticket's tab  esc closes"));
				},
				invalidate() {},
				dispose() {
					alive = false;
					clearInterval(timer);
				},
			};
		},
		{ overlay: true, overlayOptions: { width: "90%", maxHeight: "85%", anchor: "center" } },
	);
}

export interface Step {
	n: number;
	text: string;
}

/** checklist lets you mark each human verification step passed, failed or
 * skipped. It returns the marks when you close it, or undefined when
 * signal aborted. */
export function checklist(ctx: ExtensionContext, ticket: string, steps: Step[], start: Mark[], from = "", signal?: AbortSignal): Promise<Mark[] | undefined> {
	if (signal?.aborted) return Promise.resolve(undefined);
	return ctx.ui.custom<Mark[] | undefined>(
		(tui, theme, _kb, done) => {
			closeOn(signal, () => done(undefined));
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
					const w = Math.max(24, width);
					const lines: string[] = [];
					if (from) lines.push(theme.fg("muted", from), "");
					steps.forEach((s, i) => {
						const head = `${i === index ? ">" : " "} ${sign(marks[i])} ${s.n}. `;
						const wrapped = wrapTextWithAnsi(s.text, Math.max(10, w - 17));
						wrapped.forEach((l, j) => lines.push((j === 0 ? head : " ".repeat(12)) + (i === index ? theme.fg("accent", l) : l)));
					});
					return panel(theme, frame(theme, w, `${ticket}: human verification`, lines, "space cycles  p pass  f fail  s skip  enter or esc closes"));
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

/** stamp draws a time in the chat, dim, in line with the messages. */
export function stamp(theme: Theme, s: Stamp | undefined): Component | undefined {
	return s ? new Text(theme.fg("dim", stampText(s, Date.now())), 1, 0) : undefined;
}
