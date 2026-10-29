// The chat's column: the transcript, mirrored from the terminal, and the
// box you type in. What you send goes through the chat as if typed in its
// pane.

import type { Item } from "../chat.ts";
import type { Image } from "../mirror.ts";
import { md } from "./md.ts";
import type { Send } from "./query.ts";
import { completions, type State } from "./state.ts";
import { html, useEffect, useLayoutEffect, useRef, useState } from "./vendor/preact.js";

/** maxImageBytes bounds one pasted image, so a message stays well inside
 * what the chat takes at once. */
const maxImageBytes = 8 << 20;

/** Transcript draws the items and the answer streaming in. It follows the
 * end while you are at the end, and stays put while you read further up.
 * A hidden column loses its scroll, so showing it again goes back to the
 * end if you were there. */
export function Transcript({ items, live, link, shown }: { items: Item[]; live: Item[]; link: State["link"]; shown: boolean }) {
	const box = useRef<HTMLDivElement | null>(null);
	const atEnd = useRef(true);
	const onScroll = () => {
		const el = box.current;
		if (el) atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
	};
	useLayoutEffect(() => {
		const el = box.current;
		if (el && atEnd.current) el.scrollTop = el.scrollHeight;
	}, [items, live, shown]);
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
export function Composer({ s, send }: { s: State; send: Send }) {
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
