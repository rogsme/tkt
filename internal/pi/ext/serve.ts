// The web page's server: the gate, the page with its scripts and
// stylesheets, and the WebSocket that carries a mirror's messages. web.ts
// serves it from a chat; the playground under play/ serves the same page
// with made-up tickets. It imports nothing from pi, but runs on Bun: pi
// runs on Bun, so the server is Bun's, and Bun turns the page's TypeScript
// into JavaScript as it serves it.

import { readFileSync } from "node:fs";
import { networkInterfaces } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fromHere, gate } from "./gate.ts";
import type { Mirror, Page } from "./mirror.ts";

/** Bun is the runtime pi runs on; only what the server uses is named. */
declare const Bun: {
	serve(o: unknown): BunServer;
	Transpiler: new (o: { loader: string }) => { transformSync(code: string): string };
};
export interface BunServer {
	port: number;
	upgrade(req: Request, o: { data: { page?: Page; here?: boolean } }): boolean;
	requestIP(req: Request): { address: string } | null;
	stop(closeActive?: boolean): void;
}
interface Socket {
	data: { page?: Page; here?: boolean };
	send(text: string): void;
}

/** extDir holds the extension, and pageDir the page's files beside it. */
const extDir = dirname(fileURLToPath(import.meta.url));
const pageDir = join(extDir, "web");

/** shared are the extension's modules the page imports too. They import
 * nothing from pi, so the browser runs them as the board does. */
const shared = new Set(["model.ts", "attention.ts", "board.ts", "report.ts"]);

/** maxMessage bounds what a page may send at once, images included. */
const maxMessage = 32 << 20;

const headers = { "cache-control": "no-store", "x-content-type-options": "nosniff", "referrer-policy": "no-referrer" };

/** Site is what one server serves: the mirror its pages join, the token
 * the gate asks for, and the address it listens on. */
export interface Site {
	mirror: Mirror;
	token: string;
	hostname: string;
	/** health is what /health answers. */
	health(): unknown;
	/** joined and left tell about each page; here holds when its browser
	 * runs on this machine. */
	joined?(page: Page, here: boolean): void;
	left?(page: Page): void;
	/** fresh reads the files on every request, for the playground, where
	 * you edit them while it runs. A chat's extension directory is named
	 * after its content, so its files never change under it. */
	fresh?: boolean;
	/** mounts serve more directories by path prefix, such as "/play/", and
	 * head goes at the end of the page's <head>: the playground's toolbar. */
	mounts?: Record<string, string>;
	head?: string;
}

/** servePage starts a server for a site on port, or any free port with 0.
 * It throws when the port is taken. */
export function servePage(port: number, site: Site): BunServer {
	const js = new Map<string, string>();
	const mounts: [string, string][] = [["/app/", pageDir], ...Object.entries(site.mounts ?? {})];
	const asset = (rel: string, dir: string): Response => {
		const type = rel.endsWith(".css") ? "text/css" : /\.(ts|js)$/.test(rel) ? "text/javascript" : "";
		if (!type || !/^[\w./-]+$/.test(rel) || rel.split("/").some((p) => p === "" || p === "." || p === "..")) return new Response(null, { status: 404, headers });
		const file = join(dir, rel);
		let body = js.get(file);
		if (body === undefined) {
			try {
				body = readFileSync(file, "utf8");
			} catch {
				return new Response(null, { status: 404, headers });
			}
			if (rel.endsWith(".ts")) body = new Bun.Transpiler({ loader: "ts" }).transformSync(body);
			if (!site.fresh) js.set(file, body);
		}
		return new Response(body, { headers: { ...headers, "content-type": `${type}; charset=utf-8` } });
	};
	const fetch = (req: Request, server: BunServer): Response | undefined => {
		const url = new URL(req.url);
		const h = req.headers;
		const v = gate({ method: req.method, path: url.pathname, host: h.get("host"), origin: h.get("origin"), cookie: h.get("cookie"), authorization: h.get("authorization"), token: url.searchParams.get("token") }, server.port, site.token);
		if (!v.pass) {
			if ("location" in v) return new Response(null, { status: 303, headers: { ...headers, location: v.location, "set-cookie": v.setCookie } });
			return new Response(`tkt: ${v.reason}\n`, { status: v.status, headers: { ...headers, "content-type": "text/plain; charset=utf-8" } });
		}
		if (url.pathname === "/health") return Response.json(site.health(), { headers });
		if (url.pathname === "/ws") {
			const here = fromHere(server.requestIP(req)?.address ?? "", networkInterfaces());
			return server.upgrade(req, { data: { here } }) ? undefined : new Response("tkt: expected a WebSocket\n", { status: 400, headers });
		}
		if (req.method !== "GET") return new Response(null, { status: 405, headers });
		if (url.pathname === "/") return index(server.port, h.get("host")!);
		for (const [prefix, dir] of mounts) if (url.pathname.startsWith(prefix)) return asset(url.pathname.slice(prefix.length), dir);
		// The page imports these as ../model.ts and the like, as the
		// extension's tests do.
		if (shared.has(url.pathname.slice(1))) return asset(url.pathname.slice(1), extDir);
		return new Response(null, { status: 404, headers });
	};
	/** index is the page, with the theme you picked, so it paints in it
	 * from the start. */
	const index = (port: number, host: string): Response => {
		let html = readFileSync(join(pageDir, "index.html"), "utf8");
		if (site.mirror.theme) html = html.replace("<html lang=\"en\">", `<html lang="en" data-theme="${site.mirror.theme}">`);
		if (site.head) html = html.replace("</head>", `${site.head}\n</head>`);
		const self = `http://127.0.0.1:${port} http://localhost:${port} ws://127.0.0.1:${port} ws://localhost:${port} http://${host} ws://${host}`;
		const csp = `default-src 'none'; script-src 'self'; style-src 'self' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; img-src 'self' data: blob:; connect-src 'self' ${self}; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`;
		return new Response(html, { headers: { ...headers, "content-type": "text/html; charset=utf-8", "content-security-policy": csp } });
	};
	return Bun.serve({
		hostname: site.hostname,
		port,
		fetch,
		websocket: {
			maxPayloadLength: maxMessage,
			open: (ws: Socket) => {
				const page: Page = { send: (m) => ws.send(JSON.stringify(m)) };
				ws.data.page = page;
				site.joined?.(page, !!ws.data.here);
				site.mirror.join(page);
			},
			message: (ws: Socket, msg: string | Uint8Array) => {
				if (!ws.data.page) return;
				try {
					site.mirror.receive(JSON.parse(String(msg)), ws.data.page);
				} catch {
					// A page sent something other than JSON; nothing to do.
				}
			},
			close: (ws: Socket) => {
				if (!ws.data.page) return;
				site.mirror.leave(ws.data.page);
				site.left?.(ws.data.page);
			},
		},
	});
}
