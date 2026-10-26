// The tkt agent's web page: the board on the left, a ticket or another
// view in the middle, and the chat, mirrored, on the right. It draws what
// the chat sends over the WebSocket and sends back what you type, the
// answers to the chat's questions, a stop, your light or dark choice, and
// queries for the views it reads. Every button sends a slash command
// through the chat, as if you typed it in the terminal.

import type { Dialog, Theme, ToPage } from "../mirror.ts";
import type { Incident } from "../model.ts";
import type { Mark } from "../report.ts";
import { hashOf, type Nav, navOf, type Tab } from "./acts.ts";
import type { Go } from "./bits.ts";
import { Left } from "./left.ts";
import { dropAll, type Send, settle } from "./query.ts";
import { apply, type Event, initial, type State, title } from "./state.ts";
import { Composer, Transcript } from "./talk.ts";
import { Ticket } from "./ticket.ts";
import { BoardView, ChoresView, StartView, StatsView, SuggestView, VersionView } from "./views.ts";
import { html, render, useEffect, useMemo, useReducer, useRef, useState } from "./vendor/preact.js";

function App() {
	const [s, dispatch] = useReducer(apply, initial) as [State, (e: Event) => void];
	const [nav, setNav] = useState<Nav>(() => navOf(location.hash));
	const now = useNow();
	const go = (n: Nav) => {
		setNav(n);
		const h = hashOf(n);
		if (location.hash !== h) history.pushState(null, "", h === "#" ? location.pathname : h);
	};
	useEffect(() => {
		const back = () => setNav(navOf(location.hash));
		addEventListener("popstate", back);
		return () => removeEventListener("popstate", back);
	}, []);
	const send = useLink(dispatch, (o) => go(o.ticket ? { ticket: o.ticket, tab: o.tab as Tab } : { board: true }));
	const g: Go = useMemo(() => ({ send, run: (cmd: string) => void send({ t: "send", text: cmd, images: [] }), go }), [send]);
	// asked counts the questions that closed, so a view that a question
	// changes, such as the checklist's marks, reads again.
	const asked = useClosed(s.dialogs);
	useEffect(() => {
		document.title = title(s);
	}, [s.repo, s.link, s.dialogs.length, s.board]);
	useEffect(() => {
		if (s.theme) document.documentElement.dataset.theme = s.theme;
	}, [s.theme]);
	const dialog = s.dialogs[0];
	return html`<div class="app">
		<${Top} s=${s} g=${g} dispatch=${dispatch} />
		<${Banner} link=${s.link} />
		<${Incidents} list=${s.board?.incidents ?? []} />
		<div class="cols">
			<aside class="left"><${Left} board=${s.board} nav=${nav} g=${g} now=${now} /></aside>
			<main class="center"><${Center} s=${s} nav=${nav} g=${g} now=${now} asked=${asked} /></main>
			<section class="right" aria-label="Chat">
				<${Transcript} items=${s.items} live=${s.live} link=${s.link} />
				<${Composer} s=${s} send=${send} />
			</section>
		</div>
		${dialog && html`<${Ask} key=${dialog.id} d=${dialog} send=${send} />`}
		<${Toasts} s=${s} dispatch=${dispatch} />
	</div>`;
}

function Center({ s, nav, g, now, asked }: { s: State; nav: Nav; g: Go; now: number; asked: number }) {
	if ("ticket" in nav) return html`<${Ticket} key=${nav.ticket} id=${nav.ticket} tab=${nav.tab} board=${s.board} g=${g} now=${now} asked=${asked} />`;
	if ("view" in nav) {
		switch (nav.view) {
			case "start":
				return html`<${StartView} g=${g} />`;
			case "suggest":
				return html`<${SuggestView} g=${g} />`;
			case "chores":
				return html`<${ChoresView} g=${g} now=${now} />`;
			case "stats":
				return html`<${StatsView} g=${g} />`;
			case "version":
				return html`<${VersionView} g=${g} />`;
		}
	}
	return html`<${BoardView} board=${s.board} g=${g} now=${now} />`;
}

/** useNow is the time, a tick every ten seconds, for the ages the board
 * shows. */
function useNow(): number {
	const [now, setNow] = useState(Date.now());
	useEffect(() => {
		const t = setInterval(() => setNow(Date.now()), 10_000);
		return () => clearInterval(t);
	}, []);
	return now;
}

function useClosed(dialogs: Dialog[]): number {
	const [n, setN] = useState(0);
	const had = useRef(0);
	useEffect(() => {
		if (dialogs.length < had.current) setN((x) => x + 1);
		had.current = dialogs.length;
	}, [dialogs.length]);
	return n;
}

/** useLink keeps the WebSocket to the chat open, and opens it again when
 * the chat goes away and comes back. Answers to queries go to whoever
 * asked; a view a command opens goes to open. */
function useLink(dispatch: (e: Event) => void, open: (o: { ticket: string; tab: string }) => void): Send {
	const ws = useRef<WebSocket | null>(null);
	const opener = useRef(open);
	opener.current = open;
	useEffect(() => {
		let wait = 1000;
		let stopped = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const connect = () => {
			const sock = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`);
			ws.current = sock;
			let opened = false;
			sock.onopen = () => {
				opened = true;
				wait = 1000;
			};
			sock.onmessage = (ev) => {
				let msg: ToPage;
				try {
					msg = JSON.parse(String(ev.data)) as ToPage;
				} catch {
					// A message the page cannot read; the next hello sets it right.
					return;
				}
				if (msg.t === "result") settle(msg.id, msg.value);
				else if (msg.t === "open") opener.current(msg);
				else dispatch(msg);
			};
			sock.onclose = async () => {
				ws.current = null;
				dropAll();
				if (stopped) return;
				dispatch({ t: "link", link: opened ? "closed" : await why() });
				timer = setTimeout(connect, wait);
				wait = Math.min(wait * 2, 5000);
			};
		};
		connect();
		return () => {
			stopped = true;
			clearTimeout(timer);
			ws.current?.close();
		};
	}, []);
	return useMemo(
		() => (msg: unknown) => {
			if (ws.current?.readyState !== WebSocket.OPEN) return false;
			ws.current.send(JSON.stringify(msg));
			return true;
		},
		[],
	);
}

/** why tells a chat that is gone from one that shuts this page out, as
 * after a new chat took another token. */
async function why(): Promise<"closed" | "denied"> {
	try {
		const r = await fetch("/app/style.css", { cache: "no-store" });
		return r.status === 401 ? "denied" : "closed";
	} catch {
		return "closed";
	}
}

function Top({ s, g, dispatch }: { s: State; g: Go; dispatch: (e: Event) => void }) {
	const dark = s.theme ? s.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
	const flip = () => {
		const theme: Theme = dark ? "light" : "dark";
		dispatch({ t: "theme-local", theme });
		g.send({ t: "theme", theme });
	};
	const state = s.link !== "open" ? "" : s.busy ? "working" : "ready";
	const w = s.board?.watch;
	return html`<header class="top">
		<h1><a href="#" class="plain" onClick=${(e: MouseEvent) => {
			e.preventDefault();
			g.go({ board: true });
		}}>${s.repo ? `tkt ${s.repo}` : "tkt"}</a></h1>
		${w && html`<span class=${`chip watch-${w}`} title="tkt watch keeps tickets moving: it syncs bases, fixes checks and carries out merge orders">watch ${w}</span>`}
		${s.board?.update && html`<a href="#version" class="chip update" onClick=${(e: MouseEvent) => {
			e.preventDefault();
			g.go({ view: "version" });
		}}>tkt ${s.board.update} is out</a>`}
		<span class="spacer"></span>
		${state && html`<span class="status" title="the model in the chat"><span class=${`dot ${s.busy ? "busy" : "on"}`}></span>model ${state}</span>`}
		<button class="icon" onClick=${flip} title=${dark ? "Switch to light" : "Switch to dark"} aria-label=${dark ? "Switch to light" : "Switch to dark"}>
			${dark ? sun : moon}
		</button>
	</header>`;
}

const sun = html`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg>`;
const moon = html`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" /></svg>`;

function Banner({ link }: { link: State["link"] }) {
	if (link === "open") return null;
	if (link === "connecting") return html`<div class="banner">Reaching the chat...</div>`;
	if (link === "denied") return html`<div class="banner bad">This page's token is no longer the chat's. Run tkt web, or /web in the chat, for a fresh address.</div>`;
	return html`<div class="banner bad">The chat is closed. This page comes back by itself once a tkt agent chat serves it again.</div>`;
}

/** Incidents is what the status pages report, which holds tkt watch's
 * agent runs back until it ends. */
function Incidents({ list }: { list: Incident[] }) {
	if (list.length === 0) return null;
	return html`<div class="banner bad">
		${list.map(
			(i) => html`<div>${i.service}: ${i.problem}. tkt watch and tkt auto start no agent until it ends. ${i.url && html`<a href=${i.url} target="_blank" rel="noopener noreferrer">${i.page}</a>`}</div>`,
		)}
	</div>`;
}

/** Ask shows the chat's oldest open question. The terminal shows it too,
 * and whichever you answer first closes the other. */
function Ask({ d, send }: { d: Dialog; send: Send }) {
	const [text, setText] = useState(d.prefill ?? "");
	const [typing, setTyping] = useState(false);
	const [marks, setMarks] = useState<Mark[]>(d.marks ?? []);
	const box = useRef<HTMLElement | null>(null);
	const first = useRef<HTMLElement | null>(null);
	const answer = (value: unknown) => send({ t: "answer", id: d.id, value });
	// A field takes focus, so you can type at once. Buttons do not: a stray
	// Enter, such as the one that sent the command, must not answer a
	// question like "Remove ACM-12?".
	useEffect(() => {
		(first.current ?? box.current)?.focus();
	}, [typing]);
	const onKey = (e: KeyboardEvent) => {
		if (e.key === "Escape") {
			e.preventDefault();
			if (typing) setTyping(false);
			else answer(null);
			return;
		}
		if (d.kind === "ask" && !typing && /^[1-9]$/.test(e.key) && !(e.target instanceof HTMLInputElement)) {
			const o = d.options?.[Number(e.key) - 1];
			if (o) answer({ answer: o.label, typed: false });
		}
	};
	let body;
	switch (d.kind) {
		case "confirm":
			body = html`${d.message && html`<p>${d.message}</p>`}
				<div class="actions">
					<button class="btn quiet" onClick=${() => answer(false)}>No</button>
					<button class="btn" onClick=${() => answer(true)}>Yes</button>
				</div>`;
			break;
		case "input":
			body = html`<input ref=${first} value=${text} placeholder=${d.placeholder ?? ""} onInput=${(e: InputEvent) => setText((e.target as HTMLInputElement).value)} onKeyDown=${(e: KeyboardEvent) => e.key === "Enter" && answer(text)} />
				<div class="actions">
					<button class="btn quiet" onClick=${() => answer(null)}>Cancel</button>
					<button class="btn" onClick=${() => answer(text)}>Send</button>
				</div>`;
			break;
		case "editor":
			body = html`<textarea ref=${first} value=${text} onInput=${(e: InputEvent) => setText((e.target as HTMLTextAreaElement).value)} onKeyDown=${(e: KeyboardEvent) => e.key === "Enter" && (e.ctrlKey || e.metaKey) && answer(text)}></textarea>
				<div class="actions">
					<button class="btn quiet" onClick=${() => answer(null)}>Cancel</button>
					<button class="btn" onClick=${() => answer(text)} title="Ctrl+Enter">Send</button>
				</div>`;
			break;
		case "ask":
			body = typing
				? html`<input ref=${first} value=${text} placeholder="Your answer" onInput=${(e: InputEvent) => setText((e.target as HTMLInputElement).value)} onKeyDown=${(e: KeyboardEvent) => e.key === "Enter" && text.trim() && answer({ answer: text, typed: true })} />
						<div class="actions">
							<button class="btn quiet" onClick=${() => setTyping(false)}>Back</button>
							<button class="btn" onClick=${() => text.trim() && answer({ answer: text, typed: true })}>Send</button>
						</div>`
				: html`<div class="choices">
							${(d.options ?? []).map(
								(o, i) => html`<button class="choice" onClick=${() => answer({ answer: o.label, typed: false })}>
									<span class="n">${i + 1}</span>${o.label}${o.description && html`<span class="d">${o.description}</span>`}
								</button>`,
							)}
							${d.other && html`<button class="choice" onClick=${() => setTyping(true)}><span class="n">${(d.options?.length ?? 0) + 1}</span>Type something.</button>`}
						</div>
						<div class="actions"><button class="btn quiet" onClick=${() => answer(null)}>Cancel</button></div>`;
			break;
		case "checklist": {
			const set = (i: number, m: Mark) => setMarks((ms) => (d.steps ?? []).map((_, j) => (j === i ? m : (ms[j] ?? ""))));
			body = html`${d.message && html`<p>${d.message}</p>`}
				<ol class="checklist">
					${(d.steps ?? []).map((st, i) => {
						const m = marks[i] ?? "";
						return html`<li value=${st.n} class=${m}>
							<div class="step-text">${st.text}</div>
							<div class="marks" role="group" aria-label=${`step ${st.n}`}>
								${(["pass", "fail", "skip"] as Mark[]).map(
									(k) => html`<button class=${`mark-btn ${k}${m === k ? " on" : ""}`} aria-pressed=${m === k} onClick=${() => set(i, m === k ? "" : k)}>${k}</button>`,
								)}
							</div>
						</li>`;
					})}
				</ol>
				<div class="actions">
					<button class="btn quiet" onClick=${() => answer(null)} title="Close without keeping these marks">Cancel</button>
					<button class="btn" onClick=${() => answer((d.steps ?? []).map((_, i) => marks[i] ?? ""))}>Done</button>
				</div>`;
			break;
		}
	}
	return html`<div class="veil" onKeyDown=${onKey}>
		<div class=${`dialog${d.kind === "checklist" ? " wide" : ""}`} role="dialog" aria-modal="true" aria-label=${d.title} tabindex="-1" ref=${box}>
			<h2>${d.title}</h2>
			${body}
			<div class="where">The chat's pane asks this too; the first answer counts.</div>
		</div>
	</div>`;
}

function Toasts({ s, dispatch }: { s: State; dispatch: (e: Event) => void }) {
	return html`<div class="toasts" aria-live="polite">
		${s.toasts.map((t) => html`<${ToastView} key=${t.id} t=${t} dispatch=${dispatch} />`)}
	</div>`;
}

/** ToastView shows a notice for a few seconds, or until you click it. */
function ToastView({ t, dispatch }: { t: State["toasts"][number]; dispatch: (e: Event) => void }) {
	useEffect(() => {
		const timer = setTimeout(() => dispatch({ t: "dismiss", id: t.id }), 8000);
		return () => clearTimeout(timer);
	}, []);
	return html`<div class=${`toast ${t.level}`} onClick=${() => dispatch({ t: "dismiss", id: t.id })}>${t.text}</div>`;
}

render(html`<${App} />`, document.getElementById("app")!);
