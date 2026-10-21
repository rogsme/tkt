// The board: the panel pinned to the top of the chat, and the /board
// columns. It returns plain text with a tone per line; the extension colors
// it and draws the frame.

import type { Attention, AttentionKind } from "./attention.ts";
import { since, type Row } from "./model.ts";

export type Tone = "text" | "accent" | "warning" | "muted" | "success" | "error";

export interface Line {
	text: string;
	tone: Tone;
}

/** The /board columns and the phases each one holds. */
export const columns: { title: string; phases: string[] }[] = [
	{ title: "Queued", phases: ["queued"] },
	{ title: "Planning", phases: ["starting", "planning"] },
	{ title: "Needs you", phases: ["awaiting_approval", "needs_you", "no_agent", "unknown"] },
	{ title: "Building", phases: ["building", "ci", "working"] },
	{ title: "Review", phases: ["waiting_review", "reviewing", "needs_fix", "fixing", "verifying"] },
	{ title: "Ready", phases: ["ready_for_human"] },
	{ title: "Merged", phases: ["merged", "closed"] },
];

/** columnOf names the column a ticket sits in. */
export function columnOf(row: Row): string {
	for (const c of columns) if (c.phases.includes(row.phase)) return c.title;
	return "Needs you";
}

const spinner = ["|", "/", "-", "\\"];

export interface BoardState {
	rows: Row[];
	/** jobs maps a ticket to the label of its running job. */
	jobs: Map<string, string>;
	/** items is what needs you now, one per ticket at most. */
	items: Attention[];
	now: number;
	tick: number;
	repo: string;
}

/** The command that acts on an item, shown beside it on the board. */
const hints: Record<AttentionKind, (id: string) => string> = {
	plan: (id) => `/show ${id}, /approve ${id}`,
	question: (id) => `/peek ${id}`,
	blocked: (id) => `/show ${id}`,
	check: (id) => `/peek ${id}`,
	ready: (id) => `/check ${id}`,
	merged: (id) => `/done ${id}`,
	next: (id) => `/next ${id}`,
	paused: (id) => `/show ${id}`,
	stalled: (id) => `/auto ${id} on`,
	failed: () => "/inbox",
};

/** title is the board's heading: the repository, the tickets, and how many
 * need you. */
export function title(s: BoardState): string {
	const n = s.rows.length;
	const k = new Set(s.items.map((a) => a.ticket)).size;
	return `tkt  ${s.repo}  ${n} ticket${n === 1 ? "" : "s"}${k ? `  ${k} need${k === 1 ? "s" : ""} you` : ""}`;
}

/** The board's columns and their widths. AGE is the time since tkt start,
 * TIME the time in the phase, as in tkt ls; NOTES takes the rest. */
const heads: [string, number][] = [
	["TICKET", 9],
	["AGE", 4],
	["PHASE", 17],
	["TIME", 4],
	["AGENT", 8],
	["PR", 6],
];

/** boardLines renders the board's body for a width: a header, one line per
 * ticket, the ones that need you first, then what each of those needs. */
export function boardLines(s: BoardState, width: number): Line[] {
	const lines: Line[] = [];
	const needs = new Set(s.items.map((a) => a.ticket));
	const rows = [...s.rows].sort((a, b) => Number(needs.has(b.ticket)) - Number(needs.has(a.ticket)) || a.ticket.localeCompare(b.ticket, undefined, { numeric: true }));
	if (rows.length === 0) lines.push({ text: fit('No tickets yet. Say "start ACM-123".', width), tone: "muted" });
	else lines.push({ text: fit([...heads.map(([h, n]) => pad(h, n)), "NOTES"].join(" "), width), tone: "muted" });
	for (const r of rows) {
		let tone: Tone = "text";
		if (needs.has(r.ticket)) tone = "warning";
		else if (r.stale) tone = "error";
		else if (r.phase === "ready_for_human") tone = "success";
		else if (r.phase === "merged" || r.phase === "closed" || r.phase === "queued") tone = "muted";
		lines.push({ text: fit(rowText(r, s), width), tone });
	}
	if (s.items.length > 0) {
		lines.push({ text: "", tone: "text" });
		for (const a of s.items) {
			lines.push({ text: fit(`! ${a.ticket} ${a.title}   ${hints[a.kind](a.ticket)}`, width), tone: "warning" });
		}
	}
	return lines;
}

/** phaseLabel names a phase for people. */
export function phaseLabel(phase: string): string {
	return phase === "ready_for_human" ? "ready to verify" : phase.replaceAll("_", " ");
}

function rowText(r: Row, s: BoardState): string {
	const agent = !r.agent ? "-" : (r.agent.background ?? 0) > 0 ? "waiting" : r.agent.status;
	const cells = [r.ticket, since(r.started, s.now) || "-", phaseLabel(r.phase), since(r.phase_since, s.now) || "-", agent, r.pr ? `#${r.pr.number}` : "-"];
	const parts = cells.map((c, i) => pad(c, heads[i][1]));
	const flags: string[] = [];
	if (r.auto?.running) flags.push(r.auto.waiting === "slot" ? "queued" : r.auto.paused ? "paused" : "auto");
	else if (r.auto?.wanted) flags.push("auto stalled");
	const job = s.jobs.get(r.ticket);
	if (job) flags.push(`${job} ${spinner[s.tick % spinner.length]}`);
	if (r.stale) flags.push("stale");
	parts.push(flags.join(", "));
	return parts.join(" ").trimEnd();
}

/** statusText is the board as plain text, for the model. */
export function statusText(s: BoardState): string {
	const lines = [title(s), ...boardLines(s, 0).map((l) => l.text)];
	if (s.items.length > 0) {
		lines.push("", "Needs the user:", ...s.items.map((a) => `- ${a.text}`));
	} else {
		lines.push("", "Nothing needs the user.");
	}
	return lines.join("\n");
}

/** boardColumns lays the tickets out in the /board columns. Each cell is a
 * ticket id with its time in the phase; rows of cells line up. */
export function boardColumns(rows: Row[], now: number): { title: string; cells: { ticket: string; text: string }[] }[] {
	return columns.map((c) => ({
		title: c.title,
		cells: rows
			.filter((r) => columnOf(r) === c.title)
			.sort((a, b) => a.ticket.localeCompare(b.ticket, undefined, { numeric: true }))
			.map((r) => ({ ticket: r.ticket, text: `${r.ticket} ${since(r.phase_since, now) || ""}`.trim() })),
	}));
}

/** grid renders board columns as fixed-width text, marking the selected
 * ticket with a ">" . */
export function grid(cols: ReturnType<typeof boardColumns>, width: number, selected: string): string[] {
	const shown = cols.filter((c) => c.cells.length > 0 || ["Needs you", "Building", "Review", "Ready"].includes(c.title));
	const w = Math.max(10, Math.floor((width - shown.length + 1) / Math.max(1, shown.length)));
	const out = [shown.map((c) => pad(`${c.title} (${c.cells.length})`, w)).join("|")];
	out.push(shown.map(() => "-".repeat(w)).join("+"));
	const depth = Math.max(0, ...shown.map((c) => c.cells.length));
	for (let i = 0; i < depth; i++) {
		out.push(
			shown
				.map((c) => {
					const cell = c.cells[i];
					if (!cell) return " ".repeat(w);
					return pad((cell.ticket === selected ? ">" : " ") + cell.text, w);
				})
				.join("|"),
		);
	}
	return out.map((l) => l.trimEnd());
}

/** order lists the tickets in board order, for moving the selection. */
export function order(cols: ReturnType<typeof boardColumns>): string[] {
	return cols.flatMap((c) => c.cells.map((x) => x.ticket));
}

function pad(s: string, n: number): string {
	return s.length >= n ? s.slice(0, n) : s + " ".repeat(n - s.length);
}

function fit(s: string, width: number): string {
	return width > 0 && s.length > width ? s.slice(0, Math.max(0, width - 3)) + "..." : s;
}
