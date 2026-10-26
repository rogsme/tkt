// The page's reads: a query asks the chat for a view, such as a ticket's
// diff, and the chat answers with tkt's JSON envelope. Queries only read.

import type { Query } from "../mirror.ts";
import type { Envelope } from "../model.ts";
import { useEffect, useRef, useState } from "./vendor/preact.js";

/** Send sends a message to the chat, and says whether the link was open. */
export type Send = (msg: unknown) => boolean;

let next = 0;
const waiting = new Map<number, (env: Envelope) => void>();

const unreachable: Envelope = { ok: false, command: "", warnings: [], error: { code: "unreachable", exit: 0, message: "The chat is not reachable.", hint: "", details: {} } };

/** settle hands a query's answer to whoever asked. */
export function settle(id: number, value: unknown): void {
	waiting.get(id)?.(value as Envelope);
	waiting.delete(id);
}

/** dropAll answers every open query when the link closes: its answer
 * would never come. */
export function dropAll(): void {
	for (const done of waiting.values()) done(unreachable);
	waiting.clear();
}

export function query<T>(send: Send, what: Query["what"], ticket = ""): Promise<Envelope<T>> {
	const id = ++next;
	return new Promise((resolve) => {
		waiting.set(id, resolve as (env: Envelope) => void);
		if (!send({ t: "query", id, what, ticket })) settle(id, unreachable);
	});
}

/** useQuery reads a view, again whenever a key changes, and every pollMs
 * while the page shows. It keeps the last answer while the next one comes,
 * so the view does not flicker. */
export function useQuery<T>(send: Send, what: Query["what"], ticket: string, keys: unknown[], pollMs = 0): { env?: Envelope<T>; loading: boolean; reload: () => void } {
	const [env, setEnv] = useState<Envelope<T> | undefined>(undefined);
	const [loading, setLoading] = useState(true);
	const [n, setN] = useState(0);
	const asked = useRef(0);
	useEffect(() => {
		setEnv(undefined);
	}, [what, ticket]);
	useEffect(() => {
		let stopped = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const run = async () => {
			const mine = ++asked.current;
			setLoading(true);
			const got = await query<T>(send, what, ticket);
			if (stopped || mine !== asked.current) return;
			setEnv(got);
			setLoading(false);
			if (pollMs > 0) timer = setTimeout(tick, pollMs);
		};
		// A hidden page does not poll; it reads again once you look.
		const tick = () => {
			if (document.hidden) timer = setTimeout(tick, pollMs);
			else void run();
		};
		void run();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	}, [what, ticket, n, pollMs, ...keys]);
	return { env, loading, reload: () => setN((x) => x + 1) };
}
