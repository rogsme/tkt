// Small pieces the page's views share: a button for an act, a ticket's
// phase, the box that tells an agent something, markdown, and tkt's
// errors.

import { columnOf, phaseLabel } from "../board.ts";
import type { Envelope, Row } from "../model.ts";
import { type Act, type Nav, sendLine, type Tab } from "./acts.ts";
import { md } from "./md.ts";
import type { Send } from "./query.ts";
import { html, useState } from "./vendor/preact.js";

/** Go is what the page's views get to act with: run sends a slash command
 * through the chat, as if you typed it; go moves the center column.
 * pressed holds the commands run sent that tkt is still on. */
export interface Go {
	send: Send;
	run(cmd: string): void;
	go(nav: Nav): void;
	pressed: string[];
}

/** ActButtons draws acts as buttons. A reply act opens a box under them
 * for what to tell the ticket's agent. */
export function ActButtons({ acts, ticket, g, small = false }: { acts: Act[]; ticket: string; g: Go; small?: boolean }) {
	const [replying, setReplying] = useState(false);
	const click = (a: Act) => {
		if (a.tab) g.go({ ticket, tab: a.tab });
		else if (a.reply) setReplying((r) => !r);
	};
	return html`<div class=${`acts${small ? " small" : ""}`}>
			${acts.map((a) => {
				const cls = a.main ? "btn" : "btn quiet";
				if (a.cmd) return html`<${RunButton} g=${g} cmd=${a.cmd} cls=${cls} title=${a.description}>${a.label}<//>`;
				return html`<button class=${cls} title=${a.description ?? ""} onClick=${() => click(a)}>${a.label}</button>`;
			})}
		</div>
		${replying && html`<${Reply} ticket=${ticket} g=${g} done=${() => setReplying(false)} />`}`;
}

/** RunButton sends a slash command. Until tkt answers it, or until the
 * job it started ends, it spins and takes no second press. */
export function RunButton({ g, cmd, cls = "btn", title, disabled = false, onSent, children }: { g: Go; cmd: string; cls?: string; title?: string; disabled?: boolean; onSent?: () => void; children: unknown }) {
	const busy = !!cmd && g.pressed.includes(cmd);
	return html`<button class=${busy ? `${cls} busy` : cls} title=${busy ? `${cmd}: tkt is on it` : (title ?? cmd)} disabled=${busy || disabled || !cmd} aria-busy=${busy} onClick=${() => {
		g.run(cmd);
		onSent?.();
	}}>${children}</button>`;
}

/** Reply is the box that tells a ticket's agent something, with /send. */
export function Reply({ ticket, g, done }: { ticket: string; g: Go; done?: () => void }) {
	const [text, setText] = useState("");
	const submit = () => {
		const line = sendLine(ticket, text);
		if (!line) return;
		g.run(line);
		setText("");
		done?.();
	};
	return html`<div class="reply">
		<textarea
			rows="3"
			value=${text}
			placeholder=${`What to tell ${ticket}'s agent`}
			onInput=${(e: InputEvent) => setText((e.target as HTMLTextAreaElement).value)}
			onKeyDown=${(e: KeyboardEvent) => {
				if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
					e.preventDefault();
					submit();
				}
				if (e.key === "Escape") done?.();
			}}
		></textarea>
		<div class="acts small">
			${done && html`<button class="btn quiet" onClick=${done}>Cancel</button>`}
			<button class="btn" disabled=${!text.trim()} onClick=${submit} title="Ctrl+Enter">Send to the agent</button>
		</div>
	</div>`;
}

/** Phase is a ticket's phase, colored by its board column. */
export function Phase({ row }: { row: Row }) {
	return html`<span class=${`phase ${columnOf(row).toLowerCase().replace(/\s+/g, "-")}`}>${phaseLabel(row.phase, row.chore)}</span>`;
}

export function Md({ text }: { text: string }) {
	return html`<div class="md" dangerouslySetInnerHTML=${{ __html: md(text) }}></div>`;
}

/** Failure shows why tkt refused, and what to run next. */
export function Failure({ env }: { env: Envelope }) {
	const e = env.error;
	return html`<div class="failure">
		<div>${e?.message ?? "tkt failed."}</div>
		${e?.hint && html`<div class="hint">Next: <code>${e.hint}</code></div>`}
	</div>`;
}

/** Loading says a view is on its way. */
export function Loading() {
	return html`<p class="muted">Reading...</p>`;
}

export function TabLink({ ticket, tab, g, children }: { ticket: string; tab: Tab; g: Go; children: unknown }) {
	return html`<a href="#" onClick=${(e: MouseEvent) => {
		e.preventDefault();
		g.go({ ticket, tab });
	}}>${children}</a>`;
}
