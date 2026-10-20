// tkt's extension for pi. tkt agent loads it, and only it, with -e.
// TKT_BIN names the tkt that started pi.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
	pi.registerCommand("tkt-version", {
		description: "Show the version of the tkt that started this agent",
		handler: async (_args, ctx) => {
			const bin = process.env.TKT_BIN || "tkt";
			const { stdout, stderr, code } = await pi.exec(bin, ["--version"]);
			if (code !== 0) {
				ctx.ui.notify(`${bin} --version failed: ${stderr.trim()}`, "error");
				return;
			}
			ctx.ui.notify(stdout.trim(), "info");
		},
	});
}
