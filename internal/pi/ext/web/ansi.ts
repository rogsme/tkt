// The agent's screen, as herdr reads it with its colors, in spans the page
// draws. herdr writes SGR escapes for color and weight; every other escape,
// such as a cursor move or a window title, is dropped. It imports nothing,
// so node --test covers it.

/** Span is a run of text in one style. fg and bg are 0 to 15 for the
 * terminal's own colors, which the page's palette draws, or a #rrggbb. */
export interface Span {
	text: string;
	fg?: number | string;
	bg?: number | string;
	bold?: boolean;
	dim?: boolean;
	italic?: boolean;
	underline?: boolean;
	inverse?: boolean;
}

type Style = Omit<Span, "text">;

/** escapes are the sequences a screen holds: SGR, other CSI, OSC, and the
 * two-byte ones. */
const escapes = /\x1b\[([0-9;:?<=>]*)([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[()*+][0-9A-Za-z]|\x1b[@-_]|[\r\x07]/g;

/** parseAnsi reads a screen into lines of spans. A style runs on across
 * lines, as it does in a terminal. */
export function parseAnsi(text: string): Span[][] {
	const lines: Span[][] = [[]];
	let style: Style = {};
	let from = 0;
	const put = (s: string) => {
		const parts = s.split("\n");
		parts.forEach((p, i) => {
			if (i > 0) lines.push([]);
			if (!p) return;
			const line = lines[lines.length - 1];
			const last = line[line.length - 1];
			if (last && same(last, style)) last.text += p;
			else line.push({ text: p, ...style });
		});
	};
	for (const m of text.matchAll(escapes)) {
		put(text.slice(from, m.index));
		from = m.index + m[0].length;
		if (m[2] === "m") style = sgr(style, m[1]);
	}
	put(text.slice(from));
	while (lines.length > 1 && lines[lines.length - 1].every((s) => !s.text.trim())) lines.pop();
	return lines;
}

function same(a: Style, b: Style): boolean {
	return a.fg === b.fg && a.bg === b.bg && !!a.bold === !!b.bold && !!a.dim === !!b.dim && !!a.italic === !!b.italic && !!a.underline === !!b.underline && !!a.inverse === !!b.inverse;
}

/** sgr applies one SGR escape's parameters to a style. */
function sgr(style: Style, params: string): Style {
	const s: Style = { ...style };
	// 38:2::r:g:b is the colon form of 38;2;r;g;b.
	const ps = params === "" ? [0] : params.split(";").flatMap((p) => (p.includes(":") ? colon(p) : [Number(p)]));
	for (let i = 0; i < ps.length; i++) {
		const p = ps[i];
		if (p === 0) for (const k of Object.keys(s)) delete s[k as keyof Style];
		else if (p === 1) s.bold = true;
		else if (p === 2) s.dim = true;
		else if (p === 3) s.italic = true;
		else if (p === 4) s.underline = true;
		else if (p === 7) s.inverse = true;
		else if (p === 22) s.bold = s.dim = false;
		else if (p === 23) s.italic = false;
		else if (p === 24) s.underline = false;
		else if (p === 27) s.inverse = false;
		else if (p >= 30 && p <= 37) s.fg = p - 30;
		else if (p >= 90 && p <= 97) s.fg = p - 90 + 8;
		else if (p === 39) delete s.fg;
		else if (p >= 40 && p <= 47) s.bg = p - 40;
		else if (p >= 100 && p <= 107) s.bg = p - 100 + 8;
		else if (p === 49) delete s.bg;
		else if (p === 38 || p === 48) {
			const key = p === 38 ? "fg" : "bg";
			if (ps[i + 1] === 5 && i + 2 < ps.length) {
				s[key] = color256(ps[i + 2]);
				i += 2;
			} else if (ps[i + 1] === 2 && i + 4 < ps.length) {
				s[key] = hex(ps[i + 2], ps[i + 3], ps[i + 4]);
				i += 4;
			}
		}
	}
	for (const k of ["bold", "dim", "italic", "underline", "inverse"] as const) if (s[k] === false) delete s[k];
	return s;
}

/** colon reads 38:5:N and 38:2:[space]:R:G:B as the semicolon forms. */
function colon(p: string): number[] {
	const n = p.split(":").map((x) => (x === "" ? Number.NaN : Number(x)));
	if (n[1] === 2 && n.length === 6) n.splice(2, 1);
	return n.map((x) => (Number.isNaN(x) ? 0 : x));
}

/** color256 is one of the 256 colors: the 16 the terminal names, a 6x6x6
 * cube, then 24 grays. */
export function color256(n: number): number | string {
	if (n < 16) return n;
	if (n < 232) {
		const v = [0, 95, 135, 175, 215, 255];
		const c = n - 16;
		return hex(v[Math.floor(c / 36)], v[Math.floor(c / 6) % 6], v[c % 6]);
	}
	const g = 8 + 10 * (Math.min(n, 255) - 232);
	return hex(g, g, g);
}

function hex(r: number, g: number, b: number): string {
	return `#${[r, g, b].map((v) => Math.max(0, Math.min(255, v | 0)).toString(16).padStart(2, "0")).join("")}`;
}

/** look is how the page draws a span: classes for the terminal's own
 * colors and the weights, and a style for any other color. inverse swaps
 * the colors, with the screen's own where a span sets none. */
export function look(s: Span): { cls: string; style: Record<string, string> } {
	let fg = s.fg;
	let bg = s.bg;
	const cls: string[] = [];
	if (s.inverse) {
		[fg, bg] = [bg ?? "bg", fg ?? "fg"];
	}
	const style: Record<string, string> = {};
	const paint = (c: number | string | undefined, side: "fg" | "bg") => {
		if (c === undefined) return;
		if (typeof c === "number") cls.push(`${side}${c}`);
		else if (c === "fg" || c === "bg") cls.push(`${side}-${c}`);
		else style[side === "fg" ? "color" : "backgroundColor"] = c;
	};
	paint(fg, "fg");
	paint(bg, "bg");
	if (s.bold) cls.push("b");
	if (s.dim) cls.push("d");
	if (s.italic) cls.push("i");
	if (s.underline) cls.push("u");
	return { cls: cls.join(" "), style };
}
