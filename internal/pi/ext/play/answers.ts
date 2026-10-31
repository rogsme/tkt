// The playground's answers to the page's queries, shaped as tkt's --json
// envelopes for show, show --screen, show --diff, show --steps, history,
// stats, chores, suggest and the chat's version. It imports nothing at run
// time but the extension's pure modules, so node --test covers it.

import type { ChoresResult, Envelope, Row, SuggestResult } from "../model.ts";
import type { HistoryResult, HistoryReview, Mark, StatsResult } from "../report.ts";
import type { File } from "../web/diff.ts";
import { headOf, repo } from "./scenario.ts";

export function ok<T>(command: string, result: T): Envelope<T> {
	return { ok: true, command, result, warnings: [] };
}

export function fail(command: string, code: string, message: string, hint = ""): Envelope {
	return { ok: false, command, warnings: [], error: { code, exit: 1, message, hint, details: {} } };
}

const plan = (id: string, title: string) => `## Plan for ${id}: ${title}

Status: PLAN_READY

### What changes

1. Add an **Export** button to \`web/reports/ReportHeader.tsx\`, next to Print.
2. Add \`GET /reports/:id/export.csv\` in \`api/reports.go\`, streaming one row per report line.
3. Quote fields with \`encoding/csv\`, so commas and quotes in titles survive.

### Tests

- \`TestExportCSV\` covers an empty report, a report with quotes, and one with 10k lines.
- A Playwright check presses Export and reads the file.

### Human verification

1. Open a report and press Export: the file downloads.
2. Open the CSV: one row per report line, with a header.
3. Export a report with no lines: the file holds the header only.

> Out of scope: XLSX, which ACM-140 tracks.

\`\`\`go
w := csv.NewWriter(out)
w.Write([]string{"date", "account", "amount"})
\`\`\`
`;

const said: Record<string, string> = {
	"ACM-102": "Before I change the retry policy: should a webhook that fails with **410 Gone** stop retrying at once, or follow the same backoff as a 5xx?\n\nI lean to stopping at once, since the receiver says the endpoint is gone.",
	"ACM-103": "Status: BLOCKED\nReason: waits for ACM-90's API to ship\n\nThe billing calls need `POST /v2/charges`, which ACM-90 adds. Nothing else is left on this side.",
	"ACM-105": "Pushed the timezone fix. CI fails on `test (linux)` and `lint`:\n\n```\n--- FAIL: TestInvoiceDate (0.01s)\n    invoice_test.go:42: got 2026-10-28, want 2026-10-29\n```",
	"ACM-120": "Status: BLOCKED\nReason: the sample contacts file in the ticket is not attached\n\nI need a real export to match the column names. Can you attach one to ACM-120?",
};

/** message is tkt show's answer: the agent's last message. */
export function message(r: Row, now: number): Envelope {
	if (!r.agent && r.phase !== "merged") return ok("show", { message: "", source: "screen", last_status: r.last_status, at: "" });
	const text = r.phase === "awaiting_approval" ? plan(r.ticket, r.title ?? "the ticket") : (said[r.ticket] ?? `Pushed ${headOf(r.ticket).slice(0, 7)} for ${r.ticket}. ${r.title ?? ""}: the tests pass locally.\n\nStatus: ${r.last_status || "READY_FOR_REVIEW"}`);
	return ok("show", { message: text, source: r.agent?.harness === "opencode" ? "api" : "transcript", last_status: r.last_status, at: r.idle_since || new Date(now - 60_000).toISOString() });
}

const spin = ["|", "/", "-", "\\"];

/** screen is the agent's screen in color, with a clock and a spinner that
 * move every second, as a working agent's do. */
export function screen(r: Row, now: number): Envelope {
	if (!r.agent) return fail("show", "agent_missing", `no agent runs for ${r.ticket}`, `tkt resume ${r.ticket} brings it back`);
	const t = new Date(now).toTimeString().slice(0, 8);
	const e = "\x1b[";
	const working = r.agent.status === "working";
	const lines = [
		`${e}1;38;5;208m* Claude Code${e}0m  ${e}2m~/acme/app.${r.ticket}${e}0m`,
		"",
		`${e}36m>${e}0m /work-ticket ${r.ticket}`,
		"",
		`${e}32m+${e}0m Read ${e}1mweb/reports/ReportHeader.tsx${e}0m (120 lines)`,
		`${e}32m+${e}0m Edit ${e}1mapi/reports.go${e}0m  ${e}32m+42${e}0m ${e}31m-3${e}0m`,
		`${e}32m+${e}0m Bash ${e}2mgo test ./api/...${e}0m`,
		`  ${e}32mok${e}0m  acme/app/api  0.412s`,
		"",
		r.agent.status === "blocked"
			? `${e}33m? Should a 410 stop retries at once?${e}0m\n  ${e}7m 1. Stop at once ${e}0m\n    2. Same backoff as a 5xx`
			: working
				? `${e}35m${spin[Math.floor(now / 1000) % 4]} Working...${e}0m ${e}2m(${Math.floor(now / 1000) % 60}s, esc to interrupt)${e}0m`
				: `${e}2mWaiting for input${e}0m`,
		"",
		`${e}48;5;236m ${r.ticket}  ${r.stage}  ${t} ${e}0m`,
	];
	return ok("show", { screen: lines.join("\n"), source: "screen", message: "", last_status: r.last_status, at: "" });
}

/** diff is tkt show --diff's answer: the files the branch changes. */
export function diff(r: Row): Envelope {
	if (!r.pr && !r.diff) return fail("show", "no_commits", `${r.ticket}'s branch changes nothing yet`, "the diff shows once the agent commits");
	const files: File[] = [
		{
			path: "api/reports.go",
			status: "modified",
			added: 9,
			removed: 2,
			binary: false,
			hunks: [
				{
					header: "@@ -40,8 +40,15 @@ func (h *Handler) Report(w http.ResponseWriter, r *http.Request) {",
					lines: [" 	rep, err := h.store.Report(r.Context(), id)", " 	if err != nil {", "-		http.Error(w, err.Error(), 500)", "-		return", "+		h.fail(w, err)", "+		return", " 	}", "+	if r.URL.Query().Get(\"format\") == \"csv\" {", "+		w.Header().Set(\"Content-Type\", \"text/csv\")", "+		cw := csv.NewWriter(w)", "+		writeRows(cw, rep)", "+		cw.Flush()", "+		return", "+	}", " 	h.render(w, rep)", " }"],
				},
			],
		},
		{
			path: "api/reports_test.go",
			status: "added",
			added: 6,
			removed: 0,
			binary: false,
			hunks: [{ header: "@@ -0,0 +1,6 @@", lines: ["+package api", "+", "+func TestExportCSV(t *testing.T) {", "+	got := export(t, report(\"a,b\", `q\"uote`))", "+	want(t, got, \"date,account,amount\\n\")", "+}", "\\ No newline at end of file"] }],
		},
		{ path: "web/reports/ExportButton.tsx", old_path: "web/reports/PrintButton.tsx", status: "renamed", added: 2, removed: 1, binary: false, hunks: [{ header: "@@ -1,3 +1,4 @@ import", lines: [" import { Button } from \"../ui\";", "-export const PrintButton = () => <Button>Print</Button>;", "+export const ExportButton = () => <Button>Export</Button>;", "+export default ExportButton;"] }] },
		{ path: "web/public/export.png", status: "added", added: 0, removed: 0, binary: true, hunks: [] },
		{ path: "docs/old-reports.md", status: "deleted", added: 0, removed: 2, binary: false, hunks: [{ header: "@@ -1,2 +0,0 @@", lines: ["-# Old reports", "-Use the print button."] }] },
	];
	return ok("show", { base: "main", files });
}

const stepTexts = [
	"Open a report at `/reports/42` and press **Export**: the file downloads.",
	"Open the CSV: one row per report line, with a header row `date,account,amount`.",
	"Export a report with no lines: the file holds the header only.",
];

/** steps is tkt show --steps's answer, with the marks you left and the
 * head they hold for. */
export function steps(r: Row, marks: Mark[]): Envelope {
	if (!r.pr) return fail("show", "no_pull_request", `${r.ticket} has no pull request yet`, "the steps come from the pull request's review");
	return ok("show", {
		procedure: "Human verification",
		steps: stepTexts.map((text, i) => ({ n: i + 1, text })),
		verification: r.verification ?? null,
		pr: r.pr.number,
		source: r.review?.verdict === "CLEAR" ? "review" : "pull_request",
		head: r.pr.head ?? "",
		marks,
	});
}

/** stepCount is how many steps every ticket has. */
export const stepCount = stepTexts.length;

/** stepList is the steps as /check's checklist takes them. */
export function stepList(): { n: number; text: string }[] {
	return stepTexts.map((text, i) => ({ n: i + 1, text }));
}

/** newestReview is the review a row's verdict comes from, in the words the
 * review bot or review-pr would post. */
function newestReview(id: string, r: Row | undefined, at: string): HistoryReview {
	const head = headOf(id).slice(0, 7);
	switch (r?.review?.verdict) {
		case "FINDINGS":
			return { at, author: "claude", verdict: "FINDINGS", head, body: `## Review brief\n\nAdds what ${id} asks for, and tests for it.\n\n**Automated review: FINDINGS** · head \`${head}\` · reviewed against ${id}\n1. **BUG** \`api/limit.go:31\`: two requests in the same second share one counter, so a client gets twice its limit.\n2. **BUG** \`api/limit.go:58\`: a store error lets the request through without a limit.` };
		case "CHANGES_REQUESTED":
			return { at, author: "review-pr", verdict: "CHANGES_REQUESTED", head, body: `Reviewed head: ${headOf(id)}\nReviewed against: ${id}\n1. BUG \`search/index.go:112\`: the index never drops a deleted row (inline)\n2. QUESTION \`search/index.go:140\`: should a rebuild hold the write lock? (inline)\nResolved: none. Not reviewed: none.\nVerdict: CHANGES_REQUESTED` };
	}
	return { at, author: "review-pr", verdict: "CLEAR", head, body: `Verdict: CLEAR\nReviewed head: ${headOf(id)}` };
}

/** history is tkt history's answer for a ticket. */
export function history(id: string, r: Row | undefined, now: number): Envelope<HistoryResult> {
	const at = (min: number) => new Date(now - min * 60_000).toISOString();
	const n = r?.pr?.number ?? 40;
	const merged = r?.phase === "merged";
	return ok("history", {
		repo,
		forge: id === "ACM-111" ? "failed" : "ok",
		tickets: [
			{
				ticket: id,
				timeline: { ticket: id, repo, started: at(180), merged: merged ? at(20) : undefined, total_s: 160 * 60, in_progress: !merged, waiting_on_you_s: 42 * 60, rounds: 2, auto: !!r?.auto?.wanted, pr: n },
				events: [
					{ at: at(180), event: "phase", phase: "planning" },
					{ at: at(150), event: "approved" },
					{ at: at(140), event: "stage", stage: "work" },
					{ at: at(100), event: "stage", stage: "review" },
					{ at: at(90), event: "hold", action: "on" },
					{ at: at(85), event: "hold", action: "off" },
					{ at: at(70), event: "sync", action: "failed", note: "conflict in api/reports.go" },
					{ at: at(60), event: "watch", action: "run", stage: "address" },
					{ at: at(30), event: "verified", action: "evidence", pr: n },
					...(merged ? [{ at: at(20), event: "merged", pr: n }] : []),
				],
				pull_requests: id === "ACM-111" || !r?.pr
					? []
					: [
							{
								number: n,
								title: r.title ?? id,
								url: r.pr.url ?? "",
								state: r.pr.state,
								additions: r.diff?.added ?? 10,
								deletions: r.diff?.removed ?? 2,
								changed_files: r.diff?.files ?? 1,
								commits: [
									{ at: at(130), oid: "a1b2c3d", subject: `${id}: add the export endpoint` },
									{ at: at(65), oid: headOf(id).slice(0, 7), subject: `${id}: address the review` },
								],
								reviews: [
									{ at: at(95), author: "review-pr", verdict: "CHANGES_REQUESTED", head: "a1b2c3d", body: "Verdict: CHANGES_REQUESTED\n\n1. **api/reports.go:47**: a write error is dropped. Return it." },
									newestReview(id, r, at(40)),
								],
							},
						],
			},
		],
	});
}

/** stats is tkt stats's answer. */
export function stats(now: number): Envelope<StatsResult> {
	const at = (h: number) => new Date(now - h * 3600_000).toISOString();
	const t = (ticket: string, h: number, total: number, wait: number, rounds: number, a: boolean, inProgress = false) => ({ ticket, repo, started: at(h), merged: inProgress ? undefined : at(h - total / 3600), total_s: total, in_progress: inProgress, waiting_on_you_s: wait, rounds, auto: a, pr: 30 + rounds });
	return ok("stats", {
		repo,
		tickets: [t("ACM-110", 30, 6 * 3600, 3600, 2, true), t("ACM-121", 50, 3 * 3600, 0, 1, true), t("ACM-95", 90, 26 * 3600, 5 * 3600, 4, false), t("ACM-92", 120, 2 * 3600, 1200, 1, false), t("ACM-104", 3, 3 * 3600, 600, 1, true, true)],
		summary: { merged: 4, closed: 1, in_progress: 1, median_total_s: 4.5 * 3600, p90_total_s: 26 * 3600, median_waiting_on_you_s: 2400, p90_waiting_on_you_s: 5 * 3600, without_start: 2 },
	});
}

/** chores is tkt chores's answer. */
export function chores(now: number, watching: boolean, open: string[]): Envelope<ChoresResult> {
	const at = (h: number) => new Date(now + h * 3600_000).toISOString();
	return ok("chores", {
		repo,
		watch: { running: watching },
		chores: [
			{ name: "deps", schedule: "0 6 * * 1", as: "", last: at(-50), due: false, next: at(110), pr: true, runs: open, open: open[0] ?? "" },
			{ name: "flaky-tests", schedule: "30 2 * * *", as: "opencode", last: at(-30), due: true, next: at(-1), pr: true, runs: [], open: "" },
			{ name: "release", schedule: "0 9 1 * *", as: "", last: "", due: false, next: at(70), pr: false, runs: [], open: "" },
		],
	});
}

/** suggestion is the suggest tab as the last /suggest left it. */
export interface Suggestion {
	prompt: string;
	started: string;
	working: boolean;
}

/** suggest is tkt suggest --show's answer, or agent_missing before any
 * /suggest. */
export function suggest(s: Suggestion | undefined, now: number): Envelope<SuggestResult> | Envelope {
	if (!s) return fail("suggest", "agent_missing", "No suggest tab is open: /suggest opens one");
	return ok("suggest", {
		count: 0,
		prompt: s.prompt,
		tab: "suggest",
		pane: "w1:p900",
		preset: "claude",
		started: s.started,
		agent_status: s.working ? "working" : "idle",
		source: "transcript",
		message: s.working
			? ""
			: "Three tickets that can run side by side:\n\n| Ticket | Why now | Unblocks |\n|---|---|---|\n| ACM-130 | ACM-110 merged, so its blockers are done | ACM-133 |\n| ACM-131 | High priority, touches only `web/settings` | none |\n| ACM-135 | Small, and no ticket in flight touches `api/auth` | ACM-136, ACM-137 |\n\nLeft out: ACM-132 collides with ACM-104 in `api/audit.go`.",
		at: s.working ? "" : new Date(now - 5_000).toISOString(),
	});
}

/** version is the chat's version answer. */
export function version(update: string): Envelope {
	return ok("version", { tkt: "v0.9.0", pi: "1.3.0", bin: "/home/acme/.local/bin/tkt", update });
}
