// A ticket's diff, as tkt show --diff --json lists it, with each line's
// numbers in the old and the new file. It imports nothing, so node --test
// covers it.

/** File is one file the branch changes, as tkt prints it. */
export interface File {
	path: string;
	old_path?: string;
	status: "added" | "deleted" | "renamed" | "modified";
	added: number;
	removed: number;
	binary: boolean;
	hunks: Hunk[];
}

export interface Hunk {
	header: string;
	lines: string[];
}

/** Line is one line of a hunk: added, removed, kept as context, or git's
 * note that a file has no newline at its end. */
export interface Line {
	kind: "add" | "del" | "ctx" | "note";
	old?: number;
	new?: number;
	text: string;
}

/** lines numbers a hunk's lines from its @@ header. */
export function lines(h: Hunk): Line[] {
	const m = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(h.header);
	let o = m ? Number(m[1]) : 0;
	let n = m ? Number(m[2]) : 0;
	return h.lines.map((l): Line => {
		const text = l.slice(1);
		switch (l[0]) {
			case "+":
				return { kind: "add", new: n++, text };
			case "-":
				return { kind: "del", old: o++, text };
			case "\\":
				return { kind: "note", text: l.slice(2) };
		}
		return { kind: "ctx", old: o++, new: n++, text };
	});
}

/** section is what git names after a hunk's @@ line, such as the function
 * it falls in. */
export function section(h: Hunk): string {
	return h.header.replace(/^@@[^@]*@@ ?/, "");
}

/** total sums what the files add and remove. */
export function total(files: File[]): { added: number; removed: number } {
	return files.reduce((t, f) => ({ added: t.added + f.added, removed: t.removed + f.removed }), { added: 0, removed: 0 });
}
