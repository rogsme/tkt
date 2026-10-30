// What only the chat starts, as the playground lets you start it: each
// kind of question, each level of notice, the board's own changes, the
// model answering, and the link to the chat dropping. /play NAME runs one,
// /play alone asks which, and the toolbar has a button for each. It
// imports nothing, so the toolbar loads it in the browser.

export interface Trigger {
	name: string;
	label: string;
	group: "Questions" | "Notices" | "Board" | "Chat" | "Link";
}

export const triggers: Trigger[] = [
	{ name: "confirm", label: "Confirm", group: "Questions" },
	{ name: "input", label: "Input", group: "Questions" },
	{ name: "editor", label: "Editor", group: "Questions" },
	{ name: "ask", label: "Ask, with Type something", group: "Questions" },
	{ name: "checklist", label: "Checklist", group: "Questions" },
	{ name: "info", label: "Info notice, long", group: "Notices" },
	{ name: "warning", label: "Warning notice", group: "Notices" },
	{ name: "error", label: "Error notice", group: "Notices" },
	{ name: "item", label: "A ticket needs you", group: "Board" },
	{ name: "job", label: "A job runs", group: "Board" },
	{ name: "failed", label: "A job fails", group: "Board" },
	{ name: "incident", label: "Incident on or off", group: "Board" },
	{ name: "watch", label: "Watch on, off, failing", group: "Board" },
	{ name: "update", label: "Update on or off", group: "Board" },
	{ name: "answer", label: "The model answers", group: "Chat" },
	{ name: "markdown", label: "A long Markdown answer", group: "Chat" },
	{ name: "busy", label: "Model busy or ready", group: "Chat" },
	{ name: "drop", label: "Chat closes for 5 s", group: "Link" },
	{ name: "deny", label: "Token refused for 10 s", group: "Link" },
	{ name: "reset", label: "Reset the tickets", group: "Link" },
];
