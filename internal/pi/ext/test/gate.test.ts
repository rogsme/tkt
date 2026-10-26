import assert from "node:assert/strict";
import { test } from "node:test";
import { cookieName, gate, type Knock } from "../gate.ts";

const port = 4310;
const token = "a".repeat(48);
const knock = (over: Partial<Knock> = {}): Knock => ({ method: "GET", path: "/", host: `127.0.0.1:${port}`, origin: null, cookie: `${cookieName(port)}=${token}`, authorization: null, token: null, ...over });

test("a request with the cookie passes, by either name of the host", () => {
	assert.deepEqual(gate(knock(), port, token), { pass: true });
	assert.deepEqual(gate(knock({ host: `localhost:${port}`, path: "/ws", origin: `http://localhost:${port}` }), port, token), { pass: true });
	assert.deepEqual(gate(knock({ cookie: `other=1; ${cookieName(port)}=${token}` }), port, token), { pass: true });
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
