import assert from "node:assert/strict";
import { test } from "node:test";
import { Jobs } from "../jobs.ts";
import type { Envelope } from "../model.ts";

const ok: Envelope = { ok: true, command: "x", warnings: [] };

test("jobs for one ticket run in order; tickets run side by side", async () => {
	const log: string[] = [];
	const gates = new Map<string, () => void>();
	const run = (args: string[]) =>
		new Promise<Envelope>((resolve) => {
			log.push(`start ${args.join(" ")}`);
			gates.set(args.join(" "), () => {
				log.push(`end ${args.join(" ")}`);
				resolve(ok);
			});
		});
	const done: string[] = [];
	const jobs = new Jobs(run, (job) => done.push(job.label));
	jobs.add({ ticket: "ACM-1", label: "start", args: ["start", "ACM-1"] });
	jobs.add({ ticket: "ACM-1", label: "approve", args: ["approve", "ACM-1"] });
	jobs.add({ ticket: "ACM-2", label: "start", args: ["start", "ACM-2"] });
	await Promise.resolve();
	assert.deepEqual(log, ["start start ACM-1", "start start ACM-2"]);
	assert.ok(jobs.inFlight("ACM-1") && jobs.inFlight("ACM-2") && !jobs.inFlight("ACM-3"));
	gates.get("start ACM-1")!();
	await new Promise((r) => setTimeout(r, 0));
	assert.deepEqual(log.slice(2), ["end start ACM-1", "start approve ACM-1"]);
	gates.get("approve ACM-1")!();
	gates.get("start ACM-2")!();
	await new Promise((r) => setTimeout(r, 0));
	assert.equal(jobs.inFlight("ACM-1"), false);
	assert.deepEqual(done.sort(), ["approve", "start", "start"]);
});

test("a job that throws ends as an internal error, and the next one runs", async () => {
	const seen: Envelope[] = [];
	let n = 0;
	const jobs = new Jobs(async () => {
		if (n++ === 0) throw new Error("boom");
		return ok;
	}, (_job, env) => seen.push(env));
	let after: Envelope | undefined;
	jobs.add({ ticket: "ACM-1", label: "a", args: ["a"] });
	jobs.add({ ticket: "ACM-1", label: "b", args: ["b"], then: (env) => void (after = env) });
	await new Promise((r) => setTimeout(r, 10));
	assert.equal(seen[0].ok, false);
	assert.equal(seen[0].error?.code, "internal");
	assert.equal(after?.ok, true);
});

test("stop cancels what runs and drops what waits", async () => {
	let aborted = false;
	const jobs = new Jobs((_args, signal) => new Promise<Envelope>((resolve) => signal.addEventListener("abort", () => {
		aborted = true;
		resolve(ok);
	})), () => assert.fail("a stopped job must not report"));
	jobs.add({ ticket: "ACM-1", label: "a", args: ["a"] });
	jobs.add({ ticket: "ACM-1", label: "b", args: ["b"] });
	jobs.stop();
	await new Promise((r) => setTimeout(r, 0));
	assert.ok(aborted);
	assert.equal(jobs.inFlight("ACM-1"), false);
});
