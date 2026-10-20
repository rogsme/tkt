// tkt's extension for pi. tkt agent loads it, and only it, with -e; TKT_BIN
// names the tkt that started pi. The extension gives the model tools that
// run tkt and nothing else, slash commands for the same, a board above the
// prompt, and the watchers that tell you when a ticket needs you.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { truncateToWidth } from "@earendil-works/pi-tui";
import { restoreMarks } from "./actions.ts";
import { widgetLines } from "./board.ts";
import { registerCommands } from "./commands.ts";
import { Hub } from "./hub.ts";
import { registerTools } from "./tools.ts";
import { paint } from "./ui.ts";

export default function (pi: ExtensionAPI) {
	const hub = new Hub(pi);
	registerTools(pi, hub);
	registerCommands(pi, hub);

	pi.on("input", (event) => {
		if (event.source !== "extension") hub.lastInput = event.text;
		return { action: "continue" };
	});

	pi.on("session_start", (_event, ctx) => {
		hub.stop();
		restoreMarks(ctx.sessionManager.getEntries() as { type: string; customType?: string; data?: unknown }[]);
		if (ctx.hasUI) {
			ctx.ui.setTitle(`tkt ${hub.repo}`);
			ctx.ui.setWidget(
				"tkt-board",
				(tui, theme) => {
					hub.render = () => tui.requestRender();
					return {
						render(width: number): string[] {
							const s = { rows: hub.rows, jobs: hub.labels, waiting: 0, now: Date.now(), tick: hub.tick, repo: hub.repo };
							return widgetLines(s, width, hub.needsYou()).map((l) => truncateToWidth(paint(theme, l.tone, l.text), width));
						},
						invalidate() {},
						dispose() {
							hub.render = () => {};
						},
					};
				},
				{ placement: "aboveEditor" },
			);
		}
		hub.start(ctx);
	});

	pi.on("session_shutdown", () => hub.stop());
}
