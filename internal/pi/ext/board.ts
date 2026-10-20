// The board: a few lines above the prompt, and the /board columns. It
// returns plain text with a tone per piece; the extension colors it.

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
	/** queued lists the tickets with a job waiting to start, such as an auto
	 * loop still looking for a free slot before its ticket exists. */
	waiting: number;
	now: number;
	tick: number;
	repo: string;
}

/** widgetLines renders the board above the prompt: a summary line, then one
 * line per ticket, the ones that need you first. */
export function widgetLines(s: BoardState, width: number, needsYou: Set<string>): Line[] {
	const lines: Line[] = [];
	const n = s.rows.length;
	const head = `tkt  ${s.repo}  ${n} ticket${n === 1 ? "" : "s"}`;
	const flag = needsYou.size > 0 ? `, ${needsYou.size} need${needsYou.size === 1 ? "s" : ""} you (/inbox)` : "";
	lines.push({ text: fit(head + flag, width), tone: needsYou.size > 0 ? "warning" : "muted" });
	const rows = [...s.rows].sort((a, b) => Number(needsYou.has(b.ticket)) - Number(needsYou.has(a.ticket)) || a.ticket.localeCompare(b.ticket, undefined, { numeric: true }));
	for (const r of rows) {
		lines.push({ text: fit(rowText(r, s), width), tone: needsYou.has(r.ticket) ? "warning" : r.stale ? "error" : "text" });
	}
	return lines;
}

function rowText(r: Row, s: BoardState): string {
	const parts = [pad(r.ticket, 9), pad(r.phase.replaceAll("_", " "), 17), pad(since(r.phase_since, s.now) || "-", 4)];
	parts.push(pad(r.started ? since(r.started, s.now) : "-", 4));
	parts.push(pad(r.agent ? r.agent.status : "no agent", 8));
	parts.push(pad(r.pr ? `#${r.pr.number}` : "-", 5));
	const flags: string[] = [];
	if (r.auto?.running) flags.push(r.auto.approve_plan ? "auto full" : "auto");
	else if (r.auto?.waiting === "slot") flags.push("queued");
	const job = s.jobs.get(r.ticket);
	if (job) flags.push(`${job} ${spinner[s.tick % spinner.length]}`);
	if (r.stale) flags.push("! stale");
	parts.push(flags.join("  "));
	return parts.join(" ").trimEnd();
}

/** boardColumns lays the tickets out in the /board columns. Each cell is a
 * ticket id with its phase and time in it; rows of cells line up. */
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
