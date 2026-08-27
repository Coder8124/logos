package mcpserver

// The tools this server exposes to an MCP host — the surface another
// application can build on to use brain as its memory layer.
//
// Two families. The memory tools (remember, recall, list, forget, memory_diff)
// answer "what do you know about X". The continuity tools (context, resume,
// note_progress, checkpoint, handoff) answer "where were we" — they are why an
// agent can be replaced by a different agent mid-project without the user
// re-explaining anything.
//
// Descriptions are written for the host's model, not for a human reading docs.
// They say *when* to reach for each tool, because a tool the model never thinks
// to call is a tool that does not exist.

var toolDefs = []map[string]any{
	{
		"name":        "remember",
		"description": "Save something durable about the user to their private local memory — a preference, a fact about a person, standing context, or a decision. Use this whenever the user states something worth remembering across future conversations (e.g. 'I prefer short replies', 'my CFO is Sarah', 'we launch in Q4'). Stored on the user's machine, never uploaded.",
		"inputSchema": obj(map[string]any{
			"text": str("the thing to remember, as a clear standalone statement"),
			"kind": enumStr("what kind of memory it is", "preference", "person", "context", "fact"),
		}, "text"),
	},
	{
		"name":        "recall",
		"description": "Retrieve what is known about the user relevant to a query, from their private local memory. Use this at the start of a task or whenever the user's preferences, people, or prior context would help — so you act on what they've told you before instead of asking again.",
		"inputSchema": obj(map[string]any{
			"query": str("what you want to recall about the user"),
			"limit": intSchema("how many memories to return (default 5)"),
		}, "query"),
	},
	{
		"name":        "list_memories",
		"description": "List everything currently in the user's memory, with ids. Use to review or before forgetting something.",
		"inputSchema": obj(map[string]any{}),
	},
	{
		"name":        "forget",
		"description": "Delete a memory by its id (from list_memories). Use when the user asks to forget something or a memory is wrong.",
		"inputSchema": obj(map[string]any{"id": str("the memory id to forget")}, "id"),
	},
	{
		"name":        "context",
		"description": "Assemble everything needed to do a task: where the last agent stopped, the project's goals and recent progress, the actual text of the relevant vault notes, related notes reached through the user's own links, what the user has told this memory, standing preferences, and open commitments — budgeted to fit a token ceiling and cited by source. Use this at the START of any task involving the user's own work, instead of recall. Prefer it over asking the user to re-explain: they have written this down already.",
		"inputSchema": obj(map[string]any{
			"task":    str("what you are about to do, in a sentence — this decides what gets retrieved"),
			"project": str("optional: narrow to one project, file path, or topic"),
			"budget":  intSchema("approximate token ceiling for the result (default 4000)"),
		}, "task"),
	},
	{
		"name":        "resume",
		"description": "Pick up a project where the last agent — possibly a different tool entirely — left off. Returns their last checkpoint (what they were doing, what they decided, what they already tried that did NOT work, what is still open, and the next step) followed by full project context. Use this when the user says 'continue', 'pick up where we left off', or names a project you have no history with. Read the 'already tried' section before proposing anything: it is there to stop you repeating work.",
		"inputSchema": obj(map[string]any{
			"project": str("the project to resume"),
			"agent":   str("optional: your name, e.g. 'claude' or 'cursor', recorded in the trail"),
			"budget":  intSchema("approximate token ceiling for the result (default 4000)"),
		}, "project"),
	},
	{
		"name": "before_you_try",
		// Written as an instruction rather than a description, because this is
		// the one tool the model has no reason to reach for on its own. Every
		// other tool answers a question the model already has; this one answers
		// a question it does not know to ask — whether the thing it is about to
		// suggest was ruled out before it existed.
		"description": "Check whether an approach has already been tried and failed, BEFORE you propose it. Call this whenever you are about to suggest a solution, a refactor, a vendor, a library, or a fix on work the user has a history with — especially if it seems obvious, because obvious approaches are the ones already attempted. Searches every dead end recorded across the user's whole vault, including work from other projects and from agents that no longer exist. If it returns anything, say so out loud before proposing: 'this was tried in March and the drop test failed'. A recorded failure is evidence, not a veto — if you still think it is right, say what is different now.",
		"inputSchema": obj(map[string]any{
			"approach": str("the approach you are about to propose, in a sentence"),
			"project":  str("optional: the project being worked on, so rulings from elsewhere can be flagged as possibly not transferring"),
		}, "approach"),
	},
	{
		"name":        "note_progress",
		"description": "Record one line of what you just did or learned while working. Cheap and meant to be called often — after a decision, a dead end, or a surprising discovery. These stay uncommitted until checkpoint folds them into a durable record, so use them freely rather than saving everything for the end.",
		"inputSchema": obj(map[string]any{
			"project": str("the project being worked on"),
			"text":    str("what happened, in one line"),
			"agent":   str("optional: your name, e.g. 'claude'"),
		}, "project", "text"),
	},
	{
		"name":        "checkpoint",
		"description": "Write down where you are stopping, as a permanent note in the user's vault. Call this BEFORE you finish a work session, when the user says they are wrapping up, or when context is running short. The 'failed' field matters most: approaches that did not work are the expensive knowledge, and without them the next agent will repeat them. Anything you omit is lost.",
		"inputSchema": obj(map[string]any{
			"project":   str("the project being worked on"),
			"task":      str("what you were trying to do"),
			"state":     str("where things actually stand now"),
			"decisions": arrStr("decisions made and why"),
			"failed":    arrStr("approaches tried that did NOT work, and why — the most valuable field here"),
			"questions": arrStr("questions still unresolved"),
			"files":     arrStr("files touched"),
			"next":      str("the single next step whoever picks this up should take"),
			"agent":     str("optional: your name, e.g. 'claude'"),
		}, "project"),
	},
	{
		"name":        "handoff",
		"description": "Checkpoint and explicitly hand the work to another agent or person. Same fields as checkpoint, plus who is taking over. Use when the user is switching tools ('finish this in Cursor') or delegating. The recipient calls resume(project) and continues without the user re-explaining anything.",
		"inputSchema": obj(map[string]any{
			"project":   str("the project being handed off"),
			"to":        str("who is taking over, e.g. 'cursor', 'codex', or a person's name"),
			"task":      str("what you were trying to do"),
			"state":     str("where things actually stand now"),
			"decisions": arrStr("decisions made and why"),
			"failed":    arrStr("approaches tried that did NOT work, and why"),
			"questions": arrStr("questions still unresolved"),
			"files":     arrStr("files touched"),
			"next":      str("the single next step the recipient should take"),
			"agent":     str("optional: your name, e.g. 'claude'"),
		}, "project", "to"),
	},
	{
		"name":        "memory_diff",
		"description": "Report what the user's memory has learned, dropped, or corroborated over a recent window, optionally about one subject (e.g. 'Sarah'). Use to answer 'what changed?' or to catch up on how the user's context has shifted. Instant and offline.",
		"inputSchema": obj(map[string]any{
			"subject": str("optional: narrow to changes mentioning this person, project, or topic"),
			"days":    intSchema("how many days back to look (default 7)"),
		}),
	},
	{
		"name":        "list_projects",
		"description": "Enumerate the projects brain has detected from the user's activity, most recently active first. Use to discover what the user is working on, or before calling context or resume for one.",
		"inputSchema": obj(map[string]any{}),
	},
}

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func enumStr(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": vals}
}

func arrStr(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       map[string]any{"type": "string"},
	}
}
