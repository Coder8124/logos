package activity

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Turning a host's hook payload into an Event.
//
// The parsing here is deliberately forgiving. These payloads come from a host
// we do not ship and cannot version-lock, and the cost of being strict is that
// a field rename upstream turns the whole record off — silently, because a hook
// that exits non-zero is a hook nobody notices. So every field is optional,
// every unknown shape still produces a row, and anything not understood is kept
// verbatim in Extra rather than dropped.
//
// The one thing never done here: guessing. A summary is built from fields the
// payload actually contains, and when there is nothing to say the row says the
// kind and no more.

// hookEvent maps a host's event name onto our kind. Names we do not know still
// record, under their own name lowercased, because an unrecognised event is
// still evidence that something happened.
func hookKind(event string) string {
	switch event {
	case "UserPromptSubmit":
		return KindPrompt
	case "PostToolUse", "PreToolUse":
		return KindTool
	case "Stop", "SubagentStop":
		return KindTurnEnd
	case "Notification":
		return KindNotice
	case "SessionStart":
		return KindStart
	case "SessionEnd":
		return KindEnd
	case "PermissionRequest":
		return KindBlocked
	case "":
		return ""
	default:
		return strings.ToLower(event)
	}
}

// FromHook builds an Event from a host hook payload. event is the host's event
// name; raw is the JSON it wrote to the hook's stdin. project is used when the
// payload does not carry enough to work one out.
func FromHook(event string, raw []byte, project string) (Event, error) {
	kind := hookKind(event)
	if kind == "" {
		return Event{}, fmt.Errorf("activity: no hook event name given")
	}
	var p map[string]any
	// An unparseable payload is still a real event: the tool did run. Recording
	// it with no detail beats recording nothing, so this is not fatal.
	_ = json.Unmarshal(raw, &p)

	e := Event{
		Kind:    kind,
		Agent:   "claude-code",
		Session: shortID(str(p, "session_id")),
		Project: project,
	}
	if e.Project == "" {
		if cwd := str(p, "cwd"); cwd != "" {
			e.Project = filepath.Base(filepath.Clean(cwd))
		}
	}
	e.Tool = str(p, "tool_name")
	e.Summary = summarize(kind, e.Tool, p)

	// What we did not model. Keeping the input but not the response: a tool
	// result can be megabytes, and a log that grows with the size of everything
	// an agent has ever read is a log that gets deleted.
	if in, ok := p["tool_input"].(map[string]any); ok && len(in) > 0 {
		e.Extra = map[string]any{"tool_input": in}
	}
	return e, nil
}

// summarize writes the one line a person reads without expanding the row.
//
// It is per-tool because the useful half of a tool call is in a different field
// each time — a path for an edit, a command for a shell, a pattern for a search
// — and "Edit {...}" tells a reader nothing they could not have guessed.
func summarize(kind, tool string, p map[string]any) string {
	switch kind {
	case KindPrompt:
		return oneLine(str(p, "prompt"), 160)
	case KindNotice:
		return oneLine(str(p, "message"), 160)
	case KindStart:
		if s := str(p, "source"); s != "" {
			return "session started (" + s + ")"
		}
		return "session started"
	case KindEnd:
		if r := str(p, "reason"); r != "" {
			return "session ended (" + r + ")"
		}
		return "session ended"
	case KindTurnEnd:
		return "turn ended"
	case KindTool:
		in, _ := p["tool_input"].(map[string]any)
		return oneLine(toolDetail(tool, in), 160)
	}
	return ""
}

func toolDetail(tool string, in map[string]any) string {
	if in == nil {
		return tool
	}
	// The field that carries the meaning, per tool. Ordered so the most
	// specific match wins: Bash has a command, file tools have a path, search
	// tools have a pattern.
	for _, key := range []string{"command", "file_path", "path", "pattern", "query", "url", "notebook_path"} {
		if v := str(in, key); v != "" {
			if key == "command" {
				return tool + ": " + v
			}
			return tool + " " + v
		}
	}
	// A tool we have no special knowledge of. Naming it is honest and enough;
	// the full input is in Extra for anyone who wants it.
	return tool
}

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// oneLine flattens and caps. Newlines matter here beyond tidiness: the log is
// JSONL, and a summary that kept its newlines would still be *encoded* safely
// but would print across rows and wreck every terminal view of the file.
func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if max > 0 && len(s) > max {
		// Cut on a rune boundary, not a byte one.
		r := []rune(s)
		if len(r) > max {
			return strings.TrimSpace(string(r[:max-1])) + "…"
		}
	}
	return s
}

// shortID trims a session UUID to something a person can compare at a glance.
// Atlas prints "session 8c4f" for the same reason: the full UUID is noise in
// every row and identifying in none of them.
func shortID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
