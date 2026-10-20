// The chat's own background. pi's themes color text and panels and leave
// every other cell to the terminal's default background, which a
// translucent terminal shows through. In fullscreen mode pi owns each cell
// of the alternate screen, so the painter keeps a background set there:
// it sets it again after each escape that clears it, and the terminal
// fills the cells pi erases with it. In regular mode the terminal owns the
// scrollback and keeps its own background.

/** Painter rewrites what pi writes to the terminal. */
export class Painter {
	/** background returns the escape that sets the chat's background, or
	 * undefined to leave the terminal's. */
	background: () => string | undefined = () => undefined;
	private alt = false;
	/** painting is the background the painter last set. */
	private painting: string | undefined;

	/** paint returns data with the background kept on while pi is on the
	 * alternate screen. pi writes a fullscreen frame in one piece, so no
	 * escape it rewrites is split between two writes. */
	paint(data: string): string {
		const bg = this.background();
		let out = this.turn(this.alt ? bg : undefined);
		let from = 0;
		for (const m of data.matchAll(escapes)) {
			const end = m.index + m[0].length;
			out += data.slice(from, end);
			from = end;
			if (m[0] === enterAlt || m[0] === exitAlt) {
				this.alt = m[0] === enterAlt;
				out += this.turn(this.alt ? bg : undefined);
			} else if (this.painting) out += this.painting;
		}
		return out + data.slice(from);
	}

	/** turn returns the escape that changes the painter's background to bg. */
	private turn(bg: string | undefined): string {
		if (bg === this.painting) return "";
		this.painting = bg;
		return bg ?? "\x1b[49m";
	}
}

const enterAlt = "\x1b[?1049h";
const exitAlt = "\x1b[?1049l";

/** escapes are the alternate screen's switches and the resets that clear
 * the background: pi ends styled text with \x1b[0m or \x1b[49m. */
const escapes = /\x1b\[(?:\?1049[hl]|(?:0|49)?m)/g;

const key = Symbol.for("tkt.painter");

/** painter returns the painter on pi's output, and puts it there the first
 * time. Each load of the extension, after /reload or a new session, gets
 * the same one, so it still knows whether pi is on the alternate screen. */
export function painter(): Painter {
	const g = globalThis as { [key]?: Painter };
	if (!g[key]) {
		const p = new Painter();
		const write = process.stdout.write.bind(process.stdout) as (chunk: unknown, ...rest: unknown[]) => boolean;
		process.stdout.write = ((chunk: unknown, ...rest: unknown[]) => write(typeof chunk === "string" ? p.paint(chunk) : chunk, ...rest)) as typeof process.stdout.write;
		g[key] = p;
	}
	return g[key];
}
