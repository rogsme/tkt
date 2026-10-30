// The playground's chat: it stands in for web.ts and the hub, so the page
// gets what a chat would send. It keeps the made-up tickets, answers every
// slash command the extension registers by moving them as tkt would, asks
// its questions through the mirror, and answers a prompt as a model does,
// streaming in with a tool call. The cards come from attentionOf, as the
// hub's do. It imports nothing from pi or Bun, so node --test covers it.

import { type Attention, attentionOf, choicesOf, type Choice, unblockedOf } from "../attention.ts";
import { statusText } from "../board.ts";
import { type Item, pageView, slashName, terminalOnly } from "../chat.ts";
import { type Board, type Command, type Dialog, type Image, Mirror, type Option, type Page, type Query } from "../mirror.ts";
import { choresText, normalizeId, type Row, stampText, verifiedAt, versionText } from "../model.ts";
import { historyText, type Mark, marked, statsText, stepsText, tally, withEvidence } from "../report.ts";
import { cardActs } from "../web/acts.ts";
import * as answers from "./answers.ts";
import { headOf, incident, report, repo, row, type Scenario, scenario } from "./scenario.ts";
import { triggers } from "./triggers.ts";

/** Link drops the pages' link to the chat, as a chat that quits or takes a
 * new token does; main.ts does it to the server. */
export interface Link {
	url: string;
	drop(ms: number): void;
	deny(ms: number): void;
}

export interface Options {
	/** commands are the extension's slash commands, read from commands.ts. */
	commands: Command[];
	link?: Link;
	/** pace scales every wait: 1 plays at a person's speed, 0 for tests. */
	pace?: number;
}

type Handler = (w: string[], page: Page) => Promise<void>;

const play: Command = { name: "play", description: "Playground: start a question, a notice, a board change or a dropped link: /play [NAME]" };

/** commandsOf reads the slash commands commands.ts registers, with their
 * descriptions, so the playground offers the same ones. */
export function commandsOf(source: string): Command[] {
	const out: Command[] = [];
	for (const m of source.matchAll(/registerCommand\("([\w-]+)",\s*\{\s*description:\s*"((?:[^"\\]|\\.)*)"/g)) out.push({ name: m[1], description: JSON.parse(`"${m[2]}"`) });
	return out;
}

export class Fake {
	readonly mirror: Mirror;
	s: Scenario;
	private items: Item[] = [];
	private seq = 0;
	private readonly jobs = new Map<string, string>();
	private readonly dismissed = new Set<string>();
	private failed: Attention[] = [];
	private readonly marks = new Map<string, Mark[]>();
	private suggestion?: answers.Suggestion;
	private busy = false;
	/** held keeps the model shown busy, for the busy trigger. */
	private held = false;
	private stopped = false;
	private nextId = 140;
	private readonly o: Options;

	constructor(o: Options) {
		this.o = o;
		this.s = scenario(Date.now());
		this.mirror = new Mirror(repo, {
			send: (text, images, page) => this.send(text, images, page),
			stop: () => {
				this.stopped = true;
			},
			theme: () => {},
			commands: () => [...o.commands, play],
			query: (q) => Promise.resolve(this.query(q)),
		});
		this.items = [{ kind: "info", id: "i0", text: "This is the playground: made-up tickets, and a chat that answers every command. /play starts what only a chat starts." }];
		this.mirror.reset(this.items);
		this.push();
	}

	/** handlers answer each slash command. The test checks that every
	 * command commands.ts registers has one. */
	readonly handlers: Record<string, Handler> = {
		start: (w) => this.start(w),
		suggest: (w) => this.suggest(w),
		chores: (w) => this.chores(w),
		approve: (w) => this.approve(w),
		next: (w) => this.next(w),
		auto: (w) => this.auto(w),
		hold: (w) => this.hold(w),
		sync: (w) => this.sync(w),
		send: (w) => this.sendTo(w),
		run: (w) => this.runStage(w),
		relaunch: (w) => this.relaunch(w),
		dismiss: (w) => this.dismiss(w),
		show: (w) => this.show(w),
		peek: async () => this.mirror.notify("usage: /peek ACM-12", "warning"),
		accept: (w) => this.accept(w),
		check: (w) => this.check(w),
		done: (w) => this.done(w),
		inbox: (_w, page) => this.inbox(page),
		board: (w) => this.boardCmd(w),
		history: (w) => this.history(w),
		stats: async () => this.say(statsText(answers.stats(Date.now()).result!)),
		version: async () => this.say(versionText("v0.9.0", "1.3.0", "v0.9.0", "/home/acme/.local/bin/tkt")),
		web: async () => this.say(`The web page is open in your browser: ${this.o.link?.url ?? "the playground's address"}`),
		update: () => this.update(),
		play: (w) => this.play(w),
	};

	board(): Board {
		const watching = this.s.watch === "on";
		const items: Attention[] = [];
		for (const r of this.s.rows) {
			const a = attentionOf(r, this.jobs.has(r.ticket), watching);
			if (a && !this.dismissed.has(a.key)) items.push(a);
		}
		for (const u of this.s.unblocked) {
			const a = unblockedOf(u);
			if (!this.jobs.has(u.ticket) && !this.dismissed.has(a.key)) items.push(a);
		}
		return {
			rows: this.s.rows,
			items: [...items, ...this.failed].filter((a) => !a.parked),
			jobs: Object.fromEntries(this.jobs),
			incidents: this.s.incidents,
			watch: this.s.watch,
			update: this.s.update || undefined,
		};
	}

	/** push sends the pages the board as it stands. Rows change in place, so
	 * the mirror gets a copy it can compare. */
	private push(): void {
		this.mirror.setBoard(structuredClone(this.board()));
	}

	private say(text: string): void {
		this.items.push({ kind: "note", id: `n${++this.seq}`, text });
		this.mirror.setItems([...this.items]);
	}

	private wait(ms: number): Promise<void> {
		return new Promise((r) => setTimeout(r, ms * (this.o.pace ?? 1)));
	}

	private ask(title: string, options: Option[], other = false): Promise<{ answer: string; typed: boolean } | undefined> {
		return this.mirror.ask({ kind: "ask", title, options, other }).answer as Promise<{ answer: string; typed: boolean } | undefined>;
	}

	private question<T>(d: Omit<Dialog, "id">): Promise<T | undefined> {
		return this.mirror.ask(d).answer as Promise<T | undefined>;
	}

	/** job runs a tkt command for a ticket, as the hub's jobs do: the board
	 * names it while it runs, and done says how it went. A quick one, such
	 * as tkt approve, is a command's answer, so it lands in the chat before
	 * the board moves; a background one ends with a notice, as the hub's
	 * doneText does. */
	private async job(ticket: string, label: string, ms: number, done: () => string | void, quick = false): Promise<void> {
		this.jobs.set(ticket, label);
		this.push();
		await this.wait(ms);
		this.jobs.delete(ticket);
		const text = done();
		if (text && quick) this.say(text);
		else if (text) this.mirror.notify(text);
		this.push();
	}

	private rowOf(raw: string | undefined): Row | undefined {
		if (!raw) return undefined;
		return this.s.rows.find((r) => r.ticket.toLowerCase() === raw.toLowerCase()) ?? this.s.rows.find((r) => r.ticket === normalizeId(raw, "ACM"));
	}

	/** need finds the ticket a command names, or says why it cannot. */
	private need(w: string[], usage: string): Row | undefined {
		if (!w[0]) {
			this.mirror.notify(`usage: ${usage}`, "warning");
			return undefined;
		}
		const r = this.rowOf(w[0]);
		if (!r) this.say(`${normalizeId(w[0], "ACM")}: no worktree on the board. /start ${normalizeId(w[0], "ACM")} starts it.`);
		return r;
	}

	send(text: string, images: Image[], page: Page): void {
		const name = slashName(text);
		if (name && images.length === 0) {
			const view = pageView(text, (raw) => this.rowOf(raw)?.ticket ?? normalizeId(raw, "ACM"));
			if (view) {
				page.send({ t: "open", ...view });
				return;
			}
			const h = this.handlers[name];
			if (h) {
				h(text.trim().split(/\s+/).slice(1), page).catch((err: unknown) => this.mirror.notify(`/${name} failed in the playground: ${String(err)}`, "error"));
				return;
			}
			if (terminalOnly.has(name)) {
				this.mirror.notify(`/${name} opens one of pi's own screens, which only the terminal draws: type it in the chat's pane.`, "warning");
				return;
			}
		}
		void this.chat(text, images.length);
	}

	query(q: Query): unknown {
		const now = Date.now();
		const r = this.rowOf(q.ticket);
		const gone = () => answers.fail("show", "not_found", `${q.ticket} has no worktree`, `tkt history ${q.ticket} shows what happened to it`);
		switch (q.what) {
			case "message":
				return r ? answers.message(r, now) : gone();
			case "screen":
				return r ? answers.screen(r, now) : gone();
			case "diff":
				return r ? answers.diff(r) : gone();
			case "steps":
				return r ? answers.steps(r, this.marks.get(`${r.ticket}@${r.pr?.head ?? ""}`) ?? []) : gone();
			case "history":
				return answers.history(q.ticket, r, now);
			case "stats":
				return answers.stats(now);
			case "chores":
				return answers.chores(now, this.s.watch === "on", this.s.rows.filter((x) => x.chore === "deps").map((x) => x.ticket));
			case "suggest":
				return answers.suggest(this.suggestion, now);
			case "version":
				return answers.version(this.s.update);
		}
	}

	/** chat answers a prompt as the model does: thinking, a tkt_status call,
	 * then the answer, each streaming in. A prompt sent while it works
	 * steers it, as in the terminal. */
	private async chat(text: string, images: number, reply?: string): Promise<void> {
		this.items.push({ kind: "user", id: `u${++this.seq}`, text, images });
		this.mirror.setItems([...this.items]);
		if (this.busy) return;
		this.busy = true;
		this.stopped = false;
		this.mirror.setBusy(true);
		const started = Date.now();
		const id = `a${++this.seq}`;
		const thinking = "The user asks about the board. tkt_status reads it as it stands, so I read it before I answer.";
		const end = (error = "") => {
			this.mirror.setLive([]);
			if (error) this.items.push({ kind: "assistant", id: `a${++this.seq}`, text: "", thinking: "", error });
			this.items.push({ kind: "stamp", id: `s${++this.seq}`, text: stampText({ at: started, took: Date.now() - started }, Date.now()) });
			this.mirror.setItems([...this.items]);
			this.busy = false;
			this.mirror.setBusy(this.held);
		};
		if (!(await this.stream(id, "Let me read the board first.", thinking))) return end("Stopped.");
		const tool: Extract<Item, { kind: "tool" }> = { kind: "tool", id: `t${++this.seq}`, name: "tkt_status", args: "", result: "", error: false, done: false };
		this.items.push({ kind: "assistant", id, text: "Let me read the board first.", thinking, error: "" }, tool);
		this.mirror.setLive([]);
		this.mirror.setItems([...this.items]);
		await this.wait(900);
		const b = this.board();
		tool.result = statusText({ rows: b.rows, jobs: new Map(this.jobs), items: b.items, now: Date.now(), tick: 0, repo, update: this.s.update, watch: this.s.watch, incidents: this.s.incidents });
		tool.done = true;
		this.mirror.setItems([...this.items]);
		if (this.stopped) return end("Stopped.");
		const answer = reply ?? this.answerFor(images);
		const id2 = `a${++this.seq}`;
		if (!(await this.stream(id2, answer, ""))) return end("Stopped.");
		this.items.push({ kind: "assistant", id: id2, text: answer, thinking: "", error: "" });
		end();
	}

	/** stream sends text a few words at a time, at the pace web.ts sends
	 * it, and says whether it got to the end before a stop. */
	private async stream(id: string, text: string, thinking: string): Promise<boolean> {
		const think = thinking.split(" ");
		for (let i = 1; i <= think.length && thinking; i += 3) {
			if (this.stopped) return false;
			this.mirror.setLive([{ kind: "assistant", id, text: "", thinking: think.slice(0, i).join(" "), error: "" }]);
			await this.wait(50);
		}
		const words = text.split(" ");
		for (let i = 1; i <= words.length + 2; i += 3) {
			if (this.stopped) return false;
			this.mirror.setLive([{ kind: "assistant", id, text: words.slice(0, i).join(" "), thinking, error: "" }]);
			await this.wait(50);
		}
		return !this.stopped;
	}

	private answerFor(images: number): string {
		const b = this.board();
		const need = [...new Set(b.items.map((a) => a.ticket))];
		const lines = b.items.slice(0, 4).map((a) => `- ${a.text}`);
		const pic = images > 0 ? `The screenshot shows the board with ${need.length} cards on the left. ` : "";
		return `${pic}${b.rows.length} tickets are on the board, and ${need.length} need you. The first ones:\n\n${lines.join("\n")}\n\nSay "approve ACM-101", or press a card's button.`;
	}

	private async start(w: string[]): Promise<void> {
		const modes = ["auto", "auto-plan", "manual", "yolo"];
		const asAt = w.indexOf("as");
		const as = asAt >= 0 ? (w[asAt + 1] ?? "") : "";
		const words = asAt >= 0 ? w.slice(0, asAt) : w;
		const mode = words.find((x) => modes.includes(x.toLowerCase()))?.toLowerCase() ?? "auto";
		const ids = [...new Set(words.filter((x) => !modes.includes(x.toLowerCase()) && x !== "to").map((x) => normalizeId(x, "ACM")))];
		if (ids.length === 0) return this.mirror.notify("usage: /start ACM-12 [ACM-13 ...] [auto | auto-plan | manual | yolo] [as PRESET]", "warning");
		if (mode === "yolo") {
			const a = await this.ask(`No person checks anything before ${ids.join(", ")} merge${ids.length === 1 ? "s" : ""}. Start in yolo?`, [
				{ label: "Start in yolo", description: "tkt auto approves the plan, has every step reviewed and verified by agents, and merges once verify-pr passed every step." },
				{ label: "Not now" },
			]);
			if (!a?.answer.startsWith("Start")) return this.say("Nothing started.");
		} else if (ids.length > 1 && !(await this.question<boolean>({ kind: "confirm", title: `Start ${ids.join(", ")}?`, message: "Each ticket starts in its own tab." }))) return;
		const fresh = ids.filter((id) => !this.rowOf(id));
		const on = as ? ` on ${as}` : "";
		const text: Record<string, string> = {
			"auto-plan": `Queued: ${fresh.join(", ")} starting${on} in auto-plan. tkt approves the plan and drives each ticket until it is reviewed clean; the board shows when one needs you.`,
			auto: `Queued: ${fresh.join(", ")} starting${on} in auto. The board shows each plan when it is ready; after you approve it, tkt drives the ticket until it is ready for human verification.`,
			yolo: `Queued: ${fresh.join(", ")} starting${on} in yolo. tkt carries each ticket to the merge with nobody watching; the board shows when one needs you, and the plan and pull request list the assumptions its agents made.`,
			manual: `Queued: ${fresh.join(", ")} starting${on}, manual. The board shows each plan when it is ready, and you drive each step.`,
		};
		for (const id of ids.filter((x) => !fresh.includes(x))) this.say(`${id} has a worktree already: /show ${id} for where it stands.`);
		if (fresh.length === 0) return;
		this.say(text[mode]);
		this.s.unblocked = this.s.unblocked.filter((u) => !fresh.includes(u.ticket));
		for (const id of fresh) void this.startOne(id, mode);
	}

	private async startOne(id: string, mode: string): Promise<void> {
		const now = Date.now();
		const r = row(id, now, { title: "A ticket you started here", phase: "starting", stage: "start", agent: null, started: new Date(now).toISOString(), phase_since: new Date(now).toISOString(), next: { kind: "wait", reason: "starting", argv: null, text: "the agent starts" } });
		if (mode !== "manual") r.auto = { wanted: true, approve_plan: mode !== "auto", yolo: mode === "yolo", running: true, rounds: 0, stop: "", waiting: "", paused: "", why: "" };
		this.s.rows.push(r);
		await this.job(id, "start", 2000, () => {
			Object.assign(r, { phase: "planning", agent: { pane_id: `w1:p${id.replace(/\D/g, "")}`, name: id.toLowerCase(), harness: "claude", status: "working" }, phase_since: new Date().toISOString() });
		});
		await this.wait(4000);
		if (!this.s.rows.includes(r)) return;
		if (mode === "manual" || mode === "auto") {
			Object.assign(r, { phase: "awaiting_approval", last_status: "PLAN_READY", agent: { ...r.agent!, status: "idle" }, idle_since: new Date().toISOString(), next: { kind: "human", reason: "plan_ready", argv: null, text: "read the plan, then approve it" } });
			if (r.auto) r.auto.paused = "plan_awaits_approval";
		} else Object.assign(r, { phase: "working", stage: "work", last_status: "PLAN_APPROVED" });
		this.push();
	}

	private async suggest(w: string[]): Promise<void> {
		let request = w.join(" ").replace(/\bas \S+$/, "").trim();
		if (!request) {
			request = (await this.question<string>({ kind: "input", title: "What should suggest-tickets look for?", placeholder: "five tickets tagged feedback, or a number" }))?.trim() ?? "";
			if (!request) return this.mirror.notify("Nothing sent to suggest-tickets.", "info");
		}
		const s: answers.Suggestion = { prompt: /^\d+$/.test(request) ? `Suggest ${request} tickets.` : request, started: new Date().toISOString(), working: true };
		this.suggestion = s;
		this.say(`Asked suggest-tickets for ${request} in the suggest tab. Its picks show there and in the web page's Suggest view; start the ones you want.`);
		await this.wait(6000);
		s.working = false;
	}

	private async chores(w: string[]): Promise<void> {
		if (w[0] !== "run") return this.say(choresText(answers.chores(Date.now(), this.s.watch === "on", []).result!));
		const name = w[1];
		if (!name) return this.mirror.notify("usage: /chores run NAME", "warning");
		this.say(`Queued: a run of chore ${name}. It starts in a worktree of its own, and the board shows it.`);
		await this.job(`chore ${name}`, "chore", 3000, () => {
			const id = `chore/${name}-${new Date().toISOString().slice(0, 10)}-${++this.seq}`;
			this.s.rows.push(row(id, Date.now(), { chore: name, title: `Chore ${name}`, stage: "chore", phase: "working", agent: { pane_id: "w1:p800", name: id, harness: "claude", status: "working" }, started: new Date().toISOString(), phase_since: new Date().toISOString() }));
			return `Started chore ${name}'s run as ${id}.`;
		});
	}

	private async approve(w: string[]): Promise<void> {
		const r = this.need(w, "/approve ACM-12 [auto]");
		if (!r) return;
		if (r.chore) return this.say(`${r.ticket} is a run of a chore, with no ticket and no plan to approve. /send ${r.ticket} TEXT tells its agent what to do.`);
		if (r.phase !== "awaiting_approval") return this.say(`${r.ticket}: tkt approve failed: no plan waits for approval (phase ${r.phase}).`);
		const auto = w[1] === "auto" || !!r.auto?.running;
		await this.job(r.ticket, "approve", 1200, () => {
			Object.assign(r, { phase: "working", stage: "work", last_status: "PLAN_APPROVED", agent: { ...(r.agent ?? row(r.ticket, 0).agent!), status: "working" }, phase_since: new Date().toISOString(), next: { kind: "wait", reason: "agent_working", argv: null, text: "work-ticket builds it" } });
			if (auto) r.auto = { wanted: true, approve_plan: false, running: true, rounds: 0, stop: "", waiting: "", paused: "", why: "" };
			return auto ? `Approved ${r.ticket}'s plan; work-ticket builds it in the same session, and tkt auto drives it from there.` : `Approved ${r.ticket}'s plan; work-ticket builds it in the same session.`;
		}, true);
	}

	private async next(w: string[]): Promise<void> {
		const r = this.need(w, "/next ACM-12");
		if (!r) return;
		const argv = r.next?.argv;
		if (r.next?.kind !== "command" || !argv) return this.say(`${r.ticket}: no step to take now: ${r.next?.text ?? "it waits"}.`);
		this.say(`Queued: ${argv.join(" ")}.`);
		await this.job(r.ticket, argv[1], 3000, () => {
			if (argv[1] === "done") return this.remove(r);
			Object.assign(r, { phase: argv[1] === "resume" ? "needs_you" : "fixing", agent: { ...(r.agent ?? row(r.ticket, 0).agent!), status: "working" }, phase_since: new Date().toISOString(), next: { kind: "wait", reason: "agent_working", argv: null, text: "the agent works" } });
			if (r.watch) r.watch = null;
			return `${r.ticket}: ${argv.slice(1).join(" ")} started; its agent works on it.`;
		});
	}

	private async auto(w: string[]): Promise<void> {
		const r = this.need(w, "/auto ACM-12 on | off | auto-plan | yolo [as PRESET]");
		if (!r) return;
		if (r.chore) return this.say(`${r.ticket} is a run of a chore: tkt auto drives tickets only. tkt watch syncs its base and fixes its checks, and you merge its pull request.`);
		const how = (w[1] ?? "on").toLowerCase();
		if (how === "off") {
			r.auto = { wanted: false, approve_plan: false, running: false, rounds: 0, stop: "", waiting: "", paused: "", why: "" };
			this.push();
			return this.say(`Stopped tkt auto for ${r.ticket}; its loop ends at its next look.`);
		}
		if (how === "yolo") {
			const a = await this.ask(`No person checks anything before ${r.ticket} merges. Start it in yolo?`, [
				{ label: "Start in yolo", description: "tkt auto approves the plan, has every step reviewed and verified by agents, and merges once verify-pr passed every step." },
				{ label: "Not now" },
			]);
			if (!a?.answer.startsWith("Start")) return this.say("Nothing started.");
		}
		r.auto = { wanted: true, approve_plan: how === "auto-plan" || how === "yolo", yolo: how === "yolo", running: true, rounds: r.auto?.rounds ?? 0, stop: "", waiting: "", paused: "", why: "" };
		this.push();
		this.say(how === "yolo" ? `tkt auto now carries ${r.ticket} to the merge with nobody watching (yolo). The board shows when it needs you.` : `tkt auto now drives ${r.ticket}${how === "auto-plan" ? ", approving its plan too" : ""}. The board shows when it needs you.`);
	}

	private async hold(w: string[]): Promise<void> {
		const r = this.need(w, "/hold ACM-12 [off]");
		if (!r) return;
		r.hold = w[1] !== "off";
		this.push();
		this.say(r.hold ? `${r.ticket} is on hold: tkt watch and tkt auto leave it alone. /hold ${r.ticket} off takes it off.` : `${r.ticket} is off hold: tkt watch and tkt auto take it up again.`);
	}

	private async sync(w: string[]): Promise<void> {
		const r = this.need(w, "/sync ACM-12");
		if (!r) return;
		this.say(`Queued: tkt sync for ${r.ticket}. It merges the base itself when the merge is clean, and hands a conflict or any other failure to a fresh session in a split beside the agent.`);
		await this.job(r.ticket, "sync", 3000, () => {
			if (!r.behind) return `${r.ticket}: already up to date with its base.`;
			r.behind = 0;
			const head = headOf(`${r.ticket}+${++this.seq}`);
			if (r.pr) r.pr.head = head;
			// The checks run again on the new head.
			if (r.checks) r.checks = { ...r.checks, state: "pending", failing: [], pending: r.checks.total };
			if (r.next?.reason === "checks_fail") r.next = { kind: "wait", reason: "checks_pending", argv: null, text: "the checks run on the new head" };
			return `${r.ticket}: merged its base and pushed ${head.slice(0, 7)}.`;
		});
	}

	private async sendTo(w: string[]): Promise<void> {
		const r = this.need(w, "/send ACM-12 TEXT");
		if (!r) return;
		const text = w.slice(1).join(" ");
		if (!text) return this.mirror.notify("usage: /send ACM-12 TEXT", "warning");
		if (!r.agent) return this.say(`${r.ticket}: no agent runs to read it. /relaunch ${r.ticket} brings it back.`);
		r.agent.status = "working";
		this.push();
		this.say(`Sent to ${r.ticket}'s agent.`);
		await this.wait(4000);
		if (r.agent) r.agent.status = "idle";
		this.push();
	}

	private async runStage(w: string[]): Promise<void> {
		const r = this.need(w, "/run ACM-12 STAGE [as PRESET]");
		if (!r) return;
		const stage = w[1];
		if (!stage) return this.mirror.notify("usage: /run ACM-12 STAGE [as PRESET]", "warning");
		this.say(`Queued: tkt run ${r.ticket} ${stage}. It starts ${stage} in a fresh session in its tab.`);
		await this.job(r.ticket, stage, 3000, () => {
			if (stage === "verify") r.verification = report(r.ticket);
			if (r.auto?.paused) r.auto.paused = "";
			Object.assign(r, { stage, agent: { ...(r.agent ?? row(r.ticket, 0).agent!), status: "working" }, next: { kind: "wait", reason: "agent_working", argv: null, text: `${stage} runs` } });
			if (r.phase === "needs_fix") r.phase = stage === "verify" ? "verifying" : "fixing";
			return `${r.ticket}: ${stage} runs in a fresh session.`;
		});
	}

	private async relaunch(w: string[]): Promise<void> {
		const r = this.need(w, "/relaunch ACM-12");
		if (!r) return;
		if (r.agent) return this.say(`${r.ticket}: an agent runs in its worktree already. /peek ${r.ticket} shows its screen.`);
		await this.job(r.ticket, "resume", 2000, () => {
			r.agent = { pane_id: `w1:p${r.ticket.replace(/\D/g, "")}`, name: r.ticket.toLowerCase(), harness: "claude", status: "idle" };
			if (r.phase === "no_agent") Object.assign(r, { phase: "working", next: { kind: "wait", reason: "agent_working", argv: null, text: "the agent works" } });
			return `${r.ticket}: tkt resume relaunched its newest session.`;
		}, true);
	}

	private async dismiss(w: string[]): Promise<void> {
		const r = this.rowOf(w[0]);
		const id = r?.ticket ?? (w[0] ? normalizeId(w[0], "ACM") : "");
		if (!id) return this.mirror.notify("usage: /dismiss ACM-12", "warning");
		const set = this.board().items.filter((a) => a.ticket === id);
		for (const a of set) this.dismissed.add(a.key);
		this.failed = this.failed.filter((a) => a.ticket !== id);
		this.push();
		this.mirror.notify(set.length ? `Set aside what ${id} needs; it comes back once its situation changes.` : `${id} needs nothing from you now.`);
	}

	private async show(w: string[]): Promise<void> {
		const r = this.need(w, "/show ACM-12");
		if (!r) return;
		const env = answers.message(r, Date.now()) as { result: { message: string } };
		this.say(env.result.message ? `${r.ticket}'s last message:\n\n${env.result.message}` : `${r.ticket}'s agent has said nothing since the last prompt.`);
	}

	private async accept(w: string[]): Promise<void> {
		const r = this.need(w, "/accept ACM-12 [reason]");
		if (!r) return;
		let why = w.slice(1).join(" ").trim();
		if (!why) why = (await this.question<string>({ kind: "input", title: `Why can ${r.ticket}'s review findings stay as they are?`, placeholder: "the finding is about the pull request's description" }))?.trim() ?? "";
		if (!why) return this.mirror.notify(`Nothing accepted: tkt accept needs your reason, such as /accept ${r.ticket} the finding is about the description.`, "warning");
		await this.job(r.ticket, "accept", 1500, () => {
			const head = r.pr?.head ?? headOf(r.ticket);
			r.accepted = { head, reason: why, url: `${r.pr?.url ?? ""}#issuecomment-2`, at_head: true, carried: false };
			if (r.auto?.paused) r.auto.paused = "";
			Object.assign(r, { phase: "ready_for_human", phase_since: new Date().toISOString(), next: { kind: "human", reason: "steps", argv: null, text: "walk the human verification steps" } });
			return `Accepted the review of ${r.ticket} at ${head.slice(0, 7)}: ${why}. It goes on to human verification.`;
		}, true);
	}

	/** check walks the steps as /check does: merge on your word when you
	 * confirmed them, sync first when the base moved, then confirm from
	 * verify-pr's evidence, the checklist, or a verify-pr run. */
	private async check(w: string[]): Promise<void> {
		const r = this.need(w, "/check ACM-12");
		if (!r) return;
		const id = r.ticket;
		if (!r.pr) return this.say(`${id} has no pull request yet, so no steps to walk.`);
		if (r.confirmed && r.next?.reason === "steps_passed") {
			const skipped = r.confirmed.waived ?? [];
			const state = skipped.length ? `${stepsText(skipped)} skipped, and the others passed` : "every step passed";
			if (!(await this.askMerge(r, `${id}: ${state} on ${r.confirmed.head.slice(0, 7)}${r.confirmed.evidence ? ", confirmed from verify-pr's evidence" : ""}. Merge it?`))) return this.say(`${id}: ${state}. /check ${id} merges it when you are ready.`);
			return this.merge(r);
		}
		const behind = r.behind ?? 0;
		if (behind > 0) {
			const a = await this.ask(`${id} is ${behind} commit${behind === 1 ? "" : "s"} behind its base. Sync it first?`, [
				{ label: "Sync first", description: "tkt sync merges the base; the new head gets a review, then /check again." },
				{ label: "Verify this head", description: "Your steps carry over a clean sync: tkt watch syncs it after you merge, and has the new head reviewed." },
			]);
			if (!a) return this.say("Closed.");
			if (a.answer.startsWith("Sync")) return this.sync([id]);
		}
		const v = verifiedAt(r);
		const first = await this.ask(`${id}: how do you want to verify?`, howOptions(v));
		if (!first) return this.say("Closed.");
		if (first.answer.startsWith("Run verify-pr")) return this.runStage([id, "verify"]);
		const steps = answers.stepList();
		if (first.answer.startsWith("Confirm") && v) {
			const seen = steps.map((s) => {
				const x = v.steps.find((y) => y.n === s.n);
				return `${s.n}. ${x ? `${x.result}: ${x.text}` : "not in the report"}`;
			});
			const go = await this.ask(`${id}: verify-pr on ${v.head.slice(0, 7)}:\n${seen.join("\n")}\nConfirm the steps from this and merge?`, [
				{ label: "Confirm and merge now", description: "tkt merge --evidence sends finish-ticket the merge line, which says you confirmed the steps from verify-pr's evidence." },
				{ label: "Not yet" },
			]);
			if (!go?.answer.startsWith("Confirm")) return this.say(`${id}: nothing confirmed. /check ${id} goes on when you are ready.`);
			this.say(`Commented on pull request #${r.pr.number} that human verification passed.`);
			return this.merge(r);
		}
		const key = `${id}@${r.pr.head ?? ""}`;
		const from = r.review?.verdict === "CLEAR" ? "" : `The review bot cleared the head; these steps come from pull request #${r.pr.number}.`;
		const start = this.marks.get(key) ?? [];
		const got = (await this.question<Mark[]>({ kind: "checklist", title: `${id}: human verification`, message: from, steps: withEvidence(steps, v), marks: steps.map((_, i) => start[i] ?? "") })) ?? start;
		this.marks.set(key, got);
		const t = tally(got, steps.length);
		if (t.fail > 0) {
			const i = got.indexOf("fail");
			const a = await this.ask(`Step ${steps[i].n} failed. What now?`, [{ label: "Start a fix session with what I saw" }, { label: "I will handle it" }]);
			if (a?.answer.startsWith("Start")) {
				const saw = await this.question<string>({ kind: "input", title: `What did you see in step ${steps[i].n}?`, placeholder: "the error, the page, the value" });
				if (saw?.trim()) {
					this.say(`Queued: a fix session for ${id}'s step ${steps[i].n}.`);
					return this.job(id, "fix", 3000, () => `${id}: a fix session for step ${steps[i].n} runs in a split beside its agent.`);
				}
			}
			return this.say(`${id}: ${t.fail} step${t.fail === 1 ? "" : "s"} failed.`);
		}
		if (t.open > 0) return this.say(`${id}: ${t.pass} passed, ${t.skip} skipped, ${t.open} not run yet. /check ${id} goes on.`);
		if (t.pass === 0) return this.say(`${id}: no step passed, since you skipped them all, so tkt posts nothing. To waive every step, tell its agent in the tab why.`);
		const passed = marked(steps, got, "pass");
		const skipped = marked(steps, got, "skip");
		let reason = "";
		if (skipped.length) {
			const a = await this.ask(`${id}: ${stepsText(passed)} passed and ${stepsText(skipped)} skipped. Post that human verification passed, with the skipped steps?`, [
				{ label: "Post it", description: "tkt verified comments on the pull request, naming the skipped steps and your reason; finish-ticket then takes them as waived." },
				{ label: "Not yet" },
			]);
			if (a?.answer.startsWith("Post")) reason = (await this.question<string>({ kind: "input", title: `Why can ${stepsText(skipped)} stay unrun?`, placeholder: "it needs production credentials" }))?.trim() ?? "";
			if (!reason) return this.say(`${id}: ${stepsText(passed)} passed and ${t.skip} skipped; nothing posted. /check ${id} goes on.`);
		}
		const state = skipped.length ? `${stepsText(passed)} passed and ${stepsText(skipped)} skipped` : "every step passed";
		const said = `Commented on pull request #${r.pr.number} that human verification passed${skipped.length ? `, with ${stepsText(skipped)} skipped` : ""}.`;
		r.confirmed = { head: r.pr.head ?? "", evidence: false, waived: skipped, reason, url: `${r.pr.url}#issuecomment-3`, at_head: true, carried: false };
		Object.assign(r, { phase: "ready_to_merge", next: { kind: "human", reason: "steps_passed", argv: null, text: `${state}: merge it` } });
		this.push();
		if (!(await this.askMerge(r, `${id}: ${state}. Merge it?`))) return this.say(`${id}: ${state}. ${said} /check ${id} merges it when you are ready.`);
		this.say(said);
		return this.merge(r);
	}

	private async askMerge(r: Row, title: string): Promise<boolean> {
		const go = await this.ask(title, [
			(r.behind ?? 0) > 0
				? { label: "Merge once synced", description: "tkt merge leaves an order: tkt watch merges the base, has the new head reviewed, and sends finish-ticket the merge line once the checks pass." }
				: { label: "Merge now", description: "tkt merge sends finish-ticket the merge line in a fresh session in its tab, or leaves an order while another ticket merges." },
			{ label: "Not yet" },
		]);
		return !!go?.answer.startsWith("Merge");
	}

	private merge(r: Row): Promise<void> {
		this.say(`Queued: tkt merge for ${r.ticket}. finish-ticket merges it now when the checks passed at its head; otherwise tkt watch hands it over once they do.`);
		return this.job(r.ticket, "merge", 4000, () => {
			Object.assign(r, { phase: "merged", phase_since: new Date().toISOString(), next: { kind: "command", reason: "merged", argv: ["tkt", "done", r.ticket], text: `tkt done ${r.ticket} removes it` } });
			if (r.pr) r.pr.state = "MERGED";
			return `finish-ticket merged ${r.ticket}'s pull request #${r.pr?.number} at ${r.pr?.head?.slice(0, 7)}, and moved the ticket to done.`;
		});
	}

	private async done(w: string[]): Promise<void> {
		const r = this.need(w, "/done ACM-12");
		if (!r) return;
		if (!(await this.question<boolean>({ kind: "confirm", title: `Remove ${r.ticket}?`, message: "Close its tabs and remove its worktree, once tkt done confirms the merge." }))) return;
		if (r.phase !== "merged" && r.phase !== "closed") return this.say(`${r.ticket}: tkt done failed: its pull request is not merged. Merge it first, or close it on the forge.`);
		await this.job(r.ticket, "done", 1500, () => this.remove(r), true);
	}

	private remove(r: Row): string {
		this.s.rows = this.s.rows.filter((x) => x !== r);
		return `Removed ${r.ticket}: closed its tabs and removed its worktree.`;
	}

	/** inbox walks what needs you, as /inbox does, each item with the
	 * answers its card has. */
	private async inbox(page: Page): Promise<void> {
		const seen = new Set<string>();
		for (;;) {
			const a = this.board().items.find((x) => !seen.has(`${x.ticket}|${x.kind}`));
			if (!a) break;
			seen.add(`${a.ticket}|${a.kind}`);
			const choices: Choice[] = choicesOf(a);
			const got = (await this.ask(a.text, [...choices, { label: "Later" }]))?.answer;
			if (got === undefined) break;
			if (got === "Later") continue;
			const act = cardActs(a, this.rowOf(a.ticket))[choices.findIndex((c) => c.label === got)];
			if (act?.tab) {
				page.send({ t: "open", ticket: a.ticket, tab: act.tab });
				return;
			}
			if (act?.reply) {
				const text = await this.question<string>({ kind: "editor", title: a.kind === "plan" ? `What should change in ${a.ticket}'s plan?` : `What do you tell ${a.ticket}'s agent?`, prefill: "" });
				if (!text?.trim()) break;
				await this.sendTo([a.ticket, text.trim()]);
				continue;
			}
			if (act?.cmd) void this.handlers[slashName(act.cmd)]?.(act.cmd.split(/\s+/).slice(1), page);
		}
		const n = this.board().items.length;
		this.say(n === 0 ? "Nothing needs you." : `${n} item${n === 1 ? "" : "s"} still ${n === 1 ? "needs" : "need"} you; the board lists ${n === 1 ? "it" : "them"}.`);
	}

	private async boardCmd(w: string[]): Promise<void> {
		if (w[0] === "hide" || w[0] === "show") return this.say(`The board pinned to the top of the chat is ${w[0] === "hide" ? "hidden" : "back"}; only the terminal draws it.`);
		this.mirror.notify("usage: /board, /board hide or /board show", "warning");
	}

	private async history(w: string[]): Promise<void> {
		if (w.length === 0) return this.mirror.notify("usage: /history ACM-12 [ACM-13 ...]", "warning");
		for (const raw of w) {
			const id = this.rowOf(raw)?.ticket ?? normalizeId(raw, "ACM");
			this.say(historyText(answers.history(id, this.rowOf(id), Date.now()).result!, false));
		}
	}

	private async update(): Promise<void> {
		if (!this.s.update) return this.say("This tkt is a development build, or the newest release: /update has nothing to install. /play update makes one available.");
		const to = this.s.update;
		if (!(await this.question<boolean>({ kind: "confirm", title: `Update tkt from v0.9.0 to ${to}?`, message: "This chat runs the old tkt until you reopen it. tkt auto loops and tkt watch move to the new tkt by themselves." }))) return;
		await this.job("", "update", 2000, () => {
			this.s.update = "";
			return `Updated tkt to ${to} at /home/acme/.local/bin/tkt.`;
		});
		if (await this.question<boolean>({ kind: "confirm", title: "Quit now?", message: "pi prints the command that reopens this chat: tkt agent --session ID." })) this.mirror.notify("A chat would quit here; the playground stays.", "warning");
	}

	private async play(w: string[]): Promise<void> {
		let name = w[0];
		if (!name) {
			const a = await this.ask("What should the chat do?", triggers.map((t) => ({ label: t.name, description: `${t.group}: ${t.label}` })));
			if (!a) return;
			name = a.answer;
		}
		await this.trigger(name);
	}

	/** trigger starts one of the things only a chat starts, by its name in
	 * triggers.ts. */
	async trigger(name: string): Promise<void> {
		const tell = (what: string, v: unknown) => this.mirror.notify(`${what} answered: ${JSON.stringify(v)}`);
		const now = Date.now();
		switch (name) {
			case "confirm":
				return tell("Confirm", await this.question({ kind: "confirm", title: "Remove ACM-110?", message: "Close its tabs and remove its worktree, once tkt done confirms the merge." }));
			case "input":
				return tell("Input", await this.question({ kind: "input", title: "What did you see in step 2?", placeholder: "the error, the page, the value" }));
			case "editor":
				return tell("Editor", await this.question({ kind: "editor", title: "What should change in ACM-101's plan?", prefill: "Keep the export button next to Print.\nAlso cover XLSX." }));
			case "ask":
				return tell(
					"Ask",
					await this.ask("ACM-102: the agent asks whether a 410 stops retries. What do you tell it?", [
						{ label: "Stop at once", description: "The receiver says the endpoint is gone." },
						{ label: "Same backoff as a 5xx" },
						{ label: "Ask the team first", description: "Leave it blocked for now." },
					], true),
				);
			case "checklist": {
				const v = report("ACM-108", [2]);
				return tell("Checklist", await this.question({ kind: "checklist", title: "ACM-108: human verification", message: "The review bot cleared the head; these steps come from pull request #45.", steps: withEvidence(answers.stepList(), v), marks: ["pass", "", ""] }));
			}
			case "info":
				return this.mirror.notify("Queued: tkt sync for ACM-105. It merges the base itself when the merge is clean, and hands a conflict or any other failure to a fresh session in a split beside the agent. Hover over this notice for the whole text.");
			case "warning":
				return this.mirror.notify("usage: /start ACM-12 [ACM-13 ...] [auto | auto-plan | manual | yolo] [as PRESET]", "warning");
			case "error":
				return this.mirror.notify("ACM-105: sync failed: tkt sync: git merge stopped on a conflict in api/reports.go; a fresh session resolves it in a split.", "error");
			case "item": {
				const id = `ACM-${++this.nextId}`;
				this.s.rows.push(row(id, now, { title: "A ticket that just asked", phase: "needs_you", agent: { pane_id: "w1:p999", name: id.toLowerCase(), harness: "claude", status: "blocked" }, next: { kind: "human", reason: "agent_blocked", argv: null, text: "the agent asks a question in its tab" }, phase_since: new Date(now).toISOString() }));
				return this.push();
			}
			case "job":
				return this.job("ACM-104", "sync", 5000, () => "ACM-104: merged its base and pushed 4f2a9c1.");
			case "failed":
				this.failed.push({ key: `failed|ACM-105|${now}`, ticket: "ACM-105", kind: "failed", title: "sync failed", text: "ACM-105: sync failed: tkt sync: git push was refused: the remote has new commits.", byAuto: false });
				this.push();
				return this.mirror.notify("ACM-105: sync failed: tkt sync: git push was refused: the remote has new commits.", "error");
			case "incident":
				this.s.incidents = this.s.incidents.length ? [] : [incident(now)];
				return this.push();
			case "watch":
				this.s.watch = ({ on: "failing", failing: "off", off: "on" } as const)[this.s.watch];
				return this.push();
			case "update":
				this.s.update = this.s.update ? "" : "v0.10.0";
				return this.push();
			case "answer":
				return this.chat("what is on the board?", 0);
			case "markdown":
				return this.chat("explain how a ticket moves", 0, markdown);
			case "busy":
				this.held = !this.held;
				return this.mirror.setBusy(this.held || this.busy);
			case "drop":
				if (!this.o.link) return this.mirror.notify("No server to drop.", "warning");
				return this.o.link.drop(5000);
			case "deny":
				if (!this.o.link) return this.mirror.notify("No server to deny.", "warning");
				return this.o.link.deny(10_000);
			case "reset":
				this.s = scenario(now);
				this.jobs.clear();
				this.dismissed.clear();
				this.failed = [];
				this.marks.clear();
				this.suggestion = undefined;
				this.push();
				return this.mirror.notify("The tickets are as the playground started them.");
		}
		this.mirror.notify(`/play ${name}: no such trigger. /play lists them.`, "warning");
	}
}

/** howOptions are /check's first answers, as actions.ts gives them. */
function howOptions(v: ReturnType<typeof verifiedAt>): Option[] {
	const list = { label: "Open the checklist", description: v ? "verify-pr's result shows beside each step; mark each one as you run it." : "Mark each human verification step as you run it." };
	const run = { label: "Run verify-pr first", description: "verify-pr runs the steps in a fresh session and posts what it saw; it changes no code. /check again when it is done." };
	if (v?.verification === "PASSED") return [{ label: "Confirm from verify-pr's evidence", description: "Read what it saw on each step, then merge on it." }, list, { ...run, label: "Run verify-pr again" }];
	return v ? [list, { ...run, label: "Run verify-pr again" }] : [list, run];
}

const markdown = `A ticket moves through these phases, and the board puts each in a column:

## Planning

start-ticket reads the ticket and writes a plan. Nothing changes in the code yet.

## Building

work-ticket builds the plan **test first**, commits in small steps, and opens the pull request:

\`\`\`sh
tkt approve ACM-101
tkt run ACM-101 review
\`\`\`

## Review and verification

| Stage | Who | Ends with |
|---|---|---|
| review | review-pr | \`Verdict: CLEAR\` |
| verify | verify-pr | \`Verification: PASSED\` |
| merge | finish-ticket | \`Status: DONE\` |

> finish-ticket merges only on your word, with every human verification step confirmed.

1. review-pr reviews the head.
2. You walk the steps, or confirm them from verify-pr's evidence.
3. finish-ticket merges, and \`tkt done\` removes the worktree.

See [the lifecycle](https://git.example.com/acme/app/wiki/lifecycle) for the rest, and \`tkt ls\` for where each ticket stands.`;
