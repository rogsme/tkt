import type { Row } from "../model.ts";

/** row builds a tkt ls row with sensible defaults. */
export function row(over: Partial<Row> = {}): Row {
	return {
		ticket: "ACM-12",
		path: "/w/ACM-12",
		agent: { pane_id: "w1:p10", name: "acm-12", harness: "claude", status: "idle" },
		last_status: "",
		pr: null,
		checks: null,
		review: null,
		next: { kind: "human", reason: "check_agent", argv: null, text: "check the agent in its tab" },
		stage: "start",
		launch: "submitted",
		auto: { wanted: false, approve_plan: false, running: false, rounds: 0, stop: "", waiting: "", paused: "", why: "" },
		phase: "needs_you",
		phase_since: "2026-01-01T00:00:00Z",
		started: "2026-01-01T00:00:00Z",
		idle_since: "",
		stale: false,
		...over,
	};
}
