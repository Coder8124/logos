package mcpserver

import "fmt"

// The tools this server exposes to an MCP host — the surface another
// application can build on to use logos as its memory layer.
//
// Two families. The memory tools (remember, recall, list, forget, memory_diff)
// answer "what do you know about X". The continuity tools (context, resume,
// note_progress, checkpoint, handoff) answer "where were we" — they are why an
// agent can be replaced by a different agent mid-project without the user
// re-explaining anything.
//
// Descriptions are written for the host's model, not for a human reading docs.
// They say *when* to reach for each tool, because a tool the model never thinks
// to call is a tool that does not exist — and they say it in one line, as a
// CLI's help does. Some hosts send every description on every request, and a
// paragraph per tool cost 4.7k tokens a turn to say what a line says.
// tools_terse_test.go holds them to that, and to the rules they must not lose.

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
const relay = " Repeat the result's first line to the user."

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
		"description": "Save a durable fact, preference, person or decision; project-scoped unless global. May queue for review — report what the result says." + relay,
		"inputSchema": obj(map[string]any{
			"text":    str("a clear standalone statement"),
			"kind":    enumStr("procedure needs text as `route: ... | trap: ...`", "preference", "person", "context", "fact", "procedure"),
			"project": str("optional: defaults to the current folder's project"),
			"global":  boolSchema("true if it applies to every project"),
		}, "text"),
	},
	{
		"name":        "recall",
		"annotations": reads(),
		"description": "Search the user's memory for a query: this project plus global facts.",
		"inputSchema": obj(map[string]any{
			"query":        str("what to recall"),
			"limit":        intSchema("default 5"),
			"project":      str("optional: another project to search"),
			"all_projects": boolSchema("search every project; only when the user asks"),
		}, "query"),
	},
	{
		"name":        "list_memories",
		"annotations": reads(),
		"description": "List every memory with its id.",
		"inputSchema": obj(map[string]any{}),
	},
	{
		"name":        "forget",
		"annotations": writes(true, true),
		"description": "Delete a memory by id." + relay,
		"inputSchema": obj(map[string]any{"id": str("memory id")}, "id"),
	},
	{
		"name":        "pin_memory",
		"annotations": writes(false, true),
		"description": "Always include a memory in this project's context packs; unpin true undoes it." + relay,
		"inputSchema": obj(map[string]any{
			"id":    str("memory id"),
			"unpin": boolSchema("true to unpin"),
		}, "id"),
	},
	{
		"name":        "exclude_memory",
		"annotations": writes(false, true),
		"description": "Keep a memory on record but never surface it; pin_memory with unpin true reverses it." + relay,
		"inputSchema": obj(map[string]any{"id": str("memory id")}, "id"),
	},
	{
		"name":        "context",
		"annotations": reads(),
		"description": "Everything to start a task: last checkpoint, goals, notes, memories, open loops. Call at the start of work." + relay,
		"inputSchema": obj(map[string]any{
			"task":    str("what you are about to do, in a sentence"),
			"project": str("optional: a project, file path or topic"),
			"budget":  intSchema("token ceiling (default 4000)"),
			"since":   enumStr("optional: how far back to look", "day", "week", "month", "quarter", "year", "all"),
		}, "task"),
	},
	{
		"name":        "resume",
		"annotations": reads(),
		"description": "Pick up where the last agent stopped. Read what already failed before proposing anything." + relay,
		"inputSchema": obj(map[string]any{
			"project": str("omit for the most recent"),
			"agent":   str("optional: your name"),
			"budget":  intSchema("token ceiling (default 4000)"),
			"since":   enumStr("optional: how far back to look", "day", "week", "month", "quarter", "year", "all"),
		}),
	},
	{
		"name":        "before_you_try",
		"annotations": reads(),
		// Written as an instruction rather than a description, because this is
		// the one tool the model has no reason to reach for on its own. Every
		// other tool answers a question the model already has; this one answers
		// a question it does not know to ask — whether the thing it is about to
		// suggest was ruled out before it existed.
		"description": "Call BEFORE proposing a fix, refactor or library: says whether it was tried and failed. If so, say that first.",
		"inputSchema": obj(map[string]any{
			"approach": str("the approach, in a sentence"),
			"project":  str("optional: the current project"),
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
		"description": "Call BEFORE changing code that looks wrong: the decisions and dead ends recorded for a file.",
		"inputSchema": obj(map[string]any{
			"file":  str("file path"),
			"limit": intSchema("default 5"),
		}, "file"),
	},
	{
		"name":        "note_progress",
		"annotations": writes(false, false),
		"description": "Record one line of what you just did or learned; checkpoint folds these in." + relay,
		"inputSchema": obj(map[string]any{
			"project": str("the project being worked on"),
			"text":    str("one line"),
			"agent":   str("optional: your name, e.g. 'claude'"),
		}, "project", "text"),
	},
	{
		"name":        "checkpoint",
		"annotations": writes(false, false),
		"description": "Save where you're stopping. Call BEFORE ending a session; 'failed' matters most." + relay,
		"inputSchema": obj(map[string]any{
			"project":   str("the project being worked on"),
			"task":      str("what you were trying to do"),
			"intent":    str("why the task matters; later checkpoints with the same task inherit it"),
			"state":     str("where things actually stand now"),
			"decisions": arrStr("each with its reason: 'X, because Y'"),
			"failed":    arrStr("what didn't work and why; optionally 'route: ... | observation: ... | layer: ...' (environment if toolchain-only)"),
			"verified":  arrStr("what you demonstrated, with the command that showed it; belief goes in state"),
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
		"description": "Checkpoint and hand the work to another agent or person." + relay,
		"inputSchema": obj(map[string]any{
			"project":   str("the project being handed off"),
			"to":        str("who takes over: an agent or a person"),
			"task":      str("what you were trying to do"),
			"intent":    str("why the task matters; later checkpoints with the same task inherit it"),
			"state":     str("where things actually stand now"),
			"decisions": arrStr("each with its reason: 'X, because Y'"),
			"failed":    arrStr("what didn't work and why, same shape as checkpoint's"),
			"verified":  arrStr("what you demonstrated, with the command that showed it"),
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
		"description": "What the user's memory learned or dropped recently, optionally about one subject.",
		"inputSchema": obj(map[string]any{
			"subject": str("optional: narrow to changes mentioning this person, project, or topic"),
			"days":    intSchema("how many days back to look (default 7)"),
		}),
	},
	{
		// The pair that lets the calling agent be the distiller (B3). Read-only
		// on the way out, candidate-only on the way back.
		"name":        "ingest_harvest",
		"annotations": reads(),
		"description": "List queued sessions, or fetch one session's commands, files and turns to distil." + relay,
		"inputSchema": obj(map[string]any{
			"session":   str("a session id prefix or auto record path; omit to list the queue"),
			"max_turns": intSchema("default 120; elided turns cannot be cited"),
		}),
	},
	{
		"name":        "ingest_distil",
		"annotations": writes(false, true),
		"description": "Return a distillation. Cite a turn on every verified and failed entry; verified needs a successful command, not a file edit." + relay,
		"inputSchema": obj(map[string]any{
			"session":  str("the session id or auto record path you were served"),
			"verified": arrStr("each citing its turn, e.g. 'suite passes (turn 14)'"),
			"failed":   arrStr("what was ruled out, each citing its turn"),
			"blockers": arrStr("optional: what stopped the session, each citing its turn"),
			"decided":  arrStr("auto records only: what the session decided and why, each citing its turn"),
			"next":     str("the first thing the next agent should do; a proposal, so it cites nothing"),
			"model":    str("optional: your model name"),
		}, "session"),
	},
	{
		"name":        "list_projects",
		"annotations": reads(),
		"description": "List the user's projects, most recently active first.",
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

// toolSets are the tool sets `logos mcp serve --tools` can serve. Claude Code
// loads tool definitions on demand, but a host that puts every definition into
// every model request pays for all of them on every turn: 3,926 tokens for the
// full set, measured (#61). continuity is what an agent reaches for mid-task,
// plus distillation: `logos ingest` only harvests and hands the judgement to an
// agent, so no terminal command can stand in for ingest_harvest/ingest_distil.
// The curation tools it drops (forget, pin, exclude, diff, listings) each have
// a `logos memory` or `logos projects` equivalent.
var toolSets = map[string][]string{
	"all":        nil,
	"continuity": {"remember", "recall", "context", "resume", "before_you_try", "why", "note_progress", "checkpoint", "handoff", "ingest_harvest", "ingest_distil"},
}

// SetTools chooses the tool set this server shows and answers. An unknown name
// is an error rather than a fallback to all, so a typo in a host config does
// not quietly serve something other than what the user wrote.
func (s *Server) SetTools(set string) error {
	names, ok := toolSets[set]
	if !ok {
		return fmt.Errorf("unknown tool set %q (want all or continuity)", set)
	}
	s.toolSet = nil
	if names != nil {
		s.toolSet = map[string]bool{}
		for _, n := range names {
			s.toolSet[n] = true
		}
	}
	return nil
}

func (s *Server) tools() []map[string]any {
	if s.toolSet == nil {
		return toolDefs
	}
	var out []map[string]any
	for _, def := range toolDefs {
		if name, _ := def["name"].(string); s.toolSet[name] {
			out = append(out, def)
		}
	}
	return out
}
