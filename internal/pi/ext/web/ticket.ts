// The center column's ticket: where it stands, its buttons, and its tabs:
// the agent's last message, such as the plan, the pull request, the human
// verification steps, the diff, the agent's live screen, and the history.
// The tabs read through queries; the buttons send slash commands.

import type { Attention } from "../attention.ts";
import { diffText } from "../board.ts";
import type { Board } from "../mirror.ts";
import { ago, type Row, since, type Verification } from "../model.ts";
import { eventText, type HistoryResult, local, type Mark } from "../report.ts";
import { cardActs, type Tab, tabs, ticketActs } from "./acts.ts";
import { look, parseAnsi } from "./ansi.ts";
import { ActButtons, Failure, type Go, Loading, Md, Phase, Reply } from "./bits.ts";
import { type File, lines, section, total } from "./diff.ts";
import { useQuery } from "./query.ts";
import { html, useState } from "./vendor/preact.js";

interface Shown {
	message: string;
	source: string;
	last_status: string;
	at: string;
	screen: string;
	procedure: string;
	steps: { n: number; text: string }[];
	verification: Verification | null;
	pr: number;
	marks: Mark[];
	base: string;
	files: File[];
}

export function Ticket({ id, tab, board, g, now, asked }: { id: string; tab: Tab; board?: Board; g: Go; now: number; asked: number }) {
	const row = board?.rows.find((r) => r.ticket === id);
	const items = board?.items.filter((a) => a.ticket === id) ?? [];
	if (board && !row) {
		return html`<div class="ticket">
			<header class="t-head"><h2>${id}</h2></header>
			<p class="muted">${id} is not on the board: it has no worktree. Its history stays.</p>
			<${HistoryTab} id=${id} g=${g} keys=${[]} />
		</div>`;
	}
	if (!row) return html`<${Loading} />`;
	return html`<div class="ticket">
		<${Head} r=${row} g=${g} now=${now} job=${board?.jobs[id]} items=${items} />
		${items.map((a) => html`<${Need} key=${a.key} a=${a} r=${row} g=${g} />`)}
		<nav class="tabs" role="tablist">
			${tabs.map(
				(t) => html`<button role="tab" aria-selected=${t.tab === tab} class=${t.tab === tab ? "on" : ""} onClick=${() => g.go({ ticket: id, tab: t.tab })}>${t.label}</button>`,
			)}
		</nav>
		<div class="tab-body" role="tabpanel">
			${tab === "message" && html`<${MessageTab} r=${row} g=${g} now=${now} />`}
			${tab === "pr" && html`<${PrTab} r=${row} />`}
			${tab === "steps" && html`<${StepsTab} r=${row} g=${g} asked=${asked} />`}
			${tab === "diff" && html`<${DiffTab} r=${row} g=${g} />`}
			${tab === "screen" && html`<${ScreenTab} r=${row} g=${g} />`}
			${tab === "history" && html`<${HistoryTab} id=${id} g=${g} keys=${[row.phase, row.pr?.head]} />`}
		</div>
	</div>`;
}

function Head({ r, g, now, job, items }: { r: Row; g: Go; now: number; job?: string; items: Attention[] }) {
	// What the ticket needs from you has its own buttons, just below.
	const asked = new Set(items.flatMap((a) => cardActs(a, r).map((x) => x.cmd)));
	const acts = [...ticketActs(r).filter((a) => !asked.has(a.cmd)), { label: "Tell the agent", reply: true }];
	const agent = !r.agent ? "no agent" : (r.agent.background ?? 0) > 0 ? "agent waits on a background task" : `agent ${r.agent.status}`;
	return html`<header class="t-head">
		<h2>${r.ticket} ${r.title && html`<span class="t-title">${r.title}</span>`}</h2>
		<div class="facts">
			<${Phase} row=${r} />
			<span>${since(r.phase_since, now)} in this phase</span>
			<span>started ${since(r.started, now)} ago</span>
			<span>${agent}${r.agent ? ` (${r.agent.harness})` : ""}</span>
			${r.pr && (r.pr.url ? html`<a href=${r.pr.url} target="_blank" rel="noopener noreferrer">#${r.pr.number}</a>` : html`<span>#${r.pr.number}</span>`)}
			${diffText(r) !== "-" && html`<span title="what the branch changes">${diffText(r)}</span>`}
			${r.chore && html`<span class="tag">chore ${r.chore}</span>`}
			${r.hold && html`<span class="tag">hold</span>`}
			${r.auto?.running && html`<span class="tag">${r.auto.yolo ? "yolo" : "auto"}${r.auto.paused ? ", paused" : ""}</span>`}
			${job && html`<span class="tag job">${job}...</span>`}
		</div>
		${r.next?.text && html`<p class="next">Next: ${r.next.text}</p>`}
		<${ActButtons} acts=${acts} ticket=${r.ticket} g=${g} small=${true} />
	</header>`;
}

/** Need is what the ticket needs from you, with /inbox's answers. */
function Need({ a, r, g }: { a: Attention; r: Row; g: Go }) {
	return html`<div class=${`card wide ${a.kind}`}>
		<div class="card-head"><span class="what">Needs you: ${a.title}</span></div>
		<p>${a.text}</p>
		<${ActButtons} acts=${cardActs(a, r)} ticket=${a.ticket} g=${g} small=${true} />
	</div>`;
}

function MessageTab({ r, g, now }: { r: Row; g: Go; now: number }) {
	const q = useQuery<Shown>(g.send, "message", r.ticket, [r.idle_since, r.last_status, r.agent?.status]);
	if (!q.env) return html`<${Loading} />`;
	if (!q.env.ok || !q.env.result) return html`<${Failure} env=${q.env} />`;
	const s = q.env.result;
	const from = s.source === "transcript" ? "the session's transcript" : s.source === "api" ? "OpenCode's API" : "the agent's screen";
	return html`<div>
		<p class="meta">
			From ${from}${s.at ? `, ${since(s.at, now)} ago` : ""}${s.last_status ? html`, <code>Status: ${s.last_status}</code>` : ""}
			<button class="link" onClick=${q.reload}>${q.loading ? "reading..." : "read again"}</button>
		</p>
		${s.message ? html`<${Md} text=${s.message} />` : html`<p class="muted">The agent has said nothing since the last prompt.</p>`}
	</div>`;
}

function PrTab({ r }: { r: Row }) {
	if (!r.pr) return html`<p class="muted">${r.ticket} has no pull request yet.</p>`;
	const c = r.checks;
	const v = r.verification;
	return html`<div class="facts-list">
		<dl>
			<dt>Pull request</dt>
			<dd>
				${r.pr.url ? html`<a href=${r.pr.url} target="_blank" rel="noopener noreferrer">#${r.pr.number} on the forge</a>` : `#${r.pr.number}`}, ${r.pr.state.toLowerCase()}${r.pr.mergeable ? `, ${r.pr.mergeable.toLowerCase()}` : ""}
				${r.pr.head && html` at <code>${r.pr.head.slice(0, 7)}</code>`}
			</dd>
			<dt>Checks</dt>
			<dd>
				${!c ? "none read" : html`<span class=${`state ${c.state}`}>${c.state}</span>, ${c.total} in all${c.pending ? `, ${c.pending} pending` : ""}${c.failing.length ? html`; failing: ${c.failing.join(", ")}` : ""}`}
			</dd>
			<dt>Review</dt>
			<dd>${!r.review ? "none yet" : html`<code>${r.review.verdict}</code> on <code>${r.review.head.slice(0, 7)}</code>${r.review.at_head ? ", the head" : ", an older head"}`}</dd>
			<dt>Base</dt>
			<dd>${r.behind == null ? "unknown" : r.behind === 0 ? "up to date" : `${r.behind} commit${r.behind === 1 ? "" : "s"} behind`}</dd>
			${r.merge &&
			html`<dt>Merge order</dt>
				<dd>for <code>${r.merge.head.slice(0, 7)}</code>, steps ${r.merge.steps.join(", ")}${r.merge.sent ? `; finish-ticket got the merge line at ${local(r.merge.sent)}` : "; tkt watch sends it once the checks pass"}</dd>`}
		</dl>
		${v && html`<${Report} v=${v} />`}
	</div>`;
}

/** Report is verify-pr's report: its result, and each step's. */
function Report({ v }: { v: Verification }) {
	return html`<div class="report">
		<h3>verify-pr: <span class=${`state ${v.verification.toLowerCase()}`}>${v.verification}</span> on <code>${v.head.slice(0, 7)}</code>${v.at_head ? "" : v.carried ? ", carried over clean merges of the base" : ", an older head"}</h3>
		<ol class="results">
			${v.steps.map((s) => html`<li value=${s.n}><span class=${`state ${s.result.toLowerCase().replace(" ", "-")}`}>${s.result}</span> ${s.text}</li>`)}
		</ol>
	</div>`;
}

function StepsTab({ r, g, asked }: { r: Row; g: Go; asked: number }) {
	// The marks change as a /check closes, so the steps read again then.
	const q = useQuery<Shown>(g.send, "steps", r.ticket, [r.pr?.head, asked]);
	const buttons = html`<div class="acts">
		<button class="btn" onClick=${() => g.run(`/check ${r.ticket}`)} title=${`/check ${r.ticket}`}>Walk the steps</button>
		<button class="btn quiet" onClick=${() => g.run(`/run ${r.ticket} verify`)} title=${`/run ${r.ticket} verify`}>Run verify-pr</button>
	</div>`;
	if (!q.env) return html`<${Loading} />`;
	if (!q.env.ok || !q.env.result) return html`<div><${Failure} env=${q.env} />${r.pr && buttons}</div>`;
	const s = q.env.result;
	const v = s.verification;
	const marks = s.marks ?? [];
	return html`<div>
		<p class="meta">
			${s.source === "pull_request" ? `From pull request #${s.pr}'s body: the review bot cleared the head.` : `From review-pr's review of pull request #${s.pr}.`}
			${v && ` verify-pr: ${v.verification} on ${v.head.slice(0, 7)}${v.at_head || v.carried ? "" : ", an older head"}.`}
		</p>
		${buttons}
		<ol class="steps">
			${s.steps.map((st, i) => {
				const res = v?.steps.find((x) => x.n === st.n);
				const mark = marks[i] ?? "";
				return html`<li value=${st.n}>
					<div class="step-text">${st.text}</div>
					<div class="step-state">
						${mark && html`<span class=${`mark ${mark}`}>you: ${mark}</span>`}
						${res && html`<span class=${`state ${res.result.toLowerCase().replace(" ", "-")}`}>verify-pr: ${res.result}</span> <span class="muted">${res.text}</span>`}
					</div>
				</li>`;
			})}
		</ol>
	</div>`;
}

/** bigFile is the number of lines past which a file starts folded. */
const bigFile = 400;

function DiffTab({ r, g }: { r: Row; g: Go }) {
	const q = useQuery<Shown>(g.send, "diff", r.ticket, [r.pr?.head, r.diff?.added, r.diff?.removed]);
	if (!q.env) return html`<${Loading} />`;
	if (!q.env.ok || !q.env.result) return html`<${Failure} env=${q.env} />`;
	const files = q.env.result.files;
	const t = total(files);
	const many = files.reduce((n, f) => n + f.hunks.reduce((m, h) => m + h.lines.length, 0), 0) > 1500;
	return html`<div class="diff">
		<p class="meta">
			Against ${q.env.result.base} since the branch left it: ${files.length} file${files.length === 1 ? "" : "s"}, <span class="add">+${t.added}</span> <span class="del">-${t.removed}</span>
			${r.pr?.url && html`, <a href=${r.pr.url + "/files"} target="_blank" rel="noopener noreferrer">on the forge</a>`}
			<button class="link" onClick=${q.reload}>${q.loading ? "reading..." : "read again"}</button>
		</p>
		${files.length === 0 && html`<p class="muted">The branch changes nothing yet.</p>`}
		${files.map((f) => html`<${FileDiff} key=${f.path} f=${f} open=${!many} />`)}
	</div>`;
}

function FileDiff({ f, open }: { f: File; open: boolean }) {
	const [all, setAll] = useState(false);
	const n = f.hunks.reduce((m, h) => m + h.lines.length, 0);
	let left = all ? Number.POSITIVE_INFINITY : bigFile;
	return html`<details class="file" open=${open && n <= bigFile * 2}>
		<summary>
			<span class=${`fstatus ${f.status}`}>${f.status}</span>
			<span class="path">${f.old_path ? `${f.old_path} -> ${f.path}` : f.path}</span>
			<span class="spacer"></span>
			<span class="add">+${f.added}</span> <span class="del">-${f.removed}</span>
		</summary>
		${f.binary && html`<p class="muted pad">A binary file.</p>`}
		${!f.binary && f.hunks.length === 0 && html`<p class="muted pad">No lines change${f.status === "renamed" ? ": the file moved" : ""}.</p>`}
		${f.hunks.length > 0 &&
		html`<table class="hunks">
			${f.hunks.map((h) => {
				if (left <= 0) return null;
				const ls = lines(h).slice(0, left);
				left -= ls.length;
				return html`<tbody>
					<tr class="hunk"><td colspan="3">${section(h) || h.header}</td></tr>
					${ls.map(
						(l) => html`<tr class=${l.kind}><td class="n">${l.old ?? ""}</td><td class="n">${l.new ?? ""}</td><td class="code">${l.kind === "note" ? l.text : l.text || " "}</td></tr>`,
					)}
				</tbody>`;
			})}
		</table>`}
		${left <= 0 && !all && html`<button class="link pad" onClick=${() => setAll(true)}>Show all ${n} lines</button>`}
	</details>`;
}

/** ScreenTab shows the agent's live screen, read again every second while
 * the tab shows. It only reads; what you tell the agent goes by /send. */
function ScreenTab({ r, g }: { r: Row; g: Go }) {
	const q = useQuery<Shown>(g.send, "screen", r.ticket, [], 1000);
	return html`<div>
		${!q.env ? html`<${Loading} />` : !q.env.ok || !q.env.result ? html`<${Failure} env=${q.env} />` : html`<${Screen} text=${q.env.result.screen} />`}
		<p class="meta">The agent's screen, live. Answer a question it asks in its tab, or tell it something here.</p>
		<${Reply} ticket=${r.ticket} g=${g} />
	</div>`;
}

function Screen({ text }: { text: string }) {
	return html`<pre class="screen">${parseAnsi(text).map(
		(line) => html`<div class="sl">${line.length === 0 ? " " : line.map((s) => {
			const l = look(s);
			return html`<span class=${l.cls} style=${l.style}>${s.text}</span>`;
		})}</div>`,
	)}</pre>`;
}

function HistoryTab({ id, g, keys }: { id: string; g: Go; keys: unknown[] }) {
	const q = useQuery<HistoryResult>(g.send, "history", id, keys);
	if (!q.env) return html`<${Loading} />`;
	if (!q.env.ok || !q.env.result) return html`<${Failure} env=${q.env} />`;
	const r = q.env.result;
	return html`<div class="history">
		${r.tickets.map((t) => {
			const tl = t.timeline;
			const secs = (s: number) => ago(s * 1000);
			return html`<section>
				${tl && html`<p class="meta">${secs(tl.total_s)}${tl.in_progress ? " so far" : " from start to merge"}, ${secs(tl.waiting_on_you_s)} waiting on you, ${tl.rounds} round${tl.rounds === 1 ? "" : "s"}${tl.auto ? ", in auto" : ""}.</p>`}
				${t.events.length === 0 && t.pull_requests.length === 0 && html`<p class="muted">No history and no pull request.</p>`}
				<ul class="events">
					${t.events.map((e) => html`<li><span class="when">${local(e.at)}</span> ${eventText(e)}</li>`)}
				</ul>
				${t.pull_requests.map(
					(p) => html`<div class="pr">
						<h3><a href=${p.url} target="_blank" rel="noopener noreferrer">#${p.number}</a> ${p.title} <span class="muted small">${p.state.toLowerCase()}, +${p.additions} -${p.deletions} in ${p.changed_files} files</span></h3>
						<ul class="events">
							${[...p.commits.map((c) => ({ at: c.at, el: html`<li><span class="when">${local(c.at)}</span> commit <code>${c.oid}</code> ${c.subject}</li>` })), ...p.reviews.map((v) => ({ at: v.at, el: html`<li><details><summary><span class="when">${local(v.at)}</span> review ${v.verdict ? html`<code>${v.verdict}</code> at <code>${v.head}</code>` : "comment"} by ${v.author}</summary><${Md} text=${v.body} /></details></li>` }))]
								.sort((a, b) => a.at.localeCompare(b.at))
								.map((x) => x.el)}
						</ul>
					</div>`,
				)}
			</section>`;
		})}
		${r.forge === "failed" && html`<p class="muted">tkt could not read the pull requests; this is the history alone.</p>`}
	</div>`;
}
