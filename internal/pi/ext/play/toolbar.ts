// The playground's toolbar: a button for each trigger, in a corner of the
// page. Only the playground serves it. It talks to the fake chat over a
// WebSocket of its own, sending /play NAME as the page's message box
// would, so the page itself runs as it does with a chat.

import { triggers } from "./triggers.ts";

let ws: WebSocket | null = null;

function connect(): void {
	const sock = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`);
	ws = sock;
	sock.onclose = () => {
		ws = null;
		setTimeout(connect, 2000);
	};
}

function stored(): boolean {
	try {
		return localStorage.getItem("tkt.play") === "open";
	} catch {
		return false;
	}
}

function store(open: boolean): void {
	try {
		localStorage.setItem("tkt.play", open ? "open" : "closed");
	} catch {
		// Storage refused, as in a private window: the choice lasts this visit.
	}
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, text = ""): HTMLElementTagNameMap[K] {
	const e = document.createElement(tag);
	e.className = cls;
	e.textContent = text;
	return e;
}

function toolbar(): void {
	const bar = el("div", "play-bar");
	bar.setAttribute("aria-label", "Playground");
	const toggle = el("button", "play-toggle", "Playground");
	const panel = el("div", "play-panel");
	const note = el("p", "play-note", "What only a chat starts. /play in the message box does the same.");
	panel.append(note);
	const groups = new Map<string, HTMLElement>();
	for (const t of triggers) {
		let g = groups.get(t.group);
		if (!g) {
			const box = el("section", "play-group");
			box.append(el("h3", "", t.group));
			g = el("div", "play-buttons");
			box.append(g);
			panel.append(box);
			groups.set(t.group, g);
		}
		const b = el("button", "play-btn", t.label);
		b.title = `/play ${t.name}`;
		b.onclick = () => {
			if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "send", text: `/play ${t.name}`, images: [] }));
			else note.textContent = "No link to the fake chat right now; it comes back by itself.";
		};
		g.append(b);
	}
	const show = (open: boolean) => {
		panel.hidden = !open;
		toggle.setAttribute("aria-expanded", String(open));
		toggle.textContent = open ? "Hide the playground" : "Playground";
	};
	toggle.onclick = () => {
		const open = panel.hidden;
		show(open);
		store(open);
	};
	show(stored());
	bar.append(panel, toggle);
	document.body.append(bar);
}

connect();
toolbar();
