// The gate decides who reaches the web page. The server listens on every
// address, so other devices on your network reach it, and any page in your
// browser can send it requests, and a site can rebind its name to your
// address. So every request names the server by localhost or an address,
// never another name, a request from a page names the same origin it
// asks, and the token tkt web puts in the address goes into a cookie that
// every request after must carry. It imports nothing, so node --test
// covers it.

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
	const host = k.host?.toLowerCase() ?? "";
	if (!ownHost(host, port)) return { pass: false, status: 403, reason: "this server answers only its own address" };
	if (k.origin !== null && k.origin.toLowerCase() !== `http://${host}`) return { pass: false, status: 403, reason: "this server answers only its own page" };
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

/** ownHost holds for localhost or an address, on the server's port. A site
 * can rebind only a name, so an address is safe to answer. */
export function ownHost(host: string, port: number): boolean {
	const suffix = `:${port}`;
	if (!host.endsWith(suffix)) return false;
	const name = host.slice(0, -suffix.length);
	return name === "localhost" || /^\d{1,3}(\.\d{1,3}){3}$/.test(name) || /^\[[0-9a-f:.]+\]$/.test(name);
}

/** Iface is one address from node's os.networkInterfaces(). */
export interface Iface {
	address: string;
	family: string | number;
	internal: boolean;
}

/** networkURLs turns the page's address on 127.0.0.1 into the ones other
 * devices on your network open: one per IPv4 address of this machine,
 * leaving out loopback and the bridges containers and VMs make. */
export function networkURLs(url: string, ifaces: Record<string, Iface[] | undefined>): string[] {
	const out: string[] = [];
	for (const [name, addrs] of Object.entries(ifaces)) {
		if (/^(docker|br-|veth|virbr)/.test(name)) continue;
		for (const a of addrs ?? []) {
			if (a.internal || (a.family !== "IPv4" && a.family !== 4)) continue;
			const u = new URL(url);
			u.hostname = a.address;
			out.push(u.toString());
		}
	}
	return out;
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
