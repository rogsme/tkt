// The tkt agent's web page: the chat, mirrored. It draws what the chat
// sends over the WebSocket and sends back what you type, the answers to
// the chat's questions, a stop, and your light or dark choice. Everything
// you do here goes through the chat, as if you did it in the terminal.

import type { Item } from "../chat.ts";
import type { Dialog, Image, Theme, ToPage } from "../mirror.ts";
import { md } from "./md.ts";
import { apply, completions, type Event, initial, type State, title } from "./state.ts";
import { html, render, useEffect, useLayoutEffect, useReducer, useRef, useState } from "./vendor/preact.js";

type Send = (msg: unknown) => void;

/** maxImageBytes bounds one pasted image, so a message stays well inside
 * what the chat takes at once. */
const maxImageBytes = 8 << 20;

function App() {
	const [s, dispatch] = useReducer(apply, initial) as [State, (e: Event) => void];
	const send = useLink(dispatch);
	useEffect(() => {
		document.title = title(s);
	}, [s.repo, s.link, s.dialogs.length]);
	useEffect(() => {
		if (s.theme) document.documentElement.dataset.theme = s.theme;
	}, [s.theme]);
	const dialog = s.dialogs[0];
	return html`<div class="app">
		<${Top} s=${s} send=${send} dispatch=${dispatch} />
		<${Banner} link=${s.link} />
		<${Transcript} items=${s.items} live=${s.live} link=${s.link} />
		<${Composer} s=${s} send=${send} />
		${dialog && html`<${Ask} key=${dialog.id} d=${dialog} send=${send} />`}
		<${Toasts} s=${s} dispatch=${dispatch} />
	</div>`;
}

/** useLink keeps the WebSocket to the chat open, and opens it again when
 * the chat goes away and comes back. */
function useLink(dispatch: (e: Event) => void): Send {
	const ws = useRef<WebSocket | null>(null);
	useEffect(() => {
		let wait = 1000;
		let stopped = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const open = () => {
			const sock = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`);
			ws.current = sock;
			let opened = false;
			sock.onopen = () => {
				opened = true;
				wait = 1000;
			};
			sock.onmessage = (ev) => {
				try {
					dispatch(JSON.parse(String(ev.data)) as ToPage);
				} catch {
					// A message the page cannot read; the next hello sets it right.
				}
			};
			sock.onclose = async () => {
				ws.current = null;
				if (stopped) return;
				dispatch({ t: "link", link: opened ? "closed" : await why() });
				timer = setTimeout(open, wait);
				wait = Math.min(wait * 2, 5000);
			};
		};
		open();
		return () => {
			stopped = true;
			clearTimeout(timer);
			ws.current?.close();
		};
	}, []);
	return (msg) => {
		if (ws.current?.readyState === WebSocket.OPEN) ws.current.send(JSON.stringify(msg));
	};
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

function Top({ s, send, dispatch }: { s: State; send: Send; dispatch: (e: Event) => void }) {
	const dark = s.theme ? s.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
	const flip = () => {
		const theme: Theme = dark ? "light" : "dark";
		dispatch({ t: "theme-local", theme });
		send({ t: "theme", theme });
	};
	const state = s.link !== "open" ? "" : s.busy ? "working" : "ready";
	return html`<header class="top">
		<h1>${s.repo ? `tkt ${s.repo}` : "tkt"}</h1>
		<span class="spacer"></span>
		${state && html`<span class="status"><span class=${`dot ${s.busy ? "busy" : "on"}`}></span>${state}</span>`}
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

/** Transcript draws the items and the answer streaming in. It follows the
 * end while you are at the end, and stays put while you read further up. */
function Transcript({ items, live, link }: { items: Item[]; live: Item[]; link: State["link"] }) {
	const box = useRef<HTMLDivElement | null>(null);
	const atEnd = useRef(true);
	const onScroll = () => {
		const el = box.current;
		if (el) atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
	};
	useLayoutEffect(() => {
		const el = box.current;
		if (el && atEnd.current) el.scrollTop = el.scrollHeight;
	}, [items, live]);
	// Times and notices such as the model's name are no conversation yet.
	const empty = link === "open" && live.length === 0 && !items.some((it) => it.kind !== "info" && it.kind !== "stamp");
	return html`<div class="scroll" ref=${box} onScroll=${onScroll}>
		<div class="chat">
			${empty && html`<p class="empty">Say what you want, such as "start ACM-12", or type / for the commands.</p>`}
			${items.map((it) => html`<${ItemView} key=${it.id} it=${it} />`)}
			${live.map((it) => html`<${ItemView} key=${`live-${it.id}`} it=${it} live=${true} />`)}
		</div>
	</div>`;
}

function ItemView({ it, live = false }: { it: Item; live?: boolean }) {
	switch (it.kind) {
		case "user":
			return html`<div class="msg user">${it.text}${it.images > 0 && html`<div class="images">${it.images} image${it.images === 1 ? "" : "s"}</div>`}</div>`;
		case "assistant":
			return html`<div class=${`msg assistant${live ? " live" : ""}`}>
				${it.thinking && html`<details class="thinking"><summary>thinking</summary><pre>${it.thinking}</pre></details>`}
				${it.text && html`<div class="md" dangerouslySetInnerHTML=${{ __html: md(it.text) }}></div>`}
				${it.error && html`<div class="error">${it.error}</div>`}
			</div>`;
		case "tool": {
			const state = !it.done ? "running" : it.error ? "failed" : "";
			return html`<details class=${`tool${it.error ? " failed" : ""}`}>
				<summary><span class="name">${it.name}</span> ${it.args} ${state && html`<span class="state">${state}</span>`}</summary>
				${it.done && html`<pre>${it.result || "(no output)"}</pre>`}
			</details>`;
		}
		case "note":
			return html`<div class="note">${it.text}</div>`;
		case "stamp":
			return html`<div class="stamp">${it.text}</div>`;
		case "info":
			return html`<div class="info">${it.text}</div>`;
	}
	return null;
}

/** Composer is where you type. Enter sends, Shift+Enter starts a new line,
 * a pasted image goes with the message, and / offers the commands. */
function Composer({ s, send }: { s: State; send: Send }) {
	const [text, setText] = useState("");
	const [images, setImages] = useState<(Image & { url: string })[]>([]);
	const [pick, setPick] = useState(0);
	const area = useRef<HTMLTextAreaElement | null>(null);
	const offers = completions(text, s.commands);
	const open = s.link === "open";
	useLayoutEffect(() => {
		const el = area.current;
		if (!el) return;
		el.style.height = "auto";
		el.style.height = `${el.scrollHeight + 2}px`;
	}, [text]);
	useEffect(() => setPick(0), [offers.length]);

	const submit = () => {
		if (!open || (!text.trim() && images.length === 0)) return;
		send({ t: "send", text, images: images.map(({ type, data, mimeType }) => ({ type, data, mimeType })) });
		setText("");
		setImages([]);
	};
	const complete = (name: string) => {
		setText(`/${name} `);
		area.current?.focus();
	};
	const onKey = (e: KeyboardEvent) => {
		if (offers.length > 0) {
			if (e.key === "ArrowDown" || e.key === "ArrowUp") {
				e.preventDefault();
				setPick((p) => (p + (e.key === "ArrowDown" ? 1 : offers.length - 1)) % offers.length);
				return;
			}
			if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey && offers[pick] && `/${offers[pick].name}` !== text.trim())) {
				e.preventDefault();
				complete(offers[pick].name);
				return;
			}
			if (e.key === "Escape") {
				setText("");
				return;
			}
		}
		if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			submit();
		}
	};
	const onPaste = (e: ClipboardEvent) => {
		const files = [...(e.clipboardData?.files ?? [])].filter((f) => f.type.startsWith("image/"));
		if (files.length === 0) return;
		e.preventDefault();
		for (const f of files) {
			if (f.size > maxImageBytes) continue;
			const r = new FileReader();
			r.onload = () => {
				const url = String(r.result);
				const data = url.slice(url.indexOf(",") + 1);
				setImages((list) => [...list, { type: "image", data, mimeType: f.type, url }]);
			};
			r.readAsDataURL(f);
		}
	};

	return html`<div class="composer">
		<div class="inner">
			${offers.length > 0 &&
			html`<ul class="slash" role="listbox">
				${offers.map(
					(c, i) => html`<li class=${i === pick ? "on" : ""} role="option" aria-selected=${i === pick} onMouseDown=${(e: MouseEvent) => {
						e.preventDefault();
						complete(c.name);
					}}><span class="name">/${c.name}</span><span class="desc">${c.description}</span></li>`,
				)}
			</ul>`}
			${images.length > 0 &&
			html`<div class="attached">
				${images.map(
					(im, i) => html`<figure><img src=${im.url} alt=${`pasted image ${i + 1}`} /><button title="Remove" aria-label="Remove image" onClick=${() => setImages((list) => list.filter((_, j) => j !== i))}>x</button></figure>`,
				)}
			</div>`}
			<div class="row">
				<textarea
					ref=${area}
					rows="1"
					value=${text}
					placeholder=${open ? "Message the tkt agent, or / for commands" : "The chat is not reachable"}
					disabled=${!open}
					onInput=${(e: InputEvent) => setText((e.target as HTMLTextAreaElement).value)}
					onKeyDown=${onKey}
					onPaste=${onPaste}
				></textarea>
				${s.busy && html`<button class="btn quiet" onClick=${() => send({ t: "stop" })} title="Stop the model, as Escape does in the terminal">Stop</button>`}
				<button class="btn" onClick=${submit} disabled=${!open || (!text.trim() && images.length === 0)}>Send</button>
			</div>
			<div class="hint">${s.busy ? "The model works: what you send now reaches it at its next step." : "Enter sends, Shift+Enter starts a new line. Paste an image to send it along."}</div>
		</div>
	</div>`;
}

/** Ask shows the chat's oldest open question. The terminal shows it too,
 * and whichever you answer first closes the other. */
function Ask({ d, send }: { d: Dialog; send: Send }) {
	const [text, setText] = useState(d.prefill ?? "");
	const [typing, setTyping] = useState(false);
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
	}
	return html`<div class="veil" onKeyDown=${onKey}>
		<div class="dialog" role="dialog" aria-modal="true" aria-label=${d.title} tabindex="-1" ref=${box}>
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
