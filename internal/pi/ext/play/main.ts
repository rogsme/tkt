// The playground: the web page, served by the chat's own server code, with
// made-up tickets and a fake chat behind it, so you can press every button
// without a chat or a repository. Run it with just play; it needs Bun, as
// the chat's server does. It reads the page's files on every request, so a
// reload shows what you changed.

import { randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import { networkInterfaces } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { networkURLs } from "../gate.ts";
import { type BunServer, type Site, servePage } from "../serve.ts";
import { commandsOf, Fake } from "./fake.ts";
import { repo } from "./scenario.ts";

const here = dirname(fileURLToPath(import.meta.url));

const usage = `usage: just play [--port N] [--token TOKEN] [--network]

Serves the tkt agent's web page with made-up tickets on 127.0.0.1.
  --port N       the port, 7357 by default; 0 picks a free one
  --token TOKEN  the token the address carries, play by default
  --network      serve on every address, so a phone on your network reaches it
In the page, /play lists what only a chat starts, such as each question.`;

function flags(argv: string[]): { port: number; token: string; network: boolean } {
	const o = { port: 7357, token: "play", network: false };
	for (let i = 0; i < argv.length; i++) {
		const a = argv[i];
		if (a === "--port" && /^\d+$/.test(argv[i + 1] ?? "")) o.port = Number(argv[++i]);
		else if (a === "--token" && argv[i + 1]) o.token = argv[++i];
		else if (a === "--network") o.network = true;
		else {
			console.error(a === "-h" || a === "--help" ? usage : `tkt play: unknown flag ${a}\n\n${usage}`);
			process.exit(a === "-h" || a === "--help" ? 0 : 2);
		}
	}
	return o;
}

const o = flags(process.argv.slice(2));
let server: BunServer | undefined;
let port = o.port;
const url = () => `http://127.0.0.1:${port}/?token=${encodeURIComponent(o.token)}`;

const fake = new Fake({
	commands: commandsOf(readFileSync(join(here, "..", "commands.ts"), "utf8")),
	link: {
		get url() {
			return url();
		},
		// The question that asked for it gets its answer out first.
		drop: (ms) =>
			setTimeout(() => {
				server?.stop(true);
				setTimeout(() => (server = servePage(port, site)), ms);
			}, 100),
		deny: (ms) =>
			setTimeout(() => {
				const token = site.token;
				site.token = randomBytes(24).toString("hex");
				server?.stop(true);
				server = servePage(port, site);
				setTimeout(() => (site.token = token), ms);
			}, 100),
	},
});

const site: Site = {
	mirror: fake.mirror,
	token: o.token,
	hostname: o.network ? "0.0.0.0" : "127.0.0.1",
	health: () => ({ playground: true, repo }),
	fresh: true,
	mounts: { "/play/": here },
	head: `<link rel="stylesheet" href="/play/toolbar.css">\n<script type="module" src="/play/toolbar.ts"></script>`,
};

try {
	server = servePage(port, site);
} catch (err) {
	console.error(`tkt play: cannot serve on port ${port}: ${(err as Error).message}. --port picks another.`);
	process.exit(1);
}
port = server.port;
console.log(`The web page's playground: ${url()}`);
if (o.network) for (const u of networkURLs(url(), networkInterfaces())) console.log(`On your network: ${u}`);
