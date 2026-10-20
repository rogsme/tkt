// tkt's extension for pi. tkt agent loads it, and only it, with -e; TKT_BIN
// names the tkt that started pi. The extension gives the model tools that
// run tkt and nothing else, slash commands for the same, a board pinned to
// the top of the chat, and the watchers that tell you when a ticket needs
// you.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { restoreMarks } from "./actions.ts";
import { registerCommands } from "./commands.ts";
import { Hub } from "./hub.ts";
import { registerTools } from "./tools.ts";

export default function (pi: ExtensionAPI) {
	const hub = new Hub(pi);
	registerTools(pi, hub);
	registerCommands(pi, hub);

	pi.on("input", (event) => {
		if (event.source !== "extension") hub.lastInput = event.text;
		return { action: "continue" };
	});

	// What needs you goes to the model with your message, as it stands now.
	pi.on("before_agent_start", (event) => {
		if (event.messageType !== "user") return;
		const note = hub.boardNote();
		if (note) return { message: { customType: "tkt", content: note, display: false } };
	});

	pi.on("session_start", (_event, ctx) => {
		hub.stop();
		restoreMarks(ctx.sessionManager.getEntries() as { type: string; customType?: string; data?: unknown }[]);
		if (ctx.hasUI) {
			ctx.ui.setTitle(`tkt ${hub.repo}`);
			ctx.ui.setWidget("tkt-board", (tui, theme) => hub.pinBoard(tui, theme));
		}
		hub.start(ctx);
	});

	pi.on("session_shutdown", () => hub.stop());
}
