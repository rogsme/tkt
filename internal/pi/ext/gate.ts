// The gate decides who reaches the web page. The server listens on
// 127.0.0.1 only, but any page in your browser can send it requests, and a
// site can rebind its name to 127.0.0.1. So every request names the
// server's own host, a request from a page names the server's own origin,
// and the token tkt web puts in the address goes into a cookie that every
// request after must carry. It imports nothing, so node --test covers it.

/** Knock is what the gate reads from a request. */
export interface Knock {
	method: string;
	path: string;
	host: string | null;
	origin: string | null;
	cookie: string | null;
	authorization: string | null;
	/** token is the address's ?token=, as tkt web prints it. */
	token: string | null;
}

export type Verdict =
	| { pass: true }
	| { pass: false; status: number; reason: string }
	| { pass: false; status: 303; location: string; setCookie: string };

/** cookieName is per port: browsers share cookies across a host's ports,
 * and each repository's chat serves its page on a port of its own. */
export function cookieName(port: number): string {
	return `tkt_${port}`;
}

export function gate(k: Knock, port: number, token: string): Verdict {
	const hosts = [`127.0.0.1:${port}`, `localhost:${port}`];
	if (!k.host || !hosts.includes(k.host.toLowerCase())) return { pass: false, status: 403, reason: "this server answers only its own address" };
	if (k.origin !== null && !hosts.some((h) => k.origin?.toLowerCase() === `http://${h}`)) return { pass: false, status: 403, reason: "this server answers only its own page" };
	if (k.path === "/health") return k.authorization !== null && same(k.authorization, `Bearer ${token}`) ? { pass: true } : { pass: false, status: 401, reason: "no token" };
	// The token moves from the address into a cookie, and the address
	// loses it, so it stays out of the browser's history.
	if (k.method === "GET" && k.path === "/" && k.token !== null) {
		if (!same(k.token, token)) return { pass: false, status: 401, reason: "the token is not this chat's: run tkt web for a fresh address" };
		return { pass: false, status: 303, location: "/", setCookie: `${cookieName(port)}=${token}; Path=/; HttpOnly; SameSite=Strict` };
	}
	const got = cookieOf(k.cookie, cookieName(port));
	if (got === undefined || !same(got, token)) return { pass: false, status: 401, reason: "open this page with tkt web, or /web in the chat" };
	return { pass: true };
}

function cookieOf(header: string | null, name: string): string | undefined {
	for (const part of (header ?? "").split(";")) {
		const [k, ...v] = part.trim().split("=");
		if (k === name) return v.join("=");
	}
	return undefined;
}

/** same compares two strings in time that does not depend on where they
 * first differ. */
function same(a: string, b: string): boolean {
	if (a.length !== b.length) return false;
	let d = 0;
	for (let i = 0; i < a.length; i++) d |= a.charCodeAt(i) ^ b.charCodeAt(i);
	return d === 0;
}
