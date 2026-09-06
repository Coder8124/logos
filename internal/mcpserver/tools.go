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

// relay is appended to every tool whose result opens with a receipt.
//
// The receipt exists so a person can see that their memory layer did something.
// It reaches them only if the model repeats it, because hosts render a tool
// result collapsed — Claude Code shows a grey one-liner, and the receipt sits
// inside the part nobody opens. A continuity layer nobody sees restore anything
// is one they stop believing in, and they stop long before they can say why.
//
// It lives in the tool description rather than only in the server instructions
// because descriptions are the one channel every host puts in front of its
// model. `instructions` is optional in the protocol and several hosts drop it,
// so a rule that lives only there is a rule that applies only in some editors.
const relay = " The first line is a receipt for the user — repeat it in one short line, right away. Hosts hide tool results, so skipping this means they never see it."

// Annotations tell the host what a tool does before it runs one, and they are
// the only thing that distinguishes reading from writing in this protocol.
//
// Without them a host has to assume the worst of every tool, and the editors
// that offer a read-only chat mode — Cursor's Ask, and every "plan before you
// act" mode that followed it — block the lot. That is precisely backwards for
// this server: the read tools are the ones a person most wants in a mode where
// nothing may be changed, because "where did we leave off" is a question you
// ask *before* you touch anything.
//
// openWorldHint is false throughout. Every one of these reads or writes one
// directory on the user's own disk; none of them reaches a network, and a host
// deciding how much to trust a call should know that.
func reads() map[string]any {
	return map[string]any{
		"readOnlyHint":    true,
		"idempotentHint":  true,
		"openWorldHint":   false,
		"destructiveHint": false,
	}
}

// writes describes a tool that changes the vault. destructive says whether it
// can remove or overwrite something that was already there, as opposed to
// adding to it; idempotent says whether calling it twice with the same
// arguments leaves the same state as calling it once.
func writes(destructive, idempotent bool) map[string]any {
	return map[string]any{
		"readOnlyHint":    false,
		"idempotentHint":  idempotent,
		"openWorldHint":   false,
		"destructiveHint": destructive,
	}
}

var toolDefs = []map[string]any{
	{
		"name":        "remember",
		"annotations": writes(false, false),
		"description": "Save something durable about the user: a preference, a fact about a person, standing context, or a decision. Use when the user states something worth remembering (e.g. 'I prefer short replies', 'my CFO is Sarah'). Scoped to the current project by default; set global for facts true everywhere. Local only, never uploaded. May queue for review instead of storing immediately — report what the response says, not that it's already remembered." + relay,
		"inputSchema": obj(map[string]any{
			"text":    str("the thing to remember, as a clear standalone statement"),
			"kind":    enumStr("what kind of memory it is", "preference", "person", "context", "fact"),
			"project": str("optional: override the project this belongs to; defaults to the folder you are working in"),
			"global":  boolSchema("set true for a fact that applies to every project, not just this one"),
		}, "text"),
	},
	{
		"name":        "recall",
		"annotations": reads(),
		"description": "Retrieve what's known about the user relevant to a query, from private local memory. Use at the start of a task, or whenever the user's preferences, people, or prior context would help. Searches the current project plus global facts; results from elsewhere are labelled. Set all_projects only when the user explicitly asks about other work.",
		"inputSchema": obj(map[string]any{
			"query":        str("what you want to recall about the user"),
			"limit":        intSchema("how many memories to return (default 5)"),
			"project":      str("optional: search a different project than the folder you are working in"),
			"all_projects": boolSchema("search every project, not just this one — only when the user asks for it"),
		}, "query"),
	},
	{
		"name":        "list_memories",
		"annotations": reads(),
		"description": "List everything currently in the user's memory, with ids. Use to review or before forgetting something.",
		"inputSchema": obj(map[string]any{}),
	},
	{
		"name":        "forget",
		"annotations": writes(true, true),
		"description": "Delete a memory by its id (from list_memories). Use when the user asks to forget something or a memory is wrong." + relay,
		"inputSchema": obj(map[string]any{"id": str("the memory id to forget")}, "id"),
	},
	{
		"name":        "pin_memory",
		"annotations": writes(false, true),
		"description": "Mark a memory (by id, from list_memories) as always-include — carried into every context pack for its project regardless of relevance score. Use for a standing instruction or a fact everything else depends on. Call again with unpin true to undo." + relay,
		"inputSchema": obj(map[string]any{
			"id":    str("the memory id to pin or unpin"),
			"unpin": boolSchema("set true to return this memory to normal ranking instead of pinning it"),
		}, "id"),
	},
	{
		"name":        "exclude_memory",
		"annotations": writes(false, true),
		"description": "Mark a memory by its id (from list_memories) as never-include: it stays on record but is dropped from recall and context packs entirely. Use when the user wants a memory kept but never surfaced again — softer than forget, which deletes it outright. Call pin_memory with unpin true to reverse." + relay,
		"inputSchema": obj(map[string]any{"id": str("the memory id to exclude")}, "id"),
	},
	{
		"name":        "context",
		"annotations": reads(),
		"description": "Assemble everything needed to start a task: the last agent's stopping point, project goals and progress, relevant vault notes and linked neighbors, what memory knows, standing preferences, and open commitments — budgeted to a token ceiling, cited by source. Call at the START of work on the user's own project, instead of recall — it's already written down." + relay,
		"inputSchema": obj(map[string]any{
			"task":    str("what you are about to do, in a sentence — this decides what gets retrieved"),
			"project": str("optional: narrow to one project, file path, or topic"),
			"budget":  intSchema("approximate token ceiling for the result (default 4000)"),
			"since":   enumStr("optional: how far back to look, overriding the inferred window", "day", "week", "month", "quarter", "year", "all"),
		}, "task"),
	},
	{
		"name":        "resume",
		"annotations": reads(),
		"description": "Pick up a project where the last agent — possibly a different tool — left off: their last checkpoint (what they were doing, decided, already tried and failed, what's open, next step), plus full project context. Use when the user says 'continue' or names a project you have no history with. Read what already failed before proposing anything." + relay,
		"inputSchema": obj(map[string]any{
			"project": str("the project to resume"),
			"agent":   str("optional: your name, e.g. 'claude' or 'cursor', recorded in the trail"),
			"budget":  intSchema("approximate token ceiling for the result (default 4000)"),
			"since":   enumStr("optional: how far back to look, overriding the inferred window", "day", "week", "month", "quarter", "year", "all"),
		}, "project"),
	},
	{
		"name":        "before_you_try",
		"annotations": reads(),
		// Written as an instruction rather than a description, because this is
		// the one tool the model has no reason to reach for on its own. Every
		// other tool answers a question the model already has; this one answers
		// a question it does not know to ask — whether the thing it is about to
		// suggest was ruled out before it existed.
		"description": "Check whether an approach was already tried and failed, BEFORE proposing it — a fix, a refactor, a vendor, a library, especially if it seems obvious, since obvious approaches are the ones already attempted. Searches every recorded dead end across the whole vault, other projects included. If it returns a hit, say so before proposing (e.g. 'this was tried in March, the drop test failed'). Not a veto — if you still think it's right, say what's different now.",
		"inputSchema": obj(map[string]any{
			"approach": str("the approach you are about to propose, in a sentence"),
			"project":  str("optional: the project being worked on, so rulings from elsewhere can be flagged as possibly not transferring"),
		}, "approach"),
	},
	{
		"name":        "why",
		"annotations": reads(),
		// The counterpart to before_you_try. That one fires on a proposal; this
		// one fires on a file — the other moment an agent is about to act on
		// something whose history it cannot see. `git blame` answers who and
		// when and structurally cannot answer why, so the reasoning is in a pull
		// request nobody kept or the head of someone who left.
		"description": "Find out why a file is the way it is, BEFORE changing something that looks wrong. Returns the decisions and ruled-out approaches recorded while it was last worked on, with who and when. Use when code looks odd or redundant, before reverting or simplifying it, or when the user asks 'why is this like this' — code that looks wrong is often load-bearing.",
		"inputSchema": obj(map[string]any{
			"file":  str("the file path you are about to change or are curious about"),
			"limit": intSchema("how many checkpoints to return (default 5)"),
		}, "file"),
	},
	{
		"name":        "note_progress",
		"annotations": writes(false, false),
		"description": "Record one line of what you just did or learned while working. Cheap and meant to be called often — after a decision, a dead end, or a surprising discovery. These stay uncommitted until checkpoint folds them into a durable record, so use them freely rather than saving everything for the end." + relay,
		"inputSchema": obj(map[string]any{
			"project": str("the project being worked on"),
			"text":    str("what happened, in one line"),
			"agent":   str("optional: your name, e.g. 'claude'"),
		}, "project", "text"),
	},
	{
		"name":        "checkpoint",
		"annotations": writes(false, false),
		"description": "Write down where you're stopping, as a permanent vault note. Call BEFORE ending a session, when the user is wrapping up, or context is running short. 'failed' matters most — that's the expensive knowledge; omit it and the next agent repeats your dead ends. Anything else you omit is simply lost." + relay,
		"inputSchema": obj(map[string]any{
			"project":   str("the project being worked on"),
			"task":      str("what you were trying to do"),
			"state":     str("where things actually stand now"),
			"decisions": arrStr("decisions made and why"),
			"failed":    arrStr("approaches tried that did NOT work, and why — the most valuable field here. Plain prose is fine; for a record before_you_try can act on precisely, one line as 'route: ... | observation: ... | layer: ... | scope: ... | degree: ... | action: ... | alternative: ...' — see the agent instructions for the vocabulary"),
			"verified":  arrStr("claims you actually demonstrated, with the command that showed it, e.g. 'auth rejects expired tokens — go test ./internal/auth -run TestExpiry'. Only what you ran — belief goes in 'state'"),
			"blockers":  arrStr("what is known broken or unfinished, and what it blocks"),
			"commands":  arrStr("the build, test and lint commands you actually ran"),
			"questions": arrStr("questions still unresolved"),
			"files":     arrStr("files touched"),
			"next":      str("the single next step for whoever picks this up"),
			"agent":     str("optional: your name, e.g. 'claude'"),
		}, "project"),
	},
	{
		"name":        "handoff",
		"annotations": writes(false, false),
		"description": "Checkpoint and explicitly hand the work to another agent or person. Same fields as checkpoint, plus who is taking over. Use when the user is switching tools ('finish this in Cursor') or delegating. The recipient calls resume(project) and continues without the user re-explaining anything." + relay,
		"inputSchema": obj(map[string]any{
			"project":   str("the project being handed off"),
			"to":        str("who is taking over, e.g. 'cursor', 'codex', or a person's name"),
			"task":      str("what you were trying to do"),
			"state":     str("where things actually stand now"),
			"decisions": arrStr("decisions made and why"),
			"failed":    arrStr("approaches tried that did NOT work, and why. Same optional 'route: ... | layer: ... | ...' shape as checkpoint's failed field"),
			"verified":  arrStr("claims you actually demonstrated — what the recipient can build on without re-checking"),
			"blockers":  arrStr("what is known broken or unfinished, and what it blocks"),
			"commands":  arrStr("the build, test and lint commands you actually ran"),
			"questions": arrStr("questions still unresolved"),
			"files":     arrStr("files touched"),
			"next":      str("the next step the recipient should take"),
			"agent":     str("optional: your name, e.g. 'claude'"),
		}, "project", "to"),
	},
	{
		"name":        "memory_diff",
		"annotations": reads(),
		"description": "Report what the user's memory has learned, dropped, or corroborated over a recent window, optionally about one subject (e.g. 'Sarah'). Use to answer 'what changed?' or to catch up on how the user's context has shifted. Instant and offline.",
		"inputSchema": obj(map[string]any{
			"subject": str("optional: narrow to changes mentioning this person, project, or topic"),
			"days":    intSchema("how many days back to look (default 7)"),
		}),
	},
	{
		"name":        "list_projects",
		"annotations": reads(),
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

func boolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
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
