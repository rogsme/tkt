import assert from "node:assert/strict";
import { test } from "node:test";
import { cookieName, fromHere, gate, type Knock, networkURLs } from "../gate.ts";

const port = 4310;
const token = "a".repeat(48);
const knock = (over: Partial<Knock> = {}): Knock => ({ method: "GET", path: "/", host: `127.0.0.1:${port}`, origin: null, cookie: `${cookieName(port)}=${token}`, authorization: null, token: null, ...over });

test("a request with the cookie passes, by either name of the host", () => {
	assert.deepEqual(gate(knock(), port, token), { pass: true });
	assert.deepEqual(gate(knock({ host: `localhost:${port}`, path: "/ws", origin: `http://localhost:${port}` }), port, token), { pass: true });
	assert.deepEqual(gate(knock({ cookie: `other=1; ${cookieName(port)}=${token}` }), port, token), { pass: true });
});

test("another device on the network reaches it by this machine's address", () => {
	assert.deepEqual(gate(knock({ host: `192.168.1.20:${port}`, path: "/ws", origin: `http://192.168.1.20:${port}` }), port, token), { pass: true });
	assert.deepEqual(gate(knock({ host: `[fe80::1]:${port}` }), port, token), { pass: true });
	// The origin must be the address the request names, not another one.
	assert.equal((gate(knock({ host: `192.168.1.20:${port}`, path: "/ws", origin: `http://127.0.0.1:${port}` }), port, token) as { status: number }).status, 403);
	// A name other than localhost may be one a site rebound.
	assert.equal((gate(knock({ host: `git.example.com:${port}` }), port, token) as { status: number }).status, 403);
});

test("another host or another page's origin is refused, cookie or not", () => {
	assert.equal((gate(knock({ host: "evil.example.com" }), port, token) as { status: number }).status, 403);
	assert.equal((gate(knock({ host: "127.0.0.1:9999" }), port, token) as { status: number }).status, 403);
	assert.equal((gate(knock({ host: null }), port, token) as { status: number }).status, 403);
	assert.equal((gate(knock({ path: "/ws", origin: "http://evil.example.com" }), port, token) as { status: number }).status, 403);
	assert.equal((gate(knock({ path: "/ws", origin: "null" }), port, token) as { status: number }).status, 403);
});

test("without the cookie, or with another chat's, the page says to open it with tkt web", () => {
	const v = gate(knock({ cookie: null }), port, token);
	assert.deepEqual(v, { pass: false, status: 401, reason: "open this page with tkt web, or /web in the chat" });
	assert.equal((gate(knock({ cookie: `${cookieName(port)}=${"b".repeat(48)}` }), port, token) as { status: number }).status, 401);
	assert.equal((gate(knock({ cookie: `${cookieName(port + 1)}=${token}` }), port, token) as { status: number }).status, 401);
});

test("the token in the address becomes a cookie, and the address loses it", () => {
	assert.deepEqual(gate(knock({ cookie: null, token }), port, token), { pass: false, status: 303, location: "/", setCookie: `tkt_4310=${token}; Path=/; HttpOnly; SameSite=Strict` });
	assert.equal((gate(knock({ cookie: null, token: "wrong" }), port, token) as { status: number }).status, 401);
	// Only the page itself takes a token; anything else needs the cookie.
	assert.equal((gate(knock({ cookie: null, token, path: "/app/app.ts" }), port, token) as { status: number }).status, 401);
});

test("tkt web asks /health with the token as a bearer", () => {
	assert.deepEqual(gate(knock({ path: "/health", cookie: null, authorization: `Bearer ${token}` }), port, token), { pass: true });
	assert.equal((gate(knock({ path: "/health" }), port, token) as { status: number }).status, 401);
	assert.equal((gate(knock({ path: "/health", cookie: null, authorization: "Bearer nope" }), port, token) as { status: number }).status, 401);
});

test("the network addresses leave out loopback, IPv6 and container bridges", () => {
	const url = `http://127.0.0.1:${port}/?token=${token}`;
	const ifaces = {
		lo: [{ address: "127.0.0.1", family: "IPv4", internal: true }],
		wlan0: [
			{ address: "192.168.1.20", family: "IPv4", internal: false },
			{ address: "fe80::1", family: "IPv6", internal: false },
		],
		docker0: [{ address: "172.17.0.1", family: "IPv4", internal: false }],
		tailscale0: [{ address: "100.64.0.3", family: 4, internal: false }],
	};
	assert.deepEqual(networkURLs(url, ifaces), [`http://192.168.1.20:${port}/?token=${token}`, `http://100.64.0.3:${port}/?token=${token}`]);
	assert.deepEqual(networkURLs(url, {}), []);
});

test("a page from this machine is one from loopback or its own interfaces", () => {
	const ifaces = { lo: [{ address: "127.0.0.1", family: "IPv4", internal: true }], wlan0: [{ address: "192.168.1.20", family: "IPv4", internal: false }] };
	assert.equal(fromHere("127.0.0.1", ifaces), true);
	assert.equal(fromHere("::1", ifaces), true);
	assert.equal(fromHere("::ffff:127.0.0.1", ifaces), true);
	assert.equal(fromHere("192.168.1.20", ifaces), true);
	assert.equal(fromHere("::ffff:192.168.1.20", ifaces), true);
	// A phone on the same network is another machine.
	assert.equal(fromHere("192.168.1.31", ifaces), false);
});
