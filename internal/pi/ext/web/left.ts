// The left column: what needs you, as cards with their answers as buttons,
// then the merge queue, then every ticket, then the repository's views. It draws the board the
// chat sends; the buttons send the slash commands /inbox would run.

import type { Attention } from "../attention.ts";
import { diffText, pushWaitText, queueOf, queueState, queueStopped, watchStage } from "../board.ts";
import { type Row, since } from "../model.ts";
import type { Board } from "../mirror.ts";
import { cardActs, type Nav, queueActs, type View } from "./acts.ts";
import { ActButtons, type Go, Phase } from "./bits.ts";
import { html } from "./vendor/preact.js";

export function Left({ board, nav, g, now }: { board?: Board; nav: Nav; g: Go; now: number }) {
	if (!board) return html`<p class="muted pad">Reading the board...</p>`;
	const needs = new Set(board.items.map((a) => a.ticket));
	const rows = [...board.rows].sort((a, b) => Number(needs.has(b.ticket)) - Number(needs.has(a.ticket)) || a.ticket.localeCompare(b.ticket, undefined, { numeric: true }));
	const selected = "ticket" in nav ? nav.ticket : "";
	return html`<div class="left-in">
		<section>
			<h2 class="label">Needs you ${board.items.length > 0 && html`<span class="count">${needs.size}</span>`}</h2>
			${board.items.length === 0 && html`<p class="muted small">Nothing needs you now.</p>`}
			${board.items.map((a) => html`<${Card} key=${a.key} a=${a} row=${board.rows.find((r) => r.ticket === a.ticket)} g=${g} />`)}
		</section>
		<${Queue} rows=${board.rows} g=${g} />
		<section>
			<h2 class="label">
				<a href="#" class="plain" onClick=${(e: MouseEvent) => {
					e.preventDefault();
					g.go({ board: true });
				}}>Tickets</a>
				<span class="count">${rows.length}</span>
				<span class="spacer"></span>
				<button class="btn small" onClick=${() => g.go({ view: "start" })}>Start tickets</button>
			</h2>
			${rows.length === 0 && html`<p class="muted small">No tickets yet. Start one, or say "start ACM-123" in the chat.</p>`}
			<ul class="tickets">
				${rows.map((r) => html`<${TicketRow} key=${r.ticket} r=${r} on=${r.ticket === selected} need=${needs.has(r.ticket)} job=${board.jobs[r.ticket]} g=${g} now=${now} />`)}
			</ul>
		</section>
		<nav class="views">
			${(["suggest", "chores", "stats", "version"] as View[]).map(
				(v) => html`<a href="#" class=${"view" in nav && nav.view === v ? "on" : ""} onClick=${(e: MouseEvent) => {
					e.preventDefault();
					g.go({ view: v });
				}}>${viewLabel[v]}${v === "version" && board.update && html` <span class="badge">${board.update}</span>`}</a>`,
			)}
		</nav>
	</div>`;
}

const viewLabel: Record<View, string> = { start: "Start tickets", suggest: "Suggest", chores: "Chores", stats: "Stats", version: "Version" };

/** Card is one item that needs you, with the answers /inbox offers. */
function Card({ a, row, g }: { a: Attention; row?: Row; g: Go }) {
	const open = (e: MouseEvent) => {
		e.preventDefault();
		if (row) g.go({ ticket: a.ticket, tab: "message" });
	};
	return html`<div class=${`card ${a.kind}`}>
		<div class="card-head">
			${row ? html`<a href="#" class="id" onClick=${open}>${a.ticket}</a>` : html`<span class="id">${a.ticket}</span>`}
			<span class="what">${a.title}</span>
			${a.byAuto && html`<span class="tag" title="tkt auto or tkt watch waits on this">auto</span>`}
		</div>
		<p>${a.text}</p>
		<${ActButtons} acts=${cardActs(a, row)} ticket=${a.ticket} g=${g} small=${true} />
	</div>`;
}

/** Queue is the merge queue, front first: only the front syncs and
 * merges, and when it waits on you, so does the rest. It shows only while
 * an order waits. */
function Queue({ rows, g }: { rows: Row[]; g: Go }) {
	const q = queueOf(rows);
	if (q.length === 0) return null;
	const open = (id: string) => (e: MouseEvent) => {
		e.preventDefault();
		g.go({ ticket: id, tab: "message" });
	};
	return html`<section>
		<h2 class="label">Merge queue <span class="count">${q.length}</span></h2>
		<ol class="queue">
			${q.map((r, i) => {
				const stop = i === 0 && queueStopped(r);
				return html`<li key=${r.ticket} class=${i === 0 ? (stop ? "front stopped" : "front") : ""}>
					<div class="line1">
						<span class="place">${r.merge?.place ?? i + 1}</span>
						<a href="#" class="id plain" onClick=${open(r.ticket)}>${r.ticket}</a>
						${r.pr && html`<span class="muted small">#${r.pr.number}</span>`}
						<span class="spacer"></span>
						<span class="small ${i === 0 ? "" : "muted"}">${i === 0 ? queueState(r) : `after ${r.merge?.after ?? ""}`}</span>
					</div>
					${r.title && html`<div class="line2"><span class="title">${r.title}</span></div>`}
					${stop && q.length > 1 && html`<p class="small muted">The queue waits on ${r.ticket}. Skip it to let ${q[1].ticket} go.</p>`}
					<${ActButtons} acts=${queueActs(r, i === 0, q.length - 1)} ticket=${r.ticket} g=${g} small=${true} />
				</li>`;
			})}
		</ol>
	</section>`;
}

function TicketRow({ r, on, need, job, g, now }: { r: Row; on: boolean; need: boolean; job?: string; g: Go; now: number }) {
	const flags: string[] = [];
	if (r.chore) flags.push("chore");
	if (r.hold) flags.push("hold");
	else if (r.auto?.running) flags.push(r.auto.waiting === "slot" ? "queued" : r.auto.paused ? "paused" : r.auto.yolo ? "yolo" : "auto");
	else if (r.auto?.wanted) flags.push("auto stalled");
	const ws = watchStage(r);
	if (ws) flags.push(`watch: ${ws}`);
	if (r.merge?.place) flags.push(`merge queue ${r.merge.place}`);
	const pw = pushWaitText(r);
	if (pw) flags.push(pw);
	if (r.stale) flags.push("stale");
	const agent = !r.agent ? "none" : (r.agent.background ?? 0) > 0 ? "waiting" : r.agent.status;
	return html`<li class=${`${on ? "on" : ""}${need ? " need" : ""}`} onClick=${() => g.go({ ticket: r.ticket, tab: "message" })}>
		<div class="line1">
			<span class=${`agent ${agent}`} title=${`agent: ${agent}`}></span>
			<span class="id">${r.ticket}</span>
			<${Phase} row=${r} />
			<span class="spacer"></span>
			<span class="muted small" title="time in this phase">${since(r.phase_since, now)}</span>
		</div>
		<div class="line2">
			${r.title && html`<span class="title">${r.title}</span>`}
		</div>
		<div class="line3 small muted">
			${r.pr && html`<span>#${r.pr.number}</span>`}
			<span title="what the branch changes">${diffText(r)}</span>
			<span title="since tkt start">${since(r.started, now) ? `${since(r.started, now)} old` : ""}</span>
			${r.preset && html`<span class="tag preset" title="the preset its agent runs on">${r.preset}</span>`}
			${flags.map((f) => html`<span class="tag">${f}</span>`)}
			${job && html`<span class="tag job">${job}...</span>`}
		</div>
	</li>`;
}
