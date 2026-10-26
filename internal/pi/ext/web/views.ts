// The center column's views with no ticket: the board in its columns, and
// the forms and tables for starting tickets, suggest-tickets, the chores,
// the stats and the version. Each form builds the slash command you could
// type, and shows it before you send it.

import { boardColumns } from "../board.ts";
import type { Board } from "../mirror.ts";
import { ago, type ChoresResult, since } from "../model.ts";
import { local, type StatsResult } from "../report.ts";
import { type Mode, startLine, suggestLine } from "./acts.ts";
import { Failure, type Go, Loading } from "./bits.ts";
import { useQuery } from "./query.ts";
import { html, useState } from "./vendor/preact.js";

/** BoardView lays the tickets out in the /board columns. */
export function BoardView({ board, g, now }: { board?: Board; g: Go; now: number }) {
	if (!board) return html`<${Loading} />`;
	const needs = new Set(board.items.map((a) => a.ticket));
	const cols = boardColumns(board.rows, now).filter((c) => c.cells.length > 0 || ["Needs you", "Building", "Review", "Ready"].includes(c.title));
	return html`<div class="view">
		<h2>Board</h2>
		${board.rows.length === 0 ? html`<p class="muted">No tickets yet. <a href="#start">Start one</a>, or say "start ACM-123" in the chat.</p>` : html`<p class="muted">Pick a ticket for its plan, pull request, diff and screen.</p>`}
		<div class="columns">
			${cols.map(
				(c) => html`<div class="column">
					<h3>${c.title} <span class="count">${c.cells.length}</span></h3>
					${c.cells.map((cell) => {
						const r = board.rows.find((x) => x.ticket === cell.ticket);
						return html`<button class=${`cell${needs.has(cell.ticket) ? " need" : ""}`} onClick=${() => g.go({ ticket: cell.ticket, tab: "message" })}>
							<span class="id">${cell.ticket}</span>
							<span class="muted small">${since(r?.phase_since ?? "", now)}</span>
							${r?.title && html`<span class="title">${r.title}</span>`}
							${r?.phase === "blocked" && html`<span class="why small">${r.hold ? "on hold" : r.blocked_reason || "Status: BLOCKED"}</span>`}
						</button>`;
					})}
				</div>`,
			)}
		</div>
	</div>`;
}

/** Line shows the command a form sends, and sends it. */
function Line({ line, g, label, onSent }: { line: string; g: Go; label: string; onSent?: () => void }) {
	return html`<div class="line-out">
		<code>${line || " "}</code>
		<button class="btn" disabled=${!line} onClick=${() => {
			g.run(line);
			onSent?.();
		}}>${label}</button>
	</div>`;
}

export function StartView({ g }: { g: Go }) {
	const [ids, setIds] = useState("");
	const [mode, setMode] = useState<Mode>("auto");
	const [as, setAs] = useState("");
	const modes: { mode: Mode; label: string; text: string }[] = [
		{ mode: "auto", label: "Auto after I approve the plan", text: "You approve the plan; tkt auto drives it to human verification." },
		{ mode: "manual", label: "Manual", text: "start-ticket plans it; you approve the plan, and drive each step." },
		{ mode: "auto-plan", label: "Auto-plan", text: "tkt auto approves the plan too, and drives it until it is reviewed clean." },
	];
	return html`<div class="view form">
		<h2>Start tickets</h2>
		<label>Tickets <input value=${ids} placeholder="ACM-12 ACM-13, or ACM-12 to ACM-15" onInput=${(e: InputEvent) => setIds((e.target as HTMLInputElement).value)} /></label>
		<fieldset>
			<legend>How</legend>
			${modes.map(
				(m) => html`<label class="radio"><input type="radio" name="mode" checked=${mode === m.mode} onChange=${() => setMode(m.mode)} /> <span><b>${m.label}</b><span class="muted small">${m.text}</span></span></label>`,
			)}
		</fieldset>
		<label>Preset <input value=${as} placeholder="the default; or a preset from config.toml, such as opencode" onInput=${(e: InputEvent) => setAs((e.target as HTMLInputElement).value)} /></label>
		<${Line} line=${startLine(ids, mode, as)} g=${g} label="Start" onSent=${() => setIds("")} />
	</div>`;
}

export function SuggestView({ g }: { g: Go }) {
	const [request, setRequest] = useState("");
	const [as, setAs] = useState("");
	return html`<div class="view form">
		<h2>Suggest tickets</h2>
		<p class="muted">suggest-tickets reads the tracker in a suggest tab, and picks tickets that can run side by side. It changes nothing: read its picks in that tab, then start the ones you want.</p>
		<label>What to look for <input value=${request} placeholder="a number such as 3, or what you want, such as five tickets tagged feedback" onInput=${(e: InputEvent) => setRequest((e.target as HTMLInputElement).value)} /></label>
		<label>Preset <input value=${as} placeholder="the default" onInput=${(e: InputEvent) => setAs((e.target as HTMLInputElement).value)} /></label>
		<${Line} line=${suggestLine(request, as)} g=${g} label="Suggest" />
	</div>`;
}

export function ChoresView({ g, now }: { g: Go; now: number }) {
	const q = useQuery<ChoresResult>(g.send, "chores", "", []);
	if (!q.env) return html`<${Loading} />`;
	if (!q.env.ok || !q.env.result) return html`<div class="view"><h2>Chores</h2><${Failure} env=${q.env} /></div>`;
	const r = q.env.result;
	return html`<div class="view">
		<h2>Chores</h2>
		${!r.watch.running && html`<p class="warn">tkt watch does not run, so no chore runs on its schedule.</p>`}
		${r.chores.length === 0 ? html`<p class="muted">This repository has no chores. They live in .tkt files; see tkt chores --help.</p>` : html`<table class="grid">
			<thead><tr><th>Chore</th><th>Schedule</th><th>Preset</th><th>Last run</th><th>Next run</th><th>Open runs</th><th></th></tr></thead>
			<tbody>
				${r.chores.map(
					(c) => html`<tr>
						<td><b>${c.name}</b>${c.due && html` <span class="tag">due</span>`}</td>
						<td><code>${c.schedule}</code></td>
						<td>${c.as || "default"}</td>
						<td>${c.last ? `${since(c.last, now)} ago` : "never"}</td>
						<td>${c.next ? local(c.next) : "-"}</td>
						<td>${c.runs.length ? c.runs.map((id) => html`<a href="#" onClick=${(e: MouseEvent) => {
							e.preventDefault();
							g.go({ ticket: id, tab: "message" });
						}}>${id}</a> `) : "-"}</td>
						<td><button class="btn small quiet" title=${`/chores run ${c.name}`} onClick=${() => g.run(`/chores run ${c.name}`)}>Run now</button></td>
					</tr>`,
				)}
			</tbody>
		</table>`}
		<button class="link" onClick=${q.reload}>${q.loading ? "reading..." : "read again"}</button>
	</div>`;
}

export function StatsView({ g }: { g: Go }) {
	const q = useQuery<StatsResult>(g.send, "stats", "", []);
	if (!q.env) return html`<${Loading} />`;
	if (!q.env.ok || !q.env.result) return html`<div class="view"><h2>Stats</h2><${Failure} env=${q.env} /></div>`;
	const r = q.env.result;
	const s = r.summary;
	const secs = (n: number) => ago(n * 1000);
	return html`<div class="view">
		<h2>Stats</h2>
		${r.tickets.length === 0 ? html`<p class="muted">No tickets in the history yet. tkt records a ticket from its first tkt start.</p>` : html`
		<div class="tiles">
			<div class="tile"><span class="big">${s.merged}</span> merged</div>
			<div class="tile"><span class="big">${secs(s.median_total_s)}</span> start to merge, median<span class="muted small">p90 ${secs(s.p90_total_s)}</span></div>
			<div class="tile"><span class="big">${secs(s.median_waiting_on_you_s)}</span> waiting on you, median<span class="muted small">p90 ${secs(s.p90_waiting_on_you_s)}</span></div>
			<div class="tile"><span class="big">${s.in_progress}</span> in progress</div>
		</div>
		<table class="grid">
			<thead><tr><th>Ticket</th><th>Started</th><th>Start to merge</th><th>Waiting on you</th><th>Rounds</th><th>Auto</th></tr></thead>
			<tbody>
				${r.tickets.map(
					(t) => html`<tr>
						<td><a href="#" onClick=${(e: MouseEvent) => {
							e.preventDefault();
							g.go({ ticket: t.ticket, tab: "history" });
						}}>${t.ticket}</a></td>
						<td>${local(t.started)}</td>
						<td>${secs(t.total_s)}${t.in_progress ? " so far" : ""}</td>
						<td>${secs(t.waiting_on_you_s)}</td>
						<td>${t.rounds}</td>
						<td>${t.auto ? "yes" : "-"}</td>
					</tr>`,
				)}
			</tbody>
		</table>
		${s.without_start > 0 && html`<p class="muted small">${s.without_start} tickets have no recorded start and are left out.</p>`}`}
	</div>`;
}

interface Version {
	tkt: string;
	pi: string;
	bin: string;
	update: string;
}

export function VersionView({ g }: { g: Go }) {
	const q = useQuery<Version>(g.send, "version", "", []);
	if (!q.env) return html`<${Loading} />`;
	if (!q.env.ok || !q.env.result) return html`<div class="view"><h2>Version</h2><${Failure} env=${q.env} /></div>`;
	const v = q.env.result;
	return html`<div class="view">
		<h2>Version</h2>
		<dl class="facts-list">
			<dt>tkt</dt><dd><code>${v.tkt || "unknown"}</code>, at <code>${v.bin}</code></dd>
			<dt>pi</dt><dd><code>${v.pi || "unknown"}</code></dd>
		</dl>
		${v.update ? html`<p>tkt ${v.update} is available. /update installs it, after you confirm, and offers to quit the chat so you reopen it on the new tkt.</p>
			<button class="btn" onClick=${() => g.run("/update")}>Update to ${v.update}</button>` : html`<p class="muted">The chat checks for a newer tkt every six hours.</p>`}
		<p><button class="btn quiet" onClick=${() => g.run("/version")} title="/version">Ask whether the installed tkt changed</button></p>
	</div>`;
}
