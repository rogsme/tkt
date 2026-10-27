// Markdown for the model's answers. What the model writes can quote a pull
// request or a web page, so raw HTML shows as text, links go only to web
// and mail addresses, and images show as links: nothing in an answer runs
// in the page or loads from elsewhere.

import { Marked } from "./vendor/marked.js";

const marked = new Marked({ gfm: true, breaks: false, async: false });

// A human verification step keeps its lines as written: "Expected:" goes on
// a line of its own.
const stepMarked = new Marked({ gfm: true, breaks: true, async: false });

const safety = {
	renderer: {
		html({ text }: { text: string }): string {
			return escape(text);
		},
		link(this: { parser: { parseInline(t: unknown[]): string } }, { href, title, tokens }: { href: string; title?: string | null; tokens: unknown[] }): string {
			const text = this.parser.parseInline(tokens);
			if (!safe(href)) return text;
			const t = title ? ` title="${escape(title)}"` : "";
			return `<a href="${escape(href)}"${t} target="_blank" rel="noopener noreferrer">${text}</a>`;
		},
		image({ href, text }: { href: string; text: string }): string {
			const label = escape(text || href);
			return safe(href) ? `<a href="${escape(href)}" target="_blank" rel="noopener noreferrer">${label}</a>` : label;
		},
	},
};
marked.use(safety);
stepMarked.use(safety);

/** md renders markdown as HTML that is safe to put in the page. */
export function md(text: string): string {
	return marked.parse(text) as string;
}

/** mdStep renders a human verification step's text, keeping its lines. */
export function mdStep(text: string): string {
	return stepMarked.parse(text) as string;
}

function safe(href: string): boolean {
	return /^(https?:|mailto:)/i.test(href.trim());
}

function escape(s: string): string {
	return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}
