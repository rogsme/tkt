// tkt's extension for pi. tkt agent loads it, and only it, with -e; TKT_BIN
// names the tkt that started pi. The extension gives the model tools that
// run tkt and nothing else, slash commands for the same, a board pinned to
// the top of the chat, a dark background under a dark theme, and the
// watchers that tell you when a ticket needs you.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { backgroundAnsi, isViewportTUI, parseColor } from "@earendil-works/pi-tui";
import { restoreMarks } from "./actions.ts";
import { registerCommands } from "./commands.ts";
import { Hub } from "./hub.ts";
import { painter } from "./paint.ts";
import { registerTools } from "./tools.ts";

/** page is the chat's background under a dark theme: the page tkt-dark
 * gives an exported chat. */
const page = parseColor("#0a0a0a");

export default function (pi: ExtensionAPI) {
	const hub = new Hub(pi);
	const paint = painter();
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
			paint.background = () => {
				const theme = ctx.ui.theme;
				return theme.appearance === "dark" ? backgroundAnsi(page, theme.getColorMode()) : undefined;
			};
			ctx.ui.setTitle(`tkt ${hub.repo}`);
			ctx.ui.setWidget("tkt-board", (tui, theme) => {
				// pi drew the first frames before the background was known,
				// and redraws only the rows that change. A forced render in
				// regular mode would clear the terminal's scrollback.
				if (isViewportTUI(tui)) tui.requestRender(true);
				return hub.pinBoard(tui, theme);
			});
		}
		hub.start(ctx);
	});

	pi.on("session_shutdown", (event) => {
		// The session's ctx goes stale once it ends.
		paint.background = () => undefined;
		hub.stop();
		if (event.reason === "quit") nameTktInResumeLine();
	});
}

/** nameTktInResumeLine makes the line pi prints as it quits, "To resume
 * this session: pi --session ID", name tkt agent: a bare pi would open the
 * chat without tkt's extension. pi writes the line after the extension
 * shuts down, so the extension rewrites it on its way out. */
function nameTktInResumeLine(): void {
	const write = process.stdout.write.bind(process.stdout) as (chunk: unknown, ...rest: unknown[]) => boolean;
	process.stdout.write = ((chunk: unknown, ...rest: unknown[]) =>
		write(typeof chunk === "string" ? chunk.replace(/\bpi (--session-dir \S+ )?--session (\S+)/, (_, dir: string | undefined, id: string) => `tkt agent --session ${id}${dir ? ` -- ${dir.trim()}` : ""}`) : chunk, ...rest)) as typeof process.stdout.write;
}
